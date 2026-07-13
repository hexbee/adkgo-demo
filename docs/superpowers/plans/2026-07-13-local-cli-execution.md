# Local CLI Execution Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. Do not use subagents for this project. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add an unrestricted local `run_command` ADK tool that executes shell syntax and any installed CLI runtime, returns structured output, and becomes the execution foundation for Agent Skills.

**Architecture:** `internal/commandtool` separates bounded output capture and OS process execution from the ADK `functiontool` adapter. `main.go` creates one runner rooted at the startup working directory, registers it beside `lookup_time`, and leaves MCP toolsets unchanged.

**Tech Stack:** Go 1.25+, `os/exec`, Unix process groups via `syscall`, `google.golang.org/adk/v2/tool/functiontool`, existing console/web launchers.

## Global Constraints

- Run commands immediately without confirmation, sandboxing, allowlists, path restrictions, or project trust checks.
- Use executable absolute `$SHELL` when valid; otherwise use `/bin/sh`; invoke it with `-lc`.
- Default the working directory to the startup project root; resolve relative paths from it; allow absolute paths.
- Inherit the application environment; do not provide an interactive stdin or PTY.
- Capture stdout and stderr separately, retaining 64 KiB of source data per stream as a prefix and suffix.
- Return non-zero exits as tool results rather than tool errors.
- On context cancellation, kill the shell process group and return partial output with `cancelled: true`.
- Never log commands, outputs, `.env`, `.mcp.json`, or inherited environment values.
- Do not stage or commit the user's `.agents/` directory.
- Execute inline; the user explicitly prohibited subagents.

---

## File Map

- Create `internal/commandtool/output.go`: bounded prefix/suffix writer.
- Create `internal/commandtool/output_test.go`: retention and truncation tests.
- Create `internal/commandtool/runner.go`: command input/result, shell selection, cwd resolution, execution, and cancellation.
- Create `internal/commandtool/runner_test.go`: shell, cwd, environment, runtime, cancellation, and concurrency tests.
- Create `internal/commandtool/tool.go`: ADK adapter.
- Create `internal/commandtool/tool_test.go`: metadata and invocation tests.
- Modify `main.go` and `main_test.go`: register the tool.
- Modify `README.md`: usage and security warning.

### Task 1: Bounded Output Capture

**Files:**
- Create: `internal/commandtool/output.go`
- Create: `internal/commandtool/output_test.go`

**Interfaces:**
- Produces: `newHeadTailBuffer(limit int) *headTailBuffer`
- Produces: `Write`, `String`, and `Truncated` methods used by Task 2

- [ ] **Step 1: Write the failing tests**

Create `internal/commandtool/output_test.go`:

```go
package commandtool

import (
	"strings"
	"testing"
)

func TestHeadTailBufferKeepsSmallOutput(t *testing.T) {
	buffer := newHeadTailBuffer(16)
	_, _ = buffer.Write([]byte("hello world"))
	if got := buffer.String(); got != "hello world" {
		t.Fatalf("String() = %q", got)
	}
	if buffer.Truncated() {
		t.Fatal("small output was truncated")
	}
}

func TestHeadTailBufferKeepsPrefixAndSuffix(t *testing.T) {
	buffer := newHeadTailBuffer(10)
	_, _ = buffer.Write([]byte("0123456789abcdefghij"))
	got := buffer.String()
	if !strings.HasPrefix(got, "01234") || !strings.HasSuffix(got, "fghij") {
		t.Fatalf("String() = %q", got)
	}
	if !strings.Contains(got, "output truncated") || !buffer.Truncated() {
		t.Fatalf("missing truncation metadata: %q", got)
	}
}

func TestHeadTailBufferKeepsLatestSuffixAcrossWrites(t *testing.T) {
	buffer := newHeadTailBuffer(8)
	for _, part := range []string{"abcd", "efgh", "ijkl"} {
		_, _ = buffer.Write([]byte(part))
	}
	got := buffer.String()
	if !strings.HasPrefix(got, "abcd") || !strings.HasSuffix(got, "ijkl") {
		t.Fatalf("String() = %q", got)
	}
}
```

- [ ] **Step 2: Verify the tests fail**

Run: `go test ./internal/commandtool -run HeadTailBuffer -count=1`

Expected: FAIL because `newHeadTailBuffer` is undefined.

- [ ] **Step 3: Implement the bounded writer**

Create `internal/commandtool/output.go`:

