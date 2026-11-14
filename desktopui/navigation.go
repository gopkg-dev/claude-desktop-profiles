package desktopui

import (
	"time"

	"github.com/nsgo-dev/nsgo/core"
	"github.com/nsgo-dev/nsgo/desktop"

	"claudeprofiles/profiles"
)

type settingsStore interface {
	Settings() (profiles.Settings, error)
	SaveSettings(profiles.Settings) error
	ClearOnlineCache() error
}

func (a *app) initNavigation() {
	if a.page != nil {
		return
	}
	a.page = core.NewSignal(a.cx, "accounts")
	settings := profiles.DefaultSettings()
	message := ""
	if store, ok := a.store.(settingsStore); ok {
		var err error
		settings, err = store.Settings()
		if err != nil {
			message = "设置读取失败，已使用手动刷新默认值"
		}
	}
	a.settings = core.NewSignal(a.cx, settings)
	a.settingsMessage = core.NewSignal(a.cx, message)
	a.requestLog = core.NewSignal(a.cx, requestLogView{})
	a.sessionQuery = core.NewSignal(a.cx, "")
	a.sessionFilter = core.NewSignal(a.cx, sessionFilterAll)
	a.sessionReasons = core.NewSignal(a.cx, false)
	a.initExitInfo()
}

func (a *app) view() core.Node {
	a.initNavigation()
	a.cx.SetTheme(claudeTheme())
	desktop.SetTitleBar(a.cx, desktop.TitleBarHidden, titleBarHeight)
	desktop.SetWindowControls(a.cx, desktop.WindowControls{TrafficLights: core.Point{X: 20}})
	page := func(id string, node core.Node) core.Node {
		return core.View{StyleFunc: func() core.Style {
			style := core.Style{Position: core.Absolute, Top: core.Pt(0), Left: core.Pt(0),
				Width: core.Pct(100), Height: core.Pct(100), MinHeight: core.Pt(0)}
			if a.page.Get() != id {
				style.Visibility = core.VisibilityHidden
			}
			return style
		}, Children: []core.Node{node}}
	}
	return core.View{TestID: "application-shell", StyleFunc: func() core.Style {
		t := tonesOf(a.cx)
		return core.Style{Width: core.Pct(100), Height: core.Pct(100), Background: t.Window(), Color: t.Text()}
	}, Children: []core.Node{
		page("accounts", a.accountView()), page("settings", a.settingsView()), page("sessions", a.sessionsView()), page("about", a.aboutView()),
		core.Dynamic(func(cx *core.Scope) core.Node { return a.toastView(cx) }),
		core.Dynamic(func(cx *core.Scope) core.Node {
			if item := a.pending.Get(); item != nil {
				return a.alert(cx, item)
			}
			return core.View{Style: core.Style{Display: core.DisplayNone}}
		}),
		core.Dynamic(func(cx *core.Scope) core.Node {
			if value := a.transition.Get(); value != nil {
				return a.transitionView(cx, value)
			}
			return core.View{Style: core.Style{Display: core.DisplayNone}}
		}),
	}}
}

func (a *app) saveSettings(value profiles.Settings) {
	store, ok := a.store.(settingsStore)
	if !ok {
		a.settingsMessage.Set("当前数据源未提供设置存储")
		return
	}
	if err := store.SaveSettings(value); err != nil {
		a.settingsMessage.Set("设置保存失败，请重试")
		return
	}
	a.settings.Set(value)
	a.settingsMessage.Set("设置已保存")
}

func (a *app) maybeAutoRefresh(snapshot profiles.Snapshot) {
	if a.settings == nil || !a.settings.Get().AutoRefresh || a.busy.Get() ||
		a.pending.Get() != nil || snapshot.PendingRecovery {
		return
	}
	for _, profile := range snapshot.Profiles {
		if !profile.NeedsLogin && (profile.NextRefreshAt.IsZero() || !profile.NextRefreshAt.After(time.Now())) {
			a.refreshAccounts("", true)
			return
		}
	}
}
