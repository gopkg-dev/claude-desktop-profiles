package profiles

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"time"

	"claudeprofiles/desktopusage"
)

// Settings controls persisted online refresh preferences.
type Settings struct {
	CacheMinutes int  `json:"cacheMinutes"`
	AutoRefresh  bool `json:"autoRefresh"`
}

// DefaultSettings keeps network requests explicit across development restarts.
func DefaultSettings() Settings { return Settings{CacheMinutes: 5} }

func validSettings(value Settings) bool {
	return value.CacheMinutes == 1 || value.CacheMinutes == 5 || value.CacheMinutes == 15 || value.CacheMinutes == 60
}

// Settings reads this application's refresh preferences.
func (m *Manager) Settings() (Settings, error) {
	unlock, err := m.lock()
	if err != nil {
		return DefaultSettings(), err
	}
	defer unlock()
	return m.readSettings()
}

func (m *Manager) readSettings() (Settings, error) {
	value := DefaultSettings()
	err := readJSON(filepath.Join(m.paths.StoreDir, "settings.json"), &value)
	if errors.Is(err, os.ErrNotExist) {
		return value, nil
	}
	if err != nil || !validSettings(value) {
		return DefaultSettings(), ErrInvalidState
	}
	return value, nil
}

// SaveSettings persists refresh preferences for the next launch.
func (m *Manager) SaveSettings(value Settings) error {
	if !validSettings(value) {
		return ErrInvalidState
	}
	unlock, err := m.lock()
	if err != nil {
		return err
	}
	defer unlock()
	return writeJSON(filepath.Join(m.paths.StoreDir, "settings.json"), value)
}

type onlineEntry struct {
	AccountID   string          `json:"accountId"`
	Details     *AccountDetails `json:"details,omitempty"`
	Usage       *Usage          `json:"usage,omitempty"`
	LastAttempt time.Time       `json:"lastAttempt"`
	NextAllowed time.Time       `json:"nextAllowed"`
	Failures    int             `json:"failures"`
	LastError   string          `json:"lastError,omitempty"`
	// CredentialStamp fingerprints the credential a login failure was recorded against.
	CredentialStamp string `json:"credentialStamp,omitempty"`
}

// staleLoginFailure reports a login failure that a later Desktop sign-in has made obsolete,
// so its backoff no longer applies.
func staleLoginFailure(entry onlineEntry, dir string) bool {
	if entry.Failures == 0 || entry.CredentialStamp == "" {
		return false
	}
	stamp, err := credentialStamp(dir)
	return err == nil && stamp != "" && stamp != entry.CredentialStamp
}

func (e *onlineEntry) clearFailure() {
	e.Failures, e.LastError, e.CredentialStamp, e.NextAllowed = 0, "", "", time.Time{}
}

type onlineCache struct {
	Version int                    `json:"version"`
	Entries map[string]onlineEntry `json:"entries"`
}

func (m *Manager) onlineCachePath() string {
	return filepath.Join(m.paths.StoreDir, "online-cache.json")
}

func (m *Manager) readOnlineCache() (onlineCache, error) {
	cache := onlineCache{Version: 1, Entries: map[string]onlineEntry{}}
	err := readJSON(m.onlineCachePath(), &cache)
	if errors.Is(err, os.ErrNotExist) {
		return cache, nil
	}
	if err != nil || cache.Version != 1 || cache.Entries == nil || len(cache.Entries) > 1000 {
		return onlineCache{}, ErrInvalidState
	}
	for id, entry := range cache.Entries {
		if !validID(id) || entry.AccountID == "" || entry.Failures < 0 || entry.Failures > 10 ||
			entry.Usage != nil && (entry.Usage.Source != UsageSourceRemote ||
				!validUsageNumber(entry.Usage.FiveHour) || !validUsageNumber(entry.Usage.SevenDay)) {
			return onlineCache{}, ErrInvalidState
		}
	}
	return cache, nil
}

// onlineView is one read of the online cache and settings shared by every profile in a snapshot.
type onlineView struct {
	cache    onlineCache
	err      error
	settings Settings
}

func (m *Manager) loadOnlineView() onlineView {
	cache, err := m.readOnlineCache()
	settings, _ := m.readSettings()
	return onlineView{cache: cache, err: err, settings: settings}
}

// account returns the cached entry only while it still belongs to the verified login.
func (v onlineView) account(id, account string) (onlineEntry, error) {
	if v.err != nil {
		return onlineEntry{}, v.err
	}
	entry := v.cache.Entries[id]
	if account == "" || entry.AccountID != account {
		return onlineEntry{}, nil
	}
	return entry, nil
}

func (v onlineView) details(id, account string) (*AccountDetails, string) {
	entry, err := v.account(id, account)
	if err != nil {
		return nil, DetailsInvalid
	}
	if entry.Details == nil {
		return nil, DetailsMissing
	}
	details := *entry.Details
	return &details, DetailsAvailable
}

// ClearOnlineCache clears display data while preserving durable request cooldowns.
func (m *Manager) ClearOnlineCache() error {
	unlock, err := m.lock()
	if err != nil {
		return err
	}
	defer unlock()
	cache, err := m.readOnlineCache()
	if err != nil {
		return err
	}
	for id, entry := range cache.Entries {
		entry.Details, entry.Usage = nil, nil
		cache.Entries[id] = entry
	}
	return writeJSON(m.onlineCachePath(), cache)
}

