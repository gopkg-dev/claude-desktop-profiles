package desktopui

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"claudeprofiles/desktopusage"
	"claudeprofiles/httplog"
	"claudeprofiles/profiles"
	"claudeprofiles/workspaces"
	"github.com/nsgo-dev/nsgo/core"
	"github.com/nsgo-dev/nsgo/nsgotest"
	"github.com/nsgo-dev/nsgo/ui"
)

// pageStore adds the settings, request log and session list to the account fixture.
type pageStore struct {
	*fakeStore
	pageMu   sync.Mutex
	settings profiles.Settings
	log      httplog.Summary
	cleared  int
	sessions profiles.SessionList
	// resumable says whether ResumeSessionSharing can turn sharing on without a merge.
	resumable   bool
	resumeCalls int
}

func (s *pageStore) Settings() (profiles.Settings, error) { return s.settings, nil }
func (s *pageStore) SaveSettings(value profiles.Settings) error {
	s.pageMu.Lock()
	defer s.pageMu.Unlock()
	s.settings = value
	return nil
}
func (s *pageStore) ClearOnlineCache() error     { return nil }
func (s *pageStore) RequestLogDirectory() string { return "/tmp/claude-desktop-profiles-test/HTTP" }
func (s *pageStore) RequestLog() (httplog.Summary, error) {
	s.pageMu.Lock()
	defer s.pageMu.Unlock()
	return s.log, nil
}
func (s *pageStore) ClearRequestLog() error {
	s.pageMu.Lock()
	defer s.pageMu.Unlock()
	s.cleared++
	s.log = httplog.Summary{MaxExchanges: s.log.MaxExchanges, MaxBytes: s.log.MaxBytes}
	return nil
}
func (s *pageStore) Sessions() (profiles.SessionList, error) { return s.sessions, nil }
func (s *pageStore) DisableSessionSharing() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.snapshot.SharedSessions = false
	return nil
}
func (s *pageStore) ResumeSessionSharing() (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.resumeCalls++
	if !s.resumable {
		return false, nil
	}
	s.snapshot.SharedSessions = true
	return true, nil
}

// The app's store must keep every optional capability the interface looks for.
var (
	_ Store           = (*profiles.Manager)(nil)
	_ settingsStore   = (*profiles.Manager)(nil)
	_ requestLogStore = (*profiles.Manager)(nil)
	_ sessionStore    = (*profiles.Manager)(nil)
	_ sharingResumer  = (*profiles.Manager)(nil)
)

func pageFixture() *pageStore {
	now := time.Now()
	store := &pageStore{fakeStore: desktopFixture(), settings: profiles.DefaultSettings()}
	store.snapshot.Profiles = store.snapshot.Profiles[:3]
	store.log = httplog.Summary{Exchanges: 3, Bytes: 4096, Latest: now.Add(-time.Minute), MaxExchanges: 100, MaxBytes: 20 << 20}
	store.sessions = profiles.SessionList{
		Accounts: []profiles.SessionAccount{{ProfileID: "personal", Email: "personal@example.com"},
			{ProfileID: "work", Email: "work@example.com"}},
		Sessions: []workspaces.Session{
			{CLISessionID: "s-today", Title: "重写设置页", Cwd: "/tmp/project-a", LastActivityAt: now,
				Present: []bool{true, true}},
			{CLISessionID: "s-partial", Title: "修复同步", Cwd: "/tmp/project-b", LastActivityAt: now.Add(-time.Minute),
				Present: []bool{true, false}},
			{CLISessionID: "s-old", Title: "旧的会话", Cwd: "/tmp/gone", LastActivityAt: now.AddDate(0, 0, -3),
				Present: []bool{true, false}, Issue: workspaces.IssueWorkspaceMissing},
		},
		LastSync: &profiles.SyncRecord{At: now.Add(-time.Hour), Report: workspaces.Report{CodeCopied: 2, CodeUpdated: 1,
			Skipped: 3, Reasons: map[string]int{"code_transcript_missing": 2, "code_workspace_no_longer_exists": 1}}},
	}
	return store
}

