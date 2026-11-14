package desktopui

import (
	"github.com/nsgo-dev/nsgo/core"
	"github.com/nsgo-dev/nsgo/ui"
)

// claudeTheme is a macOS-like look: a light gray window, white cards and the system blue.
func claudeTheme() *core.Theme {
	theme := core.DefaultTheme()
	theme.Radius = 8
	light := &theme.Light
	light.Background = core.RGB(0xf2f2f5)
	light.Surface = core.RGB(0xffffff)
	light.Text = core.RGB(0x1d1d1f)
	light.TextMuted = core.RGB(0x6e6e73)
	light.Border = core.RGBA(0x000000, 0.06)
	light.Success = core.RGB(0x34c759)
	light.Warning = core.RGB(0xff9500)
	light.Danger = core.RGB(0xff3b30)
	light.Accent = core.RGB(0x007aff)
	light.AccentHover = core.RGB(0x006ee6)
	light.AccentPressed = core.RGB(0x0062cc)
	light.AccentText = core.RGB(0xffffff)
	light.SurfaceHover = core.RGBA(0x000000, 0.05)
	light.SurfacePressed = core.RGBA(0x000000, 0.09)
	light.Focus = core.RGBA(0x007aff, 0.4)
	light.Selection = core.RGBA(0x007aff, 0.22)
	for name, color := range map[string]core.Color{
		"accentFill": core.RGB(0x007aff), "accentSoft": core.RGB(0xeaf2ff),
		"tertiary": core.RGB(0x86868b), "sep": core.RGBA(0x000000, 0.06), "line": core.RGBA(0x000000, 0.1),
		"fill": core.RGBA(0x767680, 0.12), "track": core.RGBA(0x767680, 0.14), "strip": core.RGB(0xfafafb),
		"bar": core.RGBA(0xf2f2f5, 0.78), "glass": core.RGBA(0xffffff, 0.8), "glassBorder": core.RGBA(0x000000, 0.1),
		"alert": core.RGB(0xf7f7f8), "scrim": core.RGBA(0x000000, 0.18),
		"btnBorder": core.RGBA(0x000000, 0.14), "activeBorder": core.RGBA(0x007aff, 0.3),
		"okText": core.RGB(0x248a3d), "warnText": core.RGB(0xb25000), "dangerText": core.RGB(0xd70015),
		"warnSoft": core.RGBA(0xff9500, 0.12), "toast": core.RGBA(0x242426, 0.92),
		"inset": core.RGB(0xf5f5f7), "raised": core.RGB(0xffffff), "field": core.RGB(0xffffff),
		"fieldBorder": core.RGBA(0x000000, 0.14), "faint": core.RGB(0x86868b),
	} {
		light.SetColor(name, color)
	}

	dark := &theme.Dark
	dark.Background = core.RGB(0x1e1e20)
	dark.Surface = core.RGB(0x2b2b2e)
	dark.Text = core.RGB(0xf5f5f7)
	dark.TextMuted = core.RGB(0xa8a8ad)
	dark.Border = core.RGBA(0xffffff, 0.07)
	dark.Success = core.RGB(0x30d158)
	dark.Warning = core.RGB(0xff9f0a)
	dark.Danger = core.RGB(0xff453a)
	dark.Accent = core.RGB(0x0a84ff)
	dark.AccentHover = core.RGB(0x2f95ff)
	dark.AccentPressed = core.RGB(0x0071e0)
	dark.AccentText = core.RGB(0xffffff)
	dark.SurfaceHover = core.RGBA(0xffffff, 0.08)
	dark.SurfacePressed = core.RGBA(0xffffff, 0.12)
	dark.Focus = core.RGBA(0x0a84ff, 0.5)
	dark.Selection = core.RGBA(0x0a84ff, 0.3)
	for name, color := range map[string]core.Color{
		"accentFill": core.RGB(0x0a84ff), "accentSoft": core.RGBA(0x0a84ff, 0.18),
		"tertiary": core.RGB(0x8c8c92), "sep": core.RGBA(0xffffff, 0.08), "line": core.RGBA(0xffffff, 0.12),
		"fill": core.RGBA(0xffffff, 0.1), "track": core.RGBA(0xffffff, 0.1), "strip": core.RGBA(0xffffff, 0.03),
		"bar": core.RGBA(0x1e1e20, 0.78), "glass": core.RGBA(0x3c3c40, 0.75), "glassBorder": core.RGBA(0xffffff, 0.12),
		"alert": core.RGB(0x2c2c2f), "scrim": core.RGBA(0x000000, 0.35),
		"btnBorder": core.RGBA(0xffffff, 0.18), "activeBorder": core.RGBA(0x0a84ff, 0.4),
		"okText": core.RGB(0x30d158), "warnText": core.RGB(0xffb340), "dangerText": core.RGB(0xff6961),
		"warnSoft": core.RGBA(0xff9f0a, 0.16), "toast": core.RGBA(0x3a3a3c, 0.95),
		"inset": core.RGB(0x252528), "raised": core.RGB(0x36363a), "field": core.RGB(0x2b2b2e),
		"fieldBorder": core.RGBA(0xffffff, 0.18), "faint": core.RGB(0x8c8c92),
	} {
		dark.SetColor(name, color)
	}
	return theme
}

