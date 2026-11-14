package desktopui

import (
	"math"
	"strings"
	"time"

	"claudeprofiles/profiles"
	"github.com/nsgo-dev/nsgo/core"
	"github.com/nsgo-dev/nsgo/icons/lucide"
	"github.com/nsgo-dev/nsgo/ui"
)

// meterTracks lines up every meter's label, bar, value and reset time.
const meterTracks core.GridTracks = "56px minmax(0, 1fr) 46px 104px"

func (a *app) profileCard(cx *core.Scope, profile profiles.Profile, index int, disabled bool, refresh string) core.Node {
	t := tonesOf(cx)
	waiting := profile.Active && profile.NeedsLogin
	children := []core.Node{a.cardHeader(cx, profile, index, disabled, waiting)}
	switch {
	case waiting:
		children = append(children, a.waitingSteps(cx)...)
	case !profile.NeedsLogin:
		children = append(children, a.cardBody(cx, profile, refresh), a.cardStrip(cx, profile, disabled, refresh))
	}
	style := core.Style{CornerRadius: 14, Background: t.Card(), BorderWidth: 0.5, BorderColor: t.CardBorder(),
		Shadows: t.CardShadow(), FlexShrink: 0}
	if profile.Active {
		style.BorderColor = t.ActiveBorder()
		style.Gradients = []core.Gradient{{Kind: core.GradientLinear, Angle: 180, Stops: []core.ColorStop{
			{Color: t.ActiveTint(), At: core.Pct(0)}, {Color: t.Card(), At: core.Pct(45)}}}}
	}
	return core.View{TestID: "profile-" + profile.ID, Label: accountEmail(profile), Style: style, Children: children}
}

func (a *app) cardHeader(cx *core.Scope, profile profiles.Profile, index int, disabled, waiting bool) core.Node {
	t := tonesOf(cx)
	email := accountEmail(profile)
	face := core.Node(avatar(initial(email), index, 34, 0))
	if profile.NeedsLogin {
		face = avatar("+", -1, 34, 0)
	}
	if profile.Active && !profile.NeedsLogin {
		face = core.View{Style: core.Style{Width: core.Pt(34), Height: core.Pt(34), FlexShrink: 0}, Children: []core.Node{
			face,
			core.View{Label: "当前使用", Style: core.Style{Position: core.Absolute, Right: core.Pt(-1), Bottom: core.Pt(-1),
				Width: core.Pt(12), Height: core.Pt(12), CornerRadius: 6, Background: t.Green(),
				BorderWidth: 2, BorderColor: t.Card()}},
		}}
	}
	title := email
	sub := subscriptionLine(profile)
	if waiting {
		title, sub = "新账号 · 等待登录", "在 Claude Desktop 窗口中登录"
		if a.addDuplicate.Get() {
			sub = "登录的账号重复了"
		}
	} else if profile.NeedsLogin {
		title = "新账号 · 待登录"
	}
	line := []core.Node{}
	if !profile.NeedsLogin && profile.Details != nil && profile.Details.Plan != "" {
		line = append(line, planBadge(cx, profile.Details.Plan))
	}
	if sub != "" {
		line = append(line, label(sub, 11, core.FontWeightNormal, t.Secondary()))
	}
	identity := core.View{Tooltip: hintNode(cx, cacheTooltip(profile)), Style: core.Style{FlexGrow: 1, FlexShrink: 1,
		MinWidth: core.Pt(0), Gap: 4}, Children: []core.Node{
		core.Text{Content: title, MaxLines: 1, Selectable: core.SelectableOn, TestID: "email-" + profile.ID,
			Style: core.Style{FontSize: 13, FontWeight: core.FontWeightSemibold, Color: t.Text(), MinWidth: core.Pt(0)}},
		core.View{Style: core.Style{Direction: core.Row, AlignItems: core.AlignCenter, Gap: 6, MinWidth: core.Pt(0)},
			Children: line},
	}}
	var action core.Node
	switch {
	case profile.Active:
		action = outlineButton(cx, lucide.ExternalLink, "打开 Claude", "打开 Claude", "open-"+profile.ID, disabled,
			a.openDesktop)
	case profile.NeedsLogin:
		action = tintButton(cx, lucide.LogIn, "登录", "切换到这个账号并登录", "switch-"+profile.ID, disabled,
			func() { a.prepare("switch", profile) })
	default:
		action = tintButton(cx, lucide.ArrowLeftRight, "切换", "切换到 "+email, "switch-"+profile.ID, disabled,
			func() { a.prepare("switch", profile) })
	}
	return core.View{Style: core.Style{Direction: core.Row, AlignItems: core.AlignCenter, Gap: 10,
		Padding: core.Symmetric(12, 14)}, Children: []core.Node{face, identity, action}}
}

