package desktopusage

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httptrace"
	"net/netip"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"claudeprofiles/desktopauth"
)

func TestEgressPolicyRequiresExplicitValidRules(t *testing.T) {
	if !errors.Is(validateEgressPolicy(EgressPolicy{}), ErrEgressPolicyMissing) {
		t.Fatal("empty rule allowed")
	}
	for _, policy := range []EgressPolicy{
		{AllowedIPs: []string{""}}, {AllowedIPs: []string{"host.example"}},
		{AllowedIPs: []string{"192.0.2.1/99"}}, {AllowedCountries: []string{"USA"}},
	} {
		if !errors.Is(validateEgressPolicy(policy), ErrEgressPolicyInvalid) {
			t.Fatal("malformed egress policy accepted")
		}
	}
	policy, err := compileEgressPolicy(EgressPolicy{
		AllowedIPs: []string{"192.0.2.0/24", "2001:db8::/32"}, AllowedCountries: []string{"us"},
	})
	if err != nil || !policy.allows(netip.MustParseAddr("192.0.2.10"), "US") ||
		!policy.allows(netip.MustParseAddr("2001:db8::1"), "US") ||
		policy.allows(netip.MustParseAddr("192.0.2.10"), "JP") ||
		policy.allows(netip.MustParseAddr("198.51.100.1"), "US") {
		t.Fatal("IP and country rules did not use AND semantics")
	}
}

func TestTraceStrictParsing(t *testing.T) {
	for _, data := range []string{
		"", "ip=192.0.2.1\n", "loc=US\n", "ip=bad\nloc=US\n", "ip=192.0.2.1\nloc=USA\n",
		"ip=192.0.2.1\nloc=US\nip=192.0.2.2\n", "ip=192.0.2.1\nloc=US\nloc=US\n",
		"ip=192.0.2.1\nloc=US\n\x00evil=yes\n", strings.Repeat("x", maxTraceBytes+1),
	} {
		if _, _, err := parseTrace([]byte(data)); !errors.Is(err, ErrEgressCheck) {
			t.Fatal("malformed trace accepted")
		}
	}
	address, country, err := parseTrace([]byte("fl=123\nip=2001:db8::1\nloc=US\ntls=TLSv1.3\n"))
	if err != nil || address != netip.MustParseAddr("2001:db8::1") || country != "US" {
		t.Fatal("valid trace was not parsed")
	}
}

func TestEgressFailuresNeverReadCredentialOrQueryUsage(t *testing.T) {
	for _, fixture := range []struct {
		name, body string
		status     int
		policy     EgressPolicy
		expected   error
	}{
		{"missing policy", "", 200, EgressPolicy{}, ErrEgressPolicyMissing},
		{"wrong IP", "ip=198.51.100.1\nloc=US\n", 200,
			EgressPolicy{AllowedIPs: []string{"192.0.2.0/24"}}, ErrEgressNotAllowed},
		{"wrong country", "ip=192.0.2.10\nloc=JP\n", 200,
			EgressPolicy{AllowedCountries: []string{"US"}}, ErrEgressNotAllowed},
		{"server error", "", 503, EgressPolicy{AllowedCountries: []string{"US"}}, ErrEgressCheck},
		{"redirect", "", 302, EgressPolicy{AllowedCountries: []string{"US"}}, ErrEgressCheck},
		{"duplicate", "ip=192.0.2.10\nip=192.0.2.10\nloc=US\n", 200,
			EgressPolicy{AllowedCountries: []string{"US"}}, ErrEgressCheck},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			client := fixtureClient(nil)
			client.EgressPolicy = fixture.policy
			client.loadCredential = func(string, string, string, time.Time) (desktopauth.Credential, error) {
				t.Fatal("blocked egress accessed credential")
				return desktopauth.Credential{}, nil
			}
			requests := 0
			client.roundTripper = testTransport(func(request *http.Request) (*http.Response, error) {
				requests++
				if request.URL.String() != traceEndpoint || request.Method != http.MethodGet ||
					request.Header.Get("Authorization") != "" || request.Header.Get("Cookie") != "" {
					t.Fatal("non-trace or credential-bearing request before egress approval")
				}
				return response(fixture.status, fixture.body), nil
			})
			_, err := client.queryUsage(context.Background(), Target{})
			if !errors.Is(err, fixture.expected) {
				t.Fatalf("wrong failure: %v", err)
			}
			if fixture.name == "missing policy" && requests != 0 || requests > 1 {
				t.Fatal("unexpected requests for denied egress")
			}
		})
	}
}

func TestChangedConnectionCancelsBeforeAuthentication(t *testing.T) {
	client := fixtureClient(nil)
	traceConnection, otherConnection := &countedConnection{}, &countedConnection{}
	client.roundTripper = testTransport(func(request *http.Request) (*http.Response, error) {
		trace := httptrace.ContextClientTrace(request.Context())
		if request.URL.String() == traceEndpoint {
			trace.GotConn(httptrace.GotConnInfo{Conn: traceConnection})
			return response(200, "ip=192.0.2.10\nloc=US\n"), nil
		}
		trace.GotConn(httptrace.GotConnInfo{Conn: otherConnection, Reused: true})
		if !otherConnection.closed.Load() || request.Context().Err() == nil {
			t.Fatal("different connection was not closed before credential transmission")
		}
		return nil, context.Cause(request.Context())
	})
	_, err := client.queryUsage(context.Background(), Target{})
	if !errors.Is(err, ErrEgressConnectionChanged) {
		t.Fatalf("connection change accepted: %v", err)
	}
}

