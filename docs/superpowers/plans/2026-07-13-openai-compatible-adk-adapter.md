# OpenAI-Compatible ADK Go Adapter Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build and demonstrate a reusable ADK Go v2 `model.LLM` adapter for OpenAI-compatible Chat Completions endpoints, including tools, images, structured output, streaming, and safe `.env` configuration.

**Architecture:** `openaiadapter` converts ADK's `genai` request/response types to and from the official OpenAI Go SDK. `internal/config` owns local environment loading and validation, while the root program wires the adapter into an ADK `llmagent`, a deterministic function tool, and the standard launcher. Provider capabilities are not hard-coded; unsupported optional fields surface as sanitized provider errors.

**Tech Stack:** Go 1.26.3, `google.golang.org/adk/v2 v2.0.0`, `google.golang.org/genai v1.57.0`, `github.com/openai/openai-go/v3 v3.42.0`, `github.com/joho/godotenv v1.5.1`, standard `testing`/`httptest`.

## Global Constraints

- The module path is `github.com/hexbee/adkgo-demo`.
- The runtime requires Go 1.25 or later; use the installed Go 1.26.3 toolchain.
- The protocol target is OpenAI Chat Completions, not the OpenAI Responses API.
- No provider names, model names, or capability tables are hard-coded in `openaiadapter`.
- `.env` and API keys must never be committed, logged, returned in errors, or captured in snapshots.
- The initial example values are `BASE_URL=https://api.deepseek.com/v1`, `MODEL_NAME=deepseek-v4-flash`, `CONTEXT_WINDOW=1000000`, and `MAX_TOKENS=384000`.
- `CONTEXT_WINDOW` is diagnostic metadata only; do not estimate tokens, truncate history, or enforce it locally.
- Provider-dependent optional features are passed through only when requested; do not silently downgrade strict schemas or multimodal inputs.
- Tests must use `httptest.Server` and must pass without network credentials.

---

## File Map

- `go.mod`, `go.sum`: pinned module and dependency graph.
- `.gitignore`, `.env.example`: secret-safe local configuration contract.
- `internal/config/config.go`: `.env` loading, validation, and sanitized startup summary.
- `internal/config/config_test.go`: precedence, numeric validation, and secret-redaction tests.
- `openaiadapter/adapter.go`: public model construction and ADK `model.LLM` implementation.
- `openaiadapter/messages.go`: role, text, image, function-call, and function-response conversion.
- `openaiadapter/requests.go`: generation settings, tools, tool choice, and structured-output conversion.
- `openaiadapter/responses.go`: non-streaming Chat Completions conversion and provider metadata extraction.
- `openaiadapter/stream.go`: streaming text/tool-call accumulation and final response emission.
- `openaiadapter/errors.go`: typed conversion/provider errors and secret-safe wrapping.
- `openaiadapter/*_test.go`: focused unit tests plus HTTP integration tests.
- `main.go`: demo agent, deterministic tool, and full ADK launcher.
- `README.md`: setup, run, smoke tests, provider switching, and known capability differences.

---

### Task 1: Bootstrap the module and safe configuration

**Files:**
- Create: `go.mod`
- Create: `go.sum`
- Create: `.gitignore`
- Create: `.env.example`
- Create: `internal/config/config.go`
- Create: `internal/config/config_test.go`

**Interfaces:**
- Produces: `config.Load(path string) (config.Config, error)`
- Produces: `config.Config.SafeSummary() string`
- Produces fields: `BaseURL string`, `APIKey string`, `ModelName string`, `ContextWindow int64`, `MaxTokens int64`

- [ ] **Step 1: Initialize and pin the Go module**

Run:

```bash
go mod init github.com/hexbee/adkgo-demo
go get google.golang.org/adk/v2@v2.0.0
go get github.com/openai/openai-go/v3@v3.42.0
go get github.com/joho/godotenv@v1.5.1
```

Expected: `go.mod` declares `go 1.25.0` or newer and contains all three direct dependencies after `go mod tidy` later in this task.

- [ ] **Step 2: Add the secret-safe environment contract**

Create `.gitignore`:

```gitignore
.env
```

Create `.env.example`:

```dotenv
BASE_URL=https://api.deepseek.com/v1
API_KEY=
MODEL_NAME=deepseek-v4-flash
CONTEXT_WINDOW=1000000
MAX_TOKENS=384000
```

- [ ] **Step 3: Write failing configuration tests**

Create `internal/config/config_test.go` with tests that write a temporary `.env`, temporarily unset all five variables, and assert:

```go
func TestLoadReadsDotEnv(t *testing.T) {
	clearConfigEnv(t)
	path := writeEnv(t, `BASE_URL=https://example.test/v1
API_KEY=secret-value
MODEL_NAME=test-model
CONTEXT_WINDOW=1000000
MAX_TOKENS=384000
`)
	got, err := Load(path)
	if err != nil { t.Fatal(err) }
	if got.BaseURL != "https://example.test/v1" || got.ModelName != "test-model" {
		t.Fatalf("unexpected config: %+v", got)
	}
	if strings.Contains(got.SafeSummary(), "secret-value") {
		t.Fatal("safe summary leaked API key")
	}
}

func TestLoadProcessEnvironmentWins(t *testing.T) {
	clearConfigEnv(t)
	path := writeEnv(t, "BASE_URL=https://file.test/v1\nAPI_KEY=file-key\nMODEL_NAME=file-model\nCONTEXT_WINDOW=100\nMAX_TOKENS=50\n")
	t.Setenv("MODEL_NAME", "shell-model")
	got, err := Load(path)
	if err != nil { t.Fatal(err) }
	if got.ModelName != "shell-model" { t.Fatalf("model = %q", got.ModelName) }
}

func TestLoadRejectsMissingAndInvalidValues(t *testing.T) {
	for _, tc := range []struct{ name, body, want string }{
		{"missing key", "BASE_URL=https://x/v1\nMODEL_NAME=m\nCONTEXT_WINDOW=10\nMAX_TOKENS=5\n", "API_KEY"},
		{"bad URL", "BASE_URL=not-a-url\nAPI_KEY=k\nMODEL_NAME=m\nCONTEXT_WINDOW=10\nMAX_TOKENS=5\n", "BASE_URL"},
		{"bad context", "BASE_URL=https://x/v1\nAPI_KEY=k\nMODEL_NAME=m\nCONTEXT_WINDOW=0\nMAX_TOKENS=5\n", "CONTEXT_WINDOW"},
		{"bad max", "BASE_URL=https://x/v1\nAPI_KEY=k\nMODEL_NAME=m\nCONTEXT_WINDOW=10\nMAX_TOKENS=-1\n", "MAX_TOKENS"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			clearConfigEnv(t)
			_, err := Load(writeEnv(t, tc.body))
			if err == nil || !strings.Contains(err.Error(), tc.want) { t.Fatalf("err = %v", err) }
		})
	}
}
```

The file helper must use `t.TempDir()` and `os.WriteFile(path, []byte(body), 0o600)`. Do not use `t.Setenv(name, "")` to clear variables because godotenv intentionally refuses to overwrite even an empty existing variable. Use this cleanup-safe helper:

```go
func clearConfigEnv(t *testing.T) {
	t.Helper()
	for _, name := range []string{"BASE_URL", "API_KEY", "MODEL_NAME", "CONTEXT_WINDOW", "MAX_TOKENS"} {
		value, existed := os.LookupEnv(name)
		if err := os.Unsetenv(name); err != nil { t.Fatal(err) }
		t.Cleanup(func() {
			if existed { _ = os.Setenv(name, value) } else { _ = os.Unsetenv(name) }
		})
	}
}
```

- [ ] **Step 4: Run the tests and confirm the expected failure**

Run: `go test ./internal/config -run TestLoad -v`

Expected: FAIL because `Config` and `Load` do not exist.

- [ ] **Step 5: Implement configuration loading**

Create `internal/config/config.go` with:

