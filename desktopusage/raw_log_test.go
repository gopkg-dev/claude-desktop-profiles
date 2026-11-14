package desktopusage

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestRawLogsPreserveHTTPBytesAndGate(t *testing.T) {
	for _, country := range []string{"US", "CN"} {
		t.Run(country, func(t *testing.T) {
			var authenticated atomic.Int32
			traceBody := "ip=192.0.2.20\nloc=" + country + "\n"
			traceRaw := chunkedFixture(traceBody)
			usageRaw := chunkedFixture(`{"five_hour":{"utilization":42},"seven_day":{"utilization":59}}`)
			server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				if request.URL.Path != "/cdn-cgi/trace" || request.Header.Get("Authorization") != "" {
					t.Error("expected unauthenticated trace first")
					return
				}
				connection, stream, err := writer.(http.Hijacker).Hijack()
				if err != nil {
					t.Error("fixture hijack failed")
					return
				}
				defer connection.Close()
				_ = connection.SetDeadline(time.Now().Add(5 * time.Second))
				_, _ = stream.WriteString(traceRaw)
				_ = stream.Flush()
				next, err := http.ReadRequest(stream.Reader)
				if err != nil {
					return
				}
				defer next.Body.Close()
				if next.URL.Path != "/api/oauth/usage" || next.Header.Get("Authorization") != "Bearer fixture-token" {
					t.Error("unexpected authenticated fixture request")
					return
				}
				authenticated.Add(1)
				_, _ = stream.WriteString(usageRaw)
				_ = stream.Flush()
			}))
			defer server.Close()
			client := fixtureClient(nil)
			client.EgressPolicy = EgressPolicy{AllowedCountries: []string{"US"}}
			client.roundTripper = nil
			client.HTTPLogDirectory, _ = filepath.EvalSymlinks(t.TempDir())
			client.dialContext = func(ctx context.Context, _, _ string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, "tcp", server.Listener.Addr().String())
			}
			roots := x509.NewCertPool()
			roots.AddCert(server.Certificate())
			client.tlsConfig = &tls.Config{RootCAs: roots, ServerName: server.Certificate().DNSNames[0]}
			_, queryErr := client.queryUsage(context.Background(), Target{})
			expected := 2
			if country == "CN" {
				expected = 1
				if queryErr != ErrEgressNotAllowed || authenticated.Load() != 0 {
					t.Fatal("denied region queried authenticated quota")
				}
			} else if queryErr != nil || authenticated.Load() != 1 {
				t.Fatalf("logged query failed: %v", queryErr)
			}
			entries, err := os.ReadDir(client.HTTPLogDirectory)
			if err != nil {
				t.Fatal("cannot inspect fixture logs")
			}
			count := 0
			for _, entry := range entries {
				if !entry.IsDir() {
					continue
				}
				count++
				directory := filepath.Join(client.HTTPLogDirectory, entry.Name())
				requestBytes, _ := os.ReadFile(filepath.Join(directory, "request.raw"))
				responseBytes, _ := os.ReadFile(filepath.Join(directory, "response.raw"))
				metadataBytes, _ := os.ReadFile(filepath.Join(directory, "metadata.json"))
				var metadata struct {
					Purpose   string `json:"purpose"`
					Outcome   string `json:"outcome"`
					Truncated bool   `json:"truncated"`
				}
				if json.Unmarshal(metadataBytes, &metadata) != nil || metadata.Truncated {
					t.Fatal("complete exchange marked invalid or truncated")
				}
				if metadata.Purpose == "trace" {
					if bytes.Contains(requestBytes, []byte("Authorization:")) || string(responseBytes) != traceRaw {
						t.Fatal("trace credentials or raw response bytes changed")
					}
					outcome := "egress_allowed"
					if country == "CN" {
						outcome = "egress_denied"
					}
					if metadata.Outcome != outcome {
						t.Fatal("incorrect logged egress decision")
					}
					continue
				}
				if !bytes.Contains(requestBytes, []byte("Authorization: Bearer fixture-token\r\n")) ||
					!bytes.Contains(requestBytes, []byte("GET /api/oauth/usage HTTP/1.1\r\n")) ||
					string(responseBytes) != usageRaw {
					t.Fatal("unredacted request or chunked response bytes changed")
				}
			}
			if count != expected {
				t.Fatalf("expected %d exchange logs, got %d", expected, count)
			}
		})
	}
}

func chunkedFixture(body string) string {
	return "HTTP/1.1 200 OK\r\nx-MiXeD: first\r\nx-MiXeD: second\r\n" +
		"Transfer-Encoding: chunked\r\nTrailer: X-End\r\n\r\n" +
		fmt.Sprintf("%x\r\n%s\r\n0\r\nX-End: complete\r\n\r\n", len(body), body)
}

