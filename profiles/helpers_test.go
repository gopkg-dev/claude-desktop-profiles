package profiles

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

const (
	usageOrgA = "aaaaaaaa-1111-1111-1111-111111111111"
	usageOrgB = "bbbbbbbb-2222-2222-2222-222222222222"
)

// desktopUsageHistory is Desktop's own business cache; this app must never read it.
const desktopUsageHistory = "plan-usage-history.json"

func usageNumber(value float64) *float64 { return &value }

func putUsage(t *testing.T, dir, data string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, desktopUsageHistory), []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
}

func usageDocument(t *testing.T, version int, samples ...map[string]any) string {
	t.Helper()
	b, err := json.Marshal(map[string]any{"version": version, "samples": samples})
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func usageRecord(at time.Time, org any, fiveHour, sevenDay any) map[string]any {
	return map[string]any{
		"t": at.UnixMilli(), "org": org,
		"u": map[string]any{"fh": fiveHour, "sd": sevenDay},
	}
}

func assertPercent(t *testing.T, got *float64, want float64) {
	t.Helper()
	if got == nil || *got != want {
		t.Fatalf("got %v, want %v", got, want)
	}
}
