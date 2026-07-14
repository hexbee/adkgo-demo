package mcpconfig

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/url"
	"os"
	"slices"
	"sort"
	"strings"
)

const (
	TypeHTTP  = "http"
	TypeStdio = "stdio"
)

type fileConfig struct {
	MCPServers map[string]serverConfig `json:"mcpServers"`
}

type serverConfig struct {
	Type    string            `json:"type"`
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers,omitempty"`
	Command string            `json:"command,omitempty"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
	CWD     string            `json:"cwd,omitempty"`
}

type Server struct {
	Name    string
	Type    string
	URL     string
	Headers map[string]string
	Command string
	Args    []string
	Env     map[string]string
	CWD     string
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
		if entry.Type == "" && entry.Command != "" {
			entry.Type = TypeStdio
		}
		var err error
		entry, err = expandServer(entry, os.LookupEnv)
		if err != nil {
			return Result{}, fmt.Errorf("MCP server %q: expand configuration: %w", name, err)
		}
		if err := validateServer(name, entry); err != nil {
			return Result{}, err
		}
		result.Servers = append(result.Servers, Server{
			Name: name, Type: entry.Type, URL: entry.URL, Headers: maps.Clone(entry.Headers),
			Command: entry.Command, Args: slices.Clone(entry.Args), Env: maps.Clone(entry.Env), CWD: entry.CWD,
		})
	}
	return result, nil
}

func expandServer(entry serverConfig, lookup func(string) (string, bool)) (serverConfig, error) {
	fields := []*string{&entry.URL, &entry.Command, &entry.CWD}
	for _, field := range fields {
		expanded, err := expandString(*field, lookup)
		if err != nil {
			return serverConfig{}, err
		}
		*field = expanded
	}
	for i, arg := range entry.Args {
		expanded, err := expandString(arg, lookup)
		if err != nil {
			return serverConfig{}, err
		}
		entry.Args[i] = expanded
	}
	for name, value := range entry.Headers {
		expanded, err := expandString(value, lookup)
		if err != nil {
			return serverConfig{}, err
		}
		entry.Headers[name] = expanded
	}
	for name, value := range entry.Env {
		expanded, err := expandString(value, lookup)
		if err != nil {
			return serverConfig{}, err
		}
		entry.Env[name] = expanded
	}
	return entry, nil
}

func expandString(value string, lookup func(string) (string, bool)) (string, error) {
	var result strings.Builder
	for {
		start := strings.Index(value, "${")
		if start < 0 {
			result.WriteString(value)
			return result.String(), nil
		}
		result.WriteString(value[:start])
		value = value[start+2:]
		end := strings.IndexByte(value, '}')
		if end < 0 {
			return "", fmt.Errorf("unterminated environment variable reference")
		}
		expression := value[:end]
		value = value[end+1:]

		name, fallback, hasFallback := expression, "", false
		if before, after, ok := strings.Cut(expression, ":-"); ok {
			name, fallback, hasFallback = before, after, true
		}
		if !validEnvName(name) {
			return "", fmt.Errorf("invalid environment variable reference")
		}
		replacement, found := lookup(name)
		if hasFallback && (!found || replacement == "") {
			replacement, found = fallback, true
		}
		if !found {
			return "", fmt.Errorf("environment variable %s is not set", name)
		}
		result.WriteString(replacement)
	}
}

func validEnvName(name string) bool {
	if name == "" {
		return false
	}
	for i, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || r == '_' || (i > 0 && r >= '0' && r <= '9') {
			continue
		}
		return false
	}
	return true
}

func validateServer(name string, entry serverConfig) error {
	switch entry.Type {
	case TypeHTTP:
		if entry.Command != "" || entry.Args != nil || entry.Env != nil || entry.CWD != "" {
			return fmt.Errorf("MCP server %q: HTTP configuration cannot contain command, args, env, or cwd", name)
		}
		parsed, err := url.ParseRequestURI(entry.URL)
		if err != nil || parsed.Scheme == "" || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
			return fmt.Errorf("MCP server %q: URL must be an absolute HTTP(S) URL", name)
		}
	case TypeStdio:
		if entry.URL != "" || entry.Headers != nil {
			return fmt.Errorf("MCP server %q: stdio configuration cannot contain url or headers", name)
		}
		if strings.TrimSpace(entry.Command) == "" || strings.ContainsRune(entry.Command, 0) {
			return fmt.Errorf("MCP server %q: stdio command must not be empty or contain NUL", name)
		}
		if strings.ContainsRune(entry.CWD, 0) {
			return fmt.Errorf("MCP server %q: stdio cwd must not contain NUL", name)
		}
		for _, arg := range entry.Args {
			if strings.ContainsRune(arg, 0) {
				return fmt.Errorf("MCP server %q: stdio args must not contain NUL", name)
			}
		}
		for key, value := range entry.Env {
			if key == "" || strings.Contains(key, "=") || strings.ContainsRune(key, 0) || strings.ContainsRune(value, 0) {
				return fmt.Errorf("MCP server %q: stdio env contains an invalid name or value", name)
			}
		}
	default:
		return fmt.Errorf("MCP server %q: type must be http or stdio", name)
	}
	return nil
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

func (s Server) SafeTarget() string {
	if s.Type == TypeStdio {
		return "stdio"
	}
	return s.SafeEndpoint()
}
