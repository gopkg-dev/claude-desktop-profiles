package profiles

import "time"

// UsageSnapshot restores cached online usage after checking account ownership.
func (m *Manager) UsageSnapshot() (UsageSnapshot, error) {
	unlock, err := m.lock()
	if err != nil {
		return UsageSnapshot{NeedsRefresh: true}, err
	}
	defer unlock()
	pending, err := present(m.journalPath())
	if err != nil {
		return UsageSnapshot{NeedsRefresh: true}, err
	}
	pending = pending || m.workspaceRecovery != nil
	s, err := m.load()
	if err != nil {
		return UsageSnapshot{PendingRecovery: pending, NeedsRefresh: true}, err
	}
	result := UsageSnapshot{
		ActiveID: s.ActiveID, Entries: make([]UsageEntry, 0, len(s.Profiles)),
		PendingRecovery: pending, NeedsRefresh: pending,
	}
	if pending {
		for _, p := range s.Profiles {
			result.Entries = append(result.Entries, UsageEntry{ID: p.ID, Status: UsagePendingRecovery})
		}
		return result, nil
	}
	if s.ActiveID == "" {
		liveExists, err := present(m.paths.LiveDir)
		if err != nil {
			result.NeedsRefresh = true
			return result, err
		}
		if liveExists {
			actual, identityErr := accountIdentity(m.paths.LiveDir)
			result.NeedsRefresh = identityErr != nil || actual != ""
		}
		return result, nil
	}
	view := m.loadOnlineView()
	for _, p := range s.Profiles {
		dir := m.profileDir(p.ID)
		if p.ID == s.ActiveID {
			dir = m.paths.LiveDir
		}
		entry, refresh := m.usageEntry(view, p, dir)
		result.Entries = append(result.Entries, entry)
		result.NeedsRefresh = result.NeedsRefresh || refresh
	}
	return result, nil
}

func (m *Manager) usageEntry(view onlineView, p Profile, dir string) (UsageEntry, bool) {
	entry := UsageEntry{ID: p.ID, Status: UsageUnavailable}
	actual, err := accountIdentity(dir)
	if err != nil || actual != p.AccountID {
		return entry, true
	}
	if actual == "" {
		entry.Status = UsageMissing
		return entry, false
	}
	cached, cacheErr := view.account(p.ID, actual)
	// External login changes can happen despite our own profile mutation lock.
	after, err := accountIdentity(dir)
	if err != nil || after != actual {
		return entry, true
	}
	if cacheErr != nil {
		entry.Status = UsageInvalid
		return entry, false
	}
	if staleLoginFailure(cached, dir) {
		cached.LastError, cached.NextAllowed = "", time.Time{}
	}
	entry.Status = UsageMissing
	entry.RemoteUsageError = cached.LastError
	entry.RemoteUsageRetryAt, entry.NextRefreshAt = cached.NextAllowed, cached.NextAllowed
	if cached.Usage != nil {
		entry.Usage, entry.Status = cloneUsage(cached.Usage), UsageAvailable
		until := cached.Usage.UpdatedAt.Add(time.Duration(view.settings.CacheMinutes) * time.Minute)
		if until.After(entry.NextRefreshAt) {
			entry.NextRefreshAt = until
		}
	}
	return entry, false
}
