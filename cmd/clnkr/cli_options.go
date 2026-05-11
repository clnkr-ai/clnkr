package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/clnkr-ai/clnkr/cmd/internal/clnkrapp"
)

type cliOptions struct {
	taskPrompt, model, baseURL, provider, providerAPI, actProtocol, effort string
	eventLog, trajectory, loadMessages, systemPromptAppend                 string
	thinkingBudgetTokens, maxOutputTokens, maxSteps                        int
	fullSend, verbose, showVersion, continueSession, listSessions          bool
	noSystemPrompt, dumpSystemPrompt, singleTask                           bool
	actProtocolSet, maxOutputTokensSet, thinkingBudgetSet                  bool
}

func parseCLIOptions(args []string) cliOptions {
	flags := flag.NewFlagSet("clnkr", flag.ContinueOnError)
	flags.Usage = func() {}
	flags.SetOutput(io.Discard)
	taskPromptFlag, promptLong := flags.String("p", "", ""), flags.String("prompt", "", "")
	promptModeUnattended := flags.String("prompt-mode-unattended", "", "")
	modelFlag, modelShort := flags.String("model", "", ""), flags.String("m", "", "")
	baseURLFlag, baseURLShort := flags.String("base-url", "", ""), flags.String("u", "", "")
	providerFlag, providerAPIFlag := flags.String("provider", "", ""), flags.String("provider-api", "", "")
	actProtocolFlag, effortFlag := flags.String("act-protocol", "auto", ""), flags.String("effort", "", "")
	thinkingBudgetTokens, maxOutputTokens := flags.Int("thinking-budget-tokens", 0, ""), flags.Int("max-output-tokens", 0, "")
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
	eventLog, trajectory := flags.String("event-log", "", ""), flags.String("trajectory", "", "")
	loadMessages := flags.String("load-messages", "", "")
	continueFlag, continueShort := flags.Bool("continue", false, ""), flags.Bool("c", false, "")
	listSessions, listSessionsShort := flags.Bool("list-sessions", false, ""), flags.Bool("l", false, "")
	noSystemPrompt, noSystemPromptShort := flags.Bool("no-system-prompt", false, ""), flags.Bool("S", false, "")
	systemPromptAppend := flags.String("system-prompt-append", "", "")
	dumpSystemPrompt := flags.Bool("dump-system-prompt", false, "")

	args, dumpUnattendedPrompt := unattendedPromptDumpArgs(args)
	if err := flags.Parse(args); err != nil {
		if err == flag.ErrHelp {
			_, _ = fmt.Fprint(os.Stdout, usageText)
			os.Exit(0)
		}
		_, _ = fmt.Fprintf(os.Stderr, "Error: %v\nSee clnkr(1) for available options.\n", err)
		os.Exit(1)
	}
	maxOutputTokensSet, thinkingBudgetTokensSet := clnkrapp.RequestOptionFlagsSet(flags)
	actProtocolSet := flagIsSet(flags, "act-protocol")

	taskPrompt := aliasedString(*promptModeUnattended, aliasedString(*promptLong, *taskPromptFlag))
	singleTask := taskPrompt != "" || dumpUnattendedPrompt
	fatalWhen(singleTask && explicitFullSendFalse, "--full-send=false conflicts with -p")
	if singleTask {
		*fullSend = true
	}

	return cliOptions{taskPrompt: taskPrompt, model: aliasedString(*modelShort, *modelFlag), baseURL: aliasedString(*baseURLShort, *baseURLFlag), provider: *providerFlag, providerAPI: *providerAPIFlag, actProtocol: *actProtocolFlag, effort: *effortFlag, thinkingBudgetTokens: *thinkingBudgetTokens, maxOutputTokens: *maxOutputTokens, maxSteps: *maxSteps, fullSend: *fullSend, verbose: *verbose || *verboseShort, showVersion: *showVersion || *showVersionShort, eventLog: *eventLog, trajectory: *trajectory, loadMessages: *loadMessages, continueSession: *continueFlag || *continueShort, listSessions: *listSessions || *listSessionsShort, noSystemPrompt: *noSystemPrompt || *noSystemPromptShort, systemPromptAppend: *systemPromptAppend, dumpSystemPrompt: *dumpSystemPrompt, singleTask: singleTask, actProtocolSet: actProtocolSet, maxOutputTokensSet: maxOutputTokensSet, thinkingBudgetSet: thinkingBudgetTokensSet}
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

func unattendedPromptDumpArgs(args []string) ([]string, bool) {
	dumpUnattendedPrompt := false
	if n := len(args); n >= 2 && dumpPromptMarker(args, n-2) && promptModeMarker(args, n-1) {
		args, dumpUnattendedPrompt = args[:n-1], true
	}
	for i := 0; i+1 < len(args); i++ {
		if promptModeMarker(args, i) && args[i+1] == "--dump-system-prompt" {
			fatalf("%s requires a task. To dump the unattended prompt, use: clnkr --dump-system-prompt %s", args[i], args[i])
		}
	}
	return args, dumpUnattendedPrompt
}

func actProtocolFlagValue(opts cliOptions, env func(string) string) string {
	if opts.actProtocolSet {
		return opts.actProtocol
	}
	if value := strings.TrimSpace(env("CLNKR_ACT_PROTOCOL")); value != "" {
		return value
	}
	return opts.actProtocol
}
