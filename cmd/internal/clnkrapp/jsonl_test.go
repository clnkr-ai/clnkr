package clnkrapp

import (
	"bytes"
	"encoding/json"
	"errors"
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
			name: "prompt command",
			line: `{"type":"prompt","text":"inspect","mode":"approval"}`,
			want: JSONLCommand{Type: "prompt", Text: "inspect", Mode: "approval"},
		},
		{
			name: "full send prompt command",
			line: `{"type":"prompt","text":"ship it","mode":"full_send"}`,
			want: JSONLCommand{Type: "prompt", Text: "ship it", Mode: "full_send"},
		},
		{
			name: "reply command",
			line: `{"type":"reply","text":"y"}`,
			want: JSONLCommand{Type: "reply", Text: "y"},
		},
		{
			name: "compact command",
			line: `{"type":"compact","instructions":"focus on tests"}`,
			want: JSONLCommand{Type: "compact", Instructions: "focus on tests"},
		},
		{
			name: "shutdown command",
			line: `{"type":"shutdown"}`,
			want: JSONLCommand{Type: "shutdown"},
		},
		{
			name:    "unknown command",
			line:    `{"type":"bogus"}`,
			wantErr: `unknown JSONL command type "bogus"`,
		},
		{
			name:    "unknown prompt mode",
			line:    `{"type":"prompt","text":"inspect","mode":"manual"}`,
			wantErr: `unknown JSONL prompt mode "manual"`,
		},
		{
			name:    "missing prompt mode",
			line:    `{"type":"prompt","text":"inspect"}`,
			wantErr: `unknown JSONL prompt mode ""`,
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
	var b bytes.Buffer

	events := []any{
		clnkr.EventResponse{
			Turn:  verifiedDone("done"),
			Usage: clnkr.Usage{InputTokens: 1, OutputTokens: 2},
		},
		clnkr.EventWorkingMemoryUpdated{
			Reason: "prompt",
			Stats:  clnkr.WorkingMemoryStats{PreviousBytes: 0, UpdatedBytes: 42, DeltaMessages: 1},
		},
		clnkr.EventCommandStart{Command: "pwd", Dir: "/tmp"},
		clnkr.EventCommandDone{Command: "echo hi", Stdout: "hi\n", ExitCode: 0},
		EventClarificationRequest{Question: "Which repo?"},
		EventApprovalRequest{
			Prompt: "1. echo hi",
			Commands: []clnkr.BashAction{
				{ID: "call_1", Command: "echo hi"},
				{Command: "pwd", Workdir: "/tmp"},
			},
		},
		EventDone{Summary: "done"},
		EventCompacted{Stats: clnkr.CompactStats{CompactedMessages: 4, KeptMessages: 2}},
		EventError{Err: errors.New("boom")},
	}
	for _, event := range events {
		if err := WriteJSONL(&b, event); err != nil {
			t.Fatalf("WriteJSONL(%T): %v", event, err)
		}
	}

	decoder := json.NewDecoder(&b)
	got := make([]map[string]any, 0, len(events))
	for decoder.More() {
		var event map[string]any
		if err := decoder.Decode(&event); err != nil {
			t.Fatalf("decode JSONL event: %v", err)
		}
		got = append(got, event)
	}
	if len(got) != len(events) {
		t.Fatalf("events = %d, want %d", len(got), len(events))
	}

	wantTypes := []string{"response", "working_memory_updated", "command_start", "command_done", "clarify", "approval_request", "done", "compacted", "error"}
	for i, want := range wantTypes {
		if got[i]["type"] != want {
			t.Fatalf("event %d type = %q, want %q", i, got[i]["type"], want)
		}
	}
	for _, gotEvent := range got {
		switch gotEvent["type"] {
		case "child_probe_start", "child_probe_done", "child_probe_denied":
			t.Fatalf("child probe event leaked into JSONL output: %#v", gotEvent)
		}
	}

	clarifyPayload := got[4]["payload"].(map[string]any)
	if clarifyPayload["question"] != "Which repo?" {
		t.Fatalf("clarify payload = %#v, want question", clarifyPayload)
	}

	approvalPayload := got[5]["payload"].(map[string]any)
	if approvalPayload["prompt"] != "1. echo hi" {
		t.Fatalf("approval payload = %#v, want prompt", approvalPayload)
	}
	commands := approvalPayload["commands"].([]any)
	if !reflect.DeepEqual(commands[0], map[string]any{"command": "echo hi"}) {
		t.Fatalf("first approval command = %#v, want command only", commands[0])
	}
	if !reflect.DeepEqual(commands[1], map[string]any{"command": "pwd", "workdir": "/tmp"}) {
		t.Fatalf("second approval command = %#v, want command and workdir", commands[1])
	}

	compactedPayload := got[7]["payload"].(map[string]any)
	if compactedPayload["compacted_messages"] != float64(4) || compactedPayload["kept_messages"] != float64(2) {
		t.Fatalf("compacted payload = %#v, want explicit stats", compactedPayload)
	}

	errorPayload := got[8]["payload"].(map[string]any)
	if errorPayload["message"] != "boom" {
		t.Fatalf("error payload = %#v, want message", errorPayload)
	}
}
