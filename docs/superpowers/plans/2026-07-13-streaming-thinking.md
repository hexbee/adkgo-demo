# Streaming Thinking Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. Do not use subagents for this project. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Stream OpenAI-compatible `reasoning_content` into ADK Web UI as native Thought parts, make thinking request behavior configurable, and preserve reasoning through tool-call history.

**Architecture:** Configuration flows from `.env` through `internal/config` into `openaiadapter.Config`. The adapter encodes optional thinking request fields, maps response reasoning to `genai.Part{Thought: true}` in both stream and non-stream paths, and serializes those parts back to assistant `reasoning_content` for later tool sub-requests.

**Tech Stack:** Go 1.25+, `google.golang.org/adk/v2@v2.0.0`, `google.golang.org/genai@v1.57.0`, `github.com/openai/openai-go/v3@v3.42.0`, ADK bundled Web UI, `httptest`.

## Global Constraints

- `THINKING_MODE` accepts `auto`, `enabled`, or `disabled`; missing or blank means `auto`.
- `REASONING_EFFORT` is optional and accepts only `high` or `max`.
- `THINKING_MODE=disabled` with non-empty `REASONING_EFFORT` is a startup error.
- `auto` must not send a provider-specific `thinking` field.
- `enabled` and `disabled` send `thinking.type` through the OpenAI SDK extra-fields mechanism.
- Every returned `reasoning_content` chunk becomes an immediate `Partial: true` ADK Thought part.
- Final stream and non-stream responses contain complete Thought, visible text, and tool-call parts in that order.
- Keep complete reasoning in `CustomMetadata["reasoning_content"]` for backward compatibility.
- Assistant Thought history must be sent as `reasoning_content`, never ordinary visible `content`.
- Preserve provider chunk order and all existing images, JSON output, local tools, MCP, Skills, and CLI behavior.
- Do not fork ADK Web UI or modify ADK module-cache files.
- Never log reasoning, prompts, tool arguments, response bodies, API keys, `.env`, or `.mcp.json`.
- Preserve and do not stage the existing untracked `AI_Digest_2026-07-13.md`.
- Execute inline; the user explicitly prohibited subagents.

---

## File Map

- Modify `internal/config/config.go`: load, normalize, validate, and safely summarize thinking settings.
- Modify `internal/config/config_test.go`: defaults, accepted values, rejected values, and secret-safe summary tests.
- Modify `openaiadapter/adapter.go`: adapter-level configuration validation and propagation.
- Modify `openaiadapter/adapter_test.go`: direct adapter validation and non-stream response assertions.
- Modify `openaiadapter/requests.go`: request-level `thinking` and `reasoning_effort` encoding.
- Modify `openaiadapter/requests_test.go`: exact request JSON tests.
- Modify `openaiadapter/messages.go`: Thought history serialization into `reasoning_content`.
- Modify `openaiadapter/messages_test.go`: visible/reasoning separation and tool round-trip tests.
- Modify `openaiadapter/responses.go`: non-stream Thought part construction.
- Modify `openaiadapter/stream.go`: streamed Thought partials and final aggregate.
- Modify `openaiadapter/stream_test.go`: streaming order, final aggregation, tools, and no-reasoning compatibility.
- Modify `main.go`: pass thinking settings to the adapter.
- Modify `README.md`: configuration, Web UI behavior, compatibility, and privacy.

### Task 1: Environment Configuration

**Files:**
- Modify: `internal/config/config.go`
- Modify: `internal/config/config_test.go`

**Interfaces:**
- Produces: `Config.ThinkingMode string` and `Config.ReasoningEffort string`
- Produces: normalized values consumed by `main.go` in Task 4

- [x] **Step 1: Extend test environment cleanup and write failing default tests**

Add `THINKING_MODE` and `REASONING_EFFORT` to `clearConfigEnv`, then extend `TestLoadReadsDotEnv`:

