# Project Agent Skills Design

**Date:** 2026-07-13

## Goal

Add first-class Agent Skills support to the ADK Go demo by loading skills from the current project's `.agents/skills/` directory. Use the official ADK Go v2 Skills implementation for discovery, validation, progressive disclosure, instruction loading, and resource loading. Reuse the existing unrestricted `run_command` tool when a skill asks the agent to execute a bundled script or CLI command.

This iteration intentionally supports project-level skills only. User-level `~/.agents/skills/`, multi-source merging, and collision precedence are deferred.

## User Experience

Starting either launcher from the project root automatically discovers valid immediate subdirectories of `.agents/skills/`:

```text
.agents/skills/
├── concise-writer/
│   └── SKILL.md
└── follow-builders-lite/
    ├── SKILL.md
    ├── agents/
    └── scripts/
```

The model initially sees only each skill's `name` and `description`. When a request matches a skill, the model calls ADK's `load_skill` tool before following the full `SKILL.md` instructions. It calls `load_skill_resource` only when instructions require a file from `references/`, `assets/`, or `scripts/`.

Users may trigger a skill naturally from its description or mention it explicitly, for example:

```text
请使用 concise-writer 精简这段文字：……
使用 $follow-builders-lite 生成一份中文 AI builders 摘要。
```

## Official ADK Architecture

Create an `internal/skillsruntime` package that performs the official construction sequence:

1. Resolve `<project>/.agents/skills` from the startup working directory.
2. If the directory does not exist, return a non-error result with no toolset.
3. Build `skill.NewFileSystemSource(os.DirFS(skillsRoot))`.
4. Wrap it with `skill.WithCompletePreloadSource(ctx, source)`.
5. Build `skilltoolset.New(ctx, skilltoolset.Config{Source: source})`.
6. Return the toolset, discovered skill count, and whether the directory was found.

No custom frontmatter parser, skill catalog, load tool, or resource reader will be implemented. ADK remains responsible for the Agent Skills format and future compatibility.

`main.go` appends the Skills Toolset to the existing MCP toolsets before constructing the LLM agent. The resulting agent continues to expose:

- local `lookup_time`;
- local unrestricted `run_command`;
- confirmation-protected remote MCP toolsets;
- the official project Skills Toolset.

Console and web launchers share the same agent and therefore the same Skills behavior.

## Discovery and Validation

Only immediate subdirectories of `.agents/skills/` are candidates. A valid skill directory contains an exact `SKILL.md` filename whose frontmatter name matches the directory name and passes ADK validation.

Behavior is deliberately simple and strict:

- missing `.agents/skills/`: log that no project Skills directory was found and continue without Skills;
- existing empty directory: continue without registering an empty Skills Toolset;
- path exists but is not a directory: fail startup with a descriptive Skills setup error;
- malformed frontmatter, invalid name, name/directory mismatch, or preload failure: fail startup with the ADK error wrapped with the safe project-relative Skills root;
- directories without `SKILL.md`: ignored by the official filesystem source;
- valid skills: completely preloaded once during startup.

Complete preload reads frontmatter, instruction bodies, and supported resources into memory. Changes made after startup require restarting the process.

The application logs only the number of loaded project skills and the project-relative root. It does not log full skill instructions or resource contents.

## Progressive Disclosure and Resources

The official `skilltoolset` registers:

- `list_skills`;
- `load_skill`;
- `load_skill_resource`.

Its request processor adds the official Skills usage instruction and an XML catalog containing names and descriptions. Full instructions remain out of the prompt until `load_skill` runs.

The official filesystem source exposes resources from:

- `references/`;
- `assets/`;
- `scripts/`.

Each resource is limited by ADK to 10 MiB. Paths are cleaned and cannot escape the skill directory. Other client metadata, such as `agents/openai.yaml`, is not part of the ADK runtime resource set and remains ignored.

`skills-lock.json` is installation metadata and is not read by the runtime.

## Script and CLI Execution

The ADK Skills Toolset loads script content but does not itself start local processes. The existing `run_command` tool supplies that execution capability.

The root agent instruction will tell the model:

- use the official Skill tools to discover and load instructions and resources;
- when a loaded skill references `scripts/...` or another command, call `run_command`;
- set `working_directory` to `.agents/skills/<skill-name>` so paths in `SKILL.md` resolve from the skill root.

For example, `follow-builders-lite` can execute:

```json
{
  "command": "python3 scripts/prepare_digest.py --language zh",
  "working_directory": ".agents/skills/follow-builders-lite"
}
```

Per the user's selected local-trust mode, Skill activation and command execution add no confirmation, sandbox, allowlist, or trust prompt. The existing MCP confirmation behavior is unchanged.

ADK may parse and return `allowed-tools` metadata when present, but this iteration does not enforce it because all local tools and commands are unrestricted.

## Components and Interfaces

### `internal/skillsruntime`

Owns project directory detection and official ADK object construction. It returns a result rather than logging internally so `main` remains responsible for user-facing startup messages.

Conceptual interface:

```go
type Result struct {
    Found    bool
    Count    int
    Root     string
    Toolset  tool.Toolset
}

func Build(ctx context.Context, projectRoot string) (Result, error)
```

`Root` is the cleaned absolute path used internally. Logs and errors emitted by `main` should prefer `.agents/skills` rather than printing unrelated home or environment values.

### `main.go`

Builds the Skills runtime after the command runner and MCP toolsets are available, appends a non-nil Skills Toolset, logs the count, and supplies the combined toolsets to `buildAgent`.

### Agent instruction

Extends the current instruction with Skill activation and working-directory guidance. It does not duplicate ADK's complete built-in Skills system instruction.

## Testing

All automated tests use temporary directories and local files. They do not call the configured model, MCP servers, or external feeds.

`internal/skillsruntime` tests cover:

- missing `.agents/skills/`;
- an empty Skills directory;
- a valid minimal `SKILL.md`;
- multiple skills and deterministic count;
- malformed frontmatter;
- frontmatter/directory name mismatch;
- a path that exists as a regular file;
- preload of a `references/` resource;
- preload of a `scripts/` resource;
- official tool names `list_skills`, `load_skill`, and `load_skill_resource`;
- request processing that injects the available skill name and description;
- concurrent toolset access under the race detector.

Agent construction tests verify that MCP and Skills toolsets can coexist with local tools.

Repository validation remains:

```bash
go test ./...
go test -race ./...
go vet ./...
go build ./...
```

After automated validation, live console smoke tests will:

1. Ask `concise-writer` to shorten supplied text and verify the final answer follows its instruction.
2. Ask the model to list available Skills without calling MCP.
3. Ask `follow-builders-lite` to run its bundled script only after confirming the first two tests; this verifies the Skills-to-`run_command` path and may access the public upstream feed.

The smoke test will not modify skill files, install packages, or call MCP tools.

## Deferred Work

- user-level `~/.agents/skills/` discovery;
- project-over-user collision precedence;
- additional configurable skill roots;
- live reload without restarting;
- Skill enable/disable settings;
- trust prompts, command confirmation, sandboxing, or `allowed-tools` enforcement;
- client-specific rendering of `agents/openai.yaml`.