func hintNode(cx *core.Scope, content string) core.Node {
	if content == "" {
		return nil
	}
	return hoverHint(content, ui.ColorsOf(cx))
}

// planBadge is the plan's colored tag: a gradient for paid plans, an outline for the free one.
func planBadge(cx *core.Scope, plan string) core.Node {
	t := tonesOf(cx)
	white := core.RGB(0xffffff)
	style := core.Style{Height: core.Pt(16), Padding: core.Symmetric(0, 6), CornerRadius: 5, FlexShrink: 0,
		AlignItems: core.AlignCenter, JustifyContent: core.JustifyCenter}
	color := white
	switch planKind(plan) {
	case planFree:
		color = t.Secondary()
		style.Shadows = []core.BoxShadow{core.Ring(1, t.BtnBorder())}
	case planPro:
		style.Gradients = []core.Gradient{core.LinearGradient(135, core.RGB(0x8e7cff), core.RGB(0x5b47e0))}
	case planMax:
		style.Gradients = []core.Gradient{core.LinearGradient(135, core.RGB(0xffc65c), core.RGB(0xf0742e))}
	case planUltra:
		color = core.RGB(0xf3d892)
		style.Gradients = []core.Gradient{core.LinearGradient(135, core.RGB(0x4a4a50), core.RGB(0x141416))}
		style.Shadows = []core.BoxShadow{core.Ring(0.5, core.RGBA(0xf3d892, 0.55))}
	case planTeam:
		style.Background, color = core.RGB(0xe2f5f1), core.RGB(0x127564)
		if t.Dark() {
			style.Background, color = core.RGBA(0x30b0a0, 0.2), core.RGB(0x5fd4c2)
		}
	case planEnterprise:
		style.Background, color = core.RGB(0x2c3a55), core.RGB(0xe3e9f5)
		if t.Dark() {
			style.Background = core.RGB(0x3a4560)
		}
	default:
		style.Background, color = t.Fill(), t.Secondary()
	}
	return core.View{TestID: "plan", Label: "套餐 " + plan, Style: style, Children: []core.Node{
		core.Text{Content: planLabel(plan), MaxLines: 1, Style: core.Style{FontSize: 9.5, FontWeight: core.FontWeightBold,
			LetterSpacing: 0.6, Color: color}},
	}}
}

type meterRow struct {
	name, reset, hint string
	value             *float64
}

func profileMeters(profile profiles.Profile, now time.Time) []meterRow {
	usage := profile.Usage
	if usage == nil {
		return []meterRow{{name: "5 小时", reset: "未查询"}, {name: "每周", reset: "未查询"}}
	}
	rows := []meterRow{
		{name: "5 小时", value: usage.FiveHour, reset: sessionResetText(usage.FiveHourReset, now)},
		{name: "每周", value: usage.SevenDay, reset: weeklyResetText(usage.SevenDayReset)},
	}
	if usage.FiveHourReset != nil {
		rows[0].hint = "5 小时已用 " + percent(usage.FiveHour) + "\n" + usageTime(*usage.FiveHourReset) + " 重置"
	}
	if usage.SevenDayReset != nil {
		rows[1].hint = "每周已用 " + percent(usage.SevenDay) + "\n" + usageTime(*usage.SevenDayReset) + " 重置"
	}
	for _, scoped := range usage.Scoped {
		name := strings.TrimSpace(scoped.Name)
		if name == "" {
			name = "模型额度"
		}
		row := meterRow{name: name, value: scoped.Percent, reset: weeklyResetText(scoped.ResetAt)}
		if scoped.AllowancePercentOfWeekly != nil {
			row.hint = name + " 最多占每周额度的 " + percent(scoped.AllowancePercentOfWeekly)
		}
		rows = append(rows, row)
	}
	return rows
}

