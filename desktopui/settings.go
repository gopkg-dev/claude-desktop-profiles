package desktopui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"claudeprofiles/httplog"
	"claudeprofiles/profiles"
	"github.com/nsgo-dev/nsgo/core"
	"github.com/nsgo-dev/nsgo/icons/lucide"
	"github.com/nsgo-dev/nsgo/ui"
)

// Version is the app's version, shown on the about page.
var Version = "dev"

type requestLogStore interface {
	RequestLogDirectory() string
	RequestLog() (httplog.Summary, error)
	ClearRequestLog() error
}

// homePath shows a path under the home directory with a tilde.
func homePath(path string) string {
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		if rel, err := filepath.Rel(home, path); err == nil && !strings.HasPrefix(rel, "..") {
			return filepath.Join("~", rel)
		}
	}
	return path
}

func byteSize(size int64) string {
	switch {
	case size >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(size)/(1<<20))
	case size >= 1<<10:
		return fmt.Sprintf("%.0f KB", float64(size)/(1<<10))
	default:
		return fmt.Sprintf("%d B", size)
	}
}

// relativeTime says when, as today's or yesterday's time or a date.
func relativeTime(at, now time.Time) string {
	local, today := at.In(time.Local), now.In(time.Local)
	switch {
	case sameDay(local, today):
		return "今天 " + local.Format("15:04")
	case sameDay(local, today.AddDate(0, 0, -1)):
		return "昨天 " + local.Format("15:04")
	default:
		return local.Format("01-02 15:04")
	}
}

// pageBar is a secondary page's title bar: a back button and the page's name.
func (a *app) pageBar(cx *core.Scope, title string, trailing ...core.Node) core.Node {
	t := tonesOf(cx)
	children := []core.Node{
		glassCapsule(cx, toolButton(cx, lucide.ChevronLeft, "返回账号", "back", t.Text(), false, false,
			func() { a.page.Set("accounts") })),
		core.Text{Content: title, MaxLines: 1, Style: core.Style{FontSize: 13, FontWeight: core.FontWeightSemibold,
			Color: t.Text(), FlexGrow: 1, FlexShrink: 1, MinWidth: core.Pt(0)}},
	}
	return titleBar(cx, append(children, trailing...)...)
}

// formGroup is one grouped section of a macOS-style form, with its heading and footnote.
func formGroup(cx *core.Scope, heading, note string, rows ...core.Node) core.Node {
	t := tonesOf(cx)
	items := []core.Node{}
	for i, row := range rows {
		if i > 0 {
			items = append(items, core.View{Style: core.Style{Height: core.Pt(0.5), Margin: core.Symmetric(0, 14),
				Background: t.Line()}})
		}
		items = append(items, row)
	}
	children := []core.Node{
		core.Text{Content: heading, Style: core.Style{FontSize: 11, FontWeight: core.FontWeightSemibold,
			Color: t.Secondary(), Padding: core.Symmetric(0, 10)}},
		core.View{Style: core.Style{CornerRadius: 12, Background: t.Card(), BorderWidth: 0.5, BorderColor: t.CardBorder(),
			Overflow: core.Hidden}, Children: items},
	}
	if note != "" {
		children = append(children, core.Text{Content: note, Style: core.Style{FontSize: 11, Color: t.Secondary(),
			Padding: core.Symmetric(0, 10), LineHeightMultiple: 1.5}})
	}
	return core.View{Style: core.Style{Gap: 6}, Children: children}
}

