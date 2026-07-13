# Project Agent Skills Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. Do not use subagents for this project. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Load project-local Agent Skills from `.agents/skills/` with ADK Go v2's official Skill source and Toolset, while reusing the existing unrestricted `run_command` tool for skill scripts.

**Architecture:** A small `internal/skillsruntime` package owns project-path discovery, complete startup preload, validation, and official Skill Toolset construction. `main.go` combines its optional Toolset with the existing MCP toolsets and adds only the project-specific script working-directory guidance that ADK's built-in Skill instruction does not know.

**Tech Stack:** Go 1.25+, `google.golang.org/adk/v2@v2.0.0`, `tool/skilltoolset`, `tool/skilltoolset/skill`, existing `internal/commandtool` and launchers.

## Global Constraints

- Discover only `<startup-project-root>/.agents/skills/`; do not read `~/.agents/skills/` in this iteration.
- Use the official sequence `skill.NewFileSystemSource` → `skill.WithCompletePreloadSource` → `skilltoolset.New`.
- Do not implement custom frontmatter parsing, discovery, instruction loading, resource loading, or Skill tools.
- Missing `.agents/skills/` is non-fatal; an existing empty directory registers no empty Toolset.
- A non-directory Skills path, invalid Skill, or preload failure fails startup with a safe project-relative error.
- Preload once at startup; changes require a restart.
- Keep ADK's progressive prompt disclosure: catalog first, `load_skill` for instructions, and `load_skill_resource` for supported resources.
- Reuse unrestricted `run_command` for scripts and CLI commands. Add no confirmation, sandbox, allowlist, trust prompt, or `allowed-tools` enforcement.
- Do not change the existing MCP confirmation behavior.
- Do not read `skills-lock.json` or interpret `agents/openai.yaml` at runtime.
- Never log Skill instructions, resource contents, `.env`, `.mcp.json`, credentials, or model exchanges.
- Execute inline; the user explicitly prohibited subagents.

---

## File Map

- Create `internal/skillsruntime/runtime.go`: discovery, preload, count, and optional official Toolset construction.
- Create `internal/skillsruntime/runtime_test.go`: discovery, validation, official behavior, resources, and concurrency tests.
- Modify `main.go`: build and append the project Skill Toolset, log safe startup status, and add script execution guidance.
- Modify `main_test.go`: verify local tools, MCP, and Skills Toolsets coexist.
- Modify `README.md`: project Skills layout, progressive disclosure, scripts, examples, restart rule, and trust warning.

### Task 1: Project Skills Runtime Discovery and Validation

**Files:**
- Create: `internal/skillsruntime/runtime.go`
- Create: `internal/skillsruntime/runtime_test.go`

**Interfaces:**
- Produces: `RelativeRoot`, `Result`, and `Build(ctx, projectRoot)`
- Consumes: official `skill.Source` and `skilltoolset.SkillToolset`

- [x] **Step 1: Write failing discovery tests**

Create `internal/skillsruntime/runtime_test.go` with filesystem helpers and the first four cases:

```go
package skillsruntime

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeSkill(t *testing.T, root, name, description, body string) string {
	t.Helper()
	dir := filepath.Join(root, RelativeRoot, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "---\nname: " + name + "\ndescription: " + description + "\n---\n\n" + body + "\n"
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestBuildMissingDirectory(t *testing.T) {
	result, err := Build(t.Context(), t.TempDir())
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if result.Found || result.Count != 0 || result.Root != "" || result.Toolset != nil {
		t.Fatalf("result = %#v", result)
	}
}

func TestBuildEmptyDirectory(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, RelativeRoot), 0o755); err != nil {
		t.Fatal(err)
	}
	result, err := Build(t.Context(), root)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if !result.Found || result.Count != 0 || result.Toolset != nil {
		t.Fatalf("result = %#v", result)
	}
	wantRoot, _ := filepath.Abs(filepath.Join(root, RelativeRoot))
	if result.Root != wantRoot {
		t.Fatalf("Root = %q, want %q", result.Root, wantRoot)
	}
}

func TestBuildRejectsRegularFileAtSkillsPath(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, RelativeRoot)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("not a directory"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Build(t.Context(), root); err == nil || !strings.Contains(err.Error(), RelativeRoot) {
		t.Fatalf("Build error = %v", err)
	}
}

func TestBuildValidSkills(t *testing.T) {
	root := t.TempDir()
	writeSkill(t, root, "alpha-skill", "Alpha description", "Alpha instructions.")
	writeSkill(t, root, "beta-skill", "Beta description", "Beta instructions.")
	result, err := Build(t.Context(), root)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if !result.Found || result.Count != 2 || result.Toolset == nil {
		t.Fatalf("result = %#v", result)
	}
}
```

