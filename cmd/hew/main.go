package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"

	hew "github.com/cosgroveb/hew/internal/hew"
)

// version is set at build time via -ldflags.
var version = "dev"

func main() {
	flags := flag.NewFlagSet("hew", flag.ContinueOnError)
	flags.Usage = func() {
		fmt.Fprint(os.Stderr, `hew - a minimal coding agent

Usage:
  hew                    Start conversational mode
  hew -p "task"          Run a single task and exit

Options:
  -p, --prompt string    Task to run (exits after completion)
  --model string         Model identifier (default: claude-sonnet-4-20250514)
  --base-url string      LLM API endpoint (default: https://api.anthropic.com)
  --max-steps int        Maximum agent steps, 0 = default 100 (default: 0)
  --version              Print version and exit

Environment:
  HEW_API_KEY            API key for the LLM provider (required)
                         Falls back to ANTHROPIC_API_KEY when using Anthropic endpoint.
`)
	}

	prompt := flags.String("p", "", "")
	promptLong := flags.String("prompt", "", "")
	modelFlag := flags.String("model", "claude-sonnet-4-20250514", "")
	baseURL := flags.String("base-url", "https://api.anthropic.com", "")
	maxSteps := flags.Int("max-steps", 0, "")
	showVersion := flags.Bool("version", false, "")

	if err := flags.Parse(os.Args[1:]); err != nil {
		if err == flag.ErrHelp {
			os.Exit(0)
		}
		fmt.Fprintf(os.Stderr, "Error: %v\nRun 'hew --help' for available options.\n", err)
		os.Exit(1)
	}

	if *showVersion {
		fmt.Printf("hew %s\n", version)
		os.Exit(0)
	}

	// --prompt and -p are aliases; prefer whichever is set
	taskPrompt := *prompt
	if *promptLong != "" {
		taskPrompt = *promptLong
	}

	apiKey := os.Getenv("HEW_API_KEY")
	if apiKey == "" && strings.Contains(*baseURL, "anthropic.com") {
		apiKey = os.Getenv("ANTHROPIC_API_KEY")
	}
	if apiKey == "" {
		fmt.Fprintln(os.Stderr, `Error: No API key found.
Set it with: export HEW_API_KEY=your-api-key`)
		os.Exit(1)
	}

	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: cannot get working directory: %v\n", err)
		os.Exit(1)
	}

	systemPrompt := hew.LoadPrompt(cwd)

	var model hew.Model
	if strings.Contains(*baseURL, "anthropic.com") {
		model = hew.NewAnthropicModel(*baseURL, apiKey, *modelFlag, systemPrompt)
	} else {
		model = hew.NewOpenAIModel(*baseURL, apiKey, *modelFlag, systemPrompt)
	}

	executor := &hew.CommandExecutor{}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	agent := hew.NewAgent(model, executor, cwd, os.Stdout)
	if *maxSteps > 0 {
		agent.MaxSteps = *maxSteps
	}

	if taskPrompt != "" {
		if err := agent.Run(ctx, taskPrompt); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		return
	}

	// REPL mode
	scanner := bufio.NewScanner(os.Stdin)
	for {
		fmt.Print("hew> ")
		if !scanner.Scan() {
			break
		}
		input := strings.TrimSpace(scanner.Text())
		if input == "" {
			continue
		}
		if err := agent.Run(ctx, input); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		}
	}
}
