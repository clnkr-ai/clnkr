package main

import "flag"

func newRuntimeFlagAppliers(flags *flag.FlagSet) []func(*cliOptions, *flag.FlagSet, bool) {
	return []func(*cliOptions, *flag.FlagSet, bool){
		newRunFlags(flags).applyTo,
		newIOFlags(flags).applyTo,
		newSessionFlags(flags).applyTo,
		newSystemPromptFlags(flags).applyTo,
	}
}
