package main

import (
	"flag"
	"fmt"
	"io"
	"os"
)

func parseCLIOptions(args []string, usage string) cliOptions {
	flags := flag.NewFlagSet("clnkr", flag.ContinueOnError)
	flags.Usage = func() {}
	flags.SetOutput(io.Discard)
	prompt := newPromptFlags(flags)
	provider := newProviderFlags(flags)
	generation := newGenerationFlags(flags)
	run := newRunFlags(flags)
	inputOutput := newIOFlags(flags)
	session := newSessionFlags(flags)
	systemPrompt := newSystemPromptFlags(flags)

	args, dumpUnattendedPrompt := unattendedPromptDumpArgs(args)
	if err := flags.Parse(args); err != nil {
		if err == flag.ErrHelp {
			_, _ = fmt.Fprint(os.Stdout, usage)
			os.Exit(0)
		}
		_, _ = fmt.Fprintf(os.Stderr, "Error: %v\nSee clnkr(1) for available options.\n", err)
		os.Exit(1)
	}
	var opts cliOptions
	prompt.apply(&opts, dumpUnattendedPrompt)
	provider.apply(&opts)
	generation.apply(&opts, flags)
	run.apply(&opts)
	inputOutput.apply(&opts)
	session.apply(&opts)
	systemPrompt.apply(&opts)
	return opts
}

func fatalOptionf(format string, args ...any) {
	_, _ = fmt.Fprintf(os.Stderr, "Error: "+format+"\n", args...)
	os.Exit(1)
}
