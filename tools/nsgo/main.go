// Command nsgo preserves the upstream CLI and adds configured macOS signing to dev builds.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"syscall"
	"time"
)

const upstreamModule = "github.com/nsgo-dev/nsgo"
const upstreamCommand = upstreamModule + "/cmd/nsgo"

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:]); err != nil {
		var failure *exec.ExitError
		if errors.As(err, &failure) && failure.ExitCode() > 0 {
			os.Exit(failure.ExitCode())
		}
		fmt.Fprintln(os.Stderr, "nsgo:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string) error {
	development := runtime.GOOS == "darwin" && len(args) > 0 && args[0] == "dev"
	modfile, cleanup, err := prepareTool(ctx, development)
	if err != nil {
		return err
	}
	defer cleanup()
	goArgs := []string{"run", "-modfile=" + modfile, upstreamCommand}
	goArgs = append(goArgs, args...)
	command := exec.CommandContext(ctx, "go", goArgs...)
	command.Stdin, command.Stdout, command.Stderr = os.Stdin, os.Stdout, os.Stderr
	command.Cancel = func() error { return command.Process.Signal(os.Interrupt) }
	command.WaitDelay = 5 * time.Second
	err = command.Run()
	if ctx.Err() != nil {
		return nil
	}
	return err
}
