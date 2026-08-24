package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"sync"

	"github.com/clnkr-ai/clnkr"
	"github.com/clnkr-ai/clnkr/cmd/internal/clnkrapp"
)

// version is set at build time via -ldflags.
var version = "dev"

func usageText() string {
	return `clnkrd - stdio JSONL adapter for clnkr

Machine-facing stdio JSONL for tools, editors, wrappers, evals, automation,
and agents.

Usage:
  clnkrd [options]          Read JSONL commands on stdin, emit events on stdout

JSONL commands:
  prompt                    Start a run: text + mode approval|full_send
  reply                     Answer approval_request or clarify
  compact                   Compact transcript history
  shutdown                  Cancel active run, drain events, exit 0

JSONL events:
  debug response protocol_failure approval_request clarify command_start
  command_done completion_gate compacted done error

Options:
      --max-steps int       Limit executed commands before summary
      --continue            Resume most recent session for this project
      --load-messages file  Seed conversation from a JSON file
      --event-log file      Copy JSONL events to a file
      --version             Print version and exit

` + clnkrapp.ProviderOptionsUsage + `
` + clnkrapp.SystemPromptUsage + `
` + clnkrapp.EnvironmentUsage + `
Examples:
  printf '%s\n' '{"type":"prompt","text":"inspect","mode":"approval"}' |
    clnkrd --model "$CLNKR_MODEL" --provider anthropic
`
}

func main() {
	os.Exit(runMain(os.Args[1:], os.Stdin, os.Stdout, os.Stderr, os.Getenv, os.Environ))
}

func runMain(
	args []string,
	in io.Reader,
	out io.Writer,
	errOut io.Writer,
	env func(string) string,
	environ func() []string,
) int {
	flags := flag.NewFlagSet("clnkrd", flag.ContinueOnError)
	flags.Usage = func() {}
	flags.SetOutput(io.Discard)
	fail := func(format string, args ...any) int {
		fmt.Fprintf(errOut, "Error: "+format+"\n", args...) //nolint:errcheck
		return 1
	}

	modelFlag := flags.String("model", "", "")
	baseURLFlag := flags.String("base-url", "", "")
	providerFlag := flags.String("provider", "", "")
	providerAPIFlag := flags.String("provider-api", "", "")
	actProtocolFlag := flags.String("act-protocol", "auto", "")
	effortFlag := flags.String("effort", "", "")
	maxOutputTokens := flags.Int("max-output-tokens", 0, "")
	thinkingBudgetTokens := flags.Int("thinking-budget-tokens", 0, "")
	maxSteps := flags.Int("max-steps", 0, "")
	noSystemPrompt := flags.Bool("no-system-prompt", false, "")
	dumpSystemPrompt := flags.Bool("dump-system-prompt", false, "")
	systemPromptAppend := flags.String("system-prompt-append", "", "")
	loadMessages := flags.String("load-messages", "", "")
	continueFlag := flags.Bool("continue", false, "")
	showVersion := flags.Bool("version", false, "")
	eventLog := flags.String("event-log", "", "")

	if err := flags.Parse(args); err != nil {
		if err == flag.ErrHelp {
			fmt.Fprint(out, usageText()) //nolint:errcheck
			return 0
		}
		return fail("%v\nSee clnkrd(1) for available options.", err)
	}
	maxOutputTokensSet, thinkingBudgetTokensSet := requestOptionFlagsSet(flags)

	if *showVersion {
		fmt.Fprintf(out, "clnkrd %s\n", version) //nolint:errcheck
		return 0
	}

	cwd, err := os.Getwd()
	if err != nil {
		return fail("cannot get working directory: %v", err)
	}

	startupInputs := clnkrapp.StartupInputs{
		CWD:                     cwd,
		Version:                 version,
		Env:                     env,
		Environ:                 environ(),
		Provider:                *providerFlag,
		ProviderAPI:             *providerAPIFlag,
		Model:                   *modelFlag,
		BaseURL:                 *baseURLFlag,
		ActProtocol:             *actProtocolFlag,
		ActProtocolSet:          flagIsSet(flags, "act-protocol"),
		Effort:                  *effortFlag,
		MaxOutputTokens:         *maxOutputTokens,
		MaxOutputTokensSet:      maxOutputTokensSet,
		ThinkingBudgetTokens:    *thinkingBudgetTokens,
		ThinkingBudgetTokensSet: thinkingBudgetTokensSet,
		OmitSystemPrompt:        *noSystemPrompt,
		SystemPromptAppend:      *systemPromptAppend,
		DumpSystemPrompt:        *dumpSystemPrompt,
		Unattended:              false,
	}
	if *dumpSystemPrompt {
		systemPrompt, err := clnkrapp.LoadStartupPrompt(startupInputs)
		if err != nil {
			return fail("%v", err)
		}
		fmt.Fprint(out, systemPrompt) //nolint:errcheck
		return 0
	}

	startup, err := clnkrapp.PrepareStartup(startupInputs)
	if err != nil {
		if clnkrapp.IsMissingAPIKey(err) {
			fmt.Fprintln( //nolint:errcheck
				errOut,
				"Error: No API key found.\nSet it with: export CLNKR_API_KEY=your-api-key",
			)
			return 1
		}
		return fail("%v", err)
	}
	runMetadata := startup.Metadata
	agent := startup.Agent

	eventOut := &lockedWriter{w: out}
	if *eventLog != "" {
		eventLogFile, err := os.OpenFile(*eventLog, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			return fail("cannot open event log %q: %v", *eventLog, err)
		}
		defer eventLogFile.Close() //nolint:errcheck
		eventOut.w = io.MultiWriter(out, eventLogFile)
	}

	agent.Notify = func(event clnkr.Event) {
		if err := clnkrapp.WriteJSONL(eventOut, event); err != nil {
			fmt.Fprintf(errOut, "Error: write event: %v\n", err) //nolint:errcheck
		}
	}
	agent.Notify(clnkrapp.RunMetadataDebugEvent(runMetadata))
	if *maxSteps > 0 {
		agent.MaxSteps = *maxSteps
	}
	if *loadMessages != "" {
		if err := clnkrapp.AddMessagesFile(agent, *loadMessages); err != nil {
			return fail("%v", err)
		}
	}
	if *continueFlag {
		_, ok, err := clnkrapp.ResumeLatestSession(agent, cwd)
		if err != nil {
			return fail("%v", err)
		}
		if !ok {
			return fail("no session found for this project.")
		}
	}

	driver := startup.Driver
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if err := runJSONL(ctx, in, eventOut, errOut, driver); err != nil {
		if ctx.Err() != nil {
			return 130
		}
		return fail("%v", err)
	}
	if msgs := agent.Messages(); len(msgs) > 0 {
		if _, err := clnkrapp.SaveSession(cwd, msgs, runMetadata); err != nil {
			fmt.Fprintf(errOut, "Warning: could not save session: %v\n", err) //nolint:errcheck
		}
	}
	return 0
}

