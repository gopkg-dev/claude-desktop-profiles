package desktopusage

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptrace"
	"net/url"
	"strings"
	"testing"
	"time"

	"claudeprofiles/desktopauth"
)

type testTransport func(*http.Request) (*http.Response, error)

func (f testTransport) RoundTrip(request *http.Request) (*http.Response, error) { return f(request) }

func fixtureClient(transport testTransport) *Client {
	return &Client{
		AppVersion: "0.6.0", LiveDir: "/fixture/Claude",
		EgressPolicy: EgressPolicy{AllowedIPs: []string{"192.0.2.10"}, AllowedCountries: []string{"US"}},
		loadCredential: func(string, string, string, time.Time) (desktopauth.Credential, error) {
			return desktopauth.Credential{AccessToken: "fixture-token", ExpiresAt: time.Now().Add(time.Hour)}, nil
		},
		resolveProxy: func(context.Context, string) (*url.URL, error) { return nil, nil },
		clientHeaders: func(string) (http.Header, error) {
			return http.Header{"User-Agent": {"ClaudeProfiles/0.6.0"}}, nil
		},
		roundTripper: fixtureRoundTripper(transport),
	}
}

type mockConnection struct{}

func (*mockConnection) Read([]byte) (int, error)         { return 0, io.EOF }
func (*mockConnection) Write(data []byte) (int, error)   { return len(data), nil }
func (*mockConnection) Close() error                     { return nil }
func (*mockConnection) LocalAddr() net.Addr              { return &net.TCPAddr{} }
func (*mockConnection) RemoteAddr() net.Addr             { return &net.TCPAddr{} }
func (*mockConnection) SetDeadline(time.Time) error      { return nil }
func (*mockConnection) SetReadDeadline(time.Time) error  { return nil }
func (*mockConnection) SetWriteDeadline(time.Time) error { return nil }

func fixtureRoundTripper(transport testTransport) http.RoundTripper {
	connection := &mockConnection{}
	return testTransport(func(request *http.Request) (*http.Response, error) {
		traceRequest := request.URL.String() == traceEndpoint
		if trace := httptrace.ContextClientTrace(request.Context()); trace != nil && trace.GotConn != nil {
			trace.GotConn(httptrace.GotConnInfo{Conn: connection, Reused: !traceRequest})
		}
		if err := request.Context().Err(); err != nil {
			return nil, err
		}
		if traceRequest {
			if request.Header.Get("Authorization") != "" || request.Header.Get("Cookie") != "" {
				return nil, errors.New("credential on trace request")
			}
			return response(200, "ip=192.0.2.10\nloc=US\n"), nil
		}
		return transport(request)
	})
}

func response(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}
}

func TestQueryOnlyFixedGETAndRemovesAuthorization(t *testing.T) {
	var captured *http.Request
	calls := 0
	client := fixtureClient(func(request *http.Request) (*http.Response, error) {
		calls++
		captured = request
		if request.Method != http.MethodGet || request.URL.String() != endpoint || request.Body != nil {
			t.Fatal("unexpected request route or body")
		}
		if request.Header.Get("Authorization") != "Bearer fixture-token" ||
			request.Header.Get("anthropic-beta") != "oauth-2025-04-20" ||
			request.Header.Get("Accept") != "application/json" || request.Header.Get("Cookie") != "" {
			t.Fatal("incorrect query authentication or headers")
		}
		return response(200, `{"five_hour":{"utilization":42},"seven_day":{"utilization":59}}`), nil
	})
	record, err := client.queryUsage(context.Background(), Target{})
	if err != nil || record == nil || *record.FiveHour != 42 || *record.SevenDay != 59 || calls != 1 {
		t.Fatalf("invalid result or request count: calls=%d error=%v", calls, err)
	}
	if captured.Header.Get("Authorization") != "" {
		t.Fatal("request retained Authorization after completion")
	}
}

func TestRedirectDoesNotFollowOrLeakToken(t *testing.T) {
	calls := 0
	client := fixtureClient(func(request *http.Request) (*http.Response, error) {
		calls++
		result := response(302, "")
		result.Header.Set("Location", "https://untrusted.example/collect")
		return result, nil
	})
	_, err := client.queryUsage(context.Background(), Target{})
	if !errors.Is(err, ErrRedirect) || calls != 1 {
		t.Fatalf("redirect handling failed: calls=%d error=%v", calls, err)
	}
}

func TestQueryDoesNotRetryOrExposeErrors(t *testing.T) {
	calls := 0
	client := fixtureClient(func(request *http.Request) (*http.Response, error) {
		calls++
		return nil, errors.New("secret token or proxy password")
	})
	_, err := client.queryUsage(context.Background(), Target{})
	if !errors.Is(err, ErrNetwork) || calls != 1 || strings.Contains(UserMessage(err), "secret") {
		t.Fatal("transport failure was retried or exposed")
	}
	client.roundTripper = fixtureRoundTripper(func(*http.Request) (*http.Response, error) {
		result := response(429, `{"error":{"message":"secret token echoed"}}`)
		result.Header.Set("Retry-After", "120")
		return result, nil
	})
	now := time.Date(2026, 11, 13, 0, 0, 0, 0, time.UTC)
	client.now = func() time.Time { return now }
	_, err = client.queryUsage(context.Background(), Target{})
	if RetryAt(err) != now.Add(2*time.Minute) || strings.Contains(UserMessage(err), "secret") {
		t.Fatal("cooldown parsing or error filtering failed")
	}
}

func TestProxyFailureAndCancellationNeverReachTransport(t *testing.T) {
	client := fixtureClient(func(*http.Request) (*http.Response, error) {
		t.Fatal("transport must not run")
		return nil, nil
	})
	client.resolveProxy = func(context.Context, string) (*url.URL, error) {
		return nil, errors.New("private proxy details")
	}
	_, err := client.queryUsage(context.Background(), Target{})
	if !errors.Is(err, ErrProxy) {
		t.Fatal("proxy failure did not block request")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	client.loadCredential = func(string, string, string, time.Time) (desktopauth.Credential, error) {
		t.Fatal("cancelled request must not access credential")
		return desktopauth.Credential{}, nil
	}
	_, err = client.queryUsage(ctx, Target{})
	if !errors.Is(err, ErrCanceled) {
		t.Fatal("cancellation was not respected")
	}
}

func TestOversizedResponseRejected(t *testing.T) {
	client := fixtureClient(func(*http.Request) (*http.Response, error) {
		return response(200, strings.Repeat(" ", maxResponseBytes+1)), nil
	})
	_, err := client.queryUsage(context.Background(), Target{})
	if !errors.Is(err, ErrResponse) {
		t.Fatal("oversized response accepted")
	}
}

// queryUsage exercises the shared request path against the plain usage endpoint.
func (c *Client) queryUsage(ctx context.Context, target Target) (*UsageRecord, error) {
	return c.query(ctx, target, endpoint, parseUsage)
}
