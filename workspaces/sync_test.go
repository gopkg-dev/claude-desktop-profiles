package workspaces

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const (
	accountA = "11111111-1111-4111-8111-111111111111"
	accountB = "22222222-2222-4222-8222-222222222222"
	orgA     = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	orgB     = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
	sessionA = "local_33333333-3333-4333-8333-333333333333"
	sessionB = "local_44444444-4444-4444-8444-444444444444"
	cliA     = "55555555-5555-4555-8555-555555555555"
	cliB     = "66666666-6666-4666-8666-666666666666"
)

type fixture struct {
	root    string
	source  Source
	target  Source
	backup  string
	project string
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	f := fixture{
		root:    root,
		source:  Source{Dir: filepath.Join(root, "source"), Identity: Identity{accountA, orgA}},
		target:  Source{Dir: filepath.Join(root, "target"), Identity: Identity{accountB, orgB}},
		backup:  filepath.Join(root, "backups"),
		project: filepath.Join(root, "work"),
	}
	for _, dir := range []string{f.source.Dir, f.target.Dir, f.project} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	f.source.TranscriptRoot = filepath.Join(root, "projects")
	f.target.TranscriptRoot = f.source.TranscriptRoot
	return f
}

func writeTestFile(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
}

func writeRecord(t *testing.T, source Source, kind, id, cli string, at int64, extra map[string]any) string {
	t.Helper()
	row := map[string]any{
		"sessionId": id, "cliSessionId": cli, "title": "fixture title", "createdAt": int64(1),
		"lastActivityAt": at, "cwd": filepath.Dir(source.Dir),
	}
	for key, value := range extra {
		row[key] = value
	}
	data, err := json.Marshal(row)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(source.Dir, kind, source.Identity.AccountID, source.Identity.OrganizationID, id+".json")
	writeTestFile(t, path, string(data))
	return path
}

func readRecord(t *testing.T, path string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var row map[string]any
	if err := json.Unmarshal(data, &row); err != nil {
		t.Fatal(err)
	}
	return row
}

func expectedIndex(source Source, kind, id string) string {
	return filepath.Join(source.Dir, kind, source.Identity.AccountID, source.Identity.OrganizationID, id+".json")
}

func TestCodeImportPreservesLocalMetadataWithoutAccountBindings(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	original := writeRecord(t, f.source, codeTree, sessionA, cliA, 10, map[string]any{
		"cwd": f.project, "model": "fixture-model", "effort": "high", "lastFocusedAt": 9,
		"remoteMcpServersConfig": map[string]any{"token": "SECRET"}, "outboundCCRRemoteId": "SECRET",
		"permissionMode": "bypassPermissions", "envScopeId": "SECRET", "bridgeSessionIds": []string{"SECRET"},
	})
	before, _ := os.ReadFile(original)
	writeTestFile(t, filepath.Join(f.source.TranscriptRoot, "project-slug", cliA+".jsonl"), "fixture transcript")
	report, err := Sync([]Source{f.source}, f.target, f.backup)
	if err != nil || report.CodeCopied != 1 || report.Skipped != 0 {
		t.Fatalf("report=%+v err=%v", report, err)
	}
	result := expectedIndex(f.target, codeTree, sessionA)
	row := readRecord(t, result)
	if row["cliSessionId"] != cliA || row["cwd"] != f.project || row["model"] != "fixture-model" ||
		row["lastFocusedAt"] != float64(9) || row["importedFrom"] != "local-1p-code" {
		t.Fatalf("local metadata mismatch: keys=%v", reflectKeys(row))
	}
	data, _ := os.ReadFile(result)
	if strings.Contains(string(data), "SECRET") {
		t.Fatal("account bindings copied")
	}
	if row["permissionMode"] != "bypassPermissions" || row["resumeConfirmed"] != true {
		t.Fatalf("a session the user created keeps its mode and resumes without asking: keys=%v", reflectKeys(row))
	}
	after, _ := os.ReadFile(original)
	if string(before) != string(after) {
		t.Fatal("source changed")
	}
	report, err = Sync([]Source{f.source}, f.target, f.backup)
	if err != nil || report.CodeCopied != 0 || report.CodeUpdated != 0 {
		t.Fatalf("repeat sync was not idempotent: %+v %v", report, err)
	}
}

func reflectKeys(row map[string]any) []string {
	keys := make([]string, 0, len(row))
	for key := range row {
		keys = append(keys, key)
	}
	return keys
}