func TestPlanBadgesFollowRenamedTiers(t *testing.T) {
	for plan, want := range map[string]string{
		"Free": "FREE", "Pro": "PRO", "Max": "MAX", "Max 5x": "MAX", "Max 20x": "ULTRA", "Ultra": "ULTRA",
		"Team": "TEAM", "Enterprise": "ENTERPRISE", "Scholar": "SCHOLAR", "": "",
	} {
		if got := planLabel(plan); got != want {
			t.Errorf("planLabel(%q) = %q, want %q", plan, got, want)
		}
	}
}

func TestSubscriptionAndMetaLines(t *testing.T) {
	renewal := time.Date(2026, time.November, 16, 9, 0, 0, 0, time.Local)
	created := time.Date(2026, time.October, 23, 12, 0, 0, 0, time.Local)
	now := time.Date(2026, time.November, 14, 9, 0, 0, 0, time.Local)
	profile := profiles.Profile{Active: true, Details: &profiles.AccountDetails{Plan: "Pro", CreatedAt: &created,
		Subscription: &profiles.SubscriptionDetails{BillingType: "stripe_subscription", Status: "active", NextChargeAt: &renewal}}}
	if got := subscriptionLine(profile); got != "Stripe · 11月16日续费" {
		t.Fatal("stripe line", got)
	}
	if got := metaLine(profile, now); got != "当前使用 · 订阅有效 · 注册于 2026-10-23 · 已活 22 天" {
		t.Fatal("meta line", got)
	}
	profile.Details.Subscription = &profiles.SubscriptionDetails{BillingType: "apple_subscription"}
	if got := subscriptionLine(profile); got != "App Store" {
		t.Fatal("an unknown renewal must stay blank", got)
	}
	profile.Details.Subscription, profile.Details.Plan = nil, "Free"
	if got := subscriptionLine(profile); got != "免费版" {
		t.Fatal("free line", got)
	}
}

func TestResetTextsAndGrantChips(t *testing.T) {
	now := time.Date(2026, time.November, 14, 9, 0, 0, 0, time.Local)
	today, tomorrow := now.Add(4*time.Hour), now.Add(20*time.Hour)
	if sessionResetText(&today, now) != "今天 13:00 重置" || sessionResetText(&tomorrow, now) != "明天 05:00 重置" {
		t.Fatal("5-hour reset day is wrong")
	}
	weekly := time.Date(2026, time.November, 17, 9, 0, 0, 0, time.Local)
	if weeklyResetText(&weekly) != "周二 09:00 重置" {
		t.Fatal("weekly reset", weeklyResetText(&weekly))
	}
	usage := &profiles.Usage{ResetGrantsKnown: true, ResetGrants: []profiles.ResetGrant{
		{Remaining: 1, ExpiresAt: now.Add(48 * time.Hour)}, {Remaining: 2, ExpiresAt: now.AddDate(0, 0, 10)},
		{Remaining: 0, ExpiresAt: now.Add(time.Hour)},
	}}
	chips := grantChips(usage, now)
	if len(chips) != 2 || chips[0].text != "2 天后到期" || !chips[0].urgent ||
		chips[1].text != "×2 11-24 到期" || chips[1].urgent {
		t.Fatalf("grant chips %+v", chips)
	}
	typed := grantChips(&profiles.Usage{ResetGrantsKnown: true, ResetGrants: []profiles.ResetGrant{
		{Kind: desktopusage.ResetFull, Remaining: 1, ExpiresAt: now.AddDate(0, 0, 27)},
		{Kind: desktopusage.ResetModel, Model: "Fable", Remaining: 1, ExpiresAt: now.Add(72 * time.Hour)}}}, now)
	if typed[0].text != "完全重置 · 12-11 到期" || typed[1].text != "Fable 重置 · 3 天后到期" ||
		!strings.HasPrefix(typed[0].hint, "完全重置") || strings.Contains(typed[0].hint, "Fable") ||
		!strings.HasPrefix(typed[1].hint, "Fable 重置") || strings.Contains(typed[1].hint, "完全重置") {
		t.Fatalf("typed chips %+v", typed)
	}
	if grantChips(&profiles.Usage{}, now) != nil {
		t.Fatal("unknown grants must not show as none")
	}
}

