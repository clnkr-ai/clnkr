package clnkrapp

import (
	"bytes"
	"encoding/json"
	"io"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/clnkr-ai/clnkr"
)

type eventLogEntry struct {
	TS      string          `json:"ts"`
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload"`
}

func TestWriteEventLogIncludesTimestamp(t *testing.T) {
	entry := writeEventLogEntry(t, clnkr.EventDebug{Message: "querying model..."})

	if entry.TS == "" {
		t.Fatal("ts is empty, want RFC3339Nano UTC timestamp")
	}

	ts, err := time.Parse(time.RFC3339Nano, entry.TS)
	if err != nil {
		t.Fatalf("parse ts %q: %v", entry.TS, err)
	}
	if ts.Location() != time.UTC {
		t.Fatalf("ts location = %v, want UTC", ts.Location())
	}
	if entry.Type != "debug" {
		t.Fatalf("type = %q, want debug", entry.Type)
	}
}

func TestWriteEventLogResponsePayload(t *testing.T) {
	entry := writeEventLogEntry(t, clnkr.EventResponse{
		Turn:  verifiedDone("done"),
		Usage: clnkr.Usage{InputTokens: 3, OutputTokens: 4},
		Raw:   "raw response",
	})
	if entry.Type != "response" {
		t.Fatalf("type = %q, want response", entry.Type)
	}

	var payload struct {
		Turn  json.RawMessage            `json:"turn"`
		Usage map[string]json.RawMessage `json:"usage"`
		Raw   string                     `json:"raw"`
	}
	decodePayload(t, entry, &payload)

	wantTurn := `{"type":"done","summary":"done","verification":{"status":"verified","checks":[{"command":"go test ./...","outcome":"passed","evidence":"go test ./... passed and ls output showed current directory entries for completion"}]},"known_risks":[]}`
	if string(payload.Turn) != wantTurn {
		t.Fatalf("turn = %s, want canonical done turn", payload.Turn)
	}
	if _, ok := payload.Usage["input_tokens"]; !ok {
		t.Fatalf("usage = %#v, want input_tokens", payload.Usage)
	}
	if _, ok := payload.Usage["InputTokens"]; ok {
		t.Fatalf("usage has Go field name: %#v", payload.Usage)
	}
	if payload.Raw != "raw response" {
		t.Fatalf("raw = %q, want raw response", payload.Raw)
	}
}

func TestWriteEventLogSnakeCasePayloads(t *testing.T) {
	tests := []struct {
		name       string
		event      clnkr.Event
		wantType   string
		wantKey    string
		rejectKey  string
		wantFields map[string]any
	}{
		{
			name:      "command start",
			event:     clnkr.EventCommandStart{Command: "pwd", Dir: "/tmp"},
			wantType:  "command_start",
			wantKey:   "command",
			rejectKey: "Command",
			wantFields: map[string]any{
				"command": "pwd",
				"dir":     "/tmp",
			},
		},
		{
			name:      "debug",
			event:     clnkr.EventDebug{Message: "querying model..."},
			wantType:  "debug",
			wantKey:   "message",
			rejectKey: "Message",
			wantFields: map[string]any{
				"message": "querying model...",
			},
		},
		{
			name:      "context backstop debug",
			event:     clnkr.EventDebug{Message: clnkr.ContextLengthBackstopCompactingDebug},
			wantType:  "debug",
			wantKey:   "message",
			rejectKey: "Message",
			wantFields: map[string]any{
				"message": clnkr.ContextLengthBackstopCompactingDebug,
			},
		},
		{
			name:      "protocol failure",
			event:     clnkr.EventProtocolFailure{Reason: "bad json", Raw: "nope"},
			wantType:  "protocol_failure",
			wantKey:   "reason",
			rejectKey: "Reason",
			wantFields: map[string]any{
				"reason": "bad json",
				"raw":    "nope",
			},
		},
		{
			name: "completion gate",
			event: clnkr.EventCompletionGate{
				Decision: "challenge",
				Reasons:  []string{"artifact_claim_without_check"},
				Summary:  "created result.txt",
			},
			wantType:  "completion_gate",
			wantKey:   "decision",
			rejectKey: "Decision",
			wantFields: map[string]any{
				"decision": "challenge",
				"reasons":  []any{"artifact_claim_without_check"},
				"summary":  "created result.txt",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			entry := writeEventLogEntry(t, tt.event)
			if entry.Type != tt.wantType {
				t.Fatalf("type = %q, want %q", entry.Type, tt.wantType)
			}

			payload := payloadObject(t, entry)
			if _, ok := payload[tt.wantKey]; !ok {
				t.Fatalf("payload = %#v, want %q", payload, tt.wantKey)
			}
			if _, ok := payload[tt.rejectKey]; ok {
				t.Fatalf("payload has Go field name %q: %#v", tt.rejectKey, payload)
			}
			for key, want := range tt.wantFields {
				if !reflect.DeepEqual(payload[key], want) {
					t.Fatalf("payload[%q] = %#v, want %#v", key, payload[key], want)
				}
			}
		})
	}
}

