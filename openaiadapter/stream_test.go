package openaiadapter

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"google.golang.org/adk/v2/model"
)

func TestStreamTextAndUsage(t *testing.T) {
	m := newTestModel(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		for _, data := range []string{
			`{"id":"s1","object":"chat.completion.chunk","created":1,"model":"m","choices":[{"index":0,"delta":{"role":"assistant","reasoning_content":"think "},"finish_reason":null}]}`,
			`{"id":"s1","object":"chat.completion.chunk","created":1,"model":"m","choices":[{"index":0,"delta":{"content":"hello"},"finish_reason":null}]}`,
			`{"id":"s1","object":"chat.completion.chunk","created":1,"model":"m","choices":[{"index":0,"delta":{"content":" world"},"finish_reason":"stop"}]}`,
			`{"id":"s1","object":"chat.completion.chunk","created":1,"model":"m","choices":[],"usage":{"prompt_tokens":3,"completion_tokens":2,"total_tokens":5}}`,
		} {
			fmt.Fprintf(w, "data: %s\n\n", data)
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
	})
	var responses []*model.LLMResponse
	for response, err := range m.GenerateContent(context.Background(), testRequest(), true) {
		if err != nil {
			t.Fatal(err)
		}
		responses = append(responses, response)
	}
	if len(responses) != 3 || !responses[0].Partial || !responses[2].TurnComplete {
		t.Fatalf("responses = %#v", responses)
	}
	if responses[2].Content.Parts[0].Text != "hello world" || responses[2].UsageMetadata.TotalTokenCount != 5 {
		t.Fatalf("final = %#v", responses[2])
	}
	if responses[2].CustomMetadata["reasoning_content"] != "think " {
		t.Fatalf("metadata = %#v", responses[2].CustomMetadata)
	}
}

func TestStreamToolCallFragments(t *testing.T) {
	m := newTestModel(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		chunks := []string{
			`{"id":"s1","object":"chat.completion.chunk","created":1,"model":"m","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call-1","type":"function","function":{"name":"lookup_time","arguments":"{\"timezone\":"}}]},"finish_reason":null}]}`,
			`{"id":"s1","object":"chat.completion.chunk","created":1,"model":"m","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"Asia/Shanghai\"}"}}]},"finish_reason":"tool_calls"}]}`,
		}
		for _, chunk := range chunks {
			fmt.Fprintf(w, "data: %s\n\n", chunk)
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
	})
	var final *model.LLMResponse
	for response, err := range m.GenerateContent(context.Background(), testRequest(), true) {
		if err != nil {
			t.Fatal(err)
		}
		final = response
	}
	if final == nil || final.Content.Parts[0].FunctionCall.Name != "lookup_time" {
		t.Fatalf("final = %#v", final)
	}
}
