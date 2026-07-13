package openaiadapter

import (
	"testing"

	"google.golang.org/adk/v2/model"
	"google.golang.org/genai"
)

func TestBuildParamsMaxTokensPrecedence(t *testing.T) {
	for _, tc := range []struct {
		adk            int32
		fallback, want int64
	}{{0, 384000, 384000}, {2048, 384000, 2048}, {400000, 384000, 384000}} {
		req := &model.LLMRequest{Contents: []*genai.Content{{Role: "user", Parts: []*genai.Part{{Text: "hi"}}}}, Config: &genai.GenerateContentConfig{MaxOutputTokens: tc.adk}}
		got, _, err := buildParams(req, Config{Model: "test-model", MaxTokens: tc.fallback, ThinkingMode: "auto"})
		if err != nil {
			t.Fatal(err)
		}
		payload := marshalMap(t, got)
		if int64(payload["max_tokens"].(float64)) != tc.want {
			t.Fatalf("max_tokens = %v, want %d", payload["max_tokens"], tc.want)
		}
	}
}

func TestBuildParamsToolsAndJSONSchema(t *testing.T) {
	req := &model.LLMRequest{Contents: []*genai.Content{{Role: "user", Parts: []*genai.Part{{Text: "hi"}}}}, Config: &genai.GenerateContentConfig{
		Tools:            []*genai.Tool{{FunctionDeclarations: []*genai.FunctionDeclaration{{Name: "lookup", Description: "look up", ParametersJsonSchema: map[string]any{"type": "object"}}}}},
		ResponseMIMEType: "application/json", ResponseJsonSchema: map[string]any{"type": "object"},
	}}
	got, capabilities, err := buildParams(req, Config{Model: "test-model", MaxTokens: 100, ThinkingMode: "auto"})
	if err != nil {
		t.Fatal(err)
	}
	payload := marshalMap(t, got)
	if len(payload["tools"].([]any)) != 1 || payload["response_format"].(map[string]any)["type"] != "json_schema" {
		t.Fatalf("payload = %#v", payload)
	}
	if len(capabilities) != 2 || capabilities[0] != "json_schema" || capabilities[1] != "tools" {
		t.Fatalf("capabilities = %#v", capabilities)
	}
}

func TestBuildParamsThinkingConfiguration(t *testing.T) {
	for _, tc := range []struct {
		name, mode, effort string
		wantThinking       any
		wantEffort         string
	}{
		{name: "auto", mode: "auto", wantThinking: nil},
		{name: "enabled high", mode: "enabled", effort: "high", wantThinking: "enabled", wantEffort: "high"},
		{name: "disabled", mode: "disabled", wantThinking: "disabled"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, _, err := buildParams(testRequest(), Config{
				Model: "test-model", MaxTokens: 100,
				ThinkingMode: tc.mode, ReasoningEffort: tc.effort,
			})
			if err != nil {
				t.Fatal(err)
			}
			payload := marshalMap(t, got)
			thinking, exists := payload["thinking"]
			if tc.wantThinking == nil {
				if exists {
					t.Fatalf("thinking unexpectedly present: %#v", thinking)
				}
			} else if thinking.(map[string]any)["type"] != tc.wantThinking {
				t.Fatalf("thinking = %#v", thinking)
			}
			if tc.wantEffort == "" {
				if _, exists := payload["reasoning_effort"]; exists {
					t.Fatalf("reasoning_effort unexpectedly present: %#v", payload)
				}
			} else if payload["reasoning_effort"] != tc.wantEffort {
				t.Fatalf("reasoning_effort = %#v", payload["reasoning_effort"])
			}
		})
	}
}
