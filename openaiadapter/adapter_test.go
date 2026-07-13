package openaiadapter

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/openai/openai-go/v3"
	"google.golang.org/adk/v2/model"
	"google.golang.org/genai"
)

func newTestModel(t *testing.T, handler http.HandlerFunc) *Model {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	m, err := New(validAdapterConfig(server.URL + "/v1"))
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func validAdapterConfig(baseURL string) Config {
	return Config{
		BaseURL: baseURL, APIKey: "test-secret-key", Model: "test-model",
		ContextWindow: 1000, MaxTokens: 100,
	}
}

func testRequest() *model.LLMRequest {
	return &model.LLMRequest{Contents: []*genai.Content{{Role: "user", Parts: []*genai.Part{{Text: "hello"}}}}, Config: &genai.GenerateContentConfig{}}
}

func TestNewValidatesThinkingConfiguration(t *testing.T) {
	for _, tc := range []struct {
		name, mode, effort, wantMode, wantEffort string
	}{
		{"default", "", "", "auto", ""},
		{"enabled", " ENABLED ", " HIGH ", "enabled", "high"},
		{"disabled", "disabled", "", "disabled", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := validAdapterConfig("https://example.test/v1")
			cfg.ThinkingMode, cfg.ReasoningEffort = tc.mode, tc.effort
			got, err := New(cfg)
			if err != nil {
				t.Fatal(err)
			}
			if got.cfg.ThinkingMode != tc.wantMode || got.cfg.ReasoningEffort != tc.wantEffort {
				t.Fatalf("config = %#v", got.cfg)
			}
		})
	}

	invalid := []Config{
		validAdapterConfig("https://example.test/v1"),
		validAdapterConfig("https://example.test/v1"),
		validAdapterConfig("https://example.test/v1"),
	}
	invalid[0].ThinkingMode = "sometimes"
	invalid[1].ReasoningEffort = "medium"
	invalid[2].ThinkingMode, invalid[2].ReasoningEffort = "disabled", "high"
	for _, cfg := range invalid {
		if _, err := New(cfg); err == nil {
			t.Fatalf("New(%#v) succeeded", cfg)
		}
	}
}

func TestModelNonStreamingToolCall(t *testing.T) {
	m := newTestModel(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("path = %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer test-secret-key" {
			t.Error("missing authorization")
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":"chat-1","object":"chat.completion","created":1,"model":"provider-model","choices":[{"index":0,"finish_reason":"tool_calls","message":{"role":"assistant","content":"","reasoning_content":"checked inputs","tool_calls":[{"id":"call-1","type":"function","function":{"name":"lookup_time","arguments":"{\"timezone\":\"Asia/Shanghai\"}"}}]}}],"usage":{"prompt_tokens":10,"completion_tokens":4,"total_tokens":14}}`)
	})
	var got *model.LLMResponse
	for response, err := range m.GenerateContent(context.Background(), testRequest(), false) {
		if err != nil {
			t.Fatal(err)
		}
		got = response
	}
	if got == nil || len(got.Content.Parts) != 2 {
		t.Fatalf("response = %#v", got)
	}
	thought := got.Content.Parts[0]
	call := got.Content.Parts[1].FunctionCall
	if thought.Text != "checked inputs" || !thought.Thought {
		t.Fatalf("thought = %#v", thought)
	}
	if call == nil || call.Name != "lookup_time" {
		t.Fatalf("call = %#v", call)
	}
	if got.CustomMetadata["reasoning_content"] != "checked inputs" || got.UsageMetadata.TotalTokenCount != 14 {
		t.Fatalf("metadata = %#v usage = %#v", got.CustomMetadata, got.UsageMetadata)
	}
}

func TestFromCompletionOrdersThoughtTextAndToolCall(t *testing.T) {
	completion := &openai.ChatCompletion{}
	if err := json.Unmarshal([]byte(`{
		"model":"m","choices":[{"finish_reason":"tool_calls","message":{
			"role":"assistant","reasoning_content":"reason","content":"visible",
			"tool_calls":[{"id":"call-1","type":"function","function":{"name":"lookup","arguments":"{}"}}]
		}}],"usage":{"prompt_tokens":1,"completion_tokens":2,"total_tokens":3}
	}`), completion); err != nil {
		t.Fatal(err)
	}
	got, err := fromCompletion(completion)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Content.Parts) != 3 || !got.Content.Parts[0].Thought || got.Content.Parts[0].Text != "reason" {
		t.Fatalf("parts = %#v", got.Content.Parts)
	}
	if got.Content.Parts[1].Thought || got.Content.Parts[1].Text != "visible" {
		t.Fatalf("visible = %#v", got.Content.Parts[1])
	}
	if got.Content.Parts[2].FunctionCall == nil || got.Content.Parts[2].FunctionCall.Name != "lookup" {
		t.Fatalf("call = %#v", got.Content.Parts[2])
	}
}

func TestFromCompletionWithoutReasoning(t *testing.T) {
	completion := &openai.ChatCompletion{}
	if err := json.Unmarshal([]byte(`{
		"model":"m","choices":[{"finish_reason":"stop","message":{"role":"assistant","content":"visible"}}],
		"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}
	}`), completion); err != nil {
		t.Fatal(err)
	}
	got, err := fromCompletion(completion)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Content.Parts) != 1 || got.Content.Parts[0].Thought || got.CustomMetadata != nil {
		t.Fatalf("response = %#v", got)
	}
}

func TestProviderErrorRedactsKey(t *testing.T) {
	m := newTestModel(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, `{"error":{"message":"rejected test-secret-key"}}`)
	})
	for _, err := range m.GenerateContent(context.Background(), testRequest(), false) {
		if err == nil || strings.Contains(err.Error(), "test-secret-key") || !strings.Contains(err.Error(), "status=400") {
			t.Fatalf("err = %v", err)
		}
	}
}
