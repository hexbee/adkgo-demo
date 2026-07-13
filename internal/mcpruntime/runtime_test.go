package mcpruntime

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/hexbee/adkgo-demo/internal/mcpconfig"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/session"
	"google.golang.org/adk/v2/tool"
	"google.golang.org/adk/v2/tool/toolconfirmation"
)

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
	toolsets, err := Build([]mcpconfig.Server{{Name: "test", URL: server.URL, Headers: map[string]string{"X-Test-Key": "test-secret"}}})
	if err != nil {
		t.Fatal(err)
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