```go
type Config struct {
	BaseURL      string
	APIKey       string
	ModelName    string
	ContextWindow int64
	MaxTokens    int64
}

func Load(path string) (Config, error) {
	if err := godotenv.Load(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return Config{}, fmt.Errorf("load dotenv: %w", err)
	}
	c := Config{
		BaseURL: strings.TrimRight(strings.TrimSpace(os.Getenv("BASE_URL")), "/"),
		APIKey: strings.TrimSpace(os.Getenv("API_KEY")),
		ModelName: strings.TrimSpace(os.Getenv("MODEL_NAME")),
	}
	for name, target := range map[string]*int64{
		"CONTEXT_WINDOW": &c.ContextWindow,
		"MAX_TOKENS": &c.MaxTokens,
	} {
		value := strings.TrimSpace(os.Getenv(name))
		parsed, err := strconv.ParseInt(value, 10, 64)
		if err != nil || parsed <= 0 {
			return Config{}, fmt.Errorf("%s must be a positive integer", name)
		}
		*target = parsed
	}
	if c.BaseURL == "" || c.APIKey == "" || c.ModelName == "" {
		return Config{}, fmt.Errorf("BASE_URL, API_KEY, and MODEL_NAME are required")
	}
	u, err := url.ParseRequestURI(c.BaseURL)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return Config{}, fmt.Errorf("BASE_URL must be an absolute HTTP(S) URL")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return Config{}, fmt.Errorf("BASE_URL must use http or https")
	}
	return c, nil
}

func (c Config) SafeSummary() string {
	return fmt.Sprintf("model=%s base_url=%s context_window=%d max_tokens=%d", c.ModelName, c.BaseURL, c.ContextWindow, c.MaxTokens)
}
```

Use normal imports for `errors`, `fmt`, `net/url`, `os`, `strconv`, `strings`, and `github.com/joho/godotenv`.

- [ ] **Step 6: Verify, tidy, and commit**

Run:

```bash
gofmt -w internal/config
go mod tidy
go test ./internal/config -v
git diff --check
```

Expected: all configuration tests PASS and no whitespace errors.

Commit:

```bash
git add go.mod go.sum .gitignore .env.example internal/config
git commit -m "feat: add safe environment configuration"
```

---

### Task 2: Convert ADK conversation messages and multimodal parts

**Files:**
- Create: `openaiadapter/messages.go`
- Create: `openaiadapter/messages_test.go`
- Create: `openaiadapter/errors.go`

**Interfaces:**
- Produces: `convertContents(contents []*genai.Content) ([]openai.ChatCompletionMessageParamUnion, error)`
- Produces: `convertSystemInstruction(content *genai.Content) ([]openai.ChatCompletionMessageParamUnion, error)`
- Produces: `ConversionError{Path string, Kind string}`

- [ ] **Step 1: Write table tests for roles and parts**

Tests must marshal returned OpenAI params to JSON and compare decoded `map[string]any` values. Cover:

```go
func TestConvertContentsTextRoles(t *testing.T) {
	contents := []*genai.Content{
		{Role: "user", Parts: []*genai.Part{{Text: "hello"}}},
		{Role: "model", Parts: []*genai.Part{{Text: "hi"}}},
	}
	got, err := convertContents(contents)
	if err != nil { t.Fatal(err) }
	assertJSON(t, got[0], `{"role":"user","content":"hello"}`)
	assertJSON(t, got[1], `{"role":"assistant","content":"hi"}`)
}

func TestConvertContentsImages(t *testing.T) {
	content := &genai.Content{Role: "user", Parts: []*genai.Part{
		{Text: "describe"},
		{FileData: &genai.FileData{FileURI: "https://images.test/cat.png", MIMEType: "image/png"}},
		{InlineData: &genai.Blob{MIMEType: "image/jpeg", Data: []byte{1, 2, 3}}},
	}}
	got, err := convertContents([]*genai.Content{content})
	if err != nil { t.Fatal(err) }
	payload := marshalMap(t, got[0])
	parts := payload["content"].([]any)
	if len(parts) != 3 { t.Fatalf("parts = %d", len(parts)) }
	if !strings.HasPrefix(parts[2].(map[string]any)["image_url"].(map[string]any)["url"].(string), "data:image/jpeg;base64,") {
		t.Fatal("inline image was not converted to data URL")
	}
}
```

