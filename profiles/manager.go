package profiles

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

func labelValue(label string) (string, error) {
	label = strings.TrimSpace(label)
	if label == "" || utf8.RuneCountInString(label) > 60 {
		return "", ErrInvalidLabel
	}
	for _, ch := range label {
		if unicode.IsControl(ch) {
			return "", ErrInvalidLabel
		}
	}
	return label, nil
}

// Snapshot reports state and learns the identity after a profile's first login.
func (m *Manager) Snapshot() (Snapshot, error) {
	unlock, err := m.lock()
	if err != nil {
		return Snapshot{}, err
	}
	defer unlock()
	pending, err := present(m.journalPath())
	if err != nil {
		return Snapshot{}, err
	}
	pending = pending || m.workspaceRecovery != nil
	s, err := m.load()
	if err != nil {
		return Snapshot{PendingRecovery: pending}, err
	}
	result := Snapshot{
		Profiles: []Profile{}, ActiveID: s.ActiveID, PendingRecovery: pending, SharedSessions: s.SharedSessions,
	}
	if pending {
		var pendingJournal journal
		if m.workspaceRecovery != nil ||
			(readJSON(m.journalPath(), &pendingJournal) == nil && pendingJournal.Kind == "workspace") {
			result.RecoveryMessage = ErrWorkspaceRecovery.Error()
		}
		for _, p := range s.Profiles {
			p.Active, p.NeedsLogin = p.ID == s.ActiveID, p.AccountID == ""
			p.UsageStatus = UsagePendingRecovery
			p.DetailsStatus = DetailsPendingRecovery
			result.Profiles = append(result.Profiles, p)
		}
		return result, nil
	}
	if s.ActiveID == "" {
		liveExists, err := present(m.paths.LiveDir)
		if err != nil || !liveExists {
			return result, err
		}
		result.CurrentAccount, err = accountIdentity(m.paths.LiveDir)
		return result, err
	}
	view := m.loadOnlineView()
	for _, p := range s.Profiles {
		dir := m.profileDir(p.ID)
		if p.ID == s.ActiveID {
			dir = m.paths.LiveDir
		}
		actual, err := m.reconcile(&s, p.ID, dir)
		if err != nil {
			return result, err
		}
		p = s.Profiles[profileIndex(s, p.ID)]
		p.Active, p.NeedsLogin = p.ID == s.ActiveID, actual == ""
		p.Details, p.DetailsStatus = m.profileDetails(view, p.ID, dir, actual, time.Now())
		usageEntry, _ := m.usageEntry(view, p, dir)
		p.Usage, p.UsageStatus = usageEntry.Usage, usageEntry.Status
		p.RemoteUsageError = usageEntry.RemoteUsageError
		p.NextRefreshAt, p.RemoteUsageRetryAt = usageEntry.NextRefreshAt, usageEntry.RemoteUsageRetryAt
		result.Profiles = append(result.Profiles, p)
		if p.Active {
			result.CurrentAccount = actual
		}
	}
	return result, nil
}

// Adopt records the existing Desktop login without moving its files.
func (m *Manager) Adopt(label string) error {
	label, err := labelValue(label)
	if err != nil {
		return err
	}
	unlock, err := m.lock()
	if err != nil {
		return err
	}
	defer unlock()
	if err := m.checkJournal(); err != nil {
		return err
	}
	s, err := m.load()
	if err != nil {
		return err
	}
	if s.ActiveID != "" {
		return ErrAlreadyManaged
	}
	accountID, err := accountIdentity(m.paths.LiveDir)
	if errors.Is(err, os.ErrNotExist) || (err == nil && accountID == "") {
		return ErrNotSignedIn
	}
	if err != nil {
		return err
	}
	id, err := newID()
	if err != nil {
		return err
	}
	s.activate(id)
	s.Profiles = append(s.Profiles, Profile{ID: id, Label: label, AccountID: accountID})
	return m.save(s)
}

// Add opens a fresh Desktop environment for the user to sign in themselves.
func (m *Manager) Add(ctx context.Context, label string) error {
	label, err := labelValue(label)
	if err != nil {
		return err
	}
	unlock, err := m.lock()
	if err != nil {
		return err
	}
	defer unlock()
	s, err := m.ready()
	if err != nil {
		return err
	}
	ReportStep(ctx, StepQuit)
	if err := m.stop(ctx); err != nil {
		return err
	}
	if _, err := m.reconcile(&s, s.ActiveID, m.paths.LiveDir); err != nil {
		return m.restartAfterError(ctx, err)
	}
	id, err := newID()
	if err != nil {
		return m.restartAfterError(ctx, err)
	}
	if err := os.Mkdir(m.profileDir(id), 0o700); err != nil {
		return m.restartAfterError(ctx, err)
	}
	s.Profiles = append(s.Profiles, Profile{ID: id, Label: label})
	if err := m.save(s); err != nil {
		return m.restartAfterError(ctx, err)
	}
	return m.switchStopped(ctx, s, id, false)
}