func flagIsSet(flags *flag.FlagSet, name string) bool {
	found := false
	flags.Visit(func(f *flag.Flag) {
		if f.Name == name {
			found = true
		}
	})
	return found
}

func requestOptionFlagsSet(flags *flag.FlagSet) (maxOutputTokensSet, thinkingBudgetTokensSet bool) {
	flags.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "max-output-tokens":
			maxOutputTokensSet = true
		case "thinking-budget-tokens":
			thinkingBudgetTokensSet = true
		}
	})
	return maxOutputTokensSet, thinkingBudgetTokensSet
}

type lockedWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func (w *lockedWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.w.Write(p)
}

type jsonlInput struct {
	command clnkrapp.JSONLCommand
	err     error
}

type activeRun struct {
	done   <-chan error
	cancel context.CancelFunc
}

func (r *activeRun) doneChannel() <-chan error {
	if r == nil {
		return nil
	}
	return r.done
}

func (r *activeRun) cancelRun() {
	if r != nil {
		r.cancel()
	}
}

func runJSONL(
	ctx context.Context,
	r io.Reader,
	out io.Writer,
	errOut io.Writer,
	driver *clnkrapp.Driver,
) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	inputs, readerDone := readJSONL(ctx, r)
	var run *activeRun
	var inputsClosed bool
	fail := func(err error) error {
		fmt.Fprintf(errOut, "%v\n", err) //nolint:errcheck
		run.cancelRun()
		if cleanupErr := waitRunAndDrain(out, driver, run); cleanupErr != nil {
			return cleanupErr
		}
		return err
	}

	for inputs != nil || run != nil {
		select {
		case input, ok := <-inputs:
			if !ok {
				inputs = nil
				inputsClosed = true
				if run != nil && driver.HasPendingReply() {
					run.cancelRun()
				}
				continue
			}
			var harvestErr error
			run, harvestErr = harvestReadyRun(out, driver, run)
			if harvestErr != nil {
				return harvestErr
			}
			if input.err != nil {
				return fail(fmt.Errorf("read JSONL: %w", input.err))
			}
			var err error
			run, err = handleJSONLCommand(
				ctx,
				input.command,
				run,
				driver,
			)
			if err != nil {
				return fail(err)
			}
			if input.command.Type == "shutdown" {
				cancel()
				if closer, ok := r.(io.Closer); ok {
					_ = closer.Close()
				}
				if err := waitRunAndDrain(out, driver, run); err != nil {
					return err
				}
				<-readerDone
				return nil
			}
		case event := <-driver.Events():
			if err := clnkrapp.WriteJSONL(out, event); err != nil {
				return fail(err)
			}
			if inputsClosed && run != nil && driver.HasPendingReply() {
				run.cancelRun()
			}
		case err := <-run.doneChannel():
			run = nil
			if drainErr := drainDriverEvents(out, driver); drainErr != nil {
				return drainErr
			}
			if err != nil {
				return err
			}
		case <-ctx.Done():
			run.cancelRun()
			if cleanupErr := waitRunAndDrain(out, driver, run); cleanupErr != nil {
				return cleanupErr
			}
			return ctx.Err()
		}
	}
	return nil
}

