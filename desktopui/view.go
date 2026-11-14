package desktopui

import (
	"fmt"
	"strings"
	"time"

	"claudeprofiles/profiles"
	"github.com/nsgo-dev/nsgo/core"
	"github.com/nsgo-dev/nsgo/desktop"
	"github.com/nsgo-dev/nsgo/icons/lucide"
	"github.com/nsgo-dev/nsgo/ui"
)

// titleBarHeight is the height of the app's own title bar, the traffic lights centered in it.
const titleBarHeight core.Points = 52

func (a *app) hasManagedAccounts() bool {
	snapshot := a.snapshot.Get()
	return snapshot != nil && snapshot.ActiveID != ""
}

func (a *app) controlsDisabled() bool {
	snapshot := a.snapshot.Get()
	return a.busy.Get() || a.blocked.Get() || a.pending.Get() != nil ||
		(snapshot != nil && (snapshot.PendingRecovery || snapshot.Preview))
}

// titleBar is a page's top row: it moves the window and keeps clear of the traffic lights.
func titleBar(cx *core.Scope, children ...core.Node) core.Node {
	left := desktop.TitleBar(cx).Left
	if left < 70 {
		left = 78
	}
	return core.View{Desktop: core.DesktopViewProps{WindowDrag: core.WindowDragOn}, Style: core.Style{
		Direction: core.Row, AlignItems: core.AlignCenter, Gap: 10, Height: core.Pt(titleBarHeight), FlexShrink: 0,
		Padding: core.Edges{Left: left + 4, Right: 12}}, Children: children}
}

// glassCapsule groups toolbar buttons in a translucent capsule.
func glassCapsule(cx *core.Scope, children ...core.Node) core.Node {
	t := tonesOf(cx)
	return core.View{Style: core.Style{Direction: core.Row, AlignItems: core.AlignCenter, Height: core.Pt(32),
		Padding: core.All(2), CornerRadius: 16, Background: t.Glass(), BorderWidth: 0.5, BorderColor: t.GlassBorder(),
		Shadows: []core.BoxShadow{ui.Shadow(0, 1, 4, 0, ui.Black(0.08))}, FlexShrink: 0}, Children: children}
}

// toolButton is one button inside a glass capsule.
func toolButton(cx *core.Scope, icon func() *core.SVG, labelText, testID string, color core.Color, spin, disabled bool,
	press func()) core.Node {
	t := tonesOf(cx)
	glyph := ui.Icon(icon, 15, ui.Fixed(color))
	if spin {
		glyph = core.View{Style: core.Style{Animation: core.KeyframeAnimation{Keyframes: core.KeyframesSpin,
			Duration: 800 * time.Millisecond}}, Children: []core.Node{glyph}}
	}
	return withHint(cx, labelText, pressable{Label: labelText, TestID: testID, Disabled: disabled, OnPress: press,
		Style: core.Style{Width: core.Pt(34), Height: core.Pt(28), CornerRadius: 14,
			AlignItems: core.AlignCenter, JustifyContent: core.JustifyCenter,
			Hover: &core.Style{Background: t.Hover()}, Active: &core.Style{Background: t.Pressed()}},
		Children: []core.Node{glyph}})
}

func (a *app) accountView() core.Node {
	return core.Dynamic(func(cx *core.Scope) core.Node {
		t := tonesOf(cx)
		return core.View{TestID: "desktop-page", Style: core.Style{Width: core.Pct(100), Height: core.Pct(100),
			MinWidth: core.Pt(0), Background: t.Window(), Color: t.Text()}, Children: []core.Node{
			core.Dynamic(func(cx *core.Scope) core.Node { return a.mainTitleBar(cx) }),
			core.Dynamic(func(cx *core.Scope) core.Node { return a.notices(cx) }),
			core.Dynamic(func(cx *core.Scope) core.Node {
				if a.hasManagedAccounts() {
					return a.accountList(cx)
				}
				if snapshot := a.snapshot.Get(); snapshot != nil {
					return a.empty(cx, snapshot)
				}
				return a.messageState(cx, "正在读取本机账号…", "", nil)
			}),
			core.Dynamic(func(cx *core.Scope) core.Node { return a.footer(cx) }),
		}}
	})
}

