//go:build !unix

package profiles

import "errors"

func (m *Manager) lock() (func(), error) {
	return nil, errors.New("此版本仅支持 macOS")
}
