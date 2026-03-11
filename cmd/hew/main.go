package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"

	"github.com/cosgroveb/hew"
	"github.com/cosgroveb/hew/anthropic"
	"github.com/cosgroveb/hew/openai"
	"github.com/cosgroveb/hew/session"
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
  --model string         Model identifier (env: $HEW_MODEL, default: claude-sonnet-4-20250514)
  --base-url string      LLM endpoint (env: $HEW_BASE_URL, default: https://api.anthropic.com)
  --max-steps int        Maximum agent steps, 0 = default 100 (default: 0)
  -v, --verbose          Show internal decisions (queries, parsing, cwd)
  --load-messages string  Seed conversation from JSON file (e.g. from --trajectory)
  --event-log string      Write JSONL events to file (streams in real time)
  --trajectory string     Write message history as JSON on exit (single-task mode only)
  --continue              Resume most recent session for this project
  --list-sessions         List all saved sessions for this project
  --version               Print version and exit

Environment:
  HEW_API_KEY            API key for the LLM provider (required)
                         Falls back to ANTHROPIC_API_KEY when using Anthropic endpoint.
  HEW_MODEL              Model identifier (default: claude-sonnet-4-20250514)
                         Overridden by --model flag.
  HEW_BASE_URL           LLM endpoint (default: https://api.anthropic.com)
                         Overridden by --base-url flag.
`)
	}

	prompt := flags.String("p", "", "")
	promptLong := flags.String("prompt", "", "")
	modelFlag := flags.String("model", "", "")
	baseURL := flags.String("base-url", "", "")
	maxSteps := flags.Int("max-steps", 0, "")
	verbose := flags.Bool("verbose", false, "")
	verboseShort := flags.Bool("v", false, "")
	showVersion := flags.Bool("version", false, "")
	eventLog := flags.String("event-log", "", "")
	trajectory := flags.String("trajectory", "", "")
	loadMessages := flags.String("load-messages", "", "")
	continueFlag := flags.Bool("continue", false, "")
	listSessions := flags.Bool("list-sessions", false, "")

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

	if *listSessions {
		cwd, err := os.Getwd()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: cannot get working directory: %v\n", err)
			os.Exit(1)
		}
		sessions, err := session.ListSessions(cwd)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: cannot list sessions: %v\n", err)
			os.Exit(1)
		}
		if len(sessions) == 0 {
			fmt.Println("No sessions found for this project.")
			os.Exit(0)
		}
		fmt.Println("Saved sessions:")
		for i, s := range sessions {
			fmt.Printf("  %d. %s (%d messages) - %s\n", i+1, s.Filename, s.Messages, s.Created.Format("2006-01-02 15:04:05"))
		}
		os.Exit(0)
	}

	// --prompt and -p are aliases; prefer whichever is set
	taskPrompt := *prompt
	if *promptLong != "" {
		taskPrompt = *promptLong
	}

	// Resolve model: flag > env > default
	if *modelFlag == "" {
		if env := os.Getenv("HEW_MODEL"); env != "" {
			*modelFlag = env
		} else {
			*modelFlag = "claude-sonnet-4-20250514"
		}
	}

	// Resolve base URL: flag > env > default
	baseURLSource := "default"
	if *baseURL == "" {
		if env := os.Getenv("HEW_BASE_URL"); env != "" {
			*baseURL = env
			baseURLSource = "HEW_BASE_URL env var"
		} else {
			*baseURL = "https://api.anthropic.com"
		}
	} else {
		baseURLSource = "--base-url flag"
	}
	if !strings.HasPrefix(*baseURL, "http://") && !strings.HasPrefix(*baseURL, "https://") {
		fmt.Fprintf(os.Stderr, "Error: invalid base URL %q (from %s): must start with http:// or https://\n", *baseURL, baseURLSource)
		os.Exit(1)
	}

	// Resolve API key: HEW_API_KEY > ANTHROPIC_API_KEY (Anthropic only)
	apiKey := os.Getenv("HEW_API_KEY")
	if apiKey == "" && strings.Contains(*baseURL, "anthropic.com") {
		apiKey = os.Getenv("ANTHROPIC_API_KEY")
	}
	if apiKey == "" {
		msg := "Error: No API key found.\nSet it with: export HEW_API_KEY=your-api-key"
		if strings.Contains(*baseURL, "anthropic.com") {
			msg += "\nOr set ANTHROPIC_API_KEY when using the default Anthropic endpoint."
		}
		fmt.Fprintln(os.Stderr, msg)
		os.Exit(1)
	}

	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: cannot get working directory: %v\n", err)
		os.Exit(1)
	}

	var eventLogFile *os.File
	if *eventLog != "" {
		var err error
		eventLogFile, err = os.OpenFile(*eventLog, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: cannot open event log %q: %v\n", *eventLog, err)
			os.Exit(1)
		}
		defer eventLogFile.Close() //nolint:errcheck
	}

	systemPrompt := hew.LoadPrompt(cwd)

	var model hew.Model
	if strings.Contains(*baseURL, "anthropic.com") {
		model = anthropic.NewModel(*baseURL, apiKey, *modelFlag, systemPrompt)
	} else {
		model = openai.NewModel(*baseURL, apiKey, *modelFlag, systemPrompt)
	}

	executor := &hew.CommandExecutor{}

	agent := hew.NewAgent(model, executor, cwd)

	showDebug := *verbose || *verboseShort
	agent.Notify = func(e hew.Event) {
		switch e := e.(type) {
		case hew.EventResponse:
			fmt.Fprintln(os.Stdout, e.Message.Content) //nolint:errcheck
		case hew.EventCommandStart:
			fmt.Fprintf(os.Stdout, "--- running: %s ---\n", summarizeCommand(e.Command)) //nolint:errcheck
		case hew.EventCommandDone:
			fmt.Fprintln(os.Stdout, e.Output)       //nolint:errcheck
			fmt.Fprintln(os.Stdout, "--- done ---") //nolint:errcheck
		case hew.EventFormatError:
			// handled by agent loop; nothing to print
		case hew.EventDebug:
			if showDebug {
				fmt.Fprintf(os.Stderr, "[hew] %s\n", e.Message)
			}
		}
		if eventLogFile != nil {
			writeEventLog(eventLogFile, e)
		}
	}

	if *maxSteps > 0 {
		agent.MaxSteps = *maxSteps
	}

	if *loadMessages != "" {
		data, err := os.ReadFile(*loadMessages)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: cannot read messages file %q: %v\n", *loadMessages, err)
			os.Exit(1)
		}
		var msgs []hew.Message
		if err := json.Unmarshal(data, &msgs); err != nil {
			fmt.Fprintf(os.Stderr, "Error: cannot parse messages file %q: %v\n", *loadMessages, err)
			os.Exit(1)
		}
		if err := agent.AddMessages(msgs); err != nil {
			fmt.Fprintf(os.Stderr, "Error: cannot load messages: %v\n", err)
			os.Exit(1)
		}
	}

	if *continueFlag {
		if *trajectory != "" {
			fmt.Fprintln(os.Stderr, "Error: --continue and --trajectory are mutually exclusive")
			os.Exit(1)
		}
		msgs, err := session.LoadLatestSession(cwd)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: cannot load session: %v\n", err)
			os.Exit(1)
		}
		if msgs == nil {
			fmt.Fprintf(os.Stderr, "Error: no session found for this project.\nRun 'hew --list-sessions' to see available sessions.\n")
			os.Exit(1)
		}
		if err := agent.AddMessages(msgs); err != nil {
			fmt.Fprintf(os.Stderr, "Error: cannot resume session: %v\n", err)
			os.Exit(1)
		}
		fmt.Fprintf(os.Stderr, "[Resumed session with %d messages]\n", len(msgs))
	}

	if *trajectory != "" && taskPrompt == "" {
		fmt.Fprintln(os.Stderr, "Error: --trajectory requires -p (single-task mode)")
		os.Exit(1)
	}

	if taskPrompt != "" {
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
		defer stop()
		runErr := agent.Run(ctx, taskPrompt)
		if *trajectory != "" {
			msgs := agent.Messages()
			data, err := json.MarshalIndent(msgs, "", "  ")
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error: cannot marshal trajectory: %v\n", err)
				if runErr != nil {
					fmt.Fprintf(os.Stderr, "Error: %v\n", runErr)
				}
				os.Exit(1)
			}
			if err := os.WriteFile(*trajectory, data, 0o644); err != nil {
				fmt.Fprintf(os.Stderr, "Error: cannot write trajectory %q: %v\n", *trajectory, err)
				if runErr != nil {
					fmt.Fprintf(os.Stderr, "Error: %v\n", runErr)
				}
				os.Exit(1)
			}
		}
		if runErr != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", runErr)
			os.Exit(1)
		}
		return
	}

	// REPL mode — fresh context per run so Ctrl-C cancels the current
	// operation without killing the REPL.
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
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
		if err := agent.Run(ctx, input); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		}
		stop()
	}
	// Auto-save session on exit (conversational mode)
	if msgs := agent.Messages(); len(msgs) > 0 {
		if err := session.SaveSession(cwd, msgs); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: could not save session: %v\n", err)
		} else {
			dir, _ := session.SessionDir(cwd)
			fmt.Fprintf(os.Stderr, "[Session saved to %s]\n", dir)
		}
	}

	if err := scanner.Err(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

type jsonEvent struct {
	Type    string `json:"type"`
	Payload any    `json:"payload"`
}

func writeEventLog(f *os.File, e hew.Event) {
	var je jsonEvent
	switch e := e.(type) {
	case hew.EventResponse:
		je = jsonEvent{Type: "response", Payload: e}
	case hew.EventCommandStart:
		je = jsonEvent{Type: "command_start", Payload: e}
	case hew.EventCommandDone:
		je = jsonEvent{Type: "command_done", Payload: struct {
			Output string `json:"output"`
			Err    string `json:"err,omitempty"`
		}{Output: e.Output, Err: errString(e.Err)}}
	case hew.EventFormatError:
		je = jsonEvent{Type: "format_error", Payload: e}
	case hew.EventDebug:
		je = jsonEvent{Type: "debug", Payload: e}
	}
	data, err := json.Marshal(je)
	if err != nil {
		return
	}
	data = append(data, '\n')
	f.Write(data) //nolint:errcheck
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func summarizeCommand(cmd string) string {
	lines := strings.Split(cmd, "\n")
	first := lines[0]
	if len(lines) == 1 {
		return first
	}
	return fmt.Sprintf("%s ... (%d lines)", first, len(lines))
}
