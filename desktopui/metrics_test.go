package desktopui

import (
	"math"
	"strings"
	"testing"
	"time"

	"github.com/nsgo-dev/nsgo/core"

	"claudeprofiles/desktopusage"
	"claudeprofiles/profiles"
)

func TestPercentAndResetTime(t *testing.T) {
	value, invalid := 26.0, math.NaN()
	if percent(nil) != "—" || percent(&invalid) != "—" || percent(&value) != "26%" {
		t.Fatal("percent should distinguish missing and invalid values from zero")
	}
}

func TestQuotaThresholds(t *testing.T) {
	for _, test := range []struct {
		value float64
		want  int
	}{{0, 0}, {69.9, 0}, {70, 1}, {89.9, 1}, {90, 2}, {100, 2}} {
		if got := quotaTier(&test.value); got != test.want {
			t.Errorf("quotaTier(%v) = %d, want %d", test.value, got, test.want)
		}
	}
}

func TestGrantLineOnlyShowsReturnedCountsAndExpiry(t *testing.T) {
	expiry := time.Date(2026, time.December, 11, 6, 0, 0, 0, time.Local)
	if got := grantLine(profiles.ResetGrant{Remaining: 2, ExpiresAt: expiry, Paused: true}); got != "剩余 2 次 · 2026-12-11 06:00 到期" {
		t.Fatal("untyped grant line added inferred data", got)
	}
	if got := grantLine(profiles.ResetGrant{Remaining: 1}); got != "剩余 1 次" {
		t.Fatal("absent expiry should not add an invented value", got)
	}
	if got := grantLine(profiles.ResetGrant{Kind: desktopusage.ResetFull, Remaining: 1, ExpiresAt: expiry}); got !=
		"完全重置 · 剩余 1 次 · 2026-12-11 06:00 到期\n清空 5 小时和每周的全部用量" {
		t.Fatal("full reset line", got)
	}
	if got := grantLine(profiles.ResetGrant{Kind: desktopusage.ResetModel, Model: "Fable", Remaining: 1}); !strings.HasPrefix(got,
		"Fable 重置 · 剩余 1 次\n") || !strings.Contains(got, "返还 Fable") {
		t.Fatal("model reset line", got)
	}
}

func TestProfileTooltipOmitsLocalMetadata(t *testing.T) {
	base := time.Date(2026, 11, 13, 9, 0, 0, 0, time.Local)
	profile := profiles.Profile{Usage: &profiles.Usage{UpdatedAt: base, ScopedUpdatedAt: base.Add(-time.Hour),
		ResetGrantsAt: base.Add(-2 * time.Hour)}, Details: &profiles.AccountDetails{
		UpdatedAt: base.Add(-3 * time.Hour), Subscription: &profiles.SubscriptionDetails{UpdatedAt: base.Add(-4 * time.Hour)},
	}}
	if tip := cacheTooltip(profile); tip != "额度更新："+usageTime(base) {
		t.Fatal("local metadata appeared as account data", tip)
	}
	profile.Details.FullName, profile.Details.OrganizationName = "Test User", "Personal"
	profile.Details.CreatedAt = &base
	tip := cacheTooltip(profile)
	for _, expected := range []string{"姓名：Test User", "组织：Personal", "账号创建：2026-11-13"} {
		if !strings.Contains(tip, expected) {
			t.Fatal("returned field missing", tip)
		}
	}
	if strings.Contains(tip, "缓存") || strings.Contains(tip, "查询") || strings.Contains(tip, "时间未知") {
		t.Fatal("local or absent data appeared in tooltip", tip)
	}
}

func TestThemeUsesSystemPalette(t *testing.T) {
	theme := claudeTheme()
	if theme.Light.Background != core.RGB(0xf2f2f5) || theme.Light.Surface != core.RGB(0xffffff) ||
		theme.Light.Accent != core.RGB(0x007aff) || theme.Light.Success != core.RGB(0x34c759) {
		t.Fatal("light palette does not match the confirmed design")
	}
	if theme.Dark.Background == theme.Light.Background || theme.Dark.Text == theme.Light.Text {
		t.Fatal("dark palette must be independently legible")
	}
}

func TestUsageTimeBoundaries(t *testing.T) {
	boundary := time.Now()
	before, after := boundary.Add(-time.Second), boundary.Add(time.Second)
	for _, usage := range []*profiles.Usage{
		{FiveHourReset: &boundary}, {SevenDayReset: &boundary},
		{Scoped: []profiles.ScopedUsage{{ResetAt: &boundary}}},
		{ResetGrants: []profiles.ResetGrant{{ExpiresAt: boundary}}},
	} {
		if !usageTimeBoundaryCrossed(usage, before, after) || !usageTimeBoundaryCrossed(usage, after, before) {
			t.Fatal("crossing a deadline in either clock direction must refresh the display")
		}
		if usageTimeBoundaryCrossed(usage, after, after.Add(time.Second)) {
			t.Fatal("a settled deadline must not rebuild the view every poll")
		}
	}
	if usageTimeBoundaryCrossed(nil, before, after) || usageTimeBoundaryCrossed(&profiles.Usage{}, before, after) {
		t.Fatal("unknown timestamps cannot cross a deadline")
	}
}
