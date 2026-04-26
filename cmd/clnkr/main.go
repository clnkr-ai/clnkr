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
	"strconv"
	"strings"

	"github.com/clnkr-ai/clnkr"
	"github.com/clnkr-ai/clnkr/cmd/internal/compaction"
	"github.com/clnkr-ai/clnkr/cmd/internal/providerconfig"
	"github.com/clnkr-ai/clnkr/cmd/internal/session"
	"github.com/clnkr-ai/clnkr/internal/providers/anthropic"
	"github.com/clnkr-ai/clnkr/internal/providers/openai"
	"github.com/clnkr-ai/clnkr/internal/providers/openairesponses"
)

// version is set at build time via -ldflags.
var version = "dev"

const exitClarificationNeeded = 2
const missingAPIKeyMessage = "Error: No API key found.\nSet it with: export CLNKR_API_KEY=your-api-key"
const approvalInputMessage = "approval mode requires interactive stdin; pass --full-send=true to bypass approval"

var errApprovalPending = errors.New("approval pending")
var errCompactCommandOutsideConversation = errors.New("/compact is only available at the conversational prompt")

type providerConfig = providerconfig.ResolvedProviderConfig

type approvalPrompter interface {
	ActReply(ctx context.Context, command string) (string, error)
	Clarify(ctx context.Context, question string) (string, error)
}

type stdinPrompter struct {
	reader *lineReader
}

func usageText() string {
	return `clnkr - a minimal coding agent

Usage:
  clnkr                     Start conversational mode

  -p, --prompt string       Task to run unattended and exit
  -m, --model string        Model identifier (required; env: $CLNKR_MODEL)
  -u, --base-url string     LLM endpoint transport URL (env: $CLNKR_BASE_URL)
      --provider string     Provider adapter: anthropic|openai
      --provider-api string OpenAI-only override
      --max-steps int       Limit executed commands
                            before summary (default: 100)
      --full-send           Execute every act batch without approval
                            (implied by -p)
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
      --trajectory string      Save single-task history as JSON on exit

  -V, --version             Print version and exit

Environment:
  CLNKR_API_KEY      API key for the LLM provider (required)
  CLNKR_PROVIDER     Provider adapter semantics
  CLNKR_PROVIDER_API OpenAI-only API surface override
  CLNKR_MODEL        Model identifier override
  CLNKR_BASE_URL     Endpoint override; infers provider when provider is unset

Defaults:
  anthropic base URL  https://api.anthropic.com
  openai base URL     https://api.openai.com/v1
`
}

func aliasedString(preferred, fallback string) string {
	if strings.TrimSpace(preferred) != "" {
		return preferred
	}
	return fallback
}

func newModelForConfig(cfg providerConfig, systemPrompt string) clnkr.Model {
	if cfg.Provider == providerconfig.ProviderAnthropic {
		return anthropic.NewModel(cfg.BaseURL, cfg.APIKey, cfg.Model, systemPrompt)
	}
	if cfg.ProviderAPI == providerconfig.ProviderAPIOpenAIResponses {
		return openairesponses.NewModel(cfg.BaseURL, cfg.APIKey, cfg.Model, systemPrompt)
	}
	return openai.NewModel(cfg.BaseURL, cfg.APIKey, cfg.Model, systemPrompt)
}

