package openaiadapter

import (
	"encoding/json"
	"fmt"
	"slices"
	"sort"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/shared"
	"google.golang.org/adk/v2/model"
	"google.golang.org/genai"
)

func buildParams(req *model.LLMRequest, defaultModel string, defaultMaxTokens int64) (openai.ChatCompletionNewParams, []string, error) {
	modelName := defaultModel
	if req.Model != "" {
		modelName = req.Model
	}
	params := openai.ChatCompletionNewParams{Model: openai.ChatModel(modelName)}
	capabilities := map[string]bool{}

	if req.Config != nil {
		system, err := convertSystemInstruction(req.Config.SystemInstruction)
		if err != nil {
			return params, nil, err
		}
		params.Messages = append(params.Messages, system...)
	}
	messages, err := convertContents(req.Contents)
	if err != nil {
		return params, nil, err
	}
	params.Messages = append(params.Messages, messages...)
	if requestHasImages(req.Contents) {
		capabilities["images"] = true
	}

	maxTokens := defaultMaxTokens
	if req.Config != nil && req.Config.MaxOutputTokens > 0 && int64(req.Config.MaxOutputTokens) < maxTokens {
		maxTokens = int64(req.Config.MaxOutputTokens)
	}
	if maxTokens > 0 {
		params.MaxTokens = openai.Int(maxTokens)
	}
	if req.Config != nil {
		applyGenerationConfig(&params, req.Config)
		if err := applyTools(&params, req.Config, capabilities); err != nil {
			return params, nil, err
		}
		if err := applyResponseFormat(&params, req.Config, capabilities); err != nil {
			return params, nil, err
		}
	}

	labels := make([]string, 0, len(capabilities))
	for name := range capabilities {
		labels = append(labels, name)
	}
	sort.Strings(labels)
	return params, labels, nil
}

func applyGenerationConfig(params *openai.ChatCompletionNewParams, cfg *genai.GenerateContentConfig) {
	if cfg.Temperature != nil {
		params.Temperature = openai.Float(float64(*cfg.Temperature))
	}
	if cfg.TopP != nil {
		params.TopP = openai.Float(float64(*cfg.TopP))
	}
	if cfg.PresencePenalty != nil {
		params.PresencePenalty = openai.Float(float64(*cfg.PresencePenalty))
	}
	if cfg.FrequencyPenalty != nil {
		params.FrequencyPenalty = openai.Float(float64(*cfg.FrequencyPenalty))
	}
	if cfg.Seed != nil {
		params.Seed = openai.Int(int64(*cfg.Seed))
	}
	if len(cfg.StopSequences) > 0 {
		params.Stop = openai.ChatCompletionNewParamsStopUnion{OfStringArray: slices.Clone(cfg.StopSequences)}
	}
}

func applyTools(params *openai.ChatCompletionNewParams, cfg *genai.GenerateContentConfig, capabilities map[string]bool) error {
	for toolIndex, adkTool := range cfg.Tools {
		if adkTool == nil {
			continue
		}
		if len(adkTool.FunctionDeclarations) == 0 {
			return &ConversionError{Path: fmt.Sprintf("config.tools[%d]", toolIndex), Kind: "non-function tool"}
		}
		for declarationIndex, declaration := range adkTool.FunctionDeclarations {
			if declaration == nil || declaration.Name == "" {
				return &ConversionError{Path: fmt.Sprintf("config.tools[%d].functionDeclarations[%d]", toolIndex, declarationIndex), Kind: "function without name"}
			}
			var schema map[string]any
			var err error
			switch {
			case declaration.ParametersJsonSchema != nil:
				schema, err = jsonObject(declaration.ParametersJsonSchema)
			case declaration.Parameters != nil:
				schema, err = jsonObject(declaration.Parameters)
			default:
				schema = map[string]any{"type": "object", "properties": map[string]any{}}
			}
			if err != nil {
				return fmt.Errorf("convert schema for tool %s: %w", declaration.Name, err)
			}
			params.Tools = append(params.Tools, openai.ChatCompletionFunctionTool(shared.FunctionDefinitionParam{
				Name: declaration.Name, Description: openai.String(declaration.Description), Parameters: shared.FunctionParameters(schema),
			}))
		}
	}
	if len(params.Tools) == 0 {
		return nil
	}
	capabilities["tools"] = true
	params.ParallelToolCalls = openai.Bool(true)
	params.ToolChoice = openai.ChatCompletionToolChoiceOptionUnionParam{OfAuto: openai.String("auto")}
	if cfg.ToolConfig == nil || cfg.ToolConfig.FunctionCallingConfig == nil {
		return nil
	}
	calling := cfg.ToolConfig.FunctionCallingConfig
	switch calling.Mode {
	case genai.FunctionCallingConfigModeNone:
		params.ToolChoice = openai.ChatCompletionToolChoiceOptionUnionParam{OfAuto: openai.String("none")}
	case genai.FunctionCallingConfigModeAny:
		if len(calling.AllowedFunctionNames) == 1 {
			params.ToolChoice = openai.ToolChoiceOptionFunctionToolChoice(openai.ChatCompletionNamedToolChoiceFunctionParam{Name: calling.AllowedFunctionNames[0]})
		} else {
			params.ToolChoice = openai.ChatCompletionToolChoiceOptionUnionParam{OfAuto: openai.String("required")}
		}
	case genai.FunctionCallingConfigModeAuto, genai.FunctionCallingConfigModeUnspecified, genai.FunctionCallingConfigModeValidated:
		params.ToolChoice = openai.ChatCompletionToolChoiceOptionUnionParam{OfAuto: openai.String("auto")}
	default:
		return &ConversionError{Path: "config.toolConfig.functionCallingConfig.mode", Kind: "tool mode " + string(calling.Mode)}
	}
	return nil
}

func applyResponseFormat(params *openai.ChatCompletionNewParams, cfg *genai.GenerateContentConfig, capabilities map[string]bool) error {
	var schema any
	switch {
	case cfg.ResponseJsonSchema != nil:
		schema = cfg.ResponseJsonSchema
	case cfg.ResponseSchema != nil:
		schema = cfg.ResponseSchema
	}
	if schema != nil {
		object, err := jsonObject(schema)
		if err != nil {
			return fmt.Errorf("convert response schema: %w", err)
		}
		params.ResponseFormat = openai.ChatCompletionNewParamsResponseFormatUnion{OfJSONSchema: &shared.ResponseFormatJSONSchemaParam{
			JSONSchema: shared.ResponseFormatJSONSchemaJSONSchemaParam{Name: "adk_response", Strict: openai.Bool(true), Schema: object},
		}}
		capabilities["json_schema"] = true
		return nil
	}
	if cfg.ResponseMIMEType == "application/json" {
		format := shared.NewResponseFormatJSONObjectParam()
		params.ResponseFormat = openai.ChatCompletionNewParamsResponseFormatUnion{OfJSONObject: &format}
		capabilities["json_object"] = true
	}
	return nil
}

func jsonObject(value any) (map[string]any, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	var result map[string]any
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, err
	}
	return result, nil
}

func requestHasImages(contents []*genai.Content) bool {
	for _, content := range contents {
		if content == nil {
			continue
		}
		for _, part := range content.Parts {
			if part != nil && (part.InlineData != nil || part.FileData != nil) {
				return true
			}
		}
	}
	return false
}
