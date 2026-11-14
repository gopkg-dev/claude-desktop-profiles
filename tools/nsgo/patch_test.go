package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestDevSigningOccursBeforeWidgetRegistration(t *testing.T) {
	source := []byte("before\n" + originalDevSigning + "after\n")
	patched, err := patchDevSource(source)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(patched, []byte("before\n")) || !bytes.HasSuffix(patched, []byte("after\n")) {
		t.Fatal("unrelated upstream code changed")
	}
	signing := bytes.Index(patched, []byte("if err := signMacApp(ctx, p, app); err != nil {\n\t\t\t\treturn err"))
	widgets := bytes.Index(patched, []byte("if len(p.Widgets) == 0"))
	if signing < 0 || widgets <= signing {
		t.Fatal("dev must finish signing, including failures, before starting an app without widgets")
	}
	for _, changed := range [][]byte{[]byte("changed upstream"), append(source, source...), patched} {
		if _, err := patchDevSource(changed); err == nil {
			t.Fatal("unknown or already patched upstream must require review")
		}
	}
}

func TestDevToolCachePreservesOriginalModuleAndReusesPatchedCopy(t *testing.T) {
	directory := t.TempDir()
	upstream := filepath.Join(directory, "upstream")
	sourcePath := filepath.Join(upstream, "cmd", "nsgo", "dev.go")
	if err := os.MkdirAll(filepath.Dir(sourcePath), 0o700); err != nil {
		t.Fatal(err)
	}
	source := []byte("before\n" + originalDevSigning + "after\n")
	if err := os.WriteFile(sourcePath, source, 0o444); err != nil {
		t.Fatal(err)
	}
	resource := filepath.Join(upstream, "resource.txt")
	if err := os.WriteFile(resource, []byte("embed resource"), 0o444); err != nil {
		t.Fatal(err)
	}
	cache := filepath.Join(directory, "cache")
	module := moduleInfo{Dir: upstream, Version: "fixture"}
	fork, err := prepareFork(cache, module)
	if err != nil {
		t.Fatal(err)
	}
	if again, err := prepareFork(cache, module); err != nil || again != fork {
		t.Fatal("unchanged dependency regenerated a different tool cache", err)
	}
	original, err := os.ReadFile(sourcePath)
	if err != nil || !bytes.Equal(original, source) {
		t.Fatal("global module source was modified", err)
	}
	info, err := os.Stat(sourcePath)
	if err != nil || info.Mode().Perm() != 0o444 {
		t.Fatal("global module permissions were modified", err)
	}
	patched, err := os.ReadFile(filepath.Join(fork, "cmd", "nsgo", "dev.go"))
	if err != nil || !bytes.Contains(patched, []byte("signMacApp(ctx, p, app)")) {
		t.Fatal("configured signing is missing from the local tool copy", err)
	}
	data, err := os.ReadFile(filepath.Join(fork, "resource.txt"))
	if err != nil || string(data) != "embed resource" {
		t.Fatal("tool resource was not copied", err)
	}
}

func TestDevModfileReplacesOnlyToolDependencyWithoutChangingProject(t *testing.T) {
	directory := t.TempDir()
	cache := filepath.Join(directory, "cache")
	if err := os.Mkdir(cache, 0o700); err != nil {
		t.Fatal(err)
	}
	original := []byte("module fixture\n\ngo 1.27.3\n\nrequire " + upstreamModule + " v0.0.0\n" +
		"replace " + upstreamModule + " => ./previous\n")
	projectMod := filepath.Join(directory, "go.mod")
	if err := os.WriteFile(projectMod, original, 0o600); err != nil {
		t.Fatal(err)
	}
	checksums := []byte("example.com/fixture v1.0.0 h1:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=\n")
	if err := os.WriteFile(filepath.Join(directory, "go.sum"), checksums, 0o600); err != nil {
		t.Fatal(err)
	}
	fork := filepath.Join(directory, "path with spaces", "nsgo")
	modfile, cleanup, err := prepareModfile(context.Background(), cache, projectMod, fork, "v0.0.0")
	if err != nil {
		t.Fatal(err)
	}
	data, err := exec.Command("go", "mod", "edit", "-modfile="+modfile, "-json").Output()
	if err != nil {
		cleanup()
		t.Fatal(err)
	}
	var result struct {
		Module  struct{ Path string }
		Require []struct{ Path, Version string }
		Replace []struct {
			Old struct{ Path string }
			New struct{ Path string }
		}
	}
	if err := json.Unmarshal(data, &result); err != nil {
		cleanup()
		t.Fatal(err)
	}
	if result.Module.Path != "claudeprofiles-nsgo-tool" || len(result.Require) != 1 ||
		result.Require[0].Path != upstreamModule || result.Require[0].Version != "v0.0.0" ||
		len(result.Replace) != 1 || result.Replace[0].Old.Path != upstreamModule || result.Replace[0].New.Path != fork {
		cleanup()
		t.Fatal("tool override was not correctly isolated")
	}
	data, err = os.ReadFile(projectMod)
	if err != nil || !bytes.Equal(data, original) {
		cleanup()
		t.Fatal("project module configuration was changed", err)
	}
	data, err = os.ReadFile(filepath.Join(filepath.Dir(modfile), "tool.sum"))
	if err != nil || !bytes.Equal(data, checksums) {
		cleanup()
		t.Fatal("upstream dependency checksums were not preserved", err)
	}
	cleanup()
	if _, err := os.Stat(modfile); !os.IsNotExist(err) {
		t.Fatal("temporary module configuration was not cleaned up")
	}
}
