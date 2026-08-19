// Package execx wraps exec.Command with argument-vector-only invocation.
// Peyk never builds shell strings from user input; every external call goes
// through this package with a fixed argv.
package execx

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// Verbose controls whether Run streams child output to the terminal.
var Verbose = true

// Run executes argv streaming stdout/stderr, returning an error on non-zero exit.
func Run(ctx context.Context, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	if Verbose {
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
	} else {
		var buf bytes.Buffer
		cmd.Stdout = &buf
		cmd.Stderr = &buf
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("%s %s: %w\n%s", name, strings.Join(args, " "), err, tail(buf.String(), 2000))
		}
		return nil
	}
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s %s: %w", name, strings.Join(args, " "), err)
	}
	return nil
}

// RunEnv is Run with extra environment variables appended (KEY=VALUE form).
func RunEnv(ctx context.Context, env []string, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = append(os.Environ(), env...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s %s: %w", name, strings.Join(args, " "), err)
	}
	return nil
}

// Output executes argv and returns trimmed stdout; stderr is included in errors.
func Output(ctx context.Context, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("%s %s: %w\n%s", name, strings.Join(args, " "), err, tail(errb.String(), 2000))
	}
	return strings.TrimSpace(out.String()), nil
}

// RunDir executes argv in dir, streaming output.
func RunDir(ctx context.Context, dir, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s %s (in %s): %w", name, strings.Join(args, " "), dir, err)
	}
	return nil
}

// Exists reports whether an executable is on PATH.
func Exists(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return "…" + s[len(s)-n:]
}
