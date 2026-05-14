package main

import "flag"

type ioFlags struct {
	eventLog, trajectory, loadMessages *string
}

func newIOFlags(flags *flag.FlagSet) *ioFlags {
	return &ioFlags{
		eventLog:     flags.String("event-log", "", ""),
		trajectory:   flags.String("trajectory", "", ""),
		loadMessages: flags.String("load-messages", "", ""),
	}
}

func (values *ioFlags) apply(opts *cliOptions) {
	opts.eventLog = *values.eventLog
	opts.trajectory = *values.trajectory
	opts.loadMessages = *values.loadMessages
}

func (values *ioFlags) applyTo(opts *cliOptions, _ *flag.FlagSet, _ bool) {
	values.apply(opts)
}
