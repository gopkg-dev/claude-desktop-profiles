package desktopui

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/nsgo-dev/nsgo/core"
	"github.com/nsgo-dev/nsgo/nsgotest"

	"claudeprofiles/profiles"
)

func TestManualRefreshSkipsCoolingAccountsWithoutFailure(t *testing.T) {
	store := manualFixture()
	store.full.Profiles[0].RemoteUsageRetryAt = time.Now().Add(time.Minute)
	a, posted := refreshFixture(store, store.full)
	a.refreshRemoteUsage("")
	finishManualRefresh(t, a, posted)
	store.mu.Lock()
	calls := append([]string(nil), store.remoteCalls...)
	store.mu.Unlock()
	if !reflect.DeepEqual(calls, []string{"second"}) || a.failure.Get() != "" ||
		!strings.Contains(a.status.Get(), "冷却") || a.snapshot.Get().Profiles[0].RemoteUsageError != "" {
		t.Fatal("cooldown was queried or shown as a failed refresh", calls, a.failure.Get(), a.status.Get())
	}
	if *a.snapshot.Get().Profiles[0].Usage.FiveHour != 10 {
		t.Fatal("skipped account lost its cached quota")
	}
}

func TestCooldownDetectedAfterSnapshotIsNotARefreshFailure(t *testing.T) {
	for _, reason := range []error{profiles.ErrRemoteUsageCooldown} {
		t.Run(reason.Error(), func(t *testing.T) {
			store := manualFixture()
			store.remoteErr = map[string]error{"first": reason}
			a, posted := refreshFixture(store, store.full)
			a.refreshRemoteUsage("")
			finishManualRefresh(t, a, posted)
			if a.failure.Get() != "" || a.snapshot.Get().Profiles[0].RemoteUsageError != "" ||
				!strings.Contains(a.status.Get(), "已跳过") {
				t.Fatal("normal concurrency/cooldown state became a failure", a.failure.Get())
			}
		})
	}
}

func TestAllAccountsCoolingDoesNotStartANetworkRefresh(t *testing.T) {
	store := manualFixture()
	until := time.Now().Add(time.Minute)
	store.full.Profiles[0].RemoteUsageRetryAt = until
	store.full.Profiles[1].RemoteUsageRetryAt = until.Add(time.Minute)
	a, _ := refreshFixture(store, store.full)
	a.refreshRemoteUsage("")
	if a.busy.Get() || a.remoteRefreshing || len(store.remoteCalls) != 0 || a.failure.Get() != "" {
		t.Fatal("cooling accounts triggered a request or a failure notice")
	}
	if a.canRefreshRemote() || !strings.Contains(a.status.Get(), until.In(time.Local).Format("15:04:05")) {
		t.Fatal("cooldown deadline was not made visible", a.status.Get())
	}
}

func TestUnpersistedRefreshErrorSurvivesPollingAndClearsAfterSuccess(t *testing.T) {
	store := manualFixture()
	store.omitErrorDetails = true
	store.remoteErr = map[string]error{"first": profiles.ErrRemoteUsageChanged}
	a, posted := refreshFixture(store, store.full)
	a.refreshRemoteUsage("")
	finishManualRefresh(t, a, posted)
	if !strings.Contains(a.failure.Get(), profiles.ErrRemoteUsageChanged.Error()) ||
		a.snapshot.Get().Profiles[0].RemoteUsageError != profiles.ErrRemoteUsageChanged.Error() {
		t.Fatal("returned failure has no concrete reason on the account", a.failure.Get())
	}
	if store.full.Profiles[0].RemoteUsageError != "" {
		t.Fatal("UI error was written into the store's snapshot")
	}
	a.refreshUsage()
	runPosted(t, posted)
	if a.snapshot.Get().Profiles[0].RemoteUsageError == "" || a.failure.Get() == "" {
		t.Fatal("polling erased an unresolved failure")
	}
	store.mu.Lock()
	store.usage.Entries[0].Usage = &profiles.Usage{FiveHour: floatPointer(11), UpdatedAt: time.Now().Add(time.Second)}
	store.mu.Unlock()
	a.refreshUsage()
	runPosted(t, posted)
	if a.snapshot.Get().Profiles[0].RemoteUsageError != "" || a.failure.Get() != "" {
		t.Fatal("successful background data left a stale failure notice", a.failure.Get())
	}
}

