package desktopusage

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

const exitGeoFixture = `{"success":true,"ip":"192.0.2.10","country":"United States",` +
	`"country_code":"US","region":"Washington","city":"Seattle","connection":{"asn":64500,"isp":"Example"}}`

func exitFixtureClient(t *testing.T, transport testTransport, now func() time.Time) *ExitClient {
	t.Helper()
	return &ExitClient{LiveDir: "/fixture/Claude", now: now,
		clientFactory: func(_ context.Context, liveDir string) (*http.Client, *url.URL, error) {
			if liveDir != "/fixture/Claude" {
				t.Fatal("exit query did not use the Desktop proxy directory")
			}
			return &http.Client{Transport: testTransport(func(request *http.Request) (*http.Response, error) {
				if request.Method != http.MethodGet || request.Body != nil ||
					request.Header.Get("Authorization") != "" || request.Header.Get("Cookie") != "" {
					t.Fatal("exit query sent credentials or changed method")
				}
				return transport(request)
			}), CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, nil, nil
		}}
}

func TestExitQueryCachesGeoForSameIPAndRechecksChangedIP(t *testing.T) {
	now := time.Now()
	address := "192.0.2.10"
	var traceCalls, geoCalls int
	client := exitFixtureClient(t, func(request *http.Request) (*http.Response, error) {
		switch request.URL.String() {
		case traceEndpoint:
			traceCalls++
			return response(200, "ip="+address+"\nloc=US\n"), nil
		case geoOrigin + address:
			geoCalls++
			return response(200, strings.ReplaceAll(exitGeoFixture, "192.0.2.10", address)), nil
		default:
			t.Fatal("unexpected diagnostic endpoint", request.URL)
			return nil, ErrExitTrace
		}
	}, func() time.Time { return now })
	value, err := client.Query(context.Background(), ExitInfo{})
	if err != nil || value.IP != address || value.City != "Seattle" || value.ASN != 64500 {
		t.Fatal(value, err)
	}
	now = now.Add(time.Hour)
	value, err = client.Query(context.Background(), value)
	if err != nil || traceCalls != 2 || geoCalls != 1 || value.City != "Seattle" {
		t.Fatal("fresh GEO cache not reused", value, traceCalls, geoCalls, err)
	}
	address = "198.51.100.2"
	value, err = client.Query(context.Background(), value)
	if err != nil || geoCalls != 2 || value.IP != address {
		t.Fatal("changed IP reused old GEO", value, geoCalls, err)
	}
	now = now.Add(24 * time.Hour)
	if _, err := client.Query(context.Background(), value); err != nil || geoCalls != 3 {
		t.Fatal("expired GEO was not refreshed", geoCalls, err)
	}
}

func TestExitGeoFailureKeepsTraceAndHonorsRetryAfter(t *testing.T) {
	now := time.Now()
	geoCalls := 0
	client := exitFixtureClient(t, func(request *http.Request) (*http.Response, error) {
		if request.URL.String() == traceEndpoint {
			return response(200, "ip=192.0.2.10\nloc=US\n"), nil
		}
		geoCalls++
		limited := response(429, `{"message":"limited"}`)
		limited.Header.Set("Retry-After", "3600")
		return limited, nil
	}, func() time.Time { return now })
	old := ExitInfo{IP: "192.0.2.10", CountryCode: "US", City: "Seattle", GeoUpdatedAt: now.Add(-25 * time.Hour)}
	value, err := client.Query(context.Background(), old)
	if err != nil || value.IP != old.IP || value.City != old.City || value.GeoError == "" ||
		!value.GeoRetryAt.Equal(now.Add(time.Hour)) {
		t.Fatal("GEO failure discarded valid exit data", value, err)
	}
	now = now.Add(time.Minute)
	value, err = client.Query(context.Background(), value)
	if err != nil || geoCalls != 1 || value.IP != old.IP || value.GeoError == "" {
		t.Fatal("GEO cooldown sent another request", value, geoCalls, err)
	}
}

func TestExitRejectsWrongGeoIPAndStopsAfterInvalidTrace(t *testing.T) {
	for _, trace := range []string{"ip=192.0.2.10\nloc=US\n", "not a trace"} {
		t.Run(trace, func(t *testing.T) {
			calls := 0
			client := exitFixtureClient(t, func(request *http.Request) (*http.Response, error) {
				calls++
				if request.URL.String() == traceEndpoint {
					return response(200, trace), nil
				}
				return response(200, strings.ReplaceAll(exitGeoFixture, "192.0.2.10", "192.0.2.11")), nil
			}, time.Now)
			value, err := client.Query(context.Background(), ExitInfo{})
			if trace == "not a trace" {
				if !errors.Is(err, ErrExitTrace) || calls != 1 {
					t.Fatal("invalid trace reached GEO service", calls, err)
				}
				return
			}
			if err != nil || value.IP != "192.0.2.10" || value.City != "" || value.GeoError == "" {
				t.Fatal("GEO for a different IP was shown", value, err)
			}
		})
	}
}

func TestExitTraceRateLimitDoesNotQueryGeo(t *testing.T) {
	now := time.Now()
	calls := 0
	client := exitFixtureClient(t, func(request *http.Request) (*http.Response, error) {
		calls++
		limited := response(429, "")
		limited.Header.Set("Retry-After", "7200")
		return limited, nil
	}, func() time.Time { return now })
	value, err := client.Query(context.Background(), ExitInfo{})
	if !errors.Is(err, ErrExitTrace) || calls != 1 || !value.NextAllowed.Equal(now.Add(2*time.Hour)) {
		t.Fatal(value, calls, err)
	}
}
