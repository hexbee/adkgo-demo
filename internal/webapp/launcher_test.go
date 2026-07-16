package webapp

import (
	"context"
	"encoding/json"
	"errors"
	"iter"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"github.com/hexbee/adkgo-demo/internal/skillsruntime"
	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/cmd/launcher"
	"google.golang.org/adk/v2/model"
	"google.golang.org/adk/v2/session"
	"google.golang.org/genai"
)

type titleGeneratorFunc func(context.Context, string, string) (string, error)

func (f titleGeneratorFunc) Generate(ctx context.Context, question, answer string) (string, error) {
	return f(ctx, question, answer)
}

func TestConfigureSessionServicePersistsSessions(t *testing.T) {
	ctx := t.Context()
	dbPath := filepath.Join(t.TempDir(), "sessions.db")
	first := &launcher.Config{}
	if err := configureSessionService(first, dbPath); err != nil {
		t.Fatalf("configure first session service: %v", err)
	}
	created, err := first.SessionService.Create(ctx, &session.CreateRequest{
		AppName: "test_agent", UserID: "local-user", SessionID: "session-1",
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	if created.Session.ID() != "session-1" {
		t.Fatalf("created session ID = %q, want session-1", created.Session.ID())
	}
	event := &session.Event{
		ID:          "event-1",
		Author:      "user",
		LLMResponse: model.LLMResponse{Content: genai.NewContentFromText("persist me", genai.RoleUser)},
	}
	if err := first.SessionService.AppendEvent(ctx, created.Session, event); err != nil {
		t.Fatalf("append chat event: %v", err)
	}

	second := &launcher.Config{}
	if err := configureSessionService(second, dbPath); err != nil {
		t.Fatalf("configure second session service: %v", err)
	}
	loaded, err := second.SessionService.Get(ctx, &session.GetRequest{
		AppName: "test_agent", UserID: "local-user", SessionID: "session-1",
	})
	if err != nil {
		t.Fatalf("load persisted session: %v", err)
	}
	if loaded.Session.ID() != "session-1" {
		t.Fatalf("loaded session ID = %q, want session-1", loaded.Session.ID())
	}
	if loaded.Session.Events().Len() != 1 {
		t.Fatalf("loaded event count = %d, want 1", loaded.Session.Events().Len())
	}
	content := loaded.Session.Events().At(0).Content
	if content == nil || len(content.Parts) != 1 || content.Parts[0].Text != "persist me" {
		t.Fatalf("loaded chat content = %#v, want persist me", content)
	}
}

func TestConfigureSessionServiceCanUseMemoryOnly(t *testing.T) {
	cfg := &launcher.Config{}
	if err := configureSessionService(cfg, ""); err != nil {
		t.Fatalf("configure in-memory session service: %v", err)
	}
	if cfg.SessionService == nil {
		t.Fatal("session service is nil")
	}
}

func TestSessionTitleEndpointGeneratesPersistsAndReusesTitle(t *testing.T) {
	ctx := t.Context()
	dbPath := filepath.Join(t.TempDir(), "sessions.db")
	cfg := &launcher.Config{}
	if err := configureSessionService(cfg, dbPath); err != nil {
		t.Fatalf("configure session service: %v", err)
	}
	created, err := cfg.SessionService.Create(ctx, &session.CreateRequest{
		AppName: "test_agent", UserID: "local-user", SessionID: "session-1",
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	updatedAt := created.Session.LastUpdateTime()
	for _, event := range []*session.Event{
		{
			ID: "user-event", Author: "user", Timestamp: updatedAt.Add(time.Microsecond),
			LLMResponse: model.LLMResponse{Content: genai.NewContentFromText("请显示 Node、Go 和 Python 的版本", genai.RoleUser)},
		},
		{
			ID: "assistant-event", Author: "test_agent", Timestamp: updatedAt.Add(2 * time.Microsecond),
			LLMResponse: model.LLMResponse{Content: genai.NewContentFromText("三个运行时版本已经检查完毕。", genai.RoleModel)},
		},
	} {
		if err := cfg.SessionService.AppendEvent(ctx, created.Session, event); err != nil {
			t.Fatalf("append event %s: %v", event.ID, err)
		}
	}

	generateCalls := 0
	generator := titleGeneratorFunc(func(_ context.Context, question, answer string) (string, error) {
		generateCalls++
		if question != "请显示 Node、Go 和 Python 的版本" || answer != "三个运行时版本已经检查完毕。" {
			t.Fatalf("first exchange = (%q, %q)", question, answer)
		}
		return "运行时版本检查", nil
	})
	handler := sessionTitleHandler(cfg.SessionService, generator)

	request := httptest.NewRequest(http.MethodPost, "/title", strings.NewReader("{}"))
	request = mux.SetURLVars(request, map[string]string{
		"app_name": "test_agent", "user_id": "local-user", "session_id": "session-1",
	})
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("first title status = %d, body = %q", recorder.Code, recorder.Body.String())
	}
	var response map[string]string
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode title response: %v", err)
	}
	if response["title"] != "运行时版本检查" || response["source"] != sessionTitleSourceModel {
		t.Fatalf("title response = %#v", response)
	}

	reopened := &launcher.Config{}
	if err := configureSessionService(reopened, dbPath); err != nil {
		t.Fatalf("reopen session service: %v", err)
	}
	listed, err := reopened.SessionService.List(ctx, &session.ListRequest{AppName: "test_agent", UserID: "local-user"})
	if err != nil || len(listed.Sessions) != 1 {
		t.Fatalf("list titled session = %#v, %v", listed, err)
	}
	if got := titleFromState(listed.Sessions[0]); got != "运行时版本检查" {
		t.Fatalf("persisted title = %q", got)
	}
	if got := titleSourceFromState(listed.Sessions[0]); got != sessionTitleSourceModel {
		t.Fatalf("persisted title source = %q", got)
	}
	if listed.Sessions[0].Events().Len() != 0 {
		t.Fatalf("listed session event count = %d, want 0", listed.Sessions[0].Events().Len())
	}

	request = httptest.NewRequest(http.MethodPost, "/title", strings.NewReader("{}"))
	request = mux.SetURLVars(request, map[string]string{
		"app_name": "test_agent", "user_id": "local-user", "session_id": "session-1",
	})
	recorder = httptest.NewRecorder()
	sessionTitleHandler(reopened.SessionService, generator).ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || generateCalls != 1 {
		t.Fatalf("reused title status = %d, generation calls = %d", recorder.Code, generateCalls)
	}
}

func TestSessionTitleEndpointRetriesFallbackAndMigratesLegacyState(t *testing.T) {
	ctx := t.Context()
	service := session.InMemoryService()
	created, err := service.Create(ctx, &session.CreateRequest{
		AppName: "test_agent", UserID: "local-user", SessionID: "legacy-session",
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	updatedAt := created.Session.LastUpdateTime()
	question := "请检查运行时版本。"
	for _, event := range []*session.Event{
		{
			ID: "legacy-user", Author: "user", Timestamp: updatedAt.Add(time.Microsecond),
			LLMResponse: model.LLMResponse{Content: genai.NewContentFromText(question, genai.RoleUser)},
		},
		{
			ID: "legacy-assistant", Author: "test_agent", Timestamp: updatedAt.Add(2 * time.Microsecond),
			LLMResponse: model.LLMResponse{Content: genai.NewContentFromText("检查已经完成。", genai.RoleModel)},
		},
		{
			ID: "legacy-title", Author: "session_title", Timestamp: updatedAt.Add(3 * time.Microsecond),
			Actions: session.EventActions{StateDelta: map[string]any{
				sessionTitleStateKey: fallbackSessionTitle(question),
			}},
		},
	} {
		if err := service.AppendEvent(ctx, created.Session, event); err != nil {
			t.Fatalf("append event %s: %v", event.ID, err)
		}
	}

	call := func(generator titleGenerator) map[string]string {
		t.Helper()
		request := httptest.NewRequest(http.MethodPost, "/title", strings.NewReader("{}"))
		request = mux.SetURLVars(request, map[string]string{
			"app_name": "test_agent", "user_id": "local-user", "session_id": "legacy-session",
		})
		recorder := httptest.NewRecorder()
		sessionTitleHandler(service, generator).ServeHTTP(recorder, request)
		if recorder.Code != http.StatusOK {
			t.Fatalf("title status = %d, body = %q", recorder.Code, recorder.Body.String())
		}
		var response map[string]string
		if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
			t.Fatalf("decode title response: %v", err)
		}
		return response
	}

	failed := call(titleGeneratorFunc(func(context.Context, string, string) (string, error) {
		return "", errors.New("temporary title failure")
	}))
	if failed["title"] != fallbackSessionTitle(question) || failed["source"] != sessionTitleSourceFallback {
		t.Fatalf("fallback response = %#v", failed)
	}

	retried := call(titleGeneratorFunc(func(context.Context, string, string) (string, error) {
		return "运行时版本检查", nil
	}))
	if retried["title"] != "运行时版本检查" || retried["source"] != sessionTitleSourceModel {
		t.Fatalf("retried response = %#v", retried)
	}
	loaded, err := service.Get(ctx, &session.GetRequest{
		AppName: "test_agent", UserID: "local-user", SessionID: "legacy-session",
	})
	if err != nil {
		t.Fatalf("get retried session: %v", err)
	}
	if titleFromState(loaded.Session) != "运行时版本检查" || titleSourceFromState(loaded.Session) != sessionTitleSourceModel {
		t.Fatalf("retried state title = %q, source = %q", titleFromState(loaded.Session), titleSourceFromState(loaded.Session))
	}
}

func TestNormalizeSessionTitle(t *testing.T) {
	if got := normalizeSessionTitle("标题： “Node、Go 与 Python 版本检查。”\n额外说明"); got != "Node、Go 与 Python 版本检查" {
		t.Fatalf("normalized title = %q", got)
	}
	if got := normalizeSessionTitle(strings.Repeat("会", maxSessionTitleRunes+5)); len([]rune(got)) != maxSessionTitleRunes {
		t.Fatalf("normalized title rune count = %d", len([]rune(got)))
	}
}

func testHandler(t *testing.T) http.Handler {
	t.Helper()
	testAgent, err := agent.New(agent.Config{
		Name:        "test_agent",
		Description: "test agent",
		Run: func(agent.InvocationContext) iter.Seq2[*session.Event, error] {
			return func(func(*session.Event, error) bool) {}
		},
	})
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}
	handler, err := newHandler(&launcher.Config{
		AgentLoader: agent.NewSingleLoader(testAgent),
	}, &config{sseWriteTimeout: time.Second, traceCapacity: 10}, nil, []skillsruntime.Summary{
		{Name: "concise-writer", Description: "Make writing concise."},
		{Name: "follow-builders-lite", Description: "Summarize AI builder news."},
	}, RuntimeInfo{
		BaseURL: "https://api.deepseek.com/v1", ModelName: "deepseek-v4-flash",
		ContextWindow: 1000000, MaxTokens: 384000, ThinkingMode: "enabled", ReasoningEffort: "high",
	})
	if err != nil {
		t.Fatalf("newHandler: %v", err)
	}
	return handler
}

func TestHandlerServesRuntimeInfoWithoutCredentials(t *testing.T) {
	recorder := httptest.NewRecorder()
	testHandler(t).ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/runtime-info", nil))

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
	}
	var got RuntimeInfo
	if err := json.Unmarshal(recorder.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode runtime info: %v", err)
	}
	if got.ModelName != "deepseek-v4-flash" || got.ContextWindow != 1000000 || got.MaxTokens != 384000 || got.ThinkingMode != "enabled" || got.ReasoningEffort != "high" {
		t.Fatalf("runtime info = %#v", got)
	}
	if strings.Contains(strings.ToLower(recorder.Body.String()), "api_key") || strings.Contains(strings.ToLower(recorder.Body.String()), "apikey") {
		t.Fatalf("runtime info exposes credential field: %s", recorder.Body.String())
	}
}

func TestHandlerServesWorkbenchWithSecurityHeaders(t *testing.T) {
	recorder := httptest.NewRecorder()
	testHandler(t).ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/", nil))

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
	}
	if body := recorder.Body.String(); !strings.Contains(body, "Agent Work") || !strings.Contains(body, "MCP · CLI · Skills") || strings.Contains(body, "ADK Web UI") {
		t.Fatalf("unexpected web app body: %q", body[:min(len(body), 200)])
	}
	for _, marker := range []string{`id="confirmation-mode-menu"`, "逐项确认", "本次授权", "下一条任务中的工具自动执行"} {
		if !strings.Contains(recorder.Body.String(), marker) {
			t.Fatalf("web app body missing one-shot execution mode marker %q", marker)
		}
	}
	for _, marker := range []string{`id="run-state"`, `role="status"`, `aria-live="polite"`, `hidden><span></span>执行中`} {
		if !strings.Contains(recorder.Body.String(), marker) {
			t.Fatalf("web app body missing contextual run-state marker %q", marker)
		}
	}
	for _, marker := range []string{`id="skill-picker"`, `contenteditable="true"`, "输入 $ 调用 Skill"} {
		if !strings.Contains(recorder.Body.String(), marker) {
			t.Fatalf("web app body missing Skill composer marker %q", marker)
		}
	}
	if strings.Count(recorder.Body.String(), `class="brand-mark-links"`) != 2 {
		t.Fatal("workbench must use the Agent Work mark in both the sidebar and empty state")
	}
	if strings.Contains(recorder.Body.String(), "M11 34 24 9l13 25") {
		t.Fatal("workbench still contains the legacy empty-state mark")
	}
	for _, marker := range []string{`id="runtime-model"`, `id="runtime-base-url"`, `id="runtime-context"`, `id="runtime-max-output"`, `id="runtime-thinking"`, `id="runtime-reasoning-effort"`, "推理强度"} {
		if !strings.Contains(recorder.Body.String(), marker) {
			t.Fatalf("web app body missing runtime information marker %q", marker)
		}
	}
	for _, marker := range []string{"/assets/vendor/katex/katex.min.css", "/assets/vendor/katex/katex.min.js", "/assets/vendor/katex/contrib/auto-render.min.js"} {
		if !strings.Contains(recorder.Body.String(), marker) {
			t.Fatalf("web app body missing KaTeX asset %q", marker)
		}
	}
	for _, marker := range []string{"/assets/vendor/highlightjs/styles/github-dark-dimmed.min.css", "/assets/vendor/highlightjs/highlight.min.js"} {
		if !strings.Contains(recorder.Body.String(), marker) {
			t.Fatalf("web app body missing Highlight.js asset %q", marker)
		}
	}
	if got := recorder.Header().Get("Content-Security-Policy"); !strings.Contains(got, "default-src 'self'") || !strings.Contains(got, "style-src 'self' 'unsafe-inline'") || !strings.Contains(got, "img-src 'self' data: https:") {
		t.Fatalf("Content-Security-Policy = %q", got)
	}
}

