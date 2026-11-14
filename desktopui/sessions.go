package desktopui

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/nsgo-dev/nsgo/core"
	"github.com/nsgo-dev/nsgo/icons/lucide"
	"github.com/nsgo-dev/nsgo/ui"

	"claudeprofiles/profiles"
	"claudeprofiles/workspaces"
)

type sessionStore interface {
	Sessions() (profiles.SessionList, error)
}

// sessionPage is the last read of the local Code sessions.
type sessionPage struct {
	loading bool
	list    profiles.SessionList
	err     error
}

const (
	sessionFilterAll      = "all"
	sessionFilterUnsynced = "unsynced"
	sessionFilterIssues   = "issues"
)

// openSessions shows the session page and reads the sessions again.
func (a *app) openSessions() {
	a.page.Set("sessions")
	a.loadSessions()
}

func (a *app) loadSessions() {
	store, ok := a.store.(sessionStore)
	if !ok {
		return
	}
	if page := a.sessions.Get(); page != nil && page.loading {
		return
	}
	previous := a.sessions.Get()
	loading := &sessionPage{loading: true}
	if previous != nil {
		loading.list = previous.list
	}
	a.sessions.Set(loading)
	go func() {
		list, err := store.Sessions()
		a.cx.Post(func() { a.sessions.Set(&sessionPage{list: list, err: err}) })
	}()
}

func unsynced(session workspaces.Session) bool {
	for _, present := range session.Present {
		if !present {
			return true
		}
	}
	return false
}

// filterSessions keeps the sessions that match the filter and every word of the query.
func filterSessions(sessions []workspaces.Session, filter, query string) []workspaces.Session {
	words := strings.Fields(strings.ToLower(query))
	result := []workspaces.Session{}
	for _, session := range sessions {
		switch {
		case filter == sessionFilterUnsynced && !unsynced(session):
			continue
		case filter == sessionFilterIssues && session.Issue == "":
			continue
		}
		text := strings.ToLower(session.Title + " " + session.Cwd)
		matched := true
		for _, word := range words {
			if !strings.Contains(text, word) {
				matched = false
				break
			}
		}
		if matched {
			result = append(result, session)
		}
	}
	return result
}

func sessionTime(session workspaces.Session) time.Time {
	if !session.LastActivityAt.IsZero() {
		return session.LastActivityAt
	}
	return session.CreatedAt
}

// dayHeading names the day of a group of sessions.
func dayHeading(at, now time.Time) string {
	local, today := at.In(time.Local), now.In(time.Local)
	switch {
	case at.IsZero():
		return "时间未知"
	case sameDay(local, today):
		return "今天"
	case sameDay(local, today.AddDate(0, 0, -1)):
		return "昨天"
	case local.Year() == today.Year():
		return fmt.Sprintf("%d月%d日", local.Month(), local.Day())
	default:
		return fmt.Sprintf("%d年%d月%d日", local.Year(), local.Month(), local.Day())
	}
}

type sessionDay struct {
	heading  string
	sessions []workspaces.Session
}

// groupSessions splits sessions, newest first, into days.
func groupSessions(sessions []workspaces.Session, now time.Time) []sessionDay {
	days := []sessionDay{}
	for _, session := range sessions {
		heading := dayHeading(sessionTime(session), now)
		if len(days) == 0 || days[len(days)-1].heading != heading {
			days = append(days, sessionDay{heading: heading})
		}
		days[len(days)-1].sessions = append(days[len(days)-1].sessions, session)
	}
	return days
}

func issueText(issue string) string {
	switch issue {
	case workspaces.IssueTranscriptMissing:
		return "对话记录缺失，无法同步到其他账号"
	case workspaces.IssueManagedWorkspace:
		return "工作目录由 Claude 临时管理，不会同步"
	case workspaces.IssueWorkspaceMissing:
		return "工作目录已不存在，不会同步"
	default:
		return ""
	}
}