```go
func clearConfigEnv(t *testing.T) {
	t.Helper()
	for _, name := range []string{
		"BASE_URL", "API_KEY", "MODEL_NAME", "CONTEXT_WINDOW", "MAX_TOKENS",
		"THINKING_MODE", "REASONING_EFFORT",
	} {
		value, existed := os.LookupEnv(name)
		if err := os.Unsetenv(name); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if existed {
				_ = os.Setenv(name, value)
			} else {
				_ = os.Unsetenv(name)
			}
		})
	}
}
```

After loading the existing minimal dotenv in `TestLoadReadsDotEnv`, assert:

```go
if got.ThinkingMode != "auto" || got.ReasoningEffort != "" {
	t.Fatalf("thinking config = %q, %q", got.ThinkingMode, got.ReasoningEffort)
}
if !strings.Contains(got.SafeSummary(), "thinking_mode=auto") {
	t.Fatalf("safe summary = %q", got.SafeSummary())
}
```

- [x] **Step 2: Run the default test and verify it fails**

Run: `go test ./internal/config -run TestLoadReadsDotEnv -count=1`

Expected: FAIL because the two `Config` fields do not exist.

- [x] **Step 3: Add accepted and rejected configuration tests**

Append:

```go
func validEnvWithThinking(mode, effort string) string {
	return "BASE_URL=https://x.test/v1\nAPI_KEY=k\nMODEL_NAME=m\nCONTEXT_WINDOW=10\nMAX_TOKENS=5\n" +
		"THINKING_MODE=" + mode + "\nREASONING_EFFORT=" + effort + "\n"
}

func TestLoadThinkingConfiguration(t *testing.T) {
	for _, tc := range []struct {
		name, mode, effort, wantMode, wantEffort string
	}{
		{"auto", "auto", "", "auto", ""},
		{"enabled high", " enabled ", " HIGH ", "enabled", "high"},
		{"enabled max", "enabled", "max", "enabled", "max"},
		{"disabled", "disabled", "", "disabled", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			clearConfigEnv(t)
			got, err := Load(writeEnv(t, validEnvWithThinking(tc.mode, tc.effort)))
			if err != nil {
				t.Fatal(err)
			}
			if got.ThinkingMode != tc.wantMode || got.ReasoningEffort != tc.wantEffort {
				t.Fatalf("thinking config = %q, %q", got.ThinkingMode, got.ReasoningEffort)
			}
		})
	}
}

func TestLoadRejectsInvalidThinkingConfiguration(t *testing.T) {
	for _, tc := range []struct{ name, mode, effort, want string }{
		{"mode", "sometimes", "", "THINKING_MODE"},
		{"effort", "auto", "medium", "REASONING_EFFORT"},
		{"disabled effort", "disabled", "high", "REASONING_EFFORT"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			clearConfigEnv(t)
			_, err := Load(writeEnv(t, validEnvWithThinking(tc.mode, tc.effort)))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v", err)
			}
		})
	}
}
```

- [x] **Step 4: Implement loading, validation, and safe summary**

Add fields and constants to `config.go`:

```go
const (
	thinkingModeAuto     = "auto"
	thinkingModeEnabled  = "enabled"
	thinkingModeDisabled = "disabled"
)

type Config struct {
	BaseURL         string
	APIKey          string
	ModelName       string
	ContextWindow   int64
	MaxTokens       int64
	ThinkingMode    string
	ReasoningEffort string
}
```

Populate them in `Load` before validation:

```go
	c := Config{
		BaseURL:         strings.TrimRight(strings.TrimSpace(os.Getenv("BASE_URL")), "/"),
		APIKey:          strings.TrimSpace(os.Getenv("API_KEY")),
		ModelName:       strings.TrimSpace(os.Getenv("MODEL_NAME")),
		ThinkingMode:    strings.ToLower(strings.TrimSpace(os.Getenv("THINKING_MODE"))),
		ReasoningEffort: strings.ToLower(strings.TrimSpace(os.Getenv("REASONING_EFFORT"))),
	}
	if c.ThinkingMode == "" {
		c.ThinkingMode = thinkingModeAuto
	}
```

Validate after required-string validation:

