package profiles

import "time"

// PreviewSnapshot loads account labels and this app's cached online details
// without reconciling profile state. The result is provisional:
// callers must disable account actions until a complete Snapshot has succeeded.
func (m *Manager) PreviewSnapshot() (Snapshot, error) {
	unlock, err := m.lock()
	if err != nil {
		return Snapshot{Preview: true}, err
	}
	defer unlock()
	pending, err := present(m.journalPath())
	if err != nil {
		return Snapshot{Preview: true}, err
	}
	pending = pending || m.workspaceRecovery != nil
	state, err := m.load()
	if err != nil {
		return Snapshot{Preview: true, PendingRecovery: pending}, err
	}
	result := Snapshot{Preview: true, Profiles: []Profile{}, ActiveID: state.ActiveID,
		PendingRecovery: pending, SharedSessions: state.SharedSessions}
	if pending {
		var pendingJournal journal
		if m.workspaceRecovery != nil ||
			(readJSON(m.journalPath(), &pendingJournal) == nil && pendingJournal.Kind == "workspace") {
			result.RecoveryMessage = ErrWorkspaceRecovery.Error()
		}
	}
	if state.ActiveID == "" {
		if pending {
			return result, nil
		}
		liveExists, err := present(m.paths.LiveDir)
		if err != nil || !liveExists {
			return result, err
		}
		result.CurrentAccount, err = accountIdentity(m.paths.LiveDir)
		return result, err
	}
	now, view := time.Now(), m.loadOnlineView()
	for _, profile := range state.Profiles {
		profile.Active = profile.ID == state.ActiveID
		profile.NeedsLogin = profile.AccountID == ""
		profile.UsageStatus = UsageMissing
		profile.DetailsStatus = DetailsUnavailable
		if pending {
			profile.UsageStatus, profile.DetailsStatus = UsagePendingRecovery, DetailsPendingRecovery
			result.Profiles = append(result.Profiles, profile)
			continue
		}
		dir := m.profileDir(profile.ID)
		if profile.Active {
			dir = m.paths.LiveDir
		}
		actual, identityErr := accountIdentity(dir)
		if identityErr != nil || actual != profile.AccountID || actual == "" {
			if identityErr == nil && actual != profile.AccountID {
				profile.DetailsStatus = DetailsMismatch
			}
			result.Profiles = append(result.Profiles, profile)
			continue
		}
		profile.Details, profile.DetailsStatus = m.profileDetails(view, profile.ID, dir, actual, now)
		if profile.DetailsStatus != DetailsAvailable {
			profile.Details = nil
		}
		if profile.Active && profile.DetailsStatus != DetailsMismatch {
			result.CurrentAccount = actual
		}
		result.Profiles = append(result.Profiles, profile)
	}
	return result, nil
}