// subtitle is the gray text beside the app's name: the refresh's progress, a running operation or the count.
func (a *app) subtitle(snapshot *profiles.Snapshot) (string, string, bool) {
	progress := a.progress.Get()
	switch {
	case progress.running:
		return fmt.Sprintf("刷新中 %d/%d", max(progress.current, 1), progress.total), "", true
	case !progress.finished.IsZero() && progress.stopped:
		return fmt.Sprintf("已停止 · 更新 %d/%d", progress.succeeded, progress.total), "", false
	case !progress.finished.IsZero():
		fail := ""
		if progress.failures > 0 {
			fail = fmt.Sprintf(" · 失败 %d", progress.failures)
		}
		return fmt.Sprintf("已更新 %d", progress.succeeded), fail, false
	case a.busy.Get():
		return strings.TrimSuffix(a.status.Get(), "…") + "…", "", true
	}
	count := 0
	if snapshot != nil {
		count = len(snapshot.Profiles)
	}
	return fmt.Sprintf("%d 个账号", count), "", false
}

func (a *app) mainTitleBar(cx *core.Scope) core.Node {
	t := tonesOf(cx)
	snapshot := a.snapshot.Get()
	text, fail, highlighted := a.subtitle(snapshot)
	subColor := t.Tertiary()
	if highlighted {
		subColor = t.Accent()
	}
	sub := []core.Node{numberLabel(text, 11, core.FontWeightNormal, subColor)}
	if fail != "" {
		sub = append(sub, numberLabel(fail, 11, core.FontWeightNormal, t.DangerText()))
	}
	managed := snapshot != nil && snapshot.ActiveID != ""
	disabled := a.controlsDisabled()
	refreshColor, refreshLabel := t.Text(), a.onlineRefreshHint("")
	press := func() { a.refreshRemoteUsage("") }
	refreshDisabled := !managed || disabled || !a.canRefreshRemote()
	if a.remoteRefreshing {
		refreshColor, refreshLabel, press, refreshDisabled = t.Accent(), "停止刷新", a.stopRemoteRefresh, false
	}
	return titleBar(cx,
		core.View{Style: core.Style{Direction: core.Row, AlignItems: core.AlignCenter, Gap: 8, FlexGrow: 1,
			MinWidth: core.Pt(0)}, Children: []core.Node{
			// The title gives way before the buttons do, should the window ever be narrower than it fits.
			core.Text{Content: "Claude Desktop Profiles", MaxLines: 1, Style: core.Style{FontSize: 13,
				FontWeight: core.FontWeightSemibold, Color: t.Text(), FlexShrink: 0.2, MinWidth: core.Pt(0)}},
			core.View{TestID: "title-status", Tooltip: hoverHint(a.status.Get(), ui.ColorsOf(cx)),
				Style:    core.Style{Direction: core.Row, MinWidth: core.Pt(0), FlexShrink: 1, Overflow: core.Hidden},
				Children: sub},
		}},
		glassCapsule(cx,
			toolButton(cx, lucide.RefreshCw, refreshLabel, "refresh-usage", refreshColor, a.remoteRefreshing,
				refreshDisabled, press),
			toolButton(cx, lucide.Plus, "添加账号", "add", t.Text(), false, !managed || disabled,
				func() { a.prepare("add", profiles.Profile{}) }),
			toolButton(cx, lucide.Info, "关于", "open-about", t.Text(), false, false,
				func() { a.page.Set("about") }),
			toolButton(cx, lucide.Settings, "设置", "open-settings", t.Text(), false, false,
				func() { a.page.Set("settings") }),
		),
	)
}

