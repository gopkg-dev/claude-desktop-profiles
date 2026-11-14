package workspaces

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestImportsKeepModeAndOnlyTrustSessionsTheUserStarted(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	writeTestFile(t, filepath.Join(f.source.TranscriptRoot, "project", cliA+".jsonl"), "fixture")
	writeTestFile(t, filepath.Join(f.source.TranscriptRoot, "project", cliB+".jsonl"), "fixture")
	writeRecord(t, f.source, codeTree, sessionA, cliA, 10, map[string]any{
		"cwd": f.project, "permissionMode": "auto", "autoChosenInApp": true, "bypassChosenInApp": false,
	})
	// Imported from elsewhere and never confirmed: still asks.
	writeRecord(t, f.source, codeTree, sessionB, cliB, 10, map[string]any{
		"cwd": f.project, "importedFrom": "local-1p-code",
	})
	if _, err := Sync([]Source{f.source}, f.target, f.backup); err != nil {
		t.Fatal(err)
	}
	native := readRecord(t, expectedIndex(f.target, codeTree, sessionA))
	if native["permissionMode"] != "auto" || native["autoChosenInApp"] != true || native["bypassChosenInApp"] != false ||
		native["resumeConfirmed"] != true {
		t.Fatalf("native session lost its mode or asks again: %v", native)
	}
	foreign := readRecord(t, expectedIndex(f.target, codeTree, sessionB))
	if foreign["resumeConfirmed"] != nil || foreign["permissionMode"] != nil {
		t.Fatalf("an unconfirmed import must still ask: %v", foreign)
	}
}

func TestEarlierImportsAreConfirmedAndGetTheirModeBack(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	writeTestFile(t, filepath.Join(f.source.TranscriptRoot, "project", cliA+".jsonl"), "fixture")
	writeRecord(t, f.source, codeTree, sessionA, cliA, 10, map[string]any{
		"cwd": f.project, "permissionMode": "bypassPermissions", "bypassChosenInApp": true,
	})
	// What an earlier sync wrote, used since in the target, so it is the newest copy.
	path := writeRecord(t, f.target, codeTree, sessionA, cliA, 20, map[string]any{
		"cwd": f.project, "importedFrom": "local-1p-code", "targetOnly": "kept", "completedTurns": 3,
	})
	report, err := Sync([]Source{f.source}, f.target, f.backup)
	if err != nil || report.CodeUpdated != 1 {
		t.Fatalf("report=%+v err=%v", report, err)
	}
	row := readRecord(t, path)
	if row["resumeConfirmed"] != true || row["permissionMode"] != "bypassPermissions" || row["bypassChosenInApp"] != true ||
		row["targetOnly"] != "kept" || row["completedTurns"] != float64(3) || row["lastActivityAt"] != float64(20) {
		t.Fatalf("repair changed more than it should or missed: %v", row)
	}
	report, err = Sync([]Source{f.source}, f.target, f.backup)
	if err != nil || report.CodeUpdated != 0 || report.CodeCopied != 0 {
		t.Fatalf("repair is not idempotent: %+v %v", report, err)
	}
}

func writeConfig(t *testing.T, source Source, value map[string]any) string {
	t.Helper()
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(source.Dir, desktopConfig)
	writeTestFile(t, path, string(data))
	return path
}

func TestFolderModesAcksAndBypassOptInFollowTheUser(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	writeConfig(t, f.source, map[string]any{"preferences": map[string]any{
		"epitaxyPrefs": map[string]any{
			folderModesPrefix + accountA: map[string]any{"/work/a": "bypassPermissions", "/work/shared": "auto"},
			modeAcksPrefix + accountA:    []any{"/work/a:bypassPermissions"},
		},
		bypassOptIn:                      map[string]any{accountA: true},
		"bypassPermissionsGateByAccount": map[string]any{accountA: true},
	}})
	path := writeConfig(t, f.target, map[string]any{
		"coworkUserFilesPath": "/kept",
		"bigNumber":           json.Number("12345678901234567890"),
		"preferences": map[string]any{
			"sidebarMode": "kept",
			"epitaxyPrefs": map[string]any{
				folderModesPrefix + accountB: map[string]any{"/work/shared": "default"},
				"other.setting":              "<kept>",
			},
		},
	})
	if _, err := Sync([]Source{f.source}, f.target, f.backup); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), "12345678901234567890") || !strings.Contains(string(data), `"<kept>"`) {
		t.Fatalf("unrelated values were rewritten:\n%s", data)
	}
	root := readRecord(t, path)
	preferences := root["preferences"].(map[string]any)
	epitaxy := preferences["epitaxyPrefs"].(map[string]any)
	folders := epitaxy[folderModesPrefix+accountB].(map[string]any)
	if folders["/work/a"] != "bypassPermissions" || folders["/work/shared"] != "default" {
		t.Fatalf("folder modes: %v", folders)
	}
	if acks := epitaxy[modeAcksPrefix+accountB].([]any); len(acks) != 1 || acks[0] != "/work/a:bypassPermissions" {
		t.Fatalf("acks: %v", acks)
	}
	if preferences[bypassOptIn].(map[string]any)[accountB] != true {
		t.Fatal("bypass opt-in not carried")
	}
	if _, ok := preferences["bypassPermissionsGateByAccount"]; ok || root["coworkUserFilesPath"] != "/kept" ||
		preferences["sidebarMode"] != "kept" {
		t.Fatalf("touched what it should not: %v", root)
	}
	before, _ := os.ReadFile(path)
	if _, err := Sync([]Source{f.source}, f.target, f.backup); err != nil {
		t.Fatal(err)
	}
	if after, _ := os.ReadFile(path); string(after) != string(before) {
		t.Fatal("merging preferences again changed the config")
	}
}

func TestAnExplicitNoToBypassStays(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	writeConfig(t, f.source, map[string]any{"preferences": map[string]any{bypassOptIn: map[string]any{accountA: true}}})
	path := writeConfig(t, f.target, map[string]any{"preferences": map[string]any{bypassOptIn: map[string]any{accountB: false}}})
	before, _ := os.ReadFile(path)
	if _, err := Sync([]Source{f.source}, f.target, f.backup); err != nil {
		t.Fatal(err)
	}
	if readRecord(t, path)["preferences"].(map[string]any)[bypassOptIn].(map[string]any)[accountB] != false {
		t.Fatal("overrode the target's own choice")
	}
	if after, _ := os.ReadFile(path); string(after) != string(before) {
		t.Fatal("rewrote the config with nothing to change")
	}
}
