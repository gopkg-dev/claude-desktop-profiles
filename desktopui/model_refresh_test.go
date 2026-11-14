package desktopui

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/nsgo-dev/nsgo/core"

	"claudeprofiles/profiles"
	"claudeprofiles/workspaces"
)

type refreshTestStore struct {
	mu         sync.Mutex
	usage      profiles.UsageSnapshot
	full       profiles.Snapshot
	fullErr    error
	fullCalls  int
	usageCalls int
	started    chan struct{}
	release    chan struct{}
}

func (s *refreshTestStore) Snapshot() (profiles.Snapshot, error) {
	s.mu.Lock()
	s.fullCalls++
	full, err, started, release := s.full, s.fullErr, s.started, s.release
	s.mu.Unlock()
	if started != nil {
		started <- struct{}{}
	}
	if release != nil {
		<-release
	}
	return full, err
}

func (s *refreshTestStore) UsageSnapshot() (profiles.UsageSnapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.usageCalls++
	return s.usage, nil
}

func (*refreshTestStore) Adopt(string) error                      { return nil }
func (*refreshTestStore) Add(context.Context, string) error       { return nil }
func (*refreshTestStore) Switch(context.Context, string) error    { return nil }
func (*refreshTestStore) Archive(string) error                    { return nil }
func (*refreshTestStore) Recover(context.Context) error           { return nil }
func (*refreshTestStore) DisableSessionSharing() error            { return nil }
func (*refreshTestStore) ProfileDirectory(string) (string, error) { return "", profiles.ErrNotFound }
func (*refreshTestStore) SyncSessions(context.Context) (workspaces.Report, error) {
	return workspaces.Report{}, nil
}

func refreshFixture(store Store, current profiles.Snapshot) (*app, <-chan func()) {
	posted := make(chan func(), 64)
	cx := core.NewRoot(core.RootOptions{Post: func(fn func()) { posted <- fn }})
	a := &app{cx: cx, ctx: cx.Context(), store: store,
		snapshot: core.NewSignal(cx, &current), busy: core.NewSignal(cx, false),
		blocked: core.NewSignal(cx, false), pending: core.NewSignal[*action](cx, nil), status: core.NewSignal(cx, "原状态"),
		failure: core.NewSignal(cx, ""), lastFullRefresh: time.Now(),
		progress: core.NewSignalFunc(cx, refreshProgress{}, func(a, b refreshProgress) bool { return reflect.DeepEqual(a, b) }),
		toast:    core.NewSignal(cx, ""), addReturn: core.NewSignal(cx, ""), addDuplicate: core.NewSignal(cx, false),
		sessions: core.NewSignal[*sessionPage](cx, nil)}
	return a, posted
}

func runPosted(t *testing.T, posted <-chan func()) {
	t.Helper()
	select {
	case fn := <-posted:
		fn()
	case <-time.After(3 * time.Second):
		t.Fatal("后台刷新未按时返回")
	}
}

func TestIdentityChangeTriggersQuietFullRefresh(t *testing.T) {
	current := profiles.Snapshot{ActiveID: "first", Profiles: []profiles.Profile{{ID: "first", NeedsLogin: true}}}
	store := &refreshTestStore{
		usage: profiles.UsageSnapshot{ActiveID: "first", NeedsRefresh: true},
		full: profiles.Snapshot{ActiveID: "first", Profiles: []profiles.Profile{{ID: "first",
			Details: &profiles.AccountDetails{Email: "new@example.com"}}}},
	}
	a, posted := refreshFixture(store, current)
	a.refreshUsage()
	runPosted(t, posted)
	runPosted(t, posted)
	if got := a.snapshot.Get().Profiles[0].Details; got == nil || got.Email != "new@example.com" {
		t.Fatal("登录后的账号资料未更新")
	}
	if a.status.Get() != "原状态" || a.failure.Get() != "" {
		t.Fatal("后台刷新不应更改操作状态")
	}
}

func TestPeriodicFullRefreshLoadsDelayedMetadata(t *testing.T) {
	current := profiles.Snapshot{ActiveID: "first", Profiles: []profiles.Profile{{ID: "first"}}}
	store := &refreshTestStore{
		usage: profiles.UsageSnapshot{ActiveID: "first"},
		full: profiles.Snapshot{ActiveID: "first", Profiles: []profiles.Profile{{ID: "first",
			Details: &profiles.AccountDetails{Email: "new@example.com"}}}},
	}
	a, posted := refreshFixture(store, current)
	a.lastFullRefresh = time.Now().Add(-quietFullInterval - time.Second)
	a.refreshUsage()
	runPosted(t, posted)
	runPosted(t, posted)
	if a.snapshot.Get().Profiles[0].Details == nil {
		t.Fatal("延迟写入的资料未重新读取")
	}
}

func TestQuietFullRefreshDoesNotOverwriteNewerOperation(t *testing.T) {
	store := &refreshTestStore{usage: profiles.UsageSnapshot{ActiveID: "first", NeedsRefresh: true},
		full: profiles.Snapshot{ActiveID: "first"}, started: make(chan struct{}, 1), release: make(chan struct{})}
	a, posted := refreshFixture(store, profiles.Snapshot{ActiveID: "first"})
	a.refreshUsage()
	runPosted(t, posted)
	select {
	case <-store.started:
	case <-time.After(3 * time.Second):
		t.Fatal("完整刷新未开始")
	}
	a.sequence++
	a.snapshot.Set(&profiles.Snapshot{ActiveID: "second"})
	close(store.release)
	runPosted(t, posted)
	if a.snapshot.Get().ActiveID != "second" {
		t.Fatal("过期刷新覆盖了更新后的账号")
	}
}

func TestIdentityRefreshFailureIsThrottledAndPreservesSnapshot(t *testing.T) {
	store := &refreshTestStore{usage: profiles.UsageSnapshot{ActiveID: "first", NeedsRefresh: true},
		fullErr: errors.New("缓存暂不可用")}
	a, posted := refreshFixture(store, profiles.Snapshot{ActiveID: "first"})
	a.refreshUsage()
	runPosted(t, posted)
	runPosted(t, posted)
	if a.snapshot.Get().ActiveID != "first" || a.failure.Get() != "" {
		t.Fatal("失败的后台刷新不应覆盖有效状态或反复报错")
	}
	a.refreshUsage()
	runPosted(t, posted)
	store.mu.Lock()
	count := store.fullCalls
	store.mu.Unlock()
	if count != 1 {
		t.Fatalf("完整刷新调用 %d 次，未遵守冷却时间", count)
	}
}

func TestQuietRefreshSkipsBusyDialogAndRecovery(t *testing.T) {
	for _, reason := range []string{"busy", "dialog", "recovery"} {
		t.Run(reason, func(t *testing.T) {
			store := &refreshTestStore{}
			a, _ := refreshFixture(store, profiles.Snapshot{ActiveID: "first"})
			switch reason {
			case "busy":
				a.busy.Set(true)
			case "dialog":
				a.pending.Set(&action{kind: "switch"})
			case "recovery":
				a.snapshot.Set(&profiles.Snapshot{ActiveID: "first", PendingRecovery: true})
			}
			a.refreshUsage()
			store.mu.Lock()
			calls := store.usageCalls
			store.mu.Unlock()
			if calls != 0 || a.usageRefreshing || a.fullRefreshing {
				t.Fatal("不应在操作或恢复期间启动后台读取")
			}
		})
	}
}
