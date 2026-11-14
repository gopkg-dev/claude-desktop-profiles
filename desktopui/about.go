package desktopui

import (
	_ "embed"

	"github.com/nsgo-dev/nsgo/core"
	"github.com/nsgo-dev/nsgo/icons/lucide"
	"github.com/nsgo-dev/nsgo/ui"
	"github.com/nsgo-dev/nsgo/updater"
)

// Repository is where the app is developed and released.
const Repository = "https://github.com/gopkg-dev/claude-desktop-profiles"

//go:embed about-icon.png
var aboutIcon []byte

// releaseBuild reports whether nsgo build made this app, so it carries Sparkle and can update itself.
var releaseBuild = func() bool { return !core.App().Development }

// aboutLink is one of the about page's links.
type aboutLink struct {
	id, title, detail, url string
	icon                   func() *core.SVG
}

var aboutLinks = []aboutLink{
	{id: "repository", title: "GitHub 仓库", detail: "gopkg-dev/claude-desktop-profiles", url: Repository, icon: lucide.BookOpen},
	{id: "releases", title: "版本说明", detail: "每个版本的改动与下载", url: Repository + "/releases", icon: lucide.ScrollText},
	{id: "issues", title: "问题反馈", detail: "报告问题或提出建议", url: Repository + "/issues", icon: lucide.MessageCircle},
}

// checkForUpdates asks Sparkle for a newer release; it shows what it finds in its own windows,
// and installs and relaunches this app when the user agrees.
func (a *app) checkForUpdates() {
	if !releaseBuild() {
		a.showToast("开发版本不检查更新")
		return
	}
	updater.Check(a.cx)
}

func (a *app) openLink(url string) {
	a.cx.OpenURL(url, func(opened bool) {
		if !opened {
			a.showToast("无法打开链接")
		}
	})
}

func (a *app) aboutView() core.Node {
	scrolled := core.NewSignal(a.cx, false)
	return core.Dynamic(func(cx *core.Scope) core.Node {
		content := core.View{Style: core.Style{Gap: 18, Padding: core.Edges{Left: 16, Right: 16, Bottom: 20}},
			Children: []core.Node{aboutHeader(cx), a.updateSettings(cx), a.linkSettings(cx), aboutFootnote(cx)}}
		return frostedPage(cx, "about", scrolled, titleBarHeight+4, content, a.pageBar(cx, "关于"))
	})
}

func aboutHeader(cx *core.Scope) core.Node {
	t := tonesOf(cx)
	return core.View{TestID: "about-header", Style: core.Style{AlignItems: core.AlignCenter, Gap: 6,
		Padding: core.Edges{Top: 8, Bottom: 2}}, Children: []core.Node{
		core.Image{Source: core.ImageSource{Data: aboutIcon, Size: core.Size{Width: 256, Height: 256}}, Label: "应用图标",
			Style: core.Style{Width: core.Pt(76), Height: core.Pt(76), Margin: core.Edges{Bottom: 4}}},
		core.Text{Content: "Claude Desktop Profiles", Style: core.Style{FontSize: 17, FontWeight: core.FontWeightBold,
			Color: t.Text()}},
		core.Text{Content: "版本 " + Version, TestID: "about-version", Selectable: core.SelectableOn,
			Style: core.Style{FontSize: 12, Color: t.Secondary(), FontVariant: numberVariant}},
		core.Text{Content: "在 Mac 上管理多个 Claude Desktop 账号\n一键切换、查看额度与重置权益，在账号间共享 Code 会话",
			TestID: "about-intro", Style: core.Style{FontSize: 12, Color: t.Secondary(), TextAlign: core.TextAlignCenter,
				LineHeightMultiple: 1.45, MaxWidth: core.Pt(360), Margin: core.Edges{Top: 4}}},
	}}
}

func (a *app) updateSettings(cx *core.Scope) core.Node {
	t := tonesOf(cx)
	detail, note := "每天自动检查一次，有新版本时提示", "新版本从 GitHub Releases 下载，校验签名后安装，并重新打开本应用。"
	if !releaseBuild() {
		detail, note = "开发版本不检查更新", "用 make release 打包的版本才会检查和安装更新。"
	}
	return formGroup(cx, "更新", note, formRow(cx, "检查更新", detail,
		formButton(cx, "检查…", "check-updates", t.Text(), !releaseBuild(), a.checkForUpdates)))
}

func (a *app) linkSettings(cx *core.Scope) core.Node {
	t := tonesOf(cx)
	rows := make([]core.Node, 0, len(aboutLinks))
	for _, link := range aboutLinks {
		rows = append(rows, pressable{Label: link.title, TestID: "link-" + link.id, OnPress: func() { a.openLink(link.url) },
			Style: core.Style{Direction: core.Row, AlignItems: core.AlignCenter, Gap: 10, MinHeight: core.Pt(52),
				Padding: core.Symmetric(4, 14), Hover: &core.Style{Background: t.Fill()}, Active: &core.Style{Background: t.Pressed()}},
			Children: []core.Node{
				core.View{Style: core.Style{Width: core.Pt(28), Height: core.Pt(28), CornerRadius: 7, Background: t.AccentSoft(),
					AlignItems: core.AlignCenter, JustifyContent: core.JustifyCenter},
					Children: []core.Node{ui.Icon(link.icon, 15, ui.Fixed(t.Accent()))}},
				core.View{Style: core.Style{FlexGrow: 1, FlexShrink: 1, MinWidth: core.Pt(0), Gap: 2}, Children: []core.Node{
					core.Text{Content: link.title, MaxLines: 1, Style: core.Style{FontSize: 13, Color: t.Text()}},
					core.Text{Content: link.detail, MaxLines: 1, Style: core.Style{FontSize: 11, Color: t.Secondary()}},
				}},
				ui.Icon(lucide.ExternalLink, 13, ui.Fixed(t.Tertiary())),
			}})
	}
	return formGroup(cx, "链接", "", rows...)
}

func aboutFootnote(cx *core.Scope) core.Node {
	t := tonesOf(cx)
	return core.Text{Content: "社区开发的开源工具，与 Anthropic 无关。Claude 是 Anthropic 的商标。",
		Style: core.Style{FontSize: 11, Color: t.Tertiary(), TextAlign: core.TextAlignCenter, LineHeightMultiple: 1.45,
			Padding: core.Symmetric(0, 10)}}
}
