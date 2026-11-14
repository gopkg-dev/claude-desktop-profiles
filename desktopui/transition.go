package desktopui

import (
	"time"

	"github.com/nsgo-dev/nsgo/core"
	"github.com/nsgo-dev/nsgo/icons/lucide"
	"github.com/nsgo-dev/nsgo/ui"

	"claudeprofiles/profiles"
)

// transition is an operation that quits and reopens Claude Desktop — a switch, an added account or a
// session sync — shown step by step while it runs, since quitting alone can take a while.
type transition struct {
	kind     string
	from, to profiles.Profile
	steps    []profiles.Step
	current  profiles.Step
	err      string
	done     bool
}

// transitionDone is how long a finished transition stays on screen before it closes itself.
const transitionDone = 1200 * time.Millisecond

// restarts reports whether an action quits and reopens Claude Desktop.
func restarts(kind string) bool { return kind == "switch" || kind == "add" || kind == "sync" }

// beginTransition shows the steps an action will take.
func (a *app) beginTransition(item *action) {
	snapshot := a.snapshot.Get()
	value := &transition{kind: item.kind, to: item.profile}
	shared := false
	if snapshot != nil {
		shared = snapshot.SharedSessions && len(snapshot.Profiles) > 1
		for _, profile := range snapshot.Profiles {
			if profile.Active {
				value.from = profile
			}
		}
	}
	switch item.kind {
	case "switch":
		value.steps = []profiles.Step{profiles.StepQuit, profiles.StepMove, profiles.StepLaunch}
		if shared && !item.profile.NeedsLogin {
			value.steps = []profiles.Step{profiles.StepQuit, profiles.StepSync, profiles.StepMove, profiles.StepLaunch}
		}
	case "add":
		value.steps = []profiles.Step{profiles.StepQuit, profiles.StepMove, profiles.StepLaunch}
	default:
		value.steps = []profiles.Step{profiles.StepQuit, profiles.StepSync, profiles.StepLaunch}
	}
	value.current = value.steps[0]
	a.transition.Set(value)
}

// reportStep is called by the store, on its goroutine, as each step begins.
func (a *app) reportStep(step profiles.Step) {
	a.cx.Post(func() {
		value := a.transition.GetUntracked()
		if value == nil || value.done || value.err != "" {
			return
		}
		next := *value
		next.current = step
		a.transition.Set(&next)
	})
}

// finishTransition shows how the operation ended: success closes the panel shortly after, a failure stays
// until dismissed.
func (a *app) finishTransition(err error) {
	value := a.transition.GetUntracked()
	if value == nil {
		return
	}
	next := *value
	if err != nil {
		next.err = err.Error()
		a.transition.Set(&next)
		return
	}
	next.done = true
	a.transition.Set(&next)
	a.cx.AfterFunc(transitionDone, func() {
		if current := a.transition.GetUntracked(); current != nil && current.done {
			a.transition.Set(nil)
		}
	})
}

func (value *transition) title() string {
	name := accountEmail(value.to)
	if value.to.NeedsLogin || value.to.Details == nil {
		name = "新账号"
	}
	switch {
	case value.err != "" && value.kind == "switch":
		return "切换未完成"
	case value.err != "" && value.kind == "add":
		return "添加未完成"
	case value.err != "":
		return "同步未完成"
	case value.done && value.kind == "switch":
		return "已切换到 " + name
	case value.done && value.kind == "add":
		return "请在 Claude Desktop 中登录"
	case value.done:
		return "会话已同步"
	case value.kind == "switch":
		return "正在切换到 " + name
	case value.kind == "add":
		return "正在准备新账号"
	default:
		return "正在同步会话"
	}
}

// stepText is a step's name and what it is doing while it runs.
func (value *transition) stepText(step profiles.Step) (string, string) {
	switch step {
	case profiles.StepQuit:
		return "退出 Claude Desktop", "等待它保存数据，可能需要十几秒"
	case profiles.StepSync:
		return "同步 Code 会话", "把其他账号的会话合并进来"
	case profiles.StepMove:
		if value.kind == "add" {
			return "准备全新的登录环境", "当前账号的数据原样保存"
		}
		return "切换账号数据", "移动本机的账号目录"
	default:
		if value.kind == "add" {
			return "打开 Claude Desktop", "在新窗口里登录要添加的账号"
		}
		return "重新打开 Claude Desktop", "马上就好"
	}
}