// formRow is a 44-point form row: a title, an optional second line and a trailing control.
func formRow(cx *core.Scope, title, detail string, trailing ...core.Node) core.Node {
	t := tonesOf(cx)
	text := []core.Node{core.Text{Content: title, MaxLines: 1, Style: core.Style{FontSize: 13, Color: t.Text()}}}
	height := core.Points(44)
	if detail != "" {
		height = 52
		text = append(text, core.Text{Content: detail, MaxLines: 1, Style: core.Style{FontSize: 11, Color: t.Secondary(),
			FontVariant: numberVariant}})
	}
	children := []core.Node{core.View{Style: core.Style{FlexGrow: 1, FlexShrink: 1, MinWidth: core.Pt(0), Gap: 2},
		Children: text}}
	return core.View{Style: core.Style{Direction: core.Row, AlignItems: core.AlignCenter, Gap: 10,
		MinHeight: core.Pt(height), Padding: core.Symmetric(4, 14)}, Children: append(children, trailing...)}
}

func (a *app) settingsView() core.Node {
	a.cx.Effect(func() {
		if a.page.Get() == "settings" {
			a.cx.Post(func() {
				a.loadRequestLog()
				if a.sessions.GetUntracked() == nil {
					a.loadSessions()
				}
			})
		}
	})
	scrolled := core.NewSignal(a.cx, false)
	return core.Dynamic(func(cx *core.Scope) core.Node {
		t := tonesOf(cx)
		content := core.Dynamic(func(cx *core.Scope) core.Node {
			return core.View{Style: core.Style{Gap: 16, Padding: core.Edges{Left: 16, Right: 16, Bottom: 20}},
				Children: []core.Node{
					a.refreshSettings(cx), a.sharingSettings(cx), a.requestLogSettings(cx),
					a.exitSettings(cx), a.otherSettings(cx),
					core.Text{ContentFunc: a.settingsMessage.Get, TestID: "settings-message",
						Style: core.Style{FontSize: 11, Color: t.Accent(), Padding: core.Symmetric(0, 10)}},
				}}
		})
		return frostedPage(cx, "settings", scrolled, titleBarHeight+4, content, a.pageBar(cx, "设置"))
	})
}

// frostedPage scrolls content under its title bar, which floats over it translucent and blurred, as macOS
// toolbars do; a hairline under the bar shows once the content has scrolled beneath it.
func frostedPage(cx *core.Scope, id string, scrolled *core.Signal[bool], inset core.Points, content core.Node,
	bar ...core.Node) core.Node {
	t := tonesOf(cx)
	scroll := core.Scroll{TestID: id + "-scroll", OnScroll: func(event core.ScrollEvent) {
		scrolled.Set(event.Offset.Y > 0.5)
	}, Style: core.Style{Position: core.Absolute, Top: core.Pt(0), Left: core.Pt(0), Right: core.Pt(0),
		Bottom: core.Pt(0)}, Children: []core.Node{
		core.View{Style: core.Style{Padding: core.Edges{Top: inset}}, Children: []core.Node{content}},
	}}
	header := core.View{TestID: id + "-bar", StyleFunc: func() core.Style {
		style := core.Style{Position: core.Absolute, Top: core.Pt(0), Left: core.Pt(0), Right: core.Pt(0),
			Background: t.Bar(), BackdropFilters: []core.Filter{core.Blur(20)}}
		if scrolled.Get() {
			style.Borders, style.BorderColor = core.Edges{Bottom: 0.5}, t.Line()
		}
		return style
	}, Children: bar}
	return core.View{TestID: id + "-page", Style: core.Style{Width: core.Pct(100), Height: core.Pct(100),
		Background: t.Window()}, Children: []core.Node{scroll, header}}
}

func (a *app) refreshSettings(cx *core.Scope) core.Node {
	_, supported := a.store.(settingsStore)
	ttl := ui.Select[int]{Label: "数据有效期", TestID: "cache-minutes",
		Value:    func() int { return a.settings.Get().CacheMinutes },
		Disabled: func() bool { return !supported }, Options: []ui.Option[int]{
			{Value: 1, Label: "1 分钟"}, {Value: 5, Label: "5 分钟"}, {Value: 15, Label: "15 分钟"}, {Value: 60, Label: "1 小时"}},
		OnChange: func(minutes int) {
			value := a.settings.Get()
			value.CacheMinutes = minutes
			a.saveSettings(value)
		}, Style: core.Style{Width: core.Pt(110)}}
	auto := greenSwitch(cx, func() bool { return a.settings.Get().AutoRefresh }, "过期后自动刷新", "auto-refresh",
		func() bool { return !supported }, func(enabled bool) {
			value := a.settings.Get()
			value.AutoRefresh = enabled
			a.saveSettings(value)
		})
	return formGroup(cx, "额度刷新", "同一账号至少间隔 1 分钟才能再查；失败会自动延后，并遵循服务端的 Retry-After。",
		formRow(cx, "数据有效期", "", ttl), formRow(cx, "过期后自动刷新", "", auto))
}

