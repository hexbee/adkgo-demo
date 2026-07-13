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
		got, _, err := buildParams(req, "test-model", tc.fallback)
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
	got, capabilities, err := buildParams(req, "test-model", 100)
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
