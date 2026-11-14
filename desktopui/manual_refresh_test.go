package desktopui

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/nsgo-dev/nsgo/core"
	"github.com/nsgo-dev/nsgo/nsgotest"

	"claudeprofiles/profiles"
)

type manualTestStore struct {
	*refreshTestStore
	remoteCalls      []string
	remoteErr        map[string]error
	waitForStop      bool
	queryStart       chan struct{}
	omitErrorDetails bool
}

func (s *manualTestStore) RefreshRemoteUsage(ctx context.Context, id string) error {
	s.mu.Lock()
	s.remoteCalls = append(s.remoteCalls, id)
	wait, started := s.waitForStop, s.queryStart
	err := s.remoteErr[id]
	s.mu.Unlock()
	if started != nil {
		started <- struct{}{}
	}
	if wait {
		<-ctx.Done()
		err = ctx.Err()
	}
	if errors.Is(err, profiles.ErrRemoteUsageCooldown) {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.full.Profiles = append([]profiles.Profile(nil), s.full.Profiles...)
	for i := range s.full.Profiles {
		profile := &s.full.Profiles[i]
		if profile.ID != id {
			continue
		}
		if err != nil {
			if !s.omitErrorDetails {
				profile.RemoteUsageError = err.Error()
			}
			continue
		}
		profile.RemoteUsageError = ""
		profile.Usage = &profiles.Usage{FiveHour: floatPointer(42), SevenDay: floatPointer(59),
			Source: profiles.UsageSourceRemote, UpdatedAt: time.Now()}
		profile.UsageStatus = profiles.UsageAvailable
	}
	s.usage = profiles.UsageSnapshot{ActiveID: s.full.ActiveID}
	for _, profile := range s.full.Profiles {
		s.usage.Entries = append(s.usage.Entries, profiles.UsageEntry{ID: profile.ID, Usage: profile.Usage,
			Status: profile.UsageStatus, RemoteUsageError: profile.RemoteUsageError,
			NextRefreshAt: profile.NextRefreshAt, RemoteUsageRetryAt: profile.RemoteUsageRetryAt})
	}
	return err
}

func manualFixture() *manualTestStore {
	return &manualTestStore{refreshTestStore: &refreshTestStore{full: profiles.Snapshot{
		ActiveID: "first", Profiles: []profiles.Profile{
			{ID: "first", Active: true, Usage: &profiles.Usage{FiveHour: floatPointer(10)}},
			{ID: "second", Usage: &profiles.Usage{FiveHour: floatPointer(20)}},
			{ID: "pending", NeedsLogin: true},
		},
	}}}
}

func finishManualRefresh(t *testing.T, a *app, posted <-chan func()) {
	t.Helper()
	for a.busy.Get() {
		runPosted(t, posted)
	}
}

func TestManualRefreshProcessesAccountsAndRetainsFailedValue(t *testing.T) {
	store := manualFixture()
	store.remoteErr = map[string]error{"second": errors.New("登录凭证已过期")}
	a, posted := refreshFixture(store, store.full)
	a.refreshRemoteUsage("")
	if !a.busy.Get() || !a.controlsDisabled() || !a.remoteRefreshing {
		t.Fatal("query must disable mutations while allowing cancellation")
	}
	a.prepare("switch", profiles.Profile{ID: "second"})
	a.refreshRemoteUsage("")
	if a.pending.Get() != nil {
		t.Fatal("account mutation must not begin during a query")
	}
	finishManualRefresh(t, a, posted)
	store.mu.Lock()
	calls := append([]string(nil), store.remoteCalls...)
	store.mu.Unlock()
	if !reflect.DeepEqual(calls, []string{"first", "second"}) {
		t.Fatalf("expected one sequential call per signed-in account: %v", calls)
	}
	rows := a.snapshot.Get().Profiles
	if *rows[0].Usage.FiveHour != 42 || *rows[1].Usage.FiveHour != 20 ||
		rows[1].RemoteUsageError != "登录凭证已过期" || !strings.Contains(a.failure.Get(), "1 个账号") {
		t.Fatal("successful value, preserved failed value, or per-account error missing")
	}
	a.refreshUsage()
	runPosted(t, posted)
	if a.snapshot.Get().Profiles[1].RemoteUsageError == "" {
		t.Fatal("local polling erased the network failure")
	}
}

func TestManualRefreshOneAccountAndCancellation(t *testing.T) {
	store := manualFixture()
	a, posted := refreshFixture(store, store.full)
	a.refreshRemoteUsage("second")
	finishManualRefresh(t, a, posted)
	store.mu.Lock()
	calls := append([]string(nil), store.remoteCalls...)
	store.mu.Unlock()
	if !reflect.DeepEqual(calls, []string{"second"}) {
		t.Fatalf("individual refresh called unrelated accounts: %v", calls)
	}
	store.mu.Lock()
	store.remoteCalls = nil
	store.waitForStop = true
	store.queryStart = make(chan struct{}, 1)
	store.mu.Unlock()
	a.refreshRemoteUsage("")
	select {
	case <-store.queryStart:
	case <-time.After(3 * time.Second):
		t.Fatal("query did not start")
	}
	a.stopRemoteRefresh()
	finishManualRefresh(t, a, posted)
	store.mu.Lock()
	calls = append([]string(nil), store.remoteCalls...)
	store.mu.Unlock()
	if !reflect.DeepEqual(calls, []string{"first"}) || !strings.Contains(a.status.Get(), "已停止") {
		t.Fatalf("cancellation sent extra requests: %v, %s", calls, a.status.Get())
	}
}

func TestLocalRefreshAndTimerNeverQueryRemote(t *testing.T) {
	store := manualFixture()
	a, posted := refreshFixture(store, store.full)
	a.refresh()
	runPosted(t, posted)
	a.lastFullRefresh = time.Now().Add(-quietFullInterval - time.Second)
	a.refreshUsage()
	runPosted(t, posted)
	runPosted(t, posted)
	store.mu.Lock()
	defer store.mu.Unlock()
	if len(store.remoteCalls) != 0 {
		t.Fatal("local/periodic refresh must not query the server")
	}
}

func TestRemotePresentationOmitsLocalSourceTimes(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	profile := profiles.Profile{Usage: &profiles.Usage{
		Source: profiles.UsageSourceRemote, FiveHour: floatPointer(42), SevenDay: floatPointer(59),
		UpdatedAt: now, ScopedUpdatedAt: now.Add(-time.Hour), ResetGrantsAt: now.Add(-2 * time.Hour),
	}}
	if cacheTooltip(profile) != "额度更新："+usageTime(now) {
		t.Fatal("local sampling times appeared as account details")
	}
	meters := profileMeters(profile, now)
	if percent(meters[0].value) != "42%" || meters[0].reset != "" || meters[0].hint != "" {
		t.Fatal("remote quota contains information absent from the API", meters[0])
	}
}

func TestManualRefreshControlsOnCards(t *testing.T) {
	t.Setenv("HOME", testHome(t))
	store := manualFixture()
	for i := range store.full.Profiles {
		store.full.Profiles[i].Details = &profiles.AccountDetails{Email: store.full.Profiles[i].ID + "@example.com"}
	}
	store.remoteErr = map[string]error{"second": errors.New("登录凭证已过期")}
	application := nsgotest.Start(t, func(cx *core.Scope) core.Node {
		return newApp(cx, store, func(context.Context) error { return nil }).view()
	}, nsgotest.Size(480, 760))
	application.WaitUntil(3*time.Second, func() bool { return application.Exists("id:profile-first") })
	application.WaitUntil(3*time.Second, func() bool { return application.Enabled("id:refresh-usage-first") })
	store.mu.Lock()
	initialCalls := len(store.remoteCalls)
	store.mu.Unlock()
	if initialCalls != 0 {
		t.Fatal("opening the app or refreshing accounts made a network query")
	}
	application.Press("id:refresh-usage-first")
	application.WaitUntil(3*time.Second, func() bool { return application.Exists("id:profile-first >> text:42%") })
	application.Press("id:refresh-usage")
	application.WaitUntil(3*time.Second, func() bool {
		return strings.Contains(application.Find("id:meta-second").Text, "刷新失败")
	})
	application.Find("id:profile-second >> text:20%")
	application.Screenshot("manual-usage-cards")
	application.Resize(420, 640)
	card := application.FindMounted("id:profile-second")
	cardBounds := manualViewBounds(card)
	for _, id := range []string{"refresh-usage-second", "folder-second", "archive-second", "switch-second"} {
		bounds := manualViewBounds(application.FindMounted("id:" + id))
		if bounds.Origin.X < cardBounds.Origin.X ||
			bounds.Origin.X+bounds.Size.Width > cardBounds.Origin.X+cardBounds.Size.Width {
			t.Fatalf("%s overflows compact card: %v, card %v", id, bounds, cardBounds)
		}
	}
	application.Screenshot("manual-usage-cards-compact")
}

func TestExplicitStartupRefreshRunsOnceThroughManualRefresh(t *testing.T) {
	t.Setenv("HOME", testHome(t))
	store := manualFixture()
	application := nsgotest.Start(t, func(cx *core.Scope) core.Node {
		model := newApp(cx, store, func(context.Context) error { return nil })
		model.refreshOnceAfterStartup()
		return model.view()
	}, nsgotest.Size(480, 760))
	application.WaitUntil(3*time.Second, func() bool {
		return application.Exists("id:profile-first >> text:42%") && application.Enabled("id:refresh-usage-first")
	})
	application.Press("id:open-settings")
	application.Press("id:back")
	store.mu.Lock()
	calls := append([]string(nil), store.remoteCalls...)
	store.mu.Unlock()
	if !reflect.DeepEqual(calls, []string{"first", "second"}) {
		t.Fatalf("startup refresh repeated or queried an unsigned account: %v", calls)
	}
}

func manualViewBounds(view *nsgotest.View) core.Bounds {
	bounds := view.Frame
	for parent := view.Parent; parent != nil; parent = parent.Parent {
		bounds.Origin.X += parent.Frame.Origin.X - parent.Offset.X
		bounds.Origin.Y += parent.Frame.Origin.Y - parent.Offset.Y
	}
	return bounds
}
