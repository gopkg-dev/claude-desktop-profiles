package desktopauth

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
)

type configSnapshot struct {
	AccountID string      `json:"lastKnownAccountUuid"`
	Encrypted string      `json:"oauth:tokenCacheV2"`
	Directory os.FileInfo `json:"-"`
}

func readConfig(directory string) (configSnapshot, error) {
	root, err := openDirectory(directory)
	if err != nil {
		return configSnapshot{}, ErrUnavailable
	}
	defer root.Close()
	directoryInfo, err := root.Stat(".")
	if err != nil {
		return configSnapshot{}, ErrUnavailable
	}
	before, err := root.Lstat("config.json")
	if err != nil || !before.Mode().IsRegular() || before.Size() <= 0 || before.Size() > 4<<20 {
		return configSnapshot{}, ErrUnavailable
	}
	file, err := openNoFollow(root, "config.json")
	if err != nil {
		return configSnapshot{}, ErrUnavailable
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !sameFile(before, opened) {
		return configSnapshot{}, ErrChanged
	}
	data, err := io.ReadAll(io.LimitReader(file, (4<<20)+1))
	defer clear(data)
	if err != nil || len(data) > 4<<20 || int64(len(data)) != before.Size() {
		return configSnapshot{}, ErrFormat
	}
	after, err := root.Lstat("config.json")
	if err != nil || !sameFile(before, after) {
		return configSnapshot{}, ErrChanged
	}
	var result configSnapshot
	if json.Unmarshal(data, &result) != nil {
		return configSnapshot{}, ErrFormat
	}
	result.Directory = directoryInfo
	return result, nil
}

func sameFile(before, after os.FileInfo) bool {
	return before != nil && after != nil && os.SameFile(before, after) &&
		before.Size() == after.Size() && before.ModTime().Equal(after.ModTime()) && after.Mode().IsRegular()
}

func sameDirectory(before, after os.FileInfo) bool {
	return before != nil && after != nil && before.IsDir() && after.IsDir() && os.SameFile(before, after)
}

// openDirectory rejects symlinks in every directory component before resolving the credential file.
func openDirectory(path string) (*os.Root, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return nil, ErrUnavailable
	}
	rootPath := string(filepath.Separator)
	if volume := filepath.VolumeName(path); volume != "" {
		rootPath = volume + rootPath
	}
	root, err := os.OpenRoot(rootPath)
	if err != nil {
		return nil, ErrUnavailable
	}
	for _, part := range strings.Split(strings.TrimPrefix(path, rootPath), string(filepath.Separator)) {
		if part == "" {
			continue
		}
		before, err := root.Lstat(part)
		if err != nil || before.Mode()&os.ModeSymlink != 0 || !before.IsDir() {
			root.Close()
			return nil, ErrUnavailable
		}
		next, err := root.OpenRoot(part)
		if err != nil {
			root.Close()
			return nil, ErrUnavailable
		}
		opened, openedErr := next.Stat(".")
		after, afterErr := root.Lstat(part)
		root.Close()
		if openedErr != nil || afterErr != nil || after.Mode()&os.ModeSymlink != 0 ||
			!sameDirectory(before, opened) || !sameDirectory(before, after) {
			next.Close()
			return nil, ErrChanged
		}
		root = next
	}
	return root, nil
}
