package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"github.com/clnkr-ai/clnkr"
	"github.com/clnkr-ai/clnkr/cmd/internal/clnkrapp"
)

// version is set at build time via -ldflags.
var version = "dev"

type cliOptions struct {
	taskPrompt, model, baseURL, provider, providerAPI, actProtocol, effort string
	eventLog, trajectory, loadMessages, systemPromptAppend                 string
	thinkingBudgetTokens, maxOutputTokens, maxSteps                        int
	fullSend, verbose, showVersion, continueSession, listSessions          bool
	noSystemPrompt, dumpSystemPrompt, singleTask                           bool
	actProtocolSet, maxOutputTokensSet, thinkingBudgetSet                  bool
}

var usageText = `clnkr - a minimal coding agent

Usage:
  clnkr                     Start conversational mode
  Models may run clnkrd through bash for bounded JSONL work.

Options:
  -p, --prompt string       Task to run unattended and exit
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
  -m, -S                   Aliases for --model, --no-system-prompt
  -V, --version             Print version and exit

` + clnkrapp.ProviderOptionsUsage + `
` + clnkrapp.SystemPromptUsage + `
` + clnkrapp.EnvironmentUsage + `
Defaults:
  anthropic base URL  https://api.anthropic.com
  openai base URL     https://api.openai.com/v1
`

func isTerminal(fd uintptr) bool {
	var winsize [4]uint16
	_, _, errno := syscall.Syscall(
		syscall.SYS_IOCTL,
		fd,
		syscall.TIOCGWINSZ,
		uintptr(unsafe.Pointer(&winsize)),
	)
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
	lr := &lineReader{lines: make(chan lineResult, 1024)}
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

func (r *lineReader) ReadQueuedLines() ([]string, error) {
	timer := time.NewTimer(10 * time.Millisecond)
	defer timer.Stop()

	var lines []string
	for {
		select {
		case line, ok := <-r.lines:
			if !ok {
				return lines, nil
			}
			if line.err != nil {
				return lines, line.err
			}
			lines = append(lines, line.text)
			if !timer.Stop() {
				<-timer.C
			}
			timer.Reset(time.Millisecond)
		case <-timer.C:
			return lines, nil
		}
	}
}

func runDriverPrompt(
	ctx context.Context,
	driver *clnkrapp.Driver,
	reader *lineReader,
	input string,
	mode string,
	eventLog io.Writer,
	stopModelWait func(),
) error {
	defer stopModelWait()

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
			pendingErr, err = handleTerminalDriverEvent(
				promptCtx,
				driver,
				reader,
				event,
				pendingErr,
				eventLog,
				stopModelWait,
			)
			if err != nil {
				cancel()
				return err
			}
		case err := <-errCh:
			return drainDriverEvents(
				promptCtx,
				driver,
				reader,
				pendingErr,
				err,
				eventLog,
				stopModelWait,
			)
		case <-ctx.Done():
			cancel()
			return ctx.Err()
		}
	}
}

func drainDriverEvents(
	ctx context.Context,
	driver *clnkrapp.Driver,
	reader *lineReader,
	pendingErr error,
	runErr error,
	eventLog io.Writer,
	stopModelWait func(),
) error {
	for {
		select {
		case event := <-driver.Events():
			var err error
			pendingErr, err = handleTerminalDriverEvent(
				ctx,
				driver,
				reader,
				event,
				pendingErr,
				eventLog,
				stopModelWait,
			)
			if err != nil && runErr == nil {
				runErr = err
			}
		default:
			return runErr
		}
	}
}

func handleTerminalDriverEvent(
	ctx context.Context,
	driver *clnkrapp.Driver,
	reader *lineReader,
	event clnkrapp.DriverEvent,
	pendingErr error,
	eventLog io.Writer,
	stopModelWait func(),
) (error, error) {
	stopModelWait()
	if eventErr, ok := event.(clnkrapp.EventError); ok {
		return eventErr.Err, nil
	}
	if eventLog != nil {
		_ = clnkrapp.WriteJSONL(eventLog, event)
	}
	if pendingErr != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", pendingErr) //nolint:errcheck
	}

	switch event := event.(type) {
	case clnkrapp.EventApprovalRequest:
		return nil, replyToTerminalRequest(
			ctx,
			driver,
			reader,
			event.Prompt,
			"Send 'y' to approve, or type what the agent should do instead: ",
		)
	case clnkrapp.EventClarificationRequest:
		return nil, replyToTerminalRequest(ctx, driver, reader, event.Question, "Clarify: ")
	case clnkrapp.EventCompacted:
		fmt.Fprintf(
			os.Stderr,
			"[Session compacted: %d messages summarized, %d kept]\n",
			event.Stats.CompactedMessages,
			event.Stats.KeptMessages,
		) //nolint:errcheck
	case clnkrapp.EventDone:
	default:
		return nil, fmt.Errorf("unhandled driver event %T", event)
	}
	return nil, nil
}