```go
package commandtool

import "strings"

const truncationMarker = "\n... output truncated ...\n"

type headTailBuffer struct {
	limit int
	head  []byte
	tail  []byte
	total int64
}

func newHeadTailBuffer(limit int) *headTailBuffer {
	if limit < 2 {
		limit = 2
	}
	return &headTailBuffer{limit: limit}
}

func (b *headTailBuffer) Write(p []byte) (int, error) {
	written := len(p)
	b.total += int64(written)
	headLimit := b.limit / 2
	if len(b.head) < headLimit {
		count := min(headLimit-len(b.head), len(p))
		b.head = append(b.head, p[:count]...)
		p = p[count:]
	}
	tailLimit := b.limit - headLimit
	if len(p) >= tailLimit {
		b.tail = append(b.tail[:0], p[len(p)-tailLimit:]...)
		return written, nil
	}
	if overflow := len(b.tail) + len(p) - tailLimit; overflow > 0 {
		copy(b.tail, b.tail[overflow:])
		b.tail = b.tail[:len(b.tail)-overflow]
	}
	b.tail = append(b.tail, p...)
	return written, nil
}

func (b *headTailBuffer) Truncated() bool {
	return b.total > int64(len(b.head)+len(b.tail))
}

func (b *headTailBuffer) String() string {
	var result strings.Builder
	result.Write(b.head)
	if b.Truncated() {
		result.WriteString(truncationMarker)
	}
	result.Write(b.tail)
	return result.String()
}
```

- [ ] **Step 4: Verify and commit**

Run: `gofmt -w internal/commandtool/output*.go && go test ./internal/commandtool -run HeadTailBuffer -count=1`

Expected: PASS.

```bash
git add internal/commandtool/output.go internal/commandtool/output_test.go
git commit -m "feat: add bounded command output capture"
```

### Task 2: Shell Runner

**Files:**
- Create: `internal/commandtool/runner.go`
- Create: `internal/commandtool/runner_test.go`

**Interfaces:**
- Consumes: `newHeadTailBuffer` from Task 1
- Produces: `Args`, `Result`, `Runner`, `New(projectRoot string)`, and `(*Runner).Run`

- [ ] **Step 1: Write failing core behavior tests**

Create `internal/commandtool/runner_test.go`:

```go
package commandtool

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
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
}

func TestRunnerSupportsInstalledPythonAndNode(t *testing.T) {
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
```

- [ ] **Step 2: Verify failure**

Run: `go test ./internal/commandtool -run Runner -count=1`

Expected: FAIL because runner types are undefined.

- [ ] **Step 3: Implement the runner**

Create `internal/commandtool/runner.go`:

```go
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
	Command string `json:"command" jsonschema:"Shell command to execute, including normal shell syntax and installed CLI runtimes."`
	WorkingDirectory string `json:"working_directory,omitempty" jsonschema:"Optional absolute path or path relative to the project root."`
}

type Result struct {
	Stdout string `json:"stdout"`
	Stderr string `json:"stderr"`
	ExitCode int `json:"exit_code"`
	DurationMS int64 `json:"duration_ms"`
	Cancelled bool `json:"cancelled"`
	StdoutTruncated bool `json:"stdout_truncated"`
	StderrTruncated bool `json:"stderr_truncated"`
}

type Runner struct { projectRoot, shell string }

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
	stdout, stderr := newHeadTailBuffer(maxCapturedBytes), newHeadTailBuffer(maxCapturedBytes)
	command := exec.Command(r.shell, "-lc", args.Command)
	command.Dir, command.Stdout, command.Stderr = directory, stdout, stderr
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
		Stdout: stdout.String(), Stderr: stderr.String(), ExitCode: exitCode,
		DurationMS: time.Since(started).Milliseconds(), Cancelled: cancelled,
		StdoutTruncated: stdout.Truncated(), StderrTruncated: stderr.Truncated(),
	}, nil
}
```

- [ ] **Step 4: Verify and commit**

Run: `gofmt -w internal/commandtool/runner*.go && go test ./internal/commandtool -run Runner -count=1`

Expected: PASS; Python/Node skip only when absent.

```bash
git add internal/commandtool/runner.go internal/commandtool/runner_test.go
git commit -m "feat: add local shell command runner"
```

### Task 3: Cancellation and Concurrency Coverage

**Files:**
- Modify: `internal/commandtool/runner_test.go`
- Verify: `internal/commandtool/runner.go`

**Interfaces:**
- Consumes: `(*Runner).Run(context.Context, Args)` from Task 2
- Produces: verified descendant cleanup and concurrent safety; no new public API

- [ ] **Step 1: Add failing lifecycle tests**

Append to `internal/commandtool/runner_test.go`:

```go
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
			Command: "touch started; sleep 2; touch leaked", WorkingDirectory: directory,
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
		Command: "touch marker", WorkingDirectory: directory,
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
```

Add `context`, `sync`, and `time` to the existing test import block for these tests.

