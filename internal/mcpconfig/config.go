package mcpconfig

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/url"
	"os"
	"sort"
	"strings"
)

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

func Load(path string) (Result, error) {
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return Result{}, nil
	}
	if err != nil {
		return Result{}, fmt.Errorf("open MCP configuration: %w", err)
	}
	defer file.Close()

	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()
	var raw fileConfig
	if err := decoder.Decode(&raw); err != nil {
		return Result{}, fmt.Errorf("parse MCP configuration: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return Result{}, fmt.Errorf("parse MCP configuration: multiple JSON values")
		}
		return Result{}, fmt.Errorf("parse MCP configuration: %w", err)
	}

	names := make([]string, 0, len(raw.MCPServers))
	for name := range raw.MCPServers {
		names = append(names, name)
	}
	sort.Strings(names)
	result := Result{Found: true, Servers: make([]Server, 0, len(names))}
	for _, name := range names {
		entry := raw.MCPServers[name]
		if strings.TrimSpace(name) == "" {
			return Result{}, fmt.Errorf("MCP server name must not be empty")
		}
		if entry.Type != "http" {
			return Result{}, fmt.Errorf("MCP server %q: type must be http", name)
		}
		parsed, err := url.ParseRequestURI(entry.URL)
		if err != nil || parsed.Scheme == "" || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
			return Result{}, fmt.Errorf("MCP server %q: URL must be an absolute HTTP(S) URL", name)
		}
		result.Servers = append(result.Servers, Server{Name: name, URL: entry.URL, Headers: maps.Clone(entry.Headers)})
	}
	return result, nil
}

func (s Server) SafeEndpoint() string {
	parsed, err := url.Parse(s.URL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return s.Name
	}
	parsed.RawQuery = ""
	parsed.ForceQuery = false
	parsed.Fragment = ""
	parsed.User = nil
	return parsed.String()
}
