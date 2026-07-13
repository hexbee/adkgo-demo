package commandtool

import (
	"fmt"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/tool"
	"google.golang.org/adk/v2/tool/functiontool"
)

func NewTool(runner *Runner) (tool.Tool, error) {
	if runner == nil {
		return nil, fmt.Errorf("runner must not be nil")
	}
	return functiontool.New(functiontool.Config{
		Name: "run_command",
		Description: "Runs an unrestricted non-interactive command through the local shell. " +
			"Supports shell syntax and installed CLIs such as Bash, Python, Node.js, npm/npx, and Go. " +
			"Returns stdout, stderr, exit code, duration, cancellation, and truncation metadata.",
		RequireConfirmation: false,
	}, func(ctx agent.Context, args Args) (Result, error) {
		return runner.Run(ctx, args)
	})
}