func (a *app) transitionView(cx *core.Scope, value *transition) core.Node {
	t := tonesOf(cx)
	snapshot := a.snapshot.Get()
	current := len(value.steps)
	for i, step := range value.steps {
		if step == value.current {
			current = i
		}
	}
	if value.done {
		current = len(value.steps)
	}
	rows := make([]core.Node, 0, len(value.steps))
	for i, step := range value.steps {
		name, detail := value.stepText(step)
		var mark core.Node
		color, weight := t.Tertiary(), core.FontWeightNormal
		switch {
		case i < current:
			mark, color = ui.Icon(lucide.CircleCheck, 16, ui.Fixed(t.Green())), t.Secondary()
		case i == current && value.err != "":
			mark, color, weight = ui.Icon(lucide.CircleX, 16, ui.Fixed(t.DangerText())), t.Text(), core.FontWeightMedium
		case i == current:
			mark, color, weight = ui.Spinner{Size: 15, Label: name}, t.Text(), core.FontWeightMedium
		default:
			mark = ui.Icon(lucide.Circle, 16, ui.Fixed(t.Line()))
		}
		text := []core.Node{core.Text{Content: name, Style: core.Style{FontSize: 13, FontWeight: weight, Color: color}}}
		if i == current && value.err == "" && !value.done {
			text = append(text, core.Text{Content: detail, TestID: "transition-detail",
				Style: core.Style{FontSize: 11, Color: t.Secondary()}})
		}
		rows = append(rows, core.View{TestID: "transition-step-" + string(step), Style: core.Style{Direction: core.Row,
			Gap: 10, AlignItems: core.AlignStart, MinHeight: core.Pt(22)}, Children: []core.Node{
			core.View{Style: core.Style{Width: core.Pt(16), Height: core.Pt(18), AlignItems: core.AlignCenter,
				JustifyContent: core.JustifyCenter}, Children: []core.Node{mark}},
			core.View{Style: core.Style{Gap: 2, FlexGrow: 1, FlexShrink: 1, MinWidth: core.Pt(0)}, Children: text},
		}})
	}

	header := a.transitionHeader(cx, value, snapshot)
	footer := []core.Node{}
	switch {
	case value.err != "":
		footer = append(footer,
			core.Text{Content: value.err, TestID: "transition-error", Style: core.Style{FontSize: 11, Color: t.DangerText(),
				TextAlign: core.TextAlignCenter, LineHeightMultiple: 1.4, Width: core.Pct(100)}},
			pressable{Label: "好", TestID: "transition-close", OnPress: func() { a.transition.Set(nil) }, Style: core.Style{
				Width: core.Pct(100), Height: core.Pt(28), CornerRadius: 14, Background: t.Fill(), AlignItems: core.AlignCenter,
				JustifyContent: core.JustifyCenter, Active: &core.Style{Opacity: core.Alpha(0.85)}},
				Children: []core.Node{label("好", 13, core.FontWeightMedium, t.Text())}})
	case !value.done:
		footer = append(footer, core.Text{Content: "完成后会自动收起，请勿退出本应用", Style: core.Style{FontSize: 11,
			Color: t.Tertiary(), TextAlign: core.TextAlignCenter, Width: core.Pct(100)}})
	}
	return core.Overlay{Style: core.Style{Top: core.Pt(0), Left: core.Pt(0), Width: core.Pct(100), Height: core.Pct(100)},
		Children: []core.Node{core.View{TestID: "transition-panel", Style: core.Style{Width: core.Pct(100), Height: core.Pct(100),
			Background: t.Scrim(), AlignItems: core.AlignCenter, JustifyContent: core.JustifyCenter}, Children: []core.Node{
			core.View{Role: core.RoleAlert, Label: value.title(), Style: core.Style{Width: core.Pt(292), Gap: 14,
				Padding: core.Edges{Top: 22, Left: 20, Right: 20, Bottom: 18}, CornerRadius: 22, Background: t.Alert(),
				BorderWidth: 0.5, BorderColor: t.GlassBorder(), Shadows: []core.BoxShadow{ui.Shadow(0, 22, 60, 0, ui.Black(0.3))}},
				Children: append([]core.Node{
					header,
					core.Text{Content: value.title(), TestID: "transition-title", MaxLines: 2, Style: core.Style{FontSize: 13,
						FontWeight: core.FontWeightBold, Color: t.Text(), TextAlign: core.TextAlignCenter, Width: core.Pct(100)}},
					core.View{Style: core.Style{Gap: 9, Padding: core.Symmetric(12, 14), CornerRadius: 12, Background: t.Card(),
						BorderWidth: 0.5, BorderColor: t.CardBorder()}, Children: rows},
				}, footer...)},
		}}}}
}

// transitionHeader shows where the operation goes: from one account's avatar to another's, to a new account, or
// the current account's sessions.
func (a *app) transitionHeader(cx *core.Scope, value *transition, snapshot *profiles.Snapshot) core.Node {
	t := tonesOf(cx)
	face := func(profile profiles.Profile) core.Node {
		return avatar(initial(accountEmail(profile)), avatarIndex(snapshot, profile.ID), 44, 0)
	}
	badge := func(icon func() *core.SVG, color, background core.Color) core.Node {
		return core.View{Style: core.Style{Width: core.Pt(44), Height: core.Pt(44), CornerRadius: 22, Background: background,
			AlignItems: core.AlignCenter, JustifyContent: core.JustifyCenter},
			Children: []core.Node{ui.Icon(icon, 20, ui.Fixed(color))}}
	}
	var left, right core.Node
	switch value.kind {
	case "switch":
		left, right = face(value.from), face(value.to)
	case "add":
		left, right = face(value.from), badge(lucide.UserPlus, t.Accent(), t.AccentSoft())
	default:
		left, right = badge(lucide.MessagesSquare, t.Accent(), t.AccentSoft()), face(value.from)
	}
	arrowColor := t.Tertiary()
	arrow := lucide.ArrowRight
	switch {
	case value.err != "":
		arrowColor, arrow = t.DangerText(), lucide.X
	case value.done:
		arrowColor, arrow = t.Green(), lucide.Check
	}
	return core.View{Style: core.Style{Direction: core.Row, AlignItems: core.AlignCenter, JustifyContent: core.JustifyCenter,
		Gap: 14}, Children: []core.Node{left, ui.Icon(arrow, 18, ui.Fixed(arrowColor)), right}}
}
