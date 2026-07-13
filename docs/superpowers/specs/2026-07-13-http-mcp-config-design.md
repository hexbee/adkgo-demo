# HTTP MCP Configuration Design

## Goal

Extend the existing ADK Go v2 demo so it automatically discovers HTTP MCP servers from a project-local `.mcp.json`, exposes their tools through native ADK `mcptoolset` instances, and requires human confirmation before every remote MCP tool call.

This iteration covers HTTP transports only. Stdio transport support is explicitly deferred.

## Configuration Contract

The default path is `.mcp.json` in the process working directory. It uses this shape:

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

Each server entry has:

- a non-empty map key used only as the local server identifier;
- `type`, which must equal `http` in this version;
- an absolute `http` or `https` URL;
- optional string-to-string request headers.

Unknown top-level and server fields are rejected so spelling errors do not silently disable security settings. Empty server maps are valid and produce no MCP toolsets.

`.mcp.json` is local-only and is added to `.gitignore`. The repository commits `.mcp.example.json` with placeholders and no usable credentials. Existing `.mcp.json` contents are never copied into committed files.

## Loading Behavior

`internal/mcpconfig` owns JSON parsing and validation.

- If `.mcp.json` does not exist, startup continues without MCP and logs a short informational message.
- If the file exists but cannot be read, parsed, or validated, startup fails.
- Errors identify the server name and invalid field, but exclude header values and URL query strings.
- Parsed configuration is held in memory only. It is not logged or included in errors.
- Server names are sorted before runtime construction so startup behavior and tests are deterministic.

The existing `.env` model configuration remains unchanged.

## Runtime Construction

`internal/mcpruntime` converts each validated server into a native ADK toolset:

1. Create a dedicated `http.Client` for the server.
2. Wrap its base `http.RoundTripper` with a header-injecting transport.
3. Clone each outbound request before applying configured headers.
4. Create `mcp.StreamableClientTransport` with the configured endpoint and HTTP client.
5. Pass the transport to `mcptoolset.New`.
6. Set `RequireConfirmation: true` unconditionally.

The header transport must preserve headers already set by the MCP SDK. Configured headers override matching existing values for that server only. Headers cannot leak between servers because every server owns a separate client and transport.

ADK's MCP toolset creates its MCP session lazily when tools are first requested and handles reconnectable session failures. The application's signal-cancelled root context is propagated through launcher, tool discovery, and tool calls.

## Agent Integration

The root program will:

- use `signal.NotifyContext` so Ctrl+C cancels MCP work;
- load `.mcp.json` after model configuration is validated;
- build one `tool.Toolset` per configured HTTP server;
- attach those values through `llmagent.Config.Toolsets`;
- retain the existing local `lookup_time` function tool;
- update the instruction so the model understands that remote MCP tools require user confirmation.

The same configuration works in ADK console and web launcher modes.

## Human Confirmation

Every MCP toolset is constructed with `RequireConfirmation: true`. There is no configuration switch to disable confirmation in this iteration.

ADK controls the request/approve/reject flow. A rejected confirmation prevents the MCP call. The adapter does not emulate or bypass ADK confirmation events.

This conservative default is intentional because configured MCP servers may expose actions with financial or other external side effects.

## Tool Naming

MCP tool names are preserved exactly as returned by each server. This iteration does not prefix names with the local server identifier.

Configured servers are therefore expected to expose unique tool names. If duplicate tool names produce an ADK or provider error, the error is surfaced and the user can temporarily disable one server. A prefixing aggregator is a possible later iteration.

## Security and Error Handling

- Never log header names together with values.
- Never include header values, Authorization content, or URL query strings in errors.
- Never serialize loaded configuration after parsing.
- Clone outbound requests before setting configured headers.
- Apply headers only to the exact MCP transport for their configured server.
- Require confirmation for every remote tool call.
- Keep `.mcp.json` ignored and verify it is not staged.

Runtime connection and protocol errors may include the sanitized server name and URL origin/path, but query strings are removed.

## Testing

Tests do not connect to configured real services.

`internal/mcpconfig` tests cover:

- missing file;
- valid multiple-server configuration;
- deterministic server ordering;
- malformed JSON;
- missing type or URL;
- unsupported `stdio` type;
- non-HTTP URL;
- unknown fields;
- validation errors that do not expose headers or query values.

`internal/mcpruntime` tests use a local MCP Streamable HTTP handler and cover:

- tool discovery through the resulting ADK toolset;
- configured header injection;
- isolation of headers between two servers;
- `RequireConfirmation` behavior through ADK tool execution;
- cancellation propagation;
- connection errors without secret leakage.

The full repository continues to pass:

```bash
go test ./...
go test -race ./...
go vet ./...
go build ./...
```

## Repository Changes

```text
.
├── .gitignore                         # add .mcp.json
├── .mcp.example.json                  # safe placeholder configuration
├── README.md                          # MCP setup and confirmation flow
├── main.go                            # load and attach MCP toolsets
├── internal/mcpconfig/
│   ├── config.go
│   └── config_test.go
└── internal/mcpruntime/
    ├── runtime.go
    └── runtime_test.go
```

## Success Criteria

- The existing project-local `.mcp.json` is ignored by Git.
- Starting the demo automatically loads every valid HTTP MCP server in that file.
- The agent can discover tools from each configured server.
- Every MCP tool call presents an ADK human-confirmation request before network execution.
- Rejecting confirmation prevents the remote call.
- Console and web modes continue to start normally when `.mcp.json` is absent.
- Invalid configuration fails startup without revealing credentials or URL query secrets.
- No automated test calls a real MCP service.