var skipReasonText = map[string]string{
	"code_transcript_missing":            "对话记录缺失",
	"code_workspace_invalid":             "工作目录无效",
	"unsupported_managed_code_workspace": "工作目录由 Claude 临时管理",
	"code_scratch_workspace_missing":     "临时工作目录不存在",
	"code_workspace_no_longer_exists":    "工作目录已不存在",
	"code_workspace_not_accessible":      "工作目录无法访问",
	"invalid_session_metadata":           "会话信息无效",
	"conflicting_session_identity":       "会话身份冲突",
	"source_identity_missing":            "账号身份未知",
	"source_identity_unavailable":        "账号身份未知",
	"source_not_signed_in":               "账号未登录",
	"source_details_unavailable":         "账号资料未读取",
	"excluded_private_or_execution_file": "私有或可执行文件",
	"symlink_not_shared":                 "符号链接",
	"non_regular_file":                   "非普通文件",
}

type skipReason struct {
	text  string
	count int
}

// skipReasons lists a sync's skip reasons, most frequent first.
func skipReasons(reasons map[string]int) []skipReason {
	merged := map[string]int{}
	for code, count := range reasons {
		text := skipReasonText[code]
		if text == "" {
			text = code
		}
		merged[text] += count
	}
	result := make([]skipReason, 0, len(merged))
	for text, count := range merged {
		if count > 0 {
			result = append(result, skipReason{text, count})
		}
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].count != result[j].count {
			return result[i].count > result[j].count
		}
		return result[i].text < result[j].text
	})
	return result
}

func (a *app) sessionsView() core.Node {
	scrolled := core.NewSignal(a.cx, false)
	return core.Dynamic(func(cx *core.Scope) core.Node {
		reload := core.Dynamic(func(cx *core.Scope) core.Node {
			t := tonesOf(cx)
			page := a.sessions.Get()
			refreshing := page != nil && page.loading
			return glassCapsule(cx, toolButton(cx, lucide.RotateCw, "重新读取", "reload-sessions", t.Text(),
				refreshing, refreshing, a.loadSessions))
		})
		search := core.View{Style: core.Style{Padding: core.Edges{Left: 16, Right: 16, Bottom: 10}},
			Children: []core.Node{a.sessionSearch(cx)}}
		return frostedPage(cx, "sessions", scrolled, titleBarHeight+42, core.Dynamic(a.sessionContent),
			a.pageBar(cx, "会话管理", reload), search)
	})
}

func (a *app) sessionSearch(cx *core.Scope) core.Node {
	t := tonesOf(cx)
	return core.View{Style: core.Style{Direction: core.Row, AlignItems: core.AlignCenter, Gap: 6, Height: core.Pt(28),
		Padding: core.Edges{Left: 9, Right: 8}, CornerRadius: 8, Background: t.Fill()}, Children: []core.Node{
		ui.Icon(lucide.Search, 13, ui.Fixed(t.Tertiary())),
		core.Input{Value: a.sessionQuery.Get, OnChange: a.sessionQuery.Set, Placeholder: "搜索标题或工作目录",
			Label: "搜索会话", TestID: "session-search", Style: core.Style{FlexGrow: 1, FlexShrink: 1, MinWidth: core.Pt(0),
				Height: core.Pt(28), FontSize: 13, Color: t.Text(), PlaceholderColor: t.Tertiary(),
				CaretColor: t.Accent(), SelectionColor: cx.Palette().Selection}},
	}}
}

