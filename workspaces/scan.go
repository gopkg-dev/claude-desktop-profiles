package workspaces

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

var uuidPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

const codeTree = "claude-code-sessions"

type record struct {
	SessionID      string `json:"sessionId"`
	CLISessionID   string `json:"cliSessionId"`
	Title          string `json:"title,omitempty"`
	CreatedAt      int64  `json:"createdAt"`
	LastActivityAt int64  `json:"lastActivityAt"`
	LastFocusedAt  int64  `json:"lastFocusedAt,omitempty"`
	Model          string `json:"model,omitempty"`
	Effort         string `json:"effort,omitempty"`
	Cwd            string `json:"cwd,omitempty"`
	OriginCwd      string `json:"originCwd,omitempty"`
	IsArchived     bool   `json:"isArchived,omitempty"`
	// importedFrom and resumeConfirmed say whether Desktop asks before resuming the session.
	ImportedFrom    string `json:"importedFrom,omitempty"`
	ResumeConfirmed bool   `json:"resumeConfirmed,omitempty"`
	// The session's permission mode, and whether auto or bypass was chosen in the app, as Desktop requires.
	PermissionMode    string          `json:"permissionMode,omitempty"`
	AutoChosenInApp   json.RawMessage `json:"autoChosenInApp,omitempty"`
	BypassChosenInApp *bool           `json:"bypassChosenInApp,omitempty"`
}

type candidate struct {
	source    Source
	kind      string
	namespace string
	indexPath string
	record    record
	target    bool
}

type inventory struct {
	best    map[string]candidate
	targets map[string]candidate
	tombs   map[string]bool
	ids     map[string]string
	// trusted marks sessions the user created or already agreed to resume under one of their accounts.
	trusted map[string]bool
	// modes is the most recently active record of each session that carries a permission mode.
	modes map[string]record
	count int
}

func newInventory() *inventory {
	return &inventory{
		best: make(map[string]candidate), targets: make(map[string]candidate),
		tombs: make(map[string]bool), ids: make(map[string]string),
		trusted: make(map[string]bool), modes: make(map[string]record),
	}
}

func validIdentity(identity Identity) bool {
	return uuidPattern.MatchString(identity.AccountID) && uuidPattern.MatchString(identity.OrganizationID)
}

func validSession(id string) bool {
	return strings.HasPrefix(id, "local_") && uuidPattern.MatchString(strings.TrimPrefix(id, "local_"))
}

func namespaces(source Source, kind string) []string {
	return []string{filepath.Join(source.Dir, kind, source.Identity.AccountID, source.Identity.OrganizationID)}
}

func targetNamespace(target Source, kind string) (string, error) {
	paths := namespaces(target, kind)
	for _, path := range paths {
		if err := checkPath(path); err != nil {
			return "", err
		}
		info, err := os.Lstat(path)
		if err == nil {
			if !info.IsDir() {
				return "", ErrUnsafePath
			}
			return path, nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return "", ErrIO
		}
	}
	return paths[len(paths)-1], nil
}

func (inv *inventory) scan(source Source, isTarget bool, report *Report) error {
	for _, kind := range []string{codeTree} {
		for _, namespace := range namespaces(source, kind) {
			if err := inv.scanNamespace(source, kind, namespace, isTarget, report); err != nil {
				return err
			}
		}
	}
	return nil
}

func (inv *inventory) scanNamespace(
	source Source,
	kind, namespace string,
	isTarget bool,
	report *Report,
) error {
	if err := checkPath(namespace); err != nil {
		return err
	}
	entries, err := os.ReadDir(namespace)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return ErrIO
	}
	if len(entries) > maxFiles {
		return ErrLimit
	}
	for _, entry := range entries {
		name := entry.Name()
		if strings.HasPrefix(name, "deleted_") {
			id := strings.TrimPrefix(strings.TrimPrefix(name, "deleted_"), "local_")
			if uuidPattern.MatchString(id) {
				if entry.Type()&os.ModeSymlink != 0 {
					return ErrUnsafePath
				}
				inv.tombs[kind+"/local_"+id] = true
			}
			continue
		}
		if !strings.HasSuffix(name, ".json") || !validSession(strings.TrimSuffix(name, ".json")) {
			continue
		}
		inv.count++
		if inv.count > maxIndices {
			return ErrLimit
		}
		path := filepath.Join(namespace, name)
		data, err := readBounded(path, maxIndexBytes)
		if err != nil {
			return err
		}
		var row record
		if json.Unmarshal(data, &row) != nil || row.SessionID != strings.TrimSuffix(name, ".json") ||
			!uuidPattern.MatchString(row.CLISessionID) || row.CreatedAt < 0 || row.LastActivityAt < 0 {
			report.skip("invalid_session_metadata")
			continue
		}
		key := kind + "/" + row.CLISessionID
		idKey := row.SessionID
		if previous, exists := inv.ids[idKey]; exists && previous != key {
			report.skip("conflicting_session_identity")
			continue
		}
		inv.ids[idKey] = key
		item := candidate{
			source: source, kind: kind, namespace: namespace, indexPath: path, record: row, target: isTarget,
		}
		if isTarget {
			if existing, ok := inv.targets[key]; !ok || row.LastActivityAt > existing.record.LastActivityAt {
				inv.targets[key] = item
			}
		}
		if existing, ok := inv.best[key]; !ok || row.LastActivityAt > existing.record.LastActivityAt {
			inv.best[key] = item
		}
		if row.ImportedFrom == "" || row.ResumeConfirmed {
			inv.trusted[key] = true
		}
		if len(permissionFields(row)) > 0 {
			if existing, ok := inv.modes[key]; !ok || row.LastActivityAt > existing.LastActivityAt {
				inv.modes[key] = row
			}
		}
	}
	return nil
}

func findTranscript(root, id string) (string, error) {
	if root == "" {
		return "", os.ErrNotExist
	}
	if err := checkPath(root); err != nil {
		return "", err
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", os.ErrNotExist
		}
		return "", ErrIO
	}
	if len(entries) > maxFiles {
		return "", ErrLimit
	}
	var found []string
	for _, entry := range entries {
		if entry.Type()&os.ModeSymlink != 0 || !entry.IsDir() {
			continue
		}
		path := filepath.Join(root, entry.Name(), id+".jsonl")
		if err := checkPath(path); err != nil {
			return "", err
		}
		info, err := os.Stat(path)
		if err == nil && info.Mode().IsRegular() {
			if info.Size() > maxFileBytes {
				return "", ErrLimit
			}
			found = append(found, path)
		} else if err != nil && !errors.Is(err, os.ErrNotExist) {
			return "", ErrIO
		}
	}
	if len(found) == 0 {
		return "", os.ErrNotExist
	}
	// Multiple slugs can retain a transcript after a project rename; newest wins.
	sort.SliceStable(found, func(left, right int) bool {
		a, errA := os.Stat(found[left])
		b, errB := os.Stat(found[right])
		return errA == nil && errB == nil && a.ModTime().After(b.ModTime())
	})
	return found[0], nil
}
