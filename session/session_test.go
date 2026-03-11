package session_test

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/cosgroveb/hew"
	"github.com/cosgroveb/hew/session"
)

func TestNormalizeProjectPath(t *testing.T) {
	// Deterministic: same path always gives same hash
	got1, err := session.NormalizeProjectPath("/home/user/projects/myapp")
	if err != nil {
		t.Fatalf("NormalizeProjectPath: %v", err)
	}
	got2, err := session.NormalizeProjectPath("/home/user/projects/myapp")
	if err != nil {
		t.Fatalf("NormalizeProjectPath: %v", err)
	}
	if got1 != got2 {
		t.Errorf("same path gave different hashes: %q vs %q", got1, got2)
	}
	if len(got1) != 16 {
		t.Errorf("expected 16-char hash, got %d: %q", len(got1), got1)
	}

	// Different paths give different hashes
	got3, err := session.NormalizeProjectPath("/tmp/foo")
	if err != nil {
		t.Fatalf("NormalizeProjectPath: %v", err)
	}
	if got1 == got3 {
		t.Errorf("different paths gave same hash: %q", got1)
	}

	// Relative path fails
	_, err = session.NormalizeProjectPath("relative/path")
	if err == nil {
		t.Error("expected error for relative path")
	}

	// Empty path fails
	_, err = session.NormalizeProjectPath("")
	if err == nil {
		t.Error("expected error for empty path")
	}
}

func TestSessionDir(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", "/tmp/test-state")

	dir, err := session.SessionDir("/home/user/projects/myapp")
	if err != nil {
		t.Fatalf("SessionDir: %v", err)
	}

	want := "/tmp/test-state/hew/projects/"
	if len(dir) <= len(want) || dir[:len(want)] != want {
		t.Errorf("SessionDir: got %q, want prefix %q", dir, want)
	}
}

func TestSaveAndLoadSession(t *testing.T) {
	tmpdir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", tmpdir)

	projectDir := "/tmp/test-project-save"

	// No sessions yet
	msgs, err := session.LoadLatestSession(projectDir)
	if err != nil {
		t.Fatalf("LoadLatestSession (empty): %v", err)
	}
	if msgs != nil {
		t.Errorf("LoadLatestSession (empty): got %v, want nil", msgs)
	}

	// Save a session
	original := []hew.Message{
		{Role: "user", Content: "hello"},
		{Role: "assistant", Content: "hi there"},
	}
	if err := session.SaveSession(projectDir, original); err != nil {
		t.Fatalf("SaveSession: %v", err)
	}

	// Verify file was created
	dir, _ := session.SessionDir(projectDir)
	files, err := filepath.Glob(filepath.Join(dir, "*.json"))
	if err != nil || len(files) != 1 {
		t.Fatalf("expected 1 session file, got %d (err: %v)", len(files), err)
	}

	// Load it back
	loaded, err := session.LoadLatestSession(projectDir)
	if err != nil {
		t.Fatalf("LoadLatestSession: %v", err)
	}
	if len(loaded) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(loaded))
	}
	if loaded[0].Role != "user" || loaded[0].Content != "hello" {
		t.Errorf("first message: got %+v", loaded[0])
	}
	if loaded[1].Role != "assistant" || loaded[1].Content != "hi there" {
		t.Errorf("second message: got %+v", loaded[1])
	}
}

func TestSaveMultipleAndLoadLatest(t *testing.T) {
	tmpdir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", tmpdir)

	projectDir := "/tmp/test-project-multi"

	for i := 0; i < 3; i++ {
		msgs := []hew.Message{
			{Role: "user", Content: fmt.Sprintf("msg %d", i)},
		}
		if err := session.SaveSession(projectDir, msgs); err != nil {
			t.Fatalf("SaveSession %d: %v", i, err)
		}
		time.Sleep(10 * time.Millisecond)
	}

	// Load latest should get the last one (msg 2)
	loaded, err := session.LoadLatestSession(projectDir)
	if err != nil {
		t.Fatalf("LoadLatestSession: %v", err)
	}
	if len(loaded) != 1 || loaded[0].Content != "msg 2" {
		t.Errorf("expected latest session with 'msg 2', got %+v", loaded)
	}
}

func TestListSessions(t *testing.T) {
	tmpdir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", tmpdir)

	projectDir := "/tmp/test-project-list"

	for i := 0; i < 3; i++ {
		msgs := []hew.Message{
			{Role: "user", Content: fmt.Sprintf("msg %d", i)},
		}
		if err := session.SaveSession(projectDir, msgs); err != nil {
			t.Fatalf("SaveSession %d: %v", i, err)
		}
		time.Sleep(10 * time.Millisecond)
	}

	sessions, err := session.ListSessions(projectDir)
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(sessions) != 3 {
		t.Errorf("expected 3 sessions, got %d", len(sessions))
	}

	// Should be sorted newest first
	if len(sessions) >= 2 && sessions[0].Created.Before(sessions[1].Created) {
		t.Error("sessions not sorted newest-first")
	}
}

func TestListSessionsEmpty(t *testing.T) {
	tmpdir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", tmpdir)

	sessions, err := session.ListSessions("/tmp/nonexistent-project")
	if err != nil {
		t.Fatalf("ListSessions (empty): %v", err)
	}
	if sessions != nil {
		t.Errorf("expected nil, got %v", sessions)
	}
}

func TestSessionDirDefaultsToLocalState(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", "")
	// Unset XDG_STATE_HOME to test fallback
	_ = os.Unsetenv("XDG_STATE_HOME")

	dir, err := session.SessionDir("/tmp/test")
	if err != nil {
		t.Fatalf("SessionDir: %v", err)
	}

	home, _ := os.UserHomeDir()
	want := filepath.Join(home, ".local", "state", "hew", "projects")
	if len(dir) <= len(want) || dir[:len(want)] != want {
		t.Errorf("SessionDir without XDG: got %q, want prefix %q", dir, want)
	}
}