func newFreeformModelForConfig(cfg providerConfig, systemPrompt string) compaction.FreeformModel {
	if cfg.Provider == providerconfig.ProviderAnthropic {
		return anthropic.NewModel(cfg.BaseURL, cfg.APIKey, cfg.Model, systemPrompt)
	}
	if cfg.ProviderAPI == providerconfig.ProviderAPIOpenAIResponses {
		return openairesponses.NewModel(cfg.BaseURL, cfg.APIKey, cfg.Model, systemPrompt)
	}
	return openai.NewModel(cfg.BaseURL, cfg.APIKey, cfg.Model, systemPrompt)
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

func (p *stdinPrompter) ActReply(ctx context.Context, command string) (string, error) {
	fmt.Fprintln(os.Stderr, command) //nolint:errcheck
	for {
		fmt.Fprint(os.Stderr, "Send 'y' to approve, or type what the agent should do instead: ") //nolint:errcheck
		reply, err := p.readReply(ctx, "approval")
		if err != nil || reply != "" {
			return reply, err
		}
	}
}

func (p *stdinPrompter) Clarify(ctx context.Context, question string) (string, error) {
	fmt.Fprintln(os.Stderr, question)  //nolint:errcheck
	fmt.Fprint(os.Stderr, "Clarify: ") //nolint:errcheck
	return p.readReply(ctx, "clarification")
}

func (p *stdinPrompter) readReply(ctx context.Context, kind string) (string, error) {
	line, err := p.reader.ReadLine(ctx)
	if err != nil {
		if errors.Is(err, io.EOF) {
			return "", errApprovalPending
		}
		if errors.Is(err, context.Canceled) {
			return "", err
		}
		return "", fmt.Errorf("read %s input: %w", kind, err)
	}
	return strings.TrimSpace(line), nil
}

func parseCompactCommand(input string) (instructions string, ok bool) {
	input = strings.TrimSpace(input)
	fields := strings.Fields(input)
	if len(fields) == 0 || fields[0] != "/compact" {
		return "", false
	}
	return strings.TrimSpace(strings.TrimPrefix(input, "/compact")), true
}

func makeCompactorFactory(cfg providerConfig) compaction.Factory {
	return compaction.NewFactory(func(instructions string) compaction.FreeformModel {
		return newFreeformModelForConfig(cfg, compaction.LoadCompactionPrompt(instructions))
	})
}

func handleConversationalInput(ctx context.Context, agent *clnkr.Agent, input string, fullSend bool, prompter approvalPrompter, compactorFactory compaction.Factory) error {
	if instructions, ok := parseCompactCommand(input); ok {
		if compactorFactory == nil {
			return fmt.Errorf("compact command: no compactor factory configured")
		}
		compactor := compactorFactory(instructions)
		if compactor == nil {
			return fmt.Errorf("compact command: no compactor configured")
		}
		stats, err := agent.Compact(ctx, compactor, clnkr.CompactOptions{
			Instructions:    instructions,
			KeepRecentTurns: 2,
		})
		if err != nil {
			return err
		}
		_, _ = fmt.Fprintf(os.Stderr, "[Session compacted: %d messages summarized, %d kept]\n", stats.CompactedMessages, stats.KeptMessages)
		return nil
	}
	if fullSend {
		return agent.Run(ctx, input)
	}
	return runApprovalTask(ctx, agent, input, prompter)
}

func rejectCompactCommand(input string) error {
	if _, ok := parseCompactCommand(input); ok {
		return errCompactCommandOutsideConversation
	}
	return nil
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
			reply, err := waitForReply(ctx, prompter.Clarify, turn.Question)
			if err != nil {
				return err
			}
			agent.AppendUserMessage(reply)
		case *clnkr.ActTurn:
			reply, err := waitForReply(ctx, prompter.ActReply, formatActProposal(turn.Bash.Commands))
			if err != nil {
				return err
			}
			if strings.TrimSpace(reply) != "y" {
				agent.AppendUserMessage(reply)
				continue
			}
			result, err := agent.ExecuteTurn(ctx, turn)
			if err != nil {
				return err
			}
			steps += result.ExecCount
			if agent.MaxSteps > 0 && steps >= agent.MaxSteps {
				return agent.RequestStepLimitSummary(ctx)
			}
		default:
			return fmt.Errorf("unexpected turn type %T", turn)
		}
	}
}

func waitForReply(ctx context.Context, prompt func(context.Context, string) (string, error), text string) (string, error) {
	for {
		reply, err := prompt(ctx, text)
		if err != nil {
			return "", err
		}
		if strings.TrimSpace(reply) == "" {
			continue
		}
		if err := rejectCompactCommand(reply); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err) //nolint:errcheck
			continue
		}
		return reply, nil
	}
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "Error: "+format+"\n", args...)
	os.Exit(1)
}

