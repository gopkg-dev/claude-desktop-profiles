package desktopui

import (
	"bytes"
	"context"
	"image/png"
	"testing"
	"time"

	"github.com/nsgo-dev/nsgo/core"
	"github.com/nsgo-dev/nsgo/nsgotest"

	"claudeprofiles/profiles"
)

func TestTrayIconIsTransparentTemplatePNG(t *testing.T) {
	icon, err := png.Decode(bytes.NewReader(trayIcon()))
	if err != nil {
		t.Fatal(err)
	}
	if icon.Bounds().Dx() != 64 || icon.Bounds().Dy() != 64 {
		t.Fatalf("unexpected icon dimensions: %v", icon.Bounds())
	}
	// A template image may only carry black with varying alpha; macOS recolors it for the menu bar.
	opaque := 0
	for y := 0; y < 64; y++ {
		for x := 0; x < 64; x++ {
			r, g, b, alpha := icon.At(x, y).RGBA()
			if alpha > 0 && (r != 0 || g != 0 || b != 0) {
				t.Fatalf("pixel (%d, %d) is not black: RGBA (%d, %d, %d, %d)", x, y, r, g, b, alpha)
			}
			if alpha == 0xffff {
				opaque++
			}
		}
	}
	if _, _, _, alpha := icon.At(0, 0).RGBA(); alpha != 0 {
		t.Error("the corner should be transparent")
	}
	if opaque < 200 {
		t.Errorf("only %d opaque pixels; the icon would be too faint", opaque)
	}
}

// NSGo's pinned nsgotest version exports EventWheel but not these desktop events.
const (
	testWindowClose = nsgotest.EventWheel + 5
	testReopen      = nsgotest.EventWheel + 7
	testTrayClick   = nsgotest.EventWheel + 9
)

func TestTrayWindowLifecycle(t *testing.T) {
	limits := mainWindowConstraints()
	root := core.NewRoot(core.RootOptions{
		Post: func(func()) {}, Clock: core.NewManualClock(time.Unix(0, 0)),
		Windows: &core.WindowOptions{Width: 1280, Height: 860, Constraints: limits},
	})
	t.Cleanup(root.Dispose)
	tree := core.NewTree(root, core.ApproxTextEngine{})
	var application *app
	var window *core.Window
	tree.Mount(core.AppRoot(func(cx *core.Scope) {
		cx.SetMainView(func(cx *core.Scope) core.Node {
			window = cx.CurrentWindow()
			application = &app{
				cx: cx, busy: core.NewSignal(cx, false), blocked: core.NewSignal(cx, false),
				pending: core.NewSignal[*action](cx, nil), status: core.NewSignal(cx, ""),
				snapshot: core.NewSignal[*profiles.Snapshot](cx, nil),
			}
			application.attachTray()
			return core.Text{Content: "Fixture"}
		})
	}))
	if window == nil || application == nil || len(tree.Surfaces()) != 1 {
		t.Fatal("main view did not receive its real window scope")
	}
	var trayID uint32
	var foundLimits bool
	if batch := tree.Commit(); batch != nil {
		for _, item := range batch.Items {
			if item.Surface != nil {
				c := item.Surface.Constraints
				foundLimits = c.MinWidth == 480 && c.MinHeight == 560 && c.MaxWidth == 600 && c.MaxHeight == 1100 &&
					c.NoMaximize
			}
			if item.Tray != nil && item.Tray.Template && len(item.Tray.Icon) > 0 {
				trayID = item.Tray.ID
			}
		}
	}
	if !foundLimits || trayID == 0 {
		t.Fatal("main window constraints or template tray missing")
	}
	surface := tree.Surfaces()[0]
	surface.Commit()
	surface.Dispatch(nsgotest.Event{Kind: testWindowClose})
	if window.Closed() || window.Visible() {
		t.Fatal("closing the window must hide it without destroying it")
	}
	tree.Dispatch(nsgotest.Event{Kind: testReopen})
	if !window.Visible() || window.Closed() {
		t.Fatal("Dock reopen did not restore the existing window")
	}
	surface.Dispatch(nsgotest.Event{Kind: testWindowClose})
	tree.Dispatch(nsgotest.Event{Kind: testTrayClick, Count: trayID})
	var showID uint32
	if batch := tree.Commit(); batch != nil {
		for _, item := range batch.Items {
			for _, menu := range item.Menu {
				if item.Tray != nil && menu.Label == "显示主窗口" {
					showID = menu.ID
				}
			}
		}
	}
	if showID == 0 {
		t.Fatal("tray click did not open its menu")
	}
	tree.Dispatch(nsgotest.Event{Kind: nsgotest.EventMenu, Count: showID})
	if !window.Visible() || window.Closed() {
		t.Fatal("tray action did not restore the existing window")
	}
	application.busy.Set(true)
	application.status.Set("正在切换账号…")
	if menu := application.trayMenu(window, func() {}); menu[0].Label != "正在切换账号…" || !menu[0].Disabled {
		t.Fatalf("a running operation should lead the menu, got %q", menu[0].Label)
	}
	tree.Dispatch(nsgotest.Event{Kind: testTrayClick, Count: trayID})
	var quitID uint32
	if batch := tree.Commit(); batch != nil {
		for _, item := range batch.Items {
			for _, menu := range item.Menu {
				if menu.Label == "退出 Claude Desktop Profiles" {
					quitID = menu.ID
				}
			}
		}
	}
	if quitID == 0 {
		t.Fatal("tray quit item not found")
	}
	tree.Dispatch(nsgotest.Event{Kind: nsgotest.EventMenu, Count: quitID})
	if application.status.Get() != "操作进行中，请完成后退出" || window.Closed() {
		t.Fatal("quit should preserve a running operation and explain why")
	}
}

