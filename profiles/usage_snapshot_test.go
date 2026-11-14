package profiles

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func usageEntryByID(t *testing.T, snapshot UsageSnapshot, id string) UsageEntry {
	t.Helper()
	for _, entry := range snapshot.Entries {
		if entry.ID == id {
			return entry
		}
	}
	t.Fatalf("missing usage entry %q", id)
	return UsageEntry{}
}

func TestUsageSnapshotRefreshesOnlineCacheAndPreservesRecordedTime(t *testing.T) {
	t.Parallel()
	m, d, a, b := twoProfiles(t)
	oldAt := time.Now().Add(-time.Hour).Truncate(time.Millisecond)
	newAt := oldAt.Add(time.Minute)
	putOnlineUsage(t, m, a, "account-a", 11, 22, oldAt)
	putOnlineUsage(t, m, b, "account-b", 33, 44, oldAt)
	putUsage(t, m.paths.LiveDir, usageDocument(t, 2, usageRecord(oldAt, "org-b", 99, 99)))
	m.detailsReader = func(string, string, time.Time) (*AccountDetails, string) {
		t.Fatal("lightweight usage refresh decoded account details")
		return nil, DetailsInvalid
	}
	before := readText(t, m.statePath())
	stops, starts := d.stops, d.starts
	first, err := m.UsageSnapshot()
	if err != nil || first.NeedsRefresh || first.PendingRecovery || first.ActiveID != b || len(first.Entries) != 2 {
		t.Fatalf("snapshot=%+v err=%v", first, err)
	}
	parked := usageEntryByID(t, first, a)
	active := usageEntryByID(t, first, b)
	if parked.Status != UsageAvailable || active.Status != UsageAvailable {
		t.Fatalf("parked=%+v active=%+v", parked, active)
	}
	assertPercent(t, parked.Usage.FiveHour, 11)
	assertPercent(t, active.Usage.FiveHour, 33)
	putOnlineUsage(t, m, b, "account-b", 55, 66, newAt)
	second, err := m.UsageSnapshot()
	if err != nil || second.NeedsRefresh {
		t.Fatalf("snapshot=%+v err=%v", second, err)
	}
	parked = usageEntryByID(t, second, a)
	active = usageEntryByID(t, second, b)
	assertPercent(t, parked.Usage.FiveHour, 11)
	assertPercent(t, active.Usage.FiveHour, 55)
	if !parked.Usage.UpdatedAt.Equal(oldAt) || !active.Usage.UpdatedAt.Equal(newAt) {
		t.Fatal("refresh time replaced recorded sample time")
	}
	if readText(t, m.statePath()) != before || d.stops != stops || d.starts != starts {
		t.Fatal("usage refresh changed metadata or Desktop process state")
	}
}

func TestUsageSnapshotPendingRecoveryDoesNotReadProfileFiles(t *testing.T) {
	t.Parallel()
	m, _, a, b := twoProfiles(t)
	if err := writeJSON(m.journalPath(), journal{From: b, To: a}); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(m.paths.LiveDir, m.profileDir(b)); err != nil {
		t.Fatal(err)
	}
	putOnlineUsage(t, m, a, "account-a", 10, 20, time.Now())
	before := readText(t, m.statePath())
	snapshot, err := m.UsageSnapshot()
	if err != nil || !snapshot.PendingRecovery || !snapshot.NeedsRefresh || snapshot.ActiveID != b {
		t.Fatalf("snapshot=%+v err=%v", snapshot, err)
	}
	for _, entry := range snapshot.Entries {
		if entry.Usage != nil || entry.Status != UsagePendingRecovery {
			t.Fatalf("pending entry=%+v", entry)
		}
	}
	if readText(t, m.statePath()) != before {
		t.Fatal("usage refresh wrote state during recovery")
	}
}

func TestUsageSnapshotDetectsIdentityChangesWithoutLearningThem(t *testing.T) {
	t.Parallel()
	for _, change := range []string{"changed", "missing", "malformed"} {
		t.Run(change, func(t *testing.T) {
			m, _, _, b := twoProfiles(t)
			switch change {
			case "changed":
				putLogin(t, m.paths.LiveDir, "different-account")
			case "missing":
				if err := os.Remove(filepath.Join(m.paths.LiveDir, "config.json")); err != nil {
					t.Fatal(err)
				}
			case "malformed":
				if err := os.WriteFile(filepath.Join(m.paths.LiveDir, "config.json"), []byte("broken"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			before := readText(t, m.statePath())
			snapshot, err := m.UsageSnapshot()
			if err != nil || !snapshot.NeedsRefresh {
				t.Fatalf("snapshot=%+v err=%v", snapshot, err)
			}
			entry := usageEntryByID(t, snapshot, b)
			if entry.Usage != nil || entry.Status != UsageUnavailable || readText(t, m.statePath()) != before {
				t.Fatal("changed identity was attributed usage or persisted")
			}
		})
	}
}

func TestUsageSnapshotDetectsFirstLoginWithoutPersistingIt(t *testing.T) {
	t.Parallel()
	m, _ := fixture(t)
	if err := m.Adopt("A"); err != nil {
		t.Fatal(err)
	}
	// Build a pending profile without calling Snapshot after its first login.
	s, err := m.load()
	if err != nil {
		t.Fatal(err)
	}
	id, err := newID()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(m.profileDir(id), 0o700); err != nil {
		t.Fatal(err)
	}
	s.Profiles = append(s.Profiles, Profile{ID: id, Label: "待登录"})
	if err := m.save(s); err != nil {
		t.Fatal(err)
	}
	blank, err := m.UsageSnapshot()
	if err != nil || blank.NeedsRefresh || usageEntryByID(t, blank, id).Status != UsageMissing {
		t.Fatalf("blank snapshot=%+v err=%v", blank, err)
	}
	before := readText(t, m.statePath())
	putLogin(t, m.profileDir(id), "account-new")
	signedIn, err := m.UsageSnapshot()
	if err != nil || !signedIn.NeedsRefresh || usageEntryByID(t, signedIn, id).Usage != nil {
		t.Fatalf("first-login snapshot=%+v err=%v", signedIn, err)
	}
	if readText(t, m.statePath()) != before {
		t.Fatal("lightweight refresh learned a login instead of requesting a full refresh")
	}
}

func TestUsageSnapshotIgnoresMalformedDesktopBusinessCache(t *testing.T) {
	t.Parallel()
	m, _, a, b := twoProfiles(t)
	putUsage(t, m.profileDir(a), "malformed cache")
	snapshot, err := m.UsageSnapshot()
	if err != nil || snapshot.NeedsRefresh || usageEntryByID(t, snapshot, a).Status != UsageMissing ||
		usageEntryByID(t, snapshot, b).Status != UsageMissing {
		t.Fatalf("snapshot=%+v err=%v", snapshot, err)
	}
}

func TestUsageSnapshotUnmanagedLoginRequestsFullRefresh(t *testing.T) {
	t.Parallel()
	m, _ := fixture(t)
	snapshot, err := m.UsageSnapshot()
	if err != nil || !snapshot.NeedsRefresh || snapshot.ActiveID != "" || len(snapshot.Entries) != 0 {
		t.Fatalf("snapshot=%+v err=%v", snapshot, err)
	}
}
