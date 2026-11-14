package desktopui

import (
	"fmt"

	"github.com/nsgo-dev/nsgo/core"
	"github.com/nsgo-dev/nsgo/icons/lucide"
	"github.com/nsgo-dev/nsgo/ui"
)

type alertCopy struct {
	title, message, confirm string
	icon                    func() *core.SVG
	danger                  bool
}

func (a *app) actionCopy(item *action) alertCopy {
	current := ""
	others := 0
	if snapshot := a.snapshot.Get(); snapshot != nil {
		for _, profile := range snapshot.Profiles {
			if profile.Active {
				current = accountEmail(profile)
			} else if !profile.NeedsLogin {
				others++
			}
		}
	}
	switch item.kind {
	case "adopt":
		return alertCopy{title: "接管当前登录？", icon: lucide.UserPlus, confirm: "加入列表",
			message: "沿用 Claude Desktop 现在的登录，并读取本机账号资料。"}
	case "add":
		message := "Claude Desktop 会退出，再以一个全新的环境打开，你在新窗口里登录要添加的账号即可。"
		if current != "" {
			message += current + " 的登录和数据会原样保存。"
		}
		return alertCopy{title: "添加新账号？", icon: lucide.UserPlus, confirm: "打开并登录", message: message}
	case "archive":
		return alertCopy{title: "归档 " + accountEmail(item.profile) + "？", icon: lucide.Archive, confirm: "归档",
			danger: true, message: "账号会从列表移除，数据保留在本机的归档目录，需要时可以找回。"}
	case "recover":
		return alertCopy{title: "恢复上次操作？", icon: lucide.RotateCw, confirm: "恢复并继续",
			message: "把账号数据和列表恢复到一致的状态。"}
	case "clear-log":
		return alertCopy{title: "清空请求日志？", icon: lucide.Trash, confirm: "清空", danger: true,
			message: "删除本机记录的全部在线查询请求，无法恢复。"}
	case "clear-cache":
		return alertCopy{title: "清除显示缓存？", icon: lucide.Eraser, confirm: "清除", danger: true,
			message: "账号资料和额度会显示为未查询，需要重新在线刷新；请求冷却时间仍保留。"}
	case "sync":
		return alertCopy{title: "立即同步会话？", icon: lucide.RefreshCw, confirm: "同步并重启",
			message: fmt.Sprintf("把其他 %d 个账号的 Code 会话合并到 %s。Claude Desktop 会先退出，同步完自动重新打开，请先结束正在运行的任务。",
				others, current)}
	default:
		return alertCopy{title: "切换到 " + accountEmail(item.profile) + "？", confirm: "切换",
			message: "Claude Desktop 会退出并以这个账号重新打开。请先结束正在运行的任务。"}
	}
}

// alert asks to confirm an action in a macOS-style alert over a dimmed window.
func (a *app) alert(cx *core.Scope, item *action) core.Node {
	t := tonesOf(cx)
	copy := a.actionCopy(item)
	var mark core.Node
	if copy.icon == nil {
		snapshot := a.snapshot.Get()
		mark = avatar(initial(accountEmail(item.profile)), avatarIndex(snapshot, item.profile.ID), 48, 0)
	} else {
		color, background := t.Accent(), t.AccentSoft()
		if copy.danger {
			color, background = t.DangerText(), ui.Mix(t.Card(), t.Danger(), t.pick(0.1, 0.18))
		}
		mark = core.View{Style: core.Style{Width: core.Pt(48), Height: core.Pt(48), CornerRadius: 24,
			Background: background, AlignItems: core.AlignCenter, JustifyContent: core.JustifyCenter},
			Children: []core.Node{ui.Icon(copy.icon, 22, ui.Fixed(color))}}
	}
	confirmColor := t.Accent()
	if copy.danger {
		confirmColor = t.Danger()
	}
	button := func(text, testID string, background, foreground core.Color, press func()) core.Node {
		return pressable{Label: text, TestID: testID, OnPress: press, Style: core.Style{FlexGrow: 1, FlexBasis: core.Pt(0),
			Height: core.Pt(28), CornerRadius: 14, Background: background, AlignItems: core.AlignCenter,
			JustifyContent: core.JustifyCenter, Active: &core.Style{Opacity: core.Alpha(0.85)}},
			Children: []core.Node{label(text, 13, core.FontWeightMedium, foreground)}}
	}
	return core.Overlay{OnCancel: func() { a.pending.Set(nil) }, Style: core.Style{Top: core.Pt(0), Left: core.Pt(0),
		Width: core.Pct(100), Height: core.Pct(100)}, Children: []core.Node{core.View{TestID: "action-panel",
		Style: core.Style{Width: core.Pct(100), Height: core.Pct(100), Background: t.Scrim(), AlignItems: core.AlignCenter,
			JustifyContent: core.JustifyCenter}, Children: []core.Node{
			core.View{Role: core.RoleAlert, Label: copy.title, Style: core.Style{Width: core.Pt(268),
				AlignItems: core.AlignCenter, Gap: 8, Padding: core.Edges{Top: 20, Left: 16, Right: 16, Bottom: 16},
				CornerRadius: 22, Background: t.Alert(), BorderWidth: 0.5, BorderColor: t.GlassBorder(),
				Shadows: []core.BoxShadow{ui.Shadow(0, 22, 60, 0, ui.Black(0.3))}}, Children: []core.Node{
				core.View{Style: core.Style{Margin: core.Edges{Bottom: 4}}, Children: []core.Node{mark}},
				core.Text{Content: copy.title, Style: core.Style{FontSize: 13, FontWeight: core.FontWeightBold,
					Color: t.Text(), TextAlign: core.TextAlignCenter, Width: core.Pct(100)}},
				core.Text{Content: copy.message, TestID: "action-message", Style: core.Style{FontSize: 11, Color: t.Secondary(),
					TextAlign: core.TextAlignCenter, LineHeightMultiple: 1.45, Width: core.Pct(100)}},
				core.View{Style: core.Style{Direction: core.Row, Gap: 8, Width: core.Pct(100), Margin: core.Edges{Top: 8}},
					Children: []core.Node{
						button("取消", "cancel", t.Fill(), t.Text(), func() { a.pending.Set(nil) }),
						button(copy.confirm, "confirm", confirmColor, core.RGB(0xffffff), a.submit),
					}},
			}},
		}}}}
}
