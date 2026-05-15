package session_test

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/clnkr-ai/clnkr"
	"github.com/clnkr-ai/clnkr/internal/session"
)

func TestNormalizeProjectPath(t *testing.T) {
	got, err := session.NormalizeProjectPath("/home/user/projects/myapp")
	if err != nil {
		t.Fatalf("NormalizeProjectPath: %v", err)
	}
	again, err := session.NormalizeProjectPath("/home/user/projects/myapp")
	if err != nil {
		t.Fatalf("NormalizeProjectPath again: %v", err)
	}
	if got != again || got != "ldz77nnq4ybk5nbc" || len(got) != 16 {
		t.Fatalf("NormalizeProjectPath = %q then %q, want stable 16-char key ldz77nnq4ybk5nbc", got, again)
	}
	if got == regressionHexProjectKey("/home/user/projects/myapp") {
		t.Fatalf("NormalizeProjectPath returned regression-window hex key %q", got)
	}
	different, err := session.NormalizeProjectPath("/tmp/foo")
	if err != nil {
		t.Fatalf("NormalizeProjectPath different path: %v", err)
	}
	if different == got {
		t.Fatalf("different paths gave same hash %q", got)
	}

	for _, path := range []string{"relative/path", ""} {
		if _, err := session.NormalizeProjectPath(path); err == nil {
			t.Fatalf("NormalizeProjectPath(%q) succeeded, want error", path)
		}
	}
}

func TestSessionDir(t *testing.T) {
	t.Run("uses XDG_STATE_HOME", func(t *testing.T) {
		t.Setenv("XDG_STATE_HOME", "/tmp/test-state")
		assertSessionDirPrefix(t, "/home/user/projects/myapp", "/tmp/test-state/clnkr/projects")
	})

	t.Run("defaults to local state", func(t *testing.T) {
		t.Setenv("XDG_STATE_HOME", "")
		_ = os.Unsetenv("XDG_STATE_HOME")

		home, err := os.UserHomeDir()
		if err != nil {
			t.Fatalf("UserHomeDir: %v", err)
		}
		assertSessionDirPrefix(t, "/tmp/test", filepath.Join(home, ".local", "state", "clnkr", "projects"))
	})
}

