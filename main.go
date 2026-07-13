package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/hexbee/adkgo-demo/internal/config"
	"github.com/hexbee/adkgo-demo/openaiadapter"
	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/agent/llmagent"
	"google.golang.org/adk/v2/cmd/launcher"
	"google.golang.org/adk/v2/cmd/launcher/full"
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

func lookupTime(_ agent.Context, args timeArgs) (timeResult, error) {
	location, err := time.LoadLocation(args.Timezone)
	if err != nil {
		return timeResult{}, fmt.Errorf("invalid timezone %q: %w", args.Timezone, err)
	}
	return timeResult{Timezone: args.Timezone, Time: time.Now().In(location).Format(time.RFC3339)}, nil
}

func main() {
	ctx := context.Background()
	cfg, err := config.Load(".env")
	if err != nil {
		log.Fatalf("configuration error: %v", err)
	}
	log.Printf("starting ADK demo: %s", cfg.SafeSummary())

	adaptedModel, err := openaiadapter.New(openaiadapter.Config{
		BaseURL: cfg.BaseURL, APIKey: cfg.APIKey, Model: cfg.ModelName,
		ContextWindow: cfg.ContextWindow, MaxTokens: cfg.MaxTokens,
	})
	if err != nil {
		log.Fatalf("create model: %v", err)
	}
	timeTool, err := functiontool.New(functiontool.Config{
		Name: "lookup_time", Description: "Returns the current time in an IANA timezone.",
	}, lookupTime)
	if err != nil {
		log.Fatalf("create tool: %v", err)
	}
	rootAgent, err := llmagent.New(llmagent.Config{
		Name:        "openai_compatible_assistant",
		Description: "An assistant backed by an OpenAI-compatible endpoint.",
		Instruction: "Be concise and helpful. Use lookup_time when the user asks for the time in a named timezone.",
		Model:       adaptedModel,
		Tools:       []tool.Tool{timeTool},
	})
	if err != nil {
		log.Fatalf("create agent: %v", err)
	}
	launcherConfig := &launcher.Config{AgentLoader: agent.NewSingleLoader(rootAgent)}
	l := full.NewLauncher()
	if err := l.Execute(ctx, launcherConfig, os.Args[1:]); err != nil {
		log.Fatalf("run failed: %v\n\n%s", err, l.CommandLineSyntax())
	}
}
