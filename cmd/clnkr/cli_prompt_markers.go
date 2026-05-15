package main

func unattendedPromptDumpArgs(args []string) ([]string, bool) {
	dumpUnattendedPrompt := false
	if n := len(args); n >= 2 && dumpPromptMarker(args, n-2) && promptModeMarker(args, n-1) {
		args, dumpUnattendedPrompt = args[:n-1], true
	}
	for i := 0; i+1 < len(args); i++ {
		if promptModeMarker(args, i) && args[i+1] == "--dump-system-prompt" {
			fatalOptionf("%s requires a task. To dump the unattended prompt, use: clnkr --dump-system-prompt %s", args[i], args[i])
		}
	}
	return args, dumpUnattendedPrompt
}

func promptModeMarker(args []string, i int) bool {
	if i > 0 && valueFlag(args[i-1]) {
		return false
	}
	return args[i] == "-p" || args[i] == "--prompt" || args[i] == "--prompt-mode-unattended"
}

func dumpPromptMarker(args []string, i int) bool {
	if i > 0 && valueFlag(args[i-1]) {
		return false
	}
	return args[i] == "--dump-system-prompt"
}

func valueFlag(arg string) bool {
	switch arg {
	case "-p", "--prompt", "--prompt-mode-unattended", "-m", "--model", "-u", "--base-url", "--provider", "--provider-api", "--act-protocol", "--effort", "--thinking-budget-tokens", "--max-output-tokens", "--max-steps", "--event-log", "--trajectory", "--load-messages", "--system-prompt-append":
		return true
	}
	return false
}
