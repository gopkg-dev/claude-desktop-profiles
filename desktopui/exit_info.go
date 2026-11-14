package desktopui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/nsgo-dev/nsgo/core"

	"claudeprofiles/desktopusage"
)

type exitInfoStore interface {
	ExitInfo() (desktopusage.ExitInfo, error)
	RefreshExit(context.Context) (desktopusage.ExitInfo, error)
}

func (a *app) initExitInfo() {
	value := desktopusage.ExitInfo{}
	message := ""
	if store, ok := a.store.(exitInfoStore); ok {
		var err error
		value, err = store.ExitInfo()
		if err != nil {
			message = "出口缓存读取失败"
		}
	}
	a.exitInfo = core.NewSignal(a.cx, &value)
	a.exitBusy = core.NewSignal(a.cx, false)
	a.exitMessage = core.NewSignal(a.cx, message)
}

func (a *app) refreshExit() {
	if a.exitBusy.Get() {
		return
	}
	store, ok := a.store.(exitInfoStore)
	if !ok {
		a.exitMessage.Set("当前数据源未配置出口查询")
		return
	}
	a.exitBusy.Set(true)
	a.exitMessage.Set("")
	go func() {
		ctx, cancel := context.WithTimeout(a.ctx, 30*time.Second)
		defer cancel()
		value, err := store.RefreshExit(ctx)
		a.cx.Post(func() {
			a.exitBusy.Set(false)
			if err == nil || value.IP != "" {
				a.exitInfo.Set(&value)
			}
			if err != nil {
				if !value.NextAllowed.IsZero() && value.NextAllowed.After(time.Now()) {
					a.exitMessage.Set("出口未更新，下次可查询 " + value.NextAllowed.In(time.Local).Format("15:04:05"))
				} else {
					a.exitMessage.Set("出口查询未完成，请重试")
				}
			}
		})
	}()
}

// exitSummary is the exit's country code and IP, or why there is none yet.
func (a *app) exitSummary() string {
	value := a.exitInfo.Get()
	if value.IP == "" {
		return "出口未查询"
	}
	if value.CountryCode != "" {
		return value.CountryCode + " · " + value.IP
	}
	return value.IP
}

// exitPlace is where the exit is and when it was checked.
func (a *app) exitPlace() string {
	value := a.exitInfo.Get()
	if value.IP == "" {
		return "点击查询，走 Claude Desktop 的代理"
	}
	parts := []string{}
	for _, part := range []string{value.Region, value.City} {
		if part != "" {
			parts = append(parts, part)
		}
	}
	if !value.CheckedAt.IsZero() {
		parts = append(parts, value.CheckedAt.In(time.Local).Format("01-02 15:04")+" 查询")
	}
	if value.GeoError != "" {
		parts = append(parts, "GEO 暂未更新")
	}
	return strings.Join(parts, " · ")
}

func (a *app) exitTooltip() string {
	value := a.exitInfo.Get()
	lines := []string{}
	if value.IP == "" {
		lines = append(lines, "点击查询出口 IP")
	} else {
		place := []string{}
		for _, part := range []string{value.City, value.Region, value.Country} {
			if part != "" {
				place = append(place, part)
			}
		}
		if len(place) > 0 {
			lines = append(lines, strings.Join(place, ", "))
		}
		network := value.ISP
		if value.ASN > 0 {
			network = strings.TrimPrefix(network+fmt.Sprintf(" · AS%d", value.ASN), " · ")
		}
		if network != "" {
			lines = append(lines, network)
		}
	}
	for _, message := range []string{value.LastError, value.GeoError, a.exitMessage.Get()} {
		if message != "" {
			lines = append(lines, message)
		}
	}
	return strings.Join(lines, "\n")
}

// exitLabel is the footer's exit, which queries again when pressed.
func (a *app) exitLabel(cx *core.Scope) core.Node {
	return core.Dynamic(func(cx *core.Scope) core.Node {
		t := tonesOf(cx)
		text := a.exitSummary()
		if a.exitBusy.Get() {
			text = "查询中…"
		}
		return withHint(cx, a.exitTooltip(), pressable{Label: "查询 Claude 出口 IP", TestID: "refresh-exit",
			Disabled: a.exitBusy.Get(), OnPress: a.refreshExit, Style: core.Style{Height: core.Pt(22),
				Padding: core.Symmetric(0, 4), CornerRadius: 5, AlignItems: core.AlignCenter, JustifyContent: core.JustifyCenter,
				Hover: &core.Style{Background: t.Hover()}, Disabled: &core.Style{Opacity: core.Alpha(1)}},
			Children: []core.Node{core.Text{Content: text, MaxLines: 1, TestID: "exit-ip", Selectable: core.SelectableOn,
				Style: core.Style{FontSize: 12, Color: t.Secondary(), FontVariant: numberVariant}}}})
	})
}
