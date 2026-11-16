package desktopdriver

import (
	"context"
	"syscall"
)

var defaultKill signaler = syscall.Kill

// Stop requests a graceful exit and refuses to continue while helpers are alive.
func (d *Driver) Stop(ctx context.Context) error {
	return d.stop(ctx)
}

// Start opens the installed Claude Desktop application.
func (d *Driver) Start(ctx context.Context) error {
	return d.start(ctx)
}
