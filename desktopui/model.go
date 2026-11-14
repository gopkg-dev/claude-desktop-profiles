package desktopui

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"time"

	"github.com/nsgo-dev/nsgo/core"

	"claudeprofiles/desktopusage"
	"claudeprofiles/profiles"
)

type previewStore interface {
	PreviewSnapshot() (profiles.Snapshot, error)
}

type remoteUsageStore interface {
	RefreshRemoteUsage(context.Context, string) error
}

type sharingResumer interface {
	ResumeSessionSharing() (bool, error)
}

type action struct {
	kind    string
	profile profiles.Profile
}

type app struct {
	page            *core.Signal[string]
	settings        *core.Signal[profiles.Settings]
	settingsMessage *core.Signal[string]
	exitInfo        *core.Signal[*desktopusage.ExitInfo]
	exitBusy        *core.Signal[bool]
	exitMessage     *core.Signal[string]
	cx              *core.Scope
	ctx             context.Context
	store           Store
	launch          func(context.Context) error
	snapshot        *core.Signal[*profiles.Snapshot]
	busy            *core.Signal[bool]
	blocked         *core.Signal[bool]
	status          *core.Signal[string]
	failure         *core.Signal[string]
	pending         *core.Signal[*action]
	progress        *core.Signal[refreshProgress]
	toast           *core.Signal[string]
	transition      *core.Signal[*transition]
	toastSeq        uint64
	// addReturn is the account to go back to while a newly added one waits for its login.
	addReturn *core.Signal[string]
	// addDuplicate is set while the waiting account signed in to an account already saved.
	addDuplicate        *core.Signal[bool]
	sessions            *core.Signal[*sessionPage]
	sessionQuery        *core.Signal[string]
	sessionFilter       *core.Signal[string]
	sessionReasons      *core.Signal[bool]
	requestLog          *core.Signal[requestLogView]
	sequence            uint64
	usageRefreshing     bool
	fullRefreshing      bool
	remoteRefreshing    bool
	remoteRefreshCancel context.CancelFunc
	onlineFailures      map[string]onlineRefreshFailure
	onlineFailureNotice string
	lastFullRefresh     time.Time
	lastIdentityRefresh time.Time
	lastUsageDisplay    time.Time
}

const (
	quietFullInterval     = time.Minute
	identityRetryInterval = 30 * time.Second
)

func newApp(cx *core.Scope, store Store, launch func(context.Context) error) *app {
	a := &app{
		cx: cx, ctx: cx.Context(), store: store, launch: launch,
		snapshot: core.NewSignal[*profiles.Snapshot](cx, nil),
		busy:     core.NewSignal(cx, false), blocked: core.NewSignal(cx, false),
		status: core.NewSignal(cx, "点击读取账号，查看本机状态"), failure: core.NewSignal(cx, ""),
		pending: core.NewSignal[*action](cx, nil), lastUsageDisplay: time.Now(),
		progress: core.NewSignalFunc(cx, refreshProgress{}, func(a, b refreshProgress) bool { return reflect.DeepEqual(a, b) }), toast: core.NewSignal(cx, ""),
		addReturn: core.NewSignal(cx, ""), addDuplicate: core.NewSignal(cx, false),
		sessions: core.NewSignal[*sessionPage](cx, nil), transition: core.NewSignal[*transition](cx, nil),
	}
	a.initNavigation()
	a.watchAddedAccount()
	a.startUsageTimer()
	a.refresh()
	return a
}

func (a *app) run(message string, fn func(context.Context) (string, error)) {
	if a.busy.Get() {
		return
	}
	a.sequence++
	a.busy.Set(true)
	a.status.Set(message)
	a.failure.Set("")
	go func() {
		ctx, cancel := context.WithTimeout(a.ctx, 45*time.Second)
		defer cancel()
		message, actionErr := fn(ctx)
		snapshot, snapshotErr := a.store.Snapshot()
		a.cx.Post(func() {
			a.busy.Set(false)
			a.lastFullRefresh = time.Now()
			a.blocked.Set(snapshotErr != nil)
			if snapshotErr == nil || snapshot.PendingRecovery {
				a.reconcileOnlineFailures(&snapshot)
				a.snapshot.Set(&snapshot)
				a.lastUsageDisplay = time.Now()
			}
			a.finishTransition(actionErr)
			if err := errors.Join(actionErr, snapshotErr); err != nil {
				a.failure.Set(err.Error())
				a.status.Set("操作未完成")
				return
			}
			a.status.Set(message)
		})
	}()
}

