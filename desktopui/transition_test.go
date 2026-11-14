package desktopui

import (
	"context"
	"errors"
	"testing"
	"time"

	"claudeprofiles/profiles"
)

// steppedStore reports a switch's steps as the real store does, holding at quitting until released.
type steppedStore struct {
	*pageStore
	release chan struct{}
	fail    error
}

func (s *steppedStore) Switch(ctx context.Context, id string) error {
	profiles.ReportStep(ctx, profiles.StepQuit)
	select {
	case <-s.release:
	case <-ctx.Done():
		return ctx.Err()
	}
	if s.fail != nil {
		return s.fail
	}
	profiles.ReportStep(ctx, profiles.StepMove)
	profiles.ReportStep(ctx, profiles.StepLaunch)
	return s.pageStore.Switch(ctx, id)
}

func TestSwitchShowsEachStepThenClosesItself(t *testing.T) {
	store := &steppedStore{pageStore: pageFixture(), release: make(chan struct{})}
	application := startPages(t, store, false)
	application.Press("id:switch-work")
	application.Press("id:confirm")
	application.WaitUntil(3*time.Second, func() bool { return application.Exists("id:transition-panel") })
	if got := application.Find("id:transition-title").Text; got != "正在切换到 work@example.com" {
		t.Fatalf("title %q", got)
	}
	application.Find("id:transition-step-quit >> text:等待它保存数据，可能需要十几秒")
	for _, step := range []string{"sync", "move", "launch"} {
		application.Find("id:transition-step-" + step)
	}
	application.Screenshot("design-switch-progress")

	close(store.release)
	application.WaitUntil(3*time.Second, func() bool {
		return application.Exists("id:transition-title") && application.Find("id:transition-title").Text == "已切换到 work@example.com"
	})
	if application.Exists("id:transition-detail") {
		t.Fatal("a finished switch should not describe a running step")
	}
	application.Screenshot("design-switch-done")
	application.Advance(transitionDone + 100*time.Millisecond)
	application.WaitUntil(3*time.Second, func() bool { return !application.Exists("id:transition-panel") })
	application.WaitUntil(3*time.Second, func() bool { return application.Exists("id:open-work") })
}

func TestFailedSwitchStaysOpenWithItsReason(t *testing.T) {
	store := &steppedStore{pageStore: pageFixture(), release: make(chan struct{}), fail: errors.New("Claude Desktop 没有退出")}
	close(store.release)
	application := startPages(t, store, false)
	application.Press("id:switch-work")
	application.Press("id:confirm")
	application.WaitUntil(3*time.Second, func() bool { return application.Exists("id:transition-error") })
	if application.Find("id:transition-title").Text != "切换未完成" ||
		application.Find("id:transition-error").Text != "Claude Desktop 没有退出" {
		t.Fatal("failure not explained")
	}
	application.Advance(transitionDone * 3)
	application.Press("id:transition-close")
	if application.Exists("id:transition-panel") {
		t.Fatal("dismiss did not close the panel")
	}
}
