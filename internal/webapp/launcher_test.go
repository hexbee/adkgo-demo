package webapp

import (
	"iter"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/cmd/launcher"
	"google.golang.org/adk/v2/session"
)

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
	}, &config{sseWriteTimeout: time.Second, traceCapacity: 10})
	if err != nil {
		t.Fatalf("newHandler: %v", err)
	}
	return handler
}

func TestHandlerServesWorkbenchWithSecurityHeaders(t *testing.T) {
	recorder := httptest.NewRecorder()
	testHandler(t).ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/", nil))

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
	}
	if body := recorder.Body.String(); !strings.Contains(body, "Agent Workbench") || strings.Contains(body, "ADK Web UI") {
		t.Fatalf("unexpected web app body: %q", body[:min(len(body), 200)])
	}
	for _, marker := range []string{`id="confirmation-mode-menu"`, "逐项确认", "本次授权", "下一条任务中的工具自动执行"} {
		if !strings.Contains(recorder.Body.String(), marker) {
			t.Fatalf("web app body missing one-shot execution mode marker %q", marker)
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
	if got := recorder.Header().Get("Content-Security-Policy"); !strings.Contains(got, "default-src 'self'") || !strings.Contains(got, "style-src 'self' 'unsafe-inline'") {
		t.Fatalf("Content-Security-Policy = %q", got)
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
		"本次授权已生效",
		"confirmationMode === \"auto\"",
		"executionSummary",
		"updateThoughtItem",
		"execution-timeline",
		"executionDisclosurePreferences",
		`preference === "open" || (needsAttention && preference !== "closed")`,
		"思考中",
		"执行过程",
		"requestDetail: stringify(original.args ?? call.args)",
		"activity.responseDetail = stringify(response.response)",
		"请求参数",
		"执行结果",
		"renderMarkdownTable",
		"renderMarkdownMathBlock",
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
	} {
		if !strings.Contains(recorder.Body.String(), marker) {
			t.Fatalf("app.js missing tool disclosure marker %q", marker)
		}
	}
	if strings.Contains(recorder.Body.String(), `els.messageList.innerHTML = state.messages.map(renderMessage).join("")`) {
		t.Fatal("app.js must preserve unchanged message DOM during streaming updates")
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
	for _, rule := range []string{".message-code-block {", ".message-code-heading {", ".message-code-block pre code.hljs", ".message-code-block.streaming"} {
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
	} {
		if !strings.Contains(css, rule) {
			t.Fatalf("styles.css missing layout regression rule %q", rule)
		}
	}
}

func TestLauncherParsesWebFlags(t *testing.T) {
	launcher := NewLauncher()
	rest, err := launcher.Parse([]string{"--port", "9000", "extra"})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(rest) != 1 || rest[0] != "extra" {
		t.Fatalf("remaining args = %v", rest)
	}
}
