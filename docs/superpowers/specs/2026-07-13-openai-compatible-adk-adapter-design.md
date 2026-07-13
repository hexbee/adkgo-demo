# OpenAI-Compatible ADK Go Adapter Design

## Goal

Build a reusable `model.LLM` adapter for `google.golang.org/adk/v2` that talks to OpenAI-compatible Chat Completions endpoints. The demo must run locally without cloning the ADK Go repository and must allow switching among DeepSeek, Volcano Engine, Alibaba Cloud, and other compatible providers by editing `.env` only.

The initial local configuration is:

```dotenv
BASE_URL=https://api.deepseek.com/v1
API_KEY=
MODEL_NAME=deepseek-v4-flash
CONTEXT_WINDOW=1000000
MAX_TOKENS=384000
```

`.env` is local-only and ignored by Git. A safe `.env.example` is committed.

## Scope

The adapter targets the OpenAI Chat Completions protocol rather than provider-specific behavior. It supports:

- system, user, assistant, and tool messages;
- multi-turn text conversations;
- image URL and base64 image parts;
- function tools with JSON Schema parameters;
- tool selection, parallel tool calls, and tool-result messages;
- plain JSON output and strict JSON Schema structured output;
- streaming text and streaming tool-call argument assembly;
- finish reasons and token-usage reporting;
- generation controls including temperature, top-p, stop sequences, and maximum output tokens;
- preservation of provider-specific reasoning content when the response exposes it.

Audio, video, the OpenAI Responses API, provider-specific authentication schemes, and provider-specific capability tables are outside the first version.

## Architecture

### Configuration

A small configuration package loads `.env`, then reads process environment variables so shell-provided values can override file values. It validates that `BASE_URL`, `API_KEY`, and `MODEL_NAME` are present and that numeric limits are positive.

`MAX_TOKENS` is sent as the per-request maximum output-token limit unless an ADK request supplies a smaller explicit limit. `CONTEXT_WINDOW` records the declared model capacity and is included in sanitized startup diagnostics. The adapter does not claim exact local tokenization, enforce this limit locally, or silently truncate conversation history.

Secrets are never included in logs or returned errors.

### OpenAI-Compatible Model Adapter

The `openaiadapter` package implements ADK's `model.LLM` interface. It owns the OpenAI SDK client, configured with the environment-provided API key and base URL.

For each call it:

1. converts ADK conversation content into Chat Completions messages;
2. converts ADK tool declarations and generation settings into OpenAI request fields;
3. sends either a streaming or non-streaming request as requested by ADK;
4. converts text, tool calls, structured content, finish reasons, and usage back into ADK responses.

The adapter does not hard-code DeepSeek, Volcano Engine, Alibaba Cloud, or any model name. Optional protocol fields are sent only when the ADK request uses the corresponding feature. If a provider does not implement a field, its API error is returned with endpoint, model, and feature context.

### Multimodal Messages

Text parts map to OpenAI text content. Remote images map to `image_url` content, while inline image bytes map to data URLs with their MIME type. Unsupported ADK part types return a typed conversion error rather than being dropped.

This is protocol-level support. A selected provider or model may still reject multimodal input.

### Tools

ADK function declarations map to OpenAI function tools with their names, descriptions, and JSON Schemas intact. Assistant tool calls are converted back into ADK function-call parts, and ADK function responses are converted into tool messages linked by tool-call ID.

Streaming tool-call fragments are accumulated by choice and tool index until a complete ADK function call can be emitted. Invalid JSON arguments produce an explicit adapter error and are never passed to a local tool unchecked.

### Structured Output

When the ADK request includes an output schema, the adapter prefers OpenAI `json_schema` structured output. Plain JSON mode maps to `json_object`. The prompt must still tell the model what JSON to produce.

Because provider support varies, the adapter does not silently downgrade strict schema requests. A provider rejection is reported clearly so the caller can choose a different model or request mode.

### Streaming and Reasoning

Streaming responses emit text increments promptly. Tool-call name and argument fragments are reassembled deterministically. Final usage data and finish reason are attached when supplied by the endpoint.

Reasoning content is preserved in ADK response metadata when the OpenAI SDK exposes it. It is not mixed into the user-visible final answer and is not required for adapter correctness.

### Demo Application

The root application creates one `llmagent` with the adapter and starts the standard ADK full launcher. The primary command is:

```bash
go run . console
```

The agent includes one deterministic, side-effect-free local tool so tool calling can be tested immediately. Launcher-supported server or web modes remain available through the same binary when supported by the installed ADK version.

## Error Handling

Errors are categorized as:

- configuration errors before startup;
- ADK-to-OpenAI conversion errors;
- provider HTTP/API errors;
- stream decoding or incomplete tool-call errors;
- OpenAI-to-ADK conversion errors.

Provider errors include the sanitized base URL, model name, HTTP status, and relevant requested capability. API keys, authorization headers, and complete sensitive prompts are excluded.

Cancellation from the ADK context is propagated to outbound requests and streaming readers.

## Testing

Tests use `httptest.Server`; they never require a real API key or paid model call. Coverage includes:

- environment loading and validation;
- ordinary text requests and responses;
- multi-turn role conversion;
- image URL and base64 image conversion;
- function declaration conversion;
- non-streaming and streaming tool calls;
- tool-result round trips;
- JSON object and JSON Schema response formats;
- generation settings and maximum-token precedence;
- usage and finish-reason conversion;
- provider error sanitization;
- cancellation and malformed stream handling.

A manual smoke-test section documents DeepSeek console startup, a normal chat, and a tool-calling prompt. Multimodal and strict-schema smoke tests are documented as provider-dependent.

## Repository Layout

```text
.
├── main.go
├── go.mod
├── go.sum
├── .env.example
├── .gitignore
├── README.md
├── internal/config/
│   ├── config.go
│   └── config_test.go
└── openaiadapter/
    ├── adapter.go
    ├── messages.go
    ├── requests.go
    ├── responses.go
    └── *_test.go
```

The public adapter package is separated from demo configuration and startup code so it can later be copied into another ADK Go project without depending on this repository's CLI.

## Success Criteria

- `go test ./...` passes without network credentials.
- `go run . console` starts with a valid `.env`.
- Plain multi-turn chat works against the configured DeepSeek-compatible endpoint.
- A local ADK function tool can be selected, invoked, and returned to the model.
- The adapter generates valid OpenAI-compatible payloads for images and structured output even when the current provider does not support those features.
- Switching provider, endpoint, and model requires `.env` changes only.
- No API key appears in tracked files, logs, errors, or test snapshots.
