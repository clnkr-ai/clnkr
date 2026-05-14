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
	appliers := newCLIFlagAppliers(flags)

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
	for _, apply := range appliers {
		apply(&opts, flags, dumpUnattendedPrompt)
	}
	return opts
}

func fatalOptionf(format string, args ...any) {
	_, _ = fmt.Fprintf(os.Stderr, "Error: "+format+"\n", args...)
	os.Exit(1)
}
