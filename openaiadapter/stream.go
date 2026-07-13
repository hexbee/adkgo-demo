package openaiadapter

import (
	"context"
	"fmt"
	"strings"

	"github.com/openai/openai-go/v3"
	"google.golang.org/adk/v2/model"
	"google.golang.org/genai"
)

type toolCallAccumulator struct {
	ID        string
	Name      string
	Arguments strings.Builder
}

func (m *Model) generateStream(ctx context.Context, params openai.ChatCompletionNewParams, capabilities []string, yield func(*model.LLMResponse, error) bool) {
	params.StreamOptions.IncludeUsage = openai.Bool(true)
	stream := m.client.Chat.Completions.NewStreaming(ctx, params)
	defer stream.Close()

	var text, reasoning strings.Builder
	toolCalls := map[int64]*toolCallAccumulator{}
	modelVersion := ""
	finish := genai.FinishReasonUnspecified
	var usage *genai.GenerateContentResponseUsageMetadata

	for stream.Next() {
		chunk := stream.Current()
		if chunk.Model != "" {
			modelVersion = chunk.Model
		}
		if chunk.Usage.TotalTokens > 0 {
			usage = &genai.GenerateContentResponseUsageMetadata{
				PromptTokenCount: int32(chunk.Usage.PromptTokens), CandidatesTokenCount: int32(chunk.Usage.CompletionTokens), TotalTokenCount: int32(chunk.Usage.TotalTokens),
			}
		}
		for _, choice := range chunk.Choices {
			if choice.FinishReason != "" {
				finish = mapFinishReason(choice.FinishReason)
			}
			if deltaReasoning := extractReasoning(choice.Delta.RawJSON()); deltaReasoning != "" {
				reasoning.WriteString(deltaReasoning)
			}
			if choice.Delta.Content != "" {
				text.WriteString(choice.Delta.Content)
				if !yield(&model.LLMResponse{
					Content:      &genai.Content{Role: "model", Parts: []*genai.Part{{Text: choice.Delta.Content}}},
					ModelVersion: modelVersion, Partial: true,
				}, nil) {
					return
				}
			}
			for _, delta := range choice.Delta.ToolCalls {
				acc := toolCalls[delta.Index]
				if acc == nil {
					acc = &toolCallAccumulator{}
					toolCalls[delta.Index] = acc
				}
				if delta.ID != "" {
					acc.ID = delta.ID
				}
				if delta.Function.Name != "" {
					acc.Name += delta.Function.Name
				}
				acc.Arguments.WriteString(delta.Function.Arguments)
			}
		}
	}
	if err := stream.Err(); err != nil {
		yield(nil, m.providerError(err, capabilities))
		return
	}

	parts := make([]*genai.Part, 0, 1+len(toolCalls))
	if text.Len() > 0 {
		parts = append(parts, &genai.Part{Text: text.String()})
	}
	for index := int64(0); index < int64(len(toolCalls)); index++ {
		acc, ok := toolCalls[index]
		if !ok {
			yield(nil, fmt.Errorf("stream tool call indexes are not contiguous at %d", index))
			return
		}
		if acc.ID == "" || acc.Name == "" {
			yield(nil, fmt.Errorf("stream returned incomplete tool call at index %d", index))
			return
		}
		args, err := decodeArguments(acc.Arguments.String())
		if err != nil {
			yield(nil, fmt.Errorf("decode streamed arguments for %s: %w", acc.Name, err))
			return
		}
		parts = append(parts, &genai.Part{FunctionCall: &genai.FunctionCall{ID: acc.ID, Name: acc.Name, Args: args}})
	}
	metadata := map[string]any{}
	if reasoning.Len() > 0 {
		metadata["reasoning_content"] = reasoning.String()
	}
	if len(metadata) == 0 {
		metadata = nil
	}
	yield(&model.LLMResponse{
		Content: &genai.Content{Role: "model", Parts: parts}, CustomMetadata: metadata,
		UsageMetadata: usage, ModelVersion: modelVersion, FinishReason: finish, TurnComplete: true,
	}, nil)
}