func harvestReadyRun(
	out io.Writer,
	driver *clnkrapp.Driver,
	run *activeRun,
) (*activeRun, error) {
	if run == nil {
		return nil, nil
	}
	select {
	case err := <-run.done:
		if drainErr := drainDriverEvents(out, driver); drainErr != nil {
			return nil, drainErr
		}
		if err != nil {
			return nil, err
		}
		return nil, nil
	default:
		return run, nil
	}
}

func waitRunAndDrain(out io.Writer, driver *clnkrapp.Driver, run *activeRun) error {
	if run != nil {
		<-run.done
	}
	return drainDriverEvents(out, driver)
}

func drainDriverEvents(out io.Writer, driver *clnkrapp.Driver) error {
	for {
		select {
		case event := <-driver.Events():
			if err := clnkrapp.WriteJSONL(out, event); err != nil {
				return err
			}
		default:
			return nil
		}
	}
}

func readJSONL(ctx context.Context, r io.Reader) (<-chan jsonlInput, <-chan struct{}) {
	inputs := make(chan jsonlInput, 1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer close(inputs)
		scanner := bufio.NewScanner(r)
		scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
		send := func(input jsonlInput) bool {
			select {
			case inputs <- input:
				return true
			case <-ctx.Done():
				return false
			}
		}
		for scanner.Scan() {
			command, decodeErr := clnkrapp.DecodeJSONLCommand(scanner.Bytes())
			if !send(jsonlInput{command: command, err: decodeErr}) {
				return
			}
		}
		if err := scanner.Err(); err != nil {
			send(jsonlInput{err: err})
		}
	}()
	return inputs, done
}

func handleJSONLCommand(
	ctx context.Context,
	command clnkrapp.JSONLCommand,
	run *activeRun,
	driver *clnkrapp.Driver,
) (*activeRun, error) {
	switch command.Type {
	case "prompt":
		if run != nil {
			return run, fmt.Errorf("prompt: driver run already in progress")
		}
		run = startDriverRun(ctx, func(runCtx context.Context) error {
			return driver.Prompt(runCtx, command.Text, command.Mode)
		})
	case "reply":
		if !driver.HasPendingReply() {
			return run, fmt.Errorf("reply: no pending request")
		}
		return run, driver.Reply(ctx, command.Text)
	case "compact":
		if run != nil {
			return run, fmt.Errorf("compact: driver run already in progress")
		}
		run = startDriverRun(ctx, func(runCtx context.Context) error {
			return driver.Compact(runCtx)
		})
	case "shutdown":
		run.cancelRun()
	default:
		return run, fmt.Errorf("unknown JSONL command type %q", command.Type)
	}
	return run, nil
}

func startDriverRun(
	ctx context.Context,
	run func(context.Context) error,
) *activeRun {
	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() {
		done <- run(runCtx)
	}()
	return &activeRun{done: done, cancel: cancel}
}