func TestHandlerServesProjectSkillCatalog(t *testing.T) {
	recorder := httptest.NewRecorder()
	testHandler(t).ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/project-skills", nil))

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
	}
	if got := recorder.Header().Get("Content-Type"); got != "application/json; charset=utf-8" {
		t.Fatalf("Content-Type = %q", got)
	}
	var got []skillsruntime.Summary
	if err := json.Unmarshal(recorder.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode catalog: %v", err)
	}
	want := []skillsruntime.Summary{
		{Name: "concise-writer", Description: "Make writing concise."},
		{Name: "follow-builders-lite", Description: "Summarize AI builder news."},
	}
	if len(got) != len(want) {
		t.Fatalf("catalog = %#v, want %#v", got, want)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("catalog[%d] = %#v, want %#v", index, got[index], want[index])
		}
	}
}

func TestProjectSkillsHandlerReturnsEmptyArray(t *testing.T) {
	recorder := httptest.NewRecorder()
	projectSkillsHandler(nil).ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/project-skills", nil))
	if got := strings.TrimSpace(recorder.Body.String()); got != "[]" {
		t.Fatalf("empty catalog body = %q, want []", got)
	}
}

func TestHandlerMountsADKAPIUnderAPIPrefix(t *testing.T) {
	recorder := httptest.NewRecorder()
	testHandler(t).ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/list-apps", nil))

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %q", recorder.Code, recorder.Body.String())
	}
	if got := strings.TrimSpace(recorder.Body.String()); got != `["test_agent"]` {
		t.Fatalf("body = %q, want test agent list", got)
	}
}

