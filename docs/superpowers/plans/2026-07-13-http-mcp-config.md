# HTTP MCP Configuration Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Load HTTP MCP servers from a local `.mcp.json`, expose them as native ADK Go v2 toolsets, and require human confirmation before every MCP tool call.

**Architecture:** `internal/mcpconfig` strictly parses and validates the editor-style JSON configuration without exposing secrets. `internal/mcpruntime` builds one isolated header-injecting HTTP client and native ADK `mcptoolset` per server. `main.go` attaches those toolsets to the existing agent while preserving the local tool and OpenAI-compatible model adapter.

**Tech Stack:** Go 1.25+, `google.golang.org/adk/v2 v2.0.0`, `github.com/modelcontextprotocol/go-sdk v1.4.1`, standard `encoding/json`, `net/http`, `httptest`, and `testing`.

## Global Constraints

- Support only MCP server entries whose `type` is exactly `http`; stdio is deferred.
- Read `.mcp.json` from the process working directory by default.
- Missing `.mcp.json` is non-fatal; malformed or invalid existing configuration is fatal.
- Every MCP toolset must use `RequireConfirmation: true`; this iteration has no opt-out.
- Never log or return header values, Authorization content, full URL query strings, or raw configuration.
- Add `.mcp.json` to `.gitignore` before any broad staging command.
- Commit only `.mcp.example.json` with placeholder endpoints and credentials.
- Preserve existing request headers unless the server config intentionally overrides the same key.
- Keep each server's headers isolated in its own `http.Client` and transport.
- Do not prefix MCP tool names; configured servers must expose unique tool names.
- Automated tests must use local temporary files and local MCP/HTTP servers only.

---

## File Map

- `.gitignore`: ignore the real `.mcp.json`.
- `.mcp.example.json`: safe placeholder showing the supported schema.
- `internal/mcpconfig/config.go`: strict JSON loading, validation, deterministic ordering, and safe URL descriptions.
- `internal/mcpconfig/config_test.go`: missing/valid/invalid/secret-redaction test matrix.
- `internal/mcpruntime/runtime.go`: isolated header transport and native ADK MCP toolset construction.
- `internal/mcpruntime/runtime_test.go`: request header isolation, tool discovery, confirmation, and cancellation tests.
- `main.go`: signal context, configuration loading, and `llmagent.Config.Toolsets` wiring.
- `README.md`: setup, security, console/web usage, and confirmation behavior.

---

### Task 1: Protect and strictly load `.mcp.json`

**Files:**
- Modify: `.gitignore`
- Create: `.mcp.example.json`
- Create: `internal/mcpconfig/config.go`
- Create: `internal/mcpconfig/config_test.go`

**Interfaces:**
- Produces: `mcpconfig.Load(path string) (mcpconfig.Result, error)`
- Produces: `mcpconfig.Result{Found bool; Servers []mcpconfig.Server}`
- Produces: `mcpconfig.Server{Name string; URL string; Headers map[string]string}`
- Produces: `mcpconfig.Server.SafeEndpoint() string`

- [ ] **Step 1: Ignore the real config before touching other files**

Append under the existing env section in `.gitignore`:

```gitignore
# local MCP configuration (may contain credentials)
.mcp.json
```

Immediately verify:

```bash
git check-ignore .mcp.json
git status --short -- .mcp.json
```

Expected: the first command prints `.mcp.json`; the second prints nothing.

- [ ] **Step 2: Add a safe example configuration**

Create `.mcp.example.json`:

```json
{
  "mcpServers": {
    "example": {
      "type": "http",
      "url": "https://mcp.example.test/mcp",
      "headers": {
        "Authorization": "Bearer REPLACE_ME"
      }
    }
  }
}
```

- [ ] **Step 3: Write failing loader tests**

Create `internal/mcpconfig/config_test.go`. Use `t.TempDir()` and `os.WriteFile(..., 0o600)`. Cover these exact assertions:

```go
func TestLoadMissingFile(t *testing.T) {
	got, err := Load(filepath.Join(t.TempDir(), ".mcp.json"))
	if err != nil { t.Fatal(err) }
	if got.Found || len(got.Servers) != 0 { t.Fatalf("result = %#v", got) }
}

func TestLoadSortsAndValidatesServers(t *testing.T) {
	path := writeConfig(t, `{"mcpServers":{"z":{"type":"http","url":"https://z.test/mcp","headers":{"X-Key":"z-secret"}},"a":{"type":"http","url":"http://127.0.0.1:8080/mcp"}}}`)
	got, err := Load(path)
	if err != nil { t.Fatal(err) }
	if !got.Found || len(got.Servers) != 2 { t.Fatalf("result = %#v", got) }
	if got.Servers[0].Name != "a" || got.Servers[1].Name != "z" { t.Fatalf("servers = %#v", got.Servers) }
	if got.Servers[1].Headers["X-Key"] != "z-secret" { t.Fatal("header was not loaded") }
}

func TestLoadRejectsInvalidConfigWithoutSecrets(t *testing.T) {
	for _, tc := range []struct{ name, body, want, secret string }{
		{"malformed", `{`, "parse", ""},
		{"unknown top field", `{"mcpServers":{},"token":"secret-top"}`, "unknown field", "secret-top"},
		{"unknown server field", `{"mcpServers":{"x":{"type":"http","url":"https://x.test/mcp","credential":"secret-field"}}}`, "unknown field", "secret-field"},
		{"unsupported type", `{"mcpServers":{"x":{"type":"stdio","url":"https://x.test/mcp"}}}`, "type", ""},
		{"bad URL", `{"mcpServers":{"x":{"type":"http","url":"file:///tmp/x?token=query-secret"}}}`, "URL", "query-secret"},
		{"empty URL", `{"mcpServers":{"x":{"type":"http","url":"","headers":{"Authorization":"Bearer header-secret"}}}}`, "URL", "header-secret"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Load(writeConfig(t, tc.body))
			if err == nil || !strings.Contains(err.Error(), tc.want) { t.Fatalf("err = %v", err) }
			if tc.secret != "" && strings.Contains(err.Error(), tc.secret) { t.Fatalf("secret leaked: %v", err) }
		})
	}
}
```

- [ ] **Step 4: Run tests and confirm failure**

Run: `go test ./internal/mcpconfig -v`

Expected: FAIL because `Load`, `Result`, and `Server` are undefined.

- [ ] **Step 5: Implement strict loading and safe endpoint formatting**

Create `internal/mcpconfig/config.go` with these types and functions:

```go
type fileConfig struct {
	MCPServers map[string]serverConfig `json:"mcpServers"`
}

type serverConfig struct {
	Type    string            `json:"type"`
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers,omitempty"`
}

type Server struct {
	Name    string
	URL     string
	Headers map[string]string
}

type Result struct {
	Found   bool
	Servers []Server
}

func Load(path string) (Result, error)
func (s Server) SafeEndpoint() string
```

`Load` must:

1. return `Result{}` for `errors.Is(err, os.ErrNotExist)`;
2. decode with `json.Decoder.DisallowUnknownFields()`;
3. require EOF after the first JSON value;
4. require `type == "http"`;
5. require a non-empty server name;
6. validate `url.ParseRequestURI`, absolute host, and `http`/`https` scheme;
7. clone every headers map with `maps.Clone`;
8. sort map keys before appending servers;
9. construct validation errors from server name and field only, never the raw value.

`SafeEndpoint` must return `scheme://host/path` with `RawQuery` and `Fragment` cleared. If defensive re-parsing fails, return the server name rather than the raw URL.

- [ ] **Step 6: Verify and commit configuration loading**

Run:

```bash
gofmt -w internal/mcpconfig
go test ./internal/mcpconfig -v
git diff --check
git status --short --ignored
```

Expected: tests PASS and `.mcp.json` appears only with the `!!` ignored marker when ignored files are requested.

Commit only safe files:

```bash
git add .gitignore .mcp.example.json internal/mcpconfig
git commit -m "feat: load local HTTP MCP configuration"
```

---

### Task 2: Build isolated, confirmation-protected ADK MCP toolsets

**Files:**
- Create: `internal/mcpruntime/runtime.go`
- Create: `internal/mcpruntime/runtime_test.go`
- Modify: `go.mod`
- Modify: `go.sum`

**Interfaces:**
- Consumes: `mcpconfig.Server` from Task 1.
- Produces: `mcpruntime.Build(servers []mcpconfig.Server) ([]tool.Toolset, error)`
- Produces private `headerTransport{base http.RoundTripper; headers http.Header}` implementing `http.RoundTripper`.

