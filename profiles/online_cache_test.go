package profiles

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"claudeprofiles/desktopauth"
	"claudeprofiles/desktopusage"
)

func putOnlineUsage(t *testing.T, m *Manager, id, account string, five, weekly float64, at time.Time) {
	t.Helper()
	cache, err := m.readOnlineCache()
	if err != nil {
		t.Fatal(err)
	}
	entry := cache.Entries[id]
	entry.AccountID = account
	entry.Usage = &Usage{FiveHour: &five, SevenDay: &weekly, UpdatedAt: at, Source: UsageSourceRemote}
	cache.Entries[id] = entry
	if err := writeJSON(m.onlineCachePath(), cache); err != nil {
		t.Fatal(err)
	}
}

type accountQueryFixture struct {
	calls            atomic.Int32
	failure          error
	started, release chan struct{}
}

type grantAccountQueryFixture struct{ accountQueryFixture }

func (f *grantAccountQueryFixture) QueryAccount(
	ctx context.Context, target desktopusage.Target,
) (*desktopusage.AccountData, error) {
	data, err := f.accountQueryFixture.QueryAccount(ctx, target)
	if err != nil {
		return data, err
	}
	data.Usage.ResetGrantsKnown = true
	data.Usage.ResetGrantsAt = data.Usage.UpdatedAt
	data.Usage.ResetGrants = []desktopusage.ResetGrant{{Remaining: 2,
		ExpiresAt: time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC)}}
	return data, nil
}

func TestOnlineResetInformationSurvivesRestartWithoutAnotherRequest(t *testing.T) {
	t.Parallel()
	m, driver, parked, _ := twoProfiles(t)
	querier := &grantAccountQueryFixture{}
	m.SetUsageQuerier(querier)
	if err := m.RefreshRemoteUsage(context.Background(), parked); err != nil {
		t.Fatal(err)
	}
	restarted := New(m.paths, driver)
	restarted.SetUsageQuerier(querier)
	snapshot, err := restarted.UsageSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	usage := usageEntryByID(t, snapshot, parked).Usage
	if usage == nil || !usage.ResetGrantsKnown || len(usage.ResetGrants) != 1 ||
		usage.ResetGrants[0].Remaining != 2 ||
		usage.ResetGrants[0].ExpiresAt.Format(time.RFC3339) != "2026-12-01T00:00:00Z" {
		t.Fatal("reset display data was lost across restart")
	}
	usage.ResetGrants[0].Remaining = 99
	snapshot, err = restarted.UsageSnapshot()
	if err != nil || usageEntryByID(t, snapshot, parked).Usage.ResetGrants[0].Remaining != 2 || querier.calls.Load() != 1 {
		t.Fatal("display snapshot changed saved data or made a network request", err)
	}
}

