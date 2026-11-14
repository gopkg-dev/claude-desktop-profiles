package profiles

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProfileDirectoryTracksActiveAccount(t *testing.T) {
	t.Parallel()
	m, driver, first, second := twoProfiles(t)
	assertDir := func(id, want string) {
		t.Helper()
		got, err := m.ProfileDirectory(id)
		if err != nil || got != want {
			t.Fatalf("profile directory = %q, %v; want %q", got, err, want)
		}
	}
	stops, starts := driver.stops, driver.starts
	assertDir(first, m.profileDir(first))
	assertDir(second, m.paths.LiveDir)
	if driver.stops != stops || driver.starts != starts {
		t.Fatal("resolving a directory must not control Claude Desktop")
	}
	if err := m.Switch(context.Background(), first); err != nil {
		t.Fatal(err)
	}
	stops, starts = driver.stops, driver.starts
	assertDir(first, m.paths.LiveDir)
	assertDir(second, m.profileDir(second))
	if driver.stops != stops || driver.starts != starts {
		t.Fatal("resolving a directory must not control Claude Desktop")
	}
	for _, id := range []string{"../bad", strings.Repeat("f", 32)} {
		if _, err := m.ProfileDirectory(id); !errors.Is(err, ErrNotFound) {
			t.Fatalf("invalid or unknown id returned %v", err)
		}
	}
}

func TestProfileDirectoryRejectsRecoveryAndSymlink(t *testing.T) {
	t.Parallel()
	m, _, first, second := twoProfiles(t)
	if err := os.WriteFile(m.journalPath(), []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := m.ProfileDirectory(second); !errors.Is(err, ErrRecoveryRequired) {
		t.Fatalf("pending recovery returned %v", err)
	}
	if err := os.Remove(m.journalPath()); err != nil {
		t.Fatal(err)
	}
	parked := m.profileDir(first)
	if err := os.Rename(parked, parked+".saved"); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Dir(parked), parked); err != nil {
		t.Fatal(err)
	}
	if _, err := m.ProfileDirectory(first); !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("symlink returned %v", err)
	}
}
