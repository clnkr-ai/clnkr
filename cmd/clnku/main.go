package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"

	"github.com/clnkr-ai/clnkr"
	"github.com/clnkr-ai/clnkr/anthropic"
	"github.com/clnkr-ai/clnkr/openai"
	"github.com/clnkr-ai/clnkr/session"
)

// version is set at build time via -ldflags.
var version = "dev"

const exitClarificationNeeded = 2

var errApprovalPending = errors.New("approval pending")

type approvalPrompter interface {
	Confirm(ctx context.Context, command string) (bool, error)
	Clarify(ctx context.Context, question string) (string, error)
}

type stdinPrompter struct {
	reader *lineReader
}

type lineResult struct {
	text string
	err  error
}

type lineReader struct {
	lines chan lineResult
}

func newLineReader(r io.Reader) *lineReader {
	lr := &lineReader{lines: make(chan lineResult)}
	go func() {
		scanner := bufio.NewScanner(r)
		for scanner.Scan() {
			lr.lines <- lineResult{text: scanner.Text()}
		}
		if err := scanner.Err(); err != nil {
			lr.lines <- lineResult{err: err}
		}
		close(lr.lines)
	}()
	return lr
}

func (r *lineReader) ReadLine(ctx context.Context) (string, error) {
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case line, ok := <-r.lines:
		if !ok {
			return "", io.EOF
		}
		if line.err != nil {
			return "", line.err
		}
		return line.text, nil
	}
}

func (p *stdinPrompter) Confirm(ctx context.Context, command string) (bool, error) {
	fmt.Fprintln(os.Stderr, command) //nolint:errcheck
	for {
		fmt.Fprint(os.Stderr, "Approve command? [y/n]: ") //nolint:errcheck
		line, err := p.reader.ReadLine(ctx)
		if err != nil {
			if errors.Is(err, io.EOF) {
				return false, errApprovalPending
			}
			if errors.Is(err, context.Canceled) {
				return false, err
			}
			return false, fmt.Errorf("read approval input: %w", err)
		}
		approved, ok := parseApprovalInput(line)
		if ok {
			return approved, nil
		}
		fmt.Fprintln(os.Stderr, "Please answer yes or no.") //nolint:errcheck
	}
}

func (p *stdinPrompter) Clarify(ctx context.Context, question string) (string, error) {
	fmt.Fprintln(os.Stderr, question)  //nolint:errcheck
	fmt.Fprint(os.Stderr, "Clarify: ") //nolint:errcheck
	line, err := p.reader.ReadLine(ctx)
	if err != nil {
		if errors.Is(err, io.EOF) {
			return "", errApprovalPending
		}
		if errors.Is(err, context.Canceled) {
			return "", err
		}
		return "", fmt.Errorf("read clarification input: %w", err)
	}
	return strings.TrimSpace(line), nil
}

func parseApprovalInput(s string) (bool, bool) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "y", "yes":
		return true, true
	case "n", "no":
		return false, true
	default:
		return false, false
	}
}

func approvalInputAllowed(mode os.FileMode, termEnv string) bool {
	return mode&os.ModeCharDevice != 0 && termEnv != "" && termEnv != "dumb"
}

func requireApprovalInput() error {
	info, err := os.Stdin.Stat()
	if err != nil {
		return fmt.Errorf("stat stdin: %w", err)
	}
	if !approvalInputAllowed(info.Mode(), os.Getenv("TERM")) {
		return fmt.Errorf("approval mode requires interactive stdin; pass --full-send=true to bypass approval")
	}
	return nil
}

func runTask(ctx context.Context, agent *clnkr.Agent, task string, fullSend bool, prompter approvalPrompter) error {
	if fullSend {
		return agent.Run(ctx, task)
	}
	return runApprovalTask(ctx, agent, task, prompter)
}

func runApprovalTask(ctx context.Context, agent *clnkr.Agent, task string, prompter approvalPrompter) error {
	agent.AppendUserMessage(task)
	return runApprovalLoop(ctx, agent, prompter)
}

