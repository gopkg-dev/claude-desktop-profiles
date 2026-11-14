package profiles

import (
	"context"
	"errors"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"claudeprofiles/desktopusage"
)

func TestExitCacheSurvivesRestartAndFailurePreservesLastResult(t *testing.T) {
	t.Parallel()
	m, driver := fixture(t)
	var calls atomic.Int32
	m.exitQuerier = func(context.Context, desktopusage.ExitInfo) (desktopusage.ExitInfo, error) {
		calls.Add(1)
		return desktopusage.ExitInfo{IP: "192.0.2.10", CountryCode: "US", City: "Seattle",
			CheckedAt: time.Now(), GeoUpdatedAt: time.Now()}, nil
	}
	if _, err := m.ExitInfo(); err != nil || calls.Load() != 0 {
		t.Fatal("cache load initiated a query", err)
	}
	value, err := m.RefreshExit(context.Background())
	if err != nil || value.City != "Seattle" || calls.Load() != 1 {
		t.Fatal(value, err)
	}
	info, err := os.Stat(m.exitInfoPath())
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("invalid cache permissions", err)
	}
	restarted := New(m.paths, driver)
	restarted.exitQuerier = m.exitQuerier
	if loaded, err := restarted.ExitInfo(); err != nil || loaded.City != value.City || calls.Load() != 1 {
		t.Fatal("restart lost result or initiated a query", loaded, err)
	}
	if _, err := restarted.RefreshExit(context.Background()); !errors.Is(err, ErrExitCooldown) || calls.Load() != 1 {
		t.Fatal("restart bypassed cooldown", err)
	}
	value.NextAllowed = time.Time{}
	if err := writeJSON(m.exitInfoPath(), value); err != nil {
		t.Fatal(err)
	}
	retryAt := time.Now().Add(time.Hour).Truncate(time.Second)
	restarted.exitQuerier = func(context.Context, desktopusage.ExitInfo) (desktopusage.ExitInfo, error) {
		return desktopusage.ExitInfo{NextAllowed: retryAt}, desktopusage.ErrExitTrace
	}
	failed, err := restarted.RefreshExit(context.Background())
	if !errors.Is(err, desktopusage.ErrExitTrace) || failed.IP != value.IP || failed.City != value.City ||
		!failed.NextAllowed.Equal(retryAt) || failed.LastError == "" {
		t.Fatal("failure discarded cached data or retry time", failed, err)
	}
	loaded, err := New(m.paths, driver).ExitInfo()
	if err != nil || !loaded.NextAllowed.Equal(retryAt) || loaded.LastError == "" {
		t.Fatal("failure cooldown not persisted", loaded, err)
	}
}

func TestConcurrentExitQueryUsesDurableCooldown(t *testing.T) {
	t.Parallel()
	m, driver := fixture(t)
	started, release := make(chan struct{}), make(chan struct{})
	m.exitQuerier = func(context.Context, desktopusage.ExitInfo) (desktopusage.ExitInfo, error) {
		close(started)
		<-release
		return desktopusage.ExitInfo{IP: "192.0.2.10", CountryCode: "US", CheckedAt: time.Now()}, nil
	}
	done := make(chan error, 1)
	go func() { _, err := m.RefreshExit(context.Background()); done <- err }()
	<-started
	other := New(m.paths, driver)
	_, err := other.RefreshExit(context.Background())
	close(release)
	if !errors.Is(err, ErrExitCooldown) {
		t.Fatal("second manager queried while first request was running", err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
