package main

import "flag"

type providerFlags struct {
	model, modelShort, baseURL, baseURLShort *string
	provider, providerAPI                    *string
}

func newProviderFlags(flags *flag.FlagSet) *providerFlags {
	return &providerFlags{
		model:        flags.String("model", "", ""),
		modelShort:   flags.String("m", "", ""),
		baseURL:      flags.String("base-url", "", ""),
		baseURLShort: flags.String("u", "", ""),
		provider:     flags.String("provider", "", ""),
		providerAPI:  flags.String("provider-api", "", ""),
	}
}

func (values *providerFlags) apply(opts *cliOptions) {
	opts.model = aliasedString(*values.modelShort, *values.model)
	opts.baseURL = aliasedString(*values.baseURLShort, *values.baseURL)
	opts.provider = *values.provider
	opts.providerAPI = *values.providerAPI
}

func (values *providerFlags) applyTo(opts *cliOptions, _ *flag.FlagSet, _ bool) {
	values.apply(opts)
}
