package desktopusage

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"claudeprofiles/desktopauth"
)

const accountFixture = `{"account":{"uuid":"aaaaaaaa-1111-2222-3333-bbbbbbbbbbbb","email":"online@example.com"},"organization":{"uuid":"cccccccc-1111-2222-3333-dddddddddddd","rate_limit_tier":"default_claude_max_20x"}}`

func TestQueryAccountVerifiesIdentityAndQueriesOnlyReadEndpoints(t *testing.T) {
	account := "aaaaaaaa-1111-2222-3333-bbbbbbbbbbbb"
	organization := "cccccccc-1111-2222-3333-dddddddddddd"
	var paths []string
	client := fixtureClient(func(request *http.Request) (*http.Response, error) {
		paths = append(paths, request.URL.Path)
		if request.Method != "GET" || request.Header.Get("Authorization") != "Bearer fixture-token" {
			t.Fatal("wrong authenticated request")
		}
		if request.Header.Get("anthropic-beta") != "oauth-2025-04-20" ||
			request.Header.Get("anthropic-version") != "2023-06-01" ||
			request.Header.Get("Accept") != "application/json" || request.Header.Get("User-Agent") == "" {
			t.Fatal("incomplete OAuth request headers")
		}
		if request.URL.String() == profileEndpoint {
			return response(200, accountFixture), nil
		}
		if request.URL.String() != resetUsageEndpoint ||
			!strings.HasPrefix(request.Header.Get("User-Agent"), "claude-cli/") {
			t.Fatal("unexpected route")
		}
		return response(200, `{"five_hour":{"utilization":42}}`), nil
	})
	client.loadCredential = func(_, gotAccount, gotOrg string, _ time.Time) (desktopauth.Credential, error) {
		if gotAccount != account || gotOrg != "" && gotOrg != organization {
			t.Fatal("wrong credential scope")
		}
		return desktopauth.Credential{AccessToken: "fixture-token", OrganizationID: organization}, nil
	}
	data, err := client.QueryAccount(context.Background(), Target{AccountID: account})
	if err != nil || data.Plan != "Ultra" || data.Email != "online@example.com" || data.Usage == nil || len(paths) != 2 {
		t.Fatal(data, paths, err)
	}
	if data.NextChargeDate != "" || data.NextChargeAt != nil {
		t.Fatal("unknown billing date was invented")
	}
}

func TestProfileMapsDesktopFieldsWithoutInventingExpiry(t *testing.T) {
	const body = `{
		"account":{"uuid":"aaaaaaaa-1111-2222-3333-bbbbbbbbbbbb","email":"test@example.com",
		"display_name":"Test","full_name":"Test User","has_claude_pro":true,
		"created_at":"2026-09-01T10:00:00Z"},
		"organization":{"uuid":"cccccccc-1111-2222-3333-dddddddddddd","name":"Personal",
		"organization_type":"claude_pro","rate_limit_tier":"default_claude_ai",
		"billing_type":"apple_subscription","subscription_status":null,
		"subscription_created_at":"2026-11-08T04:19:14.264273Z"}}
	`
	data, err := parseAccount([]byte(body), Target{AccountID: "aaaaaaaa-1111-2222-3333-bbbbbbbbbbbb"}, time.Now())
	if err != nil || data.FullName != "Test User" || data.DisplayName != "Test" || data.Plan != "Pro" ||
		data.BillingType != "apple_subscription" || data.SubscriptionStatus != "" ||
		data.AccountCreatedAt == nil || data.SubscriptionCreatedAt == nil {
		t.Fatal("profile display fields missing", err)
	}
	if data.NextChargeAt != nil || data.PlanEndingAt != nil || data.NextChargeDate != "" || data.BillingInterval != "" {
		t.Fatal("subscription creation was used to invent expiry or billing interval")
	}
}

func TestProfileOptionalDatesDoNotHideVerifiedIdentity(t *testing.T) {
	body := `{"account":{"uuid":"aaaaaaaa-1111-2222-3333-bbbbbbbbbbbb","email":"test@example.com",
		"has_claude_max":true},"organization":{"uuid":"cccccccc-1111-2222-3333-dddddddddddd",
		"next_charge_at":"invalid","next_charge_date":"not-a-date","plan_ending_at":{},
		"subscription_created_at":null}}`
	data, err := parseAccount([]byte(body), Target{AccountID: "aaaaaaaa-1111-2222-3333-bbbbbbbbbbbb"}, time.Now())
	if err != nil || data.Email != "test@example.com" || data.Plan != "Max" ||
		data.NextChargeAt != nil || data.PlanEndingAt != nil || data.NextChargeDate != "" {
		t.Fatal("optional malformed date discarded identity or invented a date", err)
	}
}

func TestAccountIdentityMismatchStopsBeforeUsage(t *testing.T) {
	calls := 0
	client := fixtureClient(func(*http.Request) (*http.Response, error) { calls++; return response(200, accountFixture), nil })
	_, err := client.QueryAccount(context.Background(), Target{AccountID: "wrong-account"})
	if err == nil || calls != 1 {
		t.Fatal("mismatched identity queried quota", calls, err)
	}
}

func TestAccountQuotaFailureRetainsOnlyVerifiedPublicProfile(t *testing.T) {
	client := fixtureClient(func(request *http.Request) (*http.Response, error) {
		if request.URL.String() == profileEndpoint {
			return response(200, accountFixture), nil
		}
		return response(429, `{}`), nil
	})
	data, err := client.QueryAccount(context.Background(), Target{AccountID: "aaaaaaaa-1111-2222-3333-bbbbbbbbbbbb"})
	if err == nil || data == nil || data.Email != "online@example.com" || data.Usage != nil {
		t.Fatal(data, err)
	}
}
