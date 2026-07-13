package openaiadapter

import (
	"context"
	"fmt"
	"iter"
	"net/url"
	"strings"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"google.golang.org/adk/v2/model"
)

type Config struct {
	BaseURL         string
	APIKey          string
	Model           string
	ContextWindow   int64
	MaxTokens       int64
	ThinkingMode    string
	ReasoningEffort string
}

type Model struct {
	client openai.Client
	cfg    Config
}

func New(cfg Config) (*Model, error) {
	cfg.BaseURL = strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	cfg.APIKey = strings.TrimSpace(cfg.APIKey)
	cfg.Model = strings.TrimSpace(cfg.Model)
	cfg.ThinkingMode = strings.ToLower(strings.TrimSpace(cfg.ThinkingMode))
	cfg.ReasoningEffort = strings.ToLower(strings.TrimSpace(cfg.ReasoningEffort))
	if cfg.ThinkingMode == "" {
		cfg.ThinkingMode = "auto"
	}
	u, err := url.ParseRequestURI(cfg.BaseURL)
	if err != nil || u.Scheme == "" || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return nil, fmt.Errorf("base URL must be an absolute HTTP(S) URL")
	}
	if cfg.APIKey == "" || cfg.Model == "" {
		return nil, fmt.Errorf("API key and model are required")
	}
	if cfg.ContextWindow <= 0 || cfg.MaxTokens <= 0 {
		return nil, fmt.Errorf("context window and max tokens must be positive")
	}
	switch cfg.ThinkingMode {
	case "auto", "enabled", "disabled":
	default:
		return nil, fmt.Errorf("thinking mode must be auto, enabled, or disabled")
	}
	switch cfg.ReasoningEffort {
	case "", "high", "max":
	default:
		return nil, fmt.Errorf("reasoning effort must be high or max when set")
	}
	if cfg.ThinkingMode == "disabled" && cfg.ReasoningEffort != "" {
		return nil, fmt.Errorf("reasoning effort must be empty when thinking mode is disabled")
	}
	return &Model{
		client: openai.NewClient(option.WithAPIKey(cfg.APIKey), option.WithBaseURL(cfg.BaseURL)),
		cfg:    cfg,
	}, nil
}

func (m *Model) Name() string { return m.cfg.Model }

func (m *Model) GenerateContent(ctx context.Context, req *model.LLMRequest, stream bool) iter.Seq2[*model.LLMResponse, error] {
	return func(yield func(*model.LLMResponse, error) bool) {
		params, capabilities, err := buildParams(req, m.cfg)
		if err != nil {
			yield(nil, err)
			return
		}
		if stream {
			m.generateStream(ctx, params, capabilities, yield)
			return
		}
		completion, err := m.client.Chat.Completions.New(ctx, params)
		if err != nil {
			yield(nil, m.providerError(err, capabilities))
			return
		}
		response, err := fromCompletion(completion)
		yield(response, err)
	}
}

func (m *Model) providerError(err error, capabilities []string) error {
	status := 0
	if apiErr, ok := err.(*openai.Error); ok {
		status = apiErr.StatusCode
	}
	message := strings.ReplaceAll(err.Error(), m.cfg.APIKey, "[REDACTED]")
	return &ProviderError{BaseURL: m.cfg.BaseURL, Model: m.cfg.Model, Status: status, Capabilities: capabilities, Message: message}
}