Also test system instructions, assistant function calls, user function responses, missing function-call IDs, non-image inline data, and unknown roles.

- [ ] **Step 2: Run the message tests and verify failure**

Run: `go test ./openaiadapter -run 'TestConvert' -v`

Expected: FAIL because conversion functions do not exist.

- [ ] **Step 3: Implement typed conversion errors**

Create `openaiadapter/errors.go`:

```go
type ConversionError struct {
	Path string
	Kind string
}

func (e *ConversionError) Error() string {
	return fmt.Sprintf("cannot convert %s at %s", e.Kind, e.Path)
}
```

- [ ] **Step 4: Implement content conversion**

Implement these rules in `openaiadapter/messages.go`:

```go
func convertContents(contents []*genai.Content) ([]openai.ChatCompletionMessageParamUnion, error)
func convertSystemInstruction(content *genai.Content) ([]openai.ChatCompletionMessageParamUnion, error)
func convertUserContent(index int, content *genai.Content) ([]openai.ChatCompletionMessageParamUnion, error)
func convertAssistantContent(index int, content *genai.Content) (openai.ChatCompletionMessageParamUnion, error)
func convertUserParts(path string, parts []*genai.Part) ([]openai.ChatCompletionContentPartUnionParam, error)
```

Use the OpenAI SDK constructors:

```go
openai.UserMessage("text")
openai.AssistantMessage("text")
openai.SystemMessage("instruction")
openai.ToolMessage(toolJSON, functionResponse.ID)
openai.TextContentPart(part.Text)
openai.ImageContentPart(openai.ChatCompletionContentPartImageImageURLParam{URL: imageURL})
```

For inline images, construct `data:<mime>;base64,<payload>` with `base64.StdEncoding.EncodeToString`. For assistant calls, JSON-marshal `FunctionCall.Args` and append:

```go
message.OfAssistant.ToolCalls = append(message.OfAssistant.ToolCalls,
	openai.ChatCompletionMessageToolCallUnionParam{
		OfFunction: &openai.ChatCompletionMessageFunctionToolCallParam{
			ID: call.ID,
			Function: openai.ChatCompletionMessageFunctionToolCallFunctionParam{
				Name: call.Name,
				Arguments: string(arguments),
			},
		},
	})
```

Function responses must be emitted as separate tool messages before any remaining user text/image message. Reject audio/video MIME types and non-HTTP `FileURI` values with `ConversionError`.

- [ ] **Step 5: Verify and commit message conversion**

Run:

```bash
gofmt -w openaiadapter
go test ./openaiadapter -run 'TestConvert' -v
go test ./...
```

Expected: all tests PASS.

Commit:

```bash
git add openaiadapter
git commit -m "feat: convert ADK multimodal messages"
```

---

### Task 3: Build requests with tools and structured output

**Files:**
- Create: `openaiadapter/requests.go`
- Create: `openaiadapter/requests_test.go`

**Interfaces:**
- Produces: `buildParams(req *model.LLMRequest, defaultModel string, defaultMaxTokens int64) (openai.ChatCompletionNewParams, []string, error)`
- Consumes: message conversion from Task 2.
- The returned `[]string` lists requested optional capabilities for sanitized provider errors.

- [ ] **Step 1: Write failing generation-setting tests**

Create tests that build a request with `Temperature`, `TopP`, `StopSequences`, `PresencePenalty`, `FrequencyPenalty`, `Seed`, and `MaxOutputTokens`; JSON-marshal the result and assert exact OpenAI field names and values. Include these precedence cases:

```go
func TestBuildParamsMaxTokensPrecedence(t *testing.T) {
	for _, tc := range []struct{ adk int32; fallback, want int64 }{
		{0, 384000, 384000},
		{2048, 384000, 2048},
		{400000, 384000, 384000},
	} {
		req := &model.LLMRequest{Config: &genai.GenerateContentConfig{MaxOutputTokens: tc.adk}}
		got, _, err := buildParams(req, "test-model", tc.fallback)
		if err != nil { t.Fatal(err) }
		payload := marshalMap(t, got)
		if int64(payload["max_tokens"].(float64)) != tc.want { t.Fatalf("payload = %#v", payload) }
	}
}
```

