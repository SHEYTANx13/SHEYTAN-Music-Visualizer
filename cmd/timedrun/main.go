// Command timedrun runs a child process under a hard wall-clock
// timeout and propagates the child's REAL exit code. It exists because
// GNU coreutils `timeout` is not installed on macOS hosted runners:
// a portable, application-owned bound that cannot hang a CI job and
// cannot convert a failure into success.
//
// Usage:
//
//	timedrun -timeout 120s -- <command> [args...]
//
// Exit codes:
//
//	child exit code            - the child ran and exited (its code,
//	                             including 130 for SIGINT etc.)
//	99                         - the child was killed at the timeout
//	2                          - usage error
//	127                        - the child could not be started
//
// The timeout kill is SIGKILL after one SIGTERM grace second on Unix;
// on Windows the process tree is terminated via taskkill. No evidence
// is fabricated: stdout/stderr pass through untouched.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"time"
)

func main() {
	if rc := run(os.Args[1:]); rc != 0 {
		os.Exit(rc)
	}
}

func run(args []string) int {
	fs := flag.NewFlagSet("timedrun", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	timeout := fs.Duration("timeout", 120*time.Second, "hard wall-clock limit for the child")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	rest := fs.Args()
	if len(rest) == 0 {
		fmt.Fprintln(os.Stderr, "timedrun: usage: timedrun -timeout 120s -- <command> [args...]")
		return 2
	}
	if *timeout <= 0 {
		fmt.Fprintln(os.Stderr, "timedrun: -timeout must be positive")
		return 2
	}
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, rest[0], rest[1:]...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	if err := cmd.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "timedrun: start %q: %v\n", rest[0], err)
		return 127
	}
	err := cmd.Wait()
	if err == nil {
		return 0
	}
	if ctx.Err() == context.DeadlineExceeded {
		// Best-effort tree kill for child processes that ignore the
		// context kill on Windows.
		if runtime.GOOS == "windows" {
			_ = exec.Command("taskkill", "/F", "/T", "/PID", fmt.Sprint(cmd.Process.Pid)).Run()
		}
		fmt.Fprintf(os.Stderr, "timedrun: %q exceeded the %s limit (exit 99)\n", rest[0], *timeout)
		return 99
	}
	if ee, ok := err.(*exec.ExitError); ok {
		return ee.ExitCode()
	}
	fmt.Fprintf(os.Stderr, "timedrun: wait %q: %v\n", rest[0], err)
	return 127
}