func (f *accountQueryFixture) QueryAccount(ctx context.Context, target desktopusage.Target) (*desktopusage.AccountData, error) {
	f.calls.Add(1)
	if f.started != nil {
		close(f.started)
		select {
		case <-f.release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if f.failure != nil {
		return nil, f.failure
	}
	created := time.Date(2026, time.November, 8, 4, 19, 14, 0, time.UTC)
	return &desktopusage.AccountData{AccountID: target.AccountID, OrganizationID: usageOrgA, Email: "online@example.com",
		DisplayName: "Online", FullName: "Online User", AccountCreatedAt: &created,
		BillingType: "apple_subscription", SubscriptionCreatedAt: &created,
		Plan: "Pro", UpdatedAt: time.Now(), Usage: remoteSample(42)}, nil
}

func TestOnlineCacheSurvivesRestartWithoutNetworkAndKeepsCooldown(t *testing.T) {
	t.Parallel()
	m, driver, parked, _ := twoProfiles(t)
	querier := &accountQueryFixture{}
	m.SetUsageQuerier(querier)
	if err := m.RefreshRemoteUsage(context.Background(), parked); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(m.onlineCachePath())
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("cache permissions", err)
	}
	if text := readText(t, m.onlineCachePath()); strings.Contains(text, "AccessToken") || strings.Contains(text, "Bearer") {
		t.Fatal("credential in display cache")
	}
	restarted := New(m.paths, driver)
	restarted.SetUsageQuerier(querier)
	for range 2 {
		snapshot, err := restarted.Snapshot()
		if err != nil {
			t.Fatal(err)
		}
		for _, profile := range snapshot.Profiles {
			if profile.ID == parked {
				if profile.Details == nil || profile.Details.Email != "online@example.com" {
					t.Fatal("details not restored")
				}
				if profile.Details.FullName != "Online User" || profile.Details.CreatedAt == nil ||
					profile.Details.Subscription == nil || profile.Details.Subscription.BillingType != "apple_subscription" ||
					profile.Details.Subscription.CreatedAt == nil || profile.Details.Subscription.PlanEndingAt != nil {
					t.Fatal("profile fields did not survive restart or expiry was invented")
				}
				assertPercent(t, profile.Usage.FiveHour, 42)
			}
		}
		if _, err := restarted.UsageSnapshot(); err != nil {
			t.Fatal(err)
		}
		if _, err := restarted.PreviewSnapshot(); err != nil {
			t.Fatal(err)
		}
	}
	if querier.calls.Load() != 1 {
		t.Fatal("restart or snapshot made a network request")
	}
	if err := restarted.RefreshRemoteUsage(context.Background(), parked); !errors.Is(err, ErrRemoteUsageCooldown) {
		t.Fatal(err)
	}
	if err := restarted.ClearOnlineCache(); err != nil {
		t.Fatal(err)
	}
	if err := restarted.RefreshRemoteUsage(context.Background(), parked); !errors.Is(err, ErrRemoteUsageCooldown) {
		t.Fatal("clear bypassed cooldown", err)
	}
	if querier.calls.Load() != 1 {
		t.Fatal("cooldown sent request")
	}
}

func TestOnlineCacheDoesNotReadDesktopBusinessFilesOrCrossAccounts(t *testing.T) {
	t.Parallel()
	m, _, parked, active := twoProfiles(t)
	querier := &accountQueryFixture{}
	m.SetUsageQuerier(querier)
	putUsage(t, m.paths.LiveDir, usageDocument(t, 2, usageRecord(time.Now(), "org-b", 99, 99)))
	if err := m.RefreshRemoteUsage(context.Background(), parked); err != nil {
		t.Fatal(err)
	}
	usage, err := m.UsageSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	assertPercent(t, usageEntryByID(t, usage, parked).Usage.FiveHour, 42)
	if usageEntryByID(t, usage, active).Usage != nil {
		t.Fatal("Desktop cache used for active account")
	}
	putLogin(t, m.profileDir(parked), "replacement-account")
	usage, err = m.UsageSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	if usageEntryByID(t, usage, parked).Usage != nil {
		t.Fatal("old data followed changed identity")
	}
}

func TestOnlineFailureBackoffIsDurableAndKeepsLastGoodData(t *testing.T) {
	t.Parallel()
	m, driver, parked, _ := twoProfiles(t)
	q := &accountQueryFixture{}
	m.SetUsageQuerier(q)
	if err := m.RefreshRemoteUsage(context.Background(), parked); err != nil {
		t.Fatal(err)
	}
	cache, _ := m.readOnlineCache()
	entry := cache.Entries[parked]
	entry.NextAllowed = time.Time{}
	cache.Entries[parked] = entry
	if err := writeJSON(m.onlineCachePath(), cache); err != nil {
		t.Fatal(err)
	}
	q.failure = desktopusage.ErrNetwork
	if err := m.RefreshRemoteUsage(context.Background(), parked); err == nil {
		t.Fatal("failure accepted")
	}
	restarted := New(m.paths, driver)
	restarted.SetUsageQuerier(q)
	if err := restarted.RefreshRemoteUsage(context.Background(), parked); !errors.Is(err, ErrRemoteUsageCooldown) {
		t.Fatal(err)
	}
	usage, err := restarted.UsageSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	result := usageEntryByID(t, usage, parked)
	assertPercent(t, result.Usage.FiveHour, 42)
	if result.RemoteUsageError == "" {
		t.Fatal("failure was hidden after restart")
	}
}

func TestOnlineRefreshCoalescesAcrossManagers(t *testing.T) {
	t.Parallel()
	m, driver, parked, _ := twoProfiles(t)
	q := &accountQueryFixture{started: make(chan struct{}), release: make(chan struct{})}
	m.SetUsageQuerier(q)
	second := New(m.paths, driver)
	second.SetUsageQuerier(q)
	done := make(chan error, 1)
	go func() { done <- m.RefreshRemoteUsage(context.Background(), parked) }()
	<-q.started
	if err := second.RefreshRemoteUsage(context.Background(), parked); !errors.Is(err, ErrRemoteUsageCooldown) {
		t.Fatal(err)
	}
	close(q.release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if q.calls.Load() != 1 {
		t.Fatal("duplicate concurrent request")
	}
}

func TestSettingsPersistAndDefaultToManualRefresh(t *testing.T) {
	t.Parallel()
	m, driver := fixture(t)
	settings, err := m.Settings()
	if err != nil || settings.AutoRefresh || settings.CacheMinutes != 5 {
		t.Fatal(settings, err)
	}
	settings.CacheMinutes = 15
	settings.AutoRefresh = true
	if err := m.SaveSettings(settings); err != nil {
		t.Fatal(err)
	}
	got, err := New(m.paths, driver).Settings()
	if err != nil || got != settings {
		t.Fatal(got, err)
	}
	settings.CacheMinutes = 0
	if err := m.SaveSettings(settings); err == nil {
		t.Fatal("invalid TTL accepted")
	}
}

func TestAutomaticRefreshHonorsCacheLifetimeAcrossRestart(t *testing.T) {
	t.Parallel()
	m, driver, parked, _ := twoProfiles(t)
	q := &accountQueryFixture{}
	m.SetUsageQuerier(q)
	if err := m.RefreshRemoteUsage(context.Background(), parked); err != nil {
		t.Fatal(err)
	}
	cache, _ := m.readOnlineCache()
	entry := cache.Entries[parked]
	entry.NextAllowed = time.Time{}
	cache.Entries[parked] = entry
	if err := writeJSON(m.onlineCachePath(), cache); err != nil {
		t.Fatal(err)
	}
	restarted := New(m.paths, driver)
	restarted.SetUsageQuerier(q)
	if err := restarted.RefreshDue(context.Background(), parked); err != nil {
		t.Fatal(err)
	}
	if q.calls.Load() != 1 {
		t.Fatal("fresh cache was queried after restart")
	}
}

func TestUsagePollingExposesManualCooldownSeparatelyFromCacheLifetime(t *testing.T) {
	t.Parallel()
	m, _, parked, _ := twoProfiles(t)
	m.SetUsageQuerier(&accountQueryFixture{})
	if err := m.RefreshRemoteUsage(context.Background(), parked); err != nil {
		t.Fatal(err)
	}
	usage, err := m.UsageSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	entry := usageEntryByID(t, usage, parked)
	if !entry.RemoteUsageRetryAt.After(time.Now()) || !entry.NextRefreshAt.After(entry.RemoteUsageRetryAt) {
		t.Fatal("manual cooldown and automatic cache lifetime were conflated", entry)
	}
	snapshot, err := m.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	for _, profile := range snapshot.Profiles {
		if profile.ID == parked && (!profile.RemoteUsageRetryAt.Equal(entry.RemoteUsageRetryAt) ||
			!profile.NextRefreshAt.Equal(entry.NextRefreshAt)) {
			t.Fatal("full and periodic snapshots disagree about refresh scheduling")
		}
	}
}

func putTokenCache(t *testing.T, dir, account, token string) {
	t.Helper()
	b, err := json.Marshal(map[string]any{"lastKnownAccountUuid": account, "oauth:tokenCacheV2": token})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.json"), b, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestNewDesktopLoginLiftsLoginFailureBackoff(t *testing.T) {
	t.Parallel()
	m, driver, _, active := twoProfiles(t)
	putTokenCache(t, m.paths.LiveDir, "account-b", "old-login")
	q := &accountQueryFixture{failure: desktopauth.ErrExpired}
	m.SetUsageQuerier(q)
	if err := m.RefreshRemoteUsage(context.Background(), active); !errors.Is(err, desktopauth.ErrExpired) {
		t.Fatal(err)
	}
	if err := m.RefreshRemoteUsage(context.Background(), active); !errors.Is(err, ErrRemoteUsageCooldown) {
		t.Fatal("login failure did not back off", err)
	}

	putTokenCache(t, m.paths.LiveDir, "account-b", "new-login")
	restarted := New(m.paths, driver)
	restarted.SetUsageQuerier(q)
	usage, err := restarted.UsageSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	if entry := usageEntryByID(t, usage, active); entry.RemoteUsageError != "" || !entry.RemoteUsageRetryAt.IsZero() {
		t.Fatalf("stale login failure still shown: %q until %v", entry.RemoteUsageError, entry.RemoteUsageRetryAt)
	}
	q.failure = nil
	if err := restarted.RefreshRemoteUsage(context.Background(), active); err != nil {
		t.Fatal("refresh after a new login was blocked", err)
	}
	cache, _ := restarted.readOnlineCache()
	if entry := cache.Entries[active]; entry.Failures != 0 || entry.CredentialStamp != "" {
		t.Fatalf("failure state survived success: %+v", entry)
	}
}

func TestOtherFailuresKeepBackoffAfterNewLogin(t *testing.T) {
	t.Parallel()
	m, _, _, active := twoProfiles(t)
	putTokenCache(t, m.paths.LiveDir, "account-b", "old-login")
	m.SetUsageQuerier(&accountQueryFixture{failure: desktopusage.ErrNetwork})
	if err := m.RefreshRemoteUsage(context.Background(), active); !errors.Is(err, desktopusage.ErrNetwork) {
		t.Fatal(err)
	}
	putTokenCache(t, m.paths.LiveDir, "account-b", "new-login")
	if err := m.RefreshRemoteUsage(context.Background(), active); !errors.Is(err, ErrRemoteUsageCooldown) {
		t.Fatal("network backoff was lifted by a login change", err)
	}
}