func (a *app) sharingSettings(cx *core.Scope) core.Node {
	t := tonesOf(cx)
	snapshot := a.snapshot.Get()
	shared, count := false, 0
	if snapshot != nil {
		shared, count = snapshot.SharedSessions, len(snapshot.Profiles)
	}
	disabled := a.controlsDisabled()
	toggle := greenSwitch(cx, func() bool { return shared }, "在账号之间共享 Code 会话", "settings-sharing",
		func() bool { return disabled || count < 2 && !shared }, func(enabled bool) {
			if enabled {
				a.enableSharing()
				return
			}
			a.pauseSharing()
		})
	summary := "查看"
	if page := a.sessions.Get(); page != nil && page.err == nil {
		summary = fmt.Sprintf("%d 个", len(page.list.Sessions))
		if page.list.LastSync != nil {
			summary += " · " + relativeTime(page.list.LastSync.At, time.Now()) + " 同步"
		}
	}
	manage := pressable{Label: "会话管理", TestID: "settings-sessions", OnPress: a.openSessions, Style: core.Style{
		Direction: core.Row, AlignItems: core.AlignCenter, Gap: 8, MinHeight: core.Pt(44), Padding: core.Edges{Left: 14, Right: 12},
		Hover: &core.Style{Background: t.Hover()}}, Children: []core.Node{
		core.Text{Content: "会话管理", Style: core.Style{FontSize: 13, Color: t.Text(), FlexGrow: 1}},
		numberLabel(summary, 13, core.FontWeightNormal, t.Secondary()),
		ui.Icon(lucide.ChevronRight, 13, ui.Fixed(t.Tertiary())),
	}}
	return formGroup(cx, "会话共享", "切换账号时自动合并。对话内容只有一份，在 ~/.claude/projects。",
		formRow(cx, "在账号之间共享 Code 会话", "", toggle), manage)
}

type requestLogView struct {
	loaded  bool
	summary httplog.Summary
	err     error
}

func (a *app) loadRequestLog() {
	store, ok := a.store.(requestLogStore)
	if !ok {
		return
	}
	go func() {
		summary, err := store.RequestLog()
		a.cx.Post(func() { a.requestLog.Set(requestLogView{loaded: true, summary: summary, err: err}) })
	}()
}