- [ ] **Step 2: Write failing tool and schema tests**

Cover `FunctionDeclarations`, `ParametersJsonSchema`, legacy `Parameters`, AUTO/ANY/NONE tool modes, allowed function names, `application/json` JSON-object mode, `ResponseJsonSchema`, and legacy `ResponseSchema`. Assert that strict schema produces:

```json
{
  "response_format": {
    "type": "json_schema",
    "json_schema": {
      "name": "adk_response",
      "strict": true,
      "schema": {"type": "object"}
    }
  }
}
```

- [ ] **Step 3: Run request tests and verify failure**

Run: `go test ./openaiadapter -run 'TestBuildParams' -v`

Expected: FAIL because `buildParams` does not exist.

- [ ] **Step 4: Implement generation and schema mapping**

Build `openai.ChatCompletionNewParams` with `Model: openai.ChatModel(modelName)`. Use `openai.Float`, `openai.Int`, and `openai.Bool` for optional SDK fields. Use the lower positive value of the ADK request limit and configured fallback. Map stop sequences through:

```go
params.Stop = openai.ChatCompletionNewParamsStopUnion{OfStringArray: slices.Clone(cfg.StopSequences)}
```

Convert arbitrary schema representations through a shared helper:

```go
func jsonObject(value any) (map[string]any, error) {
	data, err := json.Marshal(value)
	if err != nil { return nil, err }
	var result map[string]any
	if err := json.Unmarshal(data, &result); err != nil { return nil, err }
	return result, nil
}
```

Set JSON object mode with `shared.NewResponseFormatJSONObjectParam()`. Set strict JSON Schema mode with:

```go
params.ResponseFormat = openai.ChatCompletionNewParamsResponseFormatUnion{
	OfJSONSchema: &shared.ResponseFormatJSONSchemaParam{
		JSONSchema: shared.ResponseFormatJSONSchemaJSONSchemaParam{
			Name: "adk_response", Strict: openai.Bool(true), Schema: schema,
		},
	},
}
```

- [ ] **Step 5: Implement function-tool and tool-choice mapping**

Flatten only `genai.Tool.FunctionDeclarations`; return `ConversionError` for server-side Gemini-only tools. Convert each declaration to:

```go
openai.ChatCompletionFunctionTool(shared.FunctionDefinitionParam{
	Name: declaration.Name,
	Description: openai.String(declaration.Description),
	Parameters: shared.FunctionParameters(schema),
})
```

Map `AUTO` to `"auto"`, `ANY` to `"required"` unless one allowed function is specified (then emit a named function choice), and `NONE` to `"none"`:

```go
params.ToolChoice = openai.ChatCompletionToolChoiceOptionUnionParam{OfAuto: openai.String("auto")}
params.ToolChoice = openai.ChatCompletionToolChoiceOptionUnionParam{OfAuto: openai.String("required")}
params.ToolChoice = openai.ChatCompletionToolChoiceOptionUnionParam{OfAuto: openai.String("none")}
params.ToolChoice = openai.ToolChoiceOptionFunctionToolChoice(
	openai.ChatCompletionNamedToolChoiceFunctionParam{Name: allowedName},
)
```

Enable `ParallelToolCalls` whenever tools are present. Add capability labels `tools`, `images`, `json_object`, and `json_schema` only when the request uses them.

- [ ] **Step 6: Verify and commit request conversion**

Run:

```bash
gofmt -w openaiadapter
go test ./openaiadapter -run 'TestBuildParams' -v
go test ./...
```

Expected: all tests PASS.

Commit:

```bash
git add openaiadapter/requests.go openaiadapter/requests_test.go
git commit -m "feat: map tools and structured output"
```

---

### Task 4: Implement non-streaming model responses and safe provider errors

**Files:**
- Create: `openaiadapter/adapter.go`
- Create: `openaiadapter/responses.go`
- Create: `openaiadapter/adapter_test.go`
- Modify: `openaiadapter/errors.go`