func (a *app) sessionContent(cx *core.Scope) core.Node {
	t := tonesOf(cx)
	page := a.sessions.Get()
	column := func(children ...core.Node) core.Node {
		return core.View{Style: core.Style{Gap: 14, Padding: core.Edges{Left: 16, Right: 16, Bottom: 20}}, Children: children}
	}
	if _, ok := a.store.(sessionStore); !ok {
		return column(messageText(cx, "当前数据源不支持会话管理"))
	}
	if page == nil || page.loading && len(page.list.Sessions) == 0 && len(page.list.Accounts) == 0 {
		return column(messageText(cx, "正在读取会话…"))
	}
	if page.err != nil && len(page.list.Sessions) == 0 {
		return column(a.sessionSummary(cx, page), messageText(cx, "无法读取会话："+page.err.Error()))
	}
	now := time.Now()
	sessions := filterSessions(page.list.Sessions, a.sessionFilter.Get(), a.sessionQuery.Get())
	children := []core.Node{a.sessionSummary(cx, page), a.sessionFilters(cx, page.list.Sessions)}
	if len(sessions) == 0 {
		empty := "没有会话"
		if a.sessionQuery.Get() != "" || a.sessionFilter.Get() != sessionFilterAll {
			empty = "没有符合条件的会话"
		}
		children = append(children, messageText(cx, empty))
	}
	for _, day := range groupSessions(sessions, now) {
		rows := []core.Node{}
		for i, session := range day.sessions {
			if i > 0 {
				rows = append(rows, core.View{Style: core.Style{Height: core.Pt(0.5), Margin: core.Edges{Left: 14},
					Background: t.Line()}})
			}
			rows = append(rows, a.sessionRow(cx, page.list.Accounts, session))
		}
		children = append(children, core.View{Style: core.Style{Gap: 6}, Children: []core.Node{
			core.Text{Content: day.heading, Style: core.Style{FontSize: 11, FontWeight: core.FontWeightSemibold,
				Color: t.Secondary(), Padding: core.Symmetric(0, 10)}},
			core.View{Style: core.Style{CornerRadius: 12, Background: t.Card(), BorderWidth: 0.5,
				BorderColor: t.CardBorder(), Overflow: core.Hidden}, Children: rows},
		}})
	}
	children = append(children, core.Text{Content: "要删除会话请在 Claude Desktop 里操作，删除会自动同步到其他账号。",
		Style: core.Style{FontSize: 11, Color: t.Secondary(), Padding: core.Symmetric(0, 10), LineHeightMultiple: 1.5}})
	return column(children...)
}

func messageText(cx *core.Scope, text string) core.Node {
	t := tonesOf(cx)
	return core.Text{Content: text, TestID: "sessions-message", Style: core.Style{FontSize: 12, Color: t.Secondary(),
		TextAlign: core.TextAlignCenter, Padding: core.Symmetric(24, 10)}}
}