func (a *app) requestLogSettings(cx *core.Scope) core.Node {
	t := tonesOf(cx)
	store, ok := a.store.(requestLogStore)
	if !ok {
		return core.View{Style: core.Style{Display: core.DisplayNone}}
	}
	directory := store.RequestLogDirectory()
	view := a.requestLog.Get()
	detail := "正在统计…"
	switch {
	case view.err != nil:
		detail = "无法读取：" + view.err.Error()
	case view.loaded && view.summary.Exchanges == 0:
		detail = "暂无记录"
	case view.loaded:
		detail = fmt.Sprintf("%d 次请求 · %s", view.summary.Exchanges, byteSize(view.summary.Bytes))
		if !view.summary.Latest.IsZero() {
			detail += " · 最新 " + relativeTime(view.summary.Latest, time.Now())
		}
	}
	path := core.View{Style: core.Style{FlexGrow: 1, FlexShrink: 1, MinWidth: core.Pt(0), Gap: 3}, Children: []core.Node{
		core.Text{Content: homePath(directory), MaxLines: 1, Hint: directory, Selectable: core.SelectableOn,
			TestID: "request-log-path", Style: core.Style{FontSize: 12, FontFamily: core.FontMonospace, Color: t.Text()}},
		core.Text{Content: detail, MaxLines: 1, TestID: "request-log-summary",
			Style: core.Style{FontSize: 11, Color: t.Secondary(), FontVariant: numberVariant}},
	}}
	reveal := withHint(cx, "在访达中显示", pressable{Label: "在访达中显示请求日志目录", TestID: "reveal-request-log",
		OnPress: func() { a.revealDirectory(directory, "请求日志目录") }, Style: core.Style{Height: core.Pt(24),
			Width: core.Pt(32), CornerRadius: 7, AlignItems: core.AlignCenter, JustifyContent: core.JustifyCenter,
			Background: t.Fill(), Hover: &core.Style{Background: ui.Mix(t.Fill(), t.Text(), 0.06)},
			Active: &core.Style{Background: t.Pressed()}},
		Children: []core.Node{ui.Icon(lucide.Folder, 14, ui.Fixed(t.Text()))}})
	clear := formButton(cx, "清空…", "clear-request-log", t.DangerText(),
		!view.loaded || view.summary.Exchanges == 0 || a.busy.Get(),
		func() { a.prepare("clear-log", profiles.Profile{}) })
	row := core.View{Style: core.Style{Direction: core.Row, AlignItems: core.AlignCenter, Gap: 10, MinHeight: core.Pt(52),
		Padding: core.Symmetric(4, 14)}, Children: []core.Node{path, reveal, clear}}
	return formGroup(cx, "请求日志", fmt.Sprintf("原样记录在线查询的请求，含登录凭据，不要发给别人。只留最近 %d 次、最多 %d MB。",
		max(view.summary.MaxExchanges, 100), max(view.summary.MaxBytes>>20, 20)), row)
}

func (a *app) exitSettings(cx *core.Scope) core.Node {
	t := tonesOf(cx)
	return core.Dynamic(func(cx *core.Scope) core.Node {
		text := "重新查询"
		if a.exitBusy.Get() {
			text = "查询中…"
		}
		note := "走 Claude Desktop 的代理，启动时只显示上次结果。"
		if message := a.exitMessage.Get(); message != "" {
			note = message
		}
		return formGroup(cx, "出口 IP", note, formRow(cx, a.exitSummary(), a.exitPlace(),
			formButton(cx, text, "settings-refresh-exit", t.Text(), a.exitBusy.Get(), a.refreshExit)))
	})
}

func (a *app) otherSettings(cx *core.Scope) core.Node {
	t := tonesOf(cx)
	_, supported := a.store.(settingsStore)
	return formGroup(cx, "其他", "",
		formRow(cx, "清除显示缓存", "", formButton(cx, "清除…", "clear-online-cache", t.DangerText(),
			!supported || a.busy.Get(), func() { a.prepare("clear-cache", profiles.Profile{}) })))
}

// revealDirectory opens a directory in Finder.
func (a *app) revealDirectory(path, name string) {
	if path == "" {
		return
	}
	a.cx.OpenFile(core.SharedFile{Path: path}, func(opened bool, err error) {
		if err != nil || !opened {
			a.settingsMessage.Set("无法打开" + name)
		}
	})
}

func (a *app) clearRequestLog() (string, error) {
	store, ok := a.store.(requestLogStore)
	if !ok {
		return "", nil
	}
	if err := store.ClearRequestLog(); err != nil {
		return "", err
	}
	a.cx.Post(a.loadRequestLog)
	return "请求日志已清空", nil
}

func (a *app) clearOnlineCache() (string, error) {
	store, ok := a.store.(settingsStore)
	if !ok {
		return "", nil
	}
	if err := store.ClearOnlineCache(); err != nil {
		return "", err
	}
	return "显示缓存已清除，请手动在线刷新；请求冷却时间仍保留", nil
}
