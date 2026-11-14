package desktopusage

import (
	"bytes"
	"testing"
)

func TestDependencyDiagnosticKeepsPayloadPrivate(t *testing.T) {
	var destination bytes.Buffer
	filter := &transportDiagnosticFilter{destination: &destination}
	data := []byte("2026/11/14 Unsolicited response received on idle HTTP channel starting with " +
		`"fixture-private-token"; err=<nil>`)
	count, err := filter.Write(data)
	if err != nil || count != len(data) || bytes.Contains(destination.Bytes(), []byte("fixture-private-token")) {
		t.Fatal("dependency diagnostic exposed its payload or broke the writer contract")
	}
	destination.Reset()
	ordinary := []byte("ordinary application diagnostic\n")
	_, err = filter.Write(ordinary)
	if err != nil || !bytes.Equal(destination.Bytes(), ordinary) {
		t.Fatal("ordinary diagnostics changed")
	}
}
