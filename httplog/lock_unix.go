//go:build unix

package httplog

import (
	"os"
	"syscall"
)

func lockDirectory(root *os.Root) (func(), error) {
	file, err := root.OpenFile(".lock", os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return nil, ErrUnsafePath
	}
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		file.Close()
		return nil, ErrUnsafePath
	}
	if stat, ok := info.Sys().(*syscall.Stat_t); !ok || stat.Nlink != 1 {
		file.Close()
		return nil, ErrUnsafePath
	}
	if err := file.Chmod(0o600); err != nil {
		file.Close()
		return nil, ErrWrite
	}
	// Another process must not extend a completed query by indefinitely holding the log lock.
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		file.Close()
		return nil, ErrWrite
	}
	return func() {
		_ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
		_ = file.Close()
	}, nil
}
