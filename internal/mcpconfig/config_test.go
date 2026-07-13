package mcpconfig

import (
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
	path := writeConfig(t, `{"mcpServers":{"z":{"type":"http","url":"https://z.test/mcp","headers":{"X-Key":"z-secret"}},"a":{"type":"http","url":"http://127.0.0.1:8080/mcp"}}}`)
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
}

func TestLoadRejectsInvalidConfigWithoutSecrets(t *testing.T) {
	for _, tc := range []struct {
		name, body, want, secret string
	}{
		{"malformed", `{`, "parse", ""},
		{"unknown top field", `{"mcpServers":{},"token":"secret-top"}`, "unknown field", "secret-top"},
		{"unknown server field", `{"mcpServers":{"x":{"type":"http","url":"https://x.test/mcp","credential":"secret-field"}}}`, "unknown field", "secret-field"},
		{"unsupported type", `{"mcpServers":{"x":{"type":"stdio","url":"https://x.test/mcp"}}}`, "type", ""},
		{"bad URL", `{"mcpServers":{"x":{"type":"http","url":"file:///tmp/x?token=query-secret"}}}`, "URL", "query-secret"},
		{"empty URL", `{"mcpServers":{"x":{"type":"http","url":"","headers":{"Authorization":"Bearer header-secret"}}}}`, "URL", "header-secret"},
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
	server := Server{Name: "maps", URL: "https://user:pass@example.test/mcp?key=query-secret#fragment"}
	got := server.SafeEndpoint()
	if got != "https://example.test/mcp" {
		t.Fatalf("safe endpoint = %q", got)
	}
}
