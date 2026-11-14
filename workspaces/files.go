package workspaces

import (
	"bytes"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// checkPath refuses links, including links in an existing parent component.
func checkPath(path string) error {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return ErrUnsafePath
	}
	volume := filepath.VolumeName(path)
	current := volume + string(filepath.Separator)
	for _, part := range strings.Split(strings.TrimPrefix(path, current), string(filepath.Separator)) {
		if part == "" {
			continue
		}
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return ErrIO
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return ErrUnsafePath
		}
	}
	return nil
}

func within(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func readBounded(path string, limit int64) ([]byte, error) {
	if err := checkPath(path); err != nil {
		return nil, err
	}
	file, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, os.ErrNotExist
		}
		return nil, ErrIO
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return nil, ErrUnsafePath
	}
	if info.Size() > limit {
		return nil, ErrLimit
	}
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return nil, ErrIO
	}
	if int64(len(data)) > limit {
		return nil, ErrLimit
	}
	return data, nil
}

type mutation struct {
	rel       string
	staged    string
	remove    bool
	backup    string
	installed bool
}

type transaction struct {
	root       string
	stage      string
	backupRoot string
	backupDir  string
	changes    []mutation
	planned    map[string]int
	bytes      int64
	files      int
	dirs       map[string]bool
}

func newTransaction(root, backupRoot string) (*transaction, error) {
	for _, path := range []string{root, backupRoot} {
		if err := checkPath(path); err != nil {
			return nil, err
		}
	}
	if within(root, backupRoot) || within(backupRoot, root) {
		return nil, ErrUnsafePath
	}
	stage, err := os.MkdirTemp(root, ".claude-profiles-workspaces-")
	if err != nil {
		return nil, ErrIO
	}
	return &transaction{
		root: root, stage: stage, backupRoot: backupRoot, planned: make(map[string]int), dirs: make(map[string]bool),
	}, nil
}

func (tx *transaction) close() { _ = os.RemoveAll(tx.stage) }

func (tx *transaction) reserve(size int64) error {
	if size > maxFileBytes || tx.files >= maxFiles || tx.bytes+size > maxTotalBytes {
		return ErrLimit
	}
	tx.files++
	tx.bytes += size
	return nil
}

func (tx *transaction) put(rel string, data []byte) error {
	if err := tx.reserve(int64(len(data))); err != nil {
		return err
	}
	if !filepath.IsLocal(rel) || blockedName(filepath.Base(rel)) || rel == "config.json" {
		return ErrUnsafePath
	}
	dest := filepath.Join(tx.root, rel)
	if err := checkPath(dest); err != nil {
		return err
	}
	previous, err := readBounded(dest, maxFileBytes)
	if err == nil && bytes.Equal(previous, data) {
		return nil
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	staged := filepath.Join(tx.stage, rel)
	if err := os.MkdirAll(filepath.Dir(staged), 0o700); err != nil {
		return ErrIO
	}
	file, err := os.OpenFile(staged, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return ErrIO
	}
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		return ErrIO
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return ErrIO
	}
	if err := file.Close(); err != nil {
		return ErrIO
	}
	entry := mutation{rel: rel, staged: staged}
	if index, ok := tx.planned[rel]; ok {
		tx.changes[index] = entry
		return nil
	}
	tx.planned[rel] = len(tx.changes)
	tx.changes = append(tx.changes, entry)
	return nil
}

func (tx *transaction) copyFile(src, rel string) error {
	data, err := readBounded(src, maxFileBytes)
	if err != nil {
		return err
	}
	if err := tx.put(rel, data); err != nil {
		return err
	}
	if index, ok := tx.planned[rel]; ok {
		info, err := os.Stat(src)
		if err != nil {
			return ErrIO
		}
		path := tx.changes[index].staged
		if err := os.Chmod(path, info.Mode().Perm()); err != nil {
			return ErrIO
		}
		if err := os.Chtimes(path, info.ModTime(), info.ModTime()); err != nil {
			return ErrIO
		}
	}
	return nil
}

func blockedName(name string) bool {
	switch strings.ToLower(name) {
	case ".credentials.json", "cookies", "cookies-journal", "local storage", "indexeddb",
		".mcp.json", ".git", "claude.md", "claude.local.md", "plugin-signin", "plugin-sign-in":
		return true
	}
	return false
}

func (tx *transaction) copyWorkTree(src, rel string, report *Report) error {
	if err := checkPath(src); err != nil {
		return err
	}
	if _, err := os.Stat(src); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return ErrIO
	}
	visited := 0
	return filepath.WalkDir(src, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return ErrIO
		}
		visited++
		if visited > maxFiles {
			return ErrLimit
		}
		if blockedName(entry.Name()) || entry.Name() == ".claude" {
			report.skip("excluded_private_or_execution_file")
			if entry.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			report.skip("symlink_not_shared")
			return nil
		}
		suffix, err := filepath.Rel(src, path)
		if err != nil {
			return ErrUnsafePath
		}
		if entry.IsDir() {
			tx.dirs[filepath.Join(rel, suffix)] = true
			return nil
		}
		if !entry.Type().IsRegular() {
			report.skip("non_regular_file")
			return nil
		}
		dstRel := filepath.Join(rel, suffix)
		dst := filepath.Join(tx.root, dstRel)
		if err := checkPath(dst); err != nil {
			return err
		}
		if targetInfo, err := os.Stat(dst); err == nil {
			sourceInfo, statErr := entry.Info()
			if statErr != nil {
				return ErrIO
			}
			if !sourceInfo.ModTime().After(targetInfo.ModTime()) {
				return nil
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return ErrIO
		}
		return tx.copyFile(path, dstRel)
	})
}

