package profiles

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"claudeprofiles/workspaces"
)

func sharingFixture(t *testing.T) (*Manager, *fakeDriver, string, string) {
	t.Helper()
	m, d, a, b := twoProfiles(t)
	m.paths.TranscriptRoot = filepath.Join(t.TempDir(), "projects")
	if err := os.Mkdir(m.paths.TranscriptRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	m.detailsReader = func(_ string, expected string, _ time.Time) (*AccountDetails, string) {
		if expected == "" {
			return nil, DetailsMissing
		}
		return &AccountDetails{OrganizationID: "org-" + strings.TrimPrefix(expected, "account-")}, DetailsAvailable
	}
	return m, d, a, b
}

func TestSyncSessionsStopsBeforeSyncAndPersistsSharing(t *testing.T) {
	t.Parallel()
	m, d, a, b := sharingFixture(t)
	stops, starts := d.stops, d.starts
	called := false
	m.workspaceSync = func(
		sources []workspaces.Source, target workspaces.Source, backup string,
	) (workspaces.Report, error) {
		called = true
		if d.stops != stops+1 || d.starts != starts {
			t.Fatal("sync ran before Desktop stopped or after it restarted")
		}
		if len(sources) != 1 || sources[0].Dir != m.profileDir(a) || target.Dir != m.paths.LiveDir ||
			sources[0].Identity.AccountID != "account-a" || target.Identity.OrganizationID != "org-b" {
			t.Fatalf("wrong source/target: %+v %+v", sources, target)
		}
		if target.TranscriptRoot != m.paths.TranscriptRoot || target.LiveDir != m.paths.LiveDir {
			t.Fatal("canonical paths not passed through")
		}
		if !strings.HasPrefix(backup, filepath.Join(m.paths.StoreDir, "backups", "workspaces")+string(os.PathSeparator)) {
			t.Fatal("backup escaped app store")
		}
		if readText(t, filepath.Join(m.paths.LiveDir, "Cookies")) != "B-COOKIE" {
			t.Fatal("sync changed active login")
		}
		return workspaces.Report{CodeCopied: 2, CodeUpdated: 3}, nil
	}
	report, err := m.SyncSessions(context.Background())
	if err != nil || !called || report.CodeCopied != 2 || report.CodeUpdated != 3 || d.starts != starts+1 {
		t.Fatalf("report=%+v err=%v driver=%+v", report, err, d)
	}
	reloaded := New(m.paths, &fakeDriver{})
	s, err := reloaded.Snapshot()
	if err != nil || !s.SharedSessions || s.ActiveID != b {
		t.Fatalf("snapshot=%+v err=%v", s, err)
	}
}

func TestSyncSessionsStopFailurePreservesState(t *testing.T) {
	t.Parallel()
	m, d, _, _ := sharingFixture(t)
	d.stopError = errors.New("still running")
	m.workspaceSync = func([]workspaces.Source, workspaces.Source, string) (workspaces.Report, error) {
		t.Fatal("sync ran despite failed stop")
		return workspaces.Report{}, nil
	}
	before := readText(t, m.statePath())
	starts := d.starts
	_, err := m.SyncSessions(context.Background())
	if !errors.Is(err, d.stopError) || readText(t, m.statePath()) != before || d.starts != starts {
		t.Fatalf("err=%v driver=%+v", err, d)
	}
}

func TestSyncSessionsFailureDoesNotEnableSharingAndRestartsSafely(t *testing.T) {
	t.Parallel()
	for _, rollbackFailure := range []bool{false, true} {
		t.Run(map[bool]string{false: "safe-failure", true: "rollback-failure"}[rollbackFailure], func(t *testing.T) {
			m, d, _, b := sharingFixture(t)
			cause := errors.New("sync failed")
			if rollbackFailure {
				cause = workspaces.ErrRollback
			}
			m.workspaceSync = func([]workspaces.Source, workspaces.Source, string) (workspaces.Report, error) {
				return workspaces.Report{Skipped: 1, Reasons: map[string]int{"unsupported": 1}}, cause
			}
			starts := d.starts
			report, err := m.SyncSessions(context.Background())
			if !errors.Is(err, cause) || report.Skipped != 1 {
				t.Fatalf("report=%+v err=%v", report, err)
			}
			s, snapshotErr := m.Snapshot()
			if snapshotErr != nil || s.SharedSessions || s.ActiveID != b || s.PendingRecovery != rollbackFailure {
				t.Fatalf("snapshot=%+v err=%v", s, snapshotErr)
			}
			wantStarts := starts + 1
			if rollbackFailure {
				wantStarts = starts
			}
			if d.starts != wantStarts {
				t.Fatalf("starts=%d want=%d", d.starts, wantStarts)
			}
			if rollbackFailure {
				reloaded := New(m.paths, d)
				if err := reloaded.Switch(context.Background(), b); !errors.Is(err, ErrRecoveryRequired) {
					t.Fatalf("damaged active profile could reopen: %v", err)
				}
				if err := reloaded.Recover(context.Background()); !errors.Is(err, ErrWorkspaceRecovery) {
					t.Fatalf("manual repair requirement lost: %v", err)
				}
				s, snapshotErr = reloaded.Snapshot()
				if snapshotErr != nil || !s.PendingRecovery || s.RecoveryMessage == "" || d.starts != starts {
					t.Fatalf("recovery cleared blocker: snapshot=%+v err=%v", s, snapshotErr)
				}
			}
		})
	}
}

func TestPreSwitchRollbackFailureBlocksTargetButReopensOriginal(t *testing.T) {
	t.Parallel()
	m, d, a, b := sharingFixture(t)
	m.workspaceSync = func([]workspaces.Source, workspaces.Source, string) (workspaces.Report, error) {
		return workspaces.Report{}, nil
	}
	if _, err := m.SyncSessions(context.Background()); err != nil {
		t.Fatal(err)
	}
	m.workspaceSync = func([]workspaces.Source, workspaces.Source, string) (workspaces.Report, error) {
		return workspaces.Report{}, workspaces.ErrRollback
	}
	starts := d.starts
	if err := m.Switch(context.Background(), a); !errors.Is(err, workspaces.ErrRollback) {
		t.Fatalf("err=%v", err)
	}
	s, err := m.Snapshot()
	if err != nil || !s.PendingRecovery || s.ActiveID != b || d.starts != starts+1 ||
		readText(t, filepath.Join(m.paths.LiveDir, "Cookies")) != "B-COOKIE" {
		t.Fatalf("snapshot=%+v err=%v driver=%+v", s, err, d)
	}
	reloaded := New(m.paths, d)
	if err := reloaded.Switch(context.Background(), a); !errors.Is(err, ErrRecoveryRequired) {
		t.Fatalf("unrecovered target could be activated: %v", err)
	}
	stops := d.stops
	if err := reloaded.Recover(context.Background()); !errors.Is(err, ErrWorkspaceRecovery) {
		t.Fatalf("manual repair requirement lost: %v", err)
	}
	if d.stops != stops || d.starts != starts+1 {
		t.Fatal("parked profile recovery unnecessarily touched healthy active Desktop")
	}
}

func TestSwitchSharesBeforeDirectorySwapAndFailurePreservesLogin(t *testing.T) {
	t.Parallel()
	m, d, a, b := sharingFixture(t)
	m.workspaceSync = func([]workspaces.Source, workspaces.Source, string) (workspaces.Report, error) {
		return workspaces.Report{}, nil
	}
	if _, err := m.SyncSessions(context.Background()); err != nil {
		t.Fatal(err)
	}
	cause := errors.New("target sync failed")
	called := 0
	m.workspaceSync = func(sources []workspaces.Source, target workspaces.Source, _ string) (workspaces.Report, error) {
		called++
		if target.Dir != m.profileDir(a) || len(sources) != 1 || sources[0].Dir != m.paths.LiveDir {
			t.Fatalf("wrong pre-switch source/target: %+v %+v", sources, target)
		}
		s, err := m.load()
		if err != nil || s.ActiveID != b || readText(t, filepath.Join(m.paths.LiveDir, "Cookies")) != "B-COOKIE" {
			t.Fatal("directory swap happened before sharing completed")
		}
		return workspaces.Report{}, cause
	}
	starts := d.starts
	if err := m.Switch(context.Background(), a); !errors.Is(err, cause) {
		t.Fatalf("err=%v", err)
	}
	s, err := m.Snapshot()
	if err != nil || s.ActiveID != b || !s.SharedSessions || s.PendingRecovery || d.starts != starts+1 {
		t.Fatalf("snapshot=%+v err=%v driver=%+v", s, err, d)
	}
	cause = nil
	if err := m.Switch(context.Background(), a); err != nil {
		t.Fatal(err)
	}
	if called != 2 || readText(t, filepath.Join(m.paths.LiveDir, "Cookies")) != "A-COOKIE" {
		t.Fatal("successful shared switch did not activate selected login")
	}
}

func TestAddingBlankProfileSkipsSharingAndCanReturnWithoutLogin(t *testing.T) {
	t.Parallel()
	m, _, _, b := sharingFixture(t)
	called := 0
	m.workspaceSync = func([]workspaces.Source, workspaces.Source, string) (workspaces.Report, error) {
		called++
		return workspaces.Report{}, nil
	}
	if _, err := m.SyncSessions(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := m.Add(context.Background(), "尚未登录"); err != nil {
		t.Fatal(err)
	}
	if called != 1 {
		t.Fatal("new blank profile tried to share unknown account data")
	}
	s, err := m.Snapshot()
	if err != nil || s.ActiveID == b || !s.SharedSessions || s.CurrentAccount != "" {
		t.Fatalf("snapshot=%+v err=%v", s, err)
	}
	if err := m.Switch(context.Background(), b); err != nil {
		t.Fatal(err)
	}
	if called != 1 || readText(t, filepath.Join(m.paths.LiveDir, "Cookies")) != "B-COOKIE" {
		t.Fatal("could not return from a canceled first login")
	}
}

func TestSyncSessionsRequiresVerifiedTargetOrganization(t *testing.T) {
	t.Parallel()
	m, d, _, _ := sharingFixture(t)
	m.detailsReader = func(string, string, time.Time) (*AccountDetails, string) { return nil, DetailsMissing }
	m.workspaceSync = func([]workspaces.Source, workspaces.Source, string) (workspaces.Report, error) {
		t.Fatal("sync guessed an unknown target organization")
		return workspaces.Report{}, nil
	}
	starts := d.starts
	if _, err := m.SyncSessions(context.Background()); !errors.Is(err, ErrSessionOrganization) {
		t.Fatalf("err=%v", err)
	}
	s, err := m.Snapshot()
	if err != nil || s.SharedSessions || d.starts != starts+1 {
		t.Fatalf("snapshot=%+v err=%v driver=%+v", s, err, d)
	}
}

func TestSyncSessionsSkipsUnknownSourcesAndMergesReasonCounts(t *testing.T) {
	t.Parallel()
	m, _, _, b := sharingFixture(t)
	if err := m.Add(context.Background(), "C"); err != nil {
		t.Fatal(err)
	}
	putLogin(t, m.paths.LiveDir, "account-c")
	if _, err := m.Snapshot(); err != nil {
		t.Fatal(err)
	}
	m.detailsReader = func(_ string, expected string, _ time.Time) (*AccountDetails, string) {
		if expected == "account-a" {
			return nil, DetailsUnavailable
		}
		return &AccountDetails{OrganizationID: "org-" + strings.TrimPrefix(expected, "account-")}, DetailsAvailable
	}
	m.workspaceSync = func(sources []workspaces.Source, target workspaces.Source, _ string) (workspaces.Report, error) {
		if len(sources) != 1 || sources[0].Dir != m.profileDir(b) || target.Identity.AccountID != "account-c" {
			t.Fatalf("wrong sources=%+v target=%+v", sources, target)
		}
		return workspaces.Report{CodeCopied: 1, Skipped: 2, Reasons: map[string]int{"code_transcript_missing": 2}}, nil
	}
	report, err := m.SyncSessions(context.Background())
	if err != nil || report.CodeCopied != 1 || report.Skipped != 3 ||
		report.Reasons["source_details_unavailable"] != 1 || report.Reasons["code_transcript_missing"] != 2 {
		t.Fatalf("report=%+v err=%v", report, err)
	}
}

func TestSyncSessionsWithNoVerifiedSourcesDoesNotEnableSharing(t *testing.T) {
	t.Parallel()
	m, _, _, _ := sharingFixture(t)
	m.detailsReader = func(_ string, expected string, _ time.Time) (*AccountDetails, string) {
		if expected == "account-b" {
			return &AccountDetails{OrganizationID: "org-b"}, DetailsAvailable
		}
		return nil, DetailsUnavailable
	}
	report, err := m.SyncSessions(context.Background())
	if !errors.Is(err, ErrNoSessionSources) || report.Skipped != 1 {
		t.Fatalf("report=%+v err=%v", report, err)
	}
	s, err := m.Snapshot()
	if err != nil || s.SharedSessions {
		t.Fatalf("snapshot=%+v err=%v", s, err)
	}
}

func TestLegacyManifestDefaultsToNoSessionSharing(t *testing.T) {
	t.Parallel()
	m, _, _, _ := twoProfiles(t)
	var state map[string]any
	if err := json.Unmarshal([]byte(readText(t, m.statePath())), &state); err != nil {
		t.Fatal(err)
	}
	delete(state, "sharedSessions")
	if err := writeJSON(m.statePath(), state); err != nil {
		t.Fatal(err)
	}
	s, err := m.Snapshot()
	if err != nil || s.SharedSessions {
		t.Fatalf("legacy snapshot=%+v err=%v", s, err)
	}
}

func TestDisableSharingAllowsOpeningAccountWithMissingMetadata(t *testing.T) {
	t.Parallel()
	m, d, a, _ := sharingFixture(t)
	m.workspaceSync = func([]workspaces.Source, workspaces.Source, string) (workspaces.Report, error) {
		return workspaces.Report{}, nil
	}
	if _, err := m.SyncSessions(context.Background()); err != nil {
		t.Fatal(err)
	}
	m.detailsReader = func(string, string, time.Time) (*AccountDetails, string) { return nil, DetailsMissing }
	stops, starts := d.stops, d.starts
	if err := m.DisableSessionSharing(); err != nil {
		t.Fatal(err)
	}
	if d.stops != stops || d.starts != starts {
		t.Fatal("changing sharing preference touched Desktop")
	}
	if err := m.Switch(context.Background(), a); err != nil {
		t.Fatal(err)
	}
	s, err := m.Snapshot()
	if err != nil || s.SharedSessions || s.ActiveID != a {
		t.Fatalf("snapshot=%+v err=%v", s, err)
	}
}

func TestSyncSessionsImportsRealCodeIndexWithoutChangingLogin(t *testing.T) {
	t.Parallel()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	d := &fakeDriver{}
	m := New(Paths{
		LiveDir: filepath.Join(root, "desktop"), StoreDir: filepath.Join(root, "state"),
		TranscriptRoot: filepath.Join(root, "projects"),
	}, d)
	accountA, accountB := "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa", "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb"
	orgA, orgB := "11111111-1111-1111-1111-111111111111", "22222222-2222-2222-2222-222222222222"
	cliID := "33333333-3333-3333-3333-333333333333"
	sessionID := "local_" + cliID
	putLogin(t, m.paths.LiveDir, accountA)
	if err := m.Adopt("A"); err != nil {
		t.Fatal(err)
	}
	s, err := m.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	a := s.ActiveID
	if err := m.Add(context.Background(), "B"); err != nil {
		t.Fatal(err)
	}
	putLogin(t, m.paths.LiveDir, accountB)
	if _, err := m.Snapshot(); err != nil {
		t.Fatal(err)
	}
	m.detailsReader = func(_ string, expected string, _ time.Time) (*AccountDetails, string) {
		org := orgA
		if expected == accountB {
			org = orgB
		}
		return &AccountDetails{OrganizationID: org}, DetailsAvailable
	}
	project := filepath.Join(m.paths.TranscriptRoot, "test-project")
	if err := os.MkdirAll(project, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, cliID+".jsonl"), []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	indexDir := filepath.Join(m.profileDir(a), "claude-code-sessions", accountA, orgA)
	if err := os.MkdirAll(indexDir, 0o700); err != nil {
		t.Fatal(err)
	}
	row := map[string]any{
		"sessionId": sessionID, "cliSessionId": cliID, "cwd": root,
		"title": "本地会话", "createdAt": 1, "lastActivityAt": 2,
	}
	sourceIndex := filepath.Join(indexDir, sessionID+".json")
	if err := writeJSON(sourceIndex, row); err != nil {
		t.Fatal(err)
	}
	beforeLogin := readText(t, filepath.Join(m.paths.LiveDir, "config.json"))
	beforeSource := readText(t, sourceIndex)
	report, err := m.SyncSessions(context.Background())
	if err != nil || report.CodeCopied != 1 {
		t.Fatalf("report=%+v err=%v", report, err)
	}
	targetIndex := filepath.Join(m.paths.LiveDir, "claude-code-sessions", accountB, orgB, sessionID+".json")
	if !strings.Contains(readText(t, targetIndex), cliID) {
		t.Fatal("imported index does not point to existing local transcript")
	}
	if readText(t, sourceIndex) != beforeSource ||
		readText(t, filepath.Join(m.paths.LiveDir, "config.json")) != beforeLogin {
		t.Fatal("import changed source or target login")
	}
}

func TestResumeSharingSkipsMergeOnlyWhileTheSameAccountStayedActive(t *testing.T) {
	t.Parallel()
	m, d, a, b := sharingFixture(t)
	merges := 0
	m.workspaceSync = func([]workspaces.Source, workspaces.Source, string) (workspaces.Report, error) {
		merges++
		return workspaces.Report{}, nil
	}
	if resumed, err := m.ResumeSessionSharing(); err != nil || resumed {
		t.Fatalf("sharing that was never on resumed without a merge: %v %v", resumed, err)
	}
	if _, err := m.SyncSessions(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := m.DisableSessionSharing(); err != nil {
		t.Fatal(err)
	}
	stops, starts, before := d.stops, d.starts, merges
	resumed, err := m.ResumeSessionSharing()
	if err != nil || !resumed || d.stops != stops || d.starts != starts || merges != before {
		t.Fatalf("resume on the same account touched Desktop or merged: %v %v %+v", resumed, err, d)
	}
	if s, err := m.Snapshot(); err != nil || !s.SharedSessions || s.ActiveID != b {
		t.Fatalf("snapshot=%+v err=%v", s, err)
	}
	if resumed, err := m.ResumeSessionSharing(); err != nil || !resumed {
		t.Fatal("resuming sharing that is already on failed", err)
	}

	// A switch while paused lets the other account run, so turning sharing on again needs a merge.
	if err := m.DisableSessionSharing(); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{a, b} {
		if err := m.Switch(context.Background(), id); err != nil {
			t.Fatal(err)
		}
	}
	if resumed, err := m.ResumeSessionSharing(); err != nil || resumed {
		t.Fatalf("resumed without a merge after switching back to the paused account: %v %v", resumed, err)
	}
	if s, err := m.Snapshot(); err != nil || s.SharedSessions {
		t.Fatalf("a refused resume changed sharing: %+v %v", s, err)
	}
	if _, err := m.SyncSessions(context.Background()); err != nil {
		t.Fatal(err)
	}
	if s, err := m.Snapshot(); err != nil || !s.SharedSessions {
		t.Fatalf("sync did not turn sharing on: %+v %v", s, err)
	}
}

func TestSwitchReportsEachStepAsItBegins(t *testing.T) {
	t.Parallel()
	m, _, a, _ := sharingFixture(t)
	m.workspaceSync = func([]workspaces.Source, workspaces.Source, string) (workspaces.Report, error) {
		return workspaces.Report{}, nil
	}
	steps := []Step{}
	ctx := WithProgress(context.Background(), func(step Step) { steps = append(steps, step) })
	if _, err := m.SyncSessions(ctx); err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(steps) != fmt.Sprint([]Step{StepQuit, StepSync, StepLaunch}) {
		t.Fatalf("sync steps %v", steps)
	}
	steps = nil
	if err := m.Switch(ctx, a); err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(steps) != fmt.Sprint([]Step{StepQuit, StepSync, StepMove, StepLaunch}) {
		t.Fatalf("switch steps %v", steps)
	}
}