```go
	switch c.ThinkingMode {
	case thinkingModeAuto, thinkingModeEnabled, thinkingModeDisabled:
	default:
		return Config{}, fmt.Errorf("THINKING_MODE must be auto, enabled, or disabled")
	}
	switch c.ReasoningEffort {
	case "", "high", "max":
	default:
		return Config{}, fmt.Errorf("REASONING_EFFORT must be high or max when set")
	}
	if c.ThinkingMode == thinkingModeDisabled && c.ReasoningEffort != "" {
		return Config{}, fmt.Errorf("REASONING_EFFORT must be empty when THINKING_MODE is disabled")
	}
```

Replace `SafeSummary` with:

```go
func (c Config) SafeSummary() string {
	summary := fmt.Sprintf(
		"model=%s base_url=%s context_window=%d max_tokens=%d thinking_mode=%s",
		c.ModelName, c.BaseURL, c.ContextWindow, c.MaxTokens, c.ThinkingMode,
	)
	if c.ReasoningEffort != "" {
		summary += " reasoning_effort=" + c.ReasoningEffort
	}
	return summary
}
```

- [x] **Step 5: Verify and commit**

Run: `gofmt -w internal/config/*.go && go test ./internal/config -count=1`

Expected: PASS.

```bash
git add internal/config/config.go internal/config/config_test.go
git commit -m "feat: configure model thinking mode"
```

### Task 2: Thinking Request Encoding and History Round-Trip

**Files:**
- Modify: `openaiadapter/adapter.go`
- Modify: `openaiadapter/adapter_test.go`
- Modify: `openaiadapter/requests.go`
- Modify: `openaiadapter/requests_test.go`
- Modify: `openaiadapter/messages.go`
- Modify: `openaiadapter/messages_test.go`

**Interfaces:**
- Consumes: `openaiadapter.Config{ThinkingMode, ReasoningEffort}`
- Produces: `buildParams(req *model.LLMRequest, cfg Config)`
- Produces: assistant request messages with optional `reasoning_content`

- [ ] **Step 1: Write failing adapter configuration tests**

Append to `adapter_test.go`:

```go
func validAdapterConfig(baseURL string) Config {
	return Config{
		BaseURL: baseURL, APIKey: "test-secret-key", Model: "test-model",
		ContextWindow: 1000, MaxTokens: 100,
	}
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

	for _, cfg := range []Config{
		func() Config { c := validAdapterConfig("https://example.test/v1"); c.ThinkingMode = "sometimes"; return c }(),
		func() Config { c := validAdapterConfig("https://example.test/v1"); c.ReasoningEffort = "medium"; return c }(),
		func() Config { c := validAdapterConfig("https://example.test/v1"); c.ThinkingMode = "disabled"; c.ReasoningEffort = "high"; return c }(),
	} {
		if _, err := New(cfg); err == nil {
			t.Fatalf("New(%#v) succeeded", cfg)
		}
	}
}
```

Refactor `newTestModel` to call `validAdapterConfig(server.URL + "/v1")` so new tests and existing tests share one valid baseline.

- [ ] **Step 2: Write failing exact request JSON tests**

Change existing `buildParams` test calls to pass `Config{Model: ..., MaxTokens: ...}`. Add:

```go
func TestBuildParamsThinkingConfiguration(t *testing.T) {
	for _, tc := range []struct {
		name, mode, effort string
		wantThinking       any
		wantEffort         string
	}{
		{name: "auto", mode: "auto", wantThinking: nil},
		{name: "enabled high", mode: "enabled", effort: "high", wantThinking: "enabled", wantEffort: "high"},
		{name: "disabled", mode: "disabled", wantThinking: "disabled"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, _, err := buildParams(testRequest(), Config{
				Model: "test-model", MaxTokens: 100,
				ThinkingMode: tc.mode, ReasoningEffort: tc.effort,
			})
			if err != nil {
				t.Fatal(err)
			}
			payload := marshalMap(t, got)
			thinking, exists := payload["thinking"]
			if tc.wantThinking == nil {
				if exists {
					t.Fatalf("thinking unexpectedly present: %#v", thinking)
				}
			} else if thinking.(map[string]any)["type"] != tc.wantThinking {
				t.Fatalf("thinking = %#v", thinking)
			}
			if tc.wantEffort == "" {
				if _, exists := payload["reasoning_effort"]; exists {
					t.Fatalf("reasoning_effort unexpectedly present: %#v", payload)
				}
			} else if payload["reasoning_effort"] != tc.wantEffort {
				t.Fatalf("reasoning_effort = %#v", payload["reasoning_effort"])
			}
		})
	}
}
```