func (a *app) cardBody(cx *core.Scope, profile profiles.Profile, refresh string) core.Node {
	t := tonesOf(cx)
	now := time.Now()
	rows := []core.Node{}
	for _, meter := range profileMeters(profile, now) {
		rows = append(rows, meterNode(cx, meter, profile.ID))
	}
	rows = append(rows, a.grantsRow(cx, profile, now))
	opacity := core.Alpha(1)
	switch {
	case refresh == refreshLoading:
		opacity = core.Alpha(0.4)
	case refresh == refreshFailed, refresh == "" && profile.RemoteUsageError != "":
		opacity = core.Alpha(0.6)
	}
	_ = t
	return core.View{Style: core.Style{Gap: 9, Padding: core.Edges{Top: 2, Left: 14, Right: 14, Bottom: 12},
		Opacity: opacity}, Children: rows}
}

func meterNode(cx *core.Scope, meter meterRow, id string) core.Node {
	t := tonesOf(cx)
	valueText, valueColor := "—", t.Tertiary()
	bar := core.Node(core.View{})
	if text := percent(meter.value); text != "—" {
		fill, color := t.meterColors(*meter.value)
		valueText, valueColor = text, color
		width := float32(math.Min(math.Max(*meter.value, 0), 100))
		bar = core.View{Style: core.Style{Width: core.Pct(width), Height: core.Pt(6), CornerRadius: 3,
			Gradients: []core.Gradient{core.LinearGradient(90, ui.Mix(fill, t.Card(), 0.3), fill)}}}
	}
	row := core.View{TestID: "meter-" + id, Label: meter.name + " 已用 " + valueText, Style: core.Style{
		Display: core.DisplayGrid, GridTemplateColumns: meterTracks, AlignItems: core.AlignCenter, Gap: 10,
		Height: core.Pt(18)}, Children: []core.Node{
		label(meter.name, 12, core.FontWeightNormal, t.Text()),
		core.View{Style: core.Style{Height: core.Pt(6), CornerRadius: 3, Background: t.Track(), Overflow: core.Hidden},
			Children: []core.Node{bar}},
		core.Text{Content: valueText, MaxLines: 1, Style: core.Style{FontSize: 14, FontWeight: core.FontWeightSemibold,
			Color: valueColor, TextAlign: core.TextAlignRight, FontVariant: numberVariant}},
		core.Text{Content: meter.reset, MaxLines: 1, Style: core.Style{FontSize: 11, Color: t.Tertiary(),
			TextAlign: core.TextAlignRight, FontVariant: numberVariant, MinWidth: core.Pt(0)}},
	}}
	return withHint(cx, meter.hint, row)
}

func (a *app) grantsRow(cx *core.Scope, profile profiles.Profile, now time.Time) core.Node {
	t := tonesOf(cx)
	chips := grantChips(profile.Usage, now)
	items := []core.Node{}
	if chips != nil && len(chips) == 0 {
		items = append(items, label("暂无", 11, core.FontWeightNormal, t.Tertiary()))
	}
	if chips == nil {
		items = append(items, label("未查询", 11, core.FontWeightNormal, t.Tertiary()))
	}
	for _, chip := range chips {
		foreground, background := t.Secondary(), t.Fill()
		if chip.urgent {
			foreground, background = t.WarnText(), t.WarnSoft()
		}
		// Each chip explains only its own grant when hovered.
		items = append(items, withHint(cx, chip.hint, core.View{Style: core.Style{Height: core.Pt(20),
			Padding: core.Symmetric(0, 8), CornerRadius: 10, Background: background, AlignItems: core.AlignCenter,
			JustifyContent: core.JustifyCenter}, Children: []core.Node{
			numberLabel(chip.text, 11, core.FontWeightSemibold, foreground)}}))
	}
	row := core.View{TestID: "grants-" + profile.ID, Style: core.Style{Display: core.DisplayGrid,
		GridTemplateColumns: "56px minmax(0, 1fr)", AlignItems: core.AlignCenter, Gap: 10, MinHeight: core.Pt(20)},
		Children: []core.Node{
			label("重置权益", 12, core.FontWeightNormal, t.Text()),
			core.View{Style: core.Style{Direction: core.Row, Wrap: core.Wrap, Gap: 6, AlignItems: core.AlignCenter},
				Children: items},
		}}
	return row
}

