package desktopusage

import (
	"testing"
	"time"
)

func TestParseQuotaWindowsAndModels(t *testing.T) {
	stamp := time.Date(2026, 11, 13, 15, 30, 0, 0, time.UTC)
	record, err := parseUsage([]byte(`{
		"five_hour":{"utilization":42,"resets_at":"2026-11-13T17:50:00Z"},
		"seven_day":{"utilization":59,"resets_at":1794848400},
		"seven_day_sonnet":null,
		"limits":[{"kind":"weekly_scoped","percent":0,"resets_at":"2026-11-16T17:00:00Z",
		"scope":{"model":{"display_name":"Fable"}}}]
	}`), stamp)
	if err != nil || record == nil || record.FiveHourReset == nil || record.SevenDayReset == nil ||
		*record.FiveHour != 42 || *record.SevenDay != 59 || len(record.Scoped) != 1 ||
		record.Scoped[0].Name != "Fable" || *record.Scoped[0].Percent != 0 || record.UpdatedAt != stamp ||
		record.ResetGrantsKnown {
		t.Fatalf("unexpected parsed quota: %v", err)
	}
}

func TestNullStaysUnknownAndMalformedNumbersFail(t *testing.T) {
	record, err := parseUsage([]byte(`{"five_hour":null,"seven_day":{"utilization":0}}`), time.Now())
	if err != nil || record.FiveHour != nil || record.SevenDay == nil || *record.SevenDay != 0 {
		t.Fatal("unknown and zero were conflated")
	}
	for _, body := range []string{`{}`, `null`, `{"five_hour":{"utilization":true}}`,
		`{"five_hour":{"utilization":-1}}`, `{"five_hour":{"utilization":"42"}}`,
		`{"limits":[{"kind":"session","percent":4},{"kind":"session","percent":5}]}`} {
		if _, err := parseUsage([]byte(body), time.Now()); err == nil {
			t.Fatalf("invalid quota accepted: %s", body)
		}
	}
}