- [ ] **Step 3: Write failing assistant history tests**

Append to `messages_test.go`:

```go
func TestConvertAssistantThoughtHistory(t *testing.T) {
	got, err := convertContents([]*genai.Content{{Role: "model", Parts: []*genai.Part{
		{Text: "reason one ", Thought: true},
		{Text: "reason two", Thought: true},
		{Text: "visible answer"},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	payload := marshalMap(t, got[0])
	if payload["reasoning_content"] != "reason one reason two" || payload["content"] != "visible answer" {
		t.Fatalf("assistant message = %#v", payload)
	}
}

func TestConvertThoughtToolCallRoundTrip(t *testing.T) {
	got, err := convertContents([]*genai.Content{
		{Role: "model", Parts: []*genai.Part{
			{Text: "need the tool", Thought: true},
			{FunctionCall: &genai.FunctionCall{ID: "call-1", Name: "lookup", Args: map[string]any{"q": "x"}},
		}},
		{Role: "user", Parts: []*genai.Part{{FunctionResponse: &genai.FunctionResponse{
			ID: "call-1", Name: "lookup", Response: map[string]any{"output": "y"},
		}}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	assistant := marshalMap(t, got[0])
	toolResult := marshalMap(t, got[1])
	if assistant["reasoning_content"] != "need the tool" || assistant["tool_calls"] == nil {
		t.Fatalf("assistant = %#v", assistant)
	}
	if content, exists := assistant["content"]; exists && content != "" {
		t.Fatalf("thought leaked into visible content: %#v", assistant)
	}
	if toolResult["tool_call_id"] != "call-1" {
		t.Fatalf("tool result = %#v", toolResult)
	}
}
```

- [ ] **Step 4: Run focused tests and verify they fail**

Run: `go test ./openaiadapter -run 'Test(NewValidatesThinking|BuildParamsThinking|ConvertAssistantThought|ConvertThoughtTool)' -count=1`

Expected: FAIL because config fields, request encoding, and `reasoning_content` history do not exist.

- [ ] **Step 5: Implement adapter configuration validation**

Add `ThinkingMode` and `ReasoningEffort` to `openaiadapter.Config`. In `New`, lowercase and trim both, default blank mode to `auto`, validate the same allowed combinations as Task 1, and return adapter-local messages:

```go
	cfg.ThinkingMode = strings.ToLower(strings.TrimSpace(cfg.ThinkingMode))
	cfg.ReasoningEffort = strings.ToLower(strings.TrimSpace(cfg.ReasoningEffort))
	if cfg.ThinkingMode == "" {
		cfg.ThinkingMode = "auto"
	}
	switch cfg.ThinkingMode {
	case "auto", "enabled", "disabled":
	default:
		return nil, fmt.Errorf("thinking mode must be auto, enabled, or disabled")
	}
	switch cfg.ReasoningEffort {
	case "", "high", "max":
	default:
		return nil, fmt.Errorf("reasoning effort must be high or max when set")
	}
	if cfg.ThinkingMode == "disabled" && cfg.ReasoningEffort != "" {
		return nil, fmt.Errorf("reasoning effort must be empty when thinking mode is disabled")
	}
```

- [ ] **Step 6: Implement request encoding**

Change the request builder signature:

```go
func buildParams(req *model.LLMRequest, cfg Config) (openai.ChatCompletionNewParams, []string, error)
```

