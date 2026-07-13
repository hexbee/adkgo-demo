package openaiadapter

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/openai/openai-go/v3"
	"google.golang.org/adk/v2/model"
	"google.golang.org/genai"
)

func fromCompletion(completion *openai.ChatCompletion) (*model.LLMResponse, error) {
	if completion == nil || len(completion.Choices) == 0 {
		return nil, fmt.Errorf("provider returned no completion choices")
	}
	choice := completion.Choices[0]
	reasoning := extractReasoning(choice.Message.RawJSON())
	parts := make([]*genai.Part, 0, 2+len(choice.Message.ToolCalls))
	if reasoning != "" {
		parts = append(parts, &genai.Part{Text: reasoning, Thought: true})
	}
	if choice.Message.Content != "" {
		parts = append(parts, &genai.Part{Text: choice.Message.Content})
	}
	for _, toolCall := range choice.Message.ToolCalls {
		if toolCall.Type != "function" {
			return nil, &ConversionError{Path: "response.choices[0].message.tool_calls", Kind: "custom tool call"}
		}
		args, err := decodeArguments(toolCall.Function.Arguments)
		if err != nil {
			return nil, fmt.Errorf("decode arguments for %s: %w", toolCall.Function.Name, err)
		}
		parts = append(parts, &genai.Part{FunctionCall: &genai.FunctionCall{ID: toolCall.ID, Name: toolCall.Function.Name, Args: args}})
	}
	metadata := map[string]any{}
	if reasoning != "" {
		metadata["reasoning_content"] = reasoning
	}
	if len(metadata) == 0 {
		metadata = nil
	}
	return &model.LLMResponse{
		Content: &genai.Content{Role: "model", Parts: parts},
		UsageMetadata: &genai.GenerateContentResponseUsageMetadata{
			PromptTokenCount: int32(completion.Usage.PromptTokens), CandidatesTokenCount: int32(completion.Usage.CompletionTokens), TotalTokenCount: int32(completion.Usage.TotalTokens),
		},
		CustomMetadata: metadata,
		ModelVersion:   completion.Model,
		FinishReason:   mapFinishReason(choice.FinishReason),
		TurnComplete:   true,
	}, nil
}

func decodeArguments(value string) (map[string]any, error) {
	decoder := json.NewDecoder(bytes.NewBufferString(value))
	decoder.UseNumber()
	var result map[string]any
	if err := decoder.Decode(&result); err != nil {
		return nil, err
	}
	if result == nil {
		return nil, fmt.Errorf("arguments must be a JSON object")
	}
	return result, nil
}

func extractReasoning(raw string) string {
	var value struct {
		Reasoning string `json:"reasoning_content"`
	}
	_ = json.Unmarshal([]byte(raw), &value)
	return value.Reasoning
}

func mapFinishReason(reason string) genai.FinishReason {
	switch reason {
	case "stop", "tool_calls", "function_call":
		return genai.FinishReasonStop
	case "length":
		return genai.FinishReasonMaxTokens
	case "content_filter":
		return genai.FinishReasonSafety
	case "":
		return genai.FinishReasonUnspecified
	default:
		return genai.FinishReasonOther
	}
}