- [ ] **Step 2: Verify cancellation and race behavior**

Run: `go test ./internal/commandtool -run 'Cancellation|Cancelled|Concurrent' -count=1`

Expected: PASS. If the descendant marker appears, correct only the `Setpgid` and negative-PID kill behavior in `Runner.Run`.

Run: `go test -race ./internal/commandtool -count=1`

Expected: PASS without race reports.

- [ ] **Step 3: Commit lifecycle coverage**

```bash
git add internal/commandtool/runner.go internal/commandtool/runner_test.go
git commit -m "test: verify command cancellation cleanup"
```

### Task 4: ADK Tool and Agent Integration

**Files:**
- Create: `internal/commandtool/tool.go`
- Create: `internal/commandtool/tool_test.go`
- Modify: `main.go`
- Modify: `main_test.go`

**Interfaces:**
- Consumes: `Runner` from Task 2
- Produces: `NewTool(runner *Runner) (tool.Tool, error)`
- Changes: `buildAgent(llm model.LLM, commandTool tool.Tool, toolsets []tool.Toolset)`

- [ ] **Step 1: Write failing ADK adapter tests**

Create `internal/commandtool/tool_test.go`:

```go
package commandtool

import (
	"context"
	"testing"
	"time"

	"google.golang.org/adk/v2/agent"
)

type runnableTool interface {
	Run(agent.Context, any) (map[string]any, error)
}

type testToolContext struct {
	agent.ContextMock
	context.Context
}

func (c *testToolContext) Deadline() (time.Time, bool) { return c.Context.Deadline() }
func (c *testToolContext) Done() <-chan struct{}       { return c.Context.Done() }
func (c *testToolContext) Err() error                  { return c.Context.Err() }
func (c *testToolContext) Value(key any) any           { return c.Context.Value(key) }

func TestNewToolMetadataAndInvocation(t *testing.T) {
	commandTool, err := NewTool(newTestRunner(t))
	if err != nil {
		t.Fatalf("NewTool: %v", err)
	}
	if commandTool.Name() != "run_command" || commandTool.IsLongRunning() {
		t.Fatalf("metadata = %q, %v", commandTool.Name(), commandTool.IsLongRunning())
	}
	runnable, ok := commandTool.(runnableTool)
	if !ok {
		t.Fatalf("tool %T is not runnable", commandTool)
	}
	result, err := runnable.Run(&testToolContext{Context: t.Context()}, map[string]any{
		"command": "printf tool-ok",
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result["stdout"] != "tool-ok" || result["exit_code"] != 0 {
		t.Fatalf("result = %#v", result)
	}
}

func TestNewToolRejectsNilRunner(t *testing.T) {
	if _, err := NewTool(nil); err == nil {
		t.Fatal("NewTool(nil) succeeded")
	}
}
```

- [ ] **Step 2: Verify failure**

Run: `go test ./internal/commandtool -run NewTool -count=1`

Expected: FAIL because `NewTool` is undefined.

- [ ] **Step 3: Implement the ADK adapter**

Create `internal/commandtool/tool.go`:

```go
package commandtool

import (
	"fmt"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/tool"
	"google.golang.org/adk/v2/tool/functiontool"
)

func NewTool(runner *Runner) (tool.Tool, error) {
	if runner == nil {
		return nil, fmt.Errorf("runner must not be nil")
	}
	return functiontool.New(functiontool.Config{
		Name: "run_command",
		Description: "Runs an unrestricted non-interactive command through the local shell. " +
			"Supports shell syntax and installed CLIs such as Bash, Python, Node.js, npm/npx, and Go. " +
			"Returns stdout, stderr, exit code, duration, cancellation, and truncation metadata.",
		RequireConfirmation: false,
	}, func(ctx agent.Context, args Args) (Result, error) {
		return runner.Run(ctx, args)
	})
}
```

- [ ] **Step 4: Integrate into `main.go`**

Add import `github.com/hexbee/adkgo-demo/internal/commandtool`.

Replace `buildAgent` with:

```go
func buildAgent(llm model.LLM, commandTool tool.Tool, toolsets []tool.Toolset) (agent.Agent, error) {
	timeTool, err := functiontool.New(functiontool.Config{
		Name: "lookup_time", Description: "Returns the current time in an IANA timezone.",
	}, lookupTime)
	if err != nil {
		return nil, fmt.Errorf("create time tool: %w", err)
	}
	return llmagent.New(llmagent.Config{
		Name:        "openai_compatible_assistant",
		Description: "An assistant backed by an OpenAI-compatible endpoint.",
		Instruction: "Be concise and helpful. Use lookup_time for timezone questions. Use run_command for local shell or CLI tasks. Commands run immediately without confirmation or sandboxing. Use available MCP tools when relevant; every remote MCP action requires explicit user confirmation before execution.",
		Model:       llm,
		Tools:       []tool.Tool{timeTool, commandTool},
		Toolsets:    toolsets,
	})
}
```

