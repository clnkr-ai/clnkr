package session_test

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/clnkr-ai/clnkr"
	"github.com/clnkr-ai/clnkr/cmd/internal/session"
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
	if got1 != "ldz77nnq4ybk5nbc" {
		t.Errorf("NormalizeProjectPath changed storage key: got %q", got1)
	}
	if got1 == regressionHexProjectKey("/home/user/projects/myapp") {
		t.Errorf("NormalizeProjectPath returned regression-window hex key %q", got1)
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

	want := "/tmp/test-state/clnkr/projects/"
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
	original := []clnkr.Message{
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
		msgs := []clnkr.Message{
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

func TestLoadLatestSessionOrdersEqualCreatedLegacyNamesBySequence(t *testing.T) {
	tmpdir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", tmpdir)

	projectDir := "/tmp/test-project-legacy-order"
	dir, err := session.SessionDir(projectDir)
	if err != nil {
		t.Fatalf("SessionDir: %v", err)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	files := map[string]string{
		"9-2026-01-10T000000.000000Z.json":  `{"created":"2026-01-10T00:00:00Z","messages":[{"role":"user","content":"old"}]}`,
		"10-2026-01-10T000000.000000Z.json": `{"created":"2026-01-10T00:00:00Z","messages":[{"role":"user","content":"new"}]}`,
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
			t.Fatalf("WriteFile %s: %v", name, err)
		}
	}

	loaded, err := session.LoadLatestSession(projectDir)
	if err != nil {
		t.Fatalf("LoadLatestSession: %v", err)
	}
	if len(loaded) != 1 || loaded[0].Content != "new" {
		t.Fatalf("latest session = %#v, want new", loaded)
	}
	sessions, err := session.ListSessions(projectDir)
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(sessions) != 2 || sessions[0].Filename != "10-2026-01-10T000000.000000Z.json" {
		t.Fatalf("sessions = %#v, want highest legacy sequence first", sessions)
	}
}

func TestLoadLatestSessionOrdersEqualTimestampLegacyNamesBySequenceBeforeCreatedTime(t *testing.T) {
	tmpdir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", tmpdir)

	projectDir := "/tmp/test-project-legacy-sequence"
	dir, err := session.SessionDir(projectDir)
	if err != nil {
		t.Fatalf("SessionDir: %v", err)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	files := map[string]string{
		"9-2026-01-10T000000.000000Z.json":  `{"created":"2026-01-11T00:00:00Z","messages":[{"role":"user","content":"old"}]}`,
		"10-2026-01-10T000000.000000Z.json": `{"created":"2026-01-10T00:00:00Z","messages":[{"role":"user","content":"new"}]}`,
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
			t.Fatalf("WriteFile %s: %v", name, err)
		}
	}

	loaded, err := session.LoadLatestSession(projectDir)
	if err != nil {
		t.Fatalf("LoadLatestSession: %v", err)
	}
	if len(loaded) != 1 || loaded[0].Content != "new" {
		t.Fatalf("latest session = %#v, want highest equal-timestamp legacy sequence", loaded)
	}
	sessions, err := session.ListSessions(projectDir)
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(sessions) != 2 || sessions[0].Filename != "10-2026-01-10T000000.000000Z.json" {
		t.Fatalf("sessions = %#v, want highest equal-timestamp legacy sequence first", sessions)
	}
}

func TestLoadLatestSessionOrdersMixedFormatsByFilenameTime(t *testing.T) {
	tmpdir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", tmpdir)

	projectDir := "/tmp/test-project-mixed-transitive-order"
	dir, err := session.SessionDir(projectDir)
	if err != nil {
		t.Fatalf("SessionDir: %v", err)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	files := map[string]string{
		"10-2026-01-01T000000.000000Z.json":     `{"created":"2026-01-01T00:00:00Z","messages":[{"role":"user","content":"legacy-high"}]}`,
		"9-2026-01-03T000000.000000Z.json":      `{"created":"2026-01-03T00:00:00Z","messages":[{"role":"user","content":"legacy-low"}]}`,
		"2026-01-02T000000.000000000Z-000.json": `{"created":"2026-01-02T00:00:00Z","messages":[{"role":"user","content":"current"}]}`,
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
			t.Fatalf("WriteFile %s: %v", name, err)
		}
	}

	loaded, err := session.LoadLatestSession(projectDir)
	if err != nil {
		t.Fatalf("LoadLatestSession: %v", err)
	}
	if len(loaded) != 1 || loaded[0].Content != "legacy-low" {
		t.Fatalf("latest session = %#v, want newest filename timestamp", loaded)
	}
	sessions, err := session.ListSessions(projectDir)
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	want := []string{
		"9-2026-01-03T000000.000000Z.json",
		"2026-01-02T000000.000000000Z-000.json",
		"10-2026-01-01T000000.000000Z.json",
	}
	if len(sessions) != len(want) {
		t.Fatalf("sessions = %#v, want %d sessions", sessions, len(want))
	}
	for i := range want {
		if sessions[i].Filename != want[i] {
			t.Fatalf("sessions[%d] = %q, want %q; sessions = %#v", i, sessions[i].Filename, want[i], sessions)
		}
	}
}

func TestLoadLatestSessionToleratesMalformedCreatedMetadata(t *testing.T) {
	tmpdir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", tmpdir)

	projectDir := "/tmp/test-project-bad-created"
	dir, err := session.SessionDir(projectDir)
	if err != nil {
		t.Fatalf("SessionDir: %v", err)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	files := map[string]string{
		"2026-01-12T000000.000000000Z-000.json": `{"created":"not-a-time","messages":[{"role":"user","content":"new"}]}`,
		"2026-01-11T000000.000000000Z-000.json": `{"created":"2026-01-11T00:00:00Z","messages":[{"role":"user","content":"old"}]}`,
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
			t.Fatalf("WriteFile %s: %v", name, err)
		}
	}

	loaded, err := session.LoadLatestSession(projectDir)
	if err != nil {
		t.Fatalf("LoadLatestSession: %v", err)
	}
	if len(loaded) != 1 || loaded[0].Content != "new" {
		t.Fatalf("latest session = %#v, want session with malformed created metadata", loaded)
	}
	sessions, err := session.ListSessions(projectDir)
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(sessions) != 2 || sessions[0].Filename != "2026-01-12T000000.000000000Z-000.json" {
		t.Fatalf("sessions = %#v, want malformed-created session listed first", sessions)
	}
	if !sessions[0].Created.IsZero() {
		t.Fatalf("sessions[0].Created = %v, want zero time for malformed created metadata", sessions[0].Created)
	}
}

func TestLoadLatestSessionOrdersCurrentNamesBySuffix(t *testing.T) {
	tmpdir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", tmpdir)

	projectDir := "/tmp/test-project-current-order"
	dir, err := session.SessionDir(projectDir)
	if err != nil {
		t.Fatalf("SessionDir: %v", err)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	files := map[string]string{
		"2026-01-10T000000.000000000Z-000.json": `{"created":"2026-01-10T00:00:00Z","messages":[{"role":"user","content":"old"}]}`,
		"2026-01-10T000000.000000000Z-001.json": `{"created":"2026-01-10T00:00:00Z","messages":[{"role":"user","content":"new"}]}`,
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
			t.Fatalf("WriteFile %s: %v", name, err)
		}
	}

	loaded, err := session.LoadLatestSession(projectDir)
	if err != nil {
		t.Fatalf("LoadLatestSession: %v", err)
	}
	if len(loaded) != 1 || loaded[0].Content != "new" {
		t.Fatalf("latest session = %#v, want new", loaded)
	}
}

func TestLoadLatestSessionReadsRegressionHexSessionDir(t *testing.T) {
	tmpdir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", tmpdir)

	projectDir := "/tmp/test-project-regression-hex"
	dir := filepath.Join(tmpdir, "clnkr", "projects", regressionHexProjectKey(projectDir))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	name := "2026-01-10T000000.000000000Z-000.json"
	content := `{"created":"2026-01-10T00:00:00Z","messages":[{"role":"user","content":"hex"}]}`
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	loaded, err := session.LoadLatestSession(projectDir)
	if err != nil {
		t.Fatalf("LoadLatestSession: %v", err)
	}
	if len(loaded) != 1 || loaded[0].Content != "hex" {
		t.Fatalf("latest session = %#v, want regression hex session", loaded)
	}
	sessions, err := session.ListSessions(projectDir)
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(sessions) != 1 || sessions[0].Filename != name {
		t.Fatalf("sessions = %#v, want regression hex session", sessions)
	}
}

func TestSaveSessionWritesCanonicalDirWhileReadingRegressionHexDir(t *testing.T) {
	tmpdir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", tmpdir)

	projectDir := "/tmp/test-project-save-canonical"
	msgs := []clnkr.Message{{Role: "user", Content: "canonical"}}
	if err := session.SaveSession(projectDir, msgs); err != nil {
		t.Fatalf("SaveSession: %v", err)
	}

	canonicalDir, err := session.SessionDir(projectDir)
	if err != nil {
		t.Fatalf("SessionDir: %v", err)
	}
	canonicalFiles, err := filepath.Glob(filepath.Join(canonicalDir, "*.json"))
	if err != nil || len(canonicalFiles) != 1 {
		t.Fatalf("canonical files = %d, err = %v; want 1", len(canonicalFiles), err)
	}

	hexDir := filepath.Join(tmpdir, "clnkr", "projects", regressionHexProjectKey(projectDir))
	hexFiles, err := filepath.Glob(filepath.Join(hexDir, "*.json"))
	if err != nil || len(hexFiles) != 0 {
		t.Fatalf("regression hex files = %d, err = %v; want 0", len(hexFiles), err)
	}
}

func TestLoadLatestSessionIgnoresCorruptOlderLegacyWhenCurrentSessionIsNewest(t *testing.T) {
	tmpdir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", tmpdir)

	projectDir := "/tmp/test-project-mixed-corrupt-legacy"
	dir, err := session.SessionDir(projectDir)
	if err != nil {
		t.Fatalf("SessionDir: %v", err)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	files := map[string]string{
		"9-2026-01-10T000000.000000Z.json":      `{`,
		"2026-01-11T000000.000000000Z-000.json": `{"created":"2026-01-11T00:00:00Z","messages":[{"role":"user","content":"new"}]}`,
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
			t.Fatalf("WriteFile %s: %v", name, err)
		}
	}

	loaded, err := session.LoadLatestSession(projectDir)
	if err != nil {
		t.Fatalf("LoadLatestSession: %v", err)
	}
	if len(loaded) != 1 || loaded[0].Content != "new" {
		t.Fatalf("latest session = %#v, want current session", loaded)
	}
}

func TestLoadLatestSessionFailsOnCorruptNewestSession(t *testing.T) {
	tmpdir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", tmpdir)

	projectDir := "/tmp/test-project-corrupt-latest"
	dir, err := session.SessionDir(projectDir)
	if err != nil {
		t.Fatalf("SessionDir: %v", err)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	files := map[string]string{
		"2026-01-10T000000.000000000Z-000.json": `{"created":"2026-01-10T00:00:00Z","messages":[{"role":"user","content":"old"}]}`,
		"2026-01-10T000000.000000000Z-001.json": `{`,
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
			t.Fatalf("WriteFile %s: %v", name, err)
		}
	}

	_, err = session.LoadLatestSession(projectDir)
	if err == nil {
		t.Fatal("LoadLatestSession succeeded, want corrupt latest session error")
	}
	if !strings.Contains(err.Error(), "2026-01-10T000000.000000000Z-001.json") {
		t.Fatalf("error = %v, want corrupt latest filename", err)
	}
}

func TestLoadLatestSessionIgnoresCorruptOlderSession(t *testing.T) {
	tmpdir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", tmpdir)

	projectDir := "/tmp/test-project-corrupt-older"
	dir, err := session.SessionDir(projectDir)
	if err != nil {
		t.Fatalf("SessionDir: %v", err)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	files := map[string]string{
		"2026-01-10T000000.000000000Z-000.json": `{`,
		"2026-01-10T000000.000000000Z-001.json": `{"created":"2026-01-10T00:00:00Z","messages":[{"role":"user","content":"new"}]}`,
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
			t.Fatalf("WriteFile %s: %v", name, err)
		}
	}

	loaded, err := session.LoadLatestSession(projectDir)
	if err != nil {
		t.Fatalf("LoadLatestSession: %v", err)
	}
	if len(loaded) != 1 || loaded[0].Content != "new" {
		t.Fatalf("latest session = %#v, want new", loaded)
	}
}

func TestListSessions(t *testing.T) {
	tmpdir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", tmpdir)

	projectDir := "/tmp/test-project-list"

	for i := 0; i < 3; i++ {
		msgs := []clnkr.Message{
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
	want := filepath.Join(home, ".local", "state", "clnkr", "projects")
	if len(dir) <= len(want) || dir[:len(want)] != want {
		t.Errorf("SessionDir without XDG: got %q, want prefix %q", dir, want)
	}
}

func regressionHexProjectKey(path string) string {
	hash := sha256.Sum256([]byte(path))
	return fmt.Sprintf("%x", hash[:8])
}