func trayItem(t *testing.T, menu []core.MenuItem, label string) core.MenuItem {
	t.Helper()
	for _, item := range menu {
		if item.Label == label {
			return item
		}
	}
	labels := []string{}
	for _, item := range menu {
		labels = append(labels, item.Label)
	}
	t.Fatalf("tray item %q missing from %q", label, labels)
	return core.MenuItem{}
}

func TestTrayMenuListsAccountsAndAsksInTheWindow(t *testing.T) {
	t.Setenv("HOME", testHome(t))
	var application *app
	var window *core.Window
	tester := nsgotest.Start(t, func(cx *core.Scope) core.Node {
		application, window = newApp(cx, pageFixture(), func(context.Context) error { return nil }), cx.CurrentWindow()
		return application.view()
	}, nsgotest.Size(480, 760))
	tester.WaitUntil(3*time.Second, func() bool { return tester.Exists("id:profile-personal") })
	var menu []core.MenuItem
	read := func() {
		tester.Post(func() { menu = application.trayMenu(window, func() {}) })
		tester.WaitUntil(3*time.Second, func() bool { return menu != nil })
	}
	read()
	active := trayItem(t, menu, "personal@example.com — 5 小时 78% · 每周 92%")
	if !active.Checkable || !active.Checked || menu[0].Label != active.Label {
		t.Fatal("the active account should come first with the check mark")
	}
	other := trayItem(t, menu, "work@example.com — 5 小时 26% · 每周 53%")
	if other.Checked || other.Disabled {
		t.Fatal("other accounts should be selectable and unchecked")
	}
	if sharing := trayItem(t, menu, "Code 会话共享"); !sharing.Checkable || !sharing.Checked {
		t.Fatal("the sharing item should reflect the current setting")
	}
	for _, label := range []string{"添加账号…", "打开 Claude Desktop", "在线刷新额度", "显示主窗口", "会话管理…", "设置…"} {
		trayItem(t, menu, label)
	}

	// The startup read may still be running; choices made while busy are ignored, as the cards' buttons are disabled.
	tester.WaitUntil(3*time.Second, func() bool { return !application.busy.Get() })
	ran := false
	tester.Post(func() { other.OnSelect(); ran = true })
	tester.WaitUntil(3*time.Second, func() bool { return ran })
	// A posted change reaches the native window on the next step.
	tester.Advance(100 * time.Millisecond)
	tester.Find("id:action-panel >> text:切换到 work@example.com？")
	menu = nil
	read()
	if !trayItem(t, menu, other.Label).Disabled {
		t.Fatal("accounts must not be switchable while a confirmation is open")
	}
}