- [x] **Step 2: Verify the tests fail**

Run: `go test ./internal/skillsruntime -run 'Build(Missing|Empty|Rejects|Valid)' -count=1`

Expected: FAIL because `RelativeRoot` and `Build` are undefined.

- [x] **Step 3: Implement the official runtime construction path**

Create `internal/skillsruntime/runtime.go`:

```go
// Package skillsruntime constructs the project-local ADK Agent Skills Toolset.
package skillsruntime

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"google.golang.org/adk/v2/tool"
	"google.golang.org/adk/v2/tool/skilltoolset"
	"google.golang.org/adk/v2/tool/skilltoolset/skill"
)

const RelativeRoot = ".agents/skills"

type Result struct {
	Found   bool
	Count   int
	Root    string
	Toolset tool.Toolset
}

func Build(ctx context.Context, projectRoot string) (Result, error) {
	root, err := filepath.Abs(filepath.Join(projectRoot, RelativeRoot))
	if err != nil {
		return Result{}, fmt.Errorf("resolve %s: %w", RelativeRoot, err)
	}
	info, err := os.Stat(root)
	if errors.Is(err, os.ErrNotExist) {
		return Result{}, nil
	}
	if err != nil {
		return Result{}, fmt.Errorf("inspect %s: %w", RelativeRoot, err)
	}
	if !info.IsDir() {
		return Result{}, fmt.Errorf("%s is not a directory", RelativeRoot)
	}

	source := skill.NewFileSystemSource(os.DirFS(root))
	preloaded, _, err := skill.WithCompletePreloadSource(ctx, source)
	if err != nil {
		return Result{}, fmt.Errorf("preload %s: %w", RelativeRoot, err)
	}
	frontmatters, err := preloaded.ListFrontmatters(ctx)
	if err != nil {
		return Result{}, fmt.Errorf("list %s: %w", RelativeRoot, err)
	}
	result := Result{Found: true, Count: len(frontmatters), Root: root}
	if result.Count == 0 {
		return result, nil
	}
	toolset, err := skilltoolset.New(ctx, skilltoolset.Config{Source: preloaded})
	if err != nil {
		return Result{}, fmt.Errorf("create toolset for %s: %w", RelativeRoot, err)
	}
	result.Toolset = toolset
	return result, nil
}
```

- [x] **Step 4: Add strict validation tests**

Append tests that deliberately write invalid `SKILL.md` files:

```go
func TestBuildRejectsMalformedFrontmatter(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, RelativeRoot, "broken-skill")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("not frontmatter"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Build(t.Context(), root); err == nil || !strings.Contains(err.Error(), RelativeRoot) {
		t.Fatalf("Build error = %v", err)
	}
}

func TestBuildRejectsNameDirectoryMismatch(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, RelativeRoot, "directory-name")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "---\nname: another-name\ndescription: mismatch\n---\n\nInstructions.\n"
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Build(t.Context(), root); err == nil || !strings.Contains(err.Error(), RelativeRoot) {
		t.Fatalf("Build error = %v", err)
	}
}
```

- [x] **Step 5: Verify and commit**

Run: `gofmt -w internal/skillsruntime/*.go && go test ./internal/skillsruntime -count=1`

Expected: PASS.

```bash
git add internal/skillsruntime/runtime.go internal/skillsruntime/runtime_test.go
git commit -m "feat: load project Agent Skills"
```

### Task 2: Verify Official Progressive Disclosure and Resources

**Files:**
- Modify: `internal/skillsruntime/runtime_test.go`

**Interfaces:**
- Verifies: official tool names, request processing, loaded instructions, and resource content
- Verifies: immutable preloaded behavior under concurrent access

- [x] **Step 1: Add test-only ADK interfaces and context adapter**

Add imports for `context`, `sort`, `sync`, `time`, `google.golang.org/adk/v2/agent`, and `google.golang.org/adk/v2/model`, then add:

```go
type runnableTool interface {
	Run(agent.Context, any) (map[string]any, error)
}

type requestProcessor interface {
	ProcessRequest(agent.Context, *model.LLMRequest) error
}

type testToolContext struct {
	agent.ContextMock
	context.Context
}

func (c *testToolContext) Deadline() (time.Time, bool) { return c.Context.Deadline() }
func (c *testToolContext) Done() <-chan struct{}       { return c.Context.Done() }
func (c *testToolContext) Err() error                  { return c.Context.Err() }
func (c *testToolContext) Value(key any) any           { return c.Context.Value(key) }

func findRunnableTool(t *testing.T, result Result, name string) runnableTool {
	t.Helper()
	tools, err := result.Toolset.Tools(nil)
	if err != nil {
		t.Fatalf("Tools: %v", err)
	}
	for _, candidate := range tools {
		if candidate.Name() == name {
			runnable, ok := candidate.(runnableTool)
			if !ok {
				t.Fatalf("tool %q is not runnable", name)
			}
			return runnable
		}
	}
	t.Fatalf("tool %q not found", name)
	return nil
}
```

