package desktopui

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"claudeprofiles/desktopusage"
	"claudeprofiles/profiles"
	"claudeprofiles/workspaces"
	"github.com/nsgo-dev/nsgo/nsgotest"
)

func TestMain(m *testing.M) { nsgotest.Main(m) }

type fakeStore struct {
	mu            sync.Mutex
	snapshot      profiles.Snapshot
	usageSnapshot *profiles.UsageSnapshot
}

func (f *fakeStore) Snapshot() (profiles.Snapshot, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	result := f.snapshot
	result.Profiles = append([]profiles.Profile(nil), f.snapshot.Profiles...)
	return result, nil
}

func (f *fakeStore) UsageSnapshot() (profiles.UsageSnapshot, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.usageSnapshot != nil {
		return *f.usageSnapshot, nil
	}
	return profiles.UsageSnapshot{ActiveID: f.snapshot.ActiveID}, nil
}

func (f *fakeStore) Adopt(string) error                      { return nil }
func (f *fakeStore) Add(context.Context, string) error       { return nil }
func (f *fakeStore) Archive(string) error                    { return nil }
func (f *fakeStore) Recover(context.Context) error           { return nil }
func (f *fakeStore) DisableSessionSharing() error            { return nil }
func (f *fakeStore) ProfileDirectory(string) (string, error) { return "", profiles.ErrNotFound }
func (f *fakeStore) SyncSessions(context.Context) (workspaces.Report, error) {
	return workspaces.Report{}, nil
}
func (f *fakeStore) Switch(ctx context.Context, id string) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(20 * time.Millisecond):
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.snapshot.ActiveID = id
	for i := range f.snapshot.Profiles {
		f.snapshot.Profiles[i].Active = f.snapshot.Profiles[i].ID == id
	}
	return nil
}
func (f *fakeStore) Rename(id, name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := range f.snapshot.Profiles {
		if f.snapshot.Profiles[i].ID == id {
			f.snapshot.Profiles[i].Label = name
		}
	}
	return nil
}

func desktopFixture() *fakeStore {
	location := time.FixedZone("Asia/Shanghai", 8*60*60)
	// General UI fixtures stay inside a live quota window; reset-boundary tests supply their own dates.
	now := time.Now().In(location).Truncate(time.Minute)
	sessionReset := now.Add(4 * time.Hour)
	weeklyReset := now.AddDate(0, 0, 3)
	fullExpiry := now.AddDate(0, 0, 28)
	fableExpiry := now.AddDate(0, 0, 4)
	updated := now.Add(-5 * time.Minute)
	expiryDate := func(days int) string { return now.AddDate(0, 0, days).Format("2006-01-02") }
	values := []struct {
		id         string
		five       float64
		weekly     float64
		fable      *float64
		expiry     string
		fullGrant  bool
		fableGrant bool
		pending    bool
	}{
		{id: "personal", five: 78, weekly: 92, expiry: expiryDate(3), fullGrant: true, fableGrant: true},
		{id: "work", five: 26, weekly: 53, fable: floatPointer(0), expiry: expiryDate(3), fullGrant: true, fableGrant: true},
		{id: "dev", five: 8, weekly: 24, fable: floatPointer(2), expiry: expiryDate(18), fullGrant: true},
		{id: "backup", five: 0, weekly: 5, fable: floatPointer(0), expiry: expiryDate(22)},
		{id: "team", five: 45, weekly: 68, fable: floatPointer(12), expiry: expiryDate(7), fullGrant: true, fableGrant: true},
		{id: "test", five: 65, weekly: 81, fable: floatPointer(16), expiry: expiryDate(11), fableGrant: true},
		{id: "design", five: 17, weekly: 32, fable: floatPointer(0), expiry: expiryDate(20)},
		{id: "pending", pending: true},
		{id: "ops", five: 8, weekly: 22, fable: floatPointer(4)},
		{id: "client", five: 55, weekly: 61, fable: floatPointer(31)},
		{id: "qa", five: 13, weekly: 24, fable: floatPointer(5)},
		{id: "research", five: 42, weekly: 82, fable: floatPointer(40)},
	}
	result := profiles.Snapshot{ActiveID: "personal", SharedSessions: true}
	for _, entry := range values {
		profile := profiles.Profile{
			ID: entry.id, Label: "备注不应作为账号标题", Active: entry.id == "personal", NeedsLogin: entry.pending,
			Details: &profiles.AccountDetails{Email: entry.id + "@example.com", Plan: "Pro",
				DisplayName: "测试昵称", OrganizationID: "fixture-organization-id",
				Subscription: &profiles.SubscriptionDetails{BillingInterval: "monthly", Status: "active",
					NextChargeDate: entry.expiry}},
		}
		if !entry.pending {
			grants := make([]profiles.ResetGrant, 0, 2)
			if entry.fullGrant {
				grants = append(grants, profiles.ResetGrant{Kind: desktopusage.ResetFull, Remaining: 1, ExpiresAt: fullExpiry})
			}
			if entry.fableGrant {
				grants = append(grants, profiles.ResetGrant{Kind: desktopusage.ResetModel, Model: "Fable", Remaining: 1, ExpiresAt: fableExpiry})
			}
			profile.Usage = &profiles.Usage{
				FiveHour: floatPointer(entry.five), SevenDay: floatPointer(entry.weekly),
				FiveHourReset: &sessionReset, SevenDayReset: &weeklyReset,
				Scoped:          []profiles.ScopedUsage{{Name: "Fable", Percent: entry.fable, ResetAt: &weeklyReset}},
				ScopedUpdatedAt: updated, UpdatedAt: updated, ResetGrantsKnown: true, ResetGrantsAt: updated,
				ResetGrants: grants,
			}
		}
		result.Profiles = append(result.Profiles, profile)
	}
	return &fakeStore{snapshot: result}
}

func floatPointer(value float64) *float64 { return &value }

func testHome(t *testing.T) string {
	t.Helper()
	homeDir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return homeDir
}
