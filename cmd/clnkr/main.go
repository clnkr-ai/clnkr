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

var errApprovalPending = errors.New("approval pending")
var errCompactCommandOutsideConversation = errors.New("/compact is only available at the conversational prompt")

type providerConfig = providerconfig.ResolvedProviderConfig

type providerModel interface {
	clnkr.Model
	compaction.FreeformModel
}

type approvalPrompter interface {
	ActReply(ctx context.Context, command string) (string, error)
	Clarify(ctx context.Context, question string) (string, error)
}

type stdinPrompter struct{ reader *lineReader }

func printUsage(flags *flag.FlagSet) {
	fmt.Fprint(os.Stderr, "clnkr - a minimal coding agent\n\nUsage:\n  clnkr                     Start conversational mode\n  clnkr -p \"task\"           Run a single task and exit\n\nOptions:\n")
	flags.PrintDefaults()
	fmt.Fprint(os.Stderr, "\nEnvironment:\n  CLNKR_API_KEY      API key for the LLM provider (required)\n  CLNKR_PROVIDER     Provider adapter semantics\n  CLNKR_PROVIDER_API OpenAI-only API surface override\n  CLNKR_MODEL        Model identifier override\n  CLNKR_BASE_URL     LLM endpoint override; infers provider when provider is unset\n")
}

func aliasedString(preferred, fallback string) string {
	if strings.TrimSpace(preferred) != "" {
		return preferred
	}
	return fallback
}

func newProviderModelForConfig(cfg providerConfig, systemPrompt string) providerModel {
	switch {
	case cfg.Provider == providerconfig.ProviderAnthropic:
		return anthropic.NewModel(cfg.BaseURL, cfg.APIKey, cfg.Model, systemPrompt)
	case cfg.ProviderAPI == providerconfig.ProviderAPIOpenAIResponses:
		return openairesponses.NewModel(cfg.BaseURL, cfg.APIKey, cfg.Model, systemPrompt)
	default:
		return openai.NewModel(cfg.BaseURL, cfg.APIKey, cfg.Model, systemPrompt)
	}
}

type lineResult struct {
	text string
	err  error
}

type lineReader struct{ lines chan lineResult }

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

func prepareSingleTask(task string, fullSend bool, requireApproval func() error) error {
	if err := rejectCompactCommand(task); err != nil || fullSend {
		return err
	}
	return requireApproval()
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
		return newProviderModelForConfig(cfg, compaction.LoadCompactionPrompt(instructions))
	})
}