func replyToTerminalRequest(
	ctx context.Context,
	driver *clnkrapp.Driver,
	reader *lineReader,
	text, prompt string,
) error {
	fmt.Fprintln(os.Stderr, text) //nolint:errcheck
	for {
		fmt.Fprint(os.Stderr, prompt) //nolint:errcheck
		reply, err := reader.ReadLine(ctx)
		if err != nil {
			return err
		}
		reply = strings.TrimSpace(reply)
		if reply == "" {
			continue
		}
		return driver.Reply(ctx, reply)
	}
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "Error: "+format+"\n", args...)
	os.Exit(1)
}

func fatalIfErr(err error) { fatalWhen(err != nil, "%v", err) }

func fatalWhen(cond bool, format string, args ...any) {
	if cond {
		fatalf(format, args...)
	}
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
	opts := parseCLIOptions(os.Args[1:], usageText)

	if opts.showVersion {
		fmt.Printf("clnkr %s\n", version)
		os.Exit(0)
	}

	cwd, err := os.Getwd()
	if err != nil {
		fatalf("cannot get working directory: %v", err)
	}

	if opts.listSessions {
		sessions, err := clnkrapp.ListSessions(cwd)
		if err != nil {
			fatalf("cannot list sessions: %v", err)
		}
		if len(sessions) == 0 {
			_, _ = fmt.Fprintln(os.Stdout, "No sessions found for this project.")
			os.Exit(0)
		}
		_, _ = fmt.Fprintln(os.Stdout, "Saved sessions:")
		for i, s := range sessions {
			_, _ = fmt.Fprintf(
				os.Stdout,
				"  %d. %s (%d messages) - %s\n",
				i+1,
				s.Filename,
				s.Messages,
				s.Created.Format("2006-01-02 15:04:05"),
			)
		}
		os.Exit(0)
	}

	if opts.continueSession && opts.trajectory != "" {
		fatalf("--continue and --trajectory are mutually exclusive")
	}
	if opts.trajectory != "" && !opts.singleTask {
		fatalf("--trajectory requires -p (single-task mode)")
	}

	startupInputs := clnkrapp.StartupInputs{
		CWD:                     cwd,
		Version:                 version,
		Env:                     os.Getenv,
		Environ:                 os.Environ(),
		Provider:                opts.provider,
		ProviderAPI:             opts.providerAPI,
		Model:                   opts.model,
		BaseURL:                 opts.baseURL,
		ActProtocol:             opts.actProtocol,
		ActProtocolSet:          opts.actProtocolSet,
		Effort:                  opts.effort,
		MaxOutputTokens:         opts.maxOutputTokens,
		MaxOutputTokensSet:      opts.maxOutputTokensSet,
		ThinkingBudgetTokens:    opts.thinkingBudgetTokens,
		ThinkingBudgetTokensSet: opts.thinkingBudgetSet,
		OmitSystemPrompt:        opts.noSystemPrompt,
		SystemPromptAppend:      opts.systemPromptAppend,
		DumpSystemPrompt:        opts.dumpSystemPrompt,
		Unattended:              opts.singleTask,
	}
	if opts.dumpSystemPrompt {
		systemPrompt, err := clnkrapp.LoadStartupPrompt(startupInputs)
		if err != nil {
			fatalf("%v", err)
		}
		_, _ = fmt.Fprint(os.Stdout, systemPrompt)
		os.Exit(0)
	}

	startup, err := clnkrapp.PrepareStartup(startupInputs)
	if err != nil {
		if clnkrapp.IsMissingAPIKey(err) {
			_, _ = fmt.Fprintln(
				os.Stderr,
				"Error: No API key found.\nSet it with: export CLNKR_API_KEY=your-api-key",
			)
			os.Exit(1)
		}
		fatalf("%v", err)
	}
	runMetadata := startup.Metadata
	agent := startup.Agent

	var eventLogFile *os.File
	if opts.eventLog != "" {
		eventLogFile, err = os.OpenFile(opts.eventLog, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			fatalf("cannot open event log %q: %v", opts.eventLog, err)
		}
		defer eventLogFile.Close() //nolint:errcheck
	}

	var modelWait *modelWaitIndicator
	installAgentNotify(agent, &modelWait, notifyOptions{
		cwd:        cwd,
		fullSend:   opts.fullSend,
		singleTask: opts.singleTask,
		verbose:    opts.verbose,
		eventLog:   eventLogFile,
	})
	agent.Notify(clnkrapp.RunMetadataDebugEvent(runMetadata))

	if opts.maxSteps > 0 {
		agent.MaxSteps = opts.maxSteps
	}

	if opts.loadMessages != "" {
		if err := clnkrapp.AddMessagesFile(agent, opts.loadMessages); err != nil {
			fatalf("%v", err)
		}
	}

	if opts.continueSession {
		count, ok, err := clnkrapp.ResumeLatestSession(agent, cwd)
		if err != nil {
			fatalf("%v", err)
		}
		if !ok {
			_, _ = fmt.Fprintf(
				os.Stderr,
				"Error: no session found for this project.\nRun 'clnkr --list-sessions' to see available sessions.\n",
			)
			os.Exit(1)
		}
		_, _ = fmt.Fprintf(os.Stderr, "[Resumed session with %d messages]\n", count)
	}

	driver := startup.Driver
	singleTaskOpts := singleTaskRunOptions{
		taskPrompt:  opts.taskPrompt,
		trajectory:  opts.trajectory,
		cwd:         cwd,
		runMetadata: runMetadata,
	}
	replOpts := replRunOptions{fullSend: opts.fullSend, verbose: opts.verbose}

	if opts.singleTask {
		runSingleTask(agent, driver, singleTaskOpts, eventLogFile)
		return
	}

	runREPL(agent, driver, &modelWait, cwd, runMetadata, replOpts, eventLogFile)
}

