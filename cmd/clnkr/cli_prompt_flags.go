package main

import (
	"flag"
	"strconv"
)

type promptFlags struct {
	taskPrompt, promptLong, promptModeUnattended *string
	fullSend                                     *bool
	explicitFullSendFalse                        bool
}

func newPromptFlags(flags *flag.FlagSet) *promptFlags {
	values := &promptFlags{}
	values.taskPrompt, values.promptLong = flags.String("p", "", ""), flags.String("prompt", "", "")
	values.promptModeUnattended = flags.String("prompt-mode-unattended", "", "")
	values.fullSend = new(bool)
	flags.BoolFunc("full-send", "", func(s string) error {
		value, err := strconv.ParseBool(s)
		if err != nil {
			return err
		}
		*values.fullSend = value
		values.explicitFullSendFalse = values.explicitFullSendFalse || !value
		return nil
	})
	return values
}

func (values *promptFlags) apply(opts *cliOptions, dumpUnattendedPrompt bool) {
	opts.taskPrompt = aliasedString(*values.promptModeUnattended, aliasedString(*values.promptLong, *values.taskPrompt))
	opts.singleTask = opts.taskPrompt != "" || dumpUnattendedPrompt
	if opts.singleTask && values.explicitFullSendFalse {
		fatalOptionf("--full-send=false conflicts with -p")
	}
	if opts.singleTask {
		*values.fullSend = true
	}
	opts.fullSend = *values.fullSend
}

func (values *promptFlags) applyTo(opts *cliOptions, _ *flag.FlagSet, dumpUnattendedPrompt bool) {
	values.apply(opts, dumpUnattendedPrompt)
}
