package clnkrapp

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"

	"github.com/clnkr-ai/clnkr"
)

func TestDecodeJSONLCommand(t *testing.T) {
	tests := []struct {
		name    string
		line    string
		want    JSONLCommand
		wantErr string
	}{
		{
			"prompt command",
			`{"type":"prompt","text":"inspect","mode":"approval"}`,
			JSONLCommand{Type: "prompt", Text: "inspect", Mode: "approval"},
			"",
		},
		{
			"full send prompt command",
			`{"type":"prompt","text":"ship it","mode":"full_send"}`,
			JSONLCommand{Type: "prompt", Text: "ship it", Mode: "full_send"},
			"",
		},
		{
			"reply command",
			`{"type":"reply","text":"y"}`,
			JSONLCommand{Type: "reply", Text: "y"},
			"",
		},
		{
			"compact command",
			`{"type":"compact"}`,
			JSONLCommand{Type: "compact"},
			"",
		},
		{"shutdown command", `{"type":"shutdown"}`, JSONLCommand{Type: "shutdown"}, ""},
		{
			"unknown command",
			`{"type":"bogus"}`,
			JSONLCommand{},
			`unknown JSONL command type "bogus"`,
		},
		{
			"unknown prompt mode",
			`{"type":"prompt","text":"inspect","mode":"manual"}`,
			JSONLCommand{},
			`unknown JSONL prompt mode "manual"`,
		},
		{
			"missing prompt mode",
			`{"type":"prompt","text":"inspect"}`,
			JSONLCommand{},
			`unknown JSONL prompt mode ""`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := DecodeJSONLCommand([]byte(tt.line))
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("DecodeJSONLCommand() error = %v, want %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("DecodeJSONLCommand(): %v", err)
			}
			if got != tt.want {
				t.Fatalf("DecodeJSONLCommand() = %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestWriteJSONL(t *testing.T) {
	tests := []struct {
		name  string
		event any
		typ   string
	}{
		{
			name: "core response event uses event log encoding",
			event: clnkr.EventResponse{
				Turn:  verifiedDone("done"),
				Usage: clnkr.Usage{InputTokens: 1, OutputTokens: 2},
			},
			typ: "response",
		},
		{
			name:  "core command start event uses event log encoding",
			event: clnkr.EventCommandStart{Command: "pwd", Dir: "/tmp"},
			typ:   "command_start",
		},
		{
			name:  "core command done event uses event log encoding",
			event: clnkr.EventCommandDone{Command: "echo hi", Stdout: "hi\n", ExitCode: 0},
			typ:   "command_done",
		},
		{
			name:  "clarification request",
			event: EventClarificationRequest{Question: "Which repo?"},
			typ:   "clarify",
		},
		{
			name: "approval request",
			event: EventApprovalRequest{
				Prompt: "1. echo hi",
				Commands: []clnkr.BashAction{
					{ID: "call_1", Command: "echo hi"},
					{Command: "pwd", Workdir: "/tmp"},
				},
			},
			typ: "approval_request",
		},
		{name: "done event", event: EventDone{Summary: "done"}, typ: "done"},
		{
			name:  "compacted event",
			event: EventCompacted{Stats: clnkr.CompactStats{CompactedMessages: 4, KeptMessages: 2}},
			typ:   "compacted",
		},
		{name: "error event", event: EventError{Err: errors.New("boom")}, typ: "error"},
	}

	var b bytes.Buffer
	for _, tt := range tests {
		if err := WriteJSONL(&b, tt.event); err != nil {
			t.Fatalf("WriteJSONL(%s): %v", tt.name, err)
		}
	}

	decoder := json.NewDecoder(&b)
	for i, tt := range tests {
		var got struct {
			Type    string         `json:"type"`
			Payload map[string]any `json:"payload"`
		}
		if err := decoder.Decode(&got); err != nil {
			t.Fatalf("decode event %d: %v", i, err)
		}
		if got.Type != tt.typ {
			t.Fatalf("event %d type = %q, want %q", i, got.Type, tt.typ)
		}

		switch tt.typ {
		case "clarify":
			if got.Payload["question"] != "Which repo?" {
				t.Fatalf("clarify payload = %#v, want question", got.Payload)
			}
		case "approval_request":
			if got.Payload["prompt"] != "1. echo hi" {
				t.Fatalf("approval payload = %#v, want prompt", got.Payload)
			}
			commands := got.Payload["commands"].([]any)
			if !reflect.DeepEqual(commands[0], map[string]any{"command": "echo hi"}) {
				t.Fatalf("first approval command = %#v, want command only", commands[0])
			}
			if !reflect.DeepEqual(
				commands[1],
				map[string]any{"command": "pwd", "workdir": "/tmp"},
			) {
				t.Fatalf("second approval command = %#v, want command and workdir", commands[1])
			}
		case "compacted":
			if got.Payload["compacted_messages"] != float64(4) ||
				got.Payload["kept_messages"] != float64(2) {
				t.Fatalf("compacted payload = %#v, want explicit stats", got.Payload)
			}
		case "error":
			if got.Payload["message"] != "boom" {
				t.Fatalf("error payload = %#v, want message", got.Payload)
			}
		}
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		t.Fatalf("extra JSONL event decode error = %v, want EOF", err)
	}
}
