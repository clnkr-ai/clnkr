package main

import "flag"

func newCLIFlagAppliers(flags *flag.FlagSet) []func(*cliOptions, *flag.FlagSet, bool) {
	appliers := newPromptFlagAppliers(flags)
	return append(appliers, newRuntimeFlagAppliers(flags)...)
}