type countedConnection struct {
	mockConnection
	closed atomic.Bool
}

func (c *countedConnection) Close() error { c.closed.Store(true); return nil }

func TestLoopbackTLSReusesCheckedConnectionAndBlocksRedial(t *testing.T) {
	for _, closeAfterTrace := range []bool{false, true} {
		t.Run(map[bool]string{false: "same connection", true: "closed connection"}[closeAfterTrace], func(t *testing.T) {
			var requests, authenticated, dials atomic.Int32
			server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				requests.Add(1)
				if request.URL.Path == "/cdn-cgi/trace" {
					if request.Header.Get("Authorization") != "" {
						t.Error("trace carried credentials")
					}
					if closeAfterTrace {
						writer.Header().Set("Connection", "close")
					}
					_, _ = writer.Write([]byte("ip=192.0.2.10\nloc=US\n"))
					return
				}
				if request.URL.Path != "/api/oauth/usage" || request.Header.Get("Authorization") != "Bearer fixture-token" {
					t.Error("unexpected authenticated request")
				}
				authenticated.Add(1)
				_, _ = writer.Write([]byte(`{"five_hour":{"utilization":42},"seven_day":{"utilization":59}}`))
			}))
			defer server.Close()
			client := fixtureClient(nil)
			client.roundTripper = nil
			client.dialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
				dials.Add(1)
				if address != "api.anthropic.com:443" {
					t.Error("unexpected connection destination")
				}
				return (&net.Dialer{}).DialContext(ctx, "tcp", server.Listener.Addr().String())
			}
			roots := x509.NewCertPool()
			roots.AddCert(server.Certificate())
			client.tlsConfig = &tls.Config{RootCAs: roots, ServerName: server.Certificate().DNSNames[0], MinVersion: tls.VersionTLS12}
			_, err := client.queryUsage(context.Background(), Target{})
			if closeAfterTrace {
				if !errors.Is(err, ErrEgressConnectionChanged) || authenticated.Load() != 0 || requests.Load() != 1 {
					t.Fatalf("redial was not blocked: requests=%d auth=%d error=%v", requests.Load(), authenticated.Load(), err)
				}
			} else if err != nil || authenticated.Load() != 1 || requests.Load() != 2 {
				t.Fatalf("checked sequence failed: requests=%d auth=%d error=%v", requests.Load(), authenticated.Load(), err)
			}
			if dials.Load() != 1 {
				t.Fatal("transport established a new connection after egress approval")
			}
		})
	}
}

func TestInitializedHTTP2DefaultsCannotOverridePrivateConnectionGate(t *testing.T) {
	var requests, gateDials atomic.Int32
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		if request.ProtoMajor != 1 || request.TLS == nil || request.TLS.NegotiatedProtocol != "http/1.1" {
			t.Error("private quota sequence negotiated a different HTTP protocol")
		}
		if request.URL.Path == "/cdn-cgi/trace" {
			_, _ = writer.Write([]byte("ip=192.0.2.10\nloc=US\n"))
			return
		}
		_, _ = writer.Write([]byte(`{"five_hour":{"utilization":42},"seven_day":{"utilization":59}}`))
	}))
	server.EnableHTTP2 = true
	server.TLS = &tls.Config{MinVersion: tls.VersionTLS12, NextProtos: []string{"h2", "http/1.1"}}
	server.StartTLS()
	defer server.Close()

	// Simulate an application whose global transport has already initialized HTTP/2 and custom TLS hooks.
	previous := http.DefaultTransport
	polluted := previous.(*http.Transport).Clone()
	polluted.TLSClientConfig = &tls.Config{NextProtos: []string{"h2", "http/1.1"}}
	polluted.DialTLSContext = func(context.Context, string, string) (net.Conn, error) {
		t.Error("global TLS dial hook bypassed the private connection gate")
		return nil, ErrEgressConnectionChanged
	}
	//lint:ignore SA1019 the deprecated hook must also be bypassed
	polluted.DialTLS = func(string, string) (net.Conn, error) {
		t.Error("global legacy TLS dial hook bypassed the private connection gate")
		return nil, ErrEgressConnectionChanged
	}
	http.DefaultTransport = polluted
	t.Cleanup(func() { http.DefaultTransport = previous; polluted.CloseIdleConnections() })

	client := fixtureClient(nil)
	client.roundTripper = nil
	client.dialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		gateDials.Add(1)
		return (&net.Dialer{}).DialContext(ctx, "tcp", server.Listener.Addr().String())
	}
	roots := x509.NewCertPool()
	roots.AddCert(server.Certificate())
	client.tlsConfig = &tls.Config{
		RootCAs: roots, ServerName: server.Certificate().DNSNames[0],
		MinVersion: tls.VersionTLS12, NextProtos: []string{"h2", "http/1.1"},
	}
	if _, err := client.queryUsage(context.Background(), Target{}); err != nil {
		t.Fatalf("HTTP/2 defaults broke the checked HTTP/1.1 sequence: %v", err)
	}
	if requests.Load() != 2 || gateDials.Load() != 1 {
		t.Fatalf("invalid checked sequence: requests=%d connections=%d", requests.Load(), gateDials.Load())
	}
}

func validateEgressPolicy(policy EgressPolicy) error {
	_, err := compileEgressPolicy(policy)
	return err
}
