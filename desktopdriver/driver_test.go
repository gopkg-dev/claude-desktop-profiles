package desktopdriver

import (
	"context"
	"errors"
	"fmt"
	"os"
	"reflect"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestTouchesDesktop(t *testing.T) {
	t.Parallel()
	live := "/Users/test/Library/Application Support/Claude"
	tests := []struct {
		name string
		proc process
		want bool
	}{
		{"application", process{executable: "/Applications/Claude.app/Contents/MacOS/Claude"}, true},
		{"updated application", process{executable: "/tmp/update/Claude.app/Contents/MacOS/Claude"}, true},
		{"renderer", process{
			executable: "/Applications/Claude.app/Contents/Frameworks/Claude Helper.app/Contents/MacOS/Claude Helper",
		}, true},
		{"code title rewritten", process{executable: "claude", paths: []string{live + "/claude-code/claude"}}, true},
		{"work directory", process{executable: "/bin/sh", paths: []string{live}}, true},
		{"similar directory", process{executable: "node", paths: []string{live + "-other/code"}}, false},
		{"unrelated CLI", process{executable: "/opt/homebrew/bin/claude", paths: []string{"/Users/test/project"}}, false},
		{"browser host", process{executable: "/Applications/Claude.app/Contents/Helpers/chrome-native-host"}, false},
		{"host accessing live dir", process{
			executable: "/Applications/Claude.app/Contents/Helpers/chrome-native-host",
			paths:      []string{live + "/work"},
		}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := touchesDesktop(tt.proc, live); got != tt.want {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestProcessParsingPreservesPathsAndFiltersUID(t *testing.T) {
	t.Parallel()
	input := "  12 501 /Applications/Claude.app/Contents/MacOS/Claude\n" +
		"  13 502 /Applications/Claude.app/Contents/MacOS/Claude\n" +
		"  14 501 /Users/test/My Apps/Claude.app/Contents/MacOS/Claude\n"
	procs, err := parseProcesses(input, 501)
	if err != nil || len(procs) != 2 {
		t.Fatalf("processes=%v, err=%v", procs, err)
	}
	if procs[1].executable != "/Users/test/My Apps/Claude.app/Contents/MacOS/Claude" {
		t.Fatalf("path changed: %q", procs[1].executable)
	}
	if _, err := parseProcesses("invalid output", 501); !errors.Is(err, ErrInspection) {
		t.Fatalf("malformed process output accepted: %v", err)
	}
}

func TestOpenPathParsing(t *testing.T) {
	t.Parallel()
	paths, err := parseOpenPaths("p12\nn/Applications/Claude.app/Contents/MacOS/Claude\nn/Users/test/My Work\n")
	if err != nil || len(paths[12]) != 2 || paths[12][1] != "/Users/test/My Work" {
		t.Fatalf("paths=%v, err=%v", paths, err)
	}
	for _, input := range []string{"px\nn/a", "n/orphan", "p0\nn/a", "p12\nn"} {
		if _, err := parseOpenPaths(input); !errors.Is(err, ErrInspection) {
			t.Fatalf("malformed output %q accepted: %v", input, err)
		}
	}
}

type commandCall struct {
	name string
	args []string
}

type fakeCommands struct {
	live          string
	stopAfterQuit bool
	quit          bool
	commands      []commandCall
	failName      string
	childOnly     bool
	killed        bool
	hungQuit      bool
}

func (f *fakeCommands) run(ctx context.Context, name string, args ...string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	f.commands = append(f.commands, commandCall{name, append([]string(nil), args...)})
	if name == f.failName {
		return []byte("partial potentially sensitive output"), errors.New("sensitive command failure")
	}
	running := !(f.stopAfterQuit && f.quit) && !f.killed
	switch name {
	case "/bin/ps":
		text := fmt.Sprintf("1 %d /usr/bin/fake-test\n", os.Getuid())
		if running && !f.childOnly {
			text += fmt.Sprintf("42 %d /Applications/Claude.app/Contents/MacOS/Claude\n", os.Getuid())
		}
		return []byte(text), nil
	case "/usr/sbin/lsof":
		text := "p1\nn/Users/test\n"
		if running {
			text += "p42\nn" + f.live + "/embedded-code\n"
		}
		return []byte(text), nil
	case "/usr/bin/osascript":
		if f.hungQuit {
			return nil, errors.New("no reply")
		}
		f.quit = true
		return nil, nil
	case "/usr/bin/open":
		return nil, nil
	default:
		return nil, fmt.Errorf("unexpected command %s", name)
	}
}

func TestStopWaitsForAllProcessesWithoutKilling(t *testing.T) {
	t.Parallel()
	f := &fakeCommands{live: "/Users/test/Library/Application Support/Claude", stopAfterQuit: true}
	d := Driver{LiveDir: f.live, run: f.run}
	if err := d.stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	wantQuit := []string{"-e", `tell application id "com.anthropic.claudefordesktop" to quit`}
	var quitCount, inspectCount int
	for _, cmd := range f.commands {
		if cmd.name == "/usr/bin/osascript" {
			quitCount++
			if !reflect.DeepEqual(cmd.args, wantQuit) {
				t.Fatalf("unexpected quit invocation: %v", cmd)
			}
		}
		if cmd.name == "/usr/sbin/lsof" {
			inspectCount++
		}
		if strings.Contains(cmd.name, "kill") {
			t.Fatalf("must not kill Desktop: %v", cmd)
		}
	}
	if quitCount != 1 || inspectCount < 2 {
		t.Fatalf("quit=%d inspections=%d", quitCount, inspectCount)
	}
}

func TestStopDoesNothingWhenAlreadyIdle(t *testing.T) {
	t.Parallel()
	f := &fakeCommands{live: "/Users/test/Library/Application Support/Claude", stopAfterQuit: true, quit: true}
	d := Driver{LiveDir: f.live, run: f.run}
	if err := d.stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, cmd := range f.commands {
		if cmd.name == "/usr/bin/osascript" {
			t.Fatal("an idle Desktop must not be launched to quit it")
		}
	}
}

func TestStopDetectsChildCreatedBetweenListings(t *testing.T) {
	t.Parallel()
	f := &fakeCommands{
		live: "/Users/test/Library/Application Support/Claude", stopAfterQuit: true, childOnly: true,
	}
	d := Driver{LiveDir: f.live, run: f.run}
	if err := d.stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !f.quit {
		t.Fatal("process found only by lsof was dropped")
	}
}

func TestStopRefusesPartialInspection(t *testing.T) {
	t.Parallel()
	for _, failing := range []string{"/bin/ps", "/usr/sbin/lsof"} {
		t.Run(failing, func(t *testing.T) {
			f := &fakeCommands{live: "/Users/test/Library/Application Support/Claude", failName: failing}
			d := Driver{LiveDir: f.live, run: f.run}
			err := d.stop(context.Background())
			if !errors.Is(err, ErrInspection) || f.quit {
				t.Fatalf("err=%v, quit=%v", err, f.quit)
			}
			if strings.Contains(err.Error(), "sensitive") {
				t.Fatal("command output leaked into error")
			}
		})
	}
}

func TestStopTimeoutLeavesAppAlive(t *testing.T) {
	t.Parallel()
	f := &fakeCommands{live: "/Users/test/Library/Application Support/Claude"}
	d := Driver{LiveDir: f.live, run: f.run, stopTimeout: 20 * time.Millisecond, pollInterval: time.Millisecond}
	err := d.stop(context.Background())
	if !errors.Is(err, context.DeadlineExceeded) || !errors.Is(err, ErrStillRunning) {
		t.Fatalf("expected timeout, got %v", err)
	}
	message := err.Error()
	if !strings.Contains(message, "Claude（PID\u00a042）") || !strings.Contains(message, "等待超时") {
		t.Fatalf("timeout does not name the blocking process: %q", message)
	}
	if strings.Contains(message, "deadline") || strings.Contains(message, f.live) {
		t.Fatalf("timeout leaks internal detail: %q", message)
	}
}

func TestDescribeProcessesLimitsAndOrders(t *testing.T) {
	t.Parallel()
	procs := []process{
		{pid: 9, executable: "/x/claude"}, {pid: 3}, {pid: 5, executable: "/A/Claude Helper (Renderer)"},
		{pid: 7, executable: "/x/disclaimer"}, {pid: 1, executable: "/A/Claude"},
	}
	got := describeProcesses(procs)
	want := "Claude（PID\u00a01）、未知进程（PID\u00a03）、Claude Helper (Renderer)（PID\u00a05）、disclaimer（PID\u00a07） 等 5 个"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	if describeProcesses(nil) != "" {
		t.Fatal("empty list must add nothing")
	}
}

func TestStopRejectsInvalidPathBeforeAnyCommand(t *testing.T) {
	t.Parallel()
	for _, path := range []string{"", ".", "/"} {
		f := &fakeCommands{}
		d := Driver{LiveDir: path, run: f.run}
		if err := d.stop(context.Background()); !errors.Is(err, ErrInvalidPath) || len(f.commands) != 0 {
			t.Fatalf("path=%q err=%v commands=%v", path, err, f.commands)
		}
	}
}

func TestStartUsesBundleIDAndSanitizesErrors(t *testing.T) {
	t.Parallel()
	f := &fakeCommands{}
	d := Driver{run: f.run}
	if err := d.start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(f.commands) != 1 || f.commands[0].name != "/usr/bin/open" ||
		!reflect.DeepEqual(f.commands[0].args, []string{"-b", bundleID}) {
		t.Fatalf("unexpected commands: %v", f.commands)
	}
	f.failName = "/usr/bin/open"
	if err := d.start(context.Background()); !errors.Is(err, ErrLaunch) || strings.Contains(err.Error(), "sensitive") {
		t.Fatalf("bad error: %v", err)
	}
}

func TestCanceledOperationsRunNoCommands(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	f := &fakeCommands{}
	d := Driver{LiveDir: "/Users/test/Library/Application Support/Claude", run: f.run}
	if err := d.stop(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("stop: %v", err)
	}
	if err := d.start(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("start: %v", err)
	}
	if len(f.commands) != 0 {
		t.Fatalf("commands ran after cancellation: %v", f.commands)
	}
}

type signalLog struct {
	signals []syscall.Signal
	pids    []int
	dieOn   syscall.Signal
	f       *fakeCommands
}

func (l *signalLog) kill(pid int, sig syscall.Signal) error {
	l.signals = append(l.signals, sig)
	l.pids = append(l.pids, pid)
	if l.dieOn != 0 && sig == l.dieOn {
		l.f.killed = true
	}
	return nil
}

func forceDriver(f *fakeCommands, log *signalLog) Driver {
	return Driver{LiveDir: f.live, run: f.run, kill: log.kill, stopTimeout: time.Second,
		pollInterval: time.Millisecond, forceGrace: 20 * time.Millisecond}
}

func TestStopNeverSignalsWithoutForce(t *testing.T) {
	t.Parallel()
	f := &fakeCommands{live: "/Users/test/Library/Application Support/Claude"}
	log := &signalLog{f: f, dieOn: syscall.SIGTERM}
	d := forceDriver(f, log)
	d.stopTimeout = 30 * time.Millisecond
	if err := d.stop(context.Background()); !errors.Is(err, ErrStillRunning) {
		t.Fatalf("err=%v", err)
	}
	if len(log.signals) != 0 {
		t.Fatalf("signaled without the user's consent: %v", log.signals)
	}
}

func TestForceStopEscalatesOnlyAsNeeded(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name  string
		dieOn syscall.Signal
		want  []syscall.Signal
	}{
		{"terminate suffices", syscall.SIGTERM, []syscall.Signal{syscall.SIGTERM}},
		{"needs kill", syscall.SIGKILL, []syscall.Signal{syscall.SIGTERM, syscall.SIGKILL}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			f := &fakeCommands{live: "/Users/test/Library/Application Support/Claude", hungQuit: true}
			log := &signalLog{f: f, dieOn: tt.dieOn}
			d := forceDriver(f, log)
			if err := d.stop(WithForceQuit(context.Background())); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(log.signals, tt.want) {
				t.Fatalf("signals=%v, want %v", log.signals, tt.want)
			}
			for _, pid := range log.pids {
				if pid != 42 {
					t.Fatalf("signaled unrelated pid %d", pid)
				}
			}
		})
	}
}

func TestForceStopSkipsSignalsWhenDesktopQuits(t *testing.T) {
	t.Parallel()
	f := &fakeCommands{live: "/Users/test/Library/Application Support/Claude", stopAfterQuit: true}
	log := &signalLog{f: f}
	d := forceDriver(f, log)
	if err := d.stop(WithForceQuit(context.Background())); err != nil {
		t.Fatal(err)
	}
	if len(log.signals) != 0 {
		t.Fatalf("a graceful quit must not be followed by signals: %v", log.signals)
	}
}

func TestForceStopReportsSurvivors(t *testing.T) {
	t.Parallel()
	f := &fakeCommands{live: "/Users/test/Library/Application Support/Claude"}
	log := &signalLog{f: f}
	d := forceDriver(f, log)
	err := d.stop(WithForceQuit(context.Background()))
	if !errors.Is(err, ErrStillRunning) || !strings.Contains(err.Error(), "Claude（PID\u00a042）") {
		t.Fatalf("err=%v", err)
	}
}
