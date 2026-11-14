package profiles

import (
	"math"
	"strings"
	"time"

	"claudeprofiles/desktopusage"
)

func validUsageOrganization(organization string) bool {
	if len(organization) != 36 {
		return false
	}
	for _, position := range []int{8, 13, 18, 23} {
		if organization[position] != '-' {
			return false
		}
	}
	return validID(strings.ReplaceAll(organization, "-", ""))
}

// onlineUsage converts one validated online response into display data.
func onlineUsage(record *desktopusage.UsageRecord, now time.Time) (*Usage, bool) {
	if record == nil || record.UpdatedAt.IsZero() || record.UpdatedAt.UnixMilli() <= 0 ||
		record.UpdatedAt.After(now.Add(5*time.Minute)) ||
		!validUsageNumber(record.FiveHour) ||
		!validUsageNumber(record.SevenDay) {
		return nil, false
	}
	result := &Usage{
		FiveHour: cloneUsageNumber(record.FiveHour), SevenDay: cloneUsageNumber(record.SevenDay),
		FiveHourReset: cloneUsageTime(record.FiveHourReset), SevenDayReset: cloneUsageTime(record.SevenDayReset),
		UpdatedAt: record.UpdatedAt, Source: UsageSourceRemote,
		ResetGrantsAt: record.ResetGrantsAt, ResetGrantsKnown: record.ResetGrantsKnown,
	}
	if record.ResetGrantsAt.After(now.Add(5*time.Minute)) ||
		(record.ResetGrantsKnown && record.ResetGrantsAt.IsZero()) {
		result.ResetGrantsKnown = false
		result.ResetGrantsAt = time.Time{}
	}
	if result.ResetGrantsKnown {
		for _, grant := range record.ResetGrants {
			result.ResetGrants = append(result.ResetGrants, ResetGrant{
				Kind: grant.Kind, Model: grant.Model, Remaining: grant.Remaining, ExpiresAt: grant.ExpiresAt, Paused: grant.Paused,
			})
		}
	}
	for _, scoped := range record.Scoped {
		if scoped.Name == "" || !validUsageNumber(scoped.Percent) ||
			!validUsageNumber(scoped.AllowancePercentOfWeekly) {
			continue
		}
		result.Scoped = append(result.Scoped, ScopedUsage{
			Name: scoped.Name, Percent: cloneUsageNumber(scoped.Percent), ResetAt: cloneUsageTime(scoped.ResetAt),
			AllowancePercentOfWeekly: cloneUsageNumber(scoped.AllowancePercentOfWeekly),
		})
	}
	if len(result.Scoped) > 0 {
		result.ScopedUpdatedAt = record.UpdatedAt
	}
	return result, true
}

func validUsageNumber(value *float64) bool {
	return value == nil || (!math.IsNaN(*value) && !math.IsInf(*value, 0) && *value >= 0)
}

func cloneUsageNumber(value *float64) *float64 {
	if value == nil {
		return nil
	}
	number := *value
	return &number
}

func cloneUsageTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	stamp := *value
	return &stamp
}

func cloneUsage(value *Usage) *Usage {
	if value == nil {
		return nil
	}
	result := *value
	result.FiveHour = cloneUsageNumber(value.FiveHour)
	result.SevenDay = cloneUsageNumber(value.SevenDay)
	result.FiveHourReset = cloneUsageTime(value.FiveHourReset)
	result.SevenDayReset = cloneUsageTime(value.SevenDayReset)
	result.Scoped = make([]ScopedUsage, len(value.Scoped))
	for i, scoped := range value.Scoped {
		result.Scoped[i] = ScopedUsage{
			Name: scoped.Name, Percent: cloneUsageNumber(scoped.Percent), ResetAt: cloneUsageTime(scoped.ResetAt),
			AllowancePercentOfWeekly: cloneUsageNumber(scoped.AllowancePercentOfWeekly),
		}
	}
	result.ResetGrants = append([]ResetGrant(nil), value.ResetGrants...)
	return &result
}
