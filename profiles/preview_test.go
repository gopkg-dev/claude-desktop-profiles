package profiles

import (
	"testing"
	"time"
)

func TestPreviewShowsVerifiedDetailsWithoutUsageOrRegistryWrites(t *testing.T) {
	t.Parallel()
	m, d, _, activeID := twoProfiles(t)
	before := readText(t, m.statePath())
	m.detailsReader = func(_ string, expected string, _ time.Time) (*AccountDetails, string) {
		if expected == "" {
			t.Fatal("unverified identity passed to detail reader")
		}
		return &AccountDetails{Email: "fixture@example.com", Plan: "Pro", OrganizationID: usageOrgA}, DetailsAvailable
	}
	result, err := m.PreviewSnapshot()
	if err != nil || !result.Preview || result.ActiveID != activeID || len(result.Profiles) != 2 {
		t.Fatalf("invalid preview: %+v %v", result, err)
	}
	for _, profile := range result.Profiles {
		if profile.Details == nil || profile.Details.Email != "fixture@example.com" ||
			profile.DetailsStatus != DetailsAvailable || profile.Usage != nil {
			t.Fatalf("missing details or unexpected usage: %+v", profile)
		}
	}
	if readText(t, m.statePath()) != before || d.stops != 1 || d.starts != 1 {
		t.Fatal("preview changed the account registry or Desktop")
	}
}

func TestPreviewDiscardsMismatchedIdentityAndDoesNotReconcile(t *testing.T) {
	t.Parallel()
	m, _, _, activeID := twoProfiles(t)
	before := readText(t, m.statePath())
	m.detailsReader = func(dir, _ string, _ time.Time) (*AccountDetails, string) {
		if dir == m.paths.LiveDir {
			putLogin(t, dir, "changed-account")
		}
		return &AccountDetails{Email: "fixture@example.com"}, DetailsAvailable
	}
	result, err := m.PreviewSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	for _, profile := range result.Profiles {
		if profile.ID == activeID && (profile.Details != nil || profile.DetailsStatus != DetailsMismatch) {
			t.Fatalf("stale account details exposed: %+v", profile)
		}
	}
	if readText(t, m.statePath()) != before {
		t.Fatal("preview reconciled changed account")
	}
}

func TestPreviewPendingRecoveryDoesNotReadDetails(t *testing.T) {
	t.Parallel()
	m, _, a, b := twoProfiles(t)
	if err := writeJSON(m.journalPath(), journal{From: b, To: a}); err != nil {
		t.Fatal(err)
	}
	m.detailsReader = func(string, string, time.Time) (*AccountDetails, string) {
		t.Fatal("preview read details during recovery")
		return nil, ""
	}
	result, err := m.PreviewSnapshot()
	if err != nil || !result.Preview || !result.PendingRecovery {
		t.Fatalf("invalid recovery preview: %+v %v", result, err)
	}
	for _, profile := range result.Profiles {
		if profile.Details != nil || profile.DetailsStatus != DetailsPendingRecovery {
			t.Fatal("pending details exposed")
		}
	}
}