// notices shows a failed operation or an unfinished switch under the title bar.
func (a *app) notices(cx *core.Scope) core.Node {
	t := tonesOf(cx)
	items := []core.Node{}
	if snapshot := a.snapshot.Get(); snapshot != nil && snapshot.PendingRecovery {
		message := snapshot.RecoveryMessage
		var action core.Node
		if message == "" {
			message = "上次账号操作没有完成，先恢复再继续。"
			action = formButton(cx, "恢复", "recover", t.Text(), a.busy.Get(),
				func() { a.prepare("recover", profiles.Profile{}) })
		}
		items = append(items, a.banner(cx, "需要恢复", message, t.WarnText(), t.WarnSoft(), action, nil))
	}
	if message := a.failure.Get(); message != "" && !(a.addReturn.Get() != "" && a.addDuplicate.Get()) {
		items = append(items, a.banner(cx, "操作未完成", message, t.DangerText(),
			ui.Mix(t.Card(), t.Danger(), t.pick(0.08, 0.16)), nil, func() { a.failure.Set("") }))
	}
	if len(items) == 0 {
		return core.View{Style: core.Style{Display: core.DisplayNone}}
	}
	return core.View{Style: core.Style{Gap: 8, Padding: core.Edges{Left: 14, Right: 14, Bottom: 8}}, Children: items}
}

func (a *app) banner(cx *core.Scope, title, message string, color, background core.Color, action core.Node,
	dismiss func()) core.Node {
	t := tonesOf(cx)
	children := []core.Node{
		ui.Icon(lucide.TriangleAlert, 14, ui.Fixed(color)),
		core.View{Style: core.Style{FlexGrow: 1, FlexShrink: 1, MinWidth: core.Pt(0), Gap: 2}, Children: []core.Node{
			label(title, 12, core.FontWeightSemibold, color),
			core.Text{Content: message, MaxLines: 3, TestID: "notice-message",
				Style: core.Style{FontSize: 11, Color: t.Secondary(), MinWidth: core.Pt(0)}},
		}},
	}
	if action != nil {
		children = append(children, action)
	}
	if dismiss != nil {
		children = append(children, symbolButton(cx, lucide.X, "关闭提示", "dismiss-notice", 22, t.Secondary(), false, dismiss))
	}
	return core.View{TestID: "notice", Style: core.Style{Direction: core.Row, AlignItems: core.AlignStart, Gap: 8,
		Padding: core.Symmetric(9, 12), CornerRadius: 10, Background: background}, Children: children}
}

func (a *app) accountList(cx *core.Scope) core.Node {
	return core.Scroll{TestID: "account-scroll", Style: core.Style{FlexGrow: 1, FlexShrink: 1, MinHeight: core.Pt(0)},
		Children: []core.Node{core.Dynamic(func(cx *core.Scope) core.Node {
			snapshot := a.snapshot.Get()
			rows := orderedProfiles(snapshot)
			disabled := a.controlsDisabled()
			progress := a.progress.Get()
			items := make([]core.Node, 0, len(rows))
			for _, profile := range rows {
				items = append(items, a.profileCard(cx, profile, avatarIndex(snapshot, profile.ID), disabled,
					progress.state(profile.ID)))
			}
			return core.View{Style: core.Style{Gap: 10, Padding: core.Edges{Top: 2, Left: 14, Right: 14, Bottom: 14}},
				Children: items}
		})}}
}

