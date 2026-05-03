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
	"syscall"
	"unsafe"

	"github.com/clnkr-ai/clnkr"
	"github.com/clnkr-ai/clnkr/cmd/internal/clnkrapp"
	"github.com/clnkr-ai/clnkr/cmd/internal/providerconfig"
	"github.com/clnkr-ai/clnkr/cmd/internal/session"
)

// version is set at build time via -ldflags.
var version = "dev"

func usageText() string {
	return `clnkr - a minimal coding agent

Usage:
  clnkr                     Start conversational mode

Options:
  -p, --prompt string       Task to run unattended and exit
      --prompt-mode-unattended string
                            Long alias for -p/--prompt
      --max-steps int       Limit executed commands
                            before summary (default: 100)
      --full-send           Execute every act batch without approval
                            (implied by -p)
  -v, --verbose             Show internal decisions

Sessions:
  -c, --continue            Resume most recent session for this project
  -l, --list-sessions       List saved sessions for this project

Debugging:
      --load-messages string   Seed conversation from a JSON file
      --event-log string       Stream JSONL events to file during execution
      --trajectory string      Save single-task history as JSON on exit

Short aliases:
  -m, -u, -S                Aliases for --model, --base-url, --no-system-prompt
  -V, --version             Print version and exit

` + clnkrapp.ProviderOptionsUsage + `
` + clnkrapp.SystemPromptUsage + `
` + clnkrapp.EnvironmentUsage + `
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

func promptModeMarker(args []string, i int) bool {
	if i > 0 && valueFlag(args[i-1]) {
		return false
	}
	return args[i] == "-p" || args[i] == "--prompt" || args[i] == "--prompt-mode-unattended"
}

func dumpPromptMarker(args []string, i int) bool {
	if i > 0 && valueFlag(args[i-1]) {
		return false
	}
	return args[i] == "--dump-system-prompt"
}

func valueFlag(arg string) bool {
	switch arg {
	case "-p", "--prompt", "--prompt-mode-unattended", "-m", "--model", "-u", "--base-url", "--provider", "--provider-api", "--act-protocol", "--effort", "--thinking-budget-tokens", "--max-output-tokens", "--max-steps", "--event-log", "--trajectory", "--load-messages", "--system-prompt-append":
		return true
	}
	return false
}

func isTerminal(fd uintptr) bool {
	var winsize [4]uint16
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, fd, syscall.TIOCGWINSZ, uintptr(unsafe.Pointer(&winsize)))
	return errno == 0
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

func runDriverPrompt(ctx context.Context, driver *clnkrapp.Driver, reader *lineReader, input string, mode string) error {
	promptCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- driver.Prompt(promptCtx, input, mode)
	}()

	var pendingErr error
	for {
		select {
		case event := <-driver.Events():
			var err error
			pendingErr, err = handleTerminalDriverEvent(promptCtx, driver, reader, event, pendingErr)
			if err != nil {
				cancel()
				return err
			}
		case err := <-errCh:
			return drainDriverEvents(promptCtx, driver, reader, pendingErr, err)
		case <-ctx.Done():
			cancel()
			return ctx.Err()
		}
	}
}

func drainDriverEvents(ctx context.Context, driver *clnkrapp.Driver, reader *lineReader, pendingErr error, runErr error) error {
	for {
		select {
		case event := <-driver.Events():
			var err error
			pendingErr, err = handleTerminalDriverEvent(ctx, driver, reader, event, pendingErr)
			if err != nil && runErr == nil {
				runErr = err
			}
		default:
			return runErr
		}
	}
}

func handleTerminalDriverEvent(ctx context.Context, driver *clnkrapp.Driver, reader *lineReader, event clnkrapp.DriverEvent, pendingErr error) (error, error) {
	if eventErr, ok := event.(clnkrapp.EventError); ok {
		return eventErr.Err, nil
	}
	if pendingErr != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", pendingErr) //nolint:errcheck
	}

	switch event := event.(type) {
	case clnkrapp.EventApprovalRequest:
		return nil, replyToTerminalRequest(ctx, driver, reader, event.Prompt, "Send 'y' to approve, or type what the agent should do instead: ")
	case clnkrapp.EventClarificationRequest:
		return nil, replyToTerminalRequest(ctx, driver, reader, event.Question, "Clarify: ")
	case clnkrapp.EventCompacted:
		fmt.Fprintf(os.Stderr, "[Session compacted: %d messages summarized, %d kept]\n", event.Stats.CompactedMessages, event.Stats.KeptMessages) //nolint:errcheck
	case clnkrapp.EventDone:
	default:
		return nil, fmt.Errorf("unhandled driver event %T", event)
	}
	return nil, nil
}

func replyToTerminalRequest(ctx context.Context, driver *clnkrapp.Driver, reader *lineReader, text, prompt string) error {
	fmt.Fprintln(os.Stderr, text) //nolint:errcheck
	fmt.Fprint(os.Stderr, prompt) //nolint:errcheck
	reply, err := reader.ReadLine(ctx)
	if err != nil {
		return err
	}
	return driver.Reply(ctx, strings.TrimSpace(reply))
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "Error: "+format+"\n", args...)
	os.Exit(1)
}

func exitRunErr(err error) {
	switch {
	case err == nil:
		return
	case errors.Is(err, context.Canceled):
		os.Exit(130)
	case errors.Is(err, clnkr.ErrClarificationNeeded):
		fmt.Fprintln(os.Stderr, "Clarification needed.")
		os.Exit(2)
	default:
		fatalf("%v", err)
	}
}

func main() {
	flags := flag.NewFlagSet("clnkr", flag.ContinueOnError)
	flags.Usage = func() {}
	flags.SetOutput(io.Discard)

	taskPromptFlag := flags.String("p", "", "")
	promptLong := flags.String("prompt", "", "")
	promptModeUnattended := flags.String("prompt-mode-unattended", "", "")
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

	args, dumpUnattendedPrompt := os.Args[1:], false
	if n := len(args); n >= 2 && dumpPromptMarker(args, n-2) && promptModeMarker(args, n-1) {
		args, dumpUnattendedPrompt = args[:n-1], true
	}
	for i := 0; i+1 < len(args); i++ {
		if promptModeMarker(args, i) && args[i+1] == "--dump-system-prompt" {
			fatalf("%s requires a task. To dump the unattended prompt, use: clnkr --dump-system-prompt %s", args[i], args[i])
		}
	}
	if err := flags.Parse(args); err != nil {
		if err == flag.ErrHelp {
			fmt.Fprint(os.Stdout, usageText()) //nolint:errcheck
			os.Exit(0)
		}
		fmt.Fprintf(os.Stderr, "Error: %v\nSee clnkr(1) for available options.\n", err)
		os.Exit(1)
	}
	maxOutputTokensSet, thinkingBudgetTokensSet := clnkrapp.RequestOptionFlagsSet(flags)

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

	taskPrompt := aliasedString(*promptModeUnattended, aliasedString(*promptLong, *taskPromptFlag))
	singleTask := taskPrompt != "" || dumpUnattendedPrompt
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
		Unattended:         singleTask,
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
		Provider: *providerFlag, ProviderAPI: *providerAPIFlag,
		Model: aliasedString(*modelShort, *modelFlag), BaseURL: aliasedString(*baseURLShort, *baseURLFlag),
		ActProtocol:    actProtocol,
		RequestOptions: clnkrapp.RequestOptions(*effortFlag, *maxOutputTokens, maxOutputTokensSet, *thinkingBudgetTokens, thinkingBudgetTokensSet),
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

	agent := clnkr.NewAgent(clnkrapp.NewModelForConfigWithOptions(cfg, systemPrompt, clnkrapp.ModelOptions{Unattended: singleTask}), &clnkr.CommandExecutor{}, cwd)
	agent.ActProtocol = cfg.ActProtocol

	showDebug := *verbose || *verboseShort
	agent.Notify = func(e clnkr.Event) {
		switch e := e.(type) {
		case clnkr.EventResponse:
			switch turn := e.Turn.(type) {
			case *clnkr.DoneTurn:
				fmt.Fprintln(os.Stdout, turn.Summary) //nolint:errcheck
			case *clnkr.ClarifyTurn:
				if *fullSend && !singleTask {
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
	agent.Notify(clnkrapp.RunMetadataDebugEvent(runMetadata))

	if *maxSteps > 0 {
		agent.MaxSteps = *maxSteps
	}

	if *loadMessages != "" {
		if err := clnkrapp.AddMessagesFile(agent, *loadMessages); err != nil {
			fatalf("%v", err)
		}
	}

	if *continueFlag || *continueShort {
		count, ok, err := clnkrapp.ResumeLatestSession(agent, cwd)
		if err != nil {
			fatalf("%v", err)
		}
		if !ok {
			fmt.Fprintf(os.Stderr, "Error: no session found for this project.\nRun 'clnkr --list-sessions' to see available sessions.\n")
			os.Exit(1)
		}
		fmt.Fprintf(os.Stderr, "[Resumed session with %d messages]\n", count)
	}

	driver := clnkrapp.NewDriver(agent, clnkrapp.MakeCompactorFactory(cfg))

	if singleTask {
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
		defer stop()
		if err := clnkrapp.RejectCompactCommand(taskPrompt); err != nil {
			fatalf("%v", err)
		}
		runErr := runDriverPrompt(ctx, driver, newLineReader(strings.NewReader("")), taskPrompt, clnkrapp.PromptModeFullSend)
		if *trajectory != "" {
			if err := clnkrapp.WriteTrajectory(*trajectory, agent.Messages()); err != nil {
				fmt.Fprintf(os.Stderr, "Error: %v\n", err)
				if runErr != nil {
					fmt.Fprintf(os.Stderr, "Error: %v\n", runErr)
				}
				os.Exit(1)
			}
		}
		if errors.Is(runErr, clnkr.ErrClarificationNeeded) {
			fmt.Fprintln(os.Stderr, "clarify not allowed in unattended mode")
			os.Exit(2)
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
	mode := clnkrapp.PromptModeApproval
	if *fullSend {
		mode = clnkrapp.PromptModeFullSend
	}
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
		if err := runDriverPrompt(ctx, driver, reader, input, mode); err != nil {
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
		} else if dir, err := session.SessionDir(cwd); err == nil {
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