func TestRawCaptureBoundsMemory(t *testing.T) {
	entry := &rawExchange{}
	capture := &rawCapture{active: entry}
	capture.append(true, []byte(strings.Repeat("x", maxRawRequest+1)))
	capture.append(false, []byte(strings.Repeat("x", maxRawResponse+1)))
	if len(entry.sent) != maxRawRequest || len(entry.received) != maxRawResponse || !entry.metadata.RawTruncated {
		t.Fatal("raw capture exceeded bounded memory or omitted truncation status")
	}
}

func TestCaptureReadsErrorResponseBody(t *testing.T) {
	capture := &rawCapture{}
	transport := &capturingTransport{capture: capture, base: testTransport(func(request *http.Request) (*http.Response, error) {
		capture.append(false, []byte("HTTP/1.1 403 Forbidden\r\nContent-Length: 6\r\n\r\ndenied"))
		return response(403, "denied"), nil
	})}
	request, _ := http.NewRequest(http.MethodGet, endpoint, nil)
	reply, err := transport.RoundTrip(request)
	if err != nil {
		t.Fatal("failed to preserve HTTP error response")
	}
	defer reply.Body.Close()
	body, _ := io.ReadAll(reply.Body)
	if string(body) != "denied" || len(capture.entries) != 1 ||
		capture.entries[0].metadata.Outcome != "http_error" || !bytes.HasSuffix(capture.entries[0].received, body) {
		t.Fatal("error body was lost from caller or raw log")
	}
}

func TestQueryAccountWithRawLoggingRetainsProfileAndUsage(t *testing.T) {
	var authenticated atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/cdn-cgi/trace" {
			if request.Header.Get("Authorization") != "" {
				t.Error("trace request included account credentials")
			}
			_, _ = io.WriteString(writer, "ip=192.0.2.10\nloc=US\n")
			return
		}
		if request.Method != http.MethodGet || request.Header.Get("Authorization") != "Bearer fixture-token" {
			t.Error("unexpected account request")
			writer.WriteHeader(http.StatusUnauthorized)
			return
		}
		authenticated.Add(1)
		writer.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/api/oauth/profile":
			_, _ = io.WriteString(writer, accountFixture)
		case "/api/oauth/usage":
			if request.URL.RawQuery != "cedar_ember=1" ||
				!strings.HasPrefix(request.Header.Get("User-Agent"), "claude-cli/") {
				t.Error("reset status read missing its query or client surface")
			}
			_, _ = io.WriteString(writer, `{"five_hour":{"utilization":42},"seven_day":{"utilization":59},
				"cedar_ember":{"eligible":true,"grants":[{"resets_left":2,"ends_at":"2026-12-01T00:00:00Z"}]}}`)
		default:
			t.Error("unexpected request path")
			writer.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	client := fixtureClient(nil)
	client.roundTripper = nil
	var err error
	client.HTTPLogDirectory, err = filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	client.dialContext = func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "tcp", server.Listener.Addr().String())
	}
	roots := x509.NewCertPool()
	roots.AddCert(server.Certificate())
	client.tlsConfig = &tls.Config{RootCAs: roots, ServerName: server.Certificate().DNSNames[0]}
	data, err := client.QueryAccount(context.Background(), Target{AccountID: "aaaaaaaa-1111-2222-3333-bbbbbbbbbbbb"})
	if err != nil {
		t.Fatalf("valid account response was rejected with RAW logging enabled: %v", err)
	}
	if data == nil || data.Email != "online@example.com" || data.Usage == nil ||
		data.Usage.FiveHour == nil || *data.Usage.FiveHour != 42 || authenticated.Load() != 2 ||
		!data.Usage.ResetGrantsKnown || len(data.Usage.ResetGrants) != 1 || data.Usage.ResetGrants[0].Remaining != 2 {
		t.Fatal("account profile or usage was not returned")
	}
	entries, err := os.ReadDir(client.HTTPLogDirectory)
	if err != nil {
		t.Fatal(err)
	}
	purposes := map[string]int{}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		directory := filepath.Join(client.HTTPLogDirectory, entry.Name())
		metadataBytes, err := os.ReadFile(filepath.Join(directory, "metadata.json"))
		if err != nil {
			t.Fatal(err)
		}
		var metadata struct {
			Purpose    string `json:"purpose"`
			StatusCode int    `json:"status_code"`
		}
		if err := json.Unmarshal(metadataBytes, &metadata); err != nil {
			t.Fatal(err)
		}
		if metadata.StatusCode != http.StatusOK {
			t.Fatal("successful exchange was not logged")
		}
		purposes[metadata.Purpose]++
		if metadata.Purpose == "profile" {
			raw, err := os.ReadFile(filepath.Join(directory, "response.raw"))
			if err != nil || !bytes.Contains(raw, []byte(accountFixture)) {
				t.Fatal("account profile response was not preserved in the RAW log")
			}
		}
	}
	if purposes["trace"] != 2 || purposes["profile"] != 1 || purposes["usage"] != 1 || len(purposes) != 3 {
		t.Fatal("expected two trace checks and both account endpoint logs", purposes)
	}
}