func (a *app) footer(cx *core.Scope) core.Node {
	t := tonesOf(cx)
	snapshot := a.snapshot.Get()
	count, shared := 0, false
	if snapshot != nil {
		count, shared = len(snapshot.Profiles), snapshot.SharedSessions
	}
	disabled := a.controlsDisabled()
	on := func() bool { return shared }
	toggle := greenSwitch(cx, on, "会话共享", "sharing", func() bool { return disabled || count < 2 && !shared },
		func(enabled bool) {
			if enabled {
				a.enableSharing()
				return
			}
			a.pauseSharing()
		})
	link := pressable{Label: "会话管理", TestID: "open-sessions", OnPress: a.openSessions, Style: core.Style{
		Direction: core.Row, AlignItems: core.AlignCenter, Gap: 4, Height: core.Pt(22), Padding: core.Symmetric(0, 2),
		CornerRadius: 5, Hover: &core.Style{Opacity: core.Alpha(0.7)}}, Children: []core.Node{
		label("会话共享", 12, core.FontWeightNormal, t.Text()),
		ui.Icon(lucide.ChevronRight, 11, ui.Fixed(t.Tertiary())),
	}}
	sync := pressable{Label: "立即同步", TestID: "sync", Disabled: disabled || count < 2,
		OnPress: func() { a.prepare("sync", profiles.Profile{}) }, Style: core.Style{
			Direction: core.Row, AlignItems: core.AlignCenter, Gap: 4, Height: core.Pt(22), Padding: core.Symmetric(0, 8),
			CornerRadius: 6, Hover: &core.Style{Background: t.AccentSoft()}}, Children: []core.Node{
			ui.Icon(lucide.RefreshCw, 12, ui.Fixed(t.Accent())),
			label("立即同步", 12, core.FontWeightMedium, t.Accent()),
		}}
	return core.View{TestID: "account-footer", Style: core.Style{Direction: core.Row, AlignItems: core.AlignCenter,
		Gap: 6, Height: core.Pt(38), FlexShrink: 0, Padding: core.Symmetric(0, 14), Borders: core.Edges{Top: 0.5},
		BorderColor: t.Line()}, Children: []core.Node{
		toggle, link, withHint(cx, "合并其他账号的 Code 会话，需重启 Claude Desktop", sync), spacer(),
		a.exitLabel(cx),
	}}
}

func (a *app) toastView(cx *core.Scope) core.Node {
	message := a.toast.Get()
	if message == "" {
		return core.View{Style: core.Style{Display: core.DisplayNone}}
	}
	t := tonesOf(cx)
	return core.Overlay{PointerEvents: core.PointerEventsBoxNone, Style: core.Style{Left: core.Pt(0), Width: core.Pct(100),
		Bottom: core.Pt(50), AlignItems: core.AlignCenter}, Children: []core.Node{
		core.View{TestID: "toast", Label: message, Style: core.Style{Direction: core.Row, AlignItems: core.AlignCenter,
			Gap: 7, Height: core.Pt(32), Padding: core.Edges{Left: 10, Right: 14}, CornerRadius: 16,
			Background: t.Toast(), Shadows: []core.BoxShadow{ui.Shadow(0, 8, 24, 0, ui.Black(0.2))}},
			Children: []core.Node{
				core.View{Style: core.Style{Width: core.Pt(16), Height: core.Pt(16), CornerRadius: 8,
					Background: t.Green(), AlignItems: core.AlignCenter, JustifyContent: core.JustifyCenter},
					Children: []core.Node{ui.Icon(lucide.Check, 10, ui.Fixed(core.RGB(0xffffff)))}},
				label(message, 12, core.FontWeightNormal, core.RGB(0xffffff)),
			}},
	}}
}

func (a *app) messageState(cx *core.Scope, title, message string, button core.Node) core.Node {
	t := tonesOf(cx)
	items := []core.Node{core.Text{Content: title, Style: core.Style{FontSize: 15, FontWeight: core.FontWeightSemibold,
		Color: t.Text()}}}
	if message != "" {
		items = append(items, core.Text{Content: message, Style: core.Style{FontSize: 12, Color: t.Secondary(),
			TextAlign: core.TextAlignCenter}})
	}
	if button != nil {
		items = append(items, button)
	}
	return core.View{Style: core.Style{FlexGrow: 1, AlignItems: core.AlignCenter, JustifyContent: core.JustifyCenter,
		Gap: 12, Padding: core.All(32)}, Children: items}
}

func (a *app) empty(cx *core.Scope, snapshot *profiles.Snapshot) core.Node {
	disabled := a.controlsDisabled()
	if snapshot.CurrentAccount != "" {
		return a.messageState(cx, "把当前账号加进来", "沿用 Claude Desktop 现在的登录，账号会以邮箱显示。",
			tintButton(cx, lucide.UserPlus, "接管当前登录", "接管当前登录", "adopt", disabled,
				func() { a.prepare("adopt", profiles.Profile{}) }))
	}
	return a.messageState(cx, "先登录 Claude Desktop", "登录完成后会自动识别，然后就能接管这个账号。",
		outlineButton(cx, lucide.ExternalLink, "打开 Claude", "打开 Claude Desktop", "open-desktop", disabled,
			a.openDesktop))
}
