package profiles

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"claudeprofiles/workspaces"
)

type workspaceSyncer func([]workspaces.Source, workspaces.Source, string) (workspaces.Report, error)

// DisableSessionSharing lets the user switch normally while refreshing missing account metadata.
func (m *Manager) DisableSessionSharing() error {
	unlock, err := m.lock()
	if err != nil {
		return err
	}
	defer unlock()
	s, err := m.ready()
	if err != nil {
		return err
	}
	if !s.SharedSessions {
		return nil
	}
	s.SharedSessions, s.SharingPausedOn = false, s.ActiveID
	return m.save(s)
}

// ResumeSessionSharing turns sharing back on without stopping Desktop when it was paused and the same
// account has stayed active since: the other accounts have not run, so a merge would change nothing.
// It reports false, changing nothing, when sharing needs SyncSessions instead.
func (m *Manager) ResumeSessionSharing() (bool, error) {
	unlock, err := m.lock()
	if err != nil {
		return false, err
	}
	defer unlock()
	s, err := m.ready()
	if err != nil {
		return false, err
	}
	if s.SharedSessions {
		return true, nil
	}
	if s.SharingPausedOn == "" || s.SharingPausedOn != s.ActiveID {
		return false, nil
	}
	s.SharedSessions, s.SharingPausedOn = true, ""
	return true, m.save(s)
}

// SyncSessions imports verified local Code work and enables future Code sharing.
func (m *Manager) SyncSessions(ctx context.Context) (workspaces.Report, error) {
	unlock, err := m.lock()
	if err != nil {
		return workspaces.Report{}, err
	}
	defer unlock()
	s, err := m.ready()
	if err != nil {
		return workspaces.Report{}, err
	}
	ReportStep(ctx, StepQuit)
	if err := m.stop(ctx); err != nil {
		return workspaces.Report{}, err
	}
	if err := ctx.Err(); err != nil {
		return workspaces.Report{}, m.restartAfterSync(ctx, err)
	}
	ReportStep(ctx, StepSync)
	result, syncErr := m.syncWorkspacesStopped(&s, s.ActiveID)
	if syncErr != nil {
		if errors.Is(syncErr, workspaces.ErrRollback) {
			return result, syncErr
		}
		return result, m.restartAfterSync(ctx, syncErr)
	}
	s.SharedSessions, s.SharingPausedOn = true, ""
	if err := m.save(s); err != nil {
		return result, m.restartAfterSync(ctx, err)
	}
	ReportStep(ctx, StepLaunch)
	return result, m.restartAfterSync(ctx, nil)
}

func (m *Manager) syncWorkspacesStopped(s *manifest, targetID string) (workspaces.Report, error) {
	report := workspaces.Report{Reasons: map[string]int{}}
	if !filepath.IsAbs(m.paths.TranscriptRoot) {
		return report, errors.New("尚未配置本地 Code 对话目录，无法同步会话")
	}
	if err := checkedDir(m.paths.TranscriptRoot, true); err != nil {
		return report, err
	}
	target, reason, err := m.workspaceSource(s, targetID)
	if err != nil {
		return report, err
	}
	if reason != "" {
		return report, ErrSessionOrganization
	}
	sources := make([]workspaces.Source, 0, len(s.Profiles)-1)
	for _, p := range s.Profiles {
		if p.ID == targetID {
			continue
		}
		source, reason, sourceErr := m.workspaceSource(s, p.ID)
		if sourceErr != nil || reason != "" {
			if reason == "" {
				reason = "source_identity_unavailable"
			}
			report.Skipped++
			report.Reasons[reason]++
			continue
		}
		sources = append(sources, source)
	}
	if len(sources) == 0 {
		return report, ErrNoSessionSources
	}
	backupRoot, err := m.workspaceBackupRoot()
	if err != nil {
		return report, err
	}
	syncer := m.workspaceSync
	if syncer == nil {
		syncer = workspaces.Sync
	}
	result, err := syncer(sources, target, backupRoot)
	if result.Reasons == nil {
		result.Reasons = make(map[string]int)
	}
	result.Skipped += report.Skipped
	for reason, count := range report.Reasons {
		result.Reasons[reason] += count
	}
	if err != nil {
		if errors.Is(err, workspaces.ErrRollback) {
			marker := journal{Kind: "workspace", From: s.ActiveID, To: targetID, Backup: backupRoot}
			m.workspaceRecovery = &marker
			err = errors.Join(err, ErrWorkspaceRecovery, writeJSON(m.journalPath(), marker))
		}
		return result, fmt.Errorf("同步本地 Code 会话失败：%w", err)
	}
	// The record only informs the session page; a sync that wrote its files still succeeded.
	_ = m.saveSyncRecord(result)
	return result, nil
}

func (m *Manager) requireWorkspaceRepair(ctx context.Context, s manifest, marker journal) error {
	backupParent := filepath.Join(m.paths.StoreDir, "backups", "workspaces")
	if !validID(marker.From) || !validID(marker.To) || profileIndex(s, marker.To) < 0 || s.ActiveID != marker.From ||
		filepath.Dir(marker.Backup) != backupParent || !validID(filepath.Base(marker.Backup)) {
		return ErrInvalidState
	}
	m.workspaceRecovery = &marker
	if marker.From == marker.To {
		return errors.Join(ErrWorkspaceRecovery, m.stop(ctx))
	}
	return ErrWorkspaceRecovery
}

func (m *Manager) workspaceSource(s *manifest, id string) (workspaces.Source, string, error) {
	dir := m.profileDir(id)
	if id == s.ActiveID {
		dir = m.paths.LiveDir
	}
	accountID, err := m.reconcile(s, id, dir)
	if err != nil {
		return workspaces.Source{}, "source_identity_unavailable", err
	}
	if accountID == "" {
		return workspaces.Source{}, "source_not_signed_in", nil
	}
	details, status := m.profileDetails(m.loadOnlineView(), id, dir, accountID, time.Now())
	if status != DetailsAvailable || details == nil || details.OrganizationID == "" {
		return workspaces.Source{}, "source_details_unavailable", nil
	}
	return workspaces.Source{
		Dir: dir, LiveDir: m.paths.LiveDir, TranscriptRoot: m.paths.TranscriptRoot,
		Identity: workspaces.Identity{AccountID: accountID, OrganizationID: details.OrganizationID},
	}, "", nil
}

func (m *Manager) workspaceBackupRoot() (string, error) {
	parent := filepath.Join(m.paths.StoreDir, "backups")
	root := filepath.Join(parent, "workspaces")
	for _, dir := range []string{parent, root} {
		if err := checkedDir(dir, true); err != nil {
			return "", err
		}
		if err := os.Mkdir(dir, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
			return "", err
		}
	}
	id, err := newID()
	if err != nil {
		return "", err
	}
	backup := filepath.Join(root, id)
	if err := os.Mkdir(backup, 0o700); err != nil {
		return "", err
	}
	return backup, nil
}

func (m *Manager) restartAfterSync(ctx context.Context, cause error) error {
	// Relaunch is cleanup even if the original operation deadline has elapsed.
	restartCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 20*time.Second)
	defer cancel()
	return errors.Join(cause, m.start(restartCtx))
}
