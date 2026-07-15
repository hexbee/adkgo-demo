package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/hexbee/adkgo-demo/internal/commandtool"
	"github.com/hexbee/adkgo-demo/internal/config"
	"github.com/hexbee/adkgo-demo/internal/mcpconfig"
	"github.com/hexbee/adkgo-demo/internal/mcpruntime"
	"github.com/hexbee/adkgo-demo/internal/skillsruntime"
	"github.com/hexbee/adkgo-demo/internal/webapp"
	"github.com/hexbee/adkgo-demo/openaiadapter"
	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/agent/llmagent"
	"google.golang.org/adk/v2/cmd/launcher"
	"google.golang.org/adk/v2/cmd/launcher/console"
	"google.golang.org/adk/v2/cmd/launcher/universal"
	"google.golang.org/adk/v2/model"
	"google.golang.org/adk/v2/tool"
	"google.golang.org/adk/v2/tool/functiontool"
)

type timeArgs struct {
	Timezone string `json:"timezone" jsonschema:"IANA timezone such as Asia/Shanghai"`
}

type timeResult struct {
	Timezone string `json:"timezone"`
	Time     string `json:"time"`
}

const agentInstruction = "Be concise and helpful. Use lookup_time for timezone questions. Use run_command for local shell or CLI tasks. Commands run immediately without confirmation or sandboxing. Use available project Skills when relevant and load their instructions before acting. When a loaded Skill references scripts or commands, use run_command with working_directory set to \".agents/skills/<skill-name>\" so Skill-relative paths resolve correctly. Use available MCP tools when relevant; every MCP tool call requires explicit user confirmation before execution. When returning multiline code, use fenced code blocks with a specific canonical language identifier such as bash, python, java, go, c, cpp, csharp, javascript, typescript, json, yaml, sql, rust, mermaid, or plaintext. For Mermaid examples, output exactly one fenced mermaid block for each diagram. Do not repeat the same Mermaid source in a markdown, plaintext, or second mermaid block; one mermaid block is the canonical source for both rendering and copying. In final answers, when mathematical notation improves clarity, use KaTeX-compatible TeX. Delimit inline math with \\(...\\) and display math with \\[...\\]. Do not put math delimiters inside code fences, and do not use single-dollar math delimiters."

func lookupTime(_ agent.Context, args timeArgs) (timeResult, error) {
	location, err := time.LoadLocation(args.Timezone)
	if err != nil {
		return timeResult{}, fmt.Errorf("invalid timezone %q: %w", args.Timezone, err)
	}
	return timeResult{Timezone: args.Timezone, Time: time.Now().In(location).Format(time.RFC3339)}, nil
}

func buildAgent(llm model.LLM, commandTool tool.Tool, toolsets []tool.Toolset) (agent.Agent, error) {
	timeTool, err := functiontool.New(functiontool.Config{
		Name: "lookup_time", Description: "Returns the current time in an IANA timezone.",
	}, lookupTime)
	if err != nil {
		return nil, fmt.Errorf("create time tool: %w", err)
	}
	return llmagent.New(llmagent.Config{
		Name:        "openai_compatible_assistant",
		Description: "An assistant backed by an OpenAI-compatible endpoint.",
		Instruction: agentInstruction,
		Model:       llm,
		Tools:       []tool.Tool{timeTool, commandTool},
		Toolsets:    toolsets,
	})
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cfg, err := config.Load(".env")
	if err != nil {
		log.Fatalf("configuration error: %v", err)
	}
	log.Printf("starting ADK demo: %s", cfg.SafeSummary())

	adaptedModel, err := openaiadapter.New(openaiadapter.Config{
		BaseURL: cfg.BaseURL, APIKey: cfg.APIKey, Model: cfg.ModelName,
		ContextWindow: cfg.ContextWindow, MaxTokens: cfg.MaxTokens,
		ThinkingMode: cfg.ThinkingMode, ReasoningEffort: cfg.ReasoningEffort,
	})
	if err != nil {
		log.Fatalf("create model: %v", err)
	}
	projectRoot, err := os.Getwd()
	if err != nil {
		log.Fatalf("resolve project root: %v", err)
	}
	commandRunner, err := commandtool.New(projectRoot)
	if err != nil {
		log.Fatalf("CLI setup error: %v", err)
	}
	commandTool, err := commandtool.NewTool(commandRunner)
	if err != nil {
		log.Fatalf("create CLI tool: %v", err)
	}
	mcpResult, err := mcpconfig.Load(".mcp.json")
	if err != nil {
		log.Fatalf("MCP configuration error: %v", err)
	}
	mcpToolsets, err := mcpruntime.Build(mcpResult.Servers)
	if err != nil {
		log.Fatalf("MCP setup error: %v", err)
	}
	if mcpResult.Found {
		log.Printf("loaded %d MCP server(s)", len(mcpResult.Servers))
	} else {
		log.Printf("no .mcp.json found; starting without MCP servers")
	}
	skillsResult, err := skillsruntime.Build(ctx, projectRoot)
	if err != nil {
		log.Fatalf("Skills setup error: %v", err)
	}
	toolsets := append([]tool.Toolset(nil), mcpToolsets...)
	if skillsResult.Toolset != nil {
		toolsets = append(toolsets, skillsResult.Toolset)
	}
	if !skillsResult.Found {
		log.Printf("no %s found; starting without project Skills", skillsruntime.RelativeRoot)
	} else {
		log.Printf("loaded %d project skill(s) from %s", skillsResult.Count, skillsruntime.RelativeRoot)
	}
	rootAgent, err := buildAgent(adaptedModel, commandTool, toolsets)
	if err != nil {
		log.Fatalf("create agent: %v", err)
	}
	launcherConfig := &launcher.Config{AgentLoader: agent.NewSingleLoader(rootAgent)}
	l := universal.NewLauncher(console.NewLauncher(), webapp.NewLauncher())
	if err := l.Execute(ctx, launcherConfig, os.Args[1:]); err != nil {
		log.Fatalf("run failed: %v\n\n%s", err, l.CommandLineSyntax())
	}
}
