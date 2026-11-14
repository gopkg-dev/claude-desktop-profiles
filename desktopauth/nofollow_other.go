//go:build !darwin && !linux && !freebsd && !openbsd && !netbsd && !dragonfly

package desktopauth

import "os"

func openNoFollow(root *os.Root, name string) (*os.File, error) {
	return nil, ErrUnsupported
}