func TestHandlerServesEmbeddedAssets(t *testing.T) {
	recorder := httptest.NewRecorder()
	testHandler(t).ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/assets/app.js", nil))

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d", recorder.Code)
	}
	if body := recorder.Body.String(); !strings.Contains(body, "bootstrap();") {
		t.Fatalf("app.js body missing bootstrap call")
	}
	for _, marker := range []string{
		`<details class="execution-group`,
		`<details class="tool-card`,
		"executionItems",
		"confirmationResponse",
		"restorePersistedFunctionResponses",
		"findMessageWithExecutionItem",
		`confirmed === true ? "approved" : confirmed === false ? "rejected"`,
		`if (response.name === "adk_request_confirmation") return;`,
		"本次授权已生效",
		"confirmationMode === \"auto\"",
		"executionSummary",
		"updateThoughtItem",
		"execution-timeline",
		"executionDisclosurePreferences",
		"persistentSessionTitle",
		"sessionTitleSource",
		"hasFinalSessionTitle",
		"title_source",
		"ensureSessionTitle",
		"session.localTitle",
		"/title",
		`preference === "open" || (needsAttention && preference !== "closed")`,
		"思考中",
		"执行过程",
		"requestDetail: stringify(original.args ?? call.args)",
		"activity.responseDetail = stringify(response.response)",
		"请求参数",
		"执行结果",
		"renderMarkdownTable",
		"renderMarkdownMathBlock",
		"renderMarkdownBlockquote",
		"isMarkdownThematicBreak",
		"renderInlineFormatting",
		"message-inline-image",
		"referrerpolicy=\"no-referrer\"",
		"copyAssistantReply",
		"copyCodeBlock",
		"renderCodeCopyButton",
		"writeClipboardText",
		"navigator.clipboard.writeText",
		"data-copy-reply",
		"data-copy-code",
		"parseMarkdownSegments",
		"compactRepeatedMermaidSegments",
		"isRepeatedCodeSeparator",
		"repeatedMermaidCodeMatches",
		"repeated.unclosed && source.startsWith(candidate)",
		"codeLanguageInfo",
		"scheduleCodeHighlight",
		"renderMermaidBlock",
		"renderMarkdown(message.text, message.id, { streaming: message.streaming })",
		"reconcileMessageElements",
		"messageRenderSignature",
		"createMessageElement",
		"deferMermaid: streaming",
		`sourceLanguage === "mermaid" && deferMermaid`,
		`receiving ? "正在生成图示"`,
		`data-render-state="${deferred ? "waiting" : "pending"}`,
		"scheduleMermaidRender",
		"renderPendingMermaidDiagrams",
		"prepareMermaidFigure",
		"applyMermaidBatch",
		"applyMermaidResult",
		"/assets/vendor/mermaid/mermaid.min.js",
		`securityLevel: "strict"`,
		"suppressErrorRendering: true",
		"mermaidRenderCache",
		"cleanupMermaidArtifacts",
		"showMermaidError",
		"data-mermaid-source",
		"isConversationNearBottom",
		"scheduleConversationFollow",
		"state.conversationScrollFrame !== 0",
		"renderMessages({ forceFollow: true })",
		`"c++": "cpp"`,
		`window.hljs.highlightElement(code)`,
		"scheduleMathRender",
		`{ left: "\\[", right: "\\]", display: true }`,
		"message-table-scroll",
		`api("/runtime-info")`,
		"formatTokenCount",
		"thinkingModeLabel",
		"reasoningEffortLabel",
		"startDraft",
		"createUnpublishedSession",
		"publishCurrentSession",
		"discardUnpublishedSession",
		`els.brandHome.addEventListener("click"`,
		`api("/project-skills")`,
		"findSkillTrigger",
		"createSkillMention",
		"deleteAdjacentSkill",
		"submitText(editorText())",
	} {
		if !strings.Contains(recorder.Body.String(), marker) {
			t.Fatalf("app.js missing tool disclosure marker %q", marker)
		}
	}
	if strings.Contains(recorder.Body.String(), `els.messageList.innerHTML = state.messages.map(renderMessage).join("")`) {
		t.Fatal("app.js must preserve unchanged message DOM during streaming updates")
	}
	if strings.Contains(recorder.Body.String(), "filter(sessionHasUserMessage)") {
		t.Fatal("app.js must not filter session summaries by events because list responses may omit events")
	}
	if strings.Contains(recorder.Body.String(), "openInspector(true)") {
		t.Fatal("app.js must not open the execution inspector without an explicit user action")
	}
	if strings.Contains(recorder.Body.String(), `showToast("回复已复制")`) {
		t.Fatal("app.js must keep successful copy feedback local to the reply action")
	}
}

