package mcpruntime

import (
	"fmt"
	"net/http"
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

func Build(servers []mcpconfig.Server) ([]tool.Toolset, error) {
	result := make([]tool.Toolset, 0, len(servers))
	for _, server := range servers {
		headers := make(http.Header, len(server.Headers))
		for name, value := range server.Headers {
			headers.Set(name, value)
		}
		client := &http.Client{Transport: &headerTransport{base: http.DefaultTransport, headers: headers}}
		transport := &mcp.StreamableClientTransport{Endpoint: server.URL, HTTPClient: client}
		toolset, err := mcptoolset.New(mcptoolset.Config{Transport: transport, RequireConfirmation: true})
		if err != nil {
			return nil, fmt.Errorf("create MCP toolset for %s (%s): %w", server.Name, server.SafeEndpoint(), err)
		}
		result = append(result, &namedToolset{name: toolsetName(server.Name), safeEndpoint: server.SafeEndpoint(), inner: toolset})
	}
	return result, nil
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
