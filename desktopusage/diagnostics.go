package desktopusage

import (
	"bytes"
	"io"
	"log"
	"sync"
)

var diagnosticFilterOnce sync.Once

// InstallPrivateHTTPDiagnostics keeps req's unsolicited-response payloads out of process logs.
// Call once at application startup; ordinary application diagnostics retain their existing destination.
func InstallPrivateHTTPDiagnostics() {
	diagnosticFilterOnce.Do(func() {
		log.SetOutput(&transportDiagnosticFilter{destination: log.Writer()})
	})
}

type transportDiagnosticFilter struct {
	destination io.Writer
}

func (filter *transportDiagnosticFilter) Write(data []byte) (int, error) {
	// req v3.61.0 emits this one diagnostic through the standard logger, independently of Debugf.
	if bytes.Contains(data, []byte("Unsolicited response received on idle HTTP channel starting with ")) {
		_, err := filter.destination.Write([]byte("HTTP transport: unexpected data on an idle connection\n"))
		if err != nil {
			return 0, err
		}
		return len(data), nil
	}
	return filter.destination.Write(data)
}