func TestWriteEventLogCommandDonePayload(t *testing.T) {
	tests := []struct {
		name        string
		event       clnkr.EventCommandDone
		wantOutcome map[string]any
	}{
		{
			name: "feedback and exit outcome included",
			event: clnkr.EventCommandDone{
				Command:  "touch note.txt",
				Outcome:  exitOutcome(0),
				Feedback: clnkr.CommandFeedback{ChangedFiles: []string{"note.txt"}},
			},
			wantOutcome: map[string]any{"type": "exit", "exit_code": float64(0)},
		},
		{
			name: "host error remains typed",
			event: clnkr.EventCommandDone{
				Command: "missing-command",
				Outcome: clnkr.CommandOutcome{Type: clnkr.CommandOutcomeError, Message: "boom"},
			},
			wantOutcome: map[string]any{"type": "error", "message": "boom"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			entry := writeEventLogEntry(t, tt.event)
			if entry.Type != "command_done" {
				t.Fatalf("type = %q, want command_done", entry.Type)
			}

			payload := payloadObject(t, entry)
			if payload["Command"] != nil || payload["Outcome"] != nil {
				t.Fatalf("payload has Go field names: %#v", payload)
			}
			if payload["exit_code"] != nil || payload["err"] != nil {
				t.Fatalf("payload has legacy completion fields: %#v", payload)
			}
			if !reflect.DeepEqual(payload["outcome"], tt.wantOutcome) {
				t.Fatalf("outcome = %#v, want %#v", payload["outcome"], tt.wantOutcome)
			}
			if len(tt.event.Feedback.ChangedFiles) > 0 {
				feedback := payload["feedback"].(map[string]any)
				if !reflect.DeepEqual(feedback["changed_files"], []any{"note.txt"}) {
					t.Fatalf("feedback = %#v, want note.txt", feedback)
				}
			}
		})
	}
}

func TestWriteEventLogPreservesRawCommandOutput(t *testing.T) {
	stdout := "stdout-head\n" + strings.Repeat(
		"raw stdout\n",
		80*1024/len("raw stdout\n"),
	) + "stdout-tail\n"
	stderr := "stderr-head\n" + strings.Repeat(
		"raw stderr\n",
		80*1024/len("raw stderr\n"),
	) + "stderr-tail\n"

	entry := writeEventLogEntry(t, clnkr.EventCommandDone{
		Command: "make test",
		Stdout:  stdout,
		Stderr:  stderr,
		Outcome: exitOutcome(1),
	})
	if entry.Type != "command_done" {
		t.Fatalf("event type = %q, want command_done", entry.Type)
	}

	var payload struct {
		Stdout string `json:"stdout"`
		Stderr string `json:"stderr"`
	}
	decodePayload(t, entry, &payload)
	if payload.Stdout != stdout {
		t.Fatalf("stdout length = %d, want raw %d", len(payload.Stdout), len(stdout))
	}
	if payload.Stderr != stderr {
		t.Fatalf("stderr length = %d, want raw %d", len(payload.Stderr), len(stderr))
	}
	if strings.Contains(payload.Stdout, "compressed") ||
		strings.Contains(payload.Stderr, "compressed") {
		t.Fatalf("event log should not contain model-facing compression markers")
	}
}

func writeEventLogEntry(t *testing.T, event clnkr.Event) eventLogEntry {
	t.Helper()

	var buf bytes.Buffer
	if err := WriteEventLog(&buf, event); err != nil {
		t.Fatalf("WriteEventLog: %v", err)
	}

	var entry eventLogEntry
	decoder := json.NewDecoder(&buf)
	if err := decoder.Decode(&entry); err != nil {
		t.Fatalf("decode event log: %v", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		t.Fatalf("extra event decode error = %v, want EOF", err)
	}
	return entry
}

func payloadObject(t *testing.T, entry eventLogEntry) map[string]any {
	t.Helper()

	var payload map[string]any
	decodePayload(t, entry, &payload)
	return payload
}

func decodePayload(t *testing.T, entry eventLogEntry, v any) {
	t.Helper()

	if err := json.Unmarshal(entry.Payload, v); err != nil {
		t.Fatalf("decode %s payload: %v", entry.Type, err)
	}
}