Use `cfg.Model` and `cfg.MaxTokens` in place of the two old arguments. Before returning, apply:

```go
	if cfg.ThinkingMode == "enabled" || cfg.ThinkingMode == "disabled" {
		params.SetExtraFields(map[string]any{
			"thinking": map[string]any{"type": cfg.ThinkingMode},
		})
	}
	if cfg.ReasoningEffort != "" {
		params.ReasoningEffort = shared.ReasoningEffort(cfg.ReasoningEffort)
	}
```

Update `GenerateContent` to call `buildParams(req, m.cfg)` and update existing request tests to use `Config` values.

- [ ] **Step 7: Implement Thought history serialization**

In `convertAssistantContent`, maintain separate `thoughts` and `texts` slices:

```go

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
				return openai.ChatCompletionMessageParamUnion{}, &ConversionError{
					Path: fmt.Sprintf("contents[%d].parts[%d]", index, i), Kind: "function call without ID or name",
				}
			}
			arguments, err := json.Marshal(call.Args)
			if err != nil {
				return openai.ChatCompletionMessageParamUnion{}, fmt.Errorf("marshal function call %s: %w", call.Name, err)
			}
			calls = append(calls, openai.ChatCompletionMessageToolCallUnionParam{
				OfFunction: &openai.ChatCompletionMessageFunctionToolCallParam{
					ID: call.ID,
					Function: openai.ChatCompletionMessageFunctionToolCallFunctionParam{
						Name: call.Name, Arguments: string(arguments),
					},
				},
			})
		default:
			return openai.ChatCompletionMessageParamUnion{}, &ConversionError{
				Path: fmt.Sprintf("contents[%d].parts[%d]", index, i), Kind: "unsupported assistant part",
			}
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
```

- [ ] **Step 8: Verify and commit**

Run: `gofmt -w openaiadapter/*.go && go test ./openaiadapter -run 'Test(NewValidatesThinking|BuildParams|Convert)' -count=1`

Expected: PASS.

```bash
git add openaiadapter/adapter.go openaiadapter/adapter_test.go openaiadapter/requests.go openaiadapter/requests_test.go openaiadapter/messages.go openaiadapter/messages_test.go
git commit -m "feat: round-trip thinking configuration and history"
```

### Task 3: Native Thought Response Parts and Streaming

**Files:**
- Modify: `openaiadapter/responses.go`
- Modify: `openaiadapter/stream.go`
- Modify: `openaiadapter/adapter_test.go`
- Modify: `openaiadapter/stream_test.go`

**Interfaces:**
- Consumes: provider `reasoning_content`
- Produces: `genai.Part{Text: ..., Thought: true}` in partial and final responses
- Preserves: `CustomMetadata["reasoning_content"]`

- [ ] **Step 1: Tighten the non-streaming response test**

Update `TestModelNonStreamingToolCall` to expect two parts:

```go
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
```

Add direct canonical-order and compatibility tests:

```go
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
```

Add `encoding/json` and `github.com/openai/openai-go/v3` to `adapter_test.go` imports.

- [ ] **Step 2: Rewrite the streaming text test expectations before implementation**

Leave the four existing SSE data lines unchanged. Replace the assertions after response collection so the test expects four responses: reasoning partial, two visible partials, and final aggregate.

```go
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
```

Continue asserting usage and complete metadata on the final response.

- [ ] **Step 3: Add streamed reasoning-plus-tool test coverage**

Prepend this fixture chunk to `TestStreamToolCallFragments`:

```go
`{"id":"s1","object":"chat.completion.chunk","created":1,"model":"m","choices":[{"index":0,"delta":{"reasoning_content":"choose tool"},"finish_reason":null}]}`,
```

Keep the last response in `final`, then assert:

```go
if final == nil || len(final.Content.Parts) != 2 {
	t.Fatalf("final = %#v", final)
}
if !final.Content.Parts[0].Thought || final.Content.Parts[0].Text != "choose tool" {
	t.Fatalf("thought = %#v", final.Content.Parts[0])
}
if final.Content.Parts[1].FunctionCall == nil || final.Content.Parts[1].FunctionCall.Name != "lookup_time" {
	t.Fatalf("call = %#v", final.Content.Parts[1])
}
```

