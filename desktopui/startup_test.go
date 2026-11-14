package desktopui

import (
	"errors"
	"testing"
	"time"

	"claudeprofiles/profiles"
)

type startupTestStore struct {
	*refreshTestStore
	preview    profiles.Snapshot
	previewErr error
}

func (s *startupTestStore) PreviewSnapshot() (profiles.Snapshot, error) {
	return s.preview, s.previewErr
}

func TestStartupShowsVerifiedDetailsBeforeSlowUsageRead(t *testing.T) {
	store := &startupTestStore{
		refreshTestStore: &refreshTestStore{
			started: make(chan struct{}, 1), release: make(chan struct{}),
			full: profiles.Snapshot{ActiveID: "second", Profiles: []profiles.Profile{{ID: "second",
				Details: &profiles.AccountDetails{Email: "current@example.com"},
				Usage:   &profiles.Usage{FiveHour: floatPointer(26)},
			}}},
		},
		preview: profiles.Snapshot{ActiveID: "first", Profiles: []profiles.Profile{{ID: "first",
			Details: &profiles.AccountDetails{Email: "preview@example.com", Plan: "Pro"},
		}}},
	}
	a, posted := refreshFixture(store, profiles.Snapshot{})
	a.snapshot.Set(nil)
	a.refresh()
	runPosted(t, posted)
	select {
	case <-store.started:
	case <-time.After(3 * time.Second):
		t.Fatal("完整读取未开始")
	}
	preview := a.snapshot.Get()
	if preview == nil || !preview.Preview || preview.Profiles[0].Details.Email != "preview@example.com" {
		t.Fatal("慢速额度读取结束前未显示账号资料")
	}
	if preview.Profiles[0].Usage != nil || !a.controlsDisabled() {
		t.Fatal("预览不应展示虚构额度或允许操作")
	}
	close(store.release)
	runPosted(t, posted)
	complete := a.snapshot.Get()
	if complete.Preview || complete.ActiveID != "second" || a.controlsDisabled() {
		t.Fatal("完整快照未替换预览或未恢复操作")
	}
	if got := complete.Profiles[0].Usage.FiveHour; got == nil || *got != 26 {
		t.Fatal("完整读取的额度未显示")
	}
}

func TestStartupReadFailureLeavesPreviewBlockedAndRetryable(t *testing.T) {
	store := &startupTestStore{
		refreshTestStore: &refreshTestStore{fullErr: errors.New("读取失败")},
		preview:          profiles.Snapshot{ActiveID: "first"},
	}
	a, posted := refreshFixture(store, profiles.Snapshot{})
	a.snapshot.Set(nil)
	a.refresh()
	runPosted(t, posted)
	runPosted(t, posted)
	if a.busy.Get() || !a.controlsDisabled() || !a.snapshot.Get().Preview || a.failure.Get() == "" {
		t.Fatal("读取失败应保留预览、禁用账号动作并允许再次刷新")
	}
	a.prepare("add", profiles.Profile{})
	a.refreshUsage()
	if a.pending.Get() != nil || a.usageRefreshing {
		t.Fatal("预览不应启动账号操作或常规额度刷新")
	}
	store.mu.Lock()
	store.fullErr = nil
	store.full = profiles.Snapshot{ActiveID: "first"}
	store.mu.Unlock()
	a.refresh()
	runPosted(t, posted)
	runPosted(t, posted)
	if a.controlsDisabled() || a.snapshot.Get().Preview || a.failure.Get() != "" {
		t.Fatal("重试成功后应解除预览及错误状态")
	}
}

func TestStartupPreviewFailureFallsBackToFullRead(t *testing.T) {
	store := &startupTestStore{
		refreshTestStore: &refreshTestStore{full: profiles.Snapshot{ActiveID: "first"}},
		previewErr:       errors.New("预览不可用"),
	}
	a, posted := refreshFixture(store, profiles.Snapshot{})
	a.snapshot.Set(nil)
	a.refresh()
	runPosted(t, posted)
	if a.snapshot.Get() == nil || a.snapshot.Get().Preview || a.controlsDisabled() || a.failure.Get() != "" {
		t.Fatal("预览失败不应中止完整读取")
	}
}