- [ ] **Step 1: Pin the MCP SDK dependency**

Run:

```bash
go get github.com/modelcontextprotocol/go-sdk@v1.4.1
go mod tidy
```

Expected: `go.mod` includes the MCP SDK directly once runtime code imports it.

- [ ] **Step 2: Write a failing header-isolation test**

Use two `httptest.Server` handlers and a private helper that calls `headerTransport.RoundTrip`. Assert server A receives `Authorization: Bearer alpha`, server B receives `X-API-Key: beta`, neither receives the other's header, and the original request headers remain unchanged after each call.

The test request must start with `Mcp-Protocol-Version: test-version` and assert it remains present, proving configured headers do not replace SDK headers.

- [ ] **Step 3: Write a failing MCP discovery test**

Create a local SDK server:

```go
server := mcp.NewServer(&mcp.Implementation{Name: "test-server", Version: "1.0.0"}, nil)
mcp.AddTool(server, &mcp.Tool{Name: "echo", Description: "echo input"},
	func(ctx context.Context, req *mcp.CallToolRequest, input struct{ Text string `json:"text"` }) (*mcp.CallToolResult, map[string]any, error) {
		return nil, map[string]any{"text": input.Text}, nil
	})
handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil)
httpServer := httptest.NewServer(handler)
t.Cleanup(httpServer.Close)
```

Call `Build` with that URL and a test header, then call `toolsets[0].Tools(&agent.StrictContextMock{Ctx: t.Context()})`. Assert one tool named `echo` is returned and the handler observed the configured header.

- [ ] **Step 4: Write a failing confirmation test**

Type-assert the discovered tool to this public-shape test interface:

```go
type runnableTool interface {
	Run(agent.Context, any) (map[string]any, error)
}
```

Create a test context embedding `agent.StrictContextMock` and overriding `ToolConfirmation`, `RequestConfirmation`, and `Actions`. On the first `Run`, assert:

- `RequestConfirmation` was called;
- the MCP server's call counter remains zero;
- `errors.Is(err, tool.ErrConfirmationRequired)` is true.

Then return a confirmed `toolconfirmation.ToolConfirmation` from the fake context, call `Run` again, and assert the MCP server call counter becomes one.

- [ ] **Step 5: Run runtime tests and confirm failure**

Run: `go test ./internal/mcpruntime -v`

Expected: FAIL because `Build` and `headerTransport` are undefined.

- [ ] **Step 6: Implement isolated HTTP transports and native toolsets**

Create `internal/mcpruntime/runtime.go`:

```go
type headerTransport struct {
	base    http.RoundTripper
	headers http.Header
}

func (t *headerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	clone := req.Clone(req.Context())
	clone.Header = req.Header.Clone()
	for name, values := range t.headers {
		clone.Header.Del(name)
		for _, value := range values { clone.Header.Add(name, value) }
	}
	return t.base.RoundTrip(clone)
}

func Build(servers []mcpconfig.Server) ([]tool.Toolset, error) {
	result := make([]tool.Toolset, 0, len(servers))
	for _, server := range servers {
		headers := make(http.Header, len(server.Headers))
		for name, value := range server.Headers { headers.Set(name, value) }
		client := &http.Client{Transport: &headerTransport{base: http.DefaultTransport, headers: headers}}
		transport := &mcp.StreamableClientTransport{Endpoint: server.URL, HTTPClient: client}
		toolset, err := mcptoolset.New(mcptoolset.Config{Transport: transport, RequireConfirmation: true})
		if err != nil { return nil, fmt.Errorf("create MCP toolset for %s (%s): %w", server.Name, server.SafeEndpoint(), err) }
		result = append(result, toolset)
	}
	return result, nil
}
```

If `http.DefaultTransport` is nil in a test override, fall back to `http.DefaultTransport`'s concrete default; never use a nil base transport. Do not set `http.Client.Timeout`, because MCP may keep a standalone SSE connection open.

- [ ] **Step 7: Verify race safety and commit runtime construction**

Run:

```bash
gofmt -w internal/mcpruntime
go mod tidy
go test ./internal/mcpruntime -v
go test -race ./internal/mcpruntime
go test ./...
```

