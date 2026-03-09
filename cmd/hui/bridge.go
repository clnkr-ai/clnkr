package main

import (
	"encoding/json"
	"fmt"
	"os"

	tea "charm.land/bubbletea/v2"
	hew "github.com/cosgroveb/hew"
)

// eventChSize is the buffer capacity for the agent->TUI event channel.
// Streaming tokens are high-frequency (~100/sec from fast models).
// Bubbletea batches messages between renders (~60fps), so the consumer
// processes roughly 1-2 messages per frame. 256 provides headroom for
// burst token delivery without backpressuring the SSE read loop.
const eventChSize = 256

type eventMsg struct{ event hew.Event }

func eventBridge(ch <-chan hew.Event) tea.Cmd {
	return func() tea.Msg {
		e, ok := <-ch
		if !ok {
			return nil
		}
		return eventMsg{event: e}
	}
}

// makeNotify returns a Notify function for the agent. It writes to the
// event log synchronously (never dropped), then sends to the TUI channel
// with a non-blocking send (may drop under buffer pressure).
func makeNotify(ch chan<- hew.Event, eventLog *os.File) func(hew.Event) {
	return func(e hew.Event) {
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
func writeEventLog(f *os.File, e hew.Event) {
	var je jsonEvent
	switch ev := e.(type) {
	case hew.EventResponse:
		je = jsonEvent{Type: "response", Payload: ev}
	case hew.EventCommandStart:
		je = jsonEvent{Type: "command_start", Payload: ev}
	case hew.EventCommandDone:
		je = jsonEvent{Type: "command_done", Payload: struct {
			Output string `json:"output"`
			Err    string `json:"err,omitempty"`
		}{Output: ev.Output, Err: errString(ev.Err)}}
	case hew.EventFormatError:
		je = jsonEvent{Type: "format_error", Payload: ev}
	case hew.EventDebug:
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