func TestSkillComposerStylesAreEmbedded(t *testing.T) {
	styles, err := staticFiles.ReadFile("static/styles.css")
	if err != nil {
		t.Fatalf("read embedded styles: %v", err)
	}
	for _, rule := range []string{
		".composer-input {",
		".skill-mention {",
		".skill-picker {",
		`.skill-option[aria-selected="true"]`,
	} {
		if !strings.Contains(string(styles), rule) {
			t.Fatalf("styles.css missing Skill composer rule %q", rule)
		}
	}
}

func TestHandlerServesNestedHighlightAssets(t *testing.T) {
	tests := []struct {
		path   string
		marker string
	}{
		{path: "/assets/vendor/highlightjs/highlight.min.js", marker: "highlightElement"},
		{path: "/assets/vendor/highlightjs/styles/github-dark-dimmed.min.css", marker: ".hljs-keyword"},
	}
	for _, test := range tests {
		recorder := httptest.NewRecorder()
		testHandler(t).ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, test.path, nil))
		if recorder.Code != http.StatusOK {
			t.Errorf("GET %s status = %d", test.path, recorder.Code)
			continue
		}
		if !strings.Contains(recorder.Body.String(), test.marker) {
			t.Errorf("GET %s body missing %q", test.path, test.marker)
		}
	}
}