Add the no-reasoning streaming test:

```go
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
```

- [ ] **Step 4: Run response tests and verify they fail**

Run: `go test ./openaiadapter -run 'Test(ModelNonStreaming|Stream)' -count=1`

Expected: FAIL because reasoning is still metadata-only and no Thought partial exists.

- [ ] **Step 5: Implement non-streaming Thought parts**

In `fromCompletion`, extract reasoning before ordinary content and append it first:

```go
	reasoning := extractReasoning(choice.Message.RawJSON())
	parts := make([]*genai.Part, 0, 2+len(choice.Message.ToolCalls))
	if reasoning != "" {
		parts = append(parts, &genai.Part{Text: reasoning, Thought: true})
	}
	if choice.Message.Content != "" {
		parts = append(parts, &genai.Part{Text: choice.Message.Content})
	}
```

Use the already extracted `reasoning` when constructing metadata:

```go
	metadata := map[string]any{}
	if reasoning != "" {
		metadata["reasoning_content"] = reasoning
	}
	if len(metadata) == 0 {
		metadata = nil
	}
```

The function-call loop remains immediately after the visible text append block, so calls are appended after Thought and visible text.

- [ ] **Step 6: Implement streamed Thought partials**

Replace the current reasoning accumulation block in `generateStream`:

```go
			if deltaReasoning := extractReasoning(choice.Delta.RawJSON()); deltaReasoning != "" {
				reasoning.WriteString(deltaReasoning)
				if !yield(&model.LLMResponse{
					Content: &genai.Content{Role: "model", Parts: []*genai.Part{{
						Text: deltaReasoning, Thought: true,
					}}},
					ModelVersion: modelVersion,
					Partial:      true,
				}, nil) {
					return
				}
			}
```

Build final parts with complete reasoning before complete visible text:

```go
	parts := make([]*genai.Part, 0, 2+len(toolCalls))
	if reasoning.Len() > 0 {
		parts = append(parts, &genai.Part{Text: reasoning.String(), Thought: true})
	}
	if text.Len() > 0 {
		parts = append(parts, &genai.Part{Text: text.String()})
	}
```

Place the unchanged indexed tool-call decoding loop immediately after those two blocks. Construct metadata from `reasoning.String()` and return the final response with the current usage, finish reason, model version, and `TurnComplete: true` fields:

```go
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
```

- [ ] **Step 7: Verify response behavior and commit**

Run: `gofmt -w openaiadapter/*.go && go test ./openaiadapter -count=1 && go test -race ./openaiadapter -count=1`

Expected: PASS with no race report.

```bash
git add openaiadapter/responses.go openaiadapter/stream.go openaiadapter/adapter_test.go openaiadapter/stream_test.go
git commit -m "feat: stream reasoning as ADK Thought parts"
```

### Task 4: Application Wiring, Documentation, and End-to-End Validation

**Files:**
- Modify: `main.go`
- Modify: `README.md`

**Interfaces:**
- Consumes: `internal/config.Config.ThinkingMode` and `.ReasoningEffort`
- Supplies: corresponding `openaiadapter.Config` fields

- [ ] **Step 1: Wire validated settings into model construction**

Extend the existing `openaiadapter.New` call in `main.go`:

```go
	adaptedModel, err := openaiadapter.New(openaiadapter.Config{
		BaseURL:        cfg.BaseURL,
		APIKey:         cfg.APIKey,
		Model:          cfg.ModelName,
		ContextWindow:  cfg.ContextWindow,
		MaxTokens:      cfg.MaxTokens,
		ThinkingMode:   cfg.ThinkingMode,
		ReasoningEffort: cfg.ReasoningEffort,
	})
```

- [ ] **Step 2: Document thinking configuration and Web UI behavior**

Update the README dotenv example to include:

```dotenv
THINKING_MODE=auto
# REASONING_EFFORT=high
```

