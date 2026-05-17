package clnkrapp

import (
	"reflect"
	"testing"

	"github.com/clnkr-ai/clnkr"
	"github.com/clnkr-ai/clnkr/internal/session"
)

func TestSessionInfoIsClnkrappOwned(t *testing.T) {
	if reflect.TypeOf(SessionInfo{}) == reflect.TypeOf(session.SessionInfo{}) {
		t.Fatalf("SessionInfo aliases internal/session.SessionInfo, want clnkrapp-owned type")
	}
}

func TestSaveSessionReturnsDirectoryAndListSessions(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	cwd := t.TempDir()
	messages := []clnkr.Message{{Role: "user", Content: "hello"}}

	wantDir, err := session.SessionDir(cwd)
	if err != nil {
		t.Fatalf("SessionDir: %v", err)
	}
	gotDir, err := SaveSession(cwd, messages, RunMetadata{})
	if err != nil {
		t.Fatalf("SaveSession: %v", err)
	}
	if gotDir != wantDir {
		t.Fatalf("SaveSession dir = %q, want %q", gotDir, wantDir)
	}

	sessions, err := ListSessions(cwd)
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(sessions) != 1 {
		t.Fatalf("sessions = %#v, want one session", sessions)
	}
	if sessions[0].Filename == "" || sessions[0].Messages != 1 || sessions[0].Created.IsZero() {
		t.Fatalf(
			"session info = %#v, want filename, one message, and created timestamp",
			sessions[0],
		)
	}
}
