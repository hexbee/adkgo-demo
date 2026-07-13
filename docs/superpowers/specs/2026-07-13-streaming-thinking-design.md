# Streaming Thinking Design

**Date:** 2026-07-13

## Goal

Expose OpenAI-compatible `reasoning_content` as native ADK thought parts so ADK Web UI can display the model's thinking stream separately from its final answer. Add portable environment configuration for requesting thinking, and preserve reasoning correctly across multi-step tool calls.

This iteration targets providers that use the DeepSeek-style `reasoning_content` field in OpenAI Chat Completions responses and assistant history. It must preserve all existing text, image, structured-output, tool, MCP, Skills, and CLI behavior.

## User Experience

The existing launch command remains unchanged:

```bash
go run . web webui api
```

When the provider returns streamed `reasoning_content`, the Web UI first renders it using ADK's native Thought presentation. Streamed visible `content` then appears as the normal assistant answer. Both advance chunk by chunk in provider order.

The optional `.env` configuration is:

```dotenv
THINKING_MODE=auto
REASONING_EFFORT=high
```

`THINKING_MODE` accepts:

- `auto`, the default: do not send the provider-specific `thinking` request field, but display and preserve reasoning whenever the provider returns it;
- `enabled`: send `"thinking": {"type": "enabled"}`;
- `disabled`: send `"thinking": {"type": "disabled"}`.

`REASONING_EFFORT` is optional and accepts `high` or `max`. When present, it is sent using the standard `reasoning_effort` Chat Completions field. Setting an effort while `THINKING_MODE=disabled` is a configuration error.

This keeps the default portable across OpenAI-compatible services while allowing DeepSeek thinking to be requested explicitly. DeepSeek V4 currently defaults to thinking mode, but the application does not depend on that provider default.

## Selected Architecture

Use native `genai.Part` values with `Thought: true`. Do not fork the bundled ADK Web UI, extend the ADK REST event schema, or expose reasoning as ordinary answer text.

This matches the representation ADK Web UI already understands:

```go
&genai.Part{Text: reasoning, Thought: true}
```

The adapter will continue storing the complete reasoning string in `LLMResponse.CustomMetadata["reasoning_content"]` for backward compatibility with current adapter callers and tests. The Web UI path does not depend on that metadata because ADK's REST event model does not expose arbitrary custom metadata.

## Configuration Flow

`internal/config.Config` gains `ThinkingMode` and `ReasoningEffort`. `Load` trims and normalizes the values, applies `auto` when `THINKING_MODE` is empty, and validates the allowed values before model construction.

`SafeSummary` includes `thinking_mode` and includes `reasoning_effort` only when the latter is non-empty. It must never include the API key, raw reasoning, prompts, tool arguments, or response content.

`openaiadapter.Config` gains the same fields and repeats validation in `openaiadapter.New`. This keeps the adapter safe when used directly instead of through `internal/config`.

`main.go` passes the validated values into `openaiadapter.New`.

## Request Encoding

The request builder applies thinking configuration after creating the standard `openai.ChatCompletionNewParams`:

- `auto`: add no `thinking` extra field;
- `enabled`: use the SDK parameter object's `SetExtraFields` escape hatch to add `thinking.type=enabled`;
- `disabled`: add `thinking.type=disabled`;
- non-empty effort: set `ChatCompletionNewParams.ReasoningEffort`.

No provider is detected from its URL or model name. Behavior is entirely configuration-driven.

Thinking providers may ignore sampling parameters such as temperature and top-p. This iteration does not silently remove existing generation settings because that would change established adapter behavior for other providers.

## Response Conversion

### Non-streaming

When a response message contains `reasoning_content`, `fromCompletion` constructs content parts in this order:

1. one complete text part with `Thought: true`;
2. the visible assistant content, if any;
3. function calls, in provider order.

It also retains the complete reasoning string in `CustomMetadata`.

An absent or empty `reasoning_content` creates no thought part. If a provider returns reasoning despite `THINKING_MODE=disabled`, the adapter still faithfully represents the returned data instead of discarding it.

### Streaming

For each provider chunk and choice, processing preserves field order within the adapter:

1. if the chunk contains `reasoning_content`, append it to the reasoning accumulator and immediately yield a partial `LLMResponse` containing a `Thought: true` text part;
2. if the chunk contains visible `content`, append it to the answer accumulator and immediately yield the existing ordinary partial text response;
3. accumulate tool-call fragments as today.