// Switch replaces the default Desktop directory with the selected profile.
func (m *Manager) Switch(ctx context.Context, id string) error {
	unlock, err := m.lock()
	if err != nil {
		return err
	}
	defer unlock()
	s, err := m.ready()
	if err != nil {
		return err
	}
	if !validID(id) || profileIndex(s, id) < 0 {
		return ErrNotFound
	}
	if id == s.ActiveID {
		if _, err := m.reconcile(&s, id, m.paths.LiveDir); err != nil {
			return err
		}
		return m.start(ctx)
	}
	if _, err := m.reconcile(&s, id, m.profileDir(id)); err != nil {
		return err
	}
	ReportStep(ctx, StepQuit)
	if err := m.stop(ctx); err != nil {
		return err
	}
	return m.switchStopped(ctx, s, id, true)
}

// Rename changes only the displayed label.
func (m *Manager) Rename(id, label string) error {
	label, err := labelValue(label)
	if err != nil {
		return err
	}
	unlock, err := m.lock()
	if err != nil {
		return err
	}
	defer unlock()
	s, err := m.ready()
	if err != nil {
		return err
	}
	i := profileIndex(s, id)
	if i < 0 {
		return ErrNotFound
	}
	s.Profiles[i].Label = label
	return m.save(s)
}

// Archive preserves an inactive profile in the archive directory.
func (m *Manager) Archive(id string) error {
	unlock, err := m.lock()
	if err != nil {
		return err
	}
	defer unlock()
	s, err := m.ready()
	if err != nil {
		return err
	}
	if id == s.ActiveID {
		return ErrActiveProfile
	}
	i := profileIndex(s, id)
	if !validID(id) || i < 0 {
		return ErrNotFound
	}
	if err := checkedDir(m.profileDir(id), false); err != nil {
		return err
	}
	archiveRoot := filepath.Join(m.paths.StoreDir, "archives")
	if err := checkedDir(archiveRoot, true); err != nil {
		return err
	}
	if err := os.MkdirAll(archiveRoot, 0o700); err != nil {
		return err
	}
	archivePath := filepath.Join(archiveRoot, id)
	exists, err := present(archivePath)
	if err != nil {
		return err
	}
	if exists {
		return errors.New("归档位置已存在，未覆盖任何数据")
	}
	previous := s.Profiles[i]
	if err := writeJSON(filepath.Join(archiveRoot, id+".json"), previous); err != nil {
		return err
	}
	if err := writeJSON(m.journalPath(), journal{Kind: "archive", From: id, To: s.ActiveID}); err != nil {
		return err
	}
	if err := m.afterStep("archive-journal"); err != nil {
		return err
	}
	if err := renameDirectory(m.profileDir(id), archivePath); err != nil {
		return err
	}
	if err := m.afterStep("archive-move"); err != nil {
		return err
	}
	s.Profiles = append(s.Profiles[:i:i], s.Profiles[i+1:]...)
	if err := m.save(s); err != nil {
		return err
	}
	if err := m.afterStep("archive-state"); err != nil {
		return err
	}
	return m.clearJournal()
}

func (m *Manager) ready() (manifest, error) {
	if err := m.checkJournal(); err != nil {
		return manifest{}, err
	}
	s, err := m.load()
	if err != nil {
		return manifest{}, err
	}
	if s.ActiveID == "" {
		return manifest{}, ErrNotManaged
	}
	return s, nil
}

func (m *Manager) stop(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if m.driver == nil {
		return errors.New("未配置 Claude Desktop 进程控制")
	}
	return m.driver.Stop(ctx)
}

func (m *Manager) start(ctx context.Context) error {
	if m.driver == nil {
		return errors.New("未配置 Claude Desktop 进程控制")
	}
	if err := m.driver.Start(ctx); err != nil {
		return fmt.Errorf("账号数据已就绪，但 Claude Desktop 未能打开：%w", err)
	}
	return nil
}

func (m *Manager) restartAfterError(ctx context.Context, cause error) error {
	return errors.Join(cause, m.start(ctx))
}
