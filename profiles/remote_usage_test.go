package profiles

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"claudeprofiles/desktopusage"
)

func remoteSample(value float64) *desktopusage.UsageRecord {
	now := time.Now()
	reset := now.Add(3 * time.Hour)
	return &desktopusage.UsageRecord{UpdatedAt: now, FiveHour: usageNumber(value), FiveHourReset: &reset}
}

func TestRemoteRefreshWithoutQuerierIsUnavailable(t *testing.T) {
	t.Parallel()
	m, _, active := remoteFixture(t)
	if err := m.RefreshRemoteUsage(context.Background(), active); !errors.Is(err, ErrRemoteUsageUnavailable) {
		t.Fatalf("missing querier error=%v", err)
	}
	if err := m.RefreshDue(context.Background(), active); !errors.Is(err, ErrRemoteUsageUnavailable) {
		t.Fatalf("missing querier error=%v", err)
	}
}

func remoteFixture(t *testing.T) (*Manager, string, string) {
	t.Helper()
	m, _, parked, active := twoProfiles(t)
	return m, parked, active
}

func TestRemoteUsageDoesNotHoldSwitchLockAndRejectsMovedTarget(t *testing.T) {
	t.Parallel()
	m, parked, active := remoteFixture(t)
	querier := &accountQueryFixture{started: make(chan struct{}), release: make(chan struct{})}
	m.SetUsageQuerier(querier)
	done := make(chan error, 1)
	go func() { done <- m.RefreshRemoteUsage(context.Background(), active) }()
	<-querier.started
	switched := make(chan error, 1)
	go func() { switched <- m.Switch(context.Background(), parked) }()
	select {
	case err := <-switched:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		close(querier.release)
		t.Fatal("network wait blocked profile switching")
	}
	close(querier.release)
	if err := <-done; !errors.Is(err, ErrRemoteUsageChanged) {
		t.Fatalf("moved target accepted: %v", err)
	}
	snapshot, err := m.UsageSnapshot()
	if err != nil || usageEntryByID(t, snapshot, parked).Usage != nil || usageEntryByID(t, snapshot, active).Usage != nil {
		t.Fatal("discarded response entered an account card")
	}
}

func TestRemoteUsageRejectsIdentityMismatchWithoutQuery(t *testing.T) {
	t.Parallel()
	m, _, active := remoteFixture(t)
	querier := &accountQueryFixture{}
	m.SetUsageQuerier(querier)
	putLogin(t, m.paths.LiveDir, "different-account")
	if err := m.RefreshRemoteUsage(context.Background(), active); !errors.Is(err, ErrIdentityMismatch) {
		t.Fatalf("wrong account accepted: %v", err)
	}
	if querier.calls.Load() != 0 {
		t.Fatal("identity mismatch still sent a query")
	}
}

func TestRemoteRefreshBlocksRecoveryAndArchivedProfiles(t *testing.T) {
	t.Parallel()
	m, parked, _ := remoteFixture(t)
	querier := &accountQueryFixture{}
	m.SetUsageQuerier(querier)
	if err := m.Archive(parked); err != nil {
		t.Fatal(err)
	}
	if err := m.RefreshRemoteUsage(context.Background(), parked); !errors.Is(err, ErrNotFound) {
		t.Fatalf("archived profile queried: %v", err)
	}
	if err := writeJSON(filepath.Join(m.paths.StoreDir, "switch.json"), journal{Kind: "workspace"}); err != nil {
		t.Fatal(err)
	}
	if err := m.RefreshRemoteUsage(context.Background(), parked); !errors.Is(err, ErrRecoveryRequired) {
		t.Fatalf("pending recovery allowed refresh: %v", err)
	}
	if querier.calls.Load() != 0 {
		t.Fatal("blocked operation still queried")
	}
}
