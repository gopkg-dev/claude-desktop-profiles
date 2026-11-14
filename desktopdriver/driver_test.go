package desktopdriver

import (
	"context"
	"errors"
	"fmt"
	"os"
	"reflect"
	"strings"
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
}

func (f *fakeCommands) run(ctx context.Context, name string, args ...string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	f.commands = append(f.commands, commandCall{name, append([]string(nil), args...)})
	if name == f.failName {
		return []byte("partial potentially sensitive output"), errors.New("sensitive command failure")
	}
	running := !(f.stopAfterQuit && f.quit)
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