func handleConversationalInput(ctx context.Context, stderr io.Writer, agent *clnkr.Agent, input string, fullSend bool, prompter approvalPrompter, compactorFactory compaction.Factory) error {
	if stderr == nil {
		stderr = io.Discard
	}
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
		_, _ = fmt.Fprintf(stderr, "[Session compacted: %d messages summarized, %d kept]\n", stats.CompactedMessages, stats.KeptMessages)
		return nil
	}
	return runTask(ctx, agent, input, fullSend, prompter)
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
	flags.Usage = func() {
		printUsage(flags)
	}

	var taskPrompt, promptLong, modelFlag, modelShort, baseURL, baseURLShort, providerFlag, providerAPIFlag string
	var eventLog, trajectory, loadMessages, systemPromptAppend string
	var maxSteps int
	var fullSend, verbose, verboseShort, showVersion, showVersionShort, continueFlag, continueShort, listSessions, listSessionsShort bool
	var noSystemPrompt, noSystemPromptShort, dumpSystemPrompt bool
	flags.StringVar(&taskPrompt, "p", "", "Task to run")
	flags.StringVar(&promptLong, "prompt", "", "Task to run")
	flags.StringVar(&modelFlag, "model", "", "Model identifier")
	flags.StringVar(&modelShort, "m", "", "Model identifier")
	flags.StringVar(&baseURL, "base-url", "", "LLM endpoint URL; infers provider when provider is unset")
	flags.StringVar(&baseURLShort, "u", "", "LLM endpoint URL; infers provider when provider is unset")
	flags.StringVar(&providerFlag, "provider", "", "Provider semantics: anthropic|openai")
	flags.StringVar(&providerAPIFlag, "provider-api", "", "OpenAI API surface: auto|openai-chat-completions|openai-responses")
	flags.IntVar(&maxSteps, "max-steps", 0, "Maximum agent steps (default: 100)")
	flags.BoolVar(&fullSend, "full-send", false, "Execute Act turns without approval")
	flags.BoolVar(&verbose, "verbose", false, "Show internal decisions")
	flags.BoolVar(&verboseShort, "v", false, "Show internal decisions")
	flags.BoolVar(&showVersion, "version", false, "Print version and exit")
	flags.BoolVar(&showVersionShort, "V", false, "Print version and exit")
	flags.StringVar(&eventLog, "event-log", "", "Stream JSONL events to file")
	flags.StringVar(&trajectory, "trajectory", "", "Save message history as JSON")
	flags.StringVar(&loadMessages, "load-messages", "", "Seed conversation from JSON")
	flags.BoolVar(&continueFlag, "continue", false, "Resume latest session")
	flags.BoolVar(&continueShort, "c", false, "Resume latest session")
	flags.BoolVar(&listSessions, "list-sessions", false, "List saved sessions")
	flags.BoolVar(&listSessionsShort, "l", false, "List saved sessions")
	flags.BoolVar(&noSystemPrompt, "no-system-prompt", false, "Skip built-in system prompt")
	flags.BoolVar(&noSystemPromptShort, "S", false, "Skip built-in system prompt")
	flags.StringVar(&systemPromptAppend, "system-prompt-append", "", "Append text to system prompt")
	flags.BoolVar(&dumpSystemPrompt, "dump-system-prompt", false, "Print system prompt and exit")

	if err := flags.Parse(os.Args[1:]); err != nil {
		if err == flag.ErrHelp {
			os.Exit(0)
		}
		fmt.Fprintf(os.Stderr, "Error: %v\nRun 'clnkr --help' for available options.\n", err)
		os.Exit(1)
	}

	if showVersion || showVersionShort {
		fmt.Printf("clnkr %s\n", version)
		os.Exit(0)
	}

	if listSessions || listSessionsShort {
		cwd, err := os.Getwd()
		if err != nil {
			fatalf("cannot get working directory: %v", err)
		}
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

	taskPrompt = aliasedString(promptLong, taskPrompt)
	modelFlag = aliasedString(modelShort, modelFlag)
	baseURL = aliasedString(baseURLShort, baseURL)

	cfg, err := providerconfig.ResolveConfig(providerconfig.Inputs{
		Provider:    providerFlag,
		ProviderAPI: providerAPIFlag,
		Model:       modelFlag,
		BaseURL:     baseURL,
	}, os.Getenv)
	if err != nil {
		if strings.Contains(err.Error(), "api key is required") {
			fmt.Fprintln(os.Stderr, missingAPIKeyMessage)
			os.Exit(1)
		}
		fatalf("%v", err)
	}

	cwd, err := os.Getwd()
	if err != nil {
		fatalf("cannot get working directory: %v", err)
	}

	var eventLogFile *os.File
	if eventLog != "" {
		var err error
		eventLogFile, err = os.OpenFile(eventLog, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			fatalf("cannot open event log %q: %v", eventLog, err)
		}
		defer eventLogFile.Close() //nolint:errcheck
	}

	systemPrompt := clnkr.LoadPromptWithOptions(cwd, clnkr.PromptOptions{
		OmitSystemPrompt:   noSystemPrompt || noSystemPromptShort,
		SystemPromptAppend: systemPromptAppend,
	})

	if dumpSystemPrompt {
		fmt.Print(systemPrompt)
		os.Exit(0)
	}

	model := newProviderModelForConfig(cfg, systemPrompt)
	compactorFactory := makeCompactorFactory(cfg)

	executor := &clnkr.CommandExecutor{}

	agent := clnkr.NewAgent(model, executor, cwd)

	showDebug := verbose || verboseShort
	agent.Notify = func(e clnkr.Event) {
		switch e := e.(type) {
		case clnkr.EventResponse:
			if text, err := clnkr.CanonicalTurnJSON(e.Turn); err == nil {
				fmt.Fprintln(os.Stdout, text) //nolint:errcheck
			}
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

	if maxSteps > 0 {
		agent.MaxSteps = maxSteps
	}

	if loadMessages != "" {
		data, err := os.ReadFile(loadMessages)
		if err != nil {
			fatalf("cannot read messages file %q: %v", loadMessages, err)
		}
		var msgs []clnkr.Message
		if err := json.Unmarshal(data, &msgs); err != nil {
			fatalf("cannot parse messages file %q: %v", loadMessages, err)
		}
		if err := agent.AddMessages(msgs); err != nil {
			fatalf("cannot load messages: %v", err)
		}
	}

	if continueFlag || continueShort {
		if trajectory != "" {
			fatalf("--continue and --trajectory are mutually exclusive")
		}
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

	if trajectory != "" && taskPrompt == "" {
		fatalf("--trajectory requires -p (single-task mode)")
	}

	if taskPrompt != "" {
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
		defer stop()
		if err := prepareSingleTask(taskPrompt, fullSend, requireApprovalInput); err != nil {
			fatalf("%v", err)
		}
		runErr := runTask(ctx, agent, taskPrompt, fullSend, &stdinPrompter{reader: newLineReader(os.Stdin)})
		if trajectory != "" {
			if err := writeTrajectory(trajectory, agent.Messages()); err != nil {
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
	if !fullSend {
		if err := requireApprovalInput(); err != nil {
			fatalf("%v", err)
		}
	}
	reader := newLineReader(os.Stdin)
	prompter := &stdinPrompter{reader: reader}
	for {
		fmt.Print("clnkr> ")
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
		if err := handleConversationalInput(ctx, os.Stderr, agent, input, fullSend, prompter, compactorFactory); err != nil {
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

type commandDonePayload struct {
	Command  string                `json:"command"`
	Stdout   string                `json:"stdout"`
	Stderr   string                `json:"stderr"`
	ExitCode int                   `json:"exit_code"`
	Feedback clnkr.CommandFeedback `json:"feedback,omitempty"`
	Err      string                `json:"err,omitempty"`
}

type protocolFailurePayload struct {
	Reason string `json:"reason"`
	Raw    string `json:"raw"`
}

func writeEventLog(f *os.File, e clnkr.Event) {
	var je jsonEvent
	switch e := e.(type) {
	case clnkr.EventResponse:
		payload, ok := responseEventPayload(e)
		if !ok {
			return
		}
		je = jsonEvent{Type: "response", Payload: payload}
	case clnkr.EventCommandStart:
		je = jsonEvent{Type: "command_start", Payload: e}
	case clnkr.EventCommandDone:
		je = jsonEvent{Type: "command_done", Payload: commandDonePayload{Command: e.Command, Stdout: e.Stdout, Stderr: e.Stderr, ExitCode: e.ExitCode, Feedback: e.Feedback, Err: errString(e.Err)}}
	case clnkr.EventProtocolFailure:
		je = jsonEvent{Type: "protocol_failure", Payload: protocolFailurePayload{Reason: e.Reason, Raw: e.Raw}}
	case clnkr.EventDebug:
		je = jsonEvent{Type: "debug", Payload: e}
	default:
		return
	}
	json.NewEncoder(f).Encode(je) //nolint:errcheck
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func responseEventPayload(ev clnkr.EventResponse) (any, bool) {
	canonical, err := clnkr.CanonicalTurnJSON(ev.Turn)
	if err != nil {
		return nil, false
	}
	return struct {
		Turn  json.RawMessage `json:"turn"`
		Usage clnkr.Usage     `json:"usage"`
		Raw   string          `json:"raw,omitempty"`
	}{
		Turn:  json.RawMessage(canonical),
		Usage: ev.Usage,
		Raw:   ev.Raw,
	}, true
}

func summarizeCommand(cmd string) string {
	lines := strings.Split(cmd, "\n")
	first := lines[0]
	if len(lines) == 1 {
		return first
	}
	return fmt.Sprintf("%s ... (%d lines)", first, len(lines))
}
