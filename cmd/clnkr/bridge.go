package main

import (
	"encoding/json"
	"fmt"
	"os"

	tea "charm.land/bubbletea/v2"
	clnkr "github.com/clnkr-ai/clnkr"
)

// eventChSize is the buffer capacity for the agent->TUI event channel.
// Streaming tokens are high-frequency (~100/sec from fast models).
// Bubbletea batches messages between renders (~60fps), so the consumer
// processes roughly 1-2 messages per frame. 256 provides headroom for
// burst token delivery without backpressuring the SSE read loop.
const eventChSize = 256

type eventMsg struct{ event clnkr.Event }
type bridgeDrainedMsg struct{}

func eventBridge(ch <-chan clnkr.Event) tea.Cmd {
	return func() tea.Msg {
		e, ok := <-ch
		if !ok {
			return bridgeDrainedMsg{}
		}
		return eventMsg{event: e}
	}
}

// makeNotify returns a Notify function for the agent. It writes to the
// event log synchronously (never dropped), then sends to the TUI channel
// with a non-blocking send (may drop under buffer pressure).
func makeNotify(ch chan<- clnkr.Event, eventLog *os.File) func(clnkr.Event) {
	return func(e clnkr.Event) {
		if eventLog != nil {
			writeEventLog(eventLog, e)
		}
		select {
		case ch <- e:
		default:
		}
	}
}

type jsonEvent struct {
	Type    string `json:"type"`
	Payload any    `json:"payload"`
}

// writeEventLog writes a single event as a JSONL line to the event log file.
func writeEventLog(f *os.File, e clnkr.Event) {
	var je jsonEvent
	switch ev := e.(type) {
	case clnkr.EventResponse:
		je = jsonEvent{Type: "response", Payload: ev}
	case clnkr.EventCommandStart:
		je = jsonEvent{Type: "command_start", Payload: ev}
	case clnkr.EventCommandDone:
		je = jsonEvent{Type: "command_done", Payload: struct {
			Command  string `json:"command"`
			Stdout   string `json:"stdout"`
			Stderr   string `json:"stderr"`
			ExitCode int    `json:"exit_code"`
			Err      string `json:"err,omitempty"`
		}{Command: ev.Command, Stdout: ev.Stdout, Stderr: ev.Stderr, ExitCode: ev.ExitCode, Err: errString(ev.Err)}}
	case clnkr.EventProtocolFailure:
		je = jsonEvent{Type: "protocol_failure", Payload: struct {
			Reason string `json:"reason"`
			Raw    string `json:"raw"`
		}{Reason: ev.Reason, Raw: ev.Raw}}
	case clnkr.EventDebug:
		je = jsonEvent{Type: "debug", Payload: ev}
	default:
		return
	}
	data, err := json.Marshal(je)
	if err != nil {
		return
	}
	_, _ = fmt.Fprintf(f, "%s\n", data)
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