func (a *app) refresh() {
	current := a.snapshot.Get()
	showPreview := current == nil || current.Preview
	seq := a.sequence + 1
	a.run("正在读取账号状态…", func(context.Context) (string, error) {
		if store, ok := a.store.(previewStore); ok && showPreview {
			preview, err := store.PreviewSnapshot()
			if err == nil {
				preview.Preview = true
				a.cx.Post(func() {
					if seq != a.sequence || !a.busy.Get() {
						return
					}
					a.snapshot.Set(&preview)
					a.status.Set("账号已显示，正在读取在线数据缓存…")
				})
			}
		}
		return "账号状态已更新", nil
	})
}

func (a *app) prepare(kind string, p profiles.Profile) {
	snapshot := a.snapshot.Get()
	if a.busy.Get() || (snapshot != nil && snapshot.Preview) {
		return
	}
	a.pending.Set(&action{kind: kind, profile: p})
}

func (a *app) submit() {
	item := a.pending.Get()
	if item == nil || a.busy.Get() {
		return
	}
	a.pending.Set(nil)
	if restarts(item.kind) {
		a.beginTransition(item)
	}
	returnID := ""
	if snapshot := a.snapshot.Get(); snapshot != nil {
		returnID = snapshot.ActiveID
	}
	a.run(busyMessage(item.kind), func(ctx context.Context) (string, error) {
		if restarts(item.kind) {
			ctx = profiles.WithProgress(ctx, a.reportStep)
		}
		switch item.kind {
		case "adopt":
			return "当前账号已接管", a.store.Adopt("当前账号")
		case "add":
			label := "新账号 " + time.Now().Format("2006-01-02 15:04:05.000")
			err := a.store.Add(ctx, label)
			if err == nil {
				a.cx.Post(func() { a.addReturn.Set(returnID) })
			}
			return "请在 Claude Desktop 中完成登录", err
		case "switch":
			return "账号已切换", a.store.Switch(ctx, item.profile.ID)
		case "archive":
			return "账号已归档", a.store.Archive(item.profile.ID)
		case "recover":
			return "账号状态已恢复", a.store.Recover(ctx)
		case "clear-log":
			message, err := a.clearRequestLog()
			if err == nil && message != "" {
				a.cx.Post(func() { a.showToast(message) })
			}
			return message, err
		case "clear-cache":
			message, err := a.clearOnlineCache()
			if err == nil && message != "" {
				a.cx.Post(func() { a.showToast("显示缓存已清除") })
			}
			return message, err
		case "sync":
			report, err := a.store.SyncSessions(ctx)
			if err == nil {
				a.cx.Post(func() {
					a.showToast(fmt.Sprintf("会话已同步 · 新增 %d · 更新 %d", report.CodeCopied, report.CodeUpdated))
					a.sessions.Set(nil)
				})
			}
			return fmt.Sprintf("会话共享已开启 · 同步 %d 项，跳过 %d 项",
				report.CodeCopied+report.CodeUpdated, report.Skipped), err
		default:
			return "", errors.New("无法识别的操作")
		}
	})
}

func (a *app) pauseSharing() {
	if a.controlsDisabled() {
		return
	}
	a.run("正在暂停会话共享…", func(context.Context) (string, error) {
		return "已暂停 Code 会话共享", a.store.DisableSessionSharing()
	})
}

