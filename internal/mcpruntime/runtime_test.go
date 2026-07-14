package mcpruntime

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/hexbee/adkgo-demo/internal/mcpconfig"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/session"
	"google.golang.org/adk/v2/tool"
	"google.golang.org/adk/v2/tool/toolconfirmation"
)

const stdioHelperEnv = "ADKGO_TEST_STDIO_HELPER"

func TestMain(m *testing.M) {
	if os.Getenv(stdioHelperEnv) == "1" {
		runStdioTestServer()
		return
	}
	os.Exit(m.Run())
}

func runStdioTestServer() {
	cwd, err := os.Getwd()
	if err != nil {
		log.Fatal(err)
	}
	description := strings.Join([]string{
		strings.Join(os.Args[1:], "|"),
		os.Getenv("ADKGO_TEST_STDIO_CONFIGURED"),
		os.Getenv("ADKGO_TEST_STDIO_INHERITED"),
		cwd,
	}, ":")
	server := mcp.NewServer(&mcp.Implementation{Name: "stdio-test-server", Version: "1.0.0"}, nil)
	mcp.AddTool(server, &mcp.Tool{Name: "stdio_echo", Description: description},
		func(_ context.Context, _ *mcp.CallToolRequest, input echoInput) (*mcp.CallToolResult, map[string]any, error) {
			return nil, map[string]any{"text": input.Text}, nil
		})
	if err := server.Run(context.Background(), &mcp.StdioTransport{}); err != nil {
		log.Fatal(err)
	}
}

func TestHeaderTransportIsolatesAndPreservesHeaders(t *testing.T) {
	makeServer := func(wantName, wantValue, absent string) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get(wantName) != wantValue {
				t.Errorf("%s = %q", wantName, r.Header.Get(wantName))
			}
			if r.Header.Get(absent) != "" {
				t.Errorf("unexpected %s header", absent)
			}
			if r.Header.Get("Mcp-Protocol-Version") != "test-version" {
				t.Error("SDK header was not preserved")
			}
			w.WriteHeader(http.StatusNoContent)
		}))
	}
	a := makeServer("Authorization", "Bearer alpha", "X-API-Key")
	defer a.Close()
	b := makeServer("X-API-Key", "beta", "Authorization")
	defer b.Close()

	for _, tc := range []struct {
		url     string
		headers http.Header
	}{{a.URL, http.Header{"Authorization": {"Bearer alpha"}}}, {b.URL, http.Header{"X-API-Key": {"beta"}}}} {
		req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, tc.url, nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Mcp-Protocol-Version", "test-version")
		response, err := (&headerTransport{base: http.DefaultTransport, headers: tc.headers}).RoundTrip(req)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if req.Header.Get("Authorization") != "" || req.Header.Get("X-API-Key") != "" {
			t.Fatal("original request was mutated")
		}
	}
}

type echoInput struct {
	Text string `json:"text"`
}

func newMCPServer(t *testing.T, calls *atomic.Int32, sawHeader *atomic.Bool) *httptest.Server {
	t.Helper()
	server := mcp.NewServer(&mcp.Implementation{Name: "test-server", Version: "1.0.0"}, nil)
	mcp.AddTool(server, &mcp.Tool{Name: "echo", Description: "echo input"},
		func(_ context.Context, _ *mcp.CallToolRequest, input echoInput) (*mcp.CallToolResult, map[string]any, error) {
			calls.Add(1)
			return nil, map[string]any{"text": input.Text}, nil
		})
	handler := mcp.NewStreamableHTTPHandler(func(r *http.Request) *mcp.Server {
		if r.Header.Get("X-Test-Key") == "test-secret" {
			sawHeader.Store(true)
		}
		return server
	}, nil)
	httpServer := httptest.NewServer(handler)
	t.Cleanup(func() {
		httpServer.CloseClientConnections()
		httpServer.Close()
	})
	return httpServer
}

