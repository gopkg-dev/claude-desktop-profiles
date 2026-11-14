package profiles

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSnapshotReadsDetailsInCorrectProfileAndNeverPersistsThem(t *testing.T) {
	t.Parallel()
	m, _, a, b := twoProfiles(t)
	seen := map[string]string{}
	m.detailsReader = func(dir, expected string, now time.Time) (*AccountDetails, string) {
		seen[dir] = expected
		return &AccountDetails{Email: expected + "@example.invalid", DisplayName: "本地昵称", Plan: "Pro", UpdatedAt: now},
			DetailsAvailable
	}
	before := readText(t, m.statePath())
	s, err := m.Snapshot()
	if err != nil || s.ActiveID != b || len(s.Profiles) != 2 {
		t.Fatalf("snapshot=%+v err=%v", s, err)
	}
	if seen[m.profileDir(a)] != "account-a" || seen[m.paths.LiveDir] != "account-b" {
		t.Fatalf("wrong directory or identity selection: %+v", seen)
	}
	if s.Profiles[0].Label != "个人" || s.Profiles[1].Label != "工作" ||
		s.Profiles[0].DetailsStatus != DetailsAvailable || s.Profiles[0].Details.Email != "account-a@example.invalid" {
		t.Fatalf("details affected labels or account attribution: %+v", s.Profiles)
	}
	if readText(t, m.statePath()) != before {
		t.Fatal("details lookup rewrote manifest")
	}
	if err := m.Rename(a, "手动修改"); err != nil {
		t.Fatal(err)
	}
	if err := m.Archive(a); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{m.statePath(), filepath.Join(m.paths.StoreDir, "archives", a+".json")} {
		stored := strings.ToLower(readText(t, path))
		if strings.Contains(stored, "details") || strings.Contains(stored, "@") || strings.Contains(stored, "本地昵称") {
			t.Fatalf("account details leaked into state: %s", path)
		}
	}
}

func TestDetailsFailureDoesNotBlockAccountSwitching(t *testing.T) {
	t.Parallel()
	m, _, a, _ := twoProfiles(t)
	m.detailsReader = func(string, string, time.Time) (*AccountDetails, string) {
		return nil, DetailsMismatch
	}
	s, err := m.Snapshot()
	if err != nil || s.Profiles[0].DetailsStatus != DetailsMismatch {
		t.Fatalf("snapshot=%+v err=%v", s, err)
	}
	if err := m.Switch(context.Background(), a); err != nil {
		t.Fatalf("details blocked switch: %v", err)
	}
}

func TestDetailsNeverSerializeInProfile(t *testing.T) {
	t.Parallel()
	p := Profile{ID: "test", Label: "手动备注", DetailsStatus: DetailsAvailable,
		Details: &AccountDetails{Email: "person@example.invalid", Plan: "Pro", UpdatedAt: time.Now()}}
	encoded, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "example.invalid") ||
		strings.Contains(strings.ToLower(string(encoded)), "details") {
		t.Fatal("details unexpectedly serialized with profile metadata")
	}
}

func TestDetailsRevalidatesLogin(t *testing.T) {
	t.Parallel()
	m, _ := fixture(t)
	details, status := &AccountDetails{Email: "person@example.invalid"}, DetailsAvailable
	verified, status := verifyDetailsIdentity(m.paths.LiveDir, "account-a", details, status)
	if verified != details || status != DetailsAvailable {
		t.Fatalf("unchanged login status=%s", status)
	}
	putLogin(t, m.paths.LiveDir, "account-b")
	verified, status = verifyDetailsIdentity(m.paths.LiveDir, "account-a", details, DetailsAvailable)
	if verified != nil || status != DetailsMismatch {
		t.Fatalf("changed login status=%s", status)
	}
	if err := os.Remove(filepath.Join(m.paths.LiveDir, "config.json")); err != nil {
		t.Fatal(err)
	}
	verified, status = verifyDetailsIdentity(m.paths.LiveDir, "account-a", details, DetailsAvailable)
	if verified != nil || status != DetailsMismatch {
		t.Fatalf("signed-out login status=%s", status)
	}
	configPath := filepath.Join(m.paths.LiveDir, "config.json")
	if err := os.WriteFile(configPath, []byte("MALFORMED-PRIVATE-DATA"), 0o600); err != nil {
		t.Fatal(err)
	}
	verified, status = verifyDetailsIdentity(m.paths.LiveDir, "account-a", details, DetailsAvailable)
	if verified != nil || status != DetailsUnavailable {
		t.Fatalf("unreadable login status=%s", status)
	}
	verified, status = verifyDetailsIdentity(m.paths.LiveDir, "account-a", nil, DetailsMissing)
	if verified != nil || status != DetailsMissing {
		t.Fatalf("missing cache should keep its original status=%s", status)
	}
}
