# Local CLI Execution Design

**Date:** 2026-07-13

## Goal

Add a general-purpose local command tool to the ADK Go demo. The agent can use it to run ordinary shell commands and any installed CLI runtime, including Bash, Zsh, Python, Node.js, npm/npx, and Go. This is the execution foundation for a later Agent Skills integration, where skills may reference scripts or one-off commands.

The first iteration intentionally runs commands without confirmation, sandboxing, allowlists, or project trust checks. It is intended only for local projects and commands the user trusts.

## Scope

The iteration will:

- register a local ADK function tool named `run_command`;
- execute a command through the user's current shell;
- inherit the application's environment;
- support relative and absolute working directories;
- capture stdout, stderr, exit status, duration, cancellation, and truncation metadata;
- run non-interactively without a PTY;
- integrate the tool with both the console and web launchers;
- document the unrestricted execution behavior;
- test shell behavior and installed Python and Node.js runtimes.

It will not yet:

- discover or activate Agent Skills;
- associate commands with a particular skill;
- request human confirmation;
- sandbox filesystem or network access;
- filter command names, flags, paths, or environment access;
- install missing runtimes or packages;
- provide an interactive terminal session.

## Architecture

Create an `internal/commandtool` package with two layers:

1. A command runner owns process creation, shell selection, working-directory resolution, output capture, cancellation, and result normalization.
2. A small ADK adapter exposes the runner through `functiontool.New` as `run_command`.

`main.go` creates the tool with the startup working directory as its project root and supplies it to `buildAgent`. The agent continues to expose `lookup_time` and all configured MCP toolsets. Console and web modes use the same agent, so no launcher-specific integration is required.

The command runner is independent of ADK types where practical. This keeps process behavior directly unit-testable and lets the later Skills runtime reuse it with a skill directory as the working directory.

## Tool Contract

### Input

```json
{
  "command": "python3 scripts/process.py --input data.json",
  "working_directory": "."
}
```

- `command` is required and must not be empty after trimming.
- `working_directory` is optional. An empty value uses the project root captured at startup.
- A relative working directory is resolved from the project root.
- An absolute working directory is used as provided.
- The directory must exist and must be a directory.

There is deliberately no structured `environment` input. Commands inherit the current process environment, and callers can use normal shell syntax such as `NAME=value command` when a one-off override is needed.

### Output

```json
{
  "stdout": "...",
  "stderr": "...",
  "exit_code": 0,
  "duration_ms": 123,
  "cancelled": false,
  "stdout_truncated": false,
  "stderr_truncated": false
}
```

A command that starts successfully but exits non-zero returns a normal tool result with its exit code and captured output. This gives the model the information needed to diagnose and retry. Process setup failures, such as an invalid working directory or an unavailable shell, return a tool error.

Cancellation returns the captured partial output, a non-zero exit code, and `cancelled: true`. It is treated as a result rather than hiding useful output behind an error.

## Shell and Runtime Behavior

The runner uses the absolute executable in `$SHELL` when it is present and executable. It falls back to `/bin/sh`. Commands are invoked with `-lc` so normal shell syntax, login-shell PATH setup, pipes, redirection, variable expansion, and command composition work as expected.

The tool does not implement Python, Node.js, Go, or Bash separately. Those are ordinary executables resolved by the shell. A requested runtime must already be installed and visible in PATH. This design automatically supports other installed CLIs without code changes.

The process has no PTY and no interactive stdin. Well-behaved agent scripts should accept input through flags, environment variables, or piped stdin and should not display password prompts or menus.

## Process Lifecycle

The command is tied to the ADK tool call context. Cancelling the request or stopping the launcher terminates the shell process and its process group so child processes are not left running.

No independent timeout is imposed in this iteration. Long-running commands continue until they exit or the invocation context is cancelled. This matches the requested unrestricted local mode while retaining an operator-controlled stop path through Ctrl-C or request cancellation.

The runner records elapsed wall-clock time in milliseconds.

## Output Handling

Stdout and stderr are captured separately. Each stream retains up to 64 KiB. When a stream exceeds that size, the result marks it as truncated and preserves a useful prefix and suffix rather than returning an unbounded payload to the model context.

Output limiting is an LLM-context safeguard, not a command restriction. It does not stop the process or limit what the process can write to files.

The application does not log full commands or their outputs, reducing accidental duplication of secrets in process logs. The command and result remain visible in the ADK conversation trace because they are tool arguments and tool results.

## Security Model

`run_command` is intentionally unrestricted:

- no confirmation is requested;
- shell metacharacters are supported by design;
- absolute paths and directories outside the project are allowed;
- inherited credentials and network access are available to child processes;
- commands may create, modify, or delete files;
- commands may install packages or start other processes.

The README must state that starting this agent grants the model the same effective command-line authority as the user account running it. Users are responsible for the model endpoint, prompts, repository content, and future Skills they choose to load.

## Error Handling

- Empty commands are rejected before process creation.
- Missing or non-directory working directories produce descriptive errors without invoking the shell.
- Shell lookup failures identify the selected shell path without printing environment contents.
- Non-zero command exits remain structured results.
- Cancellation kills the process group and returns partial output.
- Output truncation is explicitly reported.
- A failure to construct `run_command` prevents agent startup, because silently starting without a requested execution capability would be misleading.

## Testing

Unit tests will cover:

- default and relative working-directory resolution;
- absolute working directories;
- stdout and stderr capture;
- non-zero exit codes;
- inherited environment variables;
- shell features such as pipes and redirection;
- empty commands and invalid working directories;
- context cancellation and child-process cleanup;
- stdout and stderr truncation;
- ADK tool name, description, schema, and invocation;
- simultaneous command calls under the race detector.

Runtime smoke tests will run a tiny Python command when `python3` is installed and a tiny JavaScript command when `node` is installed. Missing optional runtimes cause those individual tests to skip rather than fail; the shell tests remain mandatory.

Repository validation remains:

```bash
go test ./...
go test -race ./...
go vet ./...
go build ./...
```

After automated validation, a live console smoke test will ask the model to run harmless version or print commands and verify that tool execution returns to the conversation without confirmation.

## Future Skills Integration

The following iteration will use the official ADK Go `skilltoolset` for discovery and progressive disclosure. When a loaded skill references a script, the model can call `run_command` with that skill's directory as `working_directory`. The Skills iteration may add a narrower convenience wrapper, but it will reuse this runner rather than introduce another process-execution implementation.
