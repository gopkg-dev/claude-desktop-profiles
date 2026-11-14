package desktopui

import (
	"context"
	"errors"
	"fmt"
	"time"

	"claudeprofiles/profiles"
)

const remoteAccountTimeout = 35 * time.Second

// refreshRemoteUsage is invoked only by an explicit quota refresh action.
func (a *app) refreshRemoteUsage(id string) {
	a.refreshAccounts(id, false)
}

func (a *app) refreshAccounts(id string, onlyExpired bool) {
	current := a.snapshot.Get()
	if current == nil || a.controlsDisabled() {
		return
	}
	store, ok := a.store.(remoteUsageStore)
	if !ok {
		a.failure.Set("当前版本暂不支持在线查询额度")
		return
	}
	targets := make([]profiles.Profile, 0, len(current.Profiles))
	skipped := 0
	now := time.Now()
	for _, profile := range current.Profiles {
		if profile.NeedsLogin || profile.ID == "" || id != "" && profile.ID != id {
			continue
		}
		if onlyExpired && profile.NextRefreshAt.After(now) {
			continue
		}
		if refreshCooldown(profile, now) {
			skipped++
			continue
		}
		targets = append(targets, profile)
	}
	if len(targets) == 0 {
		if skipped > 0 {
			a.status.Set(a.onlineRefreshHint(id))
		} else {
			a.status.Set("没有可在线刷新的已登录账号")
		}
		return
	}
	a.sequence++
	sequence := a.sequence
	ctx, cancel := context.WithCancel(a.ctx)
	a.remoteRefreshCancel = cancel
	a.remoteRefreshing = true
	a.busy.Set(true)
	a.failure.Set("")
	a.onlineFailureNotice = ""
	a.status.Set(fmt.Sprintf("正在查询额度 · 0/%d", len(targets)))
	states := make(map[string]string, len(targets))
	for _, profile := range targets {
		states[profile.ID] = refreshWaiting
	}
	a.progress.Set(refreshProgress{running: true, total: len(targets), states: states})
	go a.queryRemoteUsage(ctx, cancel, store, sequence, targets, onlyExpired, skipped)
}

func (a *app) queryRemoteUsage(
	ctx context.Context,
	cancel context.CancelFunc,
	store remoteUsageStore,
	sequence uint64,
	targets []profiles.Profile,
	onlyExpired bool,
	skipped int,
) {
	defer cancel()
	succeeded, failed, completed := 0, 0, 0
	failures := make(map[string]onlineRefreshFailure)
	var refreshed []string
	for _, profile := range targets {
		if ctx.Err() != nil {
			break
		}
		message := fmt.Sprintf("正在查询额度 · %d/%d · %s", completed+1, len(targets), accountEmail(profile))
		id, current := profile.ID, completed+1
		a.cx.Post(func() {
			if sequence == a.sequence && a.remoteRefreshing {
				a.status.Set(message)
				a.markRefresh(id, refreshLoading, current, 0, 0)
			}
		})
		requestContext, stop := context.WithTimeout(ctx, remoteAccountTimeout)
		attempted := time.Now()
		var err error
		if due, ok := store.(interface {
			RefreshDue(context.Context, string) error
		}); onlyExpired && ok {
			err = due.RefreshDue(requestContext, profile.ID)
		} else {
			err = store.RefreshRemoteUsage(requestContext, profile.ID)
		}
		stop()
		completed++
		outcome, ok, bad := "", 0, 0
		switch {
		case errors.Is(err, profiles.ErrRemoteUsageCooldown):
			skipped++
		case err != nil:
			failed++
			outcome, bad = refreshFailed, 1
			failures[profile.ID] = onlineRefreshFailure{
				accountID: profile.AccountID, message: err.Error(), attempted: attempted,
			}
		default:
			succeeded++
			outcome, ok = refreshDone, 1
			refreshed = append(refreshed, profile.ID)
		}
		a.cx.Post(func() {
			if sequence == a.sequence {
				a.markRefresh(id, outcome, current, ok, bad)
			}
		})
		usage, readErr := a.store.UsageSnapshot()
		if readErr == nil {
			a.cx.Post(func() { a.applyRemoteUsageSnapshot(sequence, usage) })
		}
	}
	snapshot, snapshotErr := a.store.Snapshot()
	stopped := ctx.Err() != nil
	a.cx.Post(func() {
		if sequence != a.sequence {
			return
		}
		a.remoteRefreshing = false
		a.remoteRefreshCancel = nil
		a.busy.Set(false)
		a.finishRefresh(stopped)
		a.lastFullRefresh = time.Now()
		a.blocked.Set(snapshotErr != nil)
		for _, id := range refreshed {
			delete(a.onlineFailures, id)
		}
		if len(failures) > 0 && a.onlineFailures == nil {
			a.onlineFailures = make(map[string]onlineRefreshFailure)
		}
		for id, failure := range failures {
			a.onlineFailures[id] = failure
		}
		if snapshotErr == nil || snapshot.PendingRecovery {
			a.reconcileOnlineFailures(&snapshot)
			a.snapshot.Set(&snapshot)
			a.lastUsageDisplay = time.Now()
		}
		if stopped {
			a.status.Set(fmt.Sprintf("额度刷新已停止 · 已更新 %d/%d 个账号", succeeded, len(targets)))
		} else {
			a.status.Set(fmt.Sprintf("在线刷新完成 · 已更新 %d 个，失败 %d 个", succeeded, failed))
			if skipped > 0 {
				a.status.Set(a.status.Get() + fmt.Sprintf("，%d 个冷却或刷新中已跳过", skipped))
			}
		}
		if snapshotErr != nil {
			a.failure.Set("账号状态读取失败，请点击“刷新账号”重试；已取得的数据保留在本应用缓存中")
		} else if failed > 0 {
			a.setOnlineFailureNotice()
		}
	})
}