func TestHandlerServesNestedKaTeXAssets(t *testing.T) {
	tests := []struct {
		path   string
		marker string
	}{
		{path: "/assets/vendor/katex/katex.min.css", marker: ".katex"},
		{path: "/assets/vendor/katex/katex.min.js", marker: "ParseError"},
		{path: "/assets/vendor/katex/contrib/auto-render.min.js", marker: "renderMathInElement"},
	}
	for _, test := range tests {
		recorder := httptest.NewRecorder()
		testHandler(t).ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, test.path, nil))
		if recorder.Code != http.StatusOK {
			t.Errorf("GET %s status = %d", test.path, recorder.Code)
			continue
		}
		if !strings.Contains(recorder.Body.String(), test.marker) {
			t.Errorf("GET %s body missing %q", test.path, test.marker)
		}
	}
}

func TestHandlerServesNestedMermaidAssets(t *testing.T) {
	tests := []struct {
		path   string
		marker string
	}{
		{path: "/assets/vendor/mermaid/mermaid.min.js", marker: "mermaid"},
		{path: "/assets/vendor/mermaid/LICENSE", marker: "The MIT License"},
	}
	for _, test := range tests {
		recorder := httptest.NewRecorder()
		testHandler(t).ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, test.path, nil))
		if recorder.Code != http.StatusOK {
			t.Errorf("GET %s status = %d", test.path, recorder.Code)
			continue
		}
		if !strings.Contains(recorder.Body.String(), test.marker) {
			t.Errorf("GET %s body missing %q", test.path, test.marker)
		}
	}
}

