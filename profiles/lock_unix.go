//go:build unix

package profiles

import (
	"os"
	"path/filepath"
	"syscall"
)

func (m *Manager) lock() (func(), error) {
	if err := m.prepare(); err != nil {
		return nil, err
	}
	path := filepath.Join(m.paths.StoreDir, ".lock")
	if err := checkedFile(path); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		f.Close()
		return nil, err
	}
	return func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		_ = f.Close()
	}, nil
}