func (a *app) applyRemoteUsageSnapshot(sequence uint64, usage profiles.UsageSnapshot) {
	current := a.snapshot.Get()
	if sequence != a.sequence || current == nil || usage.ActiveID != current.ActiveID ||
		usage.PendingRecovery || usage.NeedsRefresh {
		return
	}
	updated := *current
	updated.Profiles = append([]profiles.Profile(nil), current.Profiles...)
	for i := range updated.Profiles {
		for _, entry := range usage.Entries {
			if entry.ID == updated.Profiles[i].ID {
				updated.Profiles[i].Usage, updated.Profiles[i].UsageStatus = entry.Usage, entry.Status
				updated.Profiles[i].RemoteUsageError = entry.RemoteUsageError
				updated.Profiles[i].RemoteUsageRetryAt = entry.RemoteUsageRetryAt
				updated.Profiles[i].NextRefreshAt = entry.NextRefreshAt
			}
		}
	}
	a.reconcileOnlineFailures(&updated)
	a.snapshot.Set(&updated)
	a.lastUsageDisplay = time.Now()
}

func (a *app) stopRemoteRefresh() {
	if a.remoteRefreshCancel == nil {
		return
	}
	a.remoteRefreshCancel()
	a.status.Set("正在停止额度查询…")
}

func (a *app) canRefreshRemote() bool {
	_, supported := a.store.(remoteUsageStore)
	if !supported || a.controlsDisabled() {
		return false
	}
	snapshot := a.snapshot.Get()
	if snapshot == nil {
		return false
	}
	for _, profile := range snapshot.Profiles {
		if !profile.NeedsLogin && !refreshCooldown(profile, time.Now()) {
			return true
		}
	}
	return false
}

// Per-account states while an online refresh runs and briefly after.
const (
	refreshWaiting = "waiting"
	refreshLoading = "loading"
	refreshDone    = "done"
	refreshFailed  = "failed"
)

// refreshProgress is what the title bar and the cards show of an online refresh.
type refreshProgress struct {
	running, stopped    bool
	total, current      int
	succeeded, failures int
	states              map[string]string
	finished            time.Time
}

func (p refreshProgress) state(id string) string {
	if p.states == nil {
		return ""
	}
	return p.states[id]
}

func (a *app) markRefresh(id, state string, current, ok, bad int) {
	progress := a.progress.Get()
	if !progress.running {
		return
	}
	states := make(map[string]string, len(progress.states))
	for key, value := range progress.states {
		states[key] = value
	}
	if state != "" {
		states[id] = state
	} else {
		delete(states, id)
	}
	progress.states = states
	progress.current = max(progress.current, current)
	progress.succeeded += ok
	progress.failures += bad
	a.progress.Set(progress)
}

// finishRefresh keeps the outcome on screen for a few seconds, then clears it.
func (a *app) finishRefresh(stopped bool) {
	progress := a.progress.Get()
	states := make(map[string]string, len(progress.states))
	for key, value := range progress.states {
		if value == refreshDone || value == refreshFailed {
			states[key] = value
		}
	}
	finished := time.Now()
	a.progress.Set(refreshProgress{stopped: stopped, total: progress.total, current: progress.current,
		succeeded: progress.succeeded, failures: progress.failures, states: states, finished: finished})
	time.AfterFunc(refreshOutcomeLinger, func() {
		a.cx.Post(func() {
			if current := a.progress.Get(); !current.running && current.finished.Equal(finished) {
				a.progress.Set(refreshProgress{})
			}
		})
	})
}

// refreshOutcomeLinger is how long the refresh outcome stays in the title bar and on the cards.
var refreshOutcomeLinger = 4 * time.Second
