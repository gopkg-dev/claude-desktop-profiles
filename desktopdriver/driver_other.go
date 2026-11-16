//go:build !darwin

package desktopdriver

import (
	"context"
	"syscall"
)

var defaultKill signaler = func(int, syscall.Signal) error { return ErrUnsupported }

// Stop refuses unsupported platforms without running commands.
func (d *Driver) Stop(context.Context) error {
	return ErrUnsupported
}

// Start refuses unsupported platforms without running commands.
func (d *Driver) Start(context.Context) error {
	return ErrUnsupported
}