func TestExecutionDisclosureStylesAreEmbedded(t *testing.T) {
	styles, err := staticFiles.ReadFile("static/styles.css")
	if err != nil {
		t.Fatalf("read embedded styles: %v", err)
	}
	css := string(styles)
	for _, rule := range []string{
		".execution-group {",
		".execution-summary {",
		"min-height: 38px;",
		".execution-timeline {",
		".thought-preview {",
		".execution-mode-control {",
		".execution-mode-control.auto {",
		".execution-mode-menu {",
		"min-height: 34px;",
	} {
		if !strings.Contains(css, rule) {
			t.Fatalf("styles.css missing execution disclosure rule %q", rule)
		}
	}
}

func TestMarkdownTableStylesAreEmbedded(t *testing.T) {
	styles, err := staticFiles.ReadFile("static/styles.css")
	if err != nil {
		t.Fatalf("read embedded styles: %v", err)
	}
	css := string(styles)
	for _, rule := range []string{
		".message-table-scroll {",
		".message-table-scroll:focus-visible",
		".message-content table {",
		".message-content .align-right",
	} {
		if !strings.Contains(css, rule) {
			t.Fatalf("styles.css missing Markdown table rule %q", rule)
		}
	}
}

func TestMarkdownBlockStylesAreEmbedded(t *testing.T) {
	styles, err := staticFiles.ReadFile("static/styles.css")
	if err != nil {
		t.Fatalf("read embedded styles: %v", err)
	}
	css := string(styles)
	for _, rule := range []string{
		".message-actions {",
		".message-action-button {",
		".message-action-button.copied {",
		".message-action-button:focus-visible::after",
		".message-content h1,",
		".message-content h6 {",
		".message-content blockquote {",
		".message-content blockquote blockquote {",
		".message-content hr {",
		".message-content del {",
		".message-content .message-inline-image {",
	} {
		if !strings.Contains(css, rule) {
			t.Fatalf("styles.css missing Markdown block rule %q", rule)
		}
	}
}

