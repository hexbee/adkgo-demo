package openaiadapter

import (
	"encoding/json"
	"strings"
	"testing"

	"google.golang.org/genai"
)

func marshalMap(t *testing.T, value any) map[string]any {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var result map[string]any
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatal(err)
	}
	return result
}

func TestConvertContentsTextRoles(t *testing.T) {
	got, err := convertContents([]*genai.Content{
		{Role: "user", Parts: []*genai.Part{{Text: "hello"}}},
		{Role: "model", Parts: []*genai.Part{{Text: "hi"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if marshalMap(t, got[0])["role"] != "user" || marshalMap(t, got[1])["role"] != "assistant" {
		t.Fatalf("unexpected roles: %#v", got)
	}
}

func TestConvertContentsImages(t *testing.T) {
	content := &genai.Content{Role: "user", Parts: []*genai.Part{
		{Text: "describe"},
		{FileData: &genai.FileData{FileURI: "https://images.test/cat.png", MIMEType: "image/png"}},
		{InlineData: &genai.Blob{MIMEType: "image/jpeg", Data: []byte{1, 2, 3}}},
	}}
	got, err := convertContents([]*genai.Content{content})
	if err != nil {
		t.Fatal(err)
	}
	parts := marshalMap(t, got[0])["content"].([]any)
	url := parts[2].(map[string]any)["image_url"].(map[string]any)["url"].(string)
	if !strings.HasPrefix(url, "data:image/jpeg;base64,") {
		t.Fatalf("inline image URL = %q", url)
	}
}

func TestConvertFunctionRoundTripMessages(t *testing.T) {
	got, err := convertContents([]*genai.Content{
		{Role: "model", Parts: []*genai.Part{{FunctionCall: &genai.FunctionCall{ID: "call-1", Name: "lookup", Args: map[string]any{"q": "x"}}}}},
		{Role: "user", Parts: []*genai.Part{{FunctionResponse: &genai.FunctionResponse{ID: "call-1", Name: "lookup", Response: map[string]any{"output": "y"}}}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if marshalMap(t, got[0])["tool_calls"] == nil || marshalMap(t, got[1])["tool_call_id"] != "call-1" {
		t.Fatalf("unexpected messages: %#v", got)
	}
}

func TestConvertRejectsUnsupportedMedia(t *testing.T) {
	_, err := convertContents([]*genai.Content{{Role: "user", Parts: []*genai.Part{{InlineData: &genai.Blob{MIMEType: "audio/wav", Data: []byte{1}}}}}})
	if err == nil {
		t.Fatal("expected conversion error")
	}
}
