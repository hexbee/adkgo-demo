package mcpruntime

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os/exec"
	"sort"
	"strings"
	"unicode"

	"github.com/hexbee/adkgo-demo/internal/mcpconfig"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/tool"
	"google.golang.org/adk/v2/tool/mcptoolset"
)

type headerTransport struct {
	base    http.RoundTripper
	headers http.Header
}

type stdioTransport struct {
	command string
	args    []string
	env     map[string]string
	cwd     string
}

type namedToolset struct {
	name         string
	safeEndpoint string
	inner        tool.Toolset
}

func (t *namedToolset) Name() string { return t.name }

func (t *namedToolset) Tools(ctx agent.ReadonlyContext) ([]tool.Tool, error) {
	tools, err := t.inner.Tools(ctx)
	if err != nil {
		return nil, fmt.Errorf("MCP tool discovery for %s (%s): %w", t.name, t.safeEndpoint, err)
	}
	return tools, nil
}

func (t *headerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	base := t.base
	if base == nil {
		base = http.DefaultTransport
	}
	clone := req.Clone(req.Context())
	clone.Header = req.Header.Clone()
	for name, values := range t.headers {
		clone.Header.Del(name)
		for _, value := range values {
			clone.Header.Add(name, value)
		}
	}
	return base.RoundTrip(clone)
}

func (t *stdioTransport) Connect(ctx context.Context) (mcp.Connection, error) {
	cmd := exec.Command(t.command, t.args...)
	cmd.Dir = t.cwd
	if len(t.env) > 0 {
		env := cmd.Environ()
		keys := make([]string, 0, len(t.env))
		for key := range t.env {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			env = append(env, key+"="+t.env[key])
		}
		cmd.Env = env
	}
	connection, err := (&mcp.CommandTransport{Command: cmd}).Connect(ctx)
	if err != nil {
		return nil, errors.New("failed to start stdio MCP server process")
	}
	return connection, nil
}

func Build(servers []mcpconfig.Server) ([]tool.Toolset, error) {
	result := make([]tool.Toolset, 0, len(servers))
	for _, server := range servers {
		transport, err := buildTransport(server)
		if err != nil {
			return nil, err
		}
		toolset, err := mcptoolset.New(mcptoolset.Config{Transport: transport, RequireConfirmation: true})
		if err != nil {
			return nil, fmt.Errorf("create MCP toolset for %s (%s): %w", server.Name, server.SafeTarget(), err)
		}
		result = append(result, &namedToolset{name: toolsetName(server.Name), safeEndpoint: server.SafeTarget(), inner: toolset})
	}
	return result, nil
}

func buildTransport(server mcpconfig.Server) (mcp.Transport, error) {
	switch server.Type {
	case mcpconfig.TypeHTTP:
		headers := make(http.Header, len(server.Headers))
		for name, value := range server.Headers {
			headers.Set(name, value)
		}
		client := &http.Client{Transport: &headerTransport{base: http.DefaultTransport, headers: headers}}
		return &mcp.StreamableClientTransport{Endpoint: server.URL, HTTPClient: client}, nil
	case mcpconfig.TypeStdio:
		return &stdioTransport{command: server.Command, args: server.Args, env: server.Env, cwd: server.CWD}, nil
	default:
		return nil, fmt.Errorf("create MCP transport for %s: unsupported type", server.Name)
	}
}

func toolsetName(serverName string) string {
	var result strings.Builder
	result.WriteString("mcp_")
	for _, r := range serverName {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' {
			result.WriteRune(r)
		} else {
			result.WriteByte('_')
		}
	}
	return result.String()
}
