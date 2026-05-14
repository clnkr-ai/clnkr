package main

import "flag"

type systemPromptFlags struct {
	noSystemPrompt, noSystemPromptShort *bool
	systemPromptAppend                  *string
	dumpSystemPrompt                    *bool
}

func newSystemPromptFlags(flags *flag.FlagSet) *systemPromptFlags {
	return &systemPromptFlags{
		noSystemPrompt:      flags.Bool("no-system-prompt", false, ""),
		noSystemPromptShort: flags.Bool("S", false, ""),
		systemPromptAppend:  flags.String("system-prompt-append", "", ""),
		dumpSystemPrompt:    flags.Bool("dump-system-prompt", false, ""),
	}
}

func (values *systemPromptFlags) apply(opts *cliOptions) {
	opts.noSystemPrompt = *values.noSystemPrompt || *values.noSystemPromptShort
	opts.systemPromptAppend = *values.systemPromptAppend
	opts.dumpSystemPrompt = *values.dumpSystemPrompt
}

func (values *systemPromptFlags) applyTo(opts *cliOptions, _ *flag.FlagSet, _ bool) {
	values.apply(opts)
}
