package commandtool

import (
	"context"
	"testing"
	"time"

	"google.golang.org/adk/v2/agent"
)

type runnableTool interface {
	Run(agent.Context, any) (map[string]any, error)
}

type testToolContext struct {
	agent.ContextMock
	context.Context
}

func (c *testToolContext) Deadline() (time.Time, bool) { return c.Context.Deadline() }
func (c *testToolContext) Done() <-chan struct{}       { return c.Context.Done() }
func (c *testToolContext) Err() error                  { return c.Context.Err() }
func (c *testToolContext) Value(key any) any           { return c.Context.Value(key) }

func TestNewToolMetadataAndInvocation(t *testing.T) {
	commandTool, err := NewTool(newTestRunner(t))
	if err != nil {
		t.Fatalf("NewTool: %v", err)
	}
	if commandTool.Name() != "run_command" || commandTool.IsLongRunning() {
		t.Fatalf("metadata = %q, %v", commandTool.Name(), commandTool.IsLongRunning())
	}
	if commandTool.Description() == "" {
		t.Fatal("description is empty")
	}
	runnable, ok := commandTool.(runnableTool)
	if !ok {
		t.Fatalf("tool %T is not runnable", commandTool)
	}
	result, err := runnable.Run(&testToolContext{Context: t.Context()}, map[string]any{
		"command": "printf tool-ok",
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result["stdout"] != "tool-ok" || result["exit_code"] != float64(0) {
		t.Fatalf("result = %#v", result)
	}
}

func TestNewToolRejectsNilRunner(t *testing.T) {
	if _, err := NewTool(nil); err == nil {
		t.Fatal("NewTool(nil) succeeded")
	}
}
