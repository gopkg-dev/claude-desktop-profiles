package desktopusage

import (
	"bytes"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"sync"
	"time"

	"claudeprofiles/httplog"
)

const (
	maxRawRequest  = 128 << 10
	maxRawResponse = maxResponseBytes + (64 << 10)
)

// rawCapture records the HTTP/1.1 plaintext at the origin TLS connection boundary.
// It keeps chunk framing and original headers, and never writes to stdout or stderr.
type rawCapture struct {
	mu            sync.Mutex
	active        *rawExchange
	entries       []*rawExchange
	logger        *httplog.Logger
	proxy         string
	egressAllowed bool
	fingerprint   TLSFingerprint
}

type rawExchange struct {
	metadata httplog.Metadata
	request  *http.Request
	response *http.Response
	sent     []byte
	received []byte
}

func newRawCapture(directory string, proxy *url.URL, fingerprint TLSFingerprint) (*rawCapture, error) {
	if directory == "" {
		return nil, nil
	}
	logger, err := httplog.New(directory)
	if err != nil {
		return nil, ErrHTTPLog
	}
	proxyType := "direct"
	if proxy != nil {
		proxyType = proxy.Scheme
	}
	if fingerprint == "" {
		fingerprint = TLSFingerprintChrome
	}
	return &rawCapture{logger: logger, proxy: proxyType, fingerprint: fingerprint}, nil
}

func (capture *rawCapture) wrap(connection net.Conn) net.Conn {
	return &capturedConnection{Conn: connection, capture: capture}
}

type capturedConnection struct {
	net.Conn
	capture *rawCapture
}

func (connection *capturedConnection) Read(buffer []byte) (int, error) {
	count, err := connection.Conn.Read(buffer)
	connection.capture.append(false, buffer[:count])
	return count, err
}

func (connection *capturedConnection) Write(buffer []byte) (int, error) {
	count, err := connection.Conn.Write(buffer)
	connection.capture.append(true, buffer[:count])
	return count, err
}

func (capture *rawCapture) append(request bool, data []byte) {
	capture.mu.Lock()
	defer capture.mu.Unlock()
	entry := capture.active
	if entry == nil || len(data) == 0 {
		return
	}
	destination, limit := &entry.received, maxRawResponse
	if request {
		destination, limit = &entry.sent, maxRawRequest
	}
	remaining := limit - len(*destination)
	if len(data) > remaining {
		entry.metadata.RawTruncated = true
		data = data[:remaining]
	}
	*destination = append(*destination, data...)
}

type capturingTransport struct {
	base    http.RoundTripper
	capture *rawCapture
}

func (transport *capturingTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	started := time.Now()
	purpose, limit := "usage", maxResponseBytes
	if request.URL.String() == traceEndpoint {
		purpose, limit = "trace", maxTraceBytes
	} else if request.URL.String() == profileEndpoint {
		purpose = "profile"
	}
	entry := &rawExchange{
		request: request.Clone(request.Context()),
		metadata: httplog.Metadata{
			StartedAt: started, Purpose: purpose, ProxyType: transport.capture.proxy,
			TLSFingerprint: string(transport.capture.fingerprint),
		},
	}
	transport.capture.mu.Lock()
	transport.capture.active = entry
	transport.capture.mu.Unlock()
	response, err := transport.base.RoundTrip(request)
	entry.response = response
	var body []byte
	partial := false
	if response != nil && response.Body != nil {
		var readErr error
		body, readErr = io.ReadAll(io.LimitReader(response.Body, int64(limit+1)))
		closeErr := response.Body.Close()
		if readErr != nil || len(body) > limit {
			partial = true
		}
		if err == nil {
			err = errors.Join(readErr, closeErr)
		}
		response.Body = &privateResponseBody{Reader: bytes.NewReader(body), data: body}
	}
	transport.capture.mu.Lock()
	entry.metadata.RawTruncated = entry.metadata.RawTruncated || partial
	entry.metadata.Duration = time.Since(started)
	entry.metadata.Outcome = "success"
	if err != nil {
		entry.metadata.Outcome = "network_error"
		entry.metadata.RawTruncated = true
	} else if response != nil && response.StatusCode != http.StatusOK {
		entry.metadata.Outcome = "http_error"
	}
	transport.capture.active = nil
	transport.capture.entries = append(transport.capture.entries, entry)
	transport.capture.mu.Unlock()
	if err != nil {
		clear(body)
		return nil, err
	}
	return response, nil
}

type privateResponseBody struct {
	*bytes.Reader
	data []byte
}

func (body *privateResponseBody) Close() error {
	clear(body.data)
	body.data = nil
	body.Reader.Reset(nil)
	return nil
}

func (capture *rawCapture) finish(queryErr error) error {
	if capture == nil {
		return nil
	}
	var logErr error
	for _, entry := range capture.entries {
		if entry.metadata.Purpose == "trace" && entry.metadata.Outcome == "success" {
			switch {
			case errors.Is(queryErr, ErrEgressNotAllowed):
				entry.metadata.Outcome = "egress_denied"
			case capture.egressAllowed:
				entry.metadata.Outcome = "egress_allowed"
			default:
				entry.metadata.Outcome = "egress_failed"
			}
		}
		if err := capture.logger.RecordRaw(
			entry.metadata, entry.request, entry.response, entry.sent, entry.received,
		); err != nil {
			logErr = ErrHTTPLog
		}
		clear(entry.sent)
		clear(entry.received)
		entry.request.Header.Del("Authorization")
	}
	return logErr
}
