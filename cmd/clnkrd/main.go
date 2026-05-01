package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"sync"

	"github.com/clnkr-ai/clnkr"
	"github.com/clnkr-ai/clnkr/cmd/internal/clnkrapp"
	"github.com/clnkr-ai/clnkr/cmd/internal/providerconfig"
	"github.com/clnkr-ai/clnkr/cmd/internal/session"
)

// version is set at build time via -ldflags.
var version = "dev"

func main() {
	os.Exit(runMain(os.Args[1:], os.Stdin, os.Stdout, os.Stderr, os.Getenv))
}

func runMain(args []string, in io.Reader, out io.Writer, errOut io.Writer, env func(string) string) int {
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
	actProtocolFlag := flags.String("act-protocol", "clnkr-inline", "")
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
			fmt.Fprint(out, "Usage: clnkrd [options]\n") //nolint:errcheck
			return 0
		}
		return fail("%v\nSee clnkrd(1) for available options.", err)
	}
	maxOutputTokensSet, thinkingBudgetTokensSet := clnkrapp.RequestOptionFlagsSet(flags)

	if *showVersion {
		fmt.Fprintf(out, "clnkrd %s\n", version) //nolint:errcheck
		return 0
	}

	cwd, err := os.Getwd()
	if err != nil {
		return fail("cannot get working directory: %v", err)
	}

	actProtocol, err := clnkr.ParseActProtocol(*actProtocolFlag)
	if err != nil {
		return fail("%v", err)
	}
	systemPrompt := clnkr.LoadPromptWithOptions(cwd, clnkr.PromptOptions{
		OmitSystemPrompt:   *noSystemPrompt,
		SystemPromptAppend: *systemPromptAppend,
		ActProtocol:        actProtocol,
	})
	if *dumpSystemPrompt {
		fmt.Fprint(out, systemPrompt) //nolint:errcheck
		return 0
	}

	cfg, err := providerconfig.ResolveConfig(providerconfig.Inputs{
		Provider: *providerFlag, ProviderAPI: *providerAPIFlag,
		Model: *modelFlag, BaseURL: *baseURLFlag,
		ActProtocol:    actProtocol,
		RequestOptions: clnkrapp.RequestOptions(*effortFlag, *maxOutputTokens, maxOutputTokensSet, *thinkingBudgetTokens, thinkingBudgetTokensSet),
	}, env)
	if err != nil {
		if strings.Contains(err.Error(), "api key is required") {
			fmt.Fprintln(errOut, "Error: No API key found.\nSet it with: export CLNKR_API_KEY=your-api-key") //nolint:errcheck
			return 1
		}
		return fail("%v", err)
	}

	runMetadata := clnkrapp.NewRunMetadata(version, cfg, systemPrompt)
	eventOut := &lockedWriter{w: out}
	if *eventLog != "" {
		eventLogFile, err := os.OpenFile(*eventLog, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			return fail("cannot open event log %q: %v", *eventLog, err)
		}
		defer eventLogFile.Close() //nolint:errcheck
		eventOut.w = io.MultiWriter(out, eventLogFile)
	}

	agent := clnkr.NewAgent(clnkrapp.NewModelForConfig(cfg, systemPrompt), &clnkr.CommandExecutor{}, cwd)
	agent.ActProtocol = cfg.ActProtocol
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

	driver := clnkrapp.NewDriver(agent, clnkrapp.MakeCompactorFactory(cfg))
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if err := runJSONL(ctx, in, eventOut, errOut, driver); err != nil {
		if ctx.Err() != nil {
			return 130
		}
		return fail("%v", err)
	}
	if msgs := agent.Messages(); len(msgs) > 0 {
		if err := session.SaveSessionWithMetadata(cwd, msgs, runMetadata); err != nil {
			fmt.Fprintf(errOut, "Warning: could not save session: %v\n", err) //nolint:errcheck
		}
	}
	return 0
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

