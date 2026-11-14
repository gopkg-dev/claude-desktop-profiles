package workspaces

import (
	"os"
	"path/filepath"
	"testing"
)

func TestListMergesSourcesWithoutWriting(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	const sessionC = "local_77777777-7777-4777-8777-777777777777"
	const cliC = "88888888-8888-4888-8888-888888888888"
	writeRecord(t, f.source, codeTree, sessionA, cliA, 1_700_000_000_000, map[string]any{"cwd": f.project, "title": "old"})
	writeRecord(t, f.target, codeTree, sessionA, cliA, 1_700_000_100_000, map[string]any{"cwd": f.project, "title": "new"})
	writeTestFile(t, filepath.Join(f.source.TranscriptRoot, "slug", cliA+".jsonl"), "transcript")
	writeRecord(t, f.source, codeTree, sessionB, cliB, 1_700_000_200_000, map[string]any{"cwd": f.project})
	writeRecord(t, f.source, codeTree, sessionC, cliC, 1_700_000_300_000, map[string]any{"cwd": f.project})
	writeTestFile(t, filepath.Join(f.source.TranscriptRoot, "slug", cliC+".jsonl"), "transcript")
	writeTestFile(t, filepath.Join(f.target.Dir, codeTree, accountB, orgB, "deleted_"+sessionC[len("local_"):]), "")
	before, _ := os.ReadDir(filepath.Join(f.target.Dir, codeTree, accountB, orgB))

	sessions, err := List([]Source{f.source, f.target})
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 2 {
		t.Fatalf("sessions = %+v", sessions)
	}
	if sessions[0].CLISessionID != cliB || sessions[0].Issue != IssueTranscriptMissing ||
		!sessions[0].Present[0] || sessions[0].Present[1] {
		t.Fatalf("missing transcript session = %+v", sessions[0])
	}
	if sessions[1].CLISessionID != cliA || sessions[1].Title != "new" || sessions[1].Issue != "" ||
		!sessions[1].Present[0] || !sessions[1].Present[1] || sessions[1].LastActivityAt.UnixMilli() != 1_700_000_100_000 {
		t.Fatalf("shared session = %+v", sessions[1])
	}
	after, _ := os.ReadDir(filepath.Join(f.target.Dir, codeTree, accountB, orgB))
	if len(after) != len(before) {
		t.Fatal("listing changed the target directory")
	}
}

func TestListReportsUnsupportedWorkspaces(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.source.LiveDir = f.source.Dir
	managed := filepath.Join(f.source.Dir, "git-worktrees", "one")
	writeRecord(t, f.source, codeTree, sessionA, cliA, 2, map[string]any{"cwd": managed})
	writeRecord(t, f.source, codeTree, sessionB, cliB, 1, map[string]any{"cwd": filepath.Join(f.root, "gone")})
	for _, cli := range []string{cliA, cliB} {
		writeTestFile(t, filepath.Join(f.source.TranscriptRoot, "slug", cli+".jsonl"), "transcript")
	}
	sessions, err := List([]Source{f.source})
	if err != nil || len(sessions) != 2 {
		t.Fatalf("sessions = %+v, %v", sessions, err)
	}
	if sessions[0].Issue != IssueManagedWorkspace || sessions[1].Issue != IssueWorkspaceMissing {
		t.Fatalf("issues = %q, %q", sessions[0].Issue, sessions[1].Issue)
	}
}