func TestSessionFiltersGroupsAndReasons(t *testing.T) {
	store := pageFixture()
	sessions := store.sessions.Sessions
	if len(filterSessions(sessions, sessionFilterAll, "")) != 3 ||
		len(filterSessions(sessions, sessionFilterUnsynced, "")) != 2 ||
		len(filterSessions(sessions, sessionFilterIssues, "")) != 1 ||
		len(filterSessions(sessions, sessionFilterAll, "project-b")) != 1 ||
		len(filterSessions(sessions, sessionFilterAll, "同步 修复")) != 1 {
		t.Fatal("session filters are wrong")
	}
	days := groupSessions(sessions, time.Now())
	if len(days) != 2 || days[0].heading != "今天" || len(days[0].sessions) != 2 {
		t.Fatalf("day groups %+v", days)
	}
	reasons := skipReasons(map[string]int{"code_transcript_missing": 2, "source_identity_missing": 1,
		"source_identity_unavailable": 1, "something_new": 1})
	if len(reasons) != 3 || reasons[0] != (skipReason{"对话记录缺失", 2}) || reasons[1] != (skipReason{"账号身份未知", 2}) ||
		reasons[2].text != "something_new" {
		t.Fatalf("skip reasons %+v", reasons)
	}
}

func startPages(t *testing.T, store Store, dark bool) *nsgotest.App {
	t.Helper()
	t.Setenv("HOME", testHome(t))
	environment := core.Environment{OS: core.OSMacOS, Idiom: core.IdiomDesktop, Dark: dark}
	application := nsgotest.Start(t, func(cx *core.Scope) core.Node {
		return newApp(cx, store, func(context.Context) error { return nil }).view()
	}, nsgotest.Size(480, 760), nsgotest.Environment(environment))
	application.WaitUntil(3*time.Second, func() bool { return application.Exists("id:profile-personal") })
	return application
}

func TestAccountCardsShowConfirmedLayout(t *testing.T) {
	for _, theme := range []string{"light", "dark"} {
		t.Run(theme, func(t *testing.T) {
			application := startPages(t, pageFixture(), theme == "dark")
			for _, selector := range []string{
				"id:profile-personal >> text:PRO", "id:profile-personal >> text:打开 Claude",
				"id:profile-work >> text:切换", "id:profile-personal >> text:78%", "id:profile-work >> text:/^Fable$/", "id:profile-work >> text:Fable 重置",
				"text:Claude Desktop Profiles", "text:3 个账号", "id:account-footer >> text:会话共享",
			} {
				application.Find(selector)
			}
			if !strings.HasPrefix(application.Find("id:meta-personal").Text, "当前使用 · 订阅有效") {
				t.Fatal("current card meta line", application.Find("id:meta-personal").Text)
			}
			if application.Exists("text:备注不应作为账号标题") || application.Exists("text:订阅始于") {
				t.Fatal("removed fields are still shown")
			}
			page := manualViewBounds(application.FindMounted("id:desktop-page"))
			for _, id := range []string{"profile-personal", "switch-work", "archive-work", "exit-ip"} {
				bounds := manualViewBounds(application.FindMounted("id:" + id))
				if bounds.Origin.X < -0.5 || bounds.Origin.X+bounds.Size.Width > page.Size.Width+0.5 {
					t.Fatalf("%s overflows the 480-point window: %v", id, bounds)
				}
			}
			application.Screenshot("design-accounts-" + theme)
		})
	}
}

func TestSwitchAsksForConfirmation(t *testing.T) {
	store := pageFixture()
	application := startPages(t, store, false)
	application.Press("id:switch-work")
	application.Find("id:action-panel >> text:切换到 work@example.com？")
	application.Screenshot("design-switch-alert")
	application.Press("id:cancel")
	if application.Exists("id:action-panel") {
		t.Fatal("cancel left the alert open")
	}
	application.Press("id:switch-work")
	application.Press("id:confirm")
	application.WaitUntil(3*time.Second, func() bool { return application.Exists("id:open-work") })
}

