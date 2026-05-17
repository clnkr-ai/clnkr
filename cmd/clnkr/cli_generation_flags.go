package main

import "flag"

type generationFlags struct {
	actProtocol, effort                   *string
	thinkingBudgetTokens, maxOutputTokens *int
}

func newGenerationFlags(flags *flag.FlagSet) *generationFlags {
	return &generationFlags{
		actProtocol:          flags.String("act-protocol", "auto", ""),
		effort:               flags.String("effort", "", ""),
		thinkingBudgetTokens: flags.Int("thinking-budget-tokens", 0, ""),
		maxOutputTokens:      flags.Int("max-output-tokens", 0, ""),
	}
}

func (values *generationFlags) apply(opts *cliOptions, flags *flag.FlagSet) {
	opts.actProtocol = *values.actProtocol
	opts.effort = *values.effort
	opts.thinkingBudgetTokens = *values.thinkingBudgetTokens
	opts.maxOutputTokens = *values.maxOutputTokens
	opts.actProtocolSet, opts.maxOutputTokensSet, opts.thinkingBudgetSet = generationFlagSetValues(
		flags,
	)
}

func (values *generationFlags) applyTo(opts *cliOptions, flags *flag.FlagSet, _ bool) {
	values.apply(opts, flags)
}

func generationFlagSetValues(
	flags *flag.FlagSet,
) (actProtocolSet, maxOutputTokensSet, thinkingBudgetTokensSet bool) {
	flags.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "act-protocol":
			actProtocolSet = true
		case "max-output-tokens":
			maxOutputTokensSet = true
		case "thinking-budget-tokens":
			thinkingBudgetTokensSet = true
		}
	})
	return actProtocolSet, maxOutputTokensSet, thinkingBudgetTokensSet
}
