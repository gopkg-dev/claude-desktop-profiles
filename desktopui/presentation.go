package desktopui

import (
	"fmt"
	"math"
	"strings"
	"time"

	"claudeprofiles/desktopusage"
	"claudeprofiles/profiles"
)

func accountEmail(profile profiles.Profile) string {
	if profile.Details != nil {
		if email := strings.TrimSpace(profile.Details.Email); email != "" {
			return email
		}
	}
	if profile.NeedsLogin {
		return "待登录"
	}
	return "邮箱暂未获取"
}

// orderedProfiles puts the current account first and keeps the rest in their saved order.
func orderedProfiles(snapshot *profiles.Snapshot) []profiles.Profile {
	if snapshot == nil {
		return []profiles.Profile{}
	}
	rows := make([]profiles.Profile, 0, len(snapshot.Profiles))
	for _, profile := range snapshot.Profiles {
		if profile.Active {
			rows = append(rows, profile)
		}
	}
	for _, profile := range snapshot.Profiles {
		if !profile.Active {
			rows = append(rows, profile)
		}
	}
	return rows
}

// avatarIndex keeps an account's color while the list reorders.
func avatarIndex(snapshot *profiles.Snapshot, id string) int {
	if snapshot == nil {
		return 0
	}
	for i, profile := range snapshot.Profiles {
		if profile.ID == id {
			return i
		}
	}
	return 0
}

// Plan kinds a badge can show.
const (
	planFree       = "free"
	planPro        = "pro"
	planMax        = "max"
	planUltra      = "ultra"
	planTeam       = "team"
	planEnterprise = "enterprise"
)

// planKind maps a plan name, current or from before Anthropic's renaming, to a badge.
func planKind(plan string) string {
	value := strings.ToLower(strings.TrimSpace(plan))
	switch {
	case value == "":
		return ""
	case strings.Contains(value, "ultra"), strings.Contains(value, "20x"), strings.Contains(value, "20×"):
		return planUltra
	case strings.Contains(value, "max"):
		return planMax
	case strings.Contains(value, "enterprise"):
		return planEnterprise
	case strings.Contains(value, "team"):
		return planTeam
	case strings.Contains(value, "pro"):
		return planPro
	case strings.Contains(value, "free"):
		return planFree
	default:
		return value
	}
}

// planLabel is the badge's text: the known plans by name, any other as returned.
func planLabel(plan string) string {
	kind := planKind(plan)
	switch kind {
	case planFree, planPro, planMax, planUltra, planTeam, planEnterprise:
		return strings.ToUpper(kind)
	default:
		return strings.ToUpper(strings.TrimSpace(plan))
	}
}

func billingChannel(kind string) string {
	switch kind {
	case "apple_subscription":
		return "App Store"
	case "stripe_subscription":
		return "Stripe"
	case "google_subscription", "google_play_subscription":
		return "Google Play"
	default:
		return ""
	}
}

func subscriptionStatus(profile profiles.Profile) string {
	if profile.Details == nil || profile.Details.Subscription == nil {
		return ""
	}
	switch profile.Details.Subscription.Status {
	case "active":
		return "订阅有效"
	case "trialing":
		return "试用中"
	case "past_due":
		return "付款逾期"
	case "canceled":
		return "已取消"
	case "unpaid":
		return "未付款"
	case "paused":
		return "已暂停"
	case "incomplete", "incomplete_expired":
		return "付款未完成"
	default:
		return ""
	}
}

func monthDay(at time.Time) string {
	local := at.In(time.Local)
	return fmt.Sprintf("%d月%d日", local.Month(), local.Day())
}

// renewalText says when the subscription renews or ends, when the account endpoint tells.
func renewalText(subscription *profiles.SubscriptionDetails) string {
	if subscription == nil {
		return ""
	}
	if subscription.PlanEndingAt != nil && !subscription.PlanEndingAt.IsZero() {
		return monthDay(*subscription.PlanEndingAt) + "到期"
	}
	if subscription.NextChargeAt != nil && !subscription.NextChargeAt.IsZero() {
		return monthDay(*subscription.NextChargeAt) + "续费"
	}
	if subscription.NextChargeDate != "" {
		if at, err := time.ParseInLocation("2006-01-02", subscription.NextChargeDate, time.Local); err == nil {
			return monthDay(at) + "续费"
		}
	}
	return ""
}

// subscriptionLine is the line under the email: the billing channel and renewal, or the free plan.
func subscriptionLine(profile profiles.Profile) string {
	if profile.NeedsLogin {
		return "在 Claude Desktop 中登录后显示账号信息"
	}
	if profile.Details == nil {
		return "账号信息待在线刷新"
	}
	subscription := profile.Details.Subscription
	channel := ""
	if subscription != nil {
		channel = billingChannel(subscription.BillingType)
	}
	if channel == "" {
		if planKind(profile.Details.Plan) == planFree {
			return "免费版"
		}
		return renewalText(subscription)
	}
	if renewal := renewalText(subscription); renewal != "" {
		return channel + " · " + renewal
	}
	return channel
}