func discoverEchoTool(t *testing.T, calls *atomic.Int32, sawHeader *atomic.Bool) tool.Tool {
	t.Helper()
	server := newMCPServer(t, calls, sawHeader)
	toolsets, err := Build([]mcpconfig.Server{{Name: "test", Type: mcpconfig.TypeHTTP, URL: server.URL, Headers: map[string]string{"X-Test-Key": "test-secret"}}})
	if err != nil {
		t.Fatal(err)
	}
	if toolsets[0].Name() != "mcp_test" {
		t.Fatalf("toolset name = %q", toolsets[0].Name())
	}
	ctx := &agent.StrictContextMock{Ctx: t.Context()}
	tools, err := toolsets[0].Tools(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(tools) != 1 || tools[0].Name() != "echo" {
		t.Fatalf("tools = %#v", tools)
	}
	return tools[0]
}

func TestBuildStdioDiscoversToolsAndAppliesProcessConfig(t *testing.T) {
	t.Setenv(stdioHelperEnv, "1")
	t.Setenv("ADKGO_TEST_STDIO_CONFIGURED", "parent-value")
	t.Setenv("ADKGO_TEST_STDIO_INHERITED", "inherited-value")
	cwd := t.TempDir()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	toolsets, err := Build([]mcpconfig.Server{{
		Name: "local-test", Type: mcpconfig.TypeStdio, Command: executable,
		Args: []string{"first-arg", "second arg"},
		Env:  map[string]string{"ADKGO_TEST_STDIO_CONFIGURED": "configured-value"},
		CWD:  cwd,
	}})
	if err != nil {
		t.Fatal(err)
	}
	tools, err := toolsets[0].Tools(&agent.StrictContextMock{Ctx: t.Context()})
	if err != nil {
		t.Fatal(err)
	}
	if len(tools) != 1 || tools[0].Name() != "stdio_echo" {
		t.Fatalf("tools = %#v", tools)
	}
	description := tools[0].Description()
	for _, want := range []string{"first-arg|second arg", "configured-value", "inherited-value", filepath.Clean(cwd)} {
		if !strings.Contains(description, want) {
			t.Fatalf("description %q does not contain %q", description, want)
		}
	}
}

func TestStdioTransportCanCreateAProcessForEachConnection(t *testing.T) {
	t.Setenv(stdioHelperEnv, "1")
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	transport, err := buildTransport(mcpconfig.Server{
		Name: "reconnect-test", Type: mcpconfig.TypeStdio, Command: executable,
	})
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		client := mcp.NewClient(&mcp.Implementation{Name: "reconnect-test-client", Version: "1.0.0"}, nil)
		session, err := client.Connect(t.Context(), transport, nil)
		if err != nil {
			t.Fatal(err)
		}
		tools, err := session.ListTools(t.Context(), nil)
		if err != nil {
			t.Fatal(err)
		}
		if len(tools.Tools) != 1 || tools.Tools[0].Name != "stdio_echo" {
			t.Fatalf("tools = %#v", tools.Tools)
		}
		if err := session.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestStdioTransportStartErrorDoesNotExposeCommand(t *testing.T) {
	const secretCommand = "missing-secret-command-for-mcp-test"
	transport, err := buildTransport(mcpconfig.Server{
		Name: "redaction-test", Type: mcpconfig.TypeStdio, Command: secretCommand,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = transport.Connect(t.Context())
	if err == nil || !strings.Contains(err.Error(), "failed to start") {
		t.Fatalf("err = %v", err)
	}
	if strings.Contains(err.Error(), secretCommand) {
		t.Fatalf("command leaked: %v", err)
	}
}

func TestBuildRejectsUnknownTransportType(t *testing.T) {
	_, err := Build([]mcpconfig.Server{{Name: "bad", Type: "unknown"}})
	if err == nil || !strings.Contains(err.Error(), "unsupported type") {
		t.Fatalf("err = %v", err)
	}
}

func TestBuildDiscoversToolsAndInjectsHeaders(t *testing.T) {
	var calls atomic.Int32
	var sawHeader atomic.Bool
	discoverEchoTool(t, &calls, &sawHeader)
	if !sawHeader.Load() {
		t.Fatal("configured header was not observed")
	}
	if calls.Load() != 0 {
		t.Fatal("tool was called during discovery")
	}
}

type runnableTool interface {
	Run(agent.Context, any) (map[string]any, error)
}

type confirmationContext struct {
	agent.StrictContextMock
	confirmation *toolconfirmation.ToolConfirmation
	requested    bool
	actions      session.EventActions
}

func (c *confirmationContext) ToolConfirmation() *toolconfirmation.ToolConfirmation {
	return c.confirmation
}

func (c *confirmationContext) RequestConfirmation(_ string, _ any) error {
	c.requested = true
	return nil
}

func (c *confirmationContext) Actions() *session.EventActions { return &c.actions }

func TestBuildRequiresConfirmationBeforeCallingServer(t *testing.T) {
	var calls atomic.Int32
	var sawHeader atomic.Bool
	discovered := discoverEchoTool(t, &calls, &sawHeader)
	runnable, ok := discovered.(runnableTool)
	if !ok {
		t.Fatalf("tool %T is not runnable", discovered)
	}

	pending := &confirmationContext{StrictContextMock: agent.NewStrictContextMock(t.Context())}
	_, err := runnable.Run(pending, map[string]any{"text": "hello"})
	if !pending.requested || !errors.Is(err, tool.ErrConfirmationRequired) {
		t.Fatalf("requested=%v err=%v", pending.requested, err)
	}
	if calls.Load() != 0 {
		t.Fatal("server called before confirmation")
	}

	approved := &confirmationContext{
		StrictContextMock: agent.NewStrictContextMock(t.Context()),
		confirmation:      &toolconfirmation.ToolConfirmation{Confirmed: true},
	}
	result, err := runnable.Run(approved, map[string]any{"text": "hello"})
	if err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 || fmt.Sprint(result["output"]) == "" {
		t.Fatalf("calls=%d result=%#v", calls.Load(), result)
	}
}
