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
	if len(responses) != 4 {
		t.Fatalf("response count = %d", len(responses))
	}
	if !responses[0].Partial || !responses[0].Content.Parts[0].Thought || responses[0].Content.Parts[0].Text != "think " {
		t.Fatalf("thought partial = %#v", responses[0])
	}
	if responses[1].Content.Parts[0].Thought || responses[1].Content.Parts[0].Text != "hello" {
		t.Fatalf("visible partial = %#v", responses[1])
	}
	final := responses[3]
	if !final.TurnComplete || len(final.Content.Parts) != 2 {
		t.Fatalf("final = %#v", final)
	}
	if !final.Content.Parts[0].Thought || final.Content.Parts[0].Text != "think " || final.Content.Parts[1].Text != "hello world" {
		t.Fatalf("final parts = %#v", final.Content.Parts)
	}
	if final.UsageMetadata.TotalTokenCount != 5 || final.CustomMetadata["reasoning_content"] != "think " {
		t.Fatalf("final metadata = %#v, usage = %#v", final.CustomMetadata, final.UsageMetadata)
	}
}

func TestStreamToolCallFragments(t *testing.T) {
	m := newTestModel(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		chunks := []string{
			`{"id":"s1","object":"chat.completion.chunk","created":1,"model":"m","choices":[{"index":0,"delta":{"reasoning_content":"choose tool"},"finish_reason":null}]}`,
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
	if final == nil || len(final.Content.Parts) != 2 {
		t.Fatalf("final = %#v", final)
	}
	if !final.Content.Parts[0].Thought || final.Content.Parts[0].Text != "choose tool" {
		t.Fatalf("thought = %#v", final.Content.Parts[0])
	}
	if final.Content.Parts[1].FunctionCall == nil || final.Content.Parts[1].FunctionCall.Name != "lookup_time" {
		t.Fatalf("call = %#v", final.Content.Parts[1])
	}
}

func TestStreamWithoutReasoning(t *testing.T) {
	m := newTestModel(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"id\":\"s1\",\"object\":\"chat.completion.chunk\",\"created\":1,\"model\":\"m\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"hello\"},\"finish_reason\":\"stop\"}]}\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	})
	var responses []*model.LLMResponse
	for response, err := range m.GenerateContent(t.Context(), testRequest(), true) {
		if err != nil {
			t.Fatal(err)
		}
		responses = append(responses, response)
	}
	if len(responses) != 2 || !responses[0].Partial || responses[0].Content.Parts[0].Thought {
		t.Fatalf("responses = %#v", responses)
	}
	final := responses[1]
	if final.Content.Parts[0].Thought || final.Content.Parts[0].Text != "hello" || final.CustomMetadata != nil {
		t.Fatalf("final = %#v", final)
	}
}