// cardStrip is the card's footer: its status line and the per-account actions.
func (a *app) cardStrip(cx *core.Scope, profile profiles.Profile, disabled bool, refresh string) core.Node {
	t := tonesOf(cx)
	text, color := metaLine(profile, time.Now()), t.Tertiary()
	switch {
	case refresh == refreshWaiting:
		text = "等待刷新"
	case refresh == refreshLoading:
		text, color = "正在查询额度…", t.Accent()
	case refresh == refreshDone:
		text, color = "✓ 刚刚更新", t.OKText()
	case refresh == refreshFailed, profile.RemoteUsageError != "":
		text, color = "刷新失败 · "+profile.RemoteUsageError+"，下面是上次的数据", t.DangerText()
		if profile.RemoteUsageError == "" {
			text = "刷新失败，下面是上次的数据"
		}
	}
	refreshColor := t.Secondary()
	if refresh == refreshLoading {
		refreshColor = t.Accent()
	} else if refresh == refreshFailed || profile.RemoteUsageError != "" {
		refreshColor = t.DangerText()
	}
	refreshButton := symbolButton(cx, lucide.RefreshCw, a.onlineRefreshHint(profile.ID), "refresh-usage-"+profile.ID,
		26, refreshColor, disabled || refreshCooldown(profile, time.Now()) || !a.canRefreshRemote(),
		func() { a.refreshRemoteUsage(profile.ID) })
	if refresh == refreshLoading {
		refreshButton = core.View{Style: core.Style{Animation: core.KeyframeAnimation{Keyframes: core.KeyframesSpin,
			Duration: 800 * time.Millisecond}}, Children: []core.Node{refreshButton}}
	}
	archiveLabel := "归档账号"
	if profile.Active {
		archiveLabel = "当前账号不能归档"
	}
	return core.View{Style: core.Style{Direction: core.Row, AlignItems: core.AlignCenter, Gap: 2, Height: core.Pt(34),
		Padding: core.Edges{Left: 14, Right: 8}, Borders: core.Edges{Top: 0.5}, BorderColor: t.Sep(),
		Background: t.Strip(), CornerRadii: core.Corners{BottomLeft: 14, BottomRight: 14}}, Children: []core.Node{
		core.Text{Content: text, MaxLines: 1, TestID: "meta-" + profile.ID, Hint: text, Style: core.Style{FontSize: 11,
			Color: color, FlexGrow: 1, FlexShrink: 1, MinWidth: core.Pt(0)}},
		refreshButton,
		symbolButton(cx, lucide.Folder, "在访达中显示配置文件夹", "folder-"+profile.ID, 26, t.Secondary(), disabled,
			func() { a.openProfileDirectory(profile.ID) }),
		symbolButton(cx, lucide.Archive, archiveLabel, "archive-"+profile.ID, 26, t.Secondary(),
			disabled || profile.Active, func() { a.prepare("archive", profile) }),
	}}
}

