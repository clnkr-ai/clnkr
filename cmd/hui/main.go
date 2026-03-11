package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/term"
	hew "github.com/cosgroveb/hew"
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
	eventLogPath := flags.String("event-log", "", "")
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

	taskPrompt := *prompt
	if *promptLong != "" {
		taskPrompt = *promptLong
	}

	if *modelFlag == "" {
		if env := os.Getenv("HEW_MODEL"); env != "" {
			*modelFlag = env
		} else {
			*modelFlag = "claude-sonnet-4-20250514"
		}
	}

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

	if *trajectory != "" && taskPrompt == "" {
		fmt.Fprintln(os.Stderr, "Error: --trajectory requires -p (single-task mode)")
		os.Exit(1)
	}

	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: cannot get working directory: %v\n", err)
		os.Exit(1)
	}

	var eventLogFile *os.File
	if *eventLogPath != "" {
		eventLogFile, err = os.OpenFile(*eventLogPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: cannot open event log %q: %v\n", *eventLogPath, err)
			os.Exit(1)
		}
		defer eventLogFile.Close() //nolint:errcheck
	}

	systemPrompt := hew.LoadPrompt(cwd)

	var llm hew.Model
	if strings.Contains(*baseURL, "anthropic.com") {
		llm = anthropic.NewModel(*baseURL, apiKey, *modelFlag, systemPrompt)
	} else {
		llm = openai.NewModel(*baseURL, apiKey, *modelFlag, systemPrompt)
	}

	agent := hew.NewAgent(llm, &hew.CommandExecutor{}, cwd)
	if *maxSteps > 0 {
		agent.MaxSteps = *maxSteps
	}

	showDebug := *verbose || *verboseShort

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
			fmt.Fprintf(os.Stderr, "Error: no session found for this project.\nRun 'hui --list-sessions' to see available sessions.\n")
			os.Exit(1)
		}
		if err := agent.AddMessages(msgs); err != nil {
			fmt.Fprintf(os.Stderr, "Error: cannot resume session: %v\n", err)
			os.Exit(1)
		}
		fmt.Fprintf(os.Stderr, "[Resumed session with %d messages]\n", len(msgs))
	}

	// TTY detection: if stdout is not a terminal, use plain-text rendering
	if !term.IsTerminal(os.Stdout.Fd()) {
		runPlain(agent, taskPrompt, *trajectory, eventLogFile, showDebug)
		return
	}

	runTUI(agent, taskPrompt, *trajectory, *modelFlag, cwd, eventLogFile, showDebug)
}

func runTUI(agent *hew.Agent, taskPrompt, trajectory, modelName, cwd string, eventLog *os.File, verbose bool) {
	s := defaultStyles(true) // TODO: detect actual background

	if taskPrompt != "" {
		// Single-task mode: set up event channel and launch agent immediately
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		eventCh := make(chan hew.Event, eventChSize)
		agent.Notify = makeNotify(eventCh, eventLog)

		m := newModel(modelOpts{
			eventCh:   eventCh,
			styles:    s,
			verbose:   verbose,
			cancel:    cancel,
			modelName: modelName,
			maxSteps:  agent.MaxSteps,
		})
		m.shared.agent = agent
		m.shared.eventLog = eventLog
		m.shared.cwd = cwd
		m.running = true
		m.status.startRun()

		p := tea.NewProgram(m)
		m.shared.program = p

		go func() {
			runErr := agent.Run(ctx, taskPrompt)
			close(eventCh)
			p.Send(agentDoneMsg{err: runErr})
		}()

		finalModel, err := p.Run()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}

		fm, ok := finalModel.(model)
		if !ok {
			os.Exit(1)
		}

		if trajectory != "" {
			writeTrajectory(agent, trajectory)
		}

		if fm.agentErr != nil {
			os.Exit(1)
		}
		return
	}

	// Conversational REPL mode: start with empty input, user types tasks
	m := newModel(modelOpts{
		styles:    s,
		verbose:   verbose,
		modelName: modelName,
		maxSteps:  agent.MaxSteps,
	})
	m.shared.agent = agent
	m.shared.eventLog = eventLog
	m.shared.cwd = cwd

	p := tea.NewProgram(m)
	m.shared.program = p

	finalModel, err := p.Run()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	fm, ok := finalModel.(model)
	if !ok {
		os.Exit(1)
	}

	if fm.agentErr != nil {
		os.Exit(1)
	}
}

// runPlain provides plain-text output for non-TTY environments.
// Matches hew-core behavior. ~30 lines of duplicated Notify logic.
func runPlain(agent *hew.Agent, taskPrompt, trajectory string, eventLog *os.File, verbose bool) {
	if taskPrompt == "" {
		fmt.Fprintf(os.Stderr, "Error: non-interactive mode requires -p\n")
		os.Exit(1)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	agent.Notify = func(e hew.Event) {
		if eventLog != nil {
			writeEventLog(eventLog, e)
		}
		switch ev := e.(type) {
		case hew.EventResponse:
			fmt.Fprintln(os.Stdout, ev.Message.Content) //nolint:errcheck
		case hew.EventCommandStart:
			fmt.Fprintf(os.Stdout, "--- running: %s ---\n", summarizeCommand(ev.Command)) //nolint:errcheck
		case hew.EventCommandDone:
			fmt.Fprintln(os.Stdout, ev.Output)      //nolint:errcheck
			fmt.Fprintln(os.Stdout, "--- done ---") //nolint:errcheck
		case hew.EventFormatError:
			// handled by agent loop
		case hew.EventDebug:
			if verbose {
				fmt.Fprintf(os.Stderr, "[hew] %s\n", ev.Message)
			}
		default:
		}
	}

	runErr := agent.Run(ctx, taskPrompt)

	if trajectory != "" {
		writeTrajectory(agent, trajectory)
	}

	if runErr != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", runErr)
		os.Exit(1)
	}
}

func writeTrajectory(agent *hew.Agent, path string) {
	msgs := agent.Messages()
	data, err := json.MarshalIndent(msgs, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: cannot marshal trajectory: %v\n", err)
		return
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "Error: cannot write trajectory %q: %v\n", path, err)
	}
}
