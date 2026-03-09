package main

import (
	"fmt"
	"os"
	"strings"
	"testing"

	hew "github.com/cosgroveb/hew"
)

func TestEventBridgeDeliversEvents(t *testing.T) {
	ch := make(chan hew.Event, eventChSize)
	ch <- hew.EventDebug{Message: "hello"}

	cmd := eventBridge(ch)
	msg := cmd()

	em, ok := msg.(eventMsg)
	if !ok {
		t.Fatalf("expected eventMsg, got %T", msg)
	}
	dbg, ok := em.event.(hew.EventDebug)
	if !ok {
		t.Fatalf("expected EventDebug, got %T", em.event)
	}
	if dbg.Message != "hello" {
		t.Errorf("expected %q, got %q", "hello", dbg.Message)
	}
}

func TestEventBridgeReturnsNilOnClose(t *testing.T) {
	ch := make(chan hew.Event, eventChSize)
	close(ch)

	cmd := eventBridge(ch)
	msg := cmd()

	if msg != nil {
		t.Fatalf("expected nil, got %T", msg)
	}
}

func TestNotifyNonBlockingWhenFull(t *testing.T) {
	ch := make(chan hew.Event, 2)
	ch <- hew.EventDebug{Message: "1"}
	ch <- hew.EventDebug{Message: "2"}

	notify := makeNotify(ch, nil)
	notify(hew.EventDebug{Message: "dropped"})

	e1 := <-ch
	e2 := <-ch
	if e1.(hew.EventDebug).Message != "1" || e2.(hew.EventDebug).Message != "2" {
		t.Error("channel contents were modified")
	}
}

func TestNotifyWritesEventLogWhenChannelFull(t *testing.T) {
	ch := make(chan hew.Event, 1)
	ch <- hew.EventDebug{Message: "fill"}

	f, err := os.CreateTemp("", "eventlog")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(f.Name()) //nolint:errcheck
	defer f.Close()           //nolint:errcheck

	notify := makeNotify(ch, f)
	notify(hew.EventDebug{Message: "logged"})

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
		event    hew.Event
		wantType string
	}{
		{"response", hew.EventResponse{
			Message: hew.Message{Role: "assistant", Content: "hello"},
			Usage:   hew.Usage{InputTokens: 10, OutputTokens: 5},
		}, `"type":"response"`},
		{"command_start", hew.EventCommandStart{Command: "ls", Dir: "/tmp"}, `"type":"command_start"`},
		{"command_done_ok", hew.EventCommandDone{Output: "file.txt", Err: nil}, `"type":"command_done"`},
		{"command_done_err", hew.EventCommandDone{Output: "", Err: fmt.Errorf("exit 1")}, `"err":"exit 1"`},
		{"format_error", hew.EventFormatError{}, `"type":"format_error"`},
		{"debug", hew.EventDebug{Message: "test"}, `"type":"debug"`},
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
