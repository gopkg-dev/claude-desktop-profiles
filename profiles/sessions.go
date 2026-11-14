package profiles

import (
	"errors"
	"os"
	"path/filepath"
	"time"

	"claudeprofiles/httplog"
	"claudeprofiles/workspaces"
)

// SessionAccount is one signed-in account whose session list was read.
type SessionAccount struct {
	ProfileID string
	Email     string
}

// SyncRecord is the outcome of the last completed session sync.
type SyncRecord struct {
	At     time.Time         `json:"at"`
	Report workspaces.Report `json:"report"`
}

// SessionList shows the local Code sessions of every verified account; Present follows Accounts.
type SessionList struct {
	Accounts []SessionAccount
	Sessions []workspaces.Session
	LastSync *SyncRecord
}

func (m *Manager) syncRecordPath() string { return filepath.Join(m.paths.StoreDir, "last-sync.json") }

// Sessions lists the local Code sessions without stopping Desktop or changing any session file.
func (m *Manager) Sessions() (SessionList, error) {
	unlock, err := m.lock()
	if err != nil {
		return SessionList{}, err
	}
	defer unlock()
	s, err := m.ready()
	if err != nil {
		return SessionList{}, err
	}
	list := SessionList{}
	var record SyncRecord
	if err := readJSON(m.syncRecordPath(), &record); err == nil && !record.At.IsZero() {
		list.LastSync = &record
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return list, err
	}
	if !filepath.IsAbs(m.paths.TranscriptRoot) {
		return list, errors.New("尚未配置本地 Code 对话目录")
	}
	view := m.loadOnlineView()
	var sources []workspaces.Source
	for _, p := range s.Profiles {
		source, reason, err := m.workspaceSource(&s, p.ID)
		if err != nil || reason != "" {
			continue
		}
		email := ""
		if details, status := m.profileDetails(view, p.ID, source.Dir, source.Identity.AccountID, time.Now()); status == DetailsAvailable {
			email = details.Email
		}
		list.Accounts = append(list.Accounts, SessionAccount{ProfileID: p.ID, Email: email})
		sources = append(sources, source)
	}
	list.Sessions, err = workspaces.List(sources)
	return list, err
}

func (m *Manager) saveSyncRecord(report workspaces.Report) error {
	return writeJSON(m.syncRecordPath(), SyncRecord{At: time.Now().UTC(), Report: report})
}

// RequestLogDirectory is where online queries keep their raw HTTP exchanges.
func (m *Manager) RequestLogDirectory() string { return m.paths.RequestLogDir }

// RequestLog summarizes the raw HTTP log without reading its contents.
func (m *Manager) RequestLog() (httplog.Summary, error) {
	if m.paths.RequestLogDir == "" {
		return httplog.Summary{}, errors.New("未配置请求日志目录")
	}
	return httplog.Summarize(m.paths.RequestLogDir)
}

// ClearRequestLog removes every stored HTTP exchange.
func (m *Manager) ClearRequestLog() error {
	if m.paths.RequestLogDir == "" {
		return errors.New("未配置请求日志目录")
	}
	return httplog.Clear(m.paths.RequestLogDir)
}
