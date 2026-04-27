package clnkrapp

import (
	"encoding/json"
	"errors"
	"os"
	"testing"

	"github.com/clnkr-ai/clnkr"
)

func TestWriteEventLogResponsePayload(t *testing.T) {
	f, err := os.CreateTemp("", "clnkr-event-log")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(f.Name()) //nolint:errcheck
	defer f.Close()           //nolint:errcheck

	if err := WriteEventLog(f, clnkr.EventResponse{
		Turn:  &clnkr.DoneTurn{Summary: "done"},
		Usage: clnkr.Usage{InputTokens: 3, OutputTokens: 4},
		Raw:   "raw response",
	}); err != nil {
		t.Fatalf("WriteEventLog: %v", err)
	}

	if _, err := f.Seek(0, 0); err != nil {
		t.Fatal(err)
	}
	var payload struct {
		Type    string `json:"type"`
		Payload struct {
			Turn  json.RawMessage            `json:"turn"`
			Usage map[string]json.RawMessage `json:"usage"`
			Raw   string                     `json:"raw"`
		} `json:"payload"`
	}
	if err := json.NewDecoder(f).Decode(&payload); err != nil {
		t.Fatalf("decode event log: %v", err)
	}
	if payload.Type != "response" {
		t.Fatalf("type = %q, want response", payload.Type)
	}
	if string(payload.Payload.Turn) != `{"type":"done","summary":"done"}` {
		t.Fatalf("turn = %s, want canonical done turn", payload.Payload.Turn)
	}
	if _, ok := payload.Payload.Usage["input_tokens"]; !ok {
		t.Fatalf("usage = %#v, want input_tokens", payload.Payload.Usage)
	}
	if _, ok := payload.Payload.Usage["InputTokens"]; ok {
		t.Fatalf("usage has Go field name: %#v", payload.Payload.Usage)
	}
	if payload.Payload.Raw != "raw response" {
		t.Fatalf("payload = %#v, want usage and raw response", payload.Payload)
	}
}

func TestWriteEventLogSnakeCasePayloads(t *testing.T) {
	f, err := os.CreateTemp("", "clnkr-event-log")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(f.Name()) //nolint:errcheck
	defer f.Close()           //nolint:errcheck

	if err := WriteEventLog(f, clnkr.EventCommandStart{Command: "pwd", Dir: "/tmp"}); err != nil {
		t.Fatalf("WriteEventLog command_start: %v", err)
	}
	if err := WriteEventLog(f, clnkr.EventDebug{Message: "querying model..."}); err != nil {
		t.Fatalf("WriteEventLog debug: %v", err)
	}

	if _, err := f.Seek(0, 0); err != nil {
		t.Fatal(err)
	}
	decoder := json.NewDecoder(f)
	var start struct {
		Type    string                     `json:"type"`
		Payload map[string]json.RawMessage `json:"payload"`
	}
	if err := decoder.Decode(&start); err != nil {
		t.Fatalf("decode command_start event: %v", err)
	}
	if _, ok := start.Payload["command"]; start.Type != "command_start" || !ok {
		t.Fatalf("command_start payload = %#v, want snake-case command", start)
	}
	if _, ok := start.Payload["Command"]; ok {
		t.Fatalf("command_start payload has Go field name: %#v", start.Payload)
	}
	var debug struct {
		Type    string                     `json:"type"`
		Payload map[string]json.RawMessage `json:"payload"`
	}
	if err := decoder.Decode(&debug); err != nil {
		t.Fatalf("decode debug event: %v", err)
	}
	if _, ok := debug.Payload["message"]; debug.Type != "debug" || !ok {
		t.Fatalf("debug payload = %#v, want snake-case message", debug)
	}
	if _, ok := debug.Payload["Message"]; ok {
		t.Fatalf("debug payload has Go field name: %#v", debug.Payload)
	}
}

