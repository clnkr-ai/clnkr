package clnkr

import "github.com/clnkr-ai/clnkr/transcript"

func toTranscriptMessages(messages []Message) []transcript.Message {
	out := make([]transcript.Message, len(messages))
	for i := range messages {
		out[i] = transcript.Message(messages[i])
	}
	return out
}

func fromTranscriptMessages(messages []transcript.Message) []Message {
	out := make([]Message, len(messages))
	for i := range messages {
		out[i] = Message(messages[i])
	}
	return out
}

func toTranscriptCommandResult(result CommandResult) transcript.CommandResult {
	return transcript.CommandResult{Command: result.Command, Stdout: result.Stdout, Stderr: result.Stderr, ExitCode: result.ExitCode}
}
