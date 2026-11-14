package desktopui

import (
	"fmt"
	"strings"
	"time"

	"claudeprofiles/profiles"
)

type onlineRefreshFailure struct {
	accountID string
	message   string
	attempted time.Time
}

func refreshCooldown(profile profiles.Profile, now time.Time) bool {
	return profile.RemoteUsageRetryAt.After(now)
}

func (a *app) onlineRefreshHint(id string) string {
	snapshot := a.snapshot.Get()
	if snapshot == nil {
		return "正在读取账号状态"
	}
	now := time.Now()
	var earliest time.Time
	for _, profile := range snapshot.Profiles {
		if profile.NeedsLogin || id != "" && profile.ID != id {
			continue
		}
		if !refreshCooldown(profile, now) {
			if id != "" {
				return "在线刷新此账号的资料和额度"
			}
			return "在线刷新可查询的账号；冷却中的账号会跳过"
		}
		if earliest.IsZero() || profile.RemoteUsageRetryAt.Before(earliest) {
			earliest = profile.RemoteUsageRetryAt
		}
	}
	if !earliest.IsZero() {
		seconds := max(1, int(time.Until(earliest).Seconds()+0.999))
		return fmt.Sprintf("冷却中，%d 秒后可刷新（%s）", seconds, earliest.In(time.Local).Format("15:04:05"))
	}
	return "没有可在线刷新的已登录账号"
}

func (a *app) setOnlineFailureNotice() {
	current := a.snapshot.Get()
	if current == nil {
		return
	}
	a.onlineFailureNotice = a.onlineFailureText(current)
	if a.onlineFailureNotice != "" {
		a.failure.Set(a.onlineFailureNotice)
	}
}

func (a *app) onlineFailureText(current *profiles.Snapshot) string {
	var reasons []string
	for _, profile := range current.Profiles {
		if failure, ok := a.onlineFailures[profile.ID]; ok {
			reasons = append(reasons, accountEmail(profile)+"："+failure.message)
		}
	}
	if len(reasons) == 0 {
		return ""
	}
	return fmt.Sprintf("%d 个账号未更新，上次数据已保留。%s", len(reasons), strings.Join(reasons, "；"))
}

// reconcileOnlineFailures keeps errors returned before persistence until a newer successful sample resolves them.
func (a *app) reconcileOnlineFailures(snapshot *profiles.Snapshot) {
	if len(a.onlineFailures) > 0 {
		snapshot.Profiles = append([]profiles.Profile(nil), snapshot.Profiles...)
	}
	alive := make(map[string]bool, len(snapshot.Profiles))
	for i := range snapshot.Profiles {
		profile := &snapshot.Profiles[i]
		alive[profile.ID] = true
		failure, ok := a.onlineFailures[profile.ID]
		if !ok {
			continue
		}
		if failure.accountID != profile.AccountID || profile.RemoteUsageError == "" &&
			profile.Usage != nil && !profile.Usage.UpdatedAt.Before(failure.attempted) {
			delete(a.onlineFailures, profile.ID)
			continue
		}
		if profile.RemoteUsageError == "" {
			profile.RemoteUsageError = failure.message
		}
	}
	for id := range a.onlineFailures {
		if !alive[id] {
			delete(a.onlineFailures, id)
		}
	}
	if a.onlineFailureNotice != "" && a.failure.Get() == a.onlineFailureNotice {
		a.onlineFailureNotice = a.onlineFailureText(snapshot)
		a.failure.Set(a.onlineFailureNotice)
	} else if len(a.onlineFailures) == 0 {
		a.onlineFailureNotice = ""
	}
}