func TestSettingsPageShowsRequestLogAndClearsIt(t *testing.T) {
	store := pageFixture()
	application := startPages(t, store, false)
	application.Press("id:open-settings")
	application.WaitUntil(3*time.Second, func() bool {
		return strings.Contains(application.Find("id:request-log-summary").Text, "3 次请求 · 4 KB")
	})
	for _, text := range []string{"额度刷新", "会话共享", "请求日志", "出口 IP", "其他"} {
		application.Find("id:settings-page >> text:" + text)
	}
	application.WaitUntil(3*time.Second, func() bool { return application.Exists("id:settings-sessions >> text:3 个 · ") })
	if application.Find("id:request-log-path").Text != "/tmp/claude-desktop-profiles-test/HTTP" {
		t.Fatal("request log path", application.Find("id:request-log-path").Text)
	}
	application.Screenshot("design-settings")
	application.Resize(480, 560)
	application.Advance(300 * time.Millisecond)
	application.Scroll("id:settings-scroll", 140)
	application.Advance(300 * time.Millisecond)
	bar := manualViewBounds(application.FindMounted("id:settings-bar"))
	if bar.Origin.Y > 0.5 || bar.Size.Height < titleBarHeight-0.5 {
		t.Fatal("settings title bar left the top while scrolling", bar)
	}
	application.Screenshot("design-settings-scrolled")
	application.SetEnvironment(core.Environment{OS: core.OSMacOS, Idiom: core.IdiomDesktop, Dark: true})
	application.Advance(300 * time.Millisecond)
	if application.FindMounted("id:settings-scroll").Offset.Y < 1 {
		application.Scroll("id:settings-scroll", 140)
		application.Advance(300 * time.Millisecond)
	}
	application.Screenshot("design-settings-scrolled-dark")
	application.SetEnvironment(core.Environment{OS: core.OSMacOS, Idiom: core.IdiomDesktop})
	application.Resize(480, 760)
	application.Press("id:clear-request-log")
	application.Find("id:action-panel >> text:清空请求日志？")
	application.Press("id:confirm")
	application.WaitUntil(3*time.Second, func() bool {
		return strings.Contains(application.Find("id:request-log-summary").Text, "暂无记录")
	})
	store.pageMu.Lock()
	cleared := store.cleared
	store.pageMu.Unlock()
	if cleared != 1 {
		t.Fatal("request log was not cleared exactly once", cleared)
	}
	application.Press("id:back")
	application.Find("id:profile-personal")
}

func TestSessionsPageFiltersAndExplainsSkips(t *testing.T) {
	application := startPages(t, pageFixture(), false)
	application.Press("id:open-sessions")
	application.WaitUntil(3*time.Second, func() bool { return application.Exists("id:session-s-today") })
	application.Find("id:sessions-summary >> text:3 个会话 · 2 个账号")
	application.Find("id:sessions-page >> text:/^今天$/")
	application.Find("id:session-s-old >> text:工作目录已不存在，不会同步")
	application.Find("text:要删除会话请在 Claude Desktop 里操作，删除会自动同步到其他账号。")
	application.Press("id:skip-reasons")
	application.Find("id:sessions-summary >> text:对话记录缺失")
	application.Screenshot("design-sessions")
	application.Press("id:session-filter-issues")
	if application.Exists("id:session-s-today") || !application.Exists("id:session-s-old") {
		t.Fatal("issue filter shows the wrong sessions")
	}
	application.Press("id:session-filter-unsynced")
	if application.Exists("id:session-s-today") || !application.Exists("id:session-s-partial") {
		t.Fatal("unsynced filter shows the wrong sessions")
	}
	application.Press("id:session-filter-all")
	application.Find("id:session-menu-s-today")
	application.Resize(480, 420)
	application.Advance(300 * time.Millisecond)
	application.Scroll("id:sessions-scroll", 200)
	application.Advance(300 * time.Millisecond)
	bar := manualViewBounds(application.FindMounted("id:sessions-bar"))
	if bar.Origin.Y > 0.5 || bar.Size.Height < titleBarHeight+37.5 {
		t.Fatal("sessions title bar and search left the top while scrolling", bar)
	}
	application.Screenshot("design-sessions-scrolled")
	application.Resize(480, 760)
	application.Type("id:session-search", "project-b")
	application.WaitUntil(3*time.Second, func() bool { return !application.Exists("id:session-s-today") })
	if !application.Exists("id:session-s-partial") {
		t.Fatal("search hid the matching session")
	}
	if application.Exists("text:/^删除/") || application.Exists("text:在终端中继续") {
		t.Fatal("deferred session actions appeared")
	}
}

