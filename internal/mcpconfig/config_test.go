package mcpconfig

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), ".mcp.json")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadMissingFile(t *testing.T) {
	got, err := Load(filepath.Join(t.TempDir(), ".mcp.json"))
	if err != nil {
		t.Fatal(err)
	}
	if got.Found || len(got.Servers) != 0 {
		t.Fatalf("result = %#v", got)
	}
}

func TestLoadSortsAndValidatesServers(t *testing.T) {
	path := writeConfig(t, `{"mcpServers":{"z":{"type":"http","url":"https://z.test/mcp","headers":{"X-Key":"z-secret"}},"a":{"type":"stdio","command":"npx","args":["-y","example-server"],"env":{"TEST_KEY":"test-value"},"cwd":"/tmp"}}}`)
	got, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Found || len(got.Servers) != 2 {
		t.Fatalf("result = %#v", got)
	}
	if got.Servers[0].Name != "a" || got.Servers[1].Name != "z" {
		t.Fatalf("servers = %#v", got.Servers)
	}
	if got.Servers[1].Headers["X-Key"] != "z-secret" {
		t.Fatal("header was not loaded")
	}
	if got.Servers[0].Type != TypeStdio || got.Servers[0].Command != "npx" || len(got.Servers[0].Args) != 2 || got.Servers[0].Env["TEST_KEY"] != "test-value" || got.Servers[0].CWD != "/tmp" {
		t.Fatalf("stdio server = %#v", got.Servers[0])
	}
}

func TestLoadSupportsClaudeCodeStdioDefaultsAndEnvironmentExpansion(t *testing.T) {
	t.Setenv("MCP_TEST_COMMAND", "test-command")
	t.Setenv("MCP_TEST_ARG", "test-arg")
	t.Setenv("MCP_TEST_TOKEN", "test-token")
	path := writeConfig(t, `{"mcpServers":{"local":{"command":"${MCP_TEST_COMMAND}","args":["--value","${MCP_TEST_ARG}","${MCP_MISSING_ARG:-fallback-arg}"],"env":{"TOKEN":"${MCP_TEST_TOKEN}","MODE":"${MCP_MISSING_MODE:-safe}"},"cwd":"${MCP_MISSING_CWD:-.}"}}}`)
	got, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	server := got.Servers[0]
	if server.Type != TypeStdio || server.Command != "test-command" || server.Args[1] != "test-arg" || server.Args[2] != "fallback-arg" || server.Env["TOKEN"] != "test-token" || server.Env["MODE"] != "safe" || server.CWD != "." {
		t.Fatalf("server = %#v", server)
	}
}

func TestLoadExpandsHTTPURLAndHeaders(t *testing.T) {
	t.Setenv("MCP_TEST_HOST", "example.test")
	t.Setenv("MCP_TEST_TOKEN", "test-token")
	path := writeConfig(t, `{"mcpServers":{"remote":{"type":"http","url":"https://${MCP_TEST_HOST}/mcp","headers":{"Authorization":"Bearer ${MCP_TEST_TOKEN}"}}}}`)
	got, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	server := got.Servers[0]
	if server.URL != "https://example.test/mcp" || server.Headers["Authorization"] != "Bearer test-token" {
		t.Fatalf("server = %#v", server)
	}
}

func TestLoadRejectsMissingOrInvalidEnvironmentReferencesWithoutDefaults(t *testing.T) {
	for _, tc := range []struct {
		name, command, want string
	}{
		{"missing", "${MCP_TEST_DEFINITELY_MISSING}", "MCP_TEST_DEFINITELY_MISSING"},
		{"unterminated", "${MCP_TEST_UNTERMINATED", "unterminated"},
		{"invalid name", "${BAD-NAME}", "invalid"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := writeConfig(t, `{"mcpServers":{"local":{"type":"stdio","command":`+fmt.Sprintf("%q", tc.command)+`}}}`)
			_, err := Load(path)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v", err)
			}
		})
	}
}

func TestLoadRejectsInvalidConfigWithoutSecrets(t *testing.T) {
	for _, tc := range []struct {
		name, body, want, secret string
	}{
		{"malformed", `{`, "parse", ""},
		{"unknown top field", `{"mcpServers":{},"token":"secret-top"}`, "unknown field", "secret-top"},
		{"unknown server field", `{"mcpServers":{"x":{"type":"http","url":"https://x.test/mcp","credential":"secret-field"}}}`, "unknown field", "secret-field"},
		{"unsupported type", `{"mcpServers":{"x":{"type":"sse","url":"https://x.test/mcp"}}}`, "type", ""},
		{"bad URL", `{"mcpServers":{"x":{"type":"http","url":"file:///tmp/x?token=query-secret"}}}`, "URL", "query-secret"},
		{"empty URL", `{"mcpServers":{"x":{"type":"http","url":"","headers":{"Authorization":"Bearer header-secret"}}}}`, "URL", "header-secret"},
		{"http with stdio field", `{"mcpServers":{"x":{"type":"http","url":"https://x.test/mcp","command":"secret-command"}}}`, "cannot contain", "secret-command"},
		{"stdio missing command", `{"mcpServers":{"x":{"type":"stdio","env":{"TOKEN":"env-secret"}}}}`, "command", "env-secret"},
		{"stdio with HTTP field", `{"mcpServers":{"x":{"type":"stdio","command":"server","headers":{"Authorization":"Bearer header-secret"}}}}`, "cannot contain", "header-secret"},
		{"stdio invalid env", `{"mcpServers":{"x":{"type":"stdio","command":"server","env":{"BAD=NAME":"env-secret"}}}}`, "invalid", "env-secret"},
		{"stdio NUL arg", `{"mcpServers":{"x":{"type":"stdio","command":"server","args":["secret-arg\u0000"]}}}`, "NUL", "secret-arg"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Load(writeConfig(t, tc.body))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v", err)
			}
			if tc.secret != "" && strings.Contains(err.Error(), tc.secret) {
				t.Fatalf("secret leaked: %v", err)
			}
		})
	}
}

func TestServerSafeEndpointRemovesSecrets(t *testing.T) {
	server := Server{Name: "maps", Type: TypeHTTP, URL: "https://user:pass@example.test/mcp?key=query-secret#fragment"}
	got := server.SafeEndpoint()
	if got != "https://example.test/mcp" {
		t.Fatalf("safe endpoint = %q", got)
	}
}

func TestServerSafeTargetDoesNotExposeStdioCommand(t *testing.T) {
	server := Server{Name: "local", Type: TypeStdio, Command: "secret-command", Args: []string{"secret-arg"}}
	if got := server.SafeTarget(); got != "stdio" {
		t.Fatalf("safe target = %q", got)
	}
}