type singleTaskRunOptions struct {
	taskPrompt, trajectory, cwd string
	runMetadata                 clnkrapp.RunMetadata
}

type replRunOptions struct {
	fullSend bool
	verbose  bool
}

type notifyOptions struct {
	cwd        string
	fullSend   bool
	singleTask bool
	verbose    bool
	eventLog   io.Writer
}

func installAgentNotify(agent *clnkr.Agent, modelWait **modelWaitIndicator, opts notifyOptions) {
	agent.Notify = func(e clnkr.Event) {
		updateModelWaitForAgentEvent(*modelWait, e)
		switch e := e.(type) {
		case clnkr.EventResponse:
			handleResponseEvent(e, opts)
		case clnkr.EventCommandStart:
			command := summarizeCommand(e.Command)
			if e.Dir != "" && e.Dir != opts.cwd {
				command += " in " + e.Dir
			}
			_, _ = fmt.Fprintf(os.Stderr, "--- running: %s ---\n", command)
		case clnkr.EventCommandDone:
			writeCommandResult(e)
		case clnkr.EventProtocolFailure:
			if opts.verbose {
				_, _ = fmt.Fprintf(os.Stderr, "[clnkr] protocol error: %s\n", e.Reason)
			}
		case clnkr.EventDebug:
			if e.Message == clnkr.ContextLengthBackstopCompactingDebug {
				_, _ = fmt.Fprintln(
					os.Stderr,
					"[Context limit reached; compacting session and retrying once]",
				)
			}
			if opts.verbose {
				_, _ = fmt.Fprintf(os.Stderr, "[clnkr] %s\n", e.Message)
			}
		}
		if opts.eventLog != nil {
			_ = clnkrapp.WriteEventLog(opts.eventLog, e)
		}
	}
}

func handleResponseEvent(e clnkr.EventResponse, opts notifyOptions) {
	switch turn := e.Turn.(type) {
	case *clnkr.DoneTurn:
		_, _ = fmt.Fprintln(os.Stdout, turn.Summary)
	case *clnkr.ClarifyTurn:
		if opts.fullSend && !opts.singleTask {
			_, _ = fmt.Fprintln(os.Stderr, turn.Question)
		}
	default:
		if opts.verbose {
			if text, err := clnkr.CanonicalTurnJSON(e.Turn); err == nil {
				_, _ = fmt.Fprintln(os.Stderr, text)
			}
		}
	}
}

func writeCommandResult(e clnkr.EventCommandDone) {
	_, _ = fmt.Fprint(os.Stdout, e.Stdout)
	if e.Stdout != "" && !strings.HasSuffix(e.Stdout, "\n") {
		_, _ = fmt.Fprintln(os.Stdout)
	}
	_, _ = fmt.Fprint(os.Stderr, e.Stderr)
	if e.Stderr != "" && !strings.HasSuffix(e.Stderr, "\n") {
		_, _ = fmt.Fprintln(os.Stderr)
	}
	_, _ = fmt.Fprintln(os.Stderr, "--- done ---")
}