func TestSaveLoadAndListSessions(t *testing.T) {
	tmpdir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", tmpdir)
	projectDir := "/tmp/test-project-save"

	loaded, err := session.LoadLatestSession(projectDir)
	if err != nil {
		t.Fatalf("LoadLatestSession empty: %v", err)
	}
	if loaded != nil {
		t.Fatalf("LoadLatestSession empty = %#v, want nil", loaded)
	}
	listed, err := session.ListSessions(projectDir)
	if err != nil {
		t.Fatalf("ListSessions empty: %v", err)
	}
	if listed != nil {
		t.Fatalf("ListSessions empty = %#v, want nil", listed)
	}

	first := []clnkr.Message{{Role: "user", Content: "hello"}, {Role: "assistant", Content: "hi there"}}
	if err := session.SaveSession(projectDir, first); err != nil {
		t.Fatalf("SaveSession first: %v", err)
	}
	assertSessionFileCount(t, projectDir, 1)
	loaded, err = session.LoadLatestSession(projectDir)
	if err != nil {
		t.Fatalf("LoadLatestSession first: %v", err)
	}
	if !reflect.DeepEqual(loaded, first) {
		t.Fatalf("loaded = %#v, want %#v", loaded, first)
	}

	for i := 1; i < 3; i++ {
		if err := session.SaveSession(projectDir, []clnkr.Message{{Role: "user", Content: fmt.Sprintf("msg %d", i)}}); err != nil {
			t.Fatalf("SaveSession %d: %v", i, err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	assertLatestContent(t, projectDir, "msg 2")

	listed, err = session.ListSessions(projectDir)
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(listed) != 3 {
		t.Fatalf("ListSessions returned %d sessions, want 3: %#v", len(listed), listed)
	}
	if listed[0].Created.Before(listed[1].Created) {
		t.Fatalf("ListSessions = %#v, want newest first", listed)
	}
}

func TestSaveSessionWithMetadataPreservesLoadCompatibility(t *testing.T) {
	tmpdir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", tmpdir)

	projectDir := "/tmp/test-project-save-metadata"
	original := []clnkr.Message{{Role: "user", Content: "hello"}}
	if err := session.SaveSessionWithMetadata(projectDir, original, map[string]any{"provider": "openai", "max_output_tokens": 8000}); err != nil {
		t.Fatalf("SaveSessionWithMetadata: %v", err)
	}

	var gotFile struct {
		Metadata map[string]any  `json:"metadata"`
		Messages []clnkr.Message `json:"messages"`
	}
	readOnlySessionFile(t, projectDir, &gotFile)
	if gotFile.Metadata["provider"] != "openai" || gotFile.Metadata["max_output_tokens"] != float64(8000) {
		t.Fatalf("metadata = %#v, want provider and max_output_tokens", gotFile.Metadata)
	}

	loaded, err := session.LoadLatestSession(projectDir)
	if err != nil {
		t.Fatalf("LoadLatestSession: %v", err)
	}
	if !reflect.DeepEqual(loaded, original) {
		t.Fatalf("loaded = %#v, want %#v", loaded, original)
	}
}

func TestLoadLatestSessionOrdersBySessionFilename(t *testing.T) {
	tests := []struct {
		name            string
		files           map[string]string
		wantLatest      string
		wantOrder       []string
		wantCreatedZero bool
	}{
		{
			name: "legacy equal created names use highest sequence",
			files: map[string]string{
				"9-2026-01-10T000000.000000Z.json":  sessionJSON("2026-01-10T00:00:00Z", "old"),
				"10-2026-01-10T000000.000000Z.json": sessionJSON("2026-01-10T00:00:00Z", "new"),
			},
			wantLatest: "new",
			wantOrder:  []string{"10-2026-01-10T000000.000000Z.json", "9-2026-01-10T000000.000000Z.json"},
		},
		{
			name: "legacy filename sequence beats conflicting created metadata",
			files: map[string]string{
				"9-2026-01-10T000000.000000Z.json":  sessionJSON("2026-01-11T00:00:00Z", "old"),
				"10-2026-01-10T000000.000000Z.json": sessionJSON("2026-01-10T00:00:00Z", "new"),
			},
			wantLatest: "new",
			wantOrder:  []string{"10-2026-01-10T000000.000000Z.json", "9-2026-01-10T000000.000000Z.json"},
		},
		{
			name: "mixed current and legacy names use filename time",
			files: map[string]string{
				"10-2026-01-01T000000.000000Z.json":     sessionJSON("2026-01-01T00:00:00Z", "legacy-high"),
				"9-2026-01-03T000000.000000Z.json":      sessionJSON("2026-01-03T00:00:00Z", "legacy-low"),
				"2026-01-02T000000.000000000Z-000.json": sessionJSON("2026-01-02T00:00:00Z", "current"),
			},
			wantLatest: "legacy-low",
			wantOrder: []string{
				"9-2026-01-03T000000.000000Z.json",
				"2026-01-02T000000.000000000Z-000.json",
				"10-2026-01-01T000000.000000Z.json",
			},
		},
		{
			name: "malformed created metadata does not override filename order",
			files: map[string]string{
				"2026-01-12T000000.000000000Z-000.json": sessionJSON("not-a-time", "new"),
				"2026-01-11T000000.000000000Z-000.json": sessionJSON("2026-01-11T00:00:00Z", "old"),
			},
			wantLatest:      "new",
			wantOrder:       []string{"2026-01-12T000000.000000000Z-000.json", "2026-01-11T000000.000000000Z-000.json"},
			wantCreatedZero: true,
		},
		{
			name: "current equal timestamp names use suffix sequence",
			files: map[string]string{
				"2026-01-10T000000.000000000Z-000.json": sessionJSON("2026-01-10T00:00:00Z", "old"),
				"2026-01-10T000000.000000000Z-001.json": sessionJSON("2026-01-10T00:00:00Z", "new"),
			},
			wantLatest: "new",
			wantOrder:  []string{"2026-01-10T000000.000000000Z-001.json", "2026-01-10T000000.000000000Z-000.json"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			projectDir := setupSessionFiles(t, tc.files)
			assertLatestContent(t, projectDir, tc.wantLatest)
			assertListOrder(t, projectDir, tc.wantOrder)
			if tc.wantCreatedZero {
				sessions, err := session.ListSessions(projectDir)
				if err != nil {
					t.Fatalf("ListSessions: %v", err)
				}
				if !sessions[0].Created.IsZero() {
					t.Fatalf("sessions[0].Created = %v, want zero time for malformed created metadata", sessions[0].Created)
				}
			}
		})
	}
}

func TestLoadLatestSessionReadsRegressionHexSessionDir(t *testing.T) {
	tmpdir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", tmpdir)

	projectDir := "/tmp/test-project-regression-hex"
	dir := filepath.Join(tmpdir, "clnkr", "projects", regressionHexProjectKey(projectDir))
	writeFile(t, dir, "2026-01-10T000000.000000000Z-000.json", sessionJSON("2026-01-10T00:00:00Z", "hex"))

	assertLatestContent(t, projectDir, "hex")
	assertListOrder(t, projectDir, []string{"2026-01-10T000000.000000000Z-000.json"})
}

func TestSaveSessionWritesCanonicalDirWhileReadingRegressionHexDir(t *testing.T) {
	tmpdir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", tmpdir)

	projectDir := "/tmp/test-project-save-canonical"
	if err := session.SaveSession(projectDir, []clnkr.Message{{Role: "user", Content: "canonical"}}); err != nil {
		t.Fatalf("SaveSession: %v", err)
	}
	assertSessionFileCount(t, projectDir, 1)

	hexFiles, err := filepath.Glob(filepath.Join(tmpdir, "clnkr", "projects", regressionHexProjectKey(projectDir), "*.json"))
	if err != nil || len(hexFiles) != 0 {
		t.Fatalf("regression hex files = %d, err = %v; want 0", len(hexFiles), err)
	}
}

func TestLoadLatestSessionCorruptFileHandling(t *testing.T) {
	tests := []struct {
		name       string
		files      map[string]string
		wantLatest string
		wantErr    string
	}{
		{
			name: "ignores corrupt older legacy when current is newest",
			files: map[string]string{
				"9-2026-01-10T000000.000000Z.json":      `{`,
				"2026-01-11T000000.000000000Z-000.json": sessionJSON("2026-01-11T00:00:00Z", "new"),
			},
			wantLatest: "new",
		},
		{
			name: "fails on corrupt newest",
			files: map[string]string{
				"2026-01-10T000000.000000000Z-000.json": sessionJSON("2026-01-10T00:00:00Z", "old"),
				"2026-01-10T000000.000000000Z-001.json": `{`,
			},
			wantErr: "2026-01-10T000000.000000000Z-001.json",
		},
		{
			name: "ignores corrupt older current session",
			files: map[string]string{
				"2026-01-10T000000.000000000Z-000.json": `{`,
				"2026-01-10T000000.000000000Z-001.json": sessionJSON("2026-01-10T00:00:00Z", "new"),
			},
			wantLatest: "new",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			projectDir := setupSessionFiles(t, tc.files)
			loaded, err := session.LoadLatestSession(projectDir)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("LoadLatestSession err = %v, want containing %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("LoadLatestSession: %v", err)
			}
			if len(loaded) != 1 || loaded[0].Content != tc.wantLatest {
				t.Fatalf("latest session = %#v, want %q", loaded, tc.wantLatest)
			}
		})
	}
}

func assertSessionDirPrefix(t *testing.T, projectDir, wantPrefix string) {
	t.Helper()

	dir, err := session.SessionDir(projectDir)
	if err != nil {
		t.Fatalf("SessionDir: %v", err)
	}
	if !strings.HasPrefix(dir, wantPrefix+string(os.PathSeparator)) {
		t.Fatalf("SessionDir = %q, want prefix %q", dir, wantPrefix)
	}
}

func assertSessionFileCount(t *testing.T, projectDir string, want int) {
	t.Helper()

	dir, err := session.SessionDir(projectDir)
	if err != nil {
		t.Fatalf("SessionDir: %v", err)
	}
	files, err := filepath.Glob(filepath.Join(dir, "*.json"))
	if err != nil || len(files) != want {
		t.Fatalf("session files = %d, err = %v; want %d", len(files), err, want)
	}
}

func readOnlySessionFile(t *testing.T, projectDir string, dest any) {
	t.Helper()

	dir, err := session.SessionDir(projectDir)
	if err != nil {
		t.Fatalf("SessionDir: %v", err)
	}
	files, err := filepath.Glob(filepath.Join(dir, "*.json"))
	if err != nil || len(files) != 1 {
		t.Fatalf("session files = %d, err = %v; want 1", len(files), err)
	}
	data, err := os.ReadFile(files[0])
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if err := json.Unmarshal(data, dest); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
}

func setupSessionFiles(t *testing.T, files map[string]string) string {
	t.Helper()

	tmpdir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", tmpdir)
	projectDir := filepath.Join(tmpdir, "project")
	dir, err := session.SessionDir(projectDir)
	if err != nil {
		t.Fatalf("SessionDir: %v", err)
	}
	for name, content := range files {
		writeFile(t, dir, name, content)
	}
	return projectDir
}

func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()

	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile %s: %v", name, err)
	}
}

func assertLatestContent(t *testing.T, projectDir, want string) {
	t.Helper()

	loaded, err := session.LoadLatestSession(projectDir)
	if err != nil {
		t.Fatalf("LoadLatestSession: %v", err)
	}
	if len(loaded) != 1 || loaded[0].Content != want {
		t.Fatalf("latest session = %#v, want content %q", loaded, want)
	}
}

func assertListOrder(t *testing.T, projectDir string, want []string) {
	t.Helper()

	sessions, err := session.ListSessions(projectDir)
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(sessions) != len(want) {
		t.Fatalf("sessions = %#v, want %d sessions", sessions, len(want))
	}
	for i, filename := range want {
		if sessions[i].Filename != filename {
			t.Fatalf("sessions[%d] = %q, want %q; sessions = %#v", i, sessions[i].Filename, filename, sessions)
		}
	}
}

func sessionJSON(created, content string) string {
	return fmt.Sprintf(`{"created":%q,"messages":[{"role":"user","content":%q}]}`, created, content)
}

func regressionHexProjectKey(path string) string {
	hash := sha256.Sum256([]byte(path))
	return fmt.Sprintf("%x", hash[:8])
}
