package mcpruntime

import (
	"fmt"
	"net/http"

	"github.com/hexbee/adkgo-demo/internal/mcpconfig"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"google.golang.org/adk/v2/tool"
	"google.golang.org/adk/v2/tool/mcptoolset"
)

type headerTransport struct {
	base    http.RoundTripper
	headers http.Header
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
		result = append(result, toolset)
	}
	return result, nil
}
