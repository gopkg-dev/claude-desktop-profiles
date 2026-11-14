package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// These fragments are deliberately exact: an upstream change must be reviewed, not silently left unsigned.
const originalDevSigning = `			if err != nil || len(p.Widgets) == 0 {
				return err
			}
`

const configuredDevSigning = `			if err != nil {
				return err
			}
			if err := signMacApp(ctx, p, app); err != nil {
				return err
			}
			if len(p.Widgets) == 0 {
				return nil
			}
`

type moduleInfo struct {
	Dir     string
	Version string
}

func prepareTool(ctx context.Context, development bool) (string, func(), error) {
	data, err := exec.CommandContext(ctx, "go", "list", "-m", "-json", upstreamModule).Output()
	if err != nil {
		return "", nil, fmt.Errorf("无法定位项目锁定的 NSGo 模块：%w", err)
	}
	var module moduleInfo
	if err := json.Unmarshal(data, &module); err != nil || module.Dir == "" || module.Version == "" {
		return "", nil, errors.New("NSGo 模块路径不可用，请先下载项目依赖")
	}
	data, err = exec.CommandContext(ctx, "go", "env", "GOMOD").Output()
	if err != nil {
		return "", nil, err
	}
	projectMod := strings.TrimSpace(string(data))
	if projectMod == "" || projectMod == os.DevNull {
		return "", nil, errors.New("请在项目模块内运行 go tool nsgo dev")
	}
	cache := filepath.Join(filepath.Dir(projectMod), ".nsgo", "tools")
	if err := os.MkdirAll(cache, 0o700); err != nil {
		return "", nil, err
	}
	source := module.Dir
	if development {
		source, err = prepareFork(cache, module)
		if err != nil {
			return "", nil, err
		}
	}
	return prepareModfile(ctx, cache, filepath.Join(module.Dir, "go.mod"), source, module.Version)
}

func patchDevSource(source []byte) ([]byte, error) {
	if bytes.Count(source, []byte(originalDevSigning)) != 1 {
		return nil, errors.New("NSGo 开发签名适配与当前上游版本不匹配，请检查 tools/nsgo/patch.go")
	}
	return bytes.Replace(source, []byte(originalDevSigning), []byte(configuredDevSigning), 1), nil
}

func prepareFork(cache string, module moduleInfo) (string, error) {
	relative := filepath.Join("cmd", "nsgo", "dev.go")
	source, err := os.ReadFile(filepath.Join(module.Dir, relative))
	if err != nil {
		return "", err
	}
	patched, err := patchDevSource(source)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(append([]byte(module.Version+"\n"+module.Dir+"\n"), patched...))
	fork := filepath.Join(cache, fmt.Sprintf("nsgo-%x", digest[:12]))
	if existing, err := os.ReadFile(filepath.Join(fork, relative)); err == nil && bytes.Equal(existing, patched) {
		return fork, nil
	}
	if err := os.MkdirAll(cache, 0o700); err != nil {
		return "", err
	}
	staging, err := os.MkdirTemp(cache, ".nsgo-signing-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(staging)
	if err := os.CopyFS(staging, os.DirFS(module.Dir)); err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(staging, relative), patched, 0o644); err != nil {
		return "", err
	}
	if err := os.Rename(staging, fork); err != nil {
		existing, readErr := os.ReadFile(filepath.Join(fork, relative))
		if readErr != nil || !bytes.Equal(existing, patched) {
			return "", err
		}
	}
	return fork, nil
}

// prepareModfile gives the CLI its upstream dependency graph without changing the app's go.mod.
func prepareModfile(ctx context.Context, cache, originalMod, source, version string) (string, func(), error) {
	directory, err := os.MkdirTemp(cache, "session-")
	if err != nil {
		return "", nil, err
	}
	cleanup := func() { _ = os.RemoveAll(directory) }
	modfile := filepath.Join(directory, "tool.mod")
	for _, extension := range []string{".mod", ".sum"} {
		from := strings.TrimSuffix(originalMod, ".mod") + extension
		data, err := os.ReadFile(from)
		if errors.Is(err, os.ErrNotExist) && extension == ".sum" {
			continue
		}
		if err != nil {
			cleanup()
			return "", nil, err
		}
		if err := os.WriteFile(filepath.Join(directory, "tool"+extension), data, 0o600); err != nil {
			cleanup()
			return "", nil, err
		}
	}
	command := exec.CommandContext(ctx, "go", "mod", "edit", "-modfile="+modfile,
		"-module=claudeprofiles-nsgo-tool", "-require="+upstreamModule+"@"+version,
		"-replace="+upstreamModule+"="+source)
	if data, err := command.CombinedOutput(); err != nil {
		cleanup()
		return "", nil, fmt.Errorf("无法准备 NSGo 开发签名工具：%w\n%s", err, data)
	}
	return modfile, cleanup, nil
}