**Interfaces:**
- Produces: `openaiadapter.Config{BaseURL, APIKey, Model string; ContextWindow, MaxTokens int64}`
- Produces: `openaiadapter.New(Config) (*openaiadapter.Model, error)`
- Produces: methods `Name() string` and `GenerateContent(context.Context, *model.LLMRequest, bool) iter.Seq2[*model.LLMResponse, error]`
- Consumes: `buildParams` from Task 3.

- [ ] **Step 1: Write an HTTP-level non-streaming test**

Use `httptest.NewServer` to assert the request path is `/v1/chat/completions`, Authorization is `Bearer test-key`, and model/message fields are correct. Return:

```json
{
  "id":"chat-1",
  "object":"chat.completion",
  "created":1,
  "model":"provider-model",
  "choices":[{
    "index":0,
    "finish_reason":"tool_calls",
    "message":{
      "role":"assistant",
      "content":"",
      "reasoning_content":"checked inputs",
      "tool_calls":[{
        "id":"call-1",
        "type":"function",
        "function":{"name":"lookup_time","arguments":"{\"timezone\":\"Asia/Shanghai\"}"}
      }]
    }
  }],
  "usage":{"prompt_tokens":10,"completion_tokens":4,"total_tokens":14}
}
```

Collect the `iter.Seq2` output and assert function name/arguments/ID, `FinishReason`, usage counts, `ModelVersion`, and `CustomMetadata["reasoning_content"]`.

- [ ] **Step 2: Write provider-error redaction tests**

Return HTTP 400 containing both the test endpoint and a fake key. Assert the returned error mentions status, model, and requested capability but not the key. Also test cancellation with a handler that blocks until `r.Context().Done()`.

- [ ] **Step 3: Run adapter tests and verify failure**

Run: `go test ./openaiadapter -run 'TestModel|TestProviderError' -v`

Expected: FAIL because `Model` and `New` do not exist.

- [ ] **Step 4: Implement model construction and non-streaming execution**

Construct the SDK client with:

```go
client := openai.NewClient(
	option.WithAPIKey(cfg.APIKey),
	option.WithBaseURL(strings.TrimRight(cfg.BaseURL, "/")),
)
```

Validate an absolute HTTP(S) base URL, non-empty key/model, and positive limits. `Name()` returns the configured model name. `GenerateContent` returns an iterator closure and delegates to non-streaming or streaming functions according to the `stream` argument.

For non-streaming calls, invoke `m.client.Chat.Completions.New(ctx, params)`, select choice zero, and convert text/tool calls into `genai.Part` values. Decode tool arguments with `json.Decoder.UseNumber()` into `map[string]any`; reject invalid or non-object JSON.

- [ ] **Step 5: Map completion metadata**

Build `model.LLMResponse` with:

```go
&model.LLMResponse{
	Content: &genai.Content{Role: "model", Parts: parts},
	UsageMetadata: &genai.GenerateContentResponseUsageMetadata{
		PromptTokenCount: int32(completion.Usage.PromptTokens),
		CandidatesTokenCount: int32(completion.Usage.CompletionTokens),
		TotalTokenCount: int32(completion.Usage.TotalTokens),
	},
	ModelVersion: completion.Model,
	FinishReason: mapFinishReason(choice.FinishReason),
	TurnComplete: true,
	CustomMetadata: metadata,
}
```

Read `reasoning_content` from `choice.Message.RawJSON()` using `encoding/json` into a private struct; preserve it in `CustomMetadata` without adding it to visible text.

- [ ] **Step 6: Implement safe provider errors**

Extend `errors.go` with `ProviderError` containing sanitized base URL, model, status, capabilities, and cause. Its `Error()` must never include response headers, Authorization, the API key, or full prompt bodies. When wrapping an `*openai.Error`, use its status and message only after replacing any exact API-key occurrence with `[REDACTED]`.

- [ ] **Step 7: Verify and commit non-streaming behavior**

Run:

```bash
gofmt -w openaiadapter
go test ./openaiadapter -run 'TestModel|TestProviderError' -v
go test ./...
go test -race ./...
```