func TestWaitingCardOffersToGoBack(t *testing.T) {
	store := pageFixture()
	store.snapshot.Profiles = append(store.snapshot.Profiles, profiles.Profile{ID: "fresh", NeedsLogin: true})
	store.snapshot.ActiveID = "fresh"
	for i := range store.snapshot.Profiles {
		store.snapshot.Profiles[i].Active = store.snapshot.Profiles[i].ID == "fresh"
	}
	t.Setenv("HOME", testHome(t))
	application := nsgotest.Start(t, func(cx *core.Scope) core.Node {
		model := newApp(cx, store, func(context.Context) error { return nil })
		model.addReturn.Set("personal")
		return model.view()
	}, nsgotest.Size(480, 760))
	application.WaitUntil(3*time.Second, func() bool { return application.Exists("id:abandon-add") })
	application.Find("text:等待你在 Claude Desktop 里登录…")
	application.Find("text:放弃添加，切回 personal@example.com")
	application.Screenshot("design-add-waiting")
}

func TestTooltipsLeaveOutNoise(t *testing.T) {
	details := &profiles.AccountDetails{Email: "person@example.com", FullName: "Person",
		OrganizationName: "person@example.com's Organization", Plan: "Pro"}
	if tip := cacheTooltip(profiles.Profile{Details: details}); strings.Contains(tip, "组织") {
		t.Fatal("the default personal organization appeared", tip)
	}
	details.OrganizationName = "Acme Research"
	if tip := cacheTooltip(profiles.Profile{Details: details}); !strings.Contains(tip, "组织：Acme Research") {
		t.Fatal("a real organization is missing", tip)
	}
	cx := core.NewRoot(core.RootOptions{Post: func(func()) {}})
	checked := time.Date(2026, time.November, 14, 20, 13, 0, 0, time.Local)
	a := &app{cx: cx, exitMessage: core.NewSignal(cx, ""), exitInfo: core.NewSignal(cx, &desktopusage.ExitInfo{
		IP: "192.0.2.10", Country: "United States", Region: "Nevada", City: "Las Vegas", ISP: "Example ISP",
		ASN: 64500, Proxy: "http://127.0.0.1:6152", TraceCountry: "US", CheckedAt: checked})}
	want := "Las Vegas, Nevada, United States\nExample ISP · AS64500"
	if got := a.exitTooltip(); got != want {
		t.Fatalf("exit tooltip:\n%s", got)
	}
}

func TestGrantChipHoverShowsOnlyItsGrant(t *testing.T) {
	if nsgotest.Target() == "macos" {
		t.Skip("macOS testhost synthetic hover is cancelled by native tracking")
	}
	application := startPages(t, pageFixture(), false)
	application.Hover("id:profile-work >> text:Fable 重置")
	application.Advance(600 * time.Millisecond)
	application.WaitUntil(3*time.Second, func() bool { return len(application.Query("role:alert")) == 1 })
	tip := application.Find("role:alert >> text:Fable 重置").Text
	if strings.Contains(tip, "完全重置") || !strings.Contains(tip, "返还 Fable") {
		t.Fatal("hovering one grant showed others", tip)
	}
}