func runJSONL(ctx context.Context, r io.Reader, out io.Writer, errOut io.Writer, driver *clnkrapp.Driver) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	inputs, readerDone := readJSONL(ctx, r)
	var runDone <-chan error
	var cancelRun context.CancelFunc
	var inputsClosed bool
	fail := func(err error) error {
		fmt.Fprintf(errOut, "%v\n", err) //nolint:errcheck
		if cancelRun != nil {
			cancelRun()
		}
		if cleanupErr := waitRunAndDrain(out, driver, runDone); cleanupErr != nil {
			return cleanupErr
		}
		return err
	}

	for inputs != nil || runDone != nil {
		select {
		case input, ok := <-inputs:
			if !ok {
				inputs = nil
				inputsClosed = true
				if runDone != nil && driver.Pending() != clnkrapp.PendingNone {
					cancelRun()
				}
				continue
			}
			var harvestErr error
			runDone, cancelRun, harvestErr = harvestReadyRun(out, driver, runDone, cancelRun)
			if harvestErr != nil {
				return harvestErr
			}
			if input.err != nil {
				return fail(fmt.Errorf("read JSONL: %w", input.err))
			}
			var err error
			runDone, cancelRun, err = handleJSONLCommand(ctx, input.command, runDone, cancelRun, driver)
			if err != nil {
				return fail(err)
			}
			if input.command.Type == "shutdown" {
				cancel()
				if closer, ok := r.(io.Closer); ok {
					_ = closer.Close()
				}
				if err := waitRunAndDrain(out, driver, runDone); err != nil {
					return err
				}
				<-readerDone
				return nil
			}
		case event := <-driver.Events():
			if err := clnkrapp.WriteJSONL(out, event); err != nil {
				return fail(err)
			}
			if inputsClosed && cancelRun != nil && driver.Pending() != clnkrapp.PendingNone {
				cancelRun()
			}
		case err := <-runDone:
			runDone = nil
			cancelRun = nil
			if drainErr := drainDriverEvents(out, driver); drainErr != nil {
				return drainErr
			}
			if err != nil {
				return err
			}
		case <-ctx.Done():
			if cancelRun != nil {
				cancelRun()
			}
			if cleanupErr := waitRunAndDrain(out, driver, runDone); cleanupErr != nil {
				return cleanupErr
			}
			return ctx.Err()
		}
	}
	return nil
}

func harvestReadyRun(out io.Writer, driver *clnkrapp.Driver, runDone <-chan error, cancelRun context.CancelFunc) (<-chan error, context.CancelFunc, error) {
	if runDone == nil {
		return runDone, cancelRun, nil
	}
	select {
	case err := <-runDone:
		if drainErr := drainDriverEvents(out, driver); drainErr != nil {
			return nil, nil, drainErr
		}
		if err != nil {
			return nil, nil, err
		}
		return nil, nil, nil
	default:
		return runDone, cancelRun, nil
	}
}

func waitRunAndDrain(out io.Writer, driver *clnkrapp.Driver, runDone <-chan error) error {
	if runDone != nil {
		<-runDone
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

func handleJSONLCommand(ctx context.Context, command clnkrapp.JSONLCommand, runDone <-chan error, cancelRun context.CancelFunc, driver *clnkrapp.Driver) (<-chan error, context.CancelFunc, error) {
	switch command.Type {
	case "prompt":
		if runDone != nil {
			return runDone, cancelRun, fmt.Errorf("prompt: driver run already in progress")
		}
		runDone, cancelRun = startDriverPrompt(ctx, driver, command.Text, command.Mode)
	case "reply":
		if driver.Pending() == clnkrapp.PendingNone {
			return runDone, cancelRun, fmt.Errorf("reply: no pending request")
		}
		return runDone, cancelRun, driver.Reply(ctx, command.Text)
	case "compact":
		if runDone != nil {
			return runDone, cancelRun, fmt.Errorf("compact: driver run already in progress")
		}
		runDone, cancelRun = startDriverPrompt(ctx, driver, compactPrompt(command.Instructions), clnkrapp.PromptModeApproval)
	case "shutdown":
		if cancelRun != nil {
			cancelRun()
		}
	default:
		return runDone, cancelRun, fmt.Errorf("unknown JSONL command type %q", command.Type)
	}
	return runDone, cancelRun, nil
}

func startDriverPrompt(ctx context.Context, driver *clnkrapp.Driver, text string, mode string) (<-chan error, context.CancelFunc) {
	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() {
		done <- driver.Prompt(runCtx, text, mode)
	}()
	return done, cancel
}

func compactPrompt(instructions string) string {
	instructions = strings.TrimSpace(instructions)
	if instructions == "" {
		return "/compact"
	}
	return "/compact " + instructions
}
