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

type Config struct {
	BaseURL       string
	APIKey        string
	ModelName     string
	ContextWindow int64
	MaxTokens     int64
}

func Load(path string) (Config, error) {
	if err := godotenv.Load(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return Config{}, fmt.Errorf("load dotenv: %w", err)
	}

	c := Config{
		BaseURL:   strings.TrimRight(strings.TrimSpace(os.Getenv("BASE_URL")), "/"),
		APIKey:    strings.TrimSpace(os.Getenv("API_KEY")),
		ModelName: strings.TrimSpace(os.Getenv("MODEL_NAME")),
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
	u, err := url.ParseRequestURI(c.BaseURL)
	if err != nil || u.Scheme == "" || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return Config{}, fmt.Errorf("BASE_URL must be an absolute HTTP(S) URL")
	}
	return c, nil
}

func (c Config) SafeSummary() string {
	return fmt.Sprintf("model=%s base_url=%s context_window=%d max_tokens=%d", c.ModelName, c.BaseURL, c.ContextWindow, c.MaxTokens)
}