After model construction and before MCP loading, add:

```go
	projectRoot, err := os.Getwd()
	if err != nil {
		log.Fatalf("resolve project root: %v", err)
	}
	commandRunner, err := commandtool.New(projectRoot)
	if err != nil {
		log.Fatalf("CLI setup error: %v", err)
	}
	commandTool, err := commandtool.NewTool(commandRunner)
	if err != nil {
		log.Fatalf("create CLI tool: %v", err)
	}
```

Call `buildAgent(adaptedModel, commandTool, mcpToolsets)`.

- [ ] **Step 5: Update `main_test.go`**

Add:

```go
type stubTool struct{}

func (stubTool) Name() string        { return "run_command" }
func (stubTool) Description() string { return "test command tool" }
func (stubTool) IsLongRunning() bool { return false }
```

Rename the existing test to `TestBuildAgentAcceptsLocalToolAndMCPToolsets` and call:

```go
if _, err := buildAgent(fakeModel{}, stubTool{}, toolsets); err != nil {
	t.Fatalf("buildAgent(%d toolsets): %v", len(toolsets), err)
}
```

- [ ] **Step 6: Verify and commit**

Run: `gofmt -w internal/commandtool/tool*.go main.go main_test.go`

Run: `go test ./internal/commandtool ./... -count=1`

Expected: PASS.

```bash
git add internal/commandtool/tool.go internal/commandtool/tool_test.go main.go main_test.go
git commit -m "feat: expose unrestricted CLI tool to agent"
```

### Task 5: Documentation and End-to-End Verification

**Files:**
- Modify: `README.md`
- Verify: all packages and the real console

**Interfaces:**
- Consumes: complete `run_command` tool
- Produces: documented usage and verified repository state

- [ ] **Step 1: Document usage and authority**

Add `本地 shell/CLI 执行与结构化结果` to `## 已支持`, then insert before `## 验证`:

````markdown
## 本地 Shell / CLI

Agent 内置 `run_command`，通过当前 `$SHELL` 执行非交互命令。它支持管道、重定向和环境变量，也可以调用 PATH 中已经安装的 Bash、Python、Node.js、npm/npx、Go 及其他 CLI。

可以尝试：

```text
请调用 run_command，执行 printf 'hello from shell'。
请调用 run_command，执行 python3 -c 'print(6 * 7)'。
请调用 run_command，分别显示 node 和 go 的版本。
```

`working_directory` 为空时使用程序启动目录；相对路径从该目录解析，也允许绝对路径。命令不提供 TTY 或密码输入界面。Python、Node.js 等运行时必须已安装并位于 PATH 中。

> **安全警告：** `run_command` 不要求人工确认，不使用沙箱或白名单，并继承当前进程的文件、网络和环境权限。模型可以修改或删除文件、安装依赖、访问网络或启动其他进程。只应在你信任模型服务、提示内容和项目文件时运行本示例。

stdout 和 stderr 分开返回；非零退出码不会被隐藏。每个输出流最多保留 64 KiB 的开头和结尾。Ctrl-C 或请求取消会终止命令及其子进程。
````

- [ ] **Step 2: Run full automated validation**

Run each command separately:

```bash
gofmt -w internal/commandtool/*.go main.go main_test.go
git diff --check
go test ./...
go test -race ./...
go vet ./...
go build ./...
```

Expected: every command exits 0.

- [ ] **Step 3: Run the live console smoke test**

Run: `go run . console`

Enter:

```text
请调用 run_command 执行以下无副作用命令：printf 'shell-ok'; python3 -c 'print("python-ok")'; node -e 'console.log("node-ok")'。返回输出，不要调用 MCP。
```

Expected: `run_command` executes without confirmation, exit code is 0, output contains `shell-ok`, `python-ok`, and `node-ok`, and no MCP tool runs. Stop the console with Ctrl-C after the response.

- [ ] **Step 4: Inspect scope and commit documentation**

Run:

```bash
git diff --check
git status --short --ignored
git diff -- README.md
```

Expected: `.agents/` is untracked and not staged; `.env` and `.mcp.json` remain ignored.

```bash
git add README.md
git commit -m "docs: document local CLI execution"
```

- [ ] **Step 5: Record final evidence**

Run: `git status --short --ignored && git log --oneline -6`

Expected: no tracked changes; local secret files remain ignored; `.agents/` remains untracked for the Skills iteration.
