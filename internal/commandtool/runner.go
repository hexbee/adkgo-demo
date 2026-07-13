package commandtool

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

const maxCapturedBytes = 64 * 1024

type Args struct {
	Command          string `json:"command" jsonschema:"Shell command to execute, including normal shell syntax and installed CLI runtimes."`
	WorkingDirectory string `json:"working_directory,omitempty" jsonschema:"Optional absolute path or path relative to the project root. Empty uses the project root."`
}

type Result struct {
	Stdout          string `json:"stdout"`
	Stderr          string `json:"stderr"`
	ExitCode        int    `json:"exit_code"`
	DurationMS      int64  `json:"duration_ms"`
	Cancelled       bool   `json:"cancelled"`
	StdoutTruncated bool   `json:"stdout_truncated"`
	StderrTruncated bool   `json:"stderr_truncated"`
}

type Runner struct {
	projectRoot string
	shell       string
}

func New(projectRoot string) (*Runner, error) {
	root, err := validDirectory(projectRoot)
	if err != nil {
		return nil, fmt.Errorf("project root: %w", err)
	}
	shell, err := selectShell()
	if err != nil {
		return nil, err
	}
	return &Runner{projectRoot: root, shell: shell}, nil
}

func selectShell() (string, error) {
	if candidate := os.Getenv("SHELL"); filepath.IsAbs(candidate) {
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() && info.Mode()&0o111 != 0 {
			return candidate, nil
		}
	}

	const fallback = "/bin/sh"
	info, err := os.Stat(fallback)
	if err != nil || info.IsDir() || info.Mode()&0o111 == 0 {
		return "", fmt.Errorf("no executable shell found at %q or %q", os.Getenv("SHELL"), fallback)
	}
	return fallback, nil
}

func validDirectory(directory string) (string, error) {
	absolute, err := filepath.Abs(directory)
	if err != nil {
		return "", fmt.Errorf("resolve %q: %w", directory, err)
	}
	info, err := os.Stat(absolute)
	if err != nil {
		return "", fmt.Errorf("stat %q: %w", absolute, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("%q is not a directory", absolute)
	}
	return filepath.Clean(absolute), nil
}

func (r *Runner) workingDirectory(value string) (string, error) {
	if value == "" {
		return r.projectRoot, nil
	}
	if !filepath.IsAbs(value) {
		value = filepath.Join(r.projectRoot, value)
	}
	return validDirectory(value)
}

func (r *Runner) Run(ctx context.Context, args Args) (Result, error) {
	if strings.TrimSpace(args.Command) == "" {
		return Result{}, fmt.Errorf("command must not be empty")
	}
	directory, err := r.workingDirectory(args.WorkingDirectory)
	if err != nil {
		return Result{}, fmt.Errorf("working directory: %w", err)
	}
	if ctx.Err() != nil {
		return Result{ExitCode: -1, Cancelled: true}, nil
	}

	stdout := newHeadTailBuffer(maxCapturedBytes)
	stderr := newHeadTailBuffer(maxCapturedBytes)
	command := exec.Command(r.shell, "-lc", args.Command)
	command.Dir = directory
	command.Stdout = stdout
	command.Stderr = stderr
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	started := time.Now()
	if err := command.Start(); err != nil {
		return Result{}, fmt.Errorf("start shell %q: %w", r.shell, err)
	}
	wait := make(chan error, 1)
	go func() { wait <- command.Wait() }()

	cancelled := false
	var waitErr error
	select {
	case waitErr = <-wait:
	case <-ctx.Done():
		cancelled = true
		_ = syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
		waitErr = <-wait
	}

	exitCode := -1
	if command.ProcessState != nil {
		exitCode = command.ProcessState.ExitCode()
	}
	var exitErr *exec.ExitError
	if waitErr != nil && !errors.As(waitErr, &exitErr) && !cancelled {
		return Result{}, fmt.Errorf("wait for command: %w", waitErr)
	}

	return Result{
		Stdout:          stdout.String(),
		Stderr:          stderr.String(),
		ExitCode:        exitCode,
		DurationMS:      time.Since(started).Milliseconds(),
		Cancelled:       cancelled,
		StdoutTruncated: stdout.Truncated(),
		StderrTruncated: stderr.Truncated(),
	}, nil
}
