package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/term"
	clnkr "github.com/clnkr-ai/clnkr"
	"github.com/clnkr-ai/clnkr/anthropic"
	"github.com/clnkr-ai/clnkr/openai"
	"github.com/clnkr-ai/clnkr/session"
)

// version is set at build time via -ldflags.
var version = "dev"

const exitClarificationNeeded = 2

func main() {
	flags := flag.NewFlagSet("hew", flag.ContinueOnError)
	flags.Usage = func() {
		fmt.Fprint(os.Stderr, `hew - a minimal coding agent

Usage:
  hew                    Start conversational mode
  hew -p "task"          Run a single task and exit

Core:
  -p, --prompt string       Task to run (exits after completion)
  -m, --model string        Model identifier (env: $HEW_MODEL, default: claude-sonnet-4-20250514)
  -u, --base-url string     LLM endpoint (env: $HEW_BASE_URL, default: https://api.anthropic.com)
      --max-steps int       Maximum agent steps (default: 100)
  -v, --verbose             Show internal decisions

Sessions:
  -c, --continue            Resume most recent session for this project
  -l, --list-sessions       List saved sessions for this project

System prompt:
  -S, --no-system-prompt              Skip the built-in system prompt entirely
      --system-prompt-append string   Append text to the built-in system prompt
      --dump-system-prompt            Print the composed system prompt and exit

Debugging:
      --load-messages string   Seed conversation from a JSON file
      --event-log string       Stream JSONL events to file during execution
      --trajectory string      Save message history as JSON on exit (single-task only)

  -V, --version             Print version and exit

Environment:
  HEW_API_KEY     API key for the LLM provider (required)
  HEW_MODEL       Model identifier override
  HEW_BASE_URL    LLM endpoint override
`)
	}

	prompt := flags.String("p", "", "")
	promptLong := flags.String("prompt", "", "")
	modelFlag := flags.String("model", "", "")
	modelShort := flags.String("m", "", "")
	baseURL := flags.String("base-url", "", "")
	baseURLShort := flags.String("u", "", "")
	maxSteps := flags.Int("max-steps", 0, "")
	verbose := flags.Bool("verbose", false, "")
	verboseShort := flags.Bool("v", false, "")
	showVersion := flags.Bool("version", false, "")
	showVersionShort := flags.Bool("V", false, "")
	eventLogPath := flags.String("event-log", "", "")
	trajectory := flags.String("trajectory", "", "")
	loadMessages := flags.String("load-messages", "", "")
	continueFlag := flags.Bool("continue", false, "")
	continueShort := flags.Bool("c", false, "")
	listSessions := flags.Bool("list-sessions", false, "")
	listSessionsShort := flags.Bool("l", false, "")
	noSystemPrompt := flags.Bool("no-system-prompt", false, "")
	noSystemPromptShort := flags.Bool("S", false, "")
	systemPromptAppend := flags.String("system-prompt-append", "", "")
	dumpSystemPrompt := flags.Bool("dump-system-prompt", false, "")

	if err := flags.Parse(os.Args[1:]); err != nil {
		if err == flag.ErrHelp {
			os.Exit(0)
		}
		fmt.Fprintf(os.Stderr, "Error: %v\nRun 'hew --help' for available options.\n", err)
		os.Exit(1)
	}

	if *showVersion || *showVersionShort {
		fmt.Printf("hew %s\n", version)
		os.Exit(0)
	}

	if *listSessions || *listSessionsShort {
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

	if *modelShort != "" {
		*modelFlag = *modelShort
	}
	if *modelFlag == "" {
		if env := os.Getenv("HEW_MODEL"); env != "" {
			*modelFlag = env
		} else {
			*modelFlag = "claude-sonnet-4-20250514"
		}
	}

	if *baseURLShort != "" {
		*baseURL = *baseURLShort
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

	systemPrompt := clnkr.LoadPromptWithOptions(cwd, clnkr.PromptOptions{
		OmitSystemPrompt:   *noSystemPrompt || *noSystemPromptShort,
		SystemPromptAppend: *systemPromptAppend,
	})

	if *dumpSystemPrompt {
		fmt.Print(systemPrompt)
		os.Exit(0)
	}

	var llm clnkr.Model
	if strings.Contains(*baseURL, "anthropic.com") {
		llm = anthropic.NewModel(*baseURL, apiKey, *modelFlag, systemPrompt)
	} else {
		llm = openai.NewModel(*baseURL, apiKey, *modelFlag, systemPrompt)
	}

	agent := clnkr.NewAgent(llm, &clnkr.CommandExecutor{}, cwd)
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
		var msgs []clnkr.Message
		if err := json.Unmarshal(data, &msgs); err != nil {
			fmt.Fprintf(os.Stderr, "Error: cannot parse messages file %q: %v\n", *loadMessages, err)
			os.Exit(1)
		}
		if err := agent.AddMessages(msgs); err != nil {
			fmt.Fprintf(os.Stderr, "Error: cannot load messages: %v\n", err)
			os.Exit(1)
		}
	}

	if *continueFlag || *continueShort {
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

	// TTY detection: if stdout is not a terminal, use plain-text rendering
	if !term.IsTerminal(os.Stdout.Fd()) {
		runPlain(agent, taskPrompt, *trajectory, eventLogFile, showDebug)
		return
	}

	runTUI(agent, taskPrompt, *trajectory, *modelFlag, cwd, eventLogFile, showDebug)
}

func runTUI(agent *clnkr.Agent, taskPrompt, trajectory, modelName, cwd string, eventLog *os.File, verbose bool) {
	s := defaultStyles(true) // TODO: detect actual background

	if taskPrompt != "" {
		// Single-task mode: set up event channel and launch agent immediately
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		eventCh := make(chan clnkr.Event, eventChSize)
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
			if errors.Is(fm.agentErr, clnkr.ErrClarificationNeeded) {
				fmt.Fprintln(os.Stderr, "Clarification needed.")
				os.Exit(exitClarificationNeeded)
			}
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

	// Auto-save session on exit (conversational mode)
	if msgs := agent.Messages(); len(msgs) > 0 {
		if err := session.SaveSession(cwd, msgs); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: could not save session: %v\n", err)
		} else {
			dir, _ := session.SessionDir(cwd)
			fmt.Fprintf(os.Stderr, "[Session saved to %s]\n", dir)
		}
	}

	if fm.agentErr != nil {
		if errors.Is(fm.agentErr, clnkr.ErrClarificationNeeded) {
			return
		}
		os.Exit(1)
	}
}

// runPlain provides plain-text output for non-TTY environments.
// Matches hew-core behavior. ~30 lines of duplicated Notify logic.
func runPlain(agent *clnkr.Agent, taskPrompt, trajectory string, eventLog *os.File, verbose bool) {
	if taskPrompt == "" {
		fmt.Fprintf(os.Stderr, "Error: non-interactive mode requires -p\n")
		os.Exit(1)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	agent.Notify = func(e clnkr.Event) {
		if eventLog != nil {
			writeEventLog(eventLog, e)
		}
		switch ev := e.(type) {
		case clnkr.EventResponse:
			fmt.Fprintln(os.Stdout, ev.Message.Content) //nolint:errcheck
		case clnkr.EventCommandStart:
			fmt.Fprintf(os.Stdout, "--- running: %s ---\n", summarizeCommand(ev.Command)) //nolint:errcheck
		case clnkr.EventCommandDone:
			if ev.Stdout != "" {
				fmt.Fprint(os.Stdout, ev.Stdout) //nolint:errcheck
			}
			if ev.Stderr != "" {
				fmt.Fprint(os.Stderr, ev.Stderr) //nolint:errcheck
			}
			fmt.Fprintln(os.Stdout, "--- done ---") //nolint:errcheck
		case clnkr.EventProtocolFailure:
			if verbose {
				fmt.Fprintf(os.Stderr, "[hew] protocol error: %s\n", ev.Reason)
			}
		case clnkr.EventDebug:
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
		if errors.Is(runErr, clnkr.ErrClarificationNeeded) {
			fmt.Fprintln(os.Stderr, "Clarification needed.")
			os.Exit(exitClarificationNeeded)
		}
		fmt.Fprintf(os.Stderr, "Error: %v\n", runErr)
		os.Exit(1)
	}
}

func writeTrajectory(agent *clnkr.Agent, path string) {
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
