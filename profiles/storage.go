package profiles

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
)

func (m *Manager) profileDir(id string) string {
	return filepath.Join(m.paths.StoreDir, "profiles", id)
}

func (m *Manager) statePath() string   { return filepath.Join(m.paths.StoreDir, "state.json") }
func (m *Manager) journalPath() string { return filepath.Join(m.paths.StoreDir, "switch.json") }

func validID(id string) bool {
	if len(id) != 32 {
		return false
	}
	for _, ch := range id {
		if (ch < '0' || ch > '9') && (ch < 'a' || ch > 'f') {
			return false
		}
	}
	return true
}

func newID() (string, error) {
	var bytes [16]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(bytes[:]), nil
}

func checkedDir(path string, absentOK bool) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) && absentOK {
		return nil
	}
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return ErrUnsafePath
	}
	return nil
}

func checkedFile(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return ErrUnsafePath
	}
	return nil
}

func present(path string) (bool, error) {
	_, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return err == nil, err
}

func (m *Manager) prepare() error {
	live, store := filepath.Clean(m.paths.LiveDir), filepath.Clean(m.paths.StoreDir)
	if !filepath.IsAbs(live) || !filepath.IsAbs(store) || live == store || live == "/" || store == "/" {
		return ErrUnsafePath
	}
	if strings.HasPrefix(live, store+string(os.PathSeparator)) ||
		strings.HasPrefix(store, live+string(os.PathSeparator)) {
		return ErrUnsafePath
	}
	for _, dir := range []string{live, store, filepath.Join(store, "profiles"), filepath.Join(store, "archives")} {
		if err := checkedDir(dir, true); err != nil {
			return err
		}
	}
	if err := os.MkdirAll(filepath.Join(store, "profiles"), 0o700); err != nil {
		return err
	}
	return nil
}

func readJSON(path string, target any) error {
	if err := checkedFile(path); err != nil {
		return err
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	dec := json.NewDecoder(io.LimitReader(f, 1024*1024))
	if err := dec.Decode(target); err != nil {
		return ErrInvalidState
	}
	if dec.Decode(&struct{}{}) != io.EOF {
		return ErrInvalidState
	}
	return nil
}

func writeJSON(path string, value any) error {
	if err := checkedFile(path); err != nil {
		return err
	}
	b, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".write-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err := f.Write(append(b, '\n')); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(f.Name(), path); err != nil {
		return err
	}
	return syncDir(filepath.Dir(path))
}

func syncDir(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}

func (m *Manager) load() (manifest, error) {
	s := manifest{Version: 1, Profiles: []Profile{}}
	if err := readJSON(m.statePath(), &s); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return s, nil
		}
		return manifest{}, err
	}
	if s.Version != 1 {
		return manifest{}, ErrInvalidState
	}
	ids, accounts := map[string]bool{}, map[string]bool{}
	for _, p := range s.Profiles {
		if !validID(p.ID) || ids[p.ID] || strings.TrimSpace(p.Label) == "" {
			return manifest{}, ErrInvalidState
		}
		if p.AccountID != "" && accounts[p.AccountID] {
			return manifest{}, ErrDuplicateAccount
		}
		ids[p.ID], accounts[p.AccountID] = true, true
	}
	if (s.ActiveID != "" && !ids[s.ActiveID]) || (s.ActiveID == "" && len(s.Profiles) != 0) {
		return manifest{}, ErrInvalidState
	}
	return s, nil
}

func (m *Manager) save(s manifest) error {
	for i := range s.Profiles {
		s.Profiles[i].Active, s.Profiles[i].NeedsLogin = false, false
	}
	return writeJSON(m.statePath(), s)
}

func profileIndex(s manifest, id string) int {
	for i, p := range s.Profiles {
		if p.ID == id {
			return i
		}
	}
	return -1
}

func (m *Manager) checkJournal() error {
	if m.workspaceRecovery != nil {
		return ErrRecoveryRequired
	}
	exists, err := present(m.journalPath())
	if err != nil {
		return err
	}
	if exists {
		return ErrRecoveryRequired
	}
	return nil
}