func TestRefreshFailureNoticeTracksRemainingAccounts(t *testing.T) {
	store := manualFixture()
	store.omitErrorDetails = true
	store.remoteErr = map[string]error{"first": profiles.ErrRemoteUsageChanged, "second": profiles.ErrNotSignedIn}
	a, posted := refreshFixture(store, store.full)
	a.refreshRemoteUsage("")
	finishManualRefresh(t, a, posted)
	if !strings.Contains(a.failure.Get(), "2 个账号") {
		t.Fatal(a.failure.Get())
	}
	store.mu.Lock()
	store.usage.Entries[0].Usage = &profiles.Usage{UpdatedAt: time.Now().Add(time.Second)}
	store.mu.Unlock()
	a.refreshUsage()
	runPosted(t, posted)
	if !strings.Contains(a.failure.Get(), "1 个账号") ||
		strings.Contains(a.failure.Get(), profiles.ErrRemoteUsageChanged.Error()) {
		t.Fatal("resolved account remained in the failure summary", a.failure.Get())
	}
}

func TestRefreshCooldownAndConcreteErrorsOnCards(t *testing.T) {
	t.Setenv("HOME", testHome(t))
	store := manualFixture()
	store.full.Profiles[0].RemoteUsageRetryAt = time.Now().Add(time.Minute)
	for i := range store.full.Profiles {
		store.full.Profiles[i].Details = &profiles.AccountDetails{Email: store.full.Profiles[i].ID + "@example.com"}
	}
	store.omitErrorDetails = true
	store.remoteErr = map[string]error{"second": profiles.ErrRemoteUsageChanged}
	application := nsgotest.Start(t, func(cx *core.Scope) core.Node {
		return newApp(cx, store, func(context.Context) error { return nil }).view()
	}, nsgotest.Size(480, 760))
	application.WaitUntil(3*time.Second, func() bool { return application.Enabled("id:refresh-usage-second") })
	if application.Enabled("id:refresh-usage-first") || !application.Enabled("id:refresh-usage") {
		t.Fatal("cooldown disabled the wrong refresh controls")
	}
	application.Press("id:refresh-usage")
	failed := func(id string) bool { return strings.Contains(application.Find("id:meta-"+id).Text, "刷新失败") }
	application.WaitUntil(3*time.Second, func() bool { return failed("second") })
	if !strings.Contains(application.Find("id:meta-second").Text, profiles.ErrRemoteUsageChanged.Error()) ||
		failed("first") || application.Enabled("id:refresh-usage-first") {
		t.Fatal("real failure or normal cooldown was presented incorrectly")
	}
	application.Screenshot("refresh-specific-error-cards")
	store.mu.Lock()
	store.remoteErr = nil
	store.mu.Unlock()
	application.Press("id:refresh-usage-second")
	application.WaitUntil(3*time.Second, func() bool { return !failed("second") })
	if application.Exists("text:操作未完成") {
		t.Fatal("successful retry left the previous failure banner visible")
	}
}

func TestUsagePollingUpdatesCooldownAndRendersExpiry(t *testing.T) {
	until := time.Now().Add(-time.Second)
	current := profiles.Snapshot{ActiveID: "first", Profiles: []profiles.Profile{{ID: "first", RemoteUsageRetryAt: until}}}
	store := &refreshTestStore{usage: profiles.UsageSnapshot{ActiveID: "first", Entries: []profiles.UsageEntry{
		{ID: "first", RemoteUsageRetryAt: until, NextRefreshAt: time.Now().Add(time.Minute)},
	}}}
	a, posted := refreshFixture(store, current)
	a.lastUsageDisplay = until.Add(-time.Second)
	before := a.snapshot.Get()
	a.refreshUsage()
	runPosted(t, posted)
	if a.snapshot.Get() == before || a.snapshot.Get().Profiles[0].NextRefreshAt.IsZero() {
		t.Fatal("polling did not update schedule or redraw an expired cooldown")
	}
	if refreshCooldown(a.snapshot.Get().Profiles[0], time.Now()) {
		t.Fatal("expired cooldown is still active")
	}
}
