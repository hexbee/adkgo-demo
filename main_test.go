package main

import (
	"context"
	"iter"
	"testing"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/model"
	"google.golang.org/adk/v2/tool"
)

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