// AccountQuerier returns public account information and quota from online endpoints.
type AccountQuerier interface {
	QueryAccount(context.Context, desktopusage.Target) (*desktopusage.AccountData, error)
}

// RefreshDue refreshes only expired online values, respecting persisted cooldowns.
func (m *Manager) RefreshDue(ctx context.Context, id string) error {
	querier := m.accountQuerier()
	if querier == nil {
		return ErrRemoteUsageUnavailable
	}
	return m.refreshOnlineAccount(ctx, id, querier, false)
}

func (m *Manager) refreshOnlineAccount(ctx context.Context, id string, querier AccountQuerier, force bool) error {
	if err := ctx.Err(); err != nil {
		return desktopusage.ErrCanceled
	}
	unlock, err := m.lock()
	if err != nil {
		return err
	}
	before, err := m.remoteUsageTarget(id)
	if err != nil {
		unlock()
		return err
	}
	cache, err := m.readOnlineCache()
	if err != nil {
		unlock()
		return err
	}
	entry := cache.Entries[id]
	if entry.AccountID != before.target.AccountID {
		entry = onlineEntry{AccountID: before.target.AccountID}
	}
	if staleLoginFailure(entry, before.target.Directory) {
		entry.clearFailure()
	}
	stamp, _ := credentialStamp(before.target.Directory)
	now := time.Now()
	if !force && entry.Usage != nil {
		settings, settingsErr := m.readSettings()
		if settingsErr != nil {
			unlock()
			return settingsErr
		}
		if now.Before(entry.Usage.UpdatedAt.Add(time.Duration(settings.CacheMinutes) * time.Minute)) {
			unlock()
			return nil
		}
	}
	if now.Before(entry.NextAllowed) {
		unlock()
		return ErrRemoteUsageCooldown
	}
	entry.LastAttempt, entry.NextAllowed = now, now.Add(remoteUsageCooldown)
	cache.Entries[id] = entry
	if err = writeJSON(m.onlineCachePath(), cache); err != nil {
		unlock()
		return err
	}
	unlock()
	data, queryErr := querier.QueryAccount(ctx, before.target)
	if ctx.Err() != nil {
		queryErr = desktopusage.ErrCanceled
		data = nil
	}
	unlock, err = m.lock()
	if err != nil {
		return err
	}
	defer unlock()
	after, err := m.remoteUsageTarget(id)
	if err != nil || !sameRemoteUsageTarget(before, after) {
		return ErrRemoteUsageChanged
	}
	cache, err = m.readOnlineCache()
	if err != nil {
		return err
	}
	entry = cache.Entries[id]
	if data != nil && data.AccountID == before.target.AccountID && validUsageOrganization(data.OrganizationID) {
		if entry.Details != nil && entry.Details.OrganizationID != data.OrganizationID {
			entry.Usage = nil
		}
		entry.Details = &AccountDetails{Email: data.Email, DisplayName: data.DisplayName, FullName: data.FullName,
			CreatedAt:      data.AccountCreatedAt,
			OrganizationID: data.OrganizationID, OrganizationName: data.OrganizationName, Plan: data.Plan,
			UpdatedAt: data.UpdatedAt, Source: UsageSourceRemote}
		if data.BillingType != "" || data.SubscriptionCreatedAt != nil ||
			data.BillingInterval != "" || data.SubscriptionStatus != "" || data.NextChargeDate != "" ||
			data.NextChargeAt != nil || data.PlanEndingAt != nil {
			entry.Details.Subscription = &SubscriptionDetails{BillingType: data.BillingType,
				CreatedAt: data.SubscriptionCreatedAt, BillingInterval: data.BillingInterval,
				Status: data.SubscriptionStatus, NextChargeDate: data.NextChargeDate,
				NextChargeAt: data.NextChargeAt, PlanEndingAt: data.PlanEndingAt, UpdatedAt: data.UpdatedAt}
		}
		if queryErr == nil {
			usage, valid := onlineUsage(data.Usage, time.Now())
			if !valid {
				queryErr = ErrRemoteUsageInvalid
			} else {
				entry.Usage = usage
			}
		}
	} else if queryErr == nil {
		queryErr = ErrRemoteUsageInvalid
	}
	if queryErr != nil {
		entry.Failures = min(entry.Failures+1, 10)
		entry.LastError = desktopusage.UserMessage(queryErr)
		if errors.Is(queryErr, ErrRemoteUsageInvalid) {
			entry.LastError = queryErr.Error()
		}
		entry.NextAllowed = time.Now().Add(time.Minute * time.Duration(1<<min(entry.Failures-1, 6)))
		if retry := desktopusage.RetryAt(queryErr); retry.After(entry.NextAllowed) {
			entry.NextAllowed = retry
		}
		entry.CredentialStamp = ""
		if desktopusage.LoginRejected(queryErr) {
			entry.CredentialStamp = stamp
		}
	} else {
		entry.Failures, entry.LastError, entry.CredentialStamp = 0, "", ""
	}
	cache.Entries[id] = entry
	if err := writeJSON(m.onlineCachePath(), cache); err != nil {
		return err
	}
	return queryErr
}