func (tx *transaction) remove(rel string) error {
	if !filepath.IsLocal(rel) {
		return ErrUnsafePath
	}
	path := filepath.Join(tx.root, rel)
	if err := checkPath(path); err != nil {
		return err
	}
	if _, err := os.Lstat(path); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return ErrIO
	}
	if _, ok := tx.planned[rel]; ok {
		return ErrUnsafePath
	}
	tx.planned[rel] = len(tx.changes)
	tx.changes = append(tx.changes, mutation{rel: rel, remove: true})
	return nil
}

func (tx *transaction) commit() error {
	if len(tx.changes) == 0 {
		return nil
	}
	if err := checkPath(tx.backupRoot); err != nil {
		return err
	}
	if err := os.MkdirAll(tx.backupRoot, 0o700); err != nil {
		return ErrIO
	}
	dir, err := os.MkdirTemp(tx.backupRoot, "workspace-sync-")
	if err != nil {
		return ErrIO
	}
	tx.backupDir = dir
	for rel := range tx.dirs {
		if !filepath.IsLocal(rel) {
			return ErrUnsafePath
		}
		path := filepath.Join(tx.root, rel)
		if err := checkPath(path); err != nil {
			return err
		}
		if err := os.MkdirAll(path, 0o700); err != nil {
			return ErrIO
		}
	}
	for index := range tx.changes {
		if err := tx.install(index); err != nil {
			if tx.rollback(index) != nil {
				return ErrRollback
			}
			return err
		}
	}
	return nil
}

func (tx *transaction) install(index int) error {
	change := &tx.changes[index]
	dest := filepath.Join(tx.root, change.rel)
	if err := checkPath(dest); err != nil {
		return err
	}
	if info, err := os.Lstat(dest); err == nil {
		if !info.Mode().IsRegular() {
			return ErrUnsafePath
		}
		backup := filepath.Join(tx.backupDir, change.rel)
		if err := os.MkdirAll(filepath.Dir(backup), 0o700); err != nil {
			return ErrIO
		}
		// Copy first, then replace atomically: an interruption never creates a
		// window where an existing index has disappeared from the target.
		if err := copyDurable(dest, backup); err != nil {
			return err
		}
		change.backup = backup
	} else if !errors.Is(err, os.ErrNotExist) {
		return ErrIO
	}
	if change.remove {
		if err := os.Remove(dest); err != nil {
			return ErrIO
		}
		change.installed = true
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o700); err != nil {
		return ErrIO
	}
	if err := os.Rename(change.staged, dest); err != nil {
		return ErrIO
	}
	change.installed = true
	return nil
}

func (tx *transaction) rollback(last int) error {
	var result error
	for index := last; index >= 0; index-- {
		change := tx.changes[index]
		dest := filepath.Join(tx.root, change.rel)
		if change.backup != "" {
			if err := copyDurable(change.backup, dest); err != nil {
				result = ErrRollback
			}
			continue
		}
		if change.installed {
			if err := os.Remove(dest); err != nil {
				result = ErrRollback
				continue
			}
		}
	}
	return result
}

func copyDurable(src, dest string) error {
	for _, path := range []string{src, dest} {
		if err := checkPath(path); err != nil {
			return err
		}
	}
	input, err := os.Open(src)
	if err != nil {
		return ErrIO
	}
	defer input.Close()
	info, err := input.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return ErrUnsafePath
	}
	if info.Size() > maxFileBytes {
		return ErrLimit
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o700); err != nil {
		return ErrIO
	}
	output, err := os.CreateTemp(filepath.Dir(dest), ".workspace-backup-*")
	if err != nil {
		return ErrIO
	}
	defer os.Remove(output.Name())
	defer output.Close()
	written, err := io.Copy(output, io.LimitReader(input, maxFileBytes+1))
	if err != nil {
		return ErrIO
	}
	if written > maxFileBytes {
		return ErrLimit
	}
	if err := output.Chmod(info.Mode().Perm()); err != nil {
		return ErrIO
	}
	if err := output.Sync(); err != nil {
		return ErrIO
	}
	if err := output.Close(); err != nil {
		return ErrIO
	}
	if err := os.Chtimes(output.Name(), info.ModTime(), info.ModTime()); err != nil {
		return ErrIO
	}
	if err := os.Rename(output.Name(), dest); err != nil {
		return ErrIO
	}
	parent, err := os.Open(filepath.Dir(dest))
	if err != nil {
		return ErrIO
	}
	defer parent.Close()
	if err := parent.Sync(); err != nil {
		return ErrIO
	}
	return nil
}
