package config

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"

	"github.com/joho/godotenv"
)

const (
	thinkingModeAuto     = "auto"
	thinkingModeEnabled  = "enabled"
	thinkingModeDisabled = "disabled"
)

type Config struct {
	BaseURL         string
	APIKey          string
	ModelName       string
	ContextWindow   int64
	MaxTokens       int64
	ThinkingMode    string
	ReasoningEffort string
}

func Load(path string) (Config, error) {
	if err := godotenv.Load(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return Config{}, fmt.Errorf("load dotenv: %w", err)
	}

	c := Config{
		BaseURL:         strings.TrimRight(strings.TrimSpace(os.Getenv("BASE_URL")), "/"),
		APIKey:          strings.TrimSpace(os.Getenv("API_KEY")),
		ModelName:       strings.TrimSpace(os.Getenv("MODEL_NAME")),
		ThinkingMode:    strings.ToLower(strings.TrimSpace(os.Getenv("THINKING_MODE"))),
		ReasoningEffort: strings.ToLower(strings.TrimSpace(os.Getenv("REASONING_EFFORT"))),
	}
	if c.ThinkingMode == "" {
		c.ThinkingMode = thinkingModeAuto
	}
	for name, target := range map[string]*int64{
		"CONTEXT_WINDOW": &c.ContextWindow,
		"MAX_TOKENS":     &c.MaxTokens,
	} {
		value := strings.TrimSpace(os.Getenv(name))
		parsed, err := strconv.ParseInt(value, 10, 64)
		if err != nil || parsed <= 0 {
			return Config{}, fmt.Errorf("%s must be a positive integer", name)
		}
		*target = parsed
	}
	for name, value := range map[string]string{
		"BASE_URL": c.BaseURL, "API_KEY": c.APIKey, "MODEL_NAME": c.ModelName,
	} {
		if value == "" {
			return Config{}, fmt.Errorf("%s is required", name)
		}
	}
	switch c.ThinkingMode {
	case thinkingModeAuto, thinkingModeEnabled, thinkingModeDisabled:
	default:
		return Config{}, fmt.Errorf("THINKING_MODE must be auto, enabled, or disabled")
	}
	switch c.ReasoningEffort {
	case "", "high", "max":
	default:
		return Config{}, fmt.Errorf("REASONING_EFFORT must be high or max when set")
	}
	if c.ThinkingMode == thinkingModeDisabled && c.ReasoningEffort != "" {
		return Config{}, fmt.Errorf("REASONING_EFFORT must be empty when THINKING_MODE is disabled")
	}
	u, err := url.ParseRequestURI(c.BaseURL)
	if err != nil || u.Scheme == "" || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return Config{}, fmt.Errorf("BASE_URL must be an absolute HTTP(S) URL")
	}
	return c, nil
}

func (c Config) SafeSummary() string {
	summary := fmt.Sprintf(
		"model=%s base_url=%s context_window=%d max_tokens=%d thinking_mode=%s",
		c.ModelName, c.BaseURL, c.ContextWindow, c.MaxTokens, c.ThinkingMode,
	)
	if c.ReasoningEffort != "" {
		summary += " reasoning_effort=" + c.ReasoningEffort
	}
	return summary
}