func TestNewestWinsTargetTieAndRoundTripDedupe(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	writeTestFile(t, filepath.Join(f.source.TranscriptRoot, "project", cliA+".jsonl"), "fixture")
	writeRecord(t, f.source, codeTree, sessionA, cliA, 20, map[string]any{"cwd": f.project})
	targetPath := writeRecord(t, f.target, codeTree, sessionB, cliA, 20, map[string]any{
		"cwd": f.project, "targetOnly": true,
	})
	report, err := Sync([]Source{f.source}, f.target, f.backup)
	if err != nil || report.CodeUpdated != 0 || readRecord(t, targetPath)["targetOnly"] != true {
		t.Fatalf("target did not win tie: %+v %v", report, err)
	}
	writeRecord(t, f.source, codeTree, sessionA, cliA, 30, map[string]any{"cwd": f.project})
	report, err = Sync([]Source{f.source}, f.target, f.backup)
	if err != nil || report.CodeUpdated != 1 || readRecord(t, targetPath)["sessionId"] != sessionB {
		t.Fatalf("new source failed to update logical session: %+v %v", report, err)
	}
	if _, err := os.Stat(expectedIndex(f.target, codeTree, sessionA)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("created a duplicate under the source id")
	}
	report, err = Sync([]Source{f.target}, f.source, f.backup)
	if err != nil || report.CodeCopied != 0 || report.CodeUpdated != 0 {
		t.Fatalf("round trip created a duplicate: %+v %v", report, err)
	}
	backups, _ := filepath.Glob(filepath.Join(f.backup, "workspace-sync-*", codeTree, accountB, orgB, sessionB+".json"))
	if len(backups) != 1 || readRecord(t, backups[0])["targetOnly"] != true {
		t.Fatal("old target index not backed up")
	}
}

func TestDeletionMarkersPreventResurrectionAndKeepBackup(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	writeRecord(t, f.target, codeTree, sessionA, cliA, 20, nil)
	marker := filepath.Join(f.source.Dir, codeTree, accountA, orgA, "deleted_"+strings.TrimPrefix(sessionA, "local_"))
	writeTestFile(t, marker, "")
	report, err := Sync([]Source{f.source}, f.target, f.backup)
	if err != nil || report.Tombstoned != 1 {
		t.Fatalf("report=%+v err=%v", report, err)
	}
	if _, err := os.Stat(expectedIndex(f.target, codeTree, sessionA)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("deleted session remains visible")
	}
	backups, _ := filepath.Glob(filepath.Join(f.backup, "workspace-sync-*", codeTree, accountB, orgB, sessionA+".json"))
	if len(backups) != 1 {
		t.Fatal("deleted index lost its backup")
	}
	writeRecord(t, f.source, codeTree, sessionA, cliA, 40, nil)
	report, err = Sync([]Source{f.source}, f.target, f.backup)
	if err != nil || report.CodeCopied != 0 {
		t.Fatalf("deleted source was resurrected: %+v %v", report, err)
	}
}

func TestCodeScratchCopiesFilesToStableLivePath(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.source.LiveDir = filepath.Join(f.root, "canonical-Claude")
	f.target.LiveDir = f.source.LiveDir
	rel := filepath.Join("scratch-workspaces", accountA, orgA, "scratch-fixture")
	writeRecord(t, f.source, codeTree, sessionA, cliA, 10, map[string]any{
		"cwd": filepath.Join(f.source.LiveDir, rel),
	})
	writeTestFile(t, filepath.Join(f.source.TranscriptRoot, "slug", cliA+".jsonl"), "fixture")
	writeTestFile(t, filepath.Join(f.source.Dir, rel, "work.txt"), "work")
	writeTestFile(t, filepath.Join(f.source.Dir, rel, "config.json"), `{"ordinaryWorkFile":true}`)
	writeTestFile(t, filepath.Join(f.source.Dir, "config.json"), "SOURCE PRIVATE")
	writeTestFile(t, filepath.Join(f.target.Dir, "config.json"), "TARGET PRIVATE")
	report, err := Sync([]Source{f.source}, f.target, f.backup)
	if err != nil || report.CodeCopied != 1 {
		t.Fatalf("report=%+v err=%v", report, err)
	}
	if data, err := os.ReadFile(filepath.Join(f.target.Dir, rel, "work.txt")); err != nil || string(data) != "work" {
		t.Fatal("scratch files missing")
	}
	if readRecord(t, expectedIndex(f.target, codeTree, sessionA))["cwd"] != filepath.Join(f.target.LiveDir, rel) {
		t.Fatal("scratch cwd points at a parked profile")
	}
	if readRecord(t, filepath.Join(f.target.Dir, rel, "config.json"))["ordinaryWorkFile"] != true {
		t.Fatal("ordinary scratch config omitted")
	}
	if data, _ := os.ReadFile(filepath.Join(f.target.Dir, "config.json")); string(data) != "TARGET PRIVATE" {
		t.Fatal("Desktop authentication configuration changed")
	}
}

