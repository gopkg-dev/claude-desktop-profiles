package profiles

import (
	"context"
	"errors"
	"net/netip"
	"os"
	"path/filepath"
	"time"

	"claudeprofiles/desktopusage"
)

// ErrExitCooldown prevents repeated exit queries, including after a restart.
var ErrExitCooldown = errors.New("出口查询至少间隔 1 分钟，请稍后重试")

func (m *Manager) exitInfoPath() string { return filepath.Join(m.paths.StoreDir, "exit-info.json") }

func (m *Manager) readExitInfo() (desktopusage.ExitInfo, error) {
	var value desktopusage.ExitInfo
	err := readJSON(m.exitInfoPath(), &value)
	if errors.Is(err, os.ErrNotExist) {
		return value, nil
	}
	if err != nil {
		return desktopusage.ExitInfo{}, err
	}
	if value.IP != "" {
		if _, err := netip.ParseAddr(value.IP); err != nil {
			return desktopusage.ExitInfo{}, ErrInvalidState
		}
	}
	if value.Failures < 0 || value.Failures > 10 {
		return desktopusage.ExitInfo{}, ErrInvalidState
	}
	return value, nil
}

// ExitInfo restores the last observation without making network requests.
func (m *Manager) ExitInfo() (desktopusage.ExitInfo, error) {
	unlock, err := m.lock()
	if err != nil {
		return desktopusage.ExitInfo{}, err
	}
	defer unlock()
	return m.readExitInfo()
}

// RefreshExit explicitly updates trace and GEO with durable throttling.
func (m *Manager) RefreshExit(ctx context.Context) (desktopusage.ExitInfo, error) {
	if err := ctx.Err(); err != nil {
		return desktopusage.ExitInfo{}, desktopusage.ErrCanceled
	}
	unlock, err := m.lock()
	if err != nil {
		return desktopusage.ExitInfo{}, err
	}
	previous, err := m.readExitInfo()
	if err != nil {
		unlock()
		return previous, err
	}
	if previous.NextAllowed.After(time.Now()) {
		unlock()
		return previous, ErrExitCooldown
	}
	previous.NextAllowed = time.Now().Add(time.Minute)
	if err := writeJSON(m.exitInfoPath(), previous); err != nil {
		unlock()
		return previous, err
	}
	unlock()
	query := m.exitQuerier
	if query == nil {
		query = (&desktopusage.ExitClient{LiveDir: m.paths.LiveDir}).Query
	}
	result, queryErr := query(ctx, previous)
	if ctx.Err() != nil {
		queryErr = desktopusage.ErrCanceled
	}
	if queryErr != nil {
		retryAt := result.NextAllowed
		result = previous
		result.Failures = min(previous.Failures+1, 10)
		result.LastError = desktopusage.UserMessage(queryErr)
		result.NextAllowed = time.Now().Add(time.Minute * time.Duration(1<<min(result.Failures-1, 6)))
		if retryAt.After(result.NextAllowed) {
			result.NextAllowed = retryAt
		}
	} else {
		result.NextAllowed = previous.NextAllowed
		result.Failures = 0
		result.LastError = ""
	}
	unlock, err = m.lock()
	if err != nil {
		return previous, err
	}
	defer unlock()
	if err := writeJSON(m.exitInfoPath(), result); err != nil {
		return previous, err
	}
	return result, queryErr
}