func runApprovalLoop(ctx context.Context, agent *clnkr.Agent, prompter approvalPrompter) error {
	steps := 0
	protocolErrors := 0

	for {
		result, err := agent.Step(ctx)
		if err != nil {
			return err
		}

		if result.ParseErr != nil {
			protocolErrors++
			if protocolErrors >= 3 {
				return fmt.Errorf("consecutive protocol failures, exiting")
			}
			continue
		}
		protocolErrors = 0

		switch turn := result.Turn.(type) {
		case *clnkr.DoneTurn:
			return nil
		case *clnkr.ClarifyTurn:
			reply, err := waitForClarification(ctx, prompter, turn.Question)
			if err != nil {
				return err
			}
			agent.AppendUserMessage(reply)
		case *clnkr.ActTurn:
			approved, err := prompter.Confirm(ctx, turn.Command)
			if err != nil {
				return err
			}
			if !approved {
				reply, err := waitForClarification(ctx, prompter, "Command denied. What should the agent do instead?")
				if err != nil {
					return err
				}
				agent.AppendUserMessage(reply)
				continue
			}
			if _, err := agent.ExecuteTurn(ctx, turn); err != nil {
				return err
			}
			steps++
			if agent.MaxSteps > 0 && steps >= agent.MaxSteps {
				return agent.RequestStepLimitSummary(ctx)
			}
		default:
			return fmt.Errorf("unexpected turn type %T", turn)
		}
	}
}

func waitForClarification(ctx context.Context, prompter approvalPrompter, question string) (string, error) {
	for {
		reply, err := prompter.Clarify(ctx, question)
		if err != nil {
			return "", err
		}
		if strings.TrimSpace(reply) == "" {
			continue
		}
		return reply, nil
	}
}

