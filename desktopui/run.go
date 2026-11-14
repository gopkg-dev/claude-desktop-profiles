// Package desktopui implements the native Claude Desktop Profiles interface and interactions.
package desktopui

import (
	"context"

	"github.com/nsgo-dev/nsgo"
	"github.com/nsgo-dev/nsgo/core"

	"claudeprofiles/profiles"
	"claudeprofiles/workspaces"
)

// Store provides the account operations used by the desktop interface.
type Store interface {
	Snapshot() (profiles.Snapshot, error)
	UsageSnapshot() (profiles.UsageSnapshot, error)
	ProfileDirectory(string) (string, error)
	Adopt(string) error
	Add(context.Context, string) error
	Switch(context.Context, string) error
	Archive(string) error
	Recover(context.Context) error
	SyncSessions(context.Context) (workspaces.Report, error)
	DisableSessionSharing() error
}

// Run starts the interface; refreshOnce explicitly requests one online refresh after local startup.
func Run(store Store, launch func(context.Context) error, refreshOnce bool) {
	nsgo.Run(func(cx *core.Scope) core.Node {
		application := newApp(cx, store, launch)
		if refreshOnce {
			application.refreshOnceAfterStartup()
		}
		application.attachTray()
		return application.view()
	}, nsgo.WindowSize(480, 760), nsgo.WindowConstraints(mainWindowConstraints()))
}

// refreshOnceAfterStartup runs the same action as the online refresh button, once the full snapshot is ready.
func (a *app) refreshOnceAfterStartup() {
	pending := true
	a.cx.Effect(func() {
		snapshot := a.snapshot.Get()
		if !pending || snapshot == nil || snapshot.Preview || a.busy.Get() || a.blocked.Get() {
			return
		}
		pending = false
		a.cx.Post(func() { a.refreshRemoteUsage("") })
	})
}