// enableSharing turns session sharing back on. When it was only paused on the account still in use, it
// resumes at once; otherwise the other accounts' sessions must be merged, which restarts Desktop, so it asks.
func (a *app) enableSharing() {
	if a.controlsDisabled() {
		return
	}
	store, ok := a.store.(sharingResumer)
	if !ok {
		a.prepare("sync", profiles.Profile{})
		return
	}
	a.sequence++
	a.busy.Set(true)
	a.failure.Set("")
	go func() {
		resumed, err := store.ResumeSessionSharing()
		a.cx.Post(func() {
			a.busy.Set(false)
			switch {
			case err != nil:
				a.failure.Set(err.Error())
				a.status.Set("操作未完成")
			case !resumed:
				a.prepare("sync", profiles.Profile{})
			default:
				if current := a.snapshot.Get(); current != nil {
					updated := *current
					updated.SharedSessions = true
					a.snapshot.Set(&updated)
				}
				a.status.Set("已恢复 Code 会话共享")
				a.showToast("会话共享已恢复")
			}
		})
	}()
}

func (a *app) openDesktop() {
	if a.controlsDisabled() {
		return
	}
	a.run("正在打开 Claude Desktop…", func(ctx context.Context) (string, error) {
		return "已请求打开 Claude Desktop", a.launch(ctx)
	})
}

func (a *app) startUsageTimer() {
	ctx := a.ctx
	go func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				a.cx.Post(a.refreshUsage)
			}
		}
	}()
}

func (a *app) openProfileDirectory(id string) {
	if a.controlsDisabled() {
		return
	}
	a.sequence++
	a.busy.Set(true)
	a.failure.Set("")
	a.status.Set("正在打开账号文件夹…")
	finish := func(opened bool, err error) {
		a.busy.Set(false)
		if err != nil || !opened {
			message := "系统未能打开账号文件夹"
			if err != nil {
				message = "无法打开账号文件夹：" + err.Error()
			}
			a.failure.Set(message)
			a.status.Set("打开账号文件夹失败")
			return
		}
		a.status.Set("已打开账号文件夹")
	}
	go func() {
		dir, err := a.store.ProfileDirectory(id)
		a.cx.Post(func() {
			if err != nil {
				finish(false, err)
				return
			}
			a.cx.OpenFile(core.SharedFile{Path: dir}, finish)
		})
	}()
}

func (a *app) refreshUsage() {
	current := a.snapshot.Get()
	if current == nil || current.Preview || a.busy.Get() || a.pending.Get() != nil || current.PendingRecovery ||
		a.usageRefreshing || a.fullRefreshing {
		return
	}
	seq := a.sequence
	a.usageRefreshing = true
	go func() {
		usage, err := a.store.UsageSnapshot()
		a.cx.Post(func() {
			a.usageRefreshing = false
			current := a.snapshot.Get()
			if current == nil || a.busy.Get() || a.pending.Get() != nil || current.PendingRecovery || seq != a.sequence {
				return
			}
			if usage.NeedsRefresh || usage.ActiveID != current.ActiveID {
				a.quietFullRefresh(seq, true)
				return
			}
			if err != nil {
				return
			}
			updated := *current
			updated.Profiles = append([]profiles.Profile(nil), current.Profiles...)
			changed := false
			now := time.Now()
			for i := range updated.Profiles {
				for _, entry := range usage.Entries {
					if entry.ID == updated.Profiles[i].ID {
						changed = changed || updated.Profiles[i].UsageStatus != entry.Status ||
							!reflect.DeepEqual(updated.Profiles[i].Usage, entry.Usage) ||
							updated.Profiles[i].RemoteUsageError != entry.RemoteUsageError ||
							!updated.Profiles[i].RemoteUsageRetryAt.Equal(entry.RemoteUsageRetryAt) ||
							!updated.Profiles[i].NextRefreshAt.Equal(entry.NextRefreshAt)
						updated.Profiles[i].Usage, updated.Profiles[i].UsageStatus = entry.Usage, entry.Status
						updated.Profiles[i].RemoteUsageError = entry.RemoteUsageError
						updated.Profiles[i].RemoteUsageRetryAt = entry.RemoteUsageRetryAt
						updated.Profiles[i].NextRefreshAt = entry.NextRefreshAt

					}
				}
				changed = changed || usageTimeBoundaryCrossed(updated.Profiles[i].Usage, a.lastUsageDisplay, now)
				changed = changed || updated.Profiles[i].RemoteUsageRetryAt.After(a.lastUsageDisplay)
			}
			a.lastUsageDisplay = now
			a.reconcileOnlineFailures(&updated)
			if changed {
				a.snapshot.Set(&updated)
			}
			if time.Since(a.lastFullRefresh) >= quietFullInterval {
				a.quietFullRefresh(seq, false)
			}
			a.maybeAutoRefresh(updated)
		})
	}()
}

