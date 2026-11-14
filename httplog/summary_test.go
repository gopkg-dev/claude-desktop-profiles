package httplog

import (
	"path/filepath"
	"testing"
	"time"
)

func TestSummarizeAndClear(t *testing.T) {
	logger, directory := testLogger(t)
	missing, err := Summarize(filepath.Join(filepath.Dir(directory), "missing"))
	if err != nil || missing.Exchanges != 0 || missing.MaxExchanges != defaultMaxExchanges {
		t.Fatalf("missing directory summary = %+v, %v", missing, err)
	}
	before := time.Now().UTC().Add(-time.Second)
	for range 2 {
		request, response := sampleExchange(t)
		if err := logger.Record(Metadata{Outcome: "success"}, request, response, []byte("{}")); err != nil {
			t.Fatal(err)
		}
	}
	summary, err := Summarize(directory)
	if err != nil || summary.Exchanges != 2 || summary.Bytes <= 0 || summary.Latest.Before(before) {
		t.Fatalf("summary = %+v, %v", summary, err)
	}
	if err := Clear(directory); err != nil {
		t.Fatal(err)
	}
	if names := exchanges(t, directory); len(names) != 0 {
		t.Fatalf("clear left %d exchanges", len(names))
	}
	if summary, err := Summarize(directory); err != nil || summary.Exchanges != 0 || summary.Bytes != 0 {
		t.Fatalf("summary after clear = %+v, %v", summary, err)
	}
}
