package commandtool

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func newTestRunner(t *testing.T) *Runner {
	t.Helper()
	runner, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return runner
}

func TestRunnerCapturesShellOutputAndExitCode(t *testing.T) {
	result, err := newTestRunner(t).Run(t.Context(), Args{
		Command: "printf hello | tr 'a-z' 'A-Z'; printf warning >&2; exit 7",
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Stdout != "HELLO" || result.Stderr != "warning" || result.ExitCode != 7 {
		t.Fatalf("result = %#v", result)
	}
}

func TestRunnerResolvesWorkingDirectories(t *testing.T) {
	root := t.TempDir()
	child := filepath.Join(root, "child")
	if err := os.Mkdir(child, 0o755); err != nil {
		t.Fatal(err)
	}
	runner, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ cwd, want string }{
		{"", root}, {"child", child}, {child, child},
	} {
		result, err := runner.Run(t.Context(), Args{Command: "pwd", WorkingDirectory: tc.cwd})
		if err != nil {
			t.Fatalf("Run(%q): %v", tc.cwd, err)
		}
		if strings.TrimSpace(result.Stdout) != tc.want {
			t.Fatalf("pwd = %q, want %q", result.Stdout, tc.want)
		}
	}
}

func TestRunnerInheritsEnvironment(t *testing.T) {
	t.Setenv("ADKGO_COMMANDTOOL_TEST", "inherited")
	result, err := newTestRunner(t).Run(t.Context(), Args{Command: "printf %s \"$ADKGO_COMMANDTOOL_TEST\""})
	if err != nil || result.Stdout != "inherited" {
		t.Fatalf("result = %#v, err = %v", result, err)
	}
}

func TestRunnerRejectsInvalidInput(t *testing.T) {
	runner := newTestRunner(t)
	if _, err := runner.Run(t.Context(), Args{Command: "  "}); err == nil {
		t.Fatal("empty command succeeded")
	}
	if _, err := runner.Run(t.Context(), Args{Command: "pwd", WorkingDirectory: "missing"}); err == nil {
		t.Fatal("missing cwd succeeded")
	}
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := runner.Run(t.Context(), Args{Command: "pwd", WorkingDirectory: file}); err == nil {
		t.Fatal("file working directory succeeded")
	}
}

func TestRunnerTruncatesEachStream(t *testing.T) {
	result, err := newTestRunner(t).Run(t.Context(), Args{
		Command: "yes stdout | head -c 70000; yes stderr | head -c 70000 >&2",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.StdoutTruncated || !result.StderrTruncated {
		t.Fatalf("result = %#v", result)
	}
	if !strings.Contains(result.Stdout, "output truncated") || !strings.Contains(result.Stderr, "output truncated") {
		t.Fatal("truncated streams do not contain markers")
	}
}

func TestRunnerSupportsInstalledRuntimes(t *testing.T) {
	runner := newTestRunner(t)
	for _, tc := range []struct{ name, executable, command string }{
		{"bash", "bash", "bash -lc 'printf 42'"},
		{"python", "python3", "python3 -c 'print(6 * 7)'"},
		{"node", "node", "node -e 'console.log(6 * 7)'"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := exec.LookPath(tc.executable); err != nil {
				t.Skipf("%s is not installed", tc.executable)
			}
			result, err := runner.Run(t.Context(), Args{Command: tc.command})
			if err != nil || strings.TrimSpace(result.Stdout) != "42" {
				t.Fatalf("result = %#v, err = %v", result, err)
			}
		})
	}
}

func TestNewFallsBackFromInvalidShell(t *testing.T) {
	t.Setenv("SHELL", filepath.Join(t.TempDir(), "missing-shell"))
	runner, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if runner.shell != "/bin/sh" {
		t.Fatalf("shell = %q, want /bin/sh", runner.shell)
	}
}

func TestRunnerCancellationKillsProcessGroup(t *testing.T) {
	runner := newTestRunner(t)
	directory := t.TempDir()
	started := filepath.Join(directory, "started")
	leaked := filepath.Join(directory, "leaked")
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan Result, 1)
	errs := make(chan error, 1)
	go func() {
		result, err := runner.Run(ctx, Args{
			Command:          "touch started; sleep 2; touch leaked",
			WorkingDirectory: directory,
		})
		done <- result
		errs <- err
	}()

	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, err := os.Stat(started); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("command did not start")
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	result := <-done
	if err := <-errs; err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !result.Cancelled || result.ExitCode == 0 {
		t.Fatalf("result = %#v", result)
	}
	time.Sleep(100 * time.Millisecond)
	if _, err := os.Stat(leaked); !os.IsNotExist(err) {
		t.Fatalf("descendant survived: %v", err)
	}
}

func TestRunnerAlreadyCancelledDoesNotStart(t *testing.T) {
	directory := t.TempDir()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	result, err := newTestRunner(t).Run(ctx, Args{
		Command:          "touch marker",
		WorkingDirectory: directory,
	})
	if err != nil || !result.Cancelled || result.ExitCode != -1 {
		t.Fatalf("result = %#v, err = %v", result, err)
	}
	if _, err := os.Stat(filepath.Join(directory, "marker")); !os.IsNotExist(err) {
		t.Fatalf("command started: %v", err)
	}
}

func TestRunnerSupportsConcurrentCalls(t *testing.T) {
	runner := newTestRunner(t)
	var wait sync.WaitGroup
	for index := range 8 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			result, err := runner.Run(context.Background(), Args{Command: "printf concurrent"})
			if err != nil || result.Stdout != "concurrent" {
				t.Errorf("Run(%d) = %#v, %v", index, result, err)
			}
		}()
	}
	wait.Wait()
}
