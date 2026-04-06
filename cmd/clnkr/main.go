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

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/term"
	clnkr "github.com/clnkr-ai/clnkr"
	"github.com/clnkr-ai/clnkr/anthropic"
	"github.com/clnkr-ai/clnkr/compaction"
	"github.com/clnkr-ai/clnkr/delegate"
	"github.com/clnkr-ai/clnkr/openai"
	"github.com/clnkr-ai/clnkr/session"
)

// version is set at build time via -ldflags.
var version = "dev"

const defaultAnthropicModel = "claude-sonnet-4-6"

const exitClarificationNeeded = 2

var errApprovalPending = errors.New("approval pending")

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
  clnkr                    Start conversational mode
  clnkr -p "task"          Run a single task and exit

Core:
  -p, --prompt string       Task to run (exits after completion)
  -m, --model string        Model identifier (env: $CLNKR_MODEL, default: ` + defaultAnthropicModel + `)
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
`
}

func resolveModelValue(flagValue, envValue string) string {
	if flagValue != "" {
		return flagValue
	}
	if envValue != "" {
		return envValue
	}
	return defaultAnthropicModel
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
		line, err := p.reader.ReadLine(ctx)
		if err != nil {
			if errors.Is(err, io.EOF) {
				return "", errApprovalPending
			}
			if errors.Is(err, context.Canceled) {
				return "", err
			}
			return "", fmt.Errorf("read approval input: %w", err)
		}
		reply := strings.TrimSpace(line)
		if reply != "" {
			return reply, nil
		}
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

func isApprovalReply(s string) bool {
	return strings.TrimSpace(s) == "y"
}

func requireApprovalInput() error {
	if !term.IsTerminal(os.Stdin.Fd()) {
		return fmt.Errorf("approval mode requires interactive stdin; pass --full-send=true to bypass approval")
	}
	return nil
}

func runPlainApproval(ctx context.Context, agent *clnkr.Agent, task string, prompter approvalPrompter) error {
	agent.AppendUserMessage(task)
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
			reply, err := waitForActReply(ctx, prompter, turn.Command)
			if err != nil {
				return err
			}
			if !isApprovalReply(reply) {
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

func waitForActReply(ctx context.Context, prompter approvalPrompter, command string) (string, error) {
	for {
		reply, err := prompter.ActReply(ctx, command)
		if err != nil {
			return "", err
		}
		if strings.TrimSpace(reply) == "" {
			continue
		}
		return reply, nil
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

func makeCompactorFactory(baseURL, apiKey, modelName string) compaction.Factory {
	return compaction.NewFactory(func(instructions string) clnkr.Model {
		systemPrompt := compaction.LoadCompactionPrompt(instructions)
		if strings.Contains(baseURL, "anthropic.com") {
			return anthropic.NewModel(baseURL, apiKey, modelName, systemPrompt)
		}
		return openai.NewModel(baseURL, apiKey, modelName, systemPrompt)
	})
}

func main() {
	flags := flag.NewFlagSet("clnkr", flag.ContinueOnError)
	flags.Usage = func() {
		fmt.Fprint(os.Stderr, usageText())
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
		fmt.Fprintf(os.Stderr, "Error: %v\nRun 'clnkr --help' for available options.\n", err)
		os.Exit(1)
	}

	if *showVersion || *showVersionShort {
		fmt.Printf("clnkr %s\n", version)
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
	*modelFlag = resolveModelValue(*modelFlag, os.Getenv("CLNKR_MODEL"))

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
			fmt.Fprintf(os.Stderr, "Error: no session found for this project.\nRun 'clnkr --list-sessions' to see available sessions.\n")
			os.Exit(1)
		}
		if err := agent.AddMessages(msgs); err != nil {
			fmt.Fprintf(os.Stderr, "Error: cannot resume session: %v\n", err)
			os.Exit(1)
		}
	}

	// TTY detection: if stdout is not a terminal, use plain-text rendering
	if !term.IsTerminal(os.Stdout.Fd()) {
		runPlain(agent, taskPrompt, *trajectory, eventLogFile, showDebug, *fullSend)
		return
	}

	runTUI(
		agent,
		taskPrompt,
		*trajectory,
		*modelFlag,
		cwd,
		eventLogFile,
		showDebug,
		*fullSend,
		makeCompactorFactory(*baseURL, apiKey, *modelFlag),
		delegate.Runner{},
	)
}

func runTUI(agent *clnkr.Agent, taskPrompt, trajectory, modelName, cwd string, eventLog *os.File, verbose bool, fullSend bool, compactorFactory compaction.Factory, delegateRunner delegate.Runner) {
	s := defaultStyles(true) // TODO: detect actual background

	if taskPrompt != "" {
		if !fullSend {
			m := newModel(modelOpts{
				styles:           s,
				verbose:          verbose,
				modelName:        modelName,
				maxSteps:         agent.MaxSteps,
				fullSend:         fullSend,
				exitOnRunFinish:  true,
				compactorFactory: compactorFactory,
				delegateRunner:   delegateRunner,
			})
			m.shared.agent = agent
			m.shared.eventLog = eventLog
			m.shared.cwd = agent.Cwd()
			seedModelHistory(&m, agent)
			m.chat.appendUserMessage(taskPrompt)
			m.chat.updateViewport()
			m.running = true
			m.status.startRun()
			ctx, cancel := context.WithCancel(context.Background())
			m.cancel = cancel
			m.runCtx = ctx
			eventCh := make(chan clnkr.Event, eventChSize)
			m.eventCh = eventCh
			m.closeEventChOnFinish = true
			m.shared.agent.Notify = makeNotify(eventCh, eventLog)
			m.shared.agent.AppendUserMessage(taskPrompt)
			m.startupCmd = tea.Batch(stepCmd(agent, ctx), tickCmd())

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

			if trajectory != "" {
				writeTrajectory(agent, trajectory)
			}
			if fm.agentErr != nil {
				os.Exit(1)
			}
			return
		}

		// Single-task mode: set up event channel and launch agent immediately
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		eventCh := make(chan clnkr.Event, eventChSize)
		agent.Notify = makeNotify(eventCh, eventLog)

		m := newModel(modelOpts{
			eventCh:          eventCh,
			styles:           s,
			verbose:          verbose,
			cancel:           cancel,
			modelName:        modelName,
			maxSteps:         agent.MaxSteps,
			fullSend:         fullSend,
			exitOnRunFinish:  true,
			compactorFactory: compactorFactory,
			delegateRunner:   delegateRunner,
		})
		m.shared.agent = agent
		m.shared.eventLog = eventLog
		m.shared.cwd = agent.Cwd()
		seedModelHistory(&m, agent)
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
			if errors.Is(fm.agentErr, context.Canceled) {
				os.Exit(130)
			}
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
		styles:           s,
		verbose:          verbose,
		modelName:        modelName,
		maxSteps:         agent.MaxSteps,
		fullSend:         fullSend,
		compactorFactory: compactorFactory,
		delegateRunner:   delegateRunner,
	})
	m.shared.agent = agent
	m.shared.eventLog = eventLog
	m.shared.cwd = agent.Cwd()
	seedModelHistory(&m, agent)

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

func seedModelHistory(m *model, agent *clnkr.Agent) {
	history := agent.Messages()
	if len(history) == 0 {
		return
	}
	m.chat.hydrateHistory(history)
	m.chat.updateViewport()
}

// runPlain provides plain-text output for non-TTY environments.
// Matches core library behavior. ~30 lines of duplicated Notify logic.
func runPlain(agent *clnkr.Agent, taskPrompt, trajectory string, eventLog *os.File, verbose bool, fullSend bool) {
	if taskPrompt == "" {
		fmt.Fprintf(os.Stderr, "Error: non-interactive mode requires -p\n")
		os.Exit(1)
	}

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
				fmt.Fprintf(os.Stderr, "[clnkr] protocol error: %s\n", ev.Reason)
			}
		case clnkr.EventDebug:
			if verbose {
				fmt.Fprintf(os.Stderr, "[clnkr] %s\n", ev.Message)
			}
		default:
		}
	}

	var runErr error
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	if fullSend {
		runErr = agent.Run(ctx, taskPrompt)
	} else {
		if err := requireApprovalInput(); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		runErr = runPlainApproval(ctx, agent, taskPrompt, &stdinPrompter{reader: newLineReader(os.Stdin)})
	}

	if trajectory != "" {
		writeTrajectory(agent, trajectory)
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
