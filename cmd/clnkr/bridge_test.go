package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"

	clnkr "github.com/clnkr-ai/clnkr"
)

func TestEventBridgeDeliversEvents(t *testing.T) {
	ch := make(chan clnkr.Event, eventChSize)
	ch <- clnkr.EventDebug{Message: "hello"}

	cmd := eventBridge(ch)
	msg := cmd()

	em, ok := msg.(eventMsg)
	if !ok {
		t.Fatalf("expected eventMsg, got %T", msg)
	}
	dbg, ok := em.event.(clnkr.EventDebug)
	if !ok {
		t.Fatalf("expected EventDebug, got %T", em.event)
	}
	if dbg.Message != "hello" {
		t.Errorf("expected %q, got %q", "hello", dbg.Message)
	}
}

func TestEventBridgeReturnsDrainMsgOnClose(t *testing.T) {
	ch := make(chan clnkr.Event, eventChSize)
	close(ch)

	cmd := eventBridge(ch)
	msg := cmd()

	if _, ok := msg.(bridgeDrainedMsg); !ok {
		t.Fatalf("expected bridgeDrainedMsg, got %T", msg)
	}
}

func TestNotifyNonBlockingWhenFull(t *testing.T) {
	ch := make(chan clnkr.Event, 2)
	ch <- clnkr.EventDebug{Message: "1"}
	ch <- clnkr.EventDebug{Message: "2"}

	notify := makeNotify(ch, nil)
	notify(clnkr.EventDebug{Message: "dropped"})

	e1 := <-ch
	e2 := <-ch
	if e1.(clnkr.EventDebug).Message != "1" || e2.(clnkr.EventDebug).Message != "2" {
		t.Error("channel contents were modified")
	}
}

func TestNotifyWritesEventLogWhenChannelFull(t *testing.T) {
	ch := make(chan clnkr.Event, 1)
	ch <- clnkr.EventDebug{Message: "fill"}

	f, err := os.CreateTemp("", "eventlog")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(f.Name()) //nolint:errcheck
	defer f.Close()           //nolint:errcheck

	notify := makeNotify(ch, f)
	notify(clnkr.EventDebug{Message: "logged"})

	if _, err := f.Seek(0, 0); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 1024)
	n, _ := f.Read(buf)
	if n == 0 {
		t.Fatal("event log file is empty — write was dropped")
	}
	got := string(buf[:n])
	if got == "" {
		t.Fatal("event log write was empty")
	}
}

func TestWriteEventLogAllTypes(t *testing.T) {
	events := []struct {
		name     string
		event    clnkr.Event
		wantType string
	}{
		{"response", clnkr.EventResponse{
			Turn:  &clnkr.DoneTurn{Summary: "hello"},
			Usage: clnkr.Usage{InputTokens: 10, OutputTokens: 5},
		}, `"type":"response"`},
		{"command_start", clnkr.EventCommandStart{Command: "ls", Dir: "/tmp"}, `"type":"command_start"`},
		{"command_done_ok", clnkr.EventCommandDone{Command: "ls", Stdout: "file.txt", ExitCode: 0, Err: nil}, `"type":"command_done"`},
		{"command_done_feedback", clnkr.EventCommandDone{Command: "ls", ExitCode: 0, Feedback: clnkr.CommandFeedback{ChangedFiles: []string{"note.txt"}}}, `"feedback":{"changed_files":["note.txt"]}`},
		{"command_done_err", clnkr.EventCommandDone{Command: "false", ExitCode: 1, Err: fmt.Errorf("exit 1")}, `"err":"exit 1"`},
		{"protocol_failure", clnkr.EventProtocolFailure{Reason: "parse_error", Raw: "bad"}, `"type":"protocol_failure"`},
		{"debug", clnkr.EventDebug{Message: "test"}, `"type":"debug"`},
	}

	for _, tc := range events {
		t.Run(tc.name, func(t *testing.T) {
			f, err := os.CreateTemp("", "eventlog")
			if err != nil {
				t.Fatal(err)
			}
			defer os.Remove(f.Name()) //nolint:errcheck
			defer f.Close()           //nolint:errcheck

			writeEventLog(f, tc.event)

			if _, err := f.Seek(0, 0); err != nil {
				t.Fatal(err)
			}
			buf := make([]byte, 4096)
			n, _ := f.Read(buf)
			got := string(buf[:n])

			if !strings.Contains(got, tc.wantType) {
				t.Errorf("expected %q in output, got: %s", tc.wantType, got)
			}
			if !strings.HasSuffix(strings.TrimSpace(got), "}") {
				t.Errorf("expected JSON line ending with }, got: %s", got)
			}
		})
	}
}

func TestBridgeEventLogRoundTripTypedResponse(t *testing.T) {
	f, err := os.CreateTemp("", "eventlog")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(f.Name()) //nolint:errcheck
	defer f.Close()           //nolint:errcheck

	writeEventLog(f, clnkr.EventResponse{
		Turn:  &clnkr.DoneTurn{Summary: "typed summary"},
		Usage: clnkr.Usage{InputTokens: 12, OutputTokens: 7},
		Raw:   `{"turn":{"type":"done","summary":"provider raw"}}`,
	})

	if _, err := f.Seek(0, 0); err != nil {
		t.Fatal(err)
	}
	var line struct {
		Type    string `json:"type"`
		Payload struct {
			Turn  json.RawMessage `json:"turn"`
			Usage clnkr.Usage     `json:"usage"`
			Raw   string          `json:"raw"`
		} `json:"payload"`
	}
	if err := json.NewDecoder(f).Decode(&line); err != nil {
		t.Fatalf("decode event log: %v", err)
	}
	if line.Type != "response" {
		t.Fatalf("type = %q, want response", line.Type)
	}
	turn, err := clnkr.ParseTurn(string(line.Payload.Turn))
	if err != nil {
		t.Fatalf("ParseTurn(payload.turn): %v", err)
	}
	done, ok := turn.(*clnkr.DoneTurn)
	if !ok {
		t.Fatalf("payload turn = %T, want *DoneTurn", turn)
	}
	if done.Summary != "typed summary" {
		t.Fatalf("summary = %q, want %q", done.Summary, "typed summary")
	}
	if line.Payload.Usage.InputTokens != 12 || line.Payload.Usage.OutputTokens != 7 {
		t.Fatalf("usage = %+v, want 12/7", line.Payload.Usage)
	}
	if line.Payload.Raw != `{"turn":{"type":"done","summary":"provider raw"}}` {
		t.Fatalf("raw = %q", line.Payload.Raw)
	}
}