func (a *app) sessionSummary(cx *core.Scope, page *sessionPage) core.Node {
	t := tonesOf(cx)
	list := page.list
	snapshot := a.snapshot.Get()
	shared := snapshot != nil && snapshot.SharedSessions
	title := fmt.Sprintf("%d 个会话 · %d 个账号", len(list.Sessions), len(list.Accounts))
	detail := "暂无同步记录"
	var reasons []skipReason
	if record := list.LastSync; record != nil {
		report := record.Report
		detail = fmt.Sprintf("上次同步 %s · 新增 %d · 更新 %d", relativeTime(record.At, time.Now()),
			report.CodeCopied, report.CodeUpdated)
		if report.Skipped > 0 {
			detail += fmt.Sprintf(" · 跳过 %d", report.Skipped)
			reasons = skipReasons(report.Reasons)
		}
	}
	if !shared {
		detail = "会话共享已暂停 · " + detail
	}
	sync := tintButton(cx, lucide.RefreshCw, "立即同步", "立即同步会话", "sessions-sync",
		a.controlsDisabled() || len(list.Accounts) < 2, func() {
			a.page.Set("accounts")
			a.prepare("sync", profiles.Profile{})
		})
	text := []core.Node{
		label(title, 13, core.FontWeightSemibold, t.Text()),
		numberLabel(detail, 11, core.FontWeightNormal, t.Secondary()),
	}
	head := core.View{Style: core.Style{Direction: core.Row, AlignItems: core.AlignCenter, Gap: 10,
		Padding: core.Symmetric(12, 14)}, Children: []core.Node{
		core.View{Style: core.Style{FlexGrow: 1, FlexShrink: 1, MinWidth: core.Pt(0), Gap: 3}, Children: text},
		sync,
	}}
	children := []core.Node{head}
	if len(reasons) > 0 {
		open := a.sessionReasons.Get()
		icon := lucide.ChevronRight
		if open {
			icon = lucide.ChevronDown
		}
		children = append(children, core.View{Style: core.Style{Height: core.Pt(0.5), Margin: core.Edges{Left: 14},
			Background: t.Line()}}, pressable{Label: "跳过原因", TestID: "skip-reasons",
			OnPress: func() { a.sessionReasons.Set(!open) }, Style: core.Style{Direction: core.Row,
				AlignItems: core.AlignCenter, Gap: 6, Height: core.Pt(32), Padding: core.Symmetric(0, 14),
				Hover: &core.Style{Background: t.Hover()}}, Children: []core.Node{
				label("跳过原因", 12, core.FontWeightNormal, t.Secondary()), spacer(),
				ui.Icon(icon, 12, ui.Fixed(t.Tertiary())),
			}})
		if open {
			rows := []core.Node{}
			for _, reason := range reasons {
				rows = append(rows, core.View{Style: core.Style{Direction: core.Row, Gap: 8}, Children: []core.Node{
					label(reason.text, 12, core.FontWeightNormal, t.Text()), spacer(),
					numberLabel(fmt.Sprint(reason.count), 12, core.FontWeightNormal, t.Secondary()),
				}})
			}
			children = append(children, core.View{Style: core.Style{Gap: 6, Padding: core.Edges{Left: 14, Right: 14,
				Bottom: 12}}, Children: rows})
		}
	}
	return core.View{TestID: "sessions-summary", Style: core.Style{CornerRadius: 12, Background: t.Card(),
		BorderWidth: 0.5, BorderColor: t.CardBorder(), Overflow: core.Hidden, Shadows: t.CardShadow()},
		Children: children}
}

func (a *app) sessionFilters(cx *core.Scope, sessions []workspaces.Session) core.Node {
	t := tonesOf(cx)
	counts := map[string]int{sessionFilterAll: len(sessions)}
	for _, session := range sessions {
		if unsynced(session) {
			counts[sessionFilterUnsynced]++
		}
		if session.Issue != "" {
			counts[sessionFilterIssues]++
		}
	}
	tab := func(id, text string) core.Node {
		selected := a.sessionFilter.Get() == id
		style := core.Style{FlexGrow: 1, FlexBasis: core.Pt(0), Height: core.Pt(22), CornerRadius: 6,
			AlignItems: core.AlignCenter, JustifyContent: core.JustifyCenter}
		weight := core.FontWeightNormal
		if selected {
			style.Background, weight = t.Card(), core.FontWeightSemibold
			style.Shadows = []core.BoxShadow{ui.Shadow(0, 1, 2, 0, ui.Black(0.12)), core.Ring(0.5, t.BtnBorder())}
		}
		return pressable{Label: text, TestID: "session-filter-" + id, OnPress: func() { a.sessionFilter.Set(id) },
			Style: style, Children: []core.Node{
				numberLabel(fmt.Sprintf("%s %d", text, counts[id]), 12, weight, t.Text())}}
	}
	return core.View{Role: core.RoleTabList, Style: core.Style{Direction: core.Row, Gap: 2, Padding: core.All(2),
		CornerRadius: 8, Background: t.Fill()}, Children: []core.Node{
		tab(sessionFilterAll, "全部"), tab(sessionFilterUnsynced, "未全部同步"), tab(sessionFilterIssues, "有问题"),
	}}
}

