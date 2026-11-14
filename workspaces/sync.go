package workspaces

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Sync unions offline local histories into target. The caller must stop Desktop and
// hold its profile mutation lock. Only files beneath target.Dir are changed; existing
// files are moved to backupRoot before replacement. TranscriptRoot is a projects
// directory, not a home directory. Sources should exclude target.
func Sync(sources []Source, target Source, backupRoot string) (Report, error) {
	report, err := syncOffline(sources, target, backupRoot)
	if err != nil {
		report.CodeCopied, report.CodeUpdated = 0, 0
		report.Tombstoned = 0
	}
	return report, err
}

func syncOffline(sources []Source, target Source, backupRoot string) (Report, error) {
	report := Report{Reasons: make(map[string]int)}
	if !validIdentity(target.Identity) {
		return report, ErrInvalidIdentity
	}
	if err := checkPath(target.Dir); err != nil {
		return report, err
	}
	inv := newInventory()
	if err := inv.scan(target, true, &report); err != nil {
		return report, err
	}
	seen := map[string]bool{target.Dir: true}
	for _, source := range sources {
		if seen[source.Dir] {
			continue
		}
		seen[source.Dir] = true
		if !validIdentity(source.Identity) {
			report.skip("source_identity_missing")
			continue
		}
		if err := checkPath(source.Dir); err != nil {
			return report, err
		}
		if err := inv.scan(source, false, &report); err != nil {
			return report, err
		}
	}
	tx, err := newTransaction(target.Dir, backupRoot)
	if err != nil {
		return report, err
	}
	defer tx.close()
	if err := applyTombstones(tx, inv, target, &report); err != nil {
		return report, err
	}
	if err := mergePermissionPrefs(tx, sources, target); err != nil {
		return report, err
	}
	keys := make([]string, 0, len(inv.best))
	for key := range inv.best {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		item := inv.best[key]
		existing, hasTarget := inv.targets[key]
		if inv.tombs[item.kind+"/"+item.record.SessionID] ||
			(hasTarget && inv.tombs[item.kind+"/"+existing.record.SessionID]) {
			continue
		}
		if item.target {
			if err := repairTarget(tx, item, target, inv.trusted[key], inv.modes[key], &report); err != nil {
				return report, err
			}
			continue
		}
		id := item.record.SessionID
		if hasTarget {
			id = existing.record.SessionID
		}
		err = importCode(tx, item, target, id, hasTarget, inv.trusted[key], inv.modes[key], &report)
		if err != nil {
			return report, err
		}
	}
	if err := tx.commit(); err != nil {
		return report, err
	}
	return report, nil
}

func applyTombstones(tx *transaction, inv *inventory, target Source, report *Report) error {
	keys := make([]string, 0, len(inv.tombs))
	for key := range inv.tombs {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		parts := strings.SplitN(key, "/", 2)
		kind, id := parts[0], parts[1]
		namespace, err := targetNamespace(target, kind)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(target.Dir, namespace)
		marker := filepath.Join(rel, "deleted_"+strings.TrimPrefix(id, "local_"))
		if err := tx.put(marker, nil); err != nil {
			return err
		}
		for _, path := range namespaces(target, kind) {
			index := filepath.Join(path, id+".json")
			if _, err := os.Lstat(index); errors.Is(err, os.ErrNotExist) {
				continue
			} else if err != nil {
				return ErrIO
			}
			relative, _ := filepath.Rel(target.Dir, index)
			if err := tx.remove(relative); err != nil {
				return err
			}
			report.Tombstoned++
		}
	}
	return nil
}

func importCode(
	tx *transaction,
	item candidate,
	target Source,
	id string,
	updating, trusted bool,
	mode record,
	report *Report,
) error {
	// Code uses the user's common CLI project transcripts. No account .claude
	// directory or credential file is copied into the target profile.
	_, err := findTranscript(target.TranscriptRoot, item.record.CLISessionID)
	if errors.Is(err, os.ErrNotExist) {
		report.skip("code_transcript_missing")
		return nil
	}
	if err != nil {
		return err
	}
	cwd, supported, err := codeWorkspace(tx, item, target, report)
	if err != nil {
		return err
	}
	if !supported {
		return nil
	}
	namespace, err := targetNamespace(target, codeTree)
	if err != nil {
		return err
	}
	row := map[string]any{
		"sessionId": id, "cliSessionId": item.record.CLISessionID,
		"cwd": cwd, "originCwd": cwd, "title": item.record.Title,
		"createdAt": item.record.CreatedAt, "lastActivityAt": item.record.LastActivityAt,
		"isArchived": item.record.IsArchived, "importedFrom": "local-1p-code",
	}
	if item.record.LastFocusedAt > 0 {
		row["lastFocusedAt"] = item.record.LastFocusedAt
	}
	if item.record.Model != "" {
		row["model"] = item.record.Model
	}
	if item.record.Effort != "" {
		row["effort"] = item.record.Effort
	}
	// The session keeps the mode it was last used in, and does not ask again before resuming
	// when the user created it, or already agreed to resume it, under one of their accounts.
	fields := permissionFields(item.record)
	if item.record.PermissionMode == "" {
		fields = permissionFields(mode)
	}
	for key, value := range fields {
		row[key] = value
	}
	if trusted {
		row["resumeConfirmed"] = true
	}
	rel, _ := filepath.Rel(target.Dir, filepath.Join(namespace, id+".json"))
	if err := putJSON(tx, rel, row); err != nil {
		return err
	}
	if updating {
		report.CodeUpdated++
	} else {
		report.CodeCopied++
	}
	return nil
}

func liveRoot(source Source) string {
	if source.LiveDir != "" {
		return source.LiveDir
	}
	return source.Dir
}

func codeWorkspace(tx *transaction, item candidate, target Source, report *Report) (string, bool, error) {
	cwd := item.record.Cwd
	if cwd == "" {
		cwd = item.record.OriginCwd
	}
	if !filepath.IsAbs(cwd) || filepath.Clean(cwd) != cwd {
		report.skip("code_workspace_invalid")
		return "", false, nil
	}
	for _, root := range []string{liveRoot(item.source), item.source.Dir} {
		if !within(root, cwd) {
			continue
		}
		rel, _ := filepath.Rel(root, cwd)
		if !strings.HasPrefix(rel, "scratch-workspaces"+string(filepath.Separator)) {
			report.skip("unsupported_managed_code_workspace")
			return "", false, nil
		}
		src := filepath.Join(item.source.Dir, rel)
		if _, err := os.Stat(src); errors.Is(err, os.ErrNotExist) {
			report.skip("code_scratch_workspace_missing")
			return "", false, nil
		} else if err != nil {
			return "", false, ErrIO
		}
		if err := tx.copyWorkTree(src, rel, report); err != nil {
			return "", false, err
		}
		return filepath.Join(liveRoot(target), rel), true, nil
	}
	if _, err := os.Stat(cwd); errors.Is(err, os.ErrNotExist) {
		report.Reasons["code_workspace_no_longer_exists"]++
	} else if err != nil {
		report.Reasons["code_workspace_not_accessible"]++
	}
	return cwd, true, nil
}

func putJSON(tx *transaction, rel string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return ErrIO
	}
	if int64(len(data))+1 > maxIndexBytes {
		return ErrLimit
	}
	return tx.put(rel, append(data, '\n'))
}