func runSingleTask(
	agent *clnkr.Agent,
	driver *clnkrapp.Driver,
	opts singleTaskRunOptions,
	eventLog io.Writer,
) {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	fatalIfErr(clnkrapp.RejectCompactCommand(opts.taskPrompt))
	runErr := runDriverPrompt(
		ctx,
		driver,
		newLineReader(strings.NewReader("")),
		opts.taskPrompt,
		clnkrapp.PromptModeFullSend,
		eventLog,
		func() {},
	)
	if opts.trajectory != "" {
		if err := clnkrapp.WriteTrajectory(opts.trajectory, agent.Messages()); err != nil {
			_, _ = fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			saveSessionIfMessages(agent, opts.cwd, opts.runMetadata, false)
			if runErr != nil {
				_, _ = fmt.Fprintf(os.Stderr, "Error: %v\n", runErr)
			}
			os.Exit(1)
		}
	}
	saveSessionIfMessages(agent, opts.cwd, opts.runMetadata, false)
	if errors.Is(runErr, clnkr.ErrClarificationNeeded) {
		_, _ = fmt.Fprintln(os.Stderr, "clarify not allowed in unattended mode")
		os.Exit(2)
	}
	exitRunErr(runErr)
}

func saveSessionIfMessages(
	agent *clnkr.Agent,
	cwd string,
	runMetadata clnkrapp.RunMetadata,
	notice bool,
) {
	if msgs := agent.Messages(); len(msgs) > 0 {
		if dir, err := clnkrapp.SaveSession(cwd, msgs, runMetadata); err != nil {
			_, _ = fmt.Fprintf(os.Stderr, "Warning: could not save session: %v\n", err)
		} else if notice {
			_, _ = fmt.Fprintf(os.Stderr, "[Session saved to %s]\n", dir)
		}
	}
}

func runREPL(
	agent *clnkr.Agent,
	driver *clnkrapp.Driver,
	modelWait **modelWaitIndicator,
	cwd string,
	runMetadata clnkrapp.RunMetadata,
	opts replRunOptions,
	eventLog io.Writer,
) {
	showPrompt := isTerminal(os.Stdin.Fd())
	fatalWhen(
		!opts.fullSend && !showPrompt,
		"approval mode requires interactive stdin; pass --full-send=true to bypass approval",
	)
	reader, mode := newLineReader(os.Stdin), clnkrapp.PromptModeApproval
	if opts.fullSend {
		mode = clnkrapp.PromptModeFullSend
	}
	if showPrompt && !opts.verbose && isTerminal(os.Stderr.Fd()) {
		*modelWait = &modelWaitIndicator{
			out:   os.Stderr,
			delay: time.Second,
			tick:  250 * time.Millisecond,
			now:   time.Now,
		}
	}
	loopErr := runPromptLoop(driver, reader, *modelWait, showPrompt, mode, eventLog)
	saveSessionIfMessages(agent, cwd, runMetadata, true)
	exitRunErr(loopErr)
}

func runPromptLoop(
	driver *clnkrapp.Driver,
	reader *lineReader,
	modelWait *modelWaitIndicator,
	showPrompt bool,
	mode string,
	eventLog io.Writer,
) error {
	for {
		if showPrompt {
			_, _ = fmt.Fprint(os.Stderr, "clnkr> ")
		}
		input, err := reader.ReadLine(context.Background())
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			fatalf("%v", err)
		}
		if lines, err := reader.ReadQueuedLines(); err != nil {
			fatalf("%v", err)
		} else if len(lines) > 0 {
			input = strings.Join(append([]string{input}, lines...), "\n")
		}
		input = strings.TrimSpace(input)
		if input == "" {
			continue
		}
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
		if err := runDriverPrompt(
			ctx,
			driver,
			reader,
			input,
			mode,
			eventLog,
			modelWait.Stop,
		); err != nil {
			if !showPrompt {
				stop()
				return err
			}
			if errors.Is(err, clnkr.ErrClarificationNeeded) || errors.Is(err, io.EOF) ||
				errors.Is(err, context.Canceled) {
				stop()
				continue
			}
			_, _ = fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		}
		stop()
	}
	return nil
}

func summarizeCommand(cmd string) string {
	if idx := strings.IndexByte(cmd, '\n'); idx >= 0 {
		return fmt.Sprintf("%s ... (%d lines)", cmd[:idx], strings.Count(cmd, "\n")+1)
	}
	return cmd
}