func exitRunErr(err error) {
	if err == nil {
		return
	}
	if errors.Is(err, context.Canceled) {
		os.Exit(130)
	}
	if errors.Is(err, clnkr.ErrClarificationNeeded) {
		fmt.Fprintln(os.Stderr, "Clarification needed.")
		os.Exit(exitClarificationNeeded)
	}
	fatalf("%v", err)
}

func writeTrajectory(path string, messages []clnkr.Message) error {
	data, err := json.MarshalIndent(messages, "", "  ")
	if err != nil {
		return fmt.Errorf("cannot marshal trajectory: %w", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("cannot write trajectory %q: %w", path, err)
	}
	return nil
}

func formatActProposal(commands []clnkr.BashAction) string {
	var b strings.Builder
	for i, action := range commands {
		if i > 0 {
			b.WriteByte('\n')
		}
		command := action.Command
		if workdir := strings.TrimSpace(action.Workdir); workdir != "" {
			command = fmt.Sprintf("%s in %s", command, workdir)
		}
		fmt.Fprintf(&b, "%d. %s", i+1, command)
	}
	return b.String()
}

func main() {
	flags := flag.NewFlagSet("clnkr", flag.ContinueOnError)
	flags.Usage = func() {}
	flags.SetOutput(io.Discard)

	taskPromptFlag := flags.String("p", "", "")
	promptLong := flags.String("prompt", "", "")
	modelFlag := flags.String("model", "", "")
	modelShort := flags.String("m", "", "")
	baseURLFlag := flags.String("base-url", "", "")
	baseURLShort := flags.String("u", "", "")
	providerFlag := flags.String("provider", "", "")
	providerAPIFlag := flags.String("provider-api", "", "")
	maxSteps := flags.Int("max-steps", 0, "")
	fullSend := new(bool)
	explicitFullSendFalse := false
	flags.BoolFunc("full-send", "", func(s string) error {
		value, err := strconv.ParseBool(s)
		if err != nil {
			return err
		}
		*fullSend = value
		explicitFullSendFalse = explicitFullSendFalse || !value
		return nil
	})
	verbose, verboseShort := flags.Bool("verbose", false, ""), flags.Bool("v", false, "")
	showVersion, showVersionShort := flags.Bool("version", false, ""), flags.Bool("V", false, "")
	eventLog := flags.String("event-log", "", "")
	trajectory := flags.String("trajectory", "", "")
	loadMessages := flags.String("load-messages", "", "")
	continueFlag := flags.Bool("continue", false, "")
	continueShort := flags.Bool("c", false, "")
	listSessions, listSessionsShort := flags.Bool("list-sessions", false, ""), flags.Bool("l", false, "")
	noSystemPrompt, noSystemPromptShort := flags.Bool("no-system-prompt", false, ""), flags.Bool("S", false, "")
	systemPromptAppend := flags.String("system-prompt-append", "", "")
	dumpSystemPrompt := flags.Bool("dump-system-prompt", false, "")

	if err := flags.Parse(os.Args[1:]); err != nil {
		if err == flag.ErrHelp {
			fmt.Fprint(os.Stdout, usageText()) //nolint:errcheck
			os.Exit(0)
		}
		fmt.Fprintf(os.Stderr, "Error: %v\nRun 'clnkr --help' for available options.\n", err)
		os.Exit(1)
	}

	if *showVersion || *showVersionShort {
		fmt.Printf("clnkr %s\n", version)
		os.Exit(0)
	}

	cwd, err := os.Getwd()
	if err != nil {
		fatalf("cannot get working directory: %v", err)
	}

	if *listSessions || *listSessionsShort {
		sessions, err := session.ListSessions(cwd)
		if err != nil {
			fatalf("cannot list sessions: %v", err)
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

	taskPrompt := aliasedString(*promptLong, *taskPromptFlag)
	singleTask := taskPrompt != ""
	if singleTask && explicitFullSendFalse {
		fatalf("--full-send=false conflicts with -p")
	}
	if singleTask {
		*fullSend = true
	}

	systemPrompt := clnkr.LoadPromptWithOptions(cwd, clnkr.PromptOptions{
		OmitSystemPrompt:   *noSystemPrompt || *noSystemPromptShort,
		SystemPromptAppend: *systemPromptAppend,
	})

	if *dumpSystemPrompt {
		fmt.Print(systemPrompt)
		os.Exit(0)
	}

	if (*continueFlag || *continueShort) && *trajectory != "" {
		fatalf("--continue and --trajectory are mutually exclusive")
	}
	if *trajectory != "" && !singleTask {
		fatalf("--trajectory requires -p (single-task mode)")
	}

	cfg, err := providerconfig.ResolveConfig(providerconfig.Inputs{
		Provider:    *providerFlag,
		ProviderAPI: *providerAPIFlag,
		Model:       aliasedString(*modelShort, *modelFlag),
		BaseURL:     aliasedString(*baseURLShort, *baseURLFlag),
	}, os.Getenv)
	if err != nil {
		if strings.Contains(err.Error(), "api key is required") {
			fmt.Fprintln(os.Stderr, missingAPIKeyMessage)
			os.Exit(1)
		}
		fatalf("%v", err)
	}

	var eventLogFile *os.File
	if *eventLog != "" {
		eventLogFile, err = os.OpenFile(*eventLog, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			fatalf("cannot open event log %q: %v", *eventLog, err)
		}
		defer eventLogFile.Close() //nolint:errcheck
	}

	model := newModelForConfig(cfg, systemPrompt)
	compactorFactory := makeCompactorFactory(cfg)

	executor := &clnkr.CommandExecutor{}

	agent := clnkr.NewAgent(model, executor, cwd)

	showDebug := *verbose || *verboseShort
	agent.Notify = func(e clnkr.Event) {
		switch e := e.(type) {
		case clnkr.EventResponse:
			switch turn := e.Turn.(type) {
			case *clnkr.DoneTurn:
				fmt.Fprintln(os.Stdout, turn.Summary) //nolint:errcheck
			case *clnkr.ClarifyTurn:
				if *fullSend {
					fmt.Fprintln(os.Stderr, turn.Question) //nolint:errcheck
				}
			default:
				if showDebug {
					if text, err := clnkr.CanonicalTurnJSON(e.Turn); err == nil {
						fmt.Fprintln(os.Stderr, text) //nolint:errcheck
					}
				}
			}
		case clnkr.EventCommandStart:
			command := summarizeCommand(e.Command)
			if e.Dir != "" && e.Dir != cwd {
				command += " in " + e.Dir
			}
			fmt.Fprintf(os.Stderr, "--- running: %s ---\n", command) //nolint:errcheck
		case clnkr.EventCommandDone:
			fmt.Fprint(os.Stdout, e.Stdout) //nolint:errcheck
			if e.Stdout != "" && !strings.HasSuffix(e.Stdout, "\n") {
				fmt.Fprintln(os.Stdout) //nolint:errcheck
			}
			fmt.Fprint(os.Stderr, e.Stderr) //nolint:errcheck
			if e.Stderr != "" && !strings.HasSuffix(e.Stderr, "\n") {
				fmt.Fprintln(os.Stderr) //nolint:errcheck
			}
			fmt.Fprintln(os.Stderr, "--- done ---") //nolint:errcheck
		case clnkr.EventProtocolFailure:
			if showDebug {
				fmt.Fprintf(os.Stderr, "[clnkr] protocol error: %s\n", e.Reason) //nolint:errcheck
			}
		case clnkr.EventDebug:
			if showDebug {
				fmt.Fprintf(os.Stderr, "[clnkr] %s\n", e.Message)
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
			fatalf("cannot read messages file %q: %v", *loadMessages, err)
		}
		var msgs []clnkr.Message
		if err := json.Unmarshal(data, &msgs); err != nil {
			fatalf("cannot parse messages file %q: %v", *loadMessages, err)
		}
		if err := agent.AddMessages(msgs); err != nil {
			fatalf("cannot load messages: %v", err)
		}
	}

	if *continueFlag || *continueShort {
		msgs, err := session.LoadLatestSession(cwd)
		if err != nil {
			fatalf("cannot load session: %v", err)
		}
		if msgs == nil {
			fmt.Fprintf(os.Stderr, "Error: no session found for this project.\nRun 'clnkr --list-sessions' to see available sessions.\n")
			os.Exit(1)
		}
		if err := agent.AddMessages(msgs); err != nil {
			fatalf("cannot resume session: %v", err)
		}
		fmt.Fprintf(os.Stderr, "[Resumed session with %d messages]\n", len(msgs))
	}

	if singleTask {
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
		defer stop()
		if err := rejectCompactCommand(taskPrompt); err != nil {
			fatalf("%v", err)
		}
		runErr := agent.Run(ctx, taskPrompt)
		if *trajectory != "" {
			if err := writeTrajectory(*trajectory, agent.Messages()); err != nil {
				fmt.Fprintf(os.Stderr, "Error: %v\n", err)
				if runErr != nil {
					fmt.Fprintf(os.Stderr, "Error: %v\n", runErr)
				}
				os.Exit(1)
			}
		}
		exitRunErr(runErr)
		return
	}

	// REPL mode — fresh context per run so Ctrl-C cancels the current
	// operation without killing the REPL.
	showPrompt := isTerminal(os.Stdin.Fd())
	if !*fullSend && !showPrompt {
		fatalf("%v", approvalInputMessage)
	}
	reader := newLineReader(os.Stdin)
	prompter := &stdinPrompter{reader: reader}
	var loopErr error
	for {
		if showPrompt {
			fmt.Fprint(os.Stderr, "clnkr> ") //nolint:errcheck
		}
		input, err := reader.ReadLine(context.Background())
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			fatalf("%v", err)
		}
		input = strings.TrimSpace(input)
		if input == "" {
			continue
		}
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
		if err := handleConversationalInput(ctx, agent, input, *fullSend, prompter, compactorFactory); err != nil {
			if !showPrompt {
				stop()
				loopErr = err
				break
			}
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
	exitRunErr(loopErr)

}

type jsonEvent struct {
	Type    string `json:"type"`
	Payload any    `json:"payload"`
}

func writeEventLog(f *os.File, e clnkr.Event) {
	var je jsonEvent
	switch e := e.(type) {
	case clnkr.EventResponse:
		canonical, err := clnkr.CanonicalTurnJSON(e.Turn)
		if err != nil {
			return
		}
		payload := map[string]any{
			"turn":  json.RawMessage(canonical),
			"usage": map[string]int{"input_tokens": e.Usage.InputTokens, "output_tokens": e.Usage.OutputTokens},
		}
		if e.Raw != "" {
			payload["raw"] = e.Raw
		}
		je = jsonEvent{Type: "response", Payload: payload}
	case clnkr.EventCommandStart:
		je = jsonEvent{Type: "command_start", Payload: map[string]string{"command": e.Command, "dir": e.Dir}}
	case clnkr.EventCommandDone:
		errText := ""
		if e.Err != nil {
			errText = e.Err.Error()
		}
		je = jsonEvent{Type: "command_done", Payload: struct {
			Command  string                `json:"command"`
			Stdout   string                `json:"stdout"`
			Stderr   string                `json:"stderr"`
			ExitCode int                   `json:"exit_code"`
			Feedback clnkr.CommandFeedback `json:"feedback,omitempty"`
			Err      string                `json:"err,omitempty"`
		}{Command: e.Command, Stdout: e.Stdout, Stderr: e.Stderr, ExitCode: e.ExitCode, Feedback: e.Feedback, Err: errText}}
	case clnkr.EventProtocolFailure:
		je = jsonEvent{Type: "protocol_failure", Payload: map[string]string{"reason": e.Reason, "raw": e.Raw}}
	case clnkr.EventDebug:
		je = jsonEvent{Type: "debug", Payload: map[string]string{"message": e.Message}}
	default:
		return
	}
	json.NewEncoder(f).Encode(je) //nolint:errcheck
}

func summarizeCommand(cmd string) string {
	if idx := strings.IndexByte(cmd, '\n'); idx >= 0 {
		return fmt.Sprintf("%s ... (%d lines)", cmd[:idx], strings.Count(cmd, "\n")+1)
	}
	return cmd
}