The final non-partial event contains the complete reasoning thought part, complete visible text, and decoded tool calls in the same canonical order as non-streaming conversion. It remains responsible for session persistence, usage, finish reason, and tool execution. Existing ADK partial-event handling prevents the final aggregate from becoming a second user-visible answer.

This produces two independently streamed phases when the provider sends reasoning first and visible content second. If a provider includes both fields in one chunk, the reasoning partial is yielded before the content partial. Cross-chunk ordering is unchanged.

## Assistant History and Tool Calls

`convertAssistantContent` separates assistant text parts into:

- thought text: parts with `Thought: true`;
- visible text: parts with `Thought: false`;
- function calls.

Visible text alone becomes the assistant message's standard `content`. Thought text is concatenated in part order and attached to that assistant message as the extra field `reasoning_content`. It is never copied into visible content.

This rule applies whether or not the assistant message includes tool calls. It is essential for DeepSeek thinking-mode tool calls: the complete reasoning that preceded a tool call must be returned with the assistant tool-call message in subsequent sub-requests. Function responses continue to use ordinary OpenAI `tool` messages.

Thought-only assistant messages are valid when accompanied by tool calls. An assistant history item containing neither visible text, thought text, nor a function call remains subject to existing SDK/provider validation.

## Error Handling and Privacy

- Invalid thinking mode or reasoning effort fails during configuration loading with the relevant environment variable name.
- `disabled` combined with non-empty effort fails startup as contradictory configuration.
- A provider rejection of explicit thinking fields uses the existing redacted `ProviderError`; the adapter does not retry in `auto` or silently switch modes.
- Malformed or unsupported provider responses follow existing conversion errors.
- Reasoning is never written to application logs.
- Web UI display intentionally exposes provider-returned reasoning to the local UI. Users should treat it as potentially sensitive model output and should not expose this development UI to untrusted viewers.
- Provider-returned reasoning is not guaranteed to be a faithful or complete account of model computation; the UI labels it as the provider's Thought content rather than an audit trace.

## Components and Changes

### `internal/config`

- add and validate `ThinkingMode` and `ReasoningEffort`;
- default the mode to `auto`;
- add only safe values to `SafeSummary`;
- extend environment cleanup and table-driven validation tests.

### `openaiadapter`

- extend `Config` and model construction validation;
- encode request-level thinking and effort;
- serialize thought history into assistant `reasoning_content`;
- convert response reasoning into native thought parts for stream and non-stream paths;
- retain `CustomMetadata` compatibility.

### `main.go` and documentation

- pass thinking configuration into the adapter;
- document the new environment variables, default behavior, Web UI command, privacy note, and service-provider compatibility.

No ADK source, bundled Web UI asset, MCP runtime, Skills runtime, or command runner file will be modified.

## Testing

All automated tests use local files or `httptest` servers and expose no real credentials.

Configuration tests cover:

- missing variables default to `THINKING_MODE=auto` with empty effort;
- `auto`, `enabled`, and `disabled` are accepted;
- `high` and `max` are accepted;
- unknown values are rejected;
- `disabled` plus effort is rejected;
- safe summaries contain no API key or response content.

Adapter request tests inspect received JSON and cover:

- `auto` omits `thinking`;
- enabled and disabled modes emit the correct object;
- effort is emitted only when configured;
- thought assistant history emits `reasoning_content` and keeps visible content separate;
- a thought-bearing assistant tool call followed by a tool response round-trips correctly.

Response tests cover:

- non-streaming thought, visible text, and tool-call part ordering;
- no-reasoning compatibility;
- streamed reasoning partials are marked `Thought: true`;
- streamed visible partials remain ordinary text;
- final aggregate contains complete reasoning, answer, and tools;
- metadata retains the complete reasoning string;
- provider errors remain redacted.

Repository validation remains:

```bash
go test ./...
go test -race ./...
go vet ./...
go build ./...
```

A final DeepSeek V4 Flash smoke test starts `go run . web webui api` and verifies:

1. a reasoning question displays streamed Thought followed by streamed visible text;
2. a tool-using question completes at least one reasoning → tool call → tool result → reasoning/final-answer cycle without a missing-reasoning 400 response;
3. no unrelated MCP tool is approved during the smoke test.

## Deferred Work

- provider-specific reasoning response fields other than `reasoning_content`;
- encrypted or opaque reasoning item formats used by other APIs;
- custom Thought rendering or UI controls beyond ADK Web UI's native presentation;
- persisting arbitrary `CustomMetadata` through ADK REST;
- per-request UI overrides for thinking mode or effort;
- reasoning redaction, summarization, export, or audit features.
