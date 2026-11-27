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
  clnkr -p task             Run an unattended one-shot task

Options:
  -p, --prompt string       Task to run unattended and exit
      --max-steps int       Limit executed commands
                            before summary (default: 100)
      --full-send           Execute every act batch without approval
                            (implied by -p)
  -v, --verbose             Show internal decisions

Interactive prompt:
  Up/Down or Ctrl-P/N       Recall prompts from this directory
  Ctrl-G                    Edit the draft in $EDITOR

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

func ioctl(fd, request uintptr, value unsafe.Pointer) syscall.Errno {
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, fd, request, uintptr(value))
	return errno
}

func terminalSize(fd uintptr) (int, bool) {
	var size [4]uint16
	errno := ioctl(fd, syscall.TIOCGWINSZ, unsafe.Pointer(&size))
	return int(size[1]), errno == 0
}

func isTerminal(fd uintptr) bool {
	_, ok := terminalSize(fd)
	return ok
}

type lineResult struct {
	text string
	err  error
}

type terminalInputError struct{ err error }

func (e *terminalInputError) Error() string { return e.err.Error() }
func (e *terminalInputError) Unwrap() error { return e.err }

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
			lr.lines <- lineResult{err: &terminalInputError{err: err}}
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
		return line.text, line.err
	}
}

type replyReader interface {
	ReadLine(context.Context) (string, error)
}

type promptReader interface {
	replyReader
	ReadPrompt(bool) (string, error)
}

func (r *lineReader) ReadPrompt(show bool) (string, error) {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if show {
		_, _ = fmt.Fprint(os.Stderr, "clnkr> ")
	}
	first, err := r.ReadLine(ctx)
	if err == io.EOF {
		return "", io.EOF
	}
	if err != nil {
		return "", fmt.Errorf("read prompt: %w", err)
	}
	lines := []string{first}
	for delay := 10 * time.Millisecond; ; delay = time.Millisecond {
		burst, cancel := context.WithTimeout(ctx, delay)
		line, err := r.ReadLine(burst)
		cancel()
		switch err {
		case nil:
			lines = append(lines, line)
		case context.DeadlineExceeded, io.EOF:
			return strings.Join(lines, "\n"), nil
		default:
			return "", fmt.Errorf("read queued prompt lines: %w", err)
		}
	}
}

