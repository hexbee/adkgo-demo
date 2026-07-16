package main

import (
	"context"
	"iter"
	"strings"
	"testing"

	"github.com/hexbee/adkgo-demo/internal/config"
	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/model"
	"google.golang.org/adk/v2/tool"
)

func TestAdapterModelConfigsDisableThinkingOnlyForTitles(t *testing.T) {
	conversation, title := adapterModelConfigs(config.Config{
		BaseURL: "https://example.test/v1", APIKey: "secret", ModelName: "model",
		ContextWindow: 1000, MaxTokens: 500, ThinkingMode: "auto", ReasoningEffort: "high",
	})
	if conversation.ThinkingMode != "auto" || conversation.ReasoningEffort != "high" {
		t.Fatalf("conversation thinking config = (%q, %q)", conversation.ThinkingMode, conversation.ReasoningEffort)
	}
	if title.ThinkingMode != "disabled" || title.ReasoningEffort != "" {
		t.Fatalf("title thinking config = (%q, %q)", title.ThinkingMode, title.ReasoningEffort)
	}
	if title.BaseURL != conversation.BaseURL || title.APIKey != conversation.APIKey || title.Model != conversation.Model {
		t.Fatalf("title model endpoint config differs from conversation model")
	}
}

func TestAgentInstructionIncludesMathFormattingContract(t *testing.T) {
	for _, want := range []string{"KaTeX-compatible TeX", `\(...\)`, `\[...\]`, "do not use single-dollar math delimiters"} {
		if !strings.Contains(agentInstruction, want) {
			t.Errorf("agent instruction missing %q", want)
		}
	}
}

func TestAgentInstructionIncludesCodeFenceContract(t *testing.T) {
	for _, want := range []string{"multiline code", "fenced code blocks", "canonical language identifier", "bash", "cpp", "mermaid", "plaintext"} {
		if !strings.Contains(agentInstruction, want) {
			t.Errorf("agent instruction missing %q", want)
		}
	}
}

func TestAgentInstructionTreatsSkillReferencesAsExplicit(t *testing.T) {
	for _, want := range []string{"$<skill-name>", "explicit request", "call load_skill for every referenced available Skill"} {
		if !strings.Contains(agentInstruction, want) {
			t.Errorf("agent instruction missing explicit Skill contract %q", want)
		}
	}
}

func TestAgentInstructionAvoidsDuplicateMermaidSource(t *testing.T) {
	for _, want := range []string{"exactly one fenced mermaid block", "Do not repeat the same Mermaid source", "canonical source for both rendering and copying"} {
		if !strings.Contains(agentInstruction, want) {
			t.Errorf("agent instruction missing Mermaid output contract %q", want)
		}
	}
}

type fakeModel struct{}

func (fakeModel) Name() string { return "fake-model" }

func (fakeModel) GenerateContent(context.Context, *model.LLMRequest, bool) iter.Seq2[*model.LLMResponse, error] {
	return func(func(*model.LLMResponse, error) bool) {}
}

type stubToolset struct{ name string }

func (s stubToolset) Name() string { return s.name }

func (stubToolset) Tools(agent.ReadonlyContext) ([]tool.Tool, error) { return nil, nil }

type stubTool struct{}

func (stubTool) Name() string        { return "run_command" }
func (stubTool) Description() string { return "test command tool" }
func (stubTool) IsLongRunning() bool { return false }

func TestBuildAgentAcceptsLocalToolAndCombinedToolsets(t *testing.T) {
	for _, toolsets := range [][]tool.Toolset{
		nil,
		{stubToolset{name: "mcp"}},
		{stubToolset{name: "mcp"}, stubToolset{name: "skills"}},
	} {
		if _, err := buildAgent(fakeModel{}, stubTool{}, toolsets); err != nil {
			t.Fatalf("buildAgent(%d toolsets): %v", len(toolsets), err)
		}
	}
}
