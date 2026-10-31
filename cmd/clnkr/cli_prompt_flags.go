package main

import (
	"flag"
	"strconv"
)

type promptFlags struct {
	taskPrompt, promptLong *string
	fullSend               bool
	fullSendSet            bool
}

func newPromptFlags(flags *flag.FlagSet) *promptFlags {
	values := &promptFlags{}
	values.taskPrompt, values.promptLong = flags.String("p", "", ""), flags.String("prompt", "", "")
	flags.BoolFunc("full-send", "", func(s string) error {
		value, err := strconv.ParseBool(s)
		if err != nil {
			return err
		}
		values.fullSend = value
		values.fullSendSet = true
		return nil
	})
	return values
}

func (values *promptFlags) apply(opts *cliOptions, dumpUnattendedPrompt bool) {
	opts.taskPrompt = aliasedString(*values.promptLong, *values.taskPrompt)
	opts.singleTask = opts.taskPrompt != "" || dumpUnattendedPrompt
	if opts.singleTask && values.fullSendSet && !values.fullSend {
		fatalOptionf("--full-send=false conflicts with -p")
	}
	opts.fullSend = opts.singleTask || values.fullSend
}