// daysAlive counts whole local days from the account's creation to now.
func daysAlive(created, now time.Time) int {
	from := created.In(time.Local)
	to := now.In(time.Local)
	start := time.Date(from.Year(), from.Month(), from.Day(), 0, 0, 0, 0, time.Local)
	end := time.Date(to.Year(), to.Month(), to.Day(), 0, 0, 0, 0, time.Local)
	return max(0, int(math.Round(end.Sub(start).Hours()/24)))
}

// metaLine is the card's footer text: current use, subscription status, registration and age.
func metaLine(profile profiles.Profile, now time.Time) string {
	parts := []string{}
	if profile.Active {
		parts = append(parts, "当前使用")
	}
	if status := subscriptionStatus(profile); status != "" {
		parts = append(parts, status)
	}
	if profile.Details != nil && profile.Details.CreatedAt != nil && !profile.Details.CreatedAt.IsZero() {
		created := *profile.Details.CreatedAt
		parts = append(parts, "注册于 "+created.In(time.Local).Format("2006-01-02"),
			fmt.Sprintf("已活 %d 天", daysAlive(created, now)))
	}
	return strings.Join(parts, " · ")
}

// sessionResetText names the day of a 5-hour reset relative to now.
func sessionResetText(reset *time.Time, now time.Time) string {
	if reset == nil || reset.IsZero() {
		return ""
	}
	local, today := reset.In(time.Local), now.In(time.Local)
	day := local.Format("01-02")
	switch {
	case sameDay(local, today):
		day = "今天"
	case sameDay(local, today.AddDate(0, 0, 1)):
		day = "明天"
	}
	return day + local.Format(" 15:04") + " 重置"
}

func sameDay(a, b time.Time) bool {
	return a.Year() == b.Year() && a.YearDay() == b.YearDay()
}

// weeklyResetText names the weekday of a weekly reset.
func weeklyResetText(reset *time.Time) string {
	if reset == nil || reset.IsZero() {
		return ""
	}
	local := reset.In(time.Local)
	return "周" + [...]string{"日", "一", "二", "三", "四", "五", "六"}[local.Weekday()] + local.Format(" 15:04") + " 重置"
}

// grantName says what a reset clears, as Claude names it: the whole weekly limit or one model's share of it.
// It is empty when the response did not say.
func grantName(grant profiles.ResetGrant) string {
	switch grant.Kind {
	case desktopusage.ResetFull:
		return "完全重置"
	case desktopusage.ResetModel:
		if grant.Model != "" {
			return grant.Model + " 重置"
		}
		return "模型重置"
	default:
		return ""
	}
}

// grantEffect explains what using a reset does.
func grantEffect(grant profiles.ResetGrant) string {
	switch grant.Kind {
	case desktopusage.ResetFull:
		return "清空 5 小时和每周的全部用量"
	case desktopusage.ResetModel:
		model := grant.Model
		if model == "" {
			model = "该模型"
		}
		return "清空 5 小时用量，并返还 " + model + " 在每周额度中已用的部分；不重置每周的全部用量"
	default:
		return ""
	}
}

type grantChip struct {
	text, hint string
	urgent     bool
}

// grantChips describes the free resets returned online; nil when the server has not said.
func grantChips(usage *profiles.Usage, now time.Time) []grantChip {
	if usage == nil || !usage.ResetGrantsKnown {
		return nil
	}
	chips := []grantChip{}
	for _, grant := range usage.ResetGrants {
		if grant.Remaining < 1 {
			continue
		}
		text := ""
		if name := grantName(grant); name != "" {
			text = name + " · "
		}
		if grant.Remaining > 1 {
			text += fmt.Sprintf("×%d ", grant.Remaining)
		}
		urgent := false
		switch {
		case grant.ExpiresAt.IsZero():
			text += "可用"
		default:
			days := int(math.Ceil(grant.ExpiresAt.Sub(now).Hours() / 24))
			if days <= 3 {
				urgent = true
				if days < 1 {
					text += "今天到期"
				} else {
					text += fmt.Sprintf("%d 天后到期", days)
				}
			} else {
				text += grant.ExpiresAt.In(time.Local).Format("01-02") + " 到期"
			}
		}
		if grant.Paused {
			text += "（暂停）"
		}
		chips = append(chips, grantChip{text: text, hint: grantLine(grant), urgent: urgent})
	}
	return chips
}