Expected: all tests PASS under normal and race execution.

Commit:

```bash
git add openaiadapter
git commit -m "feat: implement OpenAI-compatible ADK model"
```

---

### Task 5: Add streaming text, reasoning, usage, and tool calls

**Files:**
- Create: `openaiadapter/stream.go`
- Create: `openaiadapter/stream_test.go`
- Modify: `openaiadapter/adapter.go`

**Interfaces:**
- Produces: `(*Model).stream(context.Context, openai.ChatCompletionNewParams, []string, func(*model.LLMResponse, error) bool)`
- Consumes: response and error conversion from Task 4.

- [ ] **Step 1: Write an SSE text-stream test**

The `httptest` handler must return `Content-Type: text/event-stream` and flush:

```text
data: {"id":"s1","object":"chat.completion.chunk","created":1,"model":"m","choices":[{"index":0,"delta":{"role":"assistant","reasoning_content":"think "},"finish_reason":null}]}

data: {"id":"s1","object":"chat.completion.chunk","created":1,"model":"m","choices":[{"index":0,"delta":{"content":"hello"},"finish_reason":null}]}

data: {"id":"s1","object":"chat.completion.chunk","created":1,"model":"m","choices":[{"index":0,"delta":{"content":" world"},"finish_reason":"stop"}]}

data: {"id":"s1","object":"chat.completion.chunk","created":1,"model":"m","choices":[],"usage":{"prompt_tokens":3,"completion_tokens":2,"total_tokens":5}}

data: [DONE]
```

Assert two partial visible responses (`hello`, ` world`), one final response with `TurnComplete=true`, accumulated reasoning metadata, finish reason, and usage.

- [ ] **Step 2: Write a fragmented streaming tool-call test**

Send tool-call deltas split across chunks by `index:0`: ID/name in the first chunk and arguments `{"timezone":` plus `"Asia/Shanghai"}` in later chunks. Assert that no incomplete function call is emitted and the final ADK part contains one complete `genai.FunctionCall`.

- [ ] **Step 3: Write malformed/cancelled stream tests**

Cover invalid tool JSON, a stream ending before `[DONE]`, provider error before headers, and context cancellation during the stream. Assert exactly one terminal error and no goroutine leak under `go test -race`.

- [ ] **Step 4: Run stream tests and verify failure**

Run: `go test ./openaiadapter -run 'TestStream' -v`

Expected: FAIL because the stream implementation does not exist.

- [ ] **Step 5: Implement the stream accumulator**

Create private state keyed by choice index and tool index:

```go
type toolCallAccumulator struct {
	ID        string
	Name      string
	Arguments strings.Builder
}

type streamAccumulator struct {
	model       string
	reasoning   strings.Builder
	finish      genai.FinishReason
	usage       *genai.GenerateContentResponseUsageMetadata
	toolCalls   map[int]*toolCallAccumulator
}
```

Use `m.client.Chat.Completions.NewStreaming(ctx, params)`, iterate with `stream.Next()`, and defer `stream.Close()`. Emit every non-empty content delta as `Partial: true`. Parse `reasoning_content` from each delta's `RawJSON()` and append only to metadata. Merge tool deltas by tool index, validate JSON at completion, then emit one final non-partial response with assembled function calls, usage, finish reason, and `TurnComplete: true`.

- [ ] **Step 6: Verify streaming and commit**

Run:

```bash
gofmt -w openaiadapter
go test ./openaiadapter -run 'TestStream' -v
go test ./...
go test -race ./...
```

Expected: all stream and full-suite tests PASS, including race detection.

Commit:

```bash
git add openaiadapter/adapter.go openaiadapter/stream.go openaiadapter/stream_test.go
git commit -m "feat: stream text and tool calls"
```

---

### Task 6: Wire the demo agent and document end-to-end usage

**Files:**
- Create: `main.go`
- Modify: `README.md`
- Modify: `.env.example` only if configuration names changed during compilation fixes

**Interfaces:**
- Consumes: `config.Load(".env")`
- Consumes: `openaiadapter.New(openaiadapter.Config{...})`
- Produces command: `go run . console`