Expected: all tests PASS and no real network endpoint is contacted.

Commit:

```bash
git add go.mod go.sum internal/mcpruntime
git commit -m "feat: build confirmation-protected MCP toolsets"
```

---

### Task 3: Attach MCP toolsets to the demo and document usage

**Files:**
- Modify: `main.go`
- Modify: `README.md`

**Interfaces:**
- Consumes: `mcpconfig.Load(".mcp.json")`.
- Consumes: `mcpruntime.Build(result.Servers)`.
- Produces: the existing `go run . console` and `go run . web webui api` commands with optional MCP discovery.

- [ ] **Step 1: Extract a testable agent builder**

Refactor main wiring into:

```go
func buildAgent(adaptedModel model.LLM, toolsets []tool.Toolset) (agent.Agent, error)
```

Move `lookup_time`, its function-tool construction, and `llmagent.New` into this function. Set both `Tools: []tool.Tool{timeTool}` and `Toolsets: toolsets`. Update the instruction to state that remote MCP actions require explicit user confirmation.

- [ ] **Step 2: Add signal context and MCP loading to startup**

Replace `context.Background()` with:

```go
ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
defer stop()
```

After model creation:

```go
mcpResult, err := mcpconfig.Load(".mcp.json")
if err != nil { log.Fatalf("MCP configuration error: %v", err) }
mcpToolsets, err := mcpruntime.Build(mcpResult.Servers)
if err != nil { log.Fatalf("MCP setup error: %v", err) }
if mcpResult.Found { log.Printf("loaded %d HTTP MCP server(s)", len(mcpResult.Servers)) }
```

Do not log server URLs, headers, or raw config.

- [ ] **Step 3: Add agent-construction tests**

Create `main_test.go` with a minimal fake `model.LLM` and a stub `tool.Toolset`. Call `buildAgent` and assert no construction error with zero, one, and two toolsets. Keep tests offline and do not load the real `.mcp.json`.

- [ ] **Step 4: Update README**

Document:

```bash
cp .mcp.example.json .mcp.json
go run . console
```

Explain the supported `mcpServers.*.{type,url,headers}` fields, HTTP-only limitation, automatic tool discovery, first-call lazy connection, and mandatory ADK confirmation. State that `.mcp.json` often contains credentials and must never be committed. Include a generic example only; do not include real server names, endpoints, keys, or headers from the user's local file.

- [ ] **Step 5: Verify launcher startup without contacting real MCP servers**

Run with a temporary working directory that contains `.env` values but no `.mcp.json`, or override model environment variables and run:

```bash
API_KEY=test-key BASE_URL=https://example.invalid/v1 MODEL_NAME=test-model CONTEXT_WINDOW=1000 MAX_TOKENS=100 go run . --help
```

Expected: agent and launcher initialize; help syntax is displayed. No MCP endpoint is contacted because tool discovery is lazy.

- [ ] **Step 6: Run full validation and secret audit**

Run:

```bash
gofmt -w main.go main_test.go
go mod tidy
go test ./...
go test -race ./...
go vet ./...
go build ./...
git diff --check
git check-ignore .mcp.json
git status --short --ignored
```

Also scan tracked candidates without printing `.mcp.json`:

```bash
git grep -nE 'Authorization.*Bearer [^R]|[?&](key|token)=[A-Za-z0-9]' -- ':!.mcp.json' || true
```

Expected: validation commands pass, `.mcp.json` is ignored, and no usable credential is present in tracked files.

- [ ] **Step 7: Commit integration and documentation**

```bash
git add main.go main_test.go README.md
git commit -m "feat: attach configured MCP servers to agent"
```

Do not use `git add .`; explicitly confirm `.mcp.json` is absent from `git diff --cached --name-only` before committing.

---

## Final Verification

```bash
go test ./...
go test -race ./...
go vet ./...
go build ./...
git check-ignore .mcp.json
git diff --cached --name-only
git status --short --ignored
```

Expected:

- all code checks pass;
- `.mcp.json` is ignored and absent from staged/tracked files;
- `.mcp.example.json` contains placeholders only;
- the demo starts with or without MCP configuration;
- every discovered MCP tool requests ADK confirmation before its first remote execution;
- no automated test contacts a configured real MCP server.