// quietFullRefresh reloads locally cached metadata without changing status or starting a network request.
func (a *app) quietFullRefresh(seq uint64, identityChange bool) {
	if a.fullRefreshing {
		return
	}
	now := time.Now()
	if identityChange && now.Sub(a.lastIdentityRefresh) < identityRetryInterval {
		return
	}
	if identityChange {
		a.lastIdentityRefresh = now
	}
	a.fullRefreshing = true
	a.lastFullRefresh = now
	go func() {
		snapshot, err := a.store.Snapshot()
		a.cx.Post(func() {
			a.fullRefreshing = false
			a.addDuplicate.Set(errors.Is(err, profiles.ErrDuplicateAccount))
			current := a.snapshot.Get()
			if current == nil || a.busy.Get() || a.pending.Get() != nil || current.PendingRecovery || seq != a.sequence {
				return
			}
			if err == nil || snapshot.PendingRecovery {
				if err == nil {
					a.blocked.Set(false)
				}
				a.reconcileOnlineFailures(&snapshot)
				a.snapshot.Set(&snapshot)
				a.lastUsageDisplay = time.Now()
			}
		})
	}()
}

func busyMessage(kind string) string {
	switch kind {
	case "switch":
		return "正在切换账号…"
	case "add":
		return "正在打开新的 Claude Desktop…"
	case "sync":
		return "正在同步会话…"
	case "archive":
		return "正在归档…"
	case "recover":
		return "正在恢复…"
	case "clear-log":
		return "正在清空请求日志…"
	case "clear-cache":
		return "正在清除显示缓存…"
	default:
		return "正在处理…"
	}
}

// watchAddedAccount announces a newly added account once its login is recognized and queries it online.
func (a *app) watchAddedAccount() {
	a.cx.Effect(func() {
		snapshot, returnID := a.snapshot.Get(), a.addReturn.Get()
		if returnID == "" || snapshot == nil || snapshot.Preview {
			return
		}
		for _, profile := range snapshot.Profiles {
			if !profile.Active {
				continue
			}
			if profile.ID == returnID {
				a.cx.Post(func() { a.addReturn.Set("") })
				return
			}
			if !profile.NeedsLogin {
				id := profile.ID
				a.cx.Post(func() {
					a.addReturn.Set("")
					a.addDuplicate.Set(false)
					a.showToast("已添加新账号")
					a.refreshRemoteUsage(id)
				})
			}
			return
		}
	})
}

// abandonAdd goes back to the previous account and archives the empty one that waited for a login.
func (a *app) abandonAdd() {
	snapshot, returnID := a.snapshot.Get(), a.addReturn.Get()
	if snapshot == nil || returnID == "" || a.busy.Get() {
		return
	}
	pendingID := snapshot.ActiveID
	a.run("正在切回原账号…", func(ctx context.Context) (string, error) {
		if err := a.store.Switch(ctx, returnID); err != nil {
			return "", err
		}
		a.cx.Post(func() {
			a.addReturn.Set("")
			a.addDuplicate.Set(false)
		})
		return "已放弃添加，切回原账号", a.store.Archive(pendingID)
	})
}

// showToast shows a short confirmation above the footer.
func (a *app) showToast(message string) {
	a.toastSeq++
	seq := a.toastSeq
	a.toast.Set(message)
	time.AfterFunc(4*time.Second, func() {
		a.cx.Post(func() {
			if a.toastSeq == seq {
				a.toast.Set("")
			}
		})
	})
}
