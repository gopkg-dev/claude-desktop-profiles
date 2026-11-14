package profiles

// ProfileDirectory resolves the account's current directory under the manager lock.
// An unfinished switch blocks resolution until the user recovers it.
func (m *Manager) ProfileDirectory(id string) (string, error) {
	unlock, err := m.lock()
	if err != nil {
		return "", err
	}
	defer unlock()
	s, err := m.ready()
	if err != nil {
		return "", err
	}
	if !validID(id) || profileIndex(s, id) < 0 {
		return "", ErrNotFound
	}
	dir := m.profileDir(id)
	if id == s.ActiveID {
		dir = m.paths.LiveDir
	}
	if err := checkedDir(dir, false); err != nil {
		return "", err
	}
	return dir, nil
}