- [x] **Step 2: Add official tool and prompt-injection tests**

```go
func TestBuildExposesOfficialToolsAndCatalog(t *testing.T) {
	root := t.TempDir()
	writeSkill(t, root, "sample-skill", "Sample description", "Follow sample instructions.")
	result, err := Build(t.Context(), root)
	if err != nil {
		t.Fatal(err)
	}
	tools, err := result.Toolset.Tools(nil)
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(tools))
	for _, candidate := range tools {
		names = append(names, candidate.Name())
	}
	sort.Strings(names)
	want := []string{"list_skills", "load_skill", "load_skill_resource"}
	if strings.Join(names, ",") != strings.Join(want, ",") {
		t.Fatalf("tool names = %v, want %v", names, want)
	}

	processor, ok := result.Toolset.(requestProcessor)
	if !ok {
		t.Fatalf("toolset %T does not process requests", result.Toolset)
	}
	request := &model.LLMRequest{}
	if err := processor.ProcessRequest(nil, request); err != nil {
		t.Fatal(err)
	}
	if request.Config == nil || request.Config.SystemInstruction == nil {
		t.Fatal("Skills system instruction was not injected")
	}
	text := request.Config.SystemInstruction.Parts[0].Text
	for _, want := range []string{"SKILL.md", "<available_skills>", "sample-skill", "Sample description"} {
		if !strings.Contains(text, want) {
			t.Errorf("system instruction does not contain %q: %q", want, text)
		}
	}
}
```

- [x] **Step 3: Add instruction and supported-resource tests**

```go
func TestBuildPreloadsInstructionsAndResources(t *testing.T) {
	root := t.TempDir()
	dir := writeSkill(t, root, "resource-skill", "Uses resources", "Read the bundled files.")
	for path, content := range map[string]string{
		"references/guide.md": "reference content",
		"scripts/run.py":      "print('script content')",
	} {
		fullPath := filepath.Join(dir, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(fullPath, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	result, err := Build(t.Context(), root)
	if err != nil {
		t.Fatal(err)
	}
	toolContext := &testToolContext{Context: t.Context()}
	loaded, err := findRunnableTool(t, result, "load_skill").Run(toolContext, map[string]any{"name": "resource-skill"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(loaded["instructions"].(string), "Read the bundled files.") {
		t.Fatalf("load_skill result = %#v", loaded)
	}
	for path, want := range map[string]string{
		"references/guide.md": "reference content",
		"scripts/run.py":      "print('script content')",
	} {
		resource, err := findRunnableTool(t, result, "load_skill_resource").Run(toolContext, map[string]any{
			"skill_name": "resource-skill", "resource_path": path,
		})
		if err != nil {
			t.Fatal(err)
		}
		if resource["content"] != want {
			t.Fatalf("resource %q = %#v", path, resource)
		}
	}
}
```

- [x] **Step 4: Add preloaded snapshot and concurrency tests**

Build once, overwrite the original `SKILL.md`, then assert `load_skill` still returns the startup body. In a second test, start 20 goroutines that call `Toolset.Tools`, `ProcessRequest`, and `load_skill`; collect all returned errors and fail if any is non-nil. This proves the runtime uses the complete startup snapshot and remains safe under the race detector.

- [x] **Step 5: Verify and commit**

Run: `gofmt -w internal/skillsruntime/runtime_test.go && go test ./internal/skillsruntime -count=1 && go test -race ./internal/skillsruntime -count=1`

Expected: PASS, with no race report.

```bash
git add internal/skillsruntime/runtime_test.go
git commit -m "test: verify official Agent Skills behavior"
```

### Task 3: Wire Skills into the Shared Agent

**Files:**
- Modify: `main.go`
- Modify: `main_test.go`

**Interfaces:**
- Consumes: `skillsruntime.Build` and its optional `tool.Toolset`
- Preserves: existing local tools, MCP Toolsets, MCP confirmation, and both launchers

- [x] **Step 1: Clarify the agent-construction coexistence test**

Rename `TestBuildAgentAcceptsLocalToolAndMCPToolsets` to `TestBuildAgentAcceptsLocalToolAndCombinedToolsets`, and name the two stub Toolsets `mcp` and `skills`. The assertion remains that `buildAgent` accepts zero, one, and multiple Toolsets without replacing local tools.

