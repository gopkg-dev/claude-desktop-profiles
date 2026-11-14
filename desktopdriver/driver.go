// Package desktopdriver controls Claude Desktop without changing its files.
package desktopdriver

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const bundleID = "com.anthropic.claudefordesktop"

var (
	ErrUnsupported  = errors.New("仅支持 macOS 上的 Claude Desktop")
	ErrInvalidPath  = errors.New("Claude Desktop 数据目录必须是绝对路径")
	ErrInspection   = errors.New("无法确认 Claude Desktop 是否已完全退出")
	ErrQuit         = errors.New("无法请求 Claude Desktop 退出")
	ErrStillRunning = errors.New("Claude Desktop 或其会话仍在运行，请结束任务并退出后重试")
	ErrLaunch       = errors.New("无法打开 Claude Desktop，请确认应用已安装")
)

type commandRunner func(context.Context, string, ...string) ([]byte, error)

// Driver starts Desktop and waits for all processes using its data to exit.
type Driver struct {
	LiveDir      string
	run          commandRunner
	stopTimeout  time.Duration
	pollInterval time.Duration
}

type process struct {
	pid        int
	executable string
	paths      []string
}

func runCommand(ctx context.Context, name string, args ...string) ([]byte, error) {
	// Output never includes stderr, which may contain application diagnostics.
	return exec.CommandContext(ctx, name, args...).Output()
}

func (d *Driver) command(ctx context.Context, name string, args ...string) ([]byte, error) {
	run := d.run
	if run == nil {
		run = runCommand
	}
	commandCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return run(commandCtx, name, args...)
}

func (d *Driver) stop(ctx context.Context) error {
	if !filepath.IsAbs(d.LiveDir) || filepath.Clean(d.LiveDir) == string(filepath.Separator) {
		return ErrInvalidPath
	}
	timeout := d.stopTimeout
	if timeout <= 0 {
		timeout = 20 * time.Second
	}
	stopCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	running, err := d.running(stopCtx)
	if err != nil {
		return err
	}
	if len(running) == 0 {
		return nil
	}
	_, err = d.command(stopCtx, "/usr/bin/osascript", "-e",
		`tell application id "`+bundleID+`" to quit`)
	if err != nil {
		return publicError(stopCtx, ErrQuit)
	}
	interval := d.pollInterval
	if interval <= 0 {
		interval = 250 * time.Millisecond
	}
	for {
		if stopCtx.Err() != nil {
			return publicError(stopCtx, ErrStillRunning)
		}
		running, err = d.running(stopCtx)
		if err != nil {
			if stopCtx.Err() != nil {
				return publicError(stopCtx, ErrStillRunning)
			}
			return err
		}
		if len(running) == 0 {
			return nil
		}
		timer := time.NewTimer(interval)
		select {
		case <-stopCtx.Done():
			timer.Stop()
			return publicError(stopCtx, ErrStillRunning)
		case <-timer.C:
		}
	}
}

func (d *Driver) start(ctx context.Context) error {
	if ctx.Err() != nil {
		return publicError(ctx, ErrLaunch)
	}
	if _, err := d.command(ctx, "/usr/bin/open", "-b", bundleID); err != nil {
		return publicError(ctx, ErrLaunch)
	}
	return nil
}

func publicError(ctx context.Context, kind error) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("%w: %w", kind, err)
	}
	return kind
}

func (d *Driver) running(ctx context.Context) ([]process, error) {
	if ctx.Err() != nil {
		return nil, publicError(ctx, ErrInspection)
	}
	out, err := d.command(ctx, "/bin/ps", "-axo", "pid=,uid=,comm=")
	if err != nil {
		return nil, publicError(ctx, ErrInspection)
	}
	procs, err := parseProcesses(string(out), os.Getuid())
	if err != nil {
		return nil, ErrInspection
	}
	if len(procs) == 0 {
		// A running caller must be visible; an empty snapshot is not proof of idleness.
		return nil, ErrInspection
	}
	out, err = d.command(ctx, "/usr/sbin/lsof", "-nP", "-w", "-a", "-u",
		strconv.Itoa(os.Getuid()), "-d", "cwd,txt", "-Fpn")
	if err != nil {
		// A partial listing may omit an embedded Code process. Never swap on uncertainty.
		return nil, publicError(ctx, ErrInspection)
	}
	openPaths, err := parseOpenPaths(string(out))
	if err != nil || len(openPaths) == 0 {
		return nil, ErrInspection
	}
	byPID := make(map[int]int, len(procs))
	for i := range procs {
		byPID[procs[i].pid] = i
	}
	for pid, paths := range openPaths {
		if index, ok := byPID[pid]; ok {
			procs[index].paths = paths
			continue
		}
		// Include children born between ps and lsof instead of dropping them.
		procs = append(procs, process{pid: pid, paths: paths})
	}
	var running []process
	for _, proc := range procs {
		if touchesDesktop(proc, d.LiveDir) {
			running = append(running, proc)
		}
	}
	return running, nil
}

func parseProcesses(output string, uid int) ([]process, error) {
	var procs []process
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		if len(fields) < 3 {
			return nil, ErrInspection
		}
		pid, pidErr := strconv.Atoi(fields[0])
		owner, uidErr := strconv.Atoi(fields[1])
		if pidErr != nil || uidErr != nil || pid <= 0 {
			return nil, ErrInspection
		}
		if owner != uid {
			continue
		}
		// Preserve spaces in application paths rather than joining split fields.
		rest := strings.TrimSpace(line)
		rest = strings.TrimSpace(rest[len(fields[0]):])
		rest = strings.TrimSpace(rest[len(fields[1]):])
		procs = append(procs, process{pid: pid, executable: rest})
	}
	return procs, nil
}

func parseOpenPaths(output string) (map[int][]string, error) {
	paths := make(map[int][]string)
	pid := 0
	for _, line := range strings.Split(output, "\n") {
		if line == "" {
			continue
		}
		switch line[0] {
		case 'p':
			parsed, err := strconv.Atoi(line[1:])
			if err != nil || parsed <= 0 {
				return nil, ErrInspection
			}
			pid = parsed
		case 'n':
			if pid == 0 || len(line) < 2 {
				return nil, ErrInspection
			}
			paths[pid] = append(paths[pid], line[1:])
		}
	}
	return paths, nil
}

func touchesDesktop(proc process, liveDir string) bool {
	paths := append([]string{proc.executable}, proc.paths...)
	for _, path := range paths {
		clean := filepath.Clean(path)
		if clean == filepath.Clean(liveDir) || strings.HasPrefix(clean, filepath.Clean(liveDir)+"/") {
			return true
		}
		if strings.Contains(path, "/Claude.app/Contents/") &&
			!strings.HasSuffix(path, "/Helpers/chrome-native-host") {
			return true
		}
	}
	return false
}