func TestTurningSharingBackOnSkipsRestartWhenItCan(t *testing.T) {
	store := pageFixture()
	store.resumable = true
	application := startPages(t, store, false)
	checked := func() bool { return application.FindMounted("id:sharing").Props.Checked }
	application.Press("id:sharing")
	application.WaitUntil(3*time.Second, func() bool { return !checked() })
	application.Press("id:sharing")
	application.WaitUntil(3*time.Second, func() bool { return application.Exists("id:toast") })
	if application.Exists("id:action-panel") || !checked() || !application.Exists("id:toast >> text:会话共享已恢复") {
		t.Fatal("resuming on the same account asked to restart or did not turn sharing on")
	}

	store.mu.Lock()
	store.resumable = false
	store.mu.Unlock()
	application.Press("id:sharing")
	application.WaitUntil(3*time.Second, func() bool { return !checked() })
	application.Press("id:sharing")
	application.WaitUntil(3*time.Second, func() bool { return application.Exists("id:action-panel") })
	application.Find("id:action-panel >> text:立即同步会话？")
	store.mu.Lock()
	calls := store.resumeCalls
	store.mu.Unlock()
	if calls != 2 {
		t.Fatal("each turn-on should try to resume first", calls)
	}
}

func TestLongHintsWrapInsteadOfBeingCut(t *testing.T) {
	long := grantEffect(profiles.ResetGrant{Kind: desktopusage.ResetModel, Model: "Fable"})
	application := nsgotest.Start(t, func(cx *core.Scope) core.Node {
		colors := ui.ColorsOf(cx)
		return core.View{Style: core.Style{Padding: core.All(20), AlignItems: core.AlignStart}, Children: []core.Node{
			core.View{Style: core.Style{Padding: core.Symmetric(7, 9), CornerRadius: 6, Background: colors.Text()},
				Children: []core.Node{hintText(long, colors)}},
		}}
	}, nsgotest.Size(480, 200))
	frame := application.Find("id:hint-text").Frame
	if frame.Size.Width > hintWidth || frame.Size.Height < 2*11 {
		t.Fatalf("a long hint should wrap within %dpt, got %vx%v", hintWidth, frame.Size.Width, frame.Size.Height)
	}
	application.Screenshot("hint-wrapped")
}

func TestAboutPageShowsVersionLinksAndUpdates(t *testing.T) {
	application := startPages(t, pageFixture(), false)
	application.Press("id:open-about")
	application.WaitUntil(3*time.Second, func() bool { return application.Exists("id:about-header") })
	if got := application.Find("id:about-version").Text; got != "版本 "+Version {
		t.Fatalf("version line %q", got)
	}
	for _, selector := range []string{"id:about-intro", "id:link-repository >> text:gopkg-dev/claude-desktop-profiles",
		"id:link-releases", "id:link-issues", "text:开发版本不检查更新"} {
		application.Find(selector)
	}
	if !application.FindMounted("id:check-updates").Props.Disabled {
		t.Fatal("a development build cannot update itself, so the check must be disabled")
	}
	if aboutLinks[0].url != "https://github.com/gopkg-dev/claude-desktop-profiles" ||
		aboutLinks[2].url != aboutLinks[0].url+"/issues" {
		t.Fatal("links must point at the release repository", aboutLinks)
	}
	application.Screenshot("design-about")
	application.Press("id:back")
	application.WaitUntil(3*time.Second, func() bool { return application.Exists("id:profile-personal") })
}

func TestReleaseBuildOffersUpdateCheck(t *testing.T) {
	saved := releaseBuild
	releaseBuild = func() bool { return true }
	t.Cleanup(func() { releaseBuild = saved })
	application := startPages(t, pageFixture(), false)
	application.Press("id:open-about")
	application.WaitUntil(3*time.Second, func() bool { return application.Exists("id:check-updates") })
	if application.FindMounted("id:check-updates").Props.Disabled || !application.Exists("text:每天自动检查一次，有新版本时提示") {
		t.Fatal("a release build should offer to check for updates")
	}
}
