package profiles

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

type fakeDriver struct {
	stops      int
	starts     int
	stopError  error
	startError error
}

func (d *fakeDriver) Stop(context.Context) error {
	d.stops++
	return d.stopError
}

func (d *fakeDriver) Start(context.Context) error {
	d.starts++
	return d.startError
}

func putLogin(t *testing.T, dir, account string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(map[string]any{
		"unrelated":            map[string]any{"nested": []any{true, "DO-NOT-RECORD"}},
		"lastKnownAccountUuid": account,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.json"), b, 0o600); err != nil {
		t.Fatal(err)
	}
}

func fixture(t *testing.T) (*Manager, *fakeDriver) {
	t.Helper()
	root := t.TempDir()
	d := &fakeDriver{}
	m := New(Paths{
		LiveDir:  filepath.Join(root, "Application Support", "Claude"),
		StoreDir: filepath.Join(root, "manager"),
	}, d)
	putLogin(t, m.paths.LiveDir, "account-a")
	if err := os.WriteFile(filepath.Join(m.paths.LiveDir, "Cookies"), []byte("A-COOKIE"), 0o600); err != nil {
		t.Fatal(err)
	}
	return m, d
}

func twoProfiles(t *testing.T) (*Manager, *fakeDriver, string, string) {
	t.Helper()
	m, d := fixture(t)
	if err := m.Adopt("个人"); err != nil {
		t.Fatal(err)
	}
	s, err := m.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	a := s.ActiveID
	if err := m.Add(context.Background(), "工作"); err != nil {
		t.Fatal(err)
	}
	putLogin(t, m.paths.LiveDir, "account-b")
	if err := os.WriteFile(filepath.Join(m.paths.LiveDir, "Cookies"), []byte("B-COOKIE"), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err = m.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	return m, d, a, s.ActiveID
}

func readText(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestAdoptKeepsExistingLoginAndNeedsNoCLI(t *testing.T) {
	t.Parallel()
	m, d := fixture(t)
	before := readText(t, filepath.Join(m.paths.LiveDir, "config.json"))
	if err := m.Adopt("个人账号"); err != nil {
		t.Fatal(err)
	}
	s, err := m.Snapshot()
	if err != nil || len(s.Profiles) != 1 || !s.Profiles[0].Active || s.Profiles[0].NeedsLogin {
		t.Fatalf("snapshot=%+v err=%v", s, err)
	}
	if s.CurrentAccount != "account-a" || s.Profiles[0].Label != "个人账号" {
		t.Fatalf("wrong metadata: %+v", s)
	}
	if readText(t, filepath.Join(m.paths.LiveDir, "config.json")) != before || d.stops != 0 || d.starts != 0 {
		t.Fatal("adopting changed login data or app state")
	}
	if strings.Contains(readText(t, m.statePath()), "DO-NOT-RECORD") {
		t.Fatal("unrelated config escaped into metadata")
	}
}

func TestAddStartsBlankProfileAndLearnsLogin(t *testing.T) {
	t.Parallel()
	m, d := fixture(t)
	if err := m.Adopt("A"); err != nil {
		t.Fatal(err)
	}
	if err := m.Add(context.Background(), "B"); err != nil {
		t.Fatal(err)
	}
	s, err := m.Snapshot()
	if err != nil || len(s.Profiles) != 2 || !s.Profiles[1].NeedsLogin || !s.Profiles[1].Active {
		t.Fatalf("snapshot=%+v err=%v", s, err)
	}
	if _, err := os.Stat(filepath.Join(m.paths.LiveDir, "Cookies")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("new profile unexpectedly inherited cookies")
	}
	putLogin(t, m.paths.LiveDir, "new-account")
	s, err = m.Snapshot()
	if err != nil || s.CurrentAccount != "new-account" || s.Profiles[1].NeedsLogin {
		t.Fatalf("snapshot=%+v err=%v", s, err)
	}
	if d.stops != 1 || d.starts != 1 {
		t.Fatalf("driver=%+v", d)
	}
}

func TestSwitchPreservesBothCompleteProfiles(t *testing.T) {
	t.Parallel()
	m, _, a, b := twoProfiles(t)
	if err := m.Switch(context.Background(), a); err != nil {
		t.Fatal(err)
	}
	if readText(t, filepath.Join(m.paths.LiveDir, "Cookies")) != "A-COOKIE" ||
		readText(t, filepath.Join(m.profileDir(b), "Cookies")) != "B-COOKIE" {
		t.Fatal("cookies did not stay with their profile")
	}
	if err := m.Switch(context.Background(), b); err != nil {
		t.Fatal(err)
	}
	if readText(t, filepath.Join(m.paths.LiveDir, "Cookies")) != "B-COOKIE" ||
		readText(t, filepath.Join(m.profileDir(a), "Cookies")) != "A-COOKIE" {
		t.Fatal("return switch lost profile content")
	}
}

func TestStopFailureDoesNotMoveOrAddProfiles(t *testing.T) {
	t.Parallel()
	m, d, a, b := twoProfiles(t)
	d.stopError = errors.New("still running")
	before := readText(t, m.statePath())
	for _, action := range []func() error{
		func() error { return m.Switch(context.Background(), a) },
		func() error { return m.Add(context.Background(), "C") },
	} {
		if err := action(); !errors.Is(err, d.stopError) {
			t.Fatalf("wrong error: %v", err)
		}
		if readText(t, m.statePath()) != before ||
			readText(t, filepath.Join(m.paths.LiveDir, "Cookies")) != "B-COOKIE" {
			t.Fatal("stop failure changed state or login")
		}
	}
	s, err := m.Snapshot()
	if err != nil || s.ActiveID != b || s.PendingRecovery {
		t.Fatalf("snapshot=%+v err=%v", s, err)
	}
}

func TestRecoveryAtEveryDurableStage(t *testing.T) {
	t.Parallel()
	for _, phase := range []string{"journal", "park", "activate", "state"} {
		t.Run(phase, func(t *testing.T) {
			m, d, a, b := twoProfiles(t)
			crash := errors.New("simulated interruption")
			m.step = func(step string) error {
				if step == phase {
					return crash
				}
				return nil
			}
			starts := d.starts
			if err := m.Switch(context.Background(), a); !errors.Is(err, crash) {
				t.Fatalf("switch error=%v", err)
			}
			if d.starts != starts {
				t.Fatal("app restarted before recovery")
			}
			s, err := m.Snapshot()
			if err != nil || !s.PendingRecovery {
				t.Fatalf("pending snapshot=%+v err=%v", s, err)
			}
			if err := m.Switch(context.Background(), b); !errors.Is(err, ErrRecoveryRequired) {
				t.Fatalf("unrecovered switch allowed: %v", err)
			}
			m.step = nil
			if err := m.Recover(context.Background()); err != nil {
				t.Fatal(err)
			}
			s, err = m.Snapshot()
			if err != nil || s.PendingRecovery {
				t.Fatalf("recovered snapshot=%+v err=%v", s, err)
			}
			want, cookie, parkedCookie := a, "A-COOKIE", "B-COOKIE"
			parkedID := b
			if phase == "journal" {
				want, cookie, parkedCookie, parkedID = b, "B-COOKIE", "A-COOKIE", a
			}
			if s.ActiveID != want || readText(t, filepath.Join(m.paths.LiveDir, "Cookies")) != cookie ||
				readText(t, filepath.Join(m.profileDir(parkedID), "Cookies")) != parkedCookie {
				t.Fatal("recovery chose wrong profile or lost content")
			}
		})
	}
}

func TestRestartFailureReportsCommittedAccount(t *testing.T) {
	t.Parallel()
	m, d, a, _ := twoProfiles(t)
	d.startError = errors.New("cannot launch")
	if err := m.Switch(context.Background(), a); !errors.Is(err, d.startError) {
		t.Fatalf("wrong error=%v", err)
	}
	s, err := m.Snapshot()
	if err != nil || s.ActiveID != a || s.PendingRecovery {
		t.Fatalf("snapshot=%+v err=%v", s, err)
	}
}

func TestIdentityMismatchAndDuplicateRefuse(t *testing.T) {
	t.Parallel()
	t.Run("parked", func(t *testing.T) {
		m, d, a, _ := twoProfiles(t)
		putLogin(t, m.profileDir(a), "other-person")
		stops := d.stops
		if err := m.Switch(context.Background(), a); !errors.Is(err, ErrIdentityMismatch) {
			t.Fatalf("error=%v", err)
		}
		if d.stops != stops {
			t.Fatal("invalid target stopped running Desktop")
		}
	})
	t.Run("active", func(t *testing.T) {
		m, _, a, _ := twoProfiles(t)
		putLogin(t, m.paths.LiveDir, "other-person")
		if err := m.Switch(context.Background(), a); !errors.Is(err, ErrIdentityMismatch) {
			t.Fatalf("error=%v", err)
		}
		actual, _ := accountIdentity(m.paths.LiveDir)
		if actual != "other-person" {
			t.Fatal("mismatched login moved")
		}
	})
	t.Run("duplicate-first-login", func(t *testing.T) {
		m, _ := fixture(t)
		if err := m.Adopt("A"); err != nil {
			t.Fatal(err)
		}
		if err := m.Add(context.Background(), "B"); err != nil {
			t.Fatal(err)
		}
		putLogin(t, m.paths.LiveDir, "account-a")
		if _, err := m.Snapshot(); !errors.Is(err, ErrDuplicateAccount) {
			t.Fatalf("error=%v", err)
		}
	})
}

func TestArchivePreservesDataAndRejectsActive(t *testing.T) {
	t.Parallel()
	m, _, a, b := twoProfiles(t)
	if err := m.Archive(b); !errors.Is(err, ErrActiveProfile) {
		t.Fatalf("active archive error=%v", err)
	}
	if err := m.Archive(a); err != nil {
		t.Fatal(err)
	}
	archived := filepath.Join(m.paths.StoreDir, "archives", a)
	if readText(t, filepath.Join(archived, "Cookies")) != "A-COOKIE" {
		t.Fatal("archive did not preserve original data")
	}
	s, err := m.Snapshot()
	if err != nil || len(s.Profiles) != 1 || s.ActiveID != b {
		t.Fatalf("snapshot=%+v err=%v", s, err)
	}
}

func TestArchiveRecoveryAtEveryStage(t *testing.T) {
	t.Parallel()
	for _, phase := range []string{"archive-journal", "archive-move", "archive-state"} {
		t.Run(phase, func(t *testing.T) {
			m, d, a, b := twoProfiles(t)
			crash := errors.New("simulated archive interruption")
			m.step = func(step string) error {
				if step == phase {
					return crash
				}
				return nil
			}
			if err := m.Archive(a); !errors.Is(err, crash) {
				t.Fatalf("error=%v", err)
			}
			s, err := m.Snapshot()
			if err != nil || !s.PendingRecovery {
				t.Fatalf("snapshot=%+v err=%v", s, err)
			}
			m.step = nil
			stops, starts := d.stops, d.starts
			if err := m.Recover(context.Background()); err != nil {
				t.Fatal(err)
			}
			s, err = m.Snapshot()
			if err != nil || s.PendingRecovery || s.ActiveID != b || d.stops != stops || d.starts != starts {
				t.Fatalf("snapshot=%+v err=%v driver=%+v", s, err, d)
			}
			dir := filepath.Join(m.paths.StoreDir, "archives", a)
			if phase == "archive-journal" {
				dir = m.profileDir(a)
				if len(s.Profiles) != 2 {
					t.Fatal("unstarted archive removed profile")
				}
			} else if len(s.Profiles) != 1 {
				t.Fatal("completed archive left profile in registry")
			}
			if readText(t, filepath.Join(dir, "Cookies")) != "A-COOKIE" {
				t.Fatal("archive recovery lost data")
			}
		})
	}
}

func TestUnsafeProfileAndJournalPathsRejected(t *testing.T) {
	t.Parallel()
	t.Run("profile-symlink", func(t *testing.T) {
		m, _, a, _ := twoProfiles(t)
		original := m.profileDir(a) + ".real"
		if err := os.Rename(m.profileDir(a), original); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(original, m.profileDir(a)); err != nil {
			t.Fatal(err)
		}
		if err := m.Switch(context.Background(), a); !errors.Is(err, ErrUnsafePath) {
			t.Fatalf("error=%v", err)
		}
	})
	t.Run("journal-traversal", func(t *testing.T) {
		m, d, _, b := twoProfiles(t)
		if err := writeJSON(m.journalPath(), journal{From: "../../outside", To: b}); err != nil {
			t.Fatal(err)
		}
		stops := d.stops
		if err := m.Recover(context.Background()); !errors.Is(err, ErrInvalidState) {
			t.Fatalf("error=%v", err)
		}
		if d.stops != stops {
			t.Fatal("malicious journal touched Desktop")
		}
	})
	t.Run("store-symlink", func(t *testing.T) {
		m, _ := fixture(t)
		other := t.TempDir()
		if err := os.Symlink(other, m.paths.StoreDir); err != nil {
			t.Fatal(err)
		}
		if err := m.Adopt("A"); !errors.Is(err, ErrUnsafePath) {
			t.Fatalf("error=%v", err)
		}
	})
}

func TestConcurrentMetadataChangesSerialize(t *testing.T) {
	t.Parallel()
	m, _, a, _ := twoProfiles(t)
	var workers sync.WaitGroup
	errorsOut := make(chan error, 20)
	for range 20 {
		workers.Go(func() { errorsOut <- m.Rename(a, "新备注") })
	}
	workers.Wait()
	close(errorsOut)
	for err := range errorsOut {
		if err != nil {
			t.Fatal(err)
		}
	}
	s, err := m.Snapshot()
	if err != nil || len(s.Profiles) != 2 || s.Profiles[0].Label != "新备注" {
		t.Fatalf("snapshot=%+v err=%v", s, err)
	}
}

func TestSnapshotEmptyDesktopIsReadyForInitialLogin(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	m := New(Paths{LiveDir: filepath.Join(root, "missing"), StoreDir: filepath.Join(root, "state")}, &fakeDriver{})
	s, err := m.Snapshot()
	if err != nil || s.CurrentAccount != "" || len(s.Profiles) != 0 {
		t.Fatalf("snapshot=%+v err=%v", s, err)
	}
}

func TestMalformedIdentityDoesNotLeakContent(t *testing.T) {
	t.Parallel()
	m, _ := fixture(t)
	if err := os.WriteFile(filepath.Join(m.paths.LiveDir, "config.json"), []byte("SECRET-CONTENT"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := m.Adopt("A")
	if err == nil || strings.Contains(err.Error(), "SECRET-CONTENT") {
		t.Fatalf("error=%v", err)
	}
}
