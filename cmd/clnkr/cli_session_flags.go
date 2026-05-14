package main

import "flag"

type sessionFlags struct {
	continueSession, continueShort  *bool
	listSessions, listSessionsShort *bool
}

func newSessionFlags(flags *flag.FlagSet) *sessionFlags {
	return &sessionFlags{
		continueSession:   flags.Bool("continue", false, ""),
		continueShort:     flags.Bool("c", false, ""),
		listSessions:      flags.Bool("list-sessions", false, ""),
		listSessionsShort: flags.Bool("l", false, ""),
	}
}

func (values *sessionFlags) apply(opts *cliOptions) {
	opts.continueSession = *values.continueSession || *values.continueShort
	opts.listSessions = *values.listSessions || *values.listSessionsShort
}

func (values *sessionFlags) applyTo(opts *cliOptions, _ *flag.FlagSet, _ bool) {
	values.apply(opts)
}
