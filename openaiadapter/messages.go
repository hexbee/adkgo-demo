package openaiadapter

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"github.com/openai/openai-go/v3"
	"google.golang.org/genai"
)

func convertSystemInstruction(content *genai.Content) ([]openai.ChatCompletionMessageParamUnion, error) {
	if content == nil {
		return nil, nil
	}
	var texts []string
	for i, part := range content.Parts {
		if part == nil {
			continue
		}
		if part.Text == "" {
			return nil, &ConversionError{Path: fmt.Sprintf("system.parts[%d]", i), Kind: "non-text system part"}
		}
		texts = append(texts, part.Text)
	}
	if len(texts) == 0 {
		return nil, nil
	}
	return []openai.ChatCompletionMessageParamUnion{openai.SystemMessage(strings.Join(texts, "\n"))}, nil
}

func convertContents(contents []*genai.Content) ([]openai.ChatCompletionMessageParamUnion, error) {
	var result []openai.ChatCompletionMessageParamUnion
	for i, content := range contents {
		if content == nil {
			continue
		}
		switch content.Role {
		case "", "user":
			messages, err := convertUserContent(i, content)
			if err != nil {
				return nil, err
			}
			result = append(result, messages...)
		case "model", "assistant":
			message, err := convertAssistantContent(i, content)
			if err != nil {
				return nil, err
			}
			result = append(result, message)
		default:
			return nil, &ConversionError{Path: fmt.Sprintf("contents[%d].role", i), Kind: "role " + content.Role}
		}
	}
	return result, nil
}

func convertUserContent(index int, content *genai.Content) ([]openai.ChatCompletionMessageParamUnion, error) {
	var messages []openai.ChatCompletionMessageParamUnion
	var ordinary []*genai.Part
	for i, part := range content.Parts {
		if part == nil {
			continue
		}
		if part.FunctionResponse == nil {
			ordinary = append(ordinary, part)
			continue
		}
		response := part.FunctionResponse
		if response.ID == "" {
			return nil, &ConversionError{Path: fmt.Sprintf("contents[%d].parts[%d]", index, i), Kind: "function response without call ID"}
		}
		data, err := json.Marshal(response.Response)
		if err != nil {
			return nil, fmt.Errorf("marshal function response %s: %w", response.Name, err)
		}
		messages = append(messages, openai.ToolMessage(string(data), response.ID))
	}
	if len(ordinary) == 0 {
		return messages, nil
	}
	parts, err := convertUserParts(fmt.Sprintf("contents[%d]", index), ordinary)
	if err != nil {
		return nil, err
	}
	if len(parts) == 1 && parts[0].OfText != nil {
		messages = append(messages, openai.UserMessage(parts[0].OfText.Text))
	} else {
		messages = append(messages, openai.UserMessage(parts))
	}
	return messages, nil
}

func convertUserParts(path string, parts []*genai.Part) ([]openai.ChatCompletionContentPartUnionParam, error) {
	result := make([]openai.ChatCompletionContentPartUnionParam, 0, len(parts))
	for i, part := range parts {
		partPath := fmt.Sprintf("%s.parts[%d]", path, i)
		switch {
		case part.Text != "":
			result = append(result, openai.TextContentPart(part.Text))
		case part.InlineData != nil:
			if !strings.HasPrefix(part.InlineData.MIMEType, "image/") {
				return nil, &ConversionError{Path: partPath, Kind: "inline MIME type " + part.InlineData.MIMEType}
			}
			dataURL := "data:" + part.InlineData.MIMEType + ";base64," + base64.StdEncoding.EncodeToString(part.InlineData.Data)
			result = append(result, imagePart(dataURL))
		case part.FileData != nil:
			u, err := url.Parse(part.FileData.FileURI)
			if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
				return nil, &ConversionError{Path: partPath, Kind: "non-HTTP image URI"}
			}
			if part.FileData.MIMEType != "" && !strings.HasPrefix(part.FileData.MIMEType, "image/") {
				return nil, &ConversionError{Path: partPath, Kind: "file MIME type " + part.FileData.MIMEType}
			}
			result = append(result, imagePart(part.FileData.FileURI))
		default:
			return nil, &ConversionError{Path: partPath, Kind: "unsupported ADK part"}
		}
	}
	return result, nil
}

func imagePart(value string) openai.ChatCompletionContentPartUnionParam {
	return openai.ImageContentPart(openai.ChatCompletionContentPartImageImageURLParam{URL: value})
}

func convertAssistantContent(index int, content *genai.Content) (openai.ChatCompletionMessageParamUnion, error) {
	var thoughts []string
	var texts []string
	var calls []openai.ChatCompletionMessageToolCallUnionParam
	for i, part := range content.Parts {
		if part == nil {
			continue
		}
		switch {
		case part.Text != "" && part.Thought:
			thoughts = append(thoughts, part.Text)
		case part.Text != "":
			texts = append(texts, part.Text)
		case part.FunctionCall != nil:
			call := part.FunctionCall
			if call.ID == "" || call.Name == "" {
				return openai.ChatCompletionMessageParamUnion{}, &ConversionError{Path: fmt.Sprintf("contents[%d].parts[%d]", index, i), Kind: "function call without ID or name"}
			}
			arguments, err := json.Marshal(call.Args)
			if err != nil {
				return openai.ChatCompletionMessageParamUnion{}, fmt.Errorf("marshal function call %s: %w", call.Name, err)
			}
			calls = append(calls, openai.ChatCompletionMessageToolCallUnionParam{OfFunction: &openai.ChatCompletionMessageFunctionToolCallParam{
				ID:       call.ID,
				Function: openai.ChatCompletionMessageFunctionToolCallFunctionParam{Name: call.Name, Arguments: string(arguments)},
			}})
		default:
			return openai.ChatCompletionMessageParamUnion{}, &ConversionError{Path: fmt.Sprintf("contents[%d].parts[%d]", index, i), Kind: "unsupported assistant part"}
		}
	}
	message := openai.AssistantMessage(strings.Join(texts, ""))
	message.OfAssistant.ToolCalls = calls
	if len(thoughts) > 0 {
		message.OfAssistant.SetExtraFields(map[string]any{
			"reasoning_content": strings.Join(thoughts, ""),
		})
	}
	return message, nil
}
