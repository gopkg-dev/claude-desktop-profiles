package desktopui

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"claudeprofiles/profiles"
)

func percent(value *float64) string {
	if value == nil || math.IsNaN(*value) || math.IsInf(*value, 0) || *value < 0 {
		return "—"
	}
	return strings.TrimSuffix(strconv.FormatFloat(*value, 'f', 0, 64), ".0") + "%"
}

func usageTime(value time.Time) string {
	if value.IsZero() {
		return "时间未知"
	}
	return value.In(time.Local).Format("2006-01-02 15:04")
}

// quotaTier is 0 below 70%, 1 from 70% and 2 from 90%.
func quotaTier(value *float64) int {
	if value == nil || math.IsNaN(*value) || math.IsInf(*value, 0) {
		return 0
	}
	if *value >= 90 {
		return 2
	}
	if *value >= 70 {
		return 1
	}
	return 0
}

func usageTimeBoundaryCrossed(usage *profiles.Usage, before, after time.Time) bool {
	if usage == nil {
		return false
	}
	crossed := func(at *time.Time) bool {
		return at != nil && !at.IsZero() && at.After(before) != at.After(after)
	}
	if crossed(usage.FiveHourReset) || crossed(usage.SevenDayReset) {
		return true
	}
	for _, limit := range usage.Scoped {
		if crossed(limit.ResetAt) {
			return true
		}
	}
	for _, grant := range usage.ResetGrants {
		if crossed(&grant.ExpiresAt) {
			return true
		}
	}
	return false
}

// grantLine describes one reset grant: its kind, count, expiry and what it clears.
func grantLine(grant profiles.ResetGrant) string {
	line := fmt.Sprintf("剩余 %d 次", grant.Remaining)
	if name := grantName(grant); name != "" {
		line = name + " · " + line
	}
	if !grant.ExpiresAt.IsZero() {
		line += " · " + grant.ExpiresAt.In(time.Local).Format("2006-01-02 15:04") + " 到期"
	}
	if effect := grantEffect(grant); effect != "" {
		line += "\n" + effect
	}
	return line
}

// defaultOrganization reports the personal organization Claude names after the account, which says nothing new.
func defaultOrganization(details *profiles.AccountDetails) bool {
	owner, ok := strings.CutSuffix(strings.TrimSpace(details.OrganizationName), "'s Organization")
	if !ok {
		return false
	}
	for _, name := range []string{details.Email, details.FullName, details.DisplayName} {
		if name != "" && strings.EqualFold(owner, strings.TrimSpace(name)) {
			return true
		}
	}
	return false
}

// cacheTooltip lists the account's online details for the email's tooltip.
func cacheTooltip(profile profiles.Profile) string {
	lines := []string{}
	if details := profile.Details; details != nil {
		if details.FullName != "" {
			lines = append(lines, "姓名："+details.FullName)
		}
		if details.OrganizationName != "" && !defaultOrganization(details) {
			lines = append(lines, "组织："+details.OrganizationName)
		}
		if details.Plan != "" {
			lines = append(lines, "套餐："+details.Plan)
		}
		if details.CreatedAt != nil {
			lines = append(lines, "账号创建："+details.CreatedAt.In(time.Local).Format("2006-01-02"))
		}
		if subscription := details.Subscription; subscription != nil {
			if channel := billingChannel(subscription.BillingType); channel != "" {
				lines = append(lines, "订阅渠道："+channel)
			}
			if status := subscriptionStatus(profile); status != "" {
				lines = append(lines, "订阅状态："+status)
			}
			if subscription.PlanEndingAt != nil {
				lines = append(lines, "到期："+usageTime(*subscription.PlanEndingAt))
			}
			if subscription.NextChargeAt != nil {
				lines = append(lines, "续费："+usageTime(*subscription.NextChargeAt))
			}
			if subscription.NextChargeAt == nil && subscription.NextChargeDate != "" {
				lines = append(lines, "续费："+subscription.NextChargeDate)
			}
		}
	}
	if profile.Usage != nil && !profile.Usage.UpdatedAt.IsZero() {
		lines = append(lines, "额度更新："+usageTime(profile.Usage.UpdatedAt))
	}
	return strings.Join(lines, "\n")
}