// tones reads the theme's colors where a scope is, under this app's names.
type tones struct{ cx *core.Scope }

func tonesOf(cx *core.Scope) tones { return tones{cx} }

func (t tones) role(name string) core.Color { return t.cx.Palette().Colors[name] }

func (t tones) Window() core.Color      { return t.cx.Palette().Background }
func (t tones) Card() core.Color        { return t.cx.Palette().Surface }
func (t tones) CardBorder() core.Color  { return t.cx.Palette().Border }
func (t tones) Text() core.Color        { return t.cx.Palette().Text }
func (t tones) Secondary() core.Color   { return t.cx.Palette().TextMuted }
func (t tones) Tertiary() core.Color    { return t.role("tertiary") }
func (t tones) Sep() core.Color         { return t.role("sep") }
func (t tones) Line() core.Color        { return t.role("line") }
func (t tones) Fill() core.Color        { return t.role("fill") }
func (t tones) Track() core.Color       { return t.role("track") }
func (t tones) Strip() core.Color       { return t.role("strip") }
func (t tones) Glass() core.Color       { return t.role("glass") }
func (t tones) Bar() core.Color         { return t.role("bar") }
func (t tones) GlassBorder() core.Color { return t.role("glassBorder") }
func (t tones) Alert() core.Color       { return t.role("alert") }
func (t tones) Scrim() core.Color       { return t.role("scrim") }
func (t tones) BtnBorder() core.Color   { return t.role("btnBorder") }
func (t tones) Hover() core.Color       { return t.cx.Palette().SurfaceHover }
func (t tones) Pressed() core.Color     { return t.cx.Palette().SurfacePressed }
func (t tones) Accent() core.Color      { return t.cx.Palette().Accent }
func (t tones) AccentSoft() core.Color  { return t.role("accentSoft") }
func (t tones) ActiveBorder() core.Color {
	return t.role("activeBorder")
}
func (t tones) Green() core.Color      { return t.cx.Palette().Success }
func (t tones) Warn() core.Color       { return t.cx.Palette().Warning }
func (t tones) Danger() core.Color     { return t.cx.Palette().Danger }
func (t tones) OKText() core.Color     { return t.role("okText") }
func (t tones) WarnText() core.Color   { return t.role("warnText") }
func (t tones) DangerText() core.Color { return t.role("dangerText") }
func (t tones) WarnSoft() core.Color   { return t.role("warnSoft") }
func (t tones) Toast() core.Color      { return t.role("toast") }
func (t tones) Dark() bool             { return t.cx.Dark() }

// ActiveTint is the faint accent wash at the top of the current account's card.
func (t tones) ActiveTint() core.Color { return ui.Mix(t.Card(), t.Accent(), t.pick(0.045, 0.1)) }

func (t tones) pick(light, dark float32) float32 {
	if t.Dark() {
		return dark
	}
	return light
}

// CardShadow is the soft lift under every card.
func (t tones) CardShadow() []core.BoxShadow {
	if t.Dark() {
		return []core.BoxShadow{ui.Shadow(0, 1, 2, 0, ui.Black(0.3))}
	}
	return []core.BoxShadow{ui.Shadow(0, 1, 2, 0, ui.Black(0.04)), ui.Shadow(0, 4, 14, 0, ui.Black(0.04))}
}

// meterColors returns the fill and the value's color for a used percentage.
func (t tones) meterColors(value float64) (core.Color, core.Color) {
	switch {
	case value >= 90:
		return t.Danger(), t.DangerText()
	case value >= 70:
		return t.Warn(), t.WarnText()
	default:
		return t.Accent(), t.Text()
	}
}
