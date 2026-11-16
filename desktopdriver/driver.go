// Package desktopdriver controls Claude Desktop without changing its files.
package desktopdriver

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
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

type signaler func(pid int, sig syscall.Signal) error

// Driver starts Desktop and waits for all processes using its data to exit.
type Driver struct {
	LiveDir      string
	run          commandRunner
	kill         signaler
	stopTimeout  time.Duration
	pollInterval time.Duration
	// forceGrace is how long each forced step waits before escalating.
	forceGrace time.Duration
}

type forceKey struct{}

// WithForceQuit marks a stop the user explicitly allowed to terminate leftover processes.
// Without it the driver only asks Desktop to quit and never signals anything.
func WithForceQuit(ctx context.Context) context.Context {
	return context.WithValue(ctx, forceKey{}, true)
}

// ForceRequested reports whether WithForceQuit marked ctx.
func ForceRequested(ctx context.Context) bool {
	force, _ := ctx.Value(forceKey{}).(bool)
	return force
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
	if ForceRequested(ctx) {
		// A hung Desktop may not answer the quit request; that is what forcing is for.
		return d.forceStop(stopCtx, running)
	}
	if err != nil {
		return publicError(stopCtx, ErrQuit)
	}
	_, err = d.waitIdle(stopCtx, stopCtx, running)
	return err
}

// waitIdle polls until nothing touches Desktop's data or until wait ends. It returns the last
// complete snapshot so a timeout can name what is still running.
func (d *Driver) waitIdle(ctx, wait context.Context, running []process) ([]process, error) {
	interval := d.pollInterval
	if interval <= 0 {
		interval = 250 * time.Millisecond
	}
	for {
		if wait.Err() != nil {
			return running, stillRunning(wait, running)
		}
		latest, err := d.running(ctx)
		if err != nil {
			if wait.Err() != nil {
				// Report the last complete snapshot rather than nothing.
				return running, stillRunning(wait, running)
			}
			return running, err
		}
		running = latest
		if len(running) == 0 {
			return nil, nil
		}
		timer := time.NewTimer(interval)
		select {
		case <-wait.Done():
			timer.Stop()
			return running, stillRunning(wait, running)
		case <-timer.C:
		}
	}
}

// forceStop gives Desktop a short chance to quit, then sends SIGTERM and finally SIGKILL to
// the processes still touching its data. Each round signals only PIDs from a fresh snapshot
// owned by this user, and never this process.
func (d *Driver) forceStop(ctx context.Context, running []process) error {
	grace := d.forceGrace
	if grace <= 0 {
		grace = 4 * time.Second
	}
	kill := d.kill
	if kill == nil {
		kill = defaultKill
	}
	for _, sig := range []syscall.Signal{0, syscall.SIGTERM, syscall.SIGKILL} {
		if sig != 0 {
			self := os.Getpid()
			for _, proc := range running {
				if proc.pid > 1 && proc.pid != self {
					// A process that already exited or cannot be signaled is reported by the next check.
					_ = kill(proc.pid, sig)
				}
			}
		}
		wait, cancel := context.WithTimeout(ctx, grace)
		latest, err := d.waitIdle(ctx, wait, running)
		cancel()
		if err == nil {
			return nil
		}
		if !errors.Is(err, ErrStillRunning) || ctx.Err() != nil {
			return err
		}
		running = latest
	}
	return &driverError{kind: ErrStillRunning, blockers: running}
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
		return &driverError{kind: kind, cause: err}
	}
	return kind
}

// stillRunning names the processes that kept Desktop's data busy so the user
// can end them without restarting the computer.
func stillRunning(ctx context.Context, running []process) error {
	return &driverError{kind: ErrStillRunning, cause: ctx.Err(), blockers: running}
}

// driverError keeps errors.Is matching while showing a localized, path-free message.
type driverError struct {
	kind     error
	cause    error
	blockers []process
}

func (e *driverError) Error() string {
	var b strings.Builder
	b.WriteString(e.kind.Error())
	if names := describeProcesses(e.blockers); names != "" {
		b.WriteString("。未退出的进程：")
		b.WriteString(names)
	}
	switch {
	case errors.Is(e.cause, context.DeadlineExceeded):
		b.WriteString("（等待超时）")
	case errors.Is(e.cause, context.Canceled):
		b.WriteString("（已取消）")
	}
	return b.String()
}

func (e *driverError) Unwrap() []error {
	if e.cause == nil {
		return []error{e.kind}
	}
	return []error{e.kind, e.cause}
}

const maxDescribedProcesses = 4

// describeProcesses lists program names and PIDs only; full paths can contain
// the user's home directory or project names.
func describeProcesses(procs []process) string {
	if len(procs) == 0 {
		return ""
	}
	sorted := append([]process(nil), procs...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].pid < sorted[j].pid })
	var parts []string
	for _, proc := range sorted[:min(len(sorted), maxDescribedProcesses)] {
		parts = append(parts, fmt.Sprintf("%s（PID\u00a0%d）", processName(proc), proc.pid))
	}
	text := strings.Join(parts, "、")
	if extra := len(sorted) - maxDescribedProcesses; extra > 0 {
		text += fmt.Sprintf(" 等 %d 个", len(sorted))
	}
	return text
}

func processName(proc process) string {
	name := filepath.Base(proc.executable)
	if proc.executable == "" || name == "." || name == string(filepath.Separator) {
		return "未知进程"
	}
	return name
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