func TestWriteEventLogIncludesFeedback(t *testing.T) {
	f, err := os.CreateTemp("", "clnkr-event-log")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(f.Name()) //nolint:errcheck
	defer f.Close()           //nolint:errcheck

	if err := WriteEventLog(f, clnkr.EventCommandDone{
		Command:  "touch note.txt",
		ExitCode: 0,
		Feedback: clnkr.CommandFeedback{ChangedFiles: []string{"note.txt"}},
	}); err != nil {
		t.Fatalf("WriteEventLog: %v", err)
	}

	if _, err := f.Seek(0, 0); err != nil {
		t.Fatal(err)
	}
	var payload struct {
		Type    string `json:"type"`
		Payload struct {
			Feedback clnkr.CommandFeedback `json:"feedback"`
		} `json:"payload"`
	}
	if err := json.NewDecoder(f).Decode(&payload); err != nil {
		t.Fatalf("decode event log: %v", err)
	}
	if payload.Type != "command_done" {
		t.Fatalf("type = %q, want command_done", payload.Type)
	}
	if len(payload.Payload.Feedback.ChangedFiles) != 1 || payload.Payload.Feedback.ChangedFiles[0] != "note.txt" {
		t.Fatalf("feedback = %#v, want note.txt", payload.Payload.Feedback)
	}
}

func TestWriteEventLogCommandDoneErrOmittedWhenNil(t *testing.T) {
	f, err := os.CreateTemp("", "clnkr-event-log")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(f.Name()) //nolint:errcheck
	defer f.Close()           //nolint:errcheck

	if err := WriteEventLog(f, clnkr.EventCommandDone{Command: "true", ExitCode: 0}); err != nil {
		t.Fatalf("WriteEventLog: %v", err)
	}

	if _, err := f.Seek(0, 0); err != nil {
		t.Fatal(err)
	}
	var payload struct {
		Payload map[string]json.RawMessage `json:"payload"`
	}
	if err := json.NewDecoder(f).Decode(&payload); err != nil {
		t.Fatalf("decode event log: %v", err)
	}
	if _, ok := payload.Payload["err"]; ok {
		t.Fatalf("nil command error should omit err, got %#v", payload.Payload["err"])
	}
}

func TestWriteEventLogCommandDoneErrIncludedWhenPresent(t *testing.T) {
	f, err := os.CreateTemp("", "clnkr-event-log")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(f.Name()) //nolint:errcheck
	defer f.Close()           //nolint:errcheck

	if err := WriteEventLog(f, clnkr.EventCommandDone{Command: "false", ExitCode: 1, Err: errors.New("boom")}); err != nil {
		t.Fatalf("WriteEventLog: %v", err)
	}

	if _, err := f.Seek(0, 0); err != nil {
		t.Fatal(err)
	}
	var payload struct {
		Payload struct {
			Err string `json:"err"`
		} `json:"payload"`
	}
	if err := json.NewDecoder(f).Decode(&payload); err != nil {
		t.Fatalf("decode event log: %v", err)
	}
	if payload.Payload.Err != "boom" {
		t.Fatalf("err = %q, want boom", payload.Payload.Err)
	}
}

func TestWriteEventLogProtocolFailurePayload(t *testing.T) {
	f, err := os.CreateTemp("", "clnkr-event-log")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(f.Name()) //nolint:errcheck
	defer f.Close()           //nolint:errcheck

	if err := WriteEventLog(f, clnkr.EventProtocolFailure{Reason: "bad json", Raw: "nope"}); err != nil {
		t.Fatalf("WriteEventLog: %v", err)
	}

	if _, err := f.Seek(0, 0); err != nil {
		t.Fatal(err)
	}
	var payload struct {
		Type    string `json:"type"`
		Payload struct {
			Reason string `json:"reason"`
			Raw    string `json:"raw"`
		} `json:"payload"`
	}
	if err := json.NewDecoder(f).Decode(&payload); err != nil {
		t.Fatalf("decode event log: %v", err)
	}
	if payload.Type != "protocol_failure" || payload.Payload.Reason != "bad json" || payload.Payload.Raw != "nope" {
		t.Fatalf("payload = %#v, want protocol failure details", payload)
	}
}