func TestMathStylesAreEmbedded(t *testing.T) {
	styles, err := staticFiles.ReadFile("static/styles.css")
	if err != nil {
		t.Fatalf("read embedded styles: %v", err)
	}
	for _, rule := range []string{".message-math-block", ".message-content .katex-display", ".message-content .katex-error"} {
		if !strings.Contains(string(styles), rule) {
			t.Fatalf("styles.css missing math rule %q", rule)
		}
	}
}

func TestCodeHighlightStylesAreEmbedded(t *testing.T) {
	styles, err := staticFiles.ReadFile("static/styles.css")
	if err != nil {
		t.Fatalf("read embedded styles: %v", err)
	}
	for _, rule := range []string{".message-code-block {", ".message-code-heading {", ".message-code-copy {", ".message-code-copy.copied", ".message-code-block pre code.hljs", ".message-code-block.streaming"} {
		if !strings.Contains(string(styles), rule) {
			t.Fatalf("styles.css missing code highlight rule %q", rule)
		}
	}
}

func TestMermaidStylesAreEmbedded(t *testing.T) {
	styles, err := staticFiles.ReadFile("static/styles.css")
	if err != nil {
		t.Fatalf("read embedded styles: %v", err)
	}
	for _, rule := range []string{
		".message-mermaid {",
		".message-mermaid-canvas {",
		".message-mermaid-source > summary",
		`.message-mermaid[data-render-state="waiting"]`,
		`.message-mermaid[data-render-state="error"]`,
		".message-mermaid-error",
	} {
		if !strings.Contains(string(styles), rule) {
			t.Fatalf("styles.css missing Mermaid rule %q", rule)
		}
	}
}

func TestLayoutPinsConversationAndComposerToDedicatedGridRows(t *testing.T) {
	styles, err := staticFiles.ReadFile("static/styles.css")
	if err != nil {
		t.Fatalf("read embedded styles: %v", err)
	}
	css := string(styles)
	for _, rule := range []string{
		".conversation {\n  grid-row: 3;",
		".composer-wrap {\n  position: relative;\n  grid-row: 4;",
		".workspace {\n  display: grid;",
		"overflow: hidden;",
		"overflow-anchor: none;",
		"scroll-behavior: auto;",
		"--scrollbar-thumb:",
		"scrollbar-color: var(--scrollbar-thumb) transparent;",
		"scrollbar-gutter: stable;",
		".session-list::-webkit-scrollbar",
		".session-list::-webkit-scrollbar-thumb:hover",
	} {
		if !strings.Contains(css, rule) {
			t.Fatalf("styles.css missing layout regression rule %q", rule)
		}
	}
}

func TestLauncherParsesWebFlags(t *testing.T) {
	launcher := NewLauncher(nil, nil, RuntimeInfo{})
	rest, err := launcher.Parse([]string{"--port", "9000", "extra"})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(rest) != 1 || rest[0] != "extra" {
		t.Fatalf("remaining args = %v", rest)
	}
}
