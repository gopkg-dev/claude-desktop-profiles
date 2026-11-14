package workspaces

import (
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Session issues explain why a session cannot reach every account.
const (
	IssueTranscriptMissing = "transcript_missing"
	IssueManagedWorkspace  = "managed_workspace"
	IssueWorkspaceMissing  = "workspace_missing"
)

// Session is one Code session as it appears across the listed sources, without its transcript.
type Session struct {
	CLISessionID   string
	Title          string
	Cwd            string
	CreatedAt      time.Time
	LastActivityAt time.Time
	Archived       bool
	// Present has one entry per listed source: whether that account's session list holds it.
	Present []bool
	Issue   string
}

// List reads the session indexes of sources without changing any file. Sessions deleted in any
// source are left out, as a sync would delete them everywhere. The newest first.
func List(sources []Source) ([]Session, error) {
	type merged struct {
		best    candidate
		present []bool
	}
	byKey := map[string]*merged{}
	deleted := map[string]bool{}
	for index, source := range sources {
		if !validIdentity(source.Identity) {
			continue
		}
		if err := checkPath(source.Dir); err != nil {
			return nil, err
		}
		inv := newInventory()
		if err := inv.scan(source, false, &Report{}); err != nil {
			return nil, err
		}
		for key := range inv.tombs {
			deleted[key] = true
		}
		for key, item := range inv.best {
			entry := byKey[key]
			if entry == nil {
				entry = &merged{best: item, present: make([]bool, len(sources))}
				byKey[key] = entry
			} else if item.record.LastActivityAt > entry.best.record.LastActivityAt {
				entry.best = item
			}
			entry.present[index] = true
		}
	}
	sessions := make([]Session, 0, len(byKey))
	for _, entry := range byKey {
		record := entry.best.record
		if deleted[entry.best.kind+"/"+record.SessionID] {
			continue
		}
		cwd := record.Cwd
		if cwd == "" {
			cwd = record.OriginCwd
		}
		session := Session{
			CLISessionID: record.CLISessionID, Title: record.Title, Cwd: cwd,
			CreatedAt: millis(record.CreatedAt), LastActivityAt: millis(record.LastActivityAt),
			Archived: record.IsArchived, Present: entry.present,
		}
		session.Issue = sessionIssue(entry.best, cwd)
		sessions = append(sessions, session)
	}
	sort.SliceStable(sessions, func(i, j int) bool {
		if !sessions[i].LastActivityAt.Equal(sessions[j].LastActivityAt) {
			return sessions[i].LastActivityAt.After(sessions[j].LastActivityAt)
		}
		return sessions[i].CLISessionID < sessions[j].CLISessionID
	})
	return sessions, nil
}

func sessionIssue(item candidate, cwd string) string {
	if _, err := findTranscript(item.source.TranscriptRoot, item.record.CLISessionID); err != nil {
		return IssueTranscriptMissing
	}
	if !filepath.IsAbs(cwd) || filepath.Clean(cwd) != cwd {
		return IssueWorkspaceMissing
	}
	for _, root := range []string{liveRoot(item.source), item.source.Dir} {
		if !within(root, cwd) {
			continue
		}
		rel, _ := filepath.Rel(root, cwd)
		if !strings.HasPrefix(rel, "scratch-workspaces"+string(filepath.Separator)) {
			return IssueManagedWorkspace
		}
		if _, err := os.Stat(filepath.Join(item.source.Dir, rel)); errors.Is(err, os.ErrNotExist) {
			return IssueWorkspaceMissing
		}
		return ""
	}
	if _, err := os.Stat(cwd); err != nil {
		return IssueWorkspaceMissing
	}
	return ""
}

func millis(value int64) time.Time {
	if value <= 0 {
		return time.Time{}
	}
	return time.UnixMilli(value)
}
