package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strconv"
	"strings"

	"github.com/clnkr-ai/clnkr"
	"github.com/clnkr-ai/clnkr/cmd/internal/clnkrapp"
	"github.com/clnkr-ai/clnkr/cmd/internal/providerconfig"
	"github.com/clnkr-ai/clnkr/cmd/internal/session"
	providerdomain "github.com/clnkr-ai/clnkr/internal/providers/providerconfig"
)

// version is set at build time via -ldflags.
var version = "dev"

func usageText() string {
	return `clnkr - a minimal coding agent

Usage:
  clnkr                     Start conversational mode

Options:
  -p, --prompt string       Task to run unattended and exit
  -m, --model string        Model identifier (required; env: $CLNKR_MODEL)
  -u, --base-url string     LLM endpoint transport URL (env: $CLNKR_BASE_URL)
      --provider string     Provider adapter: anthropic|openai
      --act-protocol string Act protocol: clnkr-inline|tool-calls
      --effort string       Provider effort: auto|low|medium|high|xhigh|max
      --max-output-tokens int Maximum response output tokens
      --max-steps int       Limit executed commands
                            before summary (default: 100)
      --full-send           Execute every act batch without approval
                            (implied by -p)
  -v, --verbose             Show internal decisions

Provider overrides:
      --provider-api string OpenAI API override
      --thinking-budget-tokens int
                            Anthropic legacy/debug thinking budget override

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

type stdinPrompter struct{ reader *lineReader }

func aliasedString(preferred, fallback string) string {
	if strings.TrimSpace(preferred) != "" {
		return preferred
	}
	return fallback
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
			return "", io.EOF
		}
		if errors.Is(err, context.Canceled) {
			return "", err
		}
		return "", fmt.Errorf("read %s input: %w", kind, err)
	}
	return strings.TrimSpace(line), nil
}

func handleConversationalInput(ctx context.Context, agent *clnkr.Agent, input string, fullSend bool, prompter clnkrapp.ApprovalPrompter, compactorFactory func(string) clnkr.Compactor) error {
	stats, compacted, err := clnkrapp.HandleCompactCommand(ctx, agent, input, compactorFactory)
	if err != nil || compacted {
		if err == nil {
			_, _ = fmt.Fprintf(os.Stderr, "[Session compacted: %d messages summarized, %d kept]\n", stats.CompactedMessages, stats.KeptMessages)
		}
		return err
	}
	if fullSend {
		return agent.Run(ctx, input)
	}
	return clnkrapp.RunApprovalTask(ctx, agent, input, prompter, func(err error) {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err) //nolint:errcheck
	})
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
		os.Exit(2)
	}
	fatalf("%v", err)
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
	actProtocolFlag := flags.String("act-protocol", "clnkr-inline", "")
	effortFlag := flags.String("effort", "", "")
	thinkingBudgetTokens := flags.Int("thinking-budget-tokens", 0, "")
	maxOutputTokens := flags.Int("max-output-tokens", 0, "")
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
	var thinkingBudgetTokensSet, maxOutputTokensSet bool
	flags.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "thinking-budget-tokens":
			thinkingBudgetTokensSet = true
		case "max-output-tokens":
			maxOutputTokensSet = true
		}
	})

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

	actProtocol, err := clnkr.ParseActProtocol(*actProtocolFlag)
	if err != nil {
		fatalf("%v", err)
	}

	systemPrompt := clnkr.LoadPromptWithOptions(cwd, clnkr.PromptOptions{
		OmitSystemPrompt:   *noSystemPrompt || *noSystemPromptShort,
		SystemPromptAppend: *systemPromptAppend,
		ActProtocol:        actProtocol,
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
		ActProtocol: actProtocol,
		RequestOptions: providerdomain.ProviderRequestOptions{
			Effort: providerdomain.ProviderEffortOptions{
				Level: *effortFlag,
				Set:   *effortFlag != "",
			},
			Output: providerdomain.ProviderOutputOptions{
				MaxOutputTokens: providerdomain.OptionalInt{
					Value: *maxOutputTokens,
					Set:   maxOutputTokensSet,
				},
			},
			AnthropicManual: providerdomain.AnthropicManualThinkingOptions{
				ThinkingBudgetTokens: providerdomain.OptionalInt{
					Value: *thinkingBudgetTokens,
					Set:   thinkingBudgetTokensSet,
				},
			},
		},
	}, os.Getenv)
	if err != nil {
		if strings.Contains(err.Error(), "api key is required") {
			fmt.Fprintln(os.Stderr, "Error: No API key found.\nSet it with: export CLNKR_API_KEY=your-api-key")
			os.Exit(1)
		}
		fatalf("%v", err)
	}
	runMetadata := clnkrapp.NewRunMetadata(version, cfg, systemPrompt)

	var eventLogFile *os.File
	if *eventLog != "" {
		eventLogFile, err = os.OpenFile(*eventLog, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			fatalf("cannot open event log %q: %v", *eventLog, err)
		}
		defer eventLogFile.Close() //nolint:errcheck
	}

	model := clnkrapp.NewModelForConfig(cfg, systemPrompt)
	compactorFactory := clnkrapp.MakeCompactorFactory(cfg)

	executor := &clnkr.CommandExecutor{}

	agent := clnkr.NewAgent(model, executor, cwd)
	agent.ActProtocol = cfg.ActProtocol

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
			_ = clnkrapp.WriteEventLog(eventLogFile, e)
		}
	}
	if event, err := clnkrapp.RunMetadataDebugEvent(runMetadata); err != nil {
		fatalf("%v", err)
	} else {
		agent.Notify(event)
	}

	if *maxSteps > 0 {
		agent.MaxSteps = *maxSteps
	}

	if *loadMessages != "" {
		data, err := os.ReadFile(*loadMessages)
		if err != nil {
			fatalf("cannot read messages file %q: %v", *loadMessages, err)
		}
		msgs, err := clnkrapp.LoadMessages(data)
		if err != nil {
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
		if err := clnkrapp.RejectCompactCommand(taskPrompt); err != nil {
			fatalf("%v", err)
		}
		runErr := agent.Run(ctx, taskPrompt)
		if *trajectory != "" {
			if err := clnkrapp.WriteTrajectory(*trajectory, agent.Messages()); err != nil {
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
		fatalf("%v", "approval mode requires interactive stdin; pass --full-send=true to bypass approval")
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
			if errors.Is(err, clnkr.ErrClarificationNeeded) || errors.Is(err, io.EOF) || errors.Is(err, context.Canceled) {
				stop()
				continue
			}
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		}
		stop()
	}
	// Auto-save session on exit (conversational mode)
	if msgs := agent.Messages(); len(msgs) > 0 {
		if err := session.SaveSessionWithMetadata(cwd, msgs, runMetadata); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: could not save session: %v\n", err)
		} else {
			dir, _ := session.SessionDir(cwd)
			fmt.Fprintf(os.Stderr, "[Session saved to %s]\n", dir)
		}
	}
	exitRunErr(loopErr)

}

func summarizeCommand(cmd string) string {
	if idx := strings.IndexByte(cmd, '\n'); idx >= 0 {
		return fmt.Sprintf("%s ... (%d lines)", cmd[:idx], strings.Count(cmd, "\n")+1)
	}
	return cmd
}
