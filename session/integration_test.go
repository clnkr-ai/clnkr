package session_test

import (
	"path/filepath"
	"testing"

	"github.com/cosgroveb/hew"
	"github.com/cosgroveb/hew/session"
)

func TestSessionRoundTrip(t *testing.T) {
	tmpdir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", tmpdir)

	projdir := filepath.Join(tmpdir, "testproj")

	// No sessions exist initially
	sessions, err := session.ListSessions(projdir)
	if err != nil {
		t.Fatalf("ListSessions (empty): %v", err)
	}
	if len(sessions) != 0 {
		t.Fatalf("Expected no sessions, got %d", len(sessions))
	}

	// Save a session
	testMsgs := []hew.Message{
		{Role: "user", Content: "test prompt"},
		{Role: "assistant", Content: "test response"},
	}
	if err := session.SaveSession(projdir, testMsgs); err != nil {
		t.Fatalf("SaveSession: %v", err)
	}

	// Load it back
	loaded, err := session.LoadLatestSession(projdir)
	if err != nil {
		t.Fatalf("LoadLatestSession: %v", err)
	}
	if len(loaded) != 2 {
		t.Fatalf("Expected 2 messages, got %d", len(loaded))
	}
	if loaded[0].Content != "test prompt" {
		t.Errorf("First message wrong: %q", loaded[0].Content)
	}
	if loaded[1].Content != "test response" {
		t.Errorf("Second message wrong: %q", loaded[1].Content)
	}

	// List shows one session
	sessions, err = session.ListSessions(projdir)
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(sessions) != 1 {
		t.Fatalf("Expected 1 session, got %d", len(sessions))
	}
	if sessions[0].Messages != 2 {
		t.Errorf("Expected 2 messages in session info, got %d", sessions[0].Messages)
	}
}

func TestSessionIsolationBetweenProjects(t *testing.T) {
	tmpdir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", tmpdir)

	proj1 := "/tmp/project-alpha"
	proj2 := "/tmp/project-beta"

	// Save to project 1
	if err := session.SaveSession(proj1, []hew.Message{
		{Role: "user", Content: "alpha"},
	}); err != nil {
		t.Fatalf("SaveSession proj1: %v", err)
	}

	// Save to project 2
	if err := session.SaveSession(proj2, []hew.Message{
		{Role: "user", Content: "beta"},
	}); err != nil {
		t.Fatalf("SaveSession proj2: %v", err)
	}

	// Each project sees only its own sessions
	s1, _ := session.ListSessions(proj1)
	s2, _ := session.ListSessions(proj2)

	if len(s1) != 1 || len(s2) != 1 {
		t.Fatalf("Expected 1 session each, got proj1=%d, proj2=%d", len(s1), len(s2))
	}

	// Load from each project returns correct data
	m1, _ := session.LoadLatestSession(proj1)
	m2, _ := session.LoadLatestSession(proj2)

	if m1[0].Content != "alpha" {
		t.Errorf("proj1 loaded wrong content: %q", m1[0].Content)
	}
	if m2[0].Content != "beta" {
		t.Errorf("proj2 loaded wrong content: %q", m2[0].Content)
	}
}
