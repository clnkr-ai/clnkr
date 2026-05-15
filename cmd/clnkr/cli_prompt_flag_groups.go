package main

import "flag"

func newPromptFlagAppliers(flags *flag.FlagSet) []func(*cliOptions, *flag.FlagSet, bool) {
	return []func(*cliOptions, *flag.FlagSet, bool){
		newPromptFlags(flags).applyTo,
		newProviderFlags(flags).applyTo,
		newGenerationFlags(flags).applyTo,
	}
}
