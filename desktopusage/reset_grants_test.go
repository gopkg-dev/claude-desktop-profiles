package desktopusage

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestUsageIncludesReturnedResetCountsAndDates(t *testing.T) {
	at := time.Date(2026, 11, 14, 1, 0, 0, 0, time.UTC)
	record, err := parseUsage([]byte(`{
		"five_hour":{"utilization":42},
		"cedar_ember":{"eligible":true,"grants":[
		{"resets_left":2,"ends_at":"2026-12-01T00:00:00Z","paused":true},
		{"resets_left":3,"ends_at":"2026-11-01T00:00:00Z"},
		{"resets_left":0,"ends_at":null}]}}
	`), at)
	if err != nil || !record.ResetGrantsKnown || len(record.ResetGrants) != 3 ||
		record.ResetGrants[0].Remaining != 2 || record.ResetGrants[1].Remaining != 3 ||
		record.ResetGrants[2].Remaining != 0 || !record.ResetGrants[2].ExpiresAt.IsZero() ||
		!record.ResetGrantsAt.Equal(at) {
		t.Fatal("returned reset data missing or changed", err)
	}
	if record.ResetGrants[0].ExpiresAt.Format(time.RFC3339) != "2026-12-01T00:00:00Z" ||
		record.ResetGrants[1].ExpiresAt.Format(time.RFC3339) != "2026-11-01T00:00:00Z" {
		t.Fatal("returned expiry dates changed")
	}
}

func TestResetGrantKindsFollowWhatTheyClear(t *testing.T) {
	record, err := parseUsage([]byte(`{
		"limits":[{"kind":"weekly_scoped","percent":3,"scope":{"model":{"display_name":"Fable"},"surface":null}}],
		"cedar_ember":{"eligible":true,"grants":[
		{"id":"launch-fable-week","label":"One Fable reset, launch week only","type":"full","resets_left":1,
			"clears":["five_hour","seven_day_overage_included"],"paused":true},
		{"id":"full-reset","label":"One full limit reset","type":"full","resets_left":1,
			"clears":["five_hour","seven_day","seven_day_overage_included"]},
		{"id":"other","type":"full","resets_left":1,"clears":["five_hour"]},
		{"resets_left":1}]}}`), time.Now())
	if err != nil || len(record.ResetGrants) != 4 {
		t.Fatal("grants missing", err)
	}
	for i, want := range []ResetGrant{{Kind: ResetModel, Model: "Fable", Paused: true}, {Kind: ResetFull},
		{Kind: ResetModel}, {}} {
		got := record.ResetGrants[i]
		if got.Kind != want.Kind || got.Model != want.Model || got.Paused != want.Paused {
			t.Errorf("grant %d = %+v, want kind %q model %q paused %v", i, got, want.Kind, want.Model, want.Paused)
		}
	}
}

func TestMissingOrInvalidResetsDoNotBecomeZeroOrDiscardUsage(t *testing.T) {
	for _, cedar := range []string{
		`null`, `{}`, `{"eligible":false}`, `{"grants":null}`,
		`{"eligible":false,"ineligible_reason":"surface","grants":[]}`,
		`{"grants":[{"ends_at":"2026-12-01T00:00:00Z"}]}`,
		`{"grants":[{"resets_left":-1}]}`, `{"grants":[{"resets_left":1.5}]}`,
		`{"grants":[{"resets_left":true}]}`, `{"grants":{}}`,
	} {
		record, err := parseUsage([]byte(`{"five_hour":{"utilization":42},"cedar_ember":`+cedar+`}`), time.Now())
		if err != nil || record.FiveHour == nil || *record.FiveHour != 42 || record.ResetGrantsKnown ||
			!record.ResetGrantsAt.IsZero() {
			t.Fatal("unknown resets became zero or hid valid quota", cedar, err)
		}
	}
	record, err := parseUsage([]byte(`{"five_hour":null,"cedar_ember":{"grants":[]}}`), time.Now())
	if err != nil || !record.ResetGrantsKnown || len(record.ResetGrants) != 0 {
		t.Fatal("explicitly empty grants were not recognized", err)
	}
}

func TestInstalledCodeVersionIsReadWithoutLaunchingCLI(t *testing.T) {
	directory := t.TempDir()
	versions := filepath.Join(directory, "2.1.311")
	if err := os.Mkdir(versions, 0o700); err != nil {
		t.Fatal(err)
	}
	binaryPath := filepath.Join(versions, "claude")
	if err := os.WriteFile(binaryPath, []byte("invalid executable; must never be run"), 0o700); err != nil {
		t.Fatal(err)
	}
	binDir := filepath.Join(directory, "bin")
	if err := os.Mkdir(binDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(binaryPath, filepath.Join(binDir, "claude")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir)
	if got := installedCodeVersion(); got != "2.1.311" {
		t.Fatal("incorrect installed version", got)
	}
}
