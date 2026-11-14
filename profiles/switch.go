package profiles

import (
	"context"
	"errors"
	"os"
	"path/filepath"
)

func (m *Manager) switchStopped(ctx context.Context, s manifest, id string, allowSharing bool) error {
	sourceAccount, err := m.reconcile(&s, s.ActiveID, m.paths.LiveDir)
	if err != nil {
		return m.restartAfterError(ctx, err)
	}
	targetAccount, err := m.reconcile(&s, id, m.profileDir(id))
	if err != nil {
		return m.restartAfterError(ctx, err)
	}
	parked := m.profileDir(s.ActiveID)
	exists, err := present(parked)
	if err != nil {
		return m.restartAfterError(ctx, err)
	}
	if exists {
		return m.restartAfterError(ctx, errors.New("当前账号的暂存位置已被占用，未覆盖任何数据"))
	}
	if allowSharing && s.SharedSessions && sourceAccount != "" && targetAccount != "" {
		ReportStep(ctx, StepSync)
		if _, err := m.syncWorkspacesStopped(&s, id); err != nil {
			return m.restartAfterSync(ctx, err)
		}
	}
	if err := ctx.Err(); err != nil {
		return m.restartAfterError(ctx, err)
	}
	ReportStep(ctx, StepMove)
	j := journal{From: s.ActiveID, To: id}
	if err := writeJSON(m.journalPath(), j); err != nil {
		return m.restartAfterError(ctx, err)
	}
	if err := m.afterStep("journal"); err != nil {
		return err
	}
	if err := renameDirectory(m.paths.LiveDir, parked); err != nil {
		return err
	}
	if err := m.afterStep("park"); err != nil {
		return err
	}
	if err := renameDirectory(m.profileDir(id), m.paths.LiveDir); err != nil {
		return err
	}
	if err := m.afterStep("activate"); err != nil {
		return err
	}
	s.activate(id)
	if err := m.save(s); err != nil {
		return err
	}
	if err := m.afterStep("state"); err != nil {
		return err
	}
	if err := m.clearJournal(); err != nil {
		return err
	}
	ReportStep(ctx, StepLaunch)
	return m.start(ctx)
}

func renameDirectory(from, to string) error {
	if err := checkedDir(from, false); err != nil {
		return err
	}
	exists, err := present(to)
	if err != nil {
		return err
	}
	if exists {
		return ErrUnsafePath
	}
	if err := os.Rename(from, to); err != nil {
		return err
	}
	if err := syncDir(filepath.Dir(from)); err != nil {
		return err
	}
	return syncDir(filepath.Dir(to))
}

func (m *Manager) afterStep(step string) error {
	if m.step == nil {
		return nil
	}
	return m.step(step)
}

func (m *Manager) clearJournal() error {
	if err := os.Remove(m.journalPath()); err != nil {
		return err
	}
	return syncDir(m.paths.StoreDir)
}

// Recover finishes an interrupted switch after stopping every Desktop process.
func (m *Manager) Recover(ctx context.Context) error {
	unlock, err := m.lock()
	if err != nil {
		return err
	}
	defer unlock()
	var j journal
	if err := readJSON(m.journalPath(), &j); err != nil {
		if errors.Is(err, os.ErrNotExist) && m.workspaceRecovery == nil {
			return nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		j = *m.workspaceRecovery
	}
	s, err := m.load()
	if err != nil {
		return err
	}
	if j.Kind == "archive" {
		return m.recoverArchive(s, j)
	}
	if j.Kind == "workspace" {
		return m.requireWorkspaceRepair(ctx, s, j)
	}
	if j.Kind != "" {
		return ErrInvalidState
	}
	if !validID(j.From) || !validID(j.To) || j.From == j.To ||
		profileIndex(s, j.From) < 0 || profileIndex(s, j.To) < 0 ||
		(s.ActiveID != j.From && s.ActiveID != j.To) {
		return ErrInvalidState
	}
	if err := m.stop(ctx); err != nil {
		return err
	}
	live, err := present(m.paths.LiveDir)
	if err != nil {
		return err
	}
	parked, err := present(m.profileDir(j.From))
	if err != nil {
		return err
	}
	target, err := present(m.profileDir(j.To))
	if err != nil {
		return err
	}
	switch {
	case live && !parked && target:
		if _, err := m.reconcile(&s, j.From, m.paths.LiveDir); err != nil {
			return err
		}
		s.activate(j.From)
	case !live && parked && target:
		if _, err := m.reconcile(&s, j.From, m.profileDir(j.From)); err != nil {
			return err
		}
		if _, err := m.reconcile(&s, j.To, m.profileDir(j.To)); err != nil {
			return err
		}
		if err := renameDirectory(m.profileDir(j.To), m.paths.LiveDir); err != nil {
			return err
		}
		s.activate(j.To)
	case live && parked && !target:
		if _, err := m.reconcile(&s, j.To, m.paths.LiveDir); err != nil {
			return err
		}
		s.activate(j.To)
	default:
		return errors.New("切换目录状态不明确，未移动数据；请保留数据目录和切换记录以便恢复")
	}
	if err := m.save(s); err != nil {
		return err
	}
	if err := m.clearJournal(); err != nil {
		return err
	}
	return m.start(ctx)
}

func (m *Manager) recoverArchive(s manifest, j journal) error {
	if !validID(j.From) || !validID(j.To) || j.From == j.To || s.ActiveID != j.To {
		return ErrInvalidState
	}
	archivePath := filepath.Join(m.paths.StoreDir, "archives", j.From)
	var archivedProfile Profile
	if err := readJSON(archivePath+".json", &archivedProfile); err != nil {
		return err
	}
	if archivedProfile.ID != j.From {
		return ErrInvalidState
	}
	parked, err := present(m.profileDir(j.From))
	if err != nil {
		return err
	}
	archived, err := present(archivePath)
	if err != nil {
		return err
	}
	i := profileIndex(s, j.From)
	switch {
	case parked && !archived && i >= 0:
		if err := checkedDir(m.profileDir(j.From), false); err != nil {
			return err
		}
		return m.clearJournal()
	case !parked && archived:
		if err := checkedDir(archivePath, false); err != nil {
			return err
		}
		if i >= 0 {
			s.Profiles = append(s.Profiles[:i:i], s.Profiles[i+1:]...)
			if err := m.save(s); err != nil {
				return err
			}
		}
		return m.clearJournal()
	default:
		return errors.New("归档目录状态不明确，未移动数据；请保留数据目录和恢复记录")
	}
}
