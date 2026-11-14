package desktopui

import (
	"strings"

	"github.com/nsgo-dev/nsgo/base"
	"github.com/nsgo-dev/nsgo/core"
	"github.com/nsgo-dev/nsgo/ui"
)

// numberStyle lines figures up in columns.
const numberVariant = core.FontTabularNums

func label(value string, size core.Points, weight core.FontWeight, color core.Color) core.Node {
	return core.Text{Content: value, MaxLines: 1, Style: core.Style{FontSize: size, FontWeight: weight, Color: color,
		MinWidth: core.Pt(0), FlexShrink: 1}}
}

func numberLabel(value string, size core.Points, weight core.FontWeight, color core.Color) core.Node {
	return core.Text{Content: value, MaxLines: 1, Style: core.Style{FontSize: size, FontWeight: weight, Color: color,
		FontVariant: numberVariant}}
}

func spacer() core.Node { return core.View{Style: core.Style{FlexGrow: 1}} }

func hoverHint(content string, colors ui.Colors) core.Node {
	return base.Tooltip{Offset: 6, Margin: 8,
		PopupStyle: core.Style{Padding: core.Symmetric(7, 9), CornerRadius: 6, Background: colors.Text()},
		Children:   []core.Node{hintText(content, colors)},
	}
}

// hintWidth is the widest a tooltip's text runs before it wraps.
const hintWidth = 262

// hintText is a tooltip's text. The width limit sits on the text itself: on the popup alone,
// the text is measured as one line and the end is cut off instead of wrapping.
func hintText(content string, colors ui.Colors) core.Node {
	return core.Text{Content: content, TestID: "hint-text", Style: core.Style{FontSize: 11, Color: colors.Background(),
		MaxWidth: core.Pt(hintWidth), LineHeightMultiple: 1.25}}
}

// withHint wraps node in a tooltip when content is set.
func withHint(cx *core.Scope, content string, node core.Node) core.Node {
	if content == "" {
		return node
	}
	return core.View{Tooltip: hoverHint(content, ui.ColorsOf(cx)), Children: []core.Node{node}}
}

// pressable is a button with the given face; label is what assistive technology reads.
type pressable struct {
	Label    string
	TestID   string
	Disabled bool
	OnPress  func()
	Style    core.Style
	Children []core.Node
}

func (p pressable) Render(cx *core.Scope) core.Node {
	style := p.Style
	if style.FocusVisible == nil {
		style.FocusVisible = ui.FocusRing(cx, 2)
	}
	if style.Disabled == nil {
		style.Disabled = &core.Style{Opacity: core.Alpha(0.4)}
	}
	disabled := p.Disabled
	return base.Button{Label: p.Label, TestID: p.TestID, Disabled: func() bool { return disabled },
		OnPress: func(core.PressEvent) {
			if p.OnPress != nil {
				p.OnPress()
			}
		}, Style: style, Children: p.Children}
}

// symbolButton is a round, borderless icon button such as those at the foot of a card.
func symbolButton(cx *core.Scope, icon func() *core.SVG, labelText, testID string, size core.Points,
	color core.Color, disabled bool, press func()) core.Node {
	t := tonesOf(cx)
	return withHint(cx, labelText, pressable{Label: labelText, TestID: testID, Disabled: disabled, OnPress: press,
		Style: core.Style{Width: core.Pt(size), Height: core.Pt(size), CornerRadius: size / 2,
			AlignItems: core.AlignCenter, JustifyContent: core.JustifyCenter,
			Hover: &core.Style{Background: t.Hover()}, Active: &core.Style{Background: t.Pressed()}},
		Children: []core.Node{ui.Icon(icon, size*0.54, ui.Fixed(color))}})
}

// pillButton is a 26-point capsule with an icon and a word.
func pillButton(cx *core.Scope, icon func() *core.SVG, text, labelText, testID string, foreground, background core.Color,
	hover *core.Style, shadows []core.BoxShadow, disabled bool, press func()) core.Node {
	t := tonesOf(cx)
	if hover == nil {
		hover = &core.Style{Background: t.Hover()}
	}
	children := []core.Node{}
	if icon != nil {
		children = append(children, ui.Icon(icon, 12, ui.Fixed(foreground)))
	}
	children = append(children, label(text, 12, core.FontWeightSemibold, foreground))
	return pressable{Label: labelText, TestID: testID, Disabled: disabled, OnPress: press, Style: core.Style{
		Direction: core.Row, AlignItems: core.AlignCenter, Gap: 5, Height: core.Pt(26), FlexShrink: 0,
		Padding: core.Symmetric(0, 11), CornerRadius: 13, Background: background, Shadows: shadows,
		Hover: hover, Active: &core.Style{Opacity: core.Alpha(0.85)}}, Children: children}
}