func runDriverPrompt(
	ctx context.Context,
	driver *clnkrapp.Driver,
	reader replyReader,
	input string,
	mode string,
	eventLog io.Writer,
	stopModelWait func(),
) (runErr error) {
	writeProgramStatus("working", input)
	defer func() {
		stopModelWait()
		switch {
		case errors.Is(runErr, context.Canceled):
			writeProgramStatus("idle", "Cancelled")
		case runErr == nil:
			writeProgramStatus("done", "Run finished")
		// Clarification is reported where waiting versus exit is known.
		case !errors.Is(runErr, clnkr.ErrClarificationNeeded):
			writeProgramStatus("error", runErr.Error())
		}
	}()

	promptCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- driver.Prompt(promptCtx, input, mode)
	}()

	terminal := terminalRun{
		driver:   driver,
		reader:   reader,
		eventLog: eventLog,
		stopWait: stopModelWait,
	}
	for {
		select {
		case event := <-driver.Events():
			if err := terminal.handle(promptCtx, event); err != nil {
				return err
			}
		case err := <-errCh:
			return terminal.drain(promptCtx, err)
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

type terminalRun struct {
	driver     *clnkrapp.Driver
	reader     replyReader
	eventLog   io.Writer
	stopWait   func()
	pendingErr error
}

func (r *terminalRun) drain(ctx context.Context, runErr error) error {
	for {
		select {
		case event := <-r.driver.Events():
			if err := r.handle(
				ctx,
				event,
			); err != nil &&
				(runErr == nil || isTerminalInputError(err)) {
				runErr = err
			}
		default:
			return runErr
		}
	}
}

func (r *terminalRun) handle(ctx context.Context, event clnkrapp.DriverEvent) error {
	r.stopWait()
	if eventErr, ok := event.(clnkrapp.EventError); ok {
		r.pendingErr = eventErr.Err
		return nil
	}
	if r.eventLog != nil {
		_ = clnkrapp.WriteJSONL(r.eventLog, event)
	}
	if r.pendingErr != nil {
		_, _ = fmt.Fprintln(os.Stderr, "Error:", r.pendingErr)
		r.pendingErr = nil
	}

	switch event := event.(type) {
	case clnkrapp.EventApprovalRequest:
		writeProgramStatus("blocked:kind=permission", event.Prompt)
		return r.reply(
			ctx,
			event.Prompt,
			"Send 'y' to approve, or type what the agent should do instead: ",
		)
	case clnkrapp.EventClarificationRequest:
		writeProgramStatus("blocked:kind=question", event.Question)
		return r.reply(ctx, event.Question, "Clarify: ")
	case clnkrapp.EventCompacted:
		fmt.Fprintf(
			os.Stderr,
			"[Session compacted: %d messages summarized, %d kept]\n",
			event.Stats.CompactedMessages,
			event.Stats.KeptMessages,
		) //nolint:errcheck
	case clnkrapp.EventDone:
	default:
		return fmt.Errorf("unhandled driver event %T", event)
	}
	return nil
}

func (r *terminalRun) reply(ctx context.Context, text, prompt string) error {
	fmt.Fprintln(os.Stderr, text) //nolint:errcheck
	for {
		fmt.Fprint(os.Stderr, prompt) //nolint:errcheck
		reply, err := r.reader.ReadLine(ctx)
		if err == io.EOF {
			err = context.Canceled
		}
		if err != nil {
			return fmt.Errorf("read terminal reply: %w", err)
		}
		reply = strings.TrimSpace(reply)
		if reply == "" {
			continue
		}
		err = r.driver.Reply(ctx, reply)
		if err == nil && clnkrapp.RejectCompactCommand(reply) == nil {
			writeProgramStatus("working", "Continuing")
		}
		return err
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

func exitRunErr(err error, clarificationMessage string) {
	switch {
	case errors.Is(err, context.Canceled):
		os.Exit(130)
	case errors.Is(err, clnkr.ErrClarificationNeeded):
		writeProgramStatus("error", err.Error())
		fmt.Fprintln(os.Stderr, clarificationMessage)
		os.Exit(2)
	case err != nil:
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
			writeProgramStatus("error", err.Error())
			_, _ = fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			saveSessionIfMessages(agent, opts.cwd, opts.runMetadata, false)
			if runErr != nil {
				_, _ = fmt.Fprintf(os.Stderr, "Error: %v\n", runErr)
			}
			os.Exit(1)
		}
	}
	saveSessionIfMessages(agent, opts.cwd, opts.runMetadata, false)
	exitRunErr(runErr, "clarify not allowed in unattended mode")
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
	reader := interactiveInput(cwd, showPrompt && isTerminal(os.Stderr.Fd()))
	mode := clnkrapp.PromptModeApproval
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
	exitRunErr(loopErr, "Clarification needed.")
}

func runPromptLoop(
	driver *clnkrapp.Driver,
	reader promptReader,
	modelWait *modelWaitIndicator,
	showPrompt bool,
	mode string,
	eventLog io.Writer,
) (loopErr error) {
	var promptErr error
	defer func() {
		if showPrompt && errors.Is(promptErr, clnkr.ErrClarificationNeeded) {
			if loopErr == nil || errors.Is(loopErr, context.Canceled) {
				writeProgramStatus("idle", "Cancelled")
			} else {
				writeProgramStatus("error", loopErr.Error())
			}
		}
	}()
	for {
		input, err := reader.ReadPrompt(showPrompt)
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		input = strings.TrimSpace(input)
		if input == "" {
			continue
		}
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
		promptErr = runDriverPrompt(
			ctx,
			driver,
			reader,
			input,
			mode,
			eventLog,
			modelWait.Stop,
		)
		stop()
		switch {
		case promptErr == nil:
		case !showPrompt || isTerminalInputError(promptErr):
			return promptErr
		case errors.Is(promptErr, clnkr.ErrClarificationNeeded):
			writeProgramStatus("blocked:kind=question", "Clarification needed")
		case errors.Is(promptErr, io.EOF), errors.Is(promptErr, context.Canceled):
		default:
			_, _ = fmt.Fprintf(os.Stderr, "Error: %v\n", promptErr)
		}
	}
}

func isTerminalInputError(err error) bool {
	var inputErr *terminalInputError
	return errors.As(err, &inputErr)
}

func summarizeCommand(cmd string) string {
	if idx := strings.IndexByte(cmd, '\n'); idx >= 0 {
		return fmt.Sprintf("%s ... (%d lines)", cmd[:idx], strings.Count(cmd, "\n")+1)
	}
	return cmd
}
