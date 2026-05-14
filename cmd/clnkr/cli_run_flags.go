package main

import "flag"

type runFlags struct {
	maxSteps                      *int
	verbose, verboseShort         *bool
	showVersion, showVersionShort *bool
}

func newRunFlags(flags *flag.FlagSet) *runFlags {
	return &runFlags{
		maxSteps:         flags.Int("max-steps", 0, ""),
		verbose:          flags.Bool("verbose", false, ""),
		verboseShort:     flags.Bool("v", false, ""),
		showVersion:      flags.Bool("version", false, ""),
		showVersionShort: flags.Bool("V", false, ""),
	}
}

func (values *runFlags) apply(opts *cliOptions) {
	opts.maxSteps = *values.maxSteps
	opts.verbose = *values.verbose || *values.verboseShort
	opts.showVersion = *values.showVersion || *values.showVersionShort
}

func (values *runFlags) applyTo(opts *cliOptions, _ *flag.FlagSet, _ bool) {
	values.apply(opts)
}