- [ ] **Step 1: Write the deterministic demo tool**

In `main.go`, define:

```go
type timeArgs struct {
	Timezone string `json:"timezone" jsonschema:"description=IANA timezone such as Asia/Shanghai"`
}

type timeResult struct {
	Timezone string `json:"timezone"`
	Time     string `json:"time"`
}

func lookupTime(_ agent.Context, args timeArgs) (timeResult, error) {
	location, err := time.LoadLocation(args.Timezone)
	if err != nil { return timeResult{}, fmt.Errorf("invalid timezone %q", args.Timezone) }
	fixed := time.Date(2026, 7, 13, 12, 0, 0, 0, time.UTC)
	return timeResult{Timezone: args.Timezone, Time: fixed.In(location).Format(time.RFC3339)}, nil
}
```

Use a fixed instant so tests and demonstrations are deterministic and side-effect free.

- [ ] **Step 2: Wire the model, tool, agent, and launcher**

Construct the function tool with `functiontool.New`, then create:

```go
rootAgent, err := llmagent.New(llmagent.Config{
	Name: "openai_compatible_assistant",
	Description: "An assistant backed by an OpenAI-compatible endpoint.",
	Instruction: "Be concise and helpful. Use lookup_time when the user asks for the time in a named timezone.",
	Model: adaptedModel,
	Tools: []tool.Tool{timeTool},
})
```

Start `full.NewLauncher()` with `launcher.Config{AgentLoader: agent.NewSingleLoader(rootAgent)}` and call `Execute(ctx, launcherConfig, os.Args[1:])`. Log only `cfg.SafeSummary()` before startup.

- [ ] **Step 3: Run compile-time and offline verification**

Run:

```bash
gofmt -w main.go
go mod tidy
go test ./...
go test -race ./...
go vet ./...
go build ./...
git diff --check
```

Expected: every command exits zero without a real `.env` because tests do not invoke `main`; the built binary compiles successfully.

- [ ] **Step 4: Rewrite the README with exact setup and smoke tests**

Document:

```bash
cp .env.example .env
# edit API_KEY in .env
go run . console
```

Document these manual prompts:

```text
只用一句话介绍 ADK Go。
现在 Asia/Shanghai 是几点？请调用工具，不要自己计算。
```

Explain that image and strict JSON Schema payloads are implemented at the protocol layer but still depend on the selected provider/model. Include a provider-switching example that changes only `BASE_URL`, `API_KEY`, and `MODEL_NAME`. State that `MAX_TOKENS` is an output cap and that a provider may reject `384000`; users must lower it to that provider's supported maximum.

- [ ] **Step 5: Perform a secret and repository audit**

Run:

```bash
git status --short
git check-ignore .env
rg -n 'API_KEY=.+|Bearer [A-Za-z0-9_-]{8,}' --glob '!docs/superpowers/**' . || true
git diff --check
```

Expected: `.env` is ignored; no populated key or bearer token appears; only intended source/docs changes remain.

- [ ] **Step 6: Commit the runnable demo**

```bash
git add main.go README.md go.mod go.sum .env.example .gitignore
git commit -m "feat: add runnable ADK console demo"
```

- [ ] **Step 7: Optional paid smoke test with the user's local key**

Run only when `API_KEY` has been filled locally:

```bash
go run . console
```

Expected: console starts, ordinary text produces a streamed answer, and the timezone prompt invokes `lookup_time` before the model returns its final answer. If the provider rejects `MAX_TOKENS=384000`, lower the local `.env` value to its documented maximum and repeat; do not change committed defaults without user approval.

---

## Final Verification

Run:

```bash
go test ./...
go test -race ./...
go vet ./...
go build ./...
git status --short
git log --oneline -7
```

Expected:

- all tests, race tests, vet, and build pass;
- the worktree is clean except for an intentionally untracked/ignored local `.env`;
- commits show configuration, messages, tools/schemas, non-streaming adapter, streaming adapter, and runnable demo as separately reviewable changes;
- no test or build requires a paid provider call.
