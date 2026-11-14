package desktopusage

import (
	"context"
	"encoding/json"
	"strings"
	"time"
)

const profileEndpoint = "https://api.anthropic.com/api/oauth/profile"

// AccountData contains public display fields obtained with the account's credential.
type AccountData struct {
	AccountID, OrganizationID, Email, DisplayName, FullName, OrganizationName, Plan string
	BillingType, BillingInterval, SubscriptionStatus, NextChargeDate                string
	AccountCreatedAt, SubscriptionCreatedAt, NextChargeAt, PlanEndingAt             *time.Time
	UpdatedAt                                                                       time.Time
	Usage                                                                           *UsageRecord
}

// QueryAccount reads the OAuth identity before querying that account's quota.
func (c *Client) QueryAccount(ctx context.Context, target Target) (*AccountData, error) {
	var data *AccountData
	_, err := c.query(ctx, target, profileEndpoint, func(body []byte, at time.Time) (*UsageRecord, error) {
		var parseErr error
		data, parseErr = parseAccount(body, target, at)
		return &UsageRecord{UpdatedAt: at}, parseErr
	})
	if err != nil {
		return nil, err
	}
	target.OrganizationID = data.OrganizationID
	data.Usage, err = c.query(ctx, target, resetUsageEndpoint, parseUsage)
	return data, err
}

func parseAccount(body []byte, target Target, at time.Time) (*AccountData, error) {
	var root struct {
		Account struct {
			UUID         string          `json:"uuid"`
			Email        string          `json:"email"`
			EmailAddress string          `json:"email_address"`
			DisplayName  string          `json:"display_name"`
			FullName     string          `json:"full_name"`
			CreatedAt    json.RawMessage `json:"created_at"`
			HasPro       bool            `json:"has_claude_pro"`
			HasMax       bool            `json:"has_claude_max"`
		} `json:"account"`
		Organization struct {
			UUID            string          `json:"uuid"`
			Name            string          `json:"name"`
			Type            string          `json:"organization_type"`
			Tier            string          `json:"rate_limit_tier"`
			Plan            string          `json:"subscription_type"`
			BillingType     string          `json:"billing_type"`
			BillingInterval string          `json:"billing_interval"`
			Status          string          `json:"subscription_status"`
			NextChargeDate  string          `json:"next_charge_date"`
			CreatedAt       json.RawMessage `json:"subscription_created_at"`
			NextChargeAt    json.RawMessage `json:"next_charge_at"`
			PlanEndingAt    json.RawMessage `json:"plan_ending_at"`
		} `json:"organization"`
	}
	if json.Unmarshal(body, &root) != nil || root.Account.UUID != target.AccountID ||
		!uuidValue(root.Organization.UUID) || target.OrganizationID != "" && target.OrganizationID != root.Organization.UUID {
		return nil, ErrResponse
	}
	email := root.Account.Email
	if email == "" {
		email = root.Account.EmailAddress
	}
	if !strings.Contains(email, "@") || len(email) > 320 || strings.ContainsAny(email, "\r\n\x00") {
		return nil, ErrResponse
	}
	plan := root.Organization.Plan
	if plan == "" {
		// Anthropic renamed Max 5× to Max and Max 20× to Ultra; old tier names map to the new plans.
		switch strings.ToLower(root.Organization.Tier) {
		case "default_claude_ultra", "claude_ultra", "ultra", "default_claude_max_20x", "claude_max_20x", "max_20x":
			plan = "Ultra"
		case "default_claude_max", "claude_max", "max", "default_claude_max_5x", "claude_max_5x", "max_5x":
			plan = "Max"
		case "default_claude_pro", "claude_pro", "pro":
			plan = "Pro"
		default:
			switch root.Organization.Type {
			case "claude_pro":
				plan = "Pro"
			case "claude_max":
				plan = "Max"
			case "claude_ultra":
				plan = "Ultra"
			case "claude_team":
				plan = "Team"
			case "claude_enterprise":
				plan = "Enterprise"
			case "claude_free":
				plan = "Free"
			}
		}
	}
	if plan == "" {
		switch {
		case root.Account.HasMax:
			plan = "Max"
		case root.Account.HasPro:
			plan = "Pro"
		}
	}
	nextChargeDate := root.Organization.NextChargeDate
	if parsed, err := time.Parse("2006-01-02", nextChargeDate); err != nil ||
		parsed.Year() < 2020 || parsed.Year() > 2100 {
		nextChargeDate = ""
	}
	return &AccountData{AccountID: target.AccountID, OrganizationID: root.Organization.UUID,
		Email: email, DisplayName: cleanName(root.Account.DisplayName), FullName: cleanName(root.Account.FullName),
		OrganizationName: cleanName(root.Organization.Name), AccountCreatedAt: parseReset(root.Account.CreatedAt),
		Plan: cleanName(plan), BillingType: cleanName(root.Organization.BillingType),
		BillingInterval:    cleanName(root.Organization.BillingInterval),
		SubscriptionStatus: cleanName(root.Organization.Status), NextChargeDate: nextChargeDate,
		SubscriptionCreatedAt: parseReset(root.Organization.CreatedAt),
		NextChargeAt:          parseReset(root.Organization.NextChargeAt),
		PlanEndingAt:          parseReset(root.Organization.PlanEndingAt), UpdatedAt: at}, nil
}

func uuidValue(value string) bool {
	if len(value) != 36 {
		return false
	}
	for index, ch := range value {
		if index == 8 || index == 13 || index == 18 || index == 23 {
			if ch != '-' {
				return false
			}
			continue
		}
		if !(ch >= '0' && ch <= '9' || ch >= 'a' && ch <= 'f' || ch >= 'A' && ch <= 'F') {
			return false
		}
	}
	return true
}
