package desktopui

import (
	_ "embed"
	"strings"

	"github.com/nsgo-dev/nsgo/core"

	"claudeprofiles/profiles"
)

//go:embed tray-icon.png
var trayIconPNG []byte

func mainWindowConstraints() core.WindowConstraints {
	// The cards are laid out for a narrow column; full screen and zoom would only stretch them.
	return core.WindowConstraints{MinWidth: 480, MinHeight: 560, MaxWidth: 600, MaxHeight: 1100, NoMaximize: true}
}

// attachTray keeps the main window alive while it is hidden.
func (a *app) attachTray() {
	cx := a.cx
	window := cx.CurrentWindow()
	if window == nil {
		return
	}
	cx.SetShutdownMode(core.ShutdownOnExplicit)
	window.OnClosing(cx, func(closing *core.WindowClosing) {
		closing.Cancel()
		window.Hide()
	})
	cx.OnReopen(window.Focus)
	quit := func() {
		if a.busy.Get() {
			window.Focus()
			a.status.Set("操作进行中，请完成后退出")
			return
		}
		cx.Quit()
	}
	cx.NewTray(core.TrayOptions{Icon: trayIcon(), Template: true, Tooltip: "Claude Desktop Profiles", Menu: func() []core.MenuItem {
		return a.trayMenu(window, quit)
	}})
	cx.SetApplicationMenu([]core.MenuItem{
		{Label: "Claude Desktop Profiles", Items: []core.MenuItem{
			{Label: "关于 Claude Desktop Profiles", OnSelect: func() {
				if a.page != nil {
					a.page.Set("about")
				}
				window.Focus()
			}},
			{Label: "检查更新…", OnSelect: a.checkForUpdates},
			{Separator: true},
			{Role: core.MenuHide},
			{Role: core.MenuHideOthers},
			{Role: core.MenuShowAll},
			{Separator: true},
			{Label: "退出 Claude Desktop Profiles", Shortcut: "mod+q", OnSelect: quit},
		}},
		{Label: "编辑", Items: []core.MenuItem{
			{Role: core.MenuUndo}, {Role: core.MenuRedo}, {Separator: true},
			{Role: core.MenuCut}, {Role: core.MenuCopy}, {Role: core.MenuPaste}, {Role: core.MenuSelectAll},
		}},
		{Label: "窗口", Items: []core.MenuItem{
			{Role: core.MenuMinimize}, {Role: core.MenuZoom}, {Role: core.MenuClose},
		}},
	})
}

// trayMenu lists the accounts like the Wi-Fi menu, then the everyday actions, then the window's pages.
// Anything that needs a confirmation opens the main window and asks there.
func (a *app) trayMenu(window *core.Window, quit func()) []core.MenuItem {
	snapshot := a.snapshot.Get()
	disabled := a.controlsDisabled()
	show := func(page string, then func()) func() {
		return func() {
			if a.page != nil && page != "" {
				a.page.Set(page)
			}
			window.Focus()
			if then != nil {
				then()
			}
		}
	}
	items := []core.MenuItem{}
	if a.busy.Get() {
		if status := strings.TrimSpace(a.status.Get()); status != "" {
			items = append(items, core.MenuItem{Label: status, Disabled: true}, core.MenuItem{Separator: true})
		}
	}
	rows := orderedProfiles(snapshot)
	managed := snapshot != nil && snapshot.ActiveID != ""
	for _, profile := range rows {
		item := core.MenuItem{Label: trayAccountLabel(profile), Checkable: true, Checked: profile.Active}
		if profile.Active {
			item.OnSelect = show("accounts", nil)
		} else {
			item.Disabled = disabled
			item.OnSelect = show("accounts", func() { a.prepare("switch", profile) })
		}
		items = append(items, item)
	}
	switch {
	case len(rows) > 0:
		items = append(items, core.MenuItem{Label: "添加账号…", Disabled: !managed || disabled,
			OnSelect: show("accounts", func() { a.prepare("add", profiles.Profile{}) })})
	case snapshot != nil && snapshot.CurrentAccount != "":
		items = append(items, core.MenuItem{Label: "接管当前登录…", Disabled: disabled,
			OnSelect: show("accounts", func() { a.prepare("adopt", profiles.Profile{}) })})
	default:
		items = append(items, core.MenuItem{Label: "尚未添加账号", Disabled: true})
	}

	refresh := core.MenuItem{Label: "在线刷新额度", Disabled: !managed || !a.canRefreshRemote(),
		OnSelect: func() { a.refreshRemoteUsage("") }}
	if a.remoteRefreshing {
		refresh = core.MenuItem{Label: "停止在线刷新", OnSelect: a.stopRemoteRefresh}
	}
	count, shared := 0, false
	if snapshot != nil {
		count, shared = len(snapshot.Profiles), snapshot.SharedSessions
	}
	sharing := core.MenuItem{Label: "Code 会话共享", Checkable: true, Checked: shared,
		Disabled: disabled || count < 2 && !shared, OnSelect: func() {
			if shared {
				a.pauseSharing()
				return
			}
			window.Focus()
			a.enableSharing()
		}}
	items = append(items, core.MenuItem{Separator: true},
		core.MenuItem{Label: "打开 Claude Desktop", Disabled: disabled, OnSelect: a.openDesktop},
		refresh,
		sharing,
		core.MenuItem{Separator: true},
		core.MenuItem{Label: "显示主窗口", OnSelect: show("accounts", nil)},
		core.MenuItem{Label: "会话管理…", OnSelect: show("", func() {
			if a.page != nil {
				a.openSessions()
			}
		})},
		core.MenuItem{Label: "设置…", OnSelect: show("settings", nil)},
		core.MenuItem{Label: "关于…", OnSelect: show("about", nil)},
		core.MenuItem{Separator: true},
		core.MenuItem{Label: "退出 Claude Desktop Profiles", OnSelect: quit},
	)
	return items
}

// trayAccountLabel is an account's menu line: its email, then what is left of its limits when known.
func trayAccountLabel(profile profiles.Profile) string {
	name := accountEmail(profile)
	if profile.NeedsLogin {
		if profile.Details == nil || strings.TrimSpace(profile.Details.Email) == "" {
			return "新账号 · 待登录"
		}
		return name + " · 待登录"
	}
	usage := profile.Usage
	if usage == nil || usage.FiveHour == nil && usage.SevenDay == nil {
		return name
	}
	parts := []string{}
	if usage.FiveHour != nil {
		parts = append(parts, "5 小时 "+percent(usage.FiveHour))
	}
	if usage.SevenDay != nil {
		parts = append(parts, "每周 "+percent(usage.SevenDay))
	}
	return name + " — " + strings.Join(parts, " · ")
}

// trayIcon is the menu bar icon: a 64 × 64 template PNG, black on transparent, which macOS recolors for the menu bar.
func trayIcon() []byte { return trayIconPNG }