func main() {
	flags := flag.NewFlagSet("clnku", flag.ContinueOnError)
	flags.Usage = func() {
		fmt.Fprint(os.Stderr, `clnku - a minimal coding agent

Usage:
  clnku                     Start conversational mode
  clnku -p "task"           Run a single task and exit

Core:
  -p, --prompt string       Task to run (exits after completion)
  -m, --model string        Model identifier (env: $CLNKR_MODEL, default: claude-sonnet-4-20250514)
  -u, --base-url string     LLM endpoint (env: $CLNKR_BASE_URL, default: https://api.anthropic.com)
      --max-steps int       Maximum agent steps (default: 100)
      --full-send           Execute every Act turn without approval
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
  CLNKR_API_KEY     API key for the LLM provider (required)
  CLNKR_MODEL       Model identifier override
  CLNKR_BASE_URL    LLM endpoint override
`)
	}

	prompt := flags.String("p", "", "")
	promptLong := flags.String("prompt", "", "")
	modelFlag := flags.String("model", "", "")
	modelShort := flags.String("m", "", "")
	baseURL := flags.String("base-url", "", "")
	baseURLShort := flags.String("u", "", "")
	maxSteps := flags.Int("max-steps", 0, "")
	fullSend := flags.Bool("full-send", false, "")
	verbose := flags.Bool("verbose", false, "")
	verboseShort := flags.Bool("v", false, "")
	showVersion := flags.Bool("version", false, "")
	showVersionShort := flags.Bool("V", false, "")
	eventLog := flags.String("event-log", "", "")
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
		fmt.Fprintf(os.Stderr, "Error: %v\nRun 'clnku --help' for available options.\n", err)
		os.Exit(1)
	}

	if *showVersion || *showVersionShort {
		fmt.Printf("clnku %s\n", version)
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

	// --prompt and -p are aliases; prefer whichever is set
	taskPrompt := *prompt
	if *promptLong != "" {
		taskPrompt = *promptLong
	}

	// Resolve model: flag > env > default
	if *modelShort != "" {
		*modelFlag = *modelShort
	}
	if *modelFlag == "" {
		if env := os.Getenv("CLNKR_MODEL"); env != "" {
			*modelFlag = env
		} else {
			*modelFlag = "claude-sonnet-4-20250514"
		}
	}

	// Resolve base URL: flag > env > default
	if *baseURLShort != "" {
		*baseURL = *baseURLShort
	}
	baseURLSource := "default"
	if *baseURL == "" {
		if env := os.Getenv("CLNKR_BASE_URL"); env != "" {
			*baseURL = env
			baseURLSource = "CLNKR_BASE_URL env var"
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

	// Resolve API key: CLNKR_API_KEY > ANTHROPIC_API_KEY (Anthropic only)
	apiKey := os.Getenv("CLNKR_API_KEY")
	if apiKey == "" && strings.Contains(*baseURL, "anthropic.com") {
		apiKey = os.Getenv("ANTHROPIC_API_KEY")
	}
	if apiKey == "" {
		msg := "Error: No API key found.\nSet it with: export CLNKR_API_KEY=your-api-key"
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

	systemPrompt := clnkr.LoadPromptWithOptions(cwd, clnkr.PromptOptions{
		OmitSystemPrompt:   *noSystemPrompt || *noSystemPromptShort,
		SystemPromptAppend: *systemPromptAppend,
	})

	if *dumpSystemPrompt {
		fmt.Print(systemPrompt)
		os.Exit(0)
	}

	var model clnkr.Model
	if strings.Contains(*baseURL, "anthropic.com") {
		model = anthropic.NewModel(*baseURL, apiKey, *modelFlag, systemPrompt)
	} else {
		model = openai.NewModel(*baseURL, apiKey, *modelFlag, systemPrompt)
	}

	executor := &clnkr.CommandExecutor{}

	agent := clnkr.NewAgent(model, executor, cwd)

	showDebug := *verbose || *verboseShort
	agent.Notify = func(e clnkr.Event) {
		switch e := e.(type) {
		case clnkr.EventResponse:
			fmt.Fprintln(os.Stdout, e.Message.Content) //nolint:errcheck
		case clnkr.EventCommandStart:
			fmt.Fprintf(os.Stdout, "--- running: %s ---\n", summarizeCommand(e.Command)) //nolint:errcheck
		case clnkr.EventCommandDone:
			if e.Stdout != "" {
				fmt.Fprint(os.Stdout, e.Stdout) //nolint:errcheck
			}
			if e.Stderr != "" {
				fmt.Fprint(os.Stderr, e.Stderr) //nolint:errcheck
			}
			fmt.Fprintln(os.Stdout, "--- done ---") //nolint:errcheck
		case clnkr.EventProtocolFailure:
			if showDebug {
				fmt.Fprintf(os.Stderr, "[clnku] protocol error: %s\n", e.Reason) //nolint:errcheck
			}
		case clnkr.EventDebug:
			if showDebug {
				fmt.Fprintf(os.Stderr, "[clnku] %s\n", e.Message)
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
			fmt.Fprintf(os.Stderr, "Error: no session found for this project.\nRun 'clnku --list-sessions' to see available sessions.\n")
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
		var runErr error
		if *fullSend {
			runErr = agent.Run(ctx, taskPrompt)
		} else {
			if err := requireApprovalInput(); err != nil {
				fmt.Fprintf(os.Stderr, "Error: %v\n", err)
				os.Exit(1)
			}
			runErr = runApprovalTask(ctx, agent, taskPrompt, &stdinPrompter{reader: newLineReader(os.Stdin)})
		}
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
			if errors.Is(runErr, context.Canceled) {
				os.Exit(130)
			}
			if errors.Is(runErr, clnkr.ErrClarificationNeeded) {
				fmt.Fprintln(os.Stderr, "Clarification needed.")
				os.Exit(exitClarificationNeeded)
			}
			fmt.Fprintf(os.Stderr, "Error: %v\n", runErr)
			os.Exit(1)
		}
		return
	}

	// REPL mode — fresh context per run so Ctrl-C cancels the current
	// operation without killing the REPL.
	if !*fullSend {
		if err := requireApprovalInput(); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
	}
	reader := newLineReader(os.Stdin)
	prompter := &stdinPrompter{reader: reader}
	for {
		fmt.Print("clnku> ")
		input, err := reader.ReadLine(context.Background())
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		input = strings.TrimSpace(input)
		if input == "" {
			continue
		}
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
		if err := runTask(ctx, agent, input, *fullSend, prompter); err != nil {
			if errors.Is(err, clnkr.ErrClarificationNeeded) || errors.Is(err, errApprovalPending) || errors.Is(err, context.Canceled) {
				stop()
				continue
			}
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

}

type jsonEvent struct {
	Type    string `json:"type"`
	Payload any    `json:"payload"`
}

func writeEventLog(f *os.File, e clnkr.Event) {
	var je jsonEvent
	switch e := e.(type) {
	case clnkr.EventResponse:
		je = jsonEvent{Type: "response", Payload: e}
	case clnkr.EventCommandStart:
		je = jsonEvent{Type: "command_start", Payload: e}
	case clnkr.EventCommandDone:
		je = jsonEvent{Type: "command_done", Payload: struct {
			Command  string `json:"command"`
			Stdout   string `json:"stdout"`
			Stderr   string `json:"stderr"`
			ExitCode int    `json:"exit_code"`
			Err      string `json:"err,omitempty"`
		}{Command: e.Command, Stdout: e.Stdout, Stderr: e.Stderr, ExitCode: e.ExitCode, Err: errString(e.Err)}}
	case clnkr.EventProtocolFailure:
		je = jsonEvent{Type: "protocol_failure", Payload: struct {
			Reason string `json:"reason"`
			Raw    string `json:"raw"`
		}{Reason: e.Reason, Raw: e.Raw}}
	case clnkr.EventDebug:
		je = jsonEvent{Type: "debug", Payload: e}
	default:
		return
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