// accountStack shows which accounts have the session; gray ones lack it.
func (a *app) accountStack(cx *core.Scope, accounts []profiles.SessionAccount, session workspaces.Session) core.Node {
	t := tonesOf(cx)
	snapshot := a.snapshot.Get()
	children := []core.Node{}
	missing := []string{}
	for i, account := range accounts {
		index := avatarIndex(snapshot, account.ProfileID)
		present := i < len(session.Present) && session.Present[i]
		if !present {
			index = -1
			missing = append(missing, account.Email)
		}
		mark := avatar(initial(account.Email), index, 18, t.Card())
		if i > 0 {
			mark = core.View{Style: core.Style{Margin: core.Edges{Left: -5}}, Children: []core.Node{mark}}
		}
		children = append(children, mark)
	}
	hint := "所有账号都有这个会话"
	if len(missing) > 0 {
		hint = "未同步到：" + strings.Join(missing, "、")
	}
	return withHint(cx, hint, core.View{Style: core.Style{Direction: core.Row, FlexShrink: 0}, Children: children})
}

func (a *app) sessionRow(cx *core.Scope, accounts []profiles.SessionAccount, session workspaces.Session) core.Node {
	t := tonesOf(cx)
	title := strings.TrimSpace(session.Title)
	if title == "" {
		title = "未命名会话"
	}
	at := sessionTime(session)
	clock := ""
	if !at.IsZero() {
		clock = at.In(time.Local).Format("15:04")
	}
	text := []core.Node{
		core.View{Style: core.Style{Direction: core.Row, AlignItems: core.AlignCenter, Gap: 8}, Children: []core.Node{
			core.Text{Content: title, MaxLines: 1, Style: core.Style{FontSize: 13, FontWeight: core.FontWeightMedium,
				Color: t.Text(), FlexGrow: 1, FlexShrink: 1, MinWidth: core.Pt(0)}},
			numberLabel(clock, 11, core.FontWeightNormal, t.Tertiary()),
		}},
	}
	if session.Cwd != "" {
		text = append(text, core.Text{Content: homePath(session.Cwd), MaxLines: 1, Hint: session.Cwd,
			Style: core.Style{FontSize: 11, FontFamily: core.FontMonospace, Color: t.Secondary()}})
	}
	if issue := issueText(session.Issue); issue != "" {
		text = append(text, core.View{Style: core.Style{Direction: core.Row, AlignItems: core.AlignCenter, Gap: 4},
			Children: []core.Node{ui.Icon(lucide.TriangleAlert, 11, ui.Fixed(t.WarnText())),
				label(issue, 11, core.FontWeightNormal, t.WarnText())}})
	}
	canReveal := session.Cwd != "" && session.Issue != workspaces.IssueWorkspaceMissing
	id := session.CLISessionID
	menu := ui.Menu{Label: "更多操作", TestID: "session-menu-" + id, Align: core.AlignEnd,
		Trigger: ui.Button{Variant: ui.ButtonGhost, Icon: lucide.Ellipsis, Label: "更多操作", Side: 22, IconSize: 14,
			Tint: ui.Fixed(t.Secondary())},
		Children: []core.Node{
			ui.MenuItem{Text: "在访达中显示工作目录", Icon: lucide.FolderOpen,
				Disabled: func() bool { return !canReveal },
				OnSelect: func() { a.revealDirectory(session.Cwd, "工作目录") }},
			ui.MenuItem{Text: "复制会话 ID", Icon: lucide.Copy, OnSelect: func() {
				a.cx.SetClipboard(id)
				a.showToast("已复制会话 ID")
			}},
		}}
	return core.View{TestID: "session-" + id, Style: core.Style{Direction: core.Row, AlignItems: core.AlignCenter,
		Gap: 10, Padding: core.Edges{Top: 9, Bottom: 9, Left: 14, Right: 8}}, Children: []core.Node{
		core.View{Style: core.Style{FlexGrow: 1, FlexShrink: 1, MinWidth: core.Pt(0), Gap: 3}, Children: text},
		a.accountStack(cx, accounts, session),
		menu,
	}}
}