func TestInvalidMetadataMissingTranscriptsAndUnknownIdentityAreReported(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	writeRecord(t, f.source, codeTree, sessionA, cliA, 10, nil)
	writeRecord(t, f.source, codeTree, sessionB, "../invalid", 10, nil)
	unknown := Source{Dir: filepath.Join(f.root, "unknown")}
	report, err := Sync([]Source{f.source, unknown}, f.target, f.backup)
	if err != nil || report.Skipped != 3 || report.CodeCopied != 0 {
		t.Fatalf("report=%+v err=%v", report, err)
	}
}

func TestRejectsSymlinkedIndexAndTargetWithoutChangingData(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	path := writeRecord(t, f.source, codeTree, sessionA, cliA, 10, nil)
	outside := filepath.Join(f.root, "outside")
	writeTestFile(t, outside, "outside")
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, path); err != nil {
		t.Fatal(err)
	}
	_, err := Sync([]Source{f.source}, f.target, f.backup)
	if !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("expected unsafe path, got %v", err)
	}
	data, _ := os.ReadFile(outside)
	if string(data) != "outside" {
		t.Fatal("outside data changed")
	}
}

func TestFileLimitAndCommitFailureRollback(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	tx, err := newTransaction(f.target.Dir, f.backup)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.close()
	if !errors.Is(tx.reserve(maxFileBytes+1), ErrLimit) {
		t.Fatal("large file accepted")
	}
	writeTestFile(t, filepath.Join(f.target.Dir, "one.json"), "original one")
	writeTestFile(t, filepath.Join(f.target.Dir, "two.json"), "original two")
	if err := tx.put("one.json", []byte("new one")); err != nil {
		t.Fatal(err)
	}
	if err := tx.put("two.json", []byte("new two")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(tx.changes[1].staged); err != nil {
		t.Fatal(err)
	}
	if !errors.Is(tx.commit(), ErrIO) {
		t.Fatal("expected failed staged write")
	}
	for _, name := range []string{"one", "two"} {
		data, _ := os.ReadFile(filepath.Join(f.target.Dir, name+".json"))
		if string(data) != "original "+name {
			t.Fatal("rollback did not restore prior target")
		}
	}
}

func TestCopiedWorkPreservesTimestampForFutureUpdates(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	src := filepath.Join(f.source.Dir, "work")
	writeTestFile(t, filepath.Join(src, "script.sh"), "first")
	at := time.Unix(100, 0)
	if err := os.Chtimes(filepath.Join(src, "script.sh"), at, at); err != nil {
		t.Fatal(err)
	}
	tx, err := newTransaction(f.target.Dir, f.backup)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.close()
	report := Report{}
	if err := tx.copyWorkTree(src, "work", &report); err != nil {
		t.Fatal(err)
	}
	if err := tx.commit(); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(f.target.Dir, "work", "script.sh"))
	if err != nil || !info.ModTime().Equal(at) {
		t.Fatal("mtime not preserved")
	}
}

func TestFailedAtomicInstallKeepsExistingIndexBeforeRollback(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	dest := filepath.Join(f.target.Dir, "existing.json")
	writeTestFile(t, dest, "original")
	tx, err := newTransaction(f.target.Dir, f.backup)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.close()
	if err := tx.put("existing.json", []byte("replacement")); err != nil {
		t.Fatal(err)
	}
	tx.backupDir = filepath.Join(f.backup, "fixture")
	if err := os.Remove(tx.changes[0].staged); err != nil {
		t.Fatal(err)
	}
	if !errors.Is(tx.install(0), ErrIO) {
		t.Fatal("missing staged file unexpectedly installed")
	}
	if data, _ := os.ReadFile(dest); string(data) != "original" {
		t.Fatal("existing index disappeared before rollback")
	}
	if data, _ := os.ReadFile(filepath.Join(tx.backupDir, "existing.json")); string(data) != "original" {
		t.Fatal("durable backup missing")
	}
}
