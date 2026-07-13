package openaiadapter

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"google.golang.org/adk/v2/model"
	"google.golang.org/genai"
)

func newTestModel(t *testing.T, handler http.HandlerFunc) *Model {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	m, err := New(Config{BaseURL: server.URL + "/v1", APIKey: "test-secret-key", Model: "test-model", ContextWindow: 1000, MaxTokens: 100})
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func testRequest() *model.LLMRequest {
	return &model.LLMRequest{Contents: []*genai.Content{{Role: "user", Parts: []*genai.Part{{Text: "hello"}}}}, Config: &genai.GenerateContentConfig{}}
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
	if got == nil || len(got.Content.Parts) != 1 || got.Content.Parts[0].FunctionCall.Name != "lookup_time" {
		t.Fatalf("response = %#v", got)
	}
	if got.CustomMetadata["reasoning_content"] != "checked inputs" || got.UsageMetadata.TotalTokenCount != 14 {
		t.Fatalf("metadata = %#v usage = %#v", got.CustomMetadata, got.UsageMetadata)
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
