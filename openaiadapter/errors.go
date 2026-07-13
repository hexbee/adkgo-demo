package openaiadapter

import (
	"fmt"
	"strings"
)

type ConversionError struct {
	Path string
	Kind string
}

type ProviderError struct {
	BaseURL      string
	Model        string
	Status       int
	Capabilities []string
	Message      string
}

func (e *ProviderError) Error() string {
	status := ""
	if e.Status != 0 {
		status = fmt.Sprintf(" status=%d", e.Status)
	}
	capabilities := ""
	if len(e.Capabilities) > 0 {
		capabilities = " capabilities=" + strings.Join(e.Capabilities, ",")
	}
	return fmt.Sprintf("OpenAI-compatible request failed: model=%s base_url=%s%s%s: %s", e.Model, e.BaseURL, status, capabilities, e.Message)
}

func (e *ConversionError) Error() string {
	return fmt.Sprintf("cannot convert %s at %s", e.Kind, e.Path)
}