// waitingSteps show a newly added account's progress until its login is recognized.
func (a *app) waitingSteps(cx *core.Scope) []core.Node {
	t := tonesOf(cx)
	returnID := a.addReturn.Get()
	previous := ""
	if snapshot := a.snapshot.Get(); snapshot != nil {
		for _, profile := range snapshot.Profiles {
			if profile.ID == returnID {
				previous = accountEmail(profile)
			}
		}
	}
	duplicate := a.addDuplicate.Get()
	done := func(text string) core.Node {
		return stepRow(cx, core.View{Style: core.Style{Width: core.Pt(16), Height: core.Pt(16), CornerRadius: 8,
			Background: t.Green(), AlignItems: core.AlignCenter, JustifyContent: core.JustifyCenter},
			Children: []core.Node{ui.Icon(lucide.Check, 10, ui.Fixed(core.RGB(0xffffff)))}}, text, "", t.Secondary(), false)
	}
	steps := []core.Node{}
	if previous != "" {
		steps = append(steps, done("已保存 "+previous+" 的登录和数据"))
	}
	steps = append(steps, done("已用全新环境打开 Claude Desktop"))
	if duplicate {
		steps = append(steps, stepRow(cx, core.View{Style: core.Style{Width: core.Pt(16), Height: core.Pt(16),
			CornerRadius: 8, Background: t.Danger(), AlignItems: core.AlignCenter, JustifyContent: core.JustifyCenter},
			Children: []core.Node{ui.Icon(lucide.X, 10, ui.Fixed(core.RGB(0xffffff)))}},
			"这个账号已经在列表里了", "同一个账号只能保存一份。请在 Claude Desktop 里退出登录，换一个账号登录。",
			t.DangerText(), true))
	} else {
		spinner := core.View{Style: core.Style{Width: core.Pt(16), Height: core.Pt(16), AlignItems: core.AlignCenter,
			JustifyContent: core.JustifyCenter, Animation: core.KeyframeAnimation{Keyframes: core.KeyframesSpin,
				Duration: 900 * time.Millisecond}}, Children: []core.Node{ui.Icon(lucide.LoaderCircle, 14, ui.Fixed(t.Accent()))}}
		steps = append(steps, stepRow(cx, spinner, "等待你在 Claude Desktop 里登录…",
			"登录完成后会自动识别，不用回来点任何按钮。", t.Text(), true))
	}
	stripChildren := []core.Node{}
	if previous != "" && !duplicate {
		stripChildren = append(stripChildren, pressable{Label: "放弃添加", TestID: "abandon-add", Disabled: a.busy.Get(),
			OnPress: a.abandonAdd, Style: core.Style{Height: core.Pt(24), Padding: core.Symmetric(0, 6), CornerRadius: 6,
				AlignItems: core.AlignCenter, JustifyContent: core.JustifyCenter, Hover: &core.Style{Background: t.Hover()}},
			Children: []core.Node{label("放弃添加，切回 "+previous, 11, core.FontWeightMedium, t.Accent())}})
	}
	stripChildren = append(stripChildren, spacer())
	if !duplicate {
		stripChildren = append(stripChildren, label("每 5 秒检查一次", 11, core.FontWeightNormal, t.Tertiary()))
	}
	return []core.Node{
		core.View{Style: core.Style{Gap: 8, Padding: core.Edges{Left: 58, Right: 14, Bottom: 12}}, Children: steps},
		core.View{Style: core.Style{Direction: core.Row, AlignItems: core.AlignCenter, Gap: 8, Height: core.Pt(34),
			Padding: core.Edges{Left: 8, Right: 14}, Borders: core.Edges{Top: 0.5}, BorderColor: t.Sep(),
			Background: t.Strip(), CornerRadii: core.Corners{BottomLeft: 14, BottomRight: 14}}, Children: stripChildren},
	}
}

func stepRow(cx *core.Scope, mark core.Node, text, hint string, color core.Color, bold bool) core.Node {
	t := tonesOf(cx)
	weight := core.FontWeightNormal
	if bold {
		weight = core.FontWeightSemibold
	}
	lines := []core.Node{core.Text{Content: text, Style: core.Style{FontSize: 12, FontWeight: weight, Color: color}}}
	if hint != "" {
		lines = append(lines, core.Text{Content: hint, Style: core.Style{FontSize: 11, Color: t.Secondary()}})
	}
	return core.View{Style: core.Style{Direction: core.Row, AlignItems: core.AlignStart, Gap: 8}, Children: []core.Node{
		mark, core.View{Style: core.Style{FlexGrow: 1, FlexShrink: 1, MinWidth: core.Pt(0), Gap: 2}, Children: lines},
	}}
}