Add this section after “更换服务商”:

````markdown
## Thinking / Reasoning

Adapter 支持 OpenAI-compatible Chat Completions 返回的 `reasoning_content`。ADK Web UI 会把它作为独立 Thought 展示，thinking 和之后的正式回答都会按照服务商 chunk 实时流式输出：

```bash
go run . web webui api
```

`THINKING_MODE` 支持：

- `auto`（默认）：不发送服务商私有 `thinking` 参数，但只要响应包含 reasoning 就展示；
- `enabled`：发送 `thinking: {"type":"enabled"}`；
- `disabled`：发送 `thinking: {"type":"disabled"}`。

`REASONING_EFFORT` 可留空，或设置为 `high`、`max`。`THINKING_MODE=disabled` 时不能同时设置 effort。

带工具调用的 assistant reasoning 会通过 `reasoning_content` 原样回传，支持 DeepSeek thinking → tool call → tool result → thinking/answer 的多步流程。不返回 reasoning 的服务商保持普通流式回答行为。

> **隐私提示：** Thought 是服务商返回的模型输出，不保证是完整或忠实的内部计算审计记录，其中可能包含提示词、工具计划或其他敏感上下文。不要将这个本地开发 Web UI 暴露给不受信任的访问者。
````

- [ ] **Step 3: Run full automated validation**

Run:

```bash
go test ./...
go test -race ./...
go vet ./...
go build ./...
```

Expected: all commands exit 0 and the race detector reports no races.

- [ ] **Step 4: Run a raw local SSE smoke test**

Start the combined server on its default port:

```bash
go run . web webui api
```

In another terminal, create a local session:

```bash
curl -fsS -X POST \
  -H 'Content-Type: application/json' \
  -d '{}' \
  http://localhost:8080/api/apps/openai_compatible_assistant/users/thinking-smoke/sessions/thinking-smoke
```

Post a reasoning question to the SSE endpoint:

```bash
curl -fsSN -X POST \
  -H 'Content-Type: application/json' \
  -d '{"appName":"openai_compatible_assistant","userId":"thinking-smoke","sessionId":"thinking-smoke","newMessage":{"role":"user","parts":[{"text":"认真比较 9.11 和 9.8 哪个更大，给出简短答案；不要调用 MCP。"}]},"streaming":true}' \
  http://localhost:8080/api/run_sse
```

Inspect only event structure, not credentials. Verify at least one partial event contains:

```json
{"content":{"parts":[{"text":"...","thought":true}]},"partial":true}
```

and at least one later partial event contains ordinary text without `thought: true`. Verify the final event contains complete Thought and visible text parts. Stop the server with Ctrl-C after both smoke tests.

- [ ] **Step 5: Run a tool-call reasoning smoke test**

In the bundled Web UI, ask a timezone question that requires `lookup_time` and explicitly forbid MCP. Verify the request completes through reasoning → local tool call → tool result → final response without a provider 400. Do not approve any MCP confirmation.

- [ ] **Step 6: Confirm working tree scope and commit documentation**

Run `git status --short` and verify `AI_Digest_2026-07-13.md` remains untracked and unstaged. Stage only `main.go`, `README.md`, and the implementation-plan checkbox updates.

```bash
git add main.go README.md docs/superpowers/plans/2026-07-13-streaming-thinking.md
git commit -m "docs: document streaming model thinking"
```

## Completion Criteria

- Default `auto` sends no private thinking field and displays returned reasoning.
- Enabled/disabled request JSON and optional effort exactly match configuration.
- Both reasoning and visible content stream to ADK clients as separate native part types.
- Final events persist complete reasoning, visible text, and tool calls.
- Tool-call assistant history returns complete reasoning through `reasoning_content`.
- Providers without reasoning behave exactly as before.
- Existing adapter, MCP, Skills, CLI, race, vet, and build checks pass.
- DeepSeek V4 Flash completes raw streaming and local-tool smoke tests without unrelated MCP activity.
- The existing untracked digest remains untouched and no sensitive files or values enter Git.