// tintButton is the accent-tinted action that turns solid under the pointer.
func tintButton(cx *core.Scope, icon func() *core.SVG, text, labelText, testID string, disabled bool, press func()) core.Node {
	t := tonesOf(cx)
	return pillButton(cx, icon, text, labelText, testID, t.Accent(), t.AccentSoft(),
		&core.Style{Background: ui.Mix(t.AccentSoft(), t.Accent(), 0.18)}, nil, disabled, press)
}

// outlineButton is the white, hairline-bordered action used for opening Claude.
func outlineButton(cx *core.Scope, icon func() *core.SVG, text, labelText, testID string, disabled bool, press func()) core.Node {
	t := tonesOf(cx)
	return pillButton(cx, icon, text, labelText, testID, t.Text(), t.Card(), nil,
		[]core.BoxShadow{core.Ring(0.5, t.BtnBorder()), ui.Shadow(0, 0.5, 1.5, 0, ui.Black(0.08))}, disabled, press)
}

// formButton is a small macOS push button inside a grouped form.
func formButton(cx *core.Scope, text, testID string, color core.Color, disabled bool, press func()) core.Node {
	t := tonesOf(cx)
	return pressable{Label: text, TestID: testID, Disabled: disabled, OnPress: press, Style: core.Style{
		Height: core.Pt(24), Padding: core.Symmetric(0, 11), CornerRadius: 7, FlexShrink: 0,
		AlignItems: core.AlignCenter, JustifyContent: core.JustifyCenter, Background: t.Fill(),
		Hover: &core.Style{Background: ui.Mix(t.Fill(), t.Text(), 0.06)}, Active: &core.Style{Background: t.Pressed()}},
		Children: []core.Node{label(text, 13, core.FontWeightNormal, color)}}
}

// greenSwitch is the native-looking toggle that turns green when on.
func greenSwitch(cx *core.Scope, on func() bool, labelText, testID string, disabled func() bool, change func(bool)) core.Node {
	t := tonesOf(cx)
	return core.Dynamic(func(cx *core.Scope) core.Node {
		look := ui.NewSwitchLook(cx, on, ui.SizeSmall)
		return base.Switch{Checked: on, Disabled: disabled, Label: labelText, TestID: testID,
			TrackStyleFunc: func() core.Style {
				style := look.Track()
				if on() {
					style.Background, style.Animated = t.Green(), nil
				}
				return style
			}, ThumbStyleFunc: look.Thumb, OnChange: change}
	})
}

// avatarColors are the pastel gradients accounts take in turn.
var avatarColors = [][2]core.Color{
	{core.RGB(0x8fb4ff), core.RGB(0x4f74f0)}, {core.RGB(0xffc08a), core.RGB(0xee7b3e)},
	{core.RGB(0x86ddbd), core.RGB(0x2aa078)}, {core.RGB(0xd2b0ff), core.RGB(0x8b5cf6)},
	{core.RGB(0x9fddf7), core.RGB(0x3496d3)}, {core.RGB(0xffa8b8), core.RGB(0xe0567a)},
}

func avatarGradient(index int) []core.Gradient {
	if index < 0 {
		return []core.Gradient{core.LinearGradient(160, core.RGB(0xc7c7cc), core.RGB(0xa1a1a6))}
	}
	pair := avatarColors[index%len(avatarColors)]
	return []core.Gradient{core.LinearGradient(160, pair[0], pair[1])}
}

func initial(email string) string {
	for _, r := range strings.TrimSpace(email) {
		return strings.ToUpper(string(r))
	}
	return "?"
}

// avatar is a round gradient with the account's initial.
func avatar(text string, index int, size core.Points, ring core.Color) core.Node {
	style := core.Style{Width: core.Pt(size), Height: core.Pt(size), CornerRadius: size / 2, FlexShrink: 0,
		AlignItems: core.AlignCenter, JustifyContent: core.JustifyCenter, Gradients: avatarGradient(index)}
	if ring != 0 {
		style.BorderWidth, style.BorderColor = 1.5, ring
	}
	return core.View{Style: style, Children: []core.Node{
		core.Text{Content: text, Style: core.Style{FontSize: size * 0.41, FontWeight: core.FontWeightSemibold,
			Color: core.RGB(0xffffff)}},
	}}
}
