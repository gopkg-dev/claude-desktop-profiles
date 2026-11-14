package profiles

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"time"

	"claudeprofiles/desktopusage"
)

const remoteUsageCooldown = time.Minute

var (
	ErrRemoteUsageUnavailable = errors.New("当前环境不支持在线刷新额度")
	ErrRemoteUsageCooldown    = errors.New("刷新较频繁，请稍后再试")
	ErrRemoteUsageChanged     = errors.New("刷新期间账号或组织已变化，请重新刷新")
	ErrRemoteUsageInvalid     = errors.New("额度查询返回的数据无法识别，已保留上次结果")
)

type remoteUsageTarget struct {
	profileID  string
	target     desktopusage.Target
	activeID   string
	dirInfo    os.FileInfo
	configInfo os.FileInfo
}

// SetUsageQuerier configures explicit refreshes without reading files or sending requests.
func (m *Manager) SetUsageQuerier(querier AccountQuerier) {
	m.remoteMu.Lock()
	defer m.remoteMu.Unlock()
	m.accountQuery = querier
}

func (m *Manager) accountQuerier() AccountQuerier {
	m.remoteMu.Lock()
	defer m.remoteMu.Unlock()
	return m.accountQuery
}

// RefreshRemoteUsage performs one explicit query, ignoring the cache lifetime but not cooldowns.
// Only display data is cached; credentials are owned by the querier.
func (m *Manager) RefreshRemoteUsage(ctx context.Context, id string) error {
	querier := m.accountQuerier()
	if querier == nil {
		return ErrRemoteUsageUnavailable
	}
	return m.refreshOnlineAccount(ctx, id, querier, true)
}

// remoteUsageTarget is called with the directory mutation lock held.
func (m *Manager) remoteUsageTarget(id string) (remoteUsageTarget, error) {
	state, err := m.ready()
	if err != nil {
		return remoteUsageTarget{}, err
	}
	index := profileIndex(state, id)
	if !validID(id) || index < 0 {
		return remoteUsageTarget{}, ErrNotFound
	}
	profile := state.Profiles[index]
	if profile.AccountID == "" {
		return remoteUsageTarget{}, ErrNotSignedIn
	}
	dir := m.profileDir(id)
	if id == state.ActiveID {
		dir = m.paths.LiveDir
	}
	if err := checkedDir(dir, false); err != nil {
		return remoteUsageTarget{}, ErrUnsafePath
	}
	dirInfo, err := os.Lstat(dir)
	if err != nil {
		return remoteUsageTarget{}, ErrUnsafePath
	}
	configInfo, err := os.Lstat(filepath.Join(dir, "config.json"))
	if err != nil || !configInfo.Mode().IsRegular() {
		return remoteUsageTarget{}, ErrUnsafePath
	}
	account, err := accountIdentity(dir)
	if err != nil || account != profile.AccountID {
		return remoteUsageTarget{}, ErrIdentityMismatch
	}
	return remoteUsageTarget{
		profileID: id,
		target:    desktopusage.Target{Directory: dir, AccountID: account},
		activeID:  state.ActiveID, dirInfo: dirInfo, configInfo: configInfo,
	}, nil
}

func sameRemoteUsageTarget(before, after remoteUsageTarget) bool {
	return before.profileID == after.profileID && before.target == after.target &&
		before.activeID == after.activeID && os.SameFile(before.dirInfo, after.dirInfo) &&
		os.SameFile(before.configInfo, after.configInfo) && before.configInfo.Size() == after.configInfo.Size() &&
		before.configInfo.ModTime().Equal(after.configInfo.ModTime())
}