- [x] **Step 2: Add the Skills package import and startup build**

Import `github.com/hexbee/adkgo-demo/internal/skillsruntime`. After MCP setup, build and append Skills without mutating the MCP result:

```go
	skillsResult, err := skillsruntime.Build(ctx, projectRoot)
	if err != nil {
		log.Fatalf("Skills setup error: %v", err)
	}
	toolsets := append([]tool.Toolset(nil), mcpToolsets...)
	if skillsResult.Toolset != nil {
		toolsets = append(toolsets, skillsResult.Toolset)
	}
	if !skillsResult.Found {
		log.Printf("no %s found; starting without project Skills", skillsruntime.RelativeRoot)
	} else {
		log.Printf("loaded %d project skill(s) from %s", skillsResult.Count, skillsruntime.RelativeRoot)
	}
```

Pass `toolsets`, rather than `mcpToolsets`, to `buildAgent`.

- [x] **Step 3: Add only project-specific Skill guidance**

Extend the root instruction without duplicating ADK's built-in Skill Toolset instruction:

```text
Use available project Skills when relevant and load their instructions before acting. When a loaded Skill references scripts or commands, use run_command with working_directory set to ".agents/skills/<skill-name>" so Skill-relative paths resolve correctly.
```

Keep the existing unrestricted-command warning and MCP confirmation sentence unchanged.

- [x] **Step 4: Verify and commit**

Run: `gofmt -w main.go main_test.go && go test . -count=1 && go test ./internal/skillsruntime -count=1`

Expected: PASS.

```bash
git add main.go main_test.go
git commit -m "feat: expose project Skills to the agent"
```

### Task 4: Documentation and End-to-End Validation

**Files:**
- Modify: `README.md`

- [ ] **Step 1: Document project Skills usage**

Add a section covering:

- automatic discovery from `<project>/.agents/skills/<skill-name>/SKILL.md`;
- project-level only in this version;
- ADK's three official tools and progressive disclosure;
- `references/`, `assets/`, and `scripts/` resource support;
- restart required after Skill changes;
- natural and explicit `$skill-name` example prompts;
- script execution through unrestricted `run_command` from the Skill directory;
- no local confirmation, sandbox, allowlist, or `allowed-tools` enforcement;
- `skills-lock.json` and `agents/openai.yaml` are not runtime inputs;
- MCP confirmation remains independent and unchanged.

- [ ] **Step 2: Run repository validation**

Run:

```bash
go test ./...
go test -race ./...
go vet ./...
go build ./...
```

Expected: every command exits 0 and the race detector reports no races.

- [ ] **Step 3: Run local startup checks without exposing credentials**

Start the console launcher using the existing `.env` and `.mcp.json`. Verify logs contain only safe summaries plus either:

```text
loaded 2 project skill(s) from .agents/skills
```

or the actual discovered count. Do not print configuration files, Authorization headers, full Skill bodies, or resource contents.

- [ ] **Step 4: Run live Skill smoke tests in order**

Use the console launcher and record only tool names and high-level outcomes:

1. Ask `请使用 concise-writer 精简这段文字：由于目前这个功能在现阶段仍然处于尚未完全完成的状态，因此我们暂时还不能立即发布。` Verify `load_skill` is called and the answer is concise.
2. Ask the agent to list available Skills. Verify `list_skills` is used and no MCP tool is called.
3. Ask `使用 $follow-builders-lite 生成一份中文 AI builders 摘要。` Verify `load_skill`, any needed `load_skill_resource`, and then `run_command` with `working_directory: ".agents/skills/follow-builders-lite"`. This last step may access the skill's documented public feed.

If the model chooses an unrelated MCP tool, decline its confirmation and refine the prompt; do not authorize a remote action merely to complete this smoke test.

- [ ] **Step 5: Commit documentation**

```bash
git add README.md
git commit -m "docs: document project Agent Skills"
```

## Completion Criteria

- Missing or empty project Skills directories do not prevent startup.
- Invalid project Skills fail startup before the launcher runs, with no sensitive content in the error.
- Valid skills are fully preloaded once and exposed through ADK's official `list_skills`, `load_skill`, and `load_skill_resource` tools.
- The initial model request contains only the Skill catalog and ADK usage guidance, not full Skill bodies.
- The existing `run_command` tool can execute a Skill script from the documented Skill working directory.
- Existing local tools and MCP Toolsets remain registered; MCP confirmation behavior is unchanged.
- Unit, race, vet, build, and live smoke checks all pass.
- README accurately states that only project-level Skills are supported and that local execution is unrestricted.
