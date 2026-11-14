package httplog

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

func openDirectory(path string, create bool) (*os.Root, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path || path == string(os.PathSeparator) {
		return nil, ErrUnsafePath
	}
	root, err := os.OpenRoot(string(os.PathSeparator))
	if err != nil {
		return nil, ErrDirectory
	}
	parts := strings.Split(strings.TrimPrefix(path, string(os.PathSeparator)), string(os.PathSeparator))
	for index, part := range parts {
		info, err := root.Lstat(part)
		if errors.Is(err, os.ErrNotExist) && create {
			if err := root.Mkdir(part, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
				root.Close()
				return nil, ErrDirectory
			}
			info, err = root.Lstat(part)
		}
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			root.Close()
			return nil, ErrUnsafePath
		}
		next, err := checkedChild(root, part)
		root.Close()
		if err != nil {
			return nil, err
		}
		root = next
		if index == len(parts)-1 {
			if err := root.Chmod(".", 0o700); err != nil {
				root.Close()
				return nil, ErrDirectory
			}
		}
	}
	return root, nil
}

func checkedChild(parent *os.Root, name string) (*os.Root, error) {
	before, err := parent.Lstat(name)
	if err != nil || !before.IsDir() || before.Mode()&os.ModeSymlink != 0 {
		return nil, ErrUnsafePath
	}
	child, err := parent.OpenRoot(name)
	if err != nil {
		return nil, ErrUnsafePath
	}
	opened, openedErr := child.Stat(".")
	after, afterErr := parent.Lstat(name)
	if openedErr != nil || afterErr != nil || !os.SameFile(before, opened) || !os.SameFile(before, after) ||
		after.Mode()&os.ModeSymlink != 0 || !after.IsDir() {
		child.Close()
		return nil, ErrUnsafePath
	}
	return child, nil
}

func writePrivateFile(root *os.Root, name string, data []byte) error {
	file, err := root.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return ErrWrite
	}
	defer file.Close()
	if err := file.Chmod(0o600); err != nil {
		return ErrWrite
	}
	if written, err := file.Write(data); err != nil || written != len(data) {
		return ErrWrite
	}
	if err := file.Sync(); err != nil {
		return ErrWrite
	}
	return nil
}

func syncDirectory(root *os.Root) error {
	file, err := root.Open(".")
	if err != nil {
		return ErrWrite
	}
	defer file.Close()
	if err := file.Sync(); err != nil {
		return ErrWrite
	}
	return nil
}
