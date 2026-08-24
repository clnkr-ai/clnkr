package session

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/clnkr-ai/clnkr"
)

func TestSaveSessionRetriesCollisionsAndExhaustsNames(t *testing.T) {
	now := time.Date(2026, 4, 25, 12, 0, 0, 1, time.UTC)
	prefix := now.Format("2006-01-02T150405.000000000Z")

	t.Run("retries without overwriting", func(t *testing.T) {
		dir := t.TempDir()
		firstPath := filepath.Join(dir, prefix+"-000.json")
		if err := os.WriteFile(firstPath, []byte("original"), 0o600); err != nil {
			t.Fatalf("write collision: %v", err)
		}
		temporaryPath := writeTemporarySession(t, dir, "replacement")

		publishedPath, err := publishSessionFile(dir, temporaryPath, now)
		if err != nil {
			t.Fatalf("publishSessionFile: %v", err)
		}
		if want := filepath.Join(dir, prefix+"-001.json"); publishedPath != want {
			t.Fatalf("published path = %q, want %q", publishedPath, want)
		}
		original, err := os.ReadFile(firstPath)
		if err != nil {
			t.Fatalf("read collision: %v", err)
		}
		if string(original) != "original" {
			t.Fatalf("collision content = %q, want original", original)
		}
	})

	t.Run("exhausts names without overwriting", func(t *testing.T) {
		dir := t.TempDir()
		for i := 0; i < 1000; i++ {
			path := filepath.Join(dir, fmt.Sprintf("%s-%03d.json", prefix, i))
			if err := os.WriteFile(path, []byte("occupied"), 0o600); err != nil {
				t.Fatalf("write collision %d: %v", i, err)
			}
		}
		temporaryPath := writeTemporarySession(t, dir, "replacement")

		_, err := publishSessionFile(dir, temporaryPath, now)
		if err == nil || !strings.Contains(err.Error(), "too many sessions") {
			t.Fatalf("publishSessionFile error = %v, want exhaustion", err)
		}
		for _, sequence := range []int{0, 999} {
			path := filepath.Join(dir, fmt.Sprintf("%s-%03d.json", prefix, sequence))
			content, readErr := os.ReadFile(path)
			if readErr != nil {
				t.Fatalf("read collision %d: %v", sequence, readErr)
			}
			if string(content) != "occupied" {
				t.Fatalf("collision %d content = %q, want occupied", sequence, content)
			}
		}
	})
}

func TestSaveSessionConcurrentPublishers(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 4, 25, 12, 0, 0, 1, time.UTC)
	const publishers = 16

	temporaryPaths := make([]string, publishers)
	for i := range publishers {
		encoded, err := json.Marshal(sessionFile{
			Created:  now.Format(time.RFC3339Nano),
			Messages: []clnkr.Message{{Role: "user", Content: fmt.Sprintf("session-%d", i)}},
		})
		if err != nil {
			t.Fatalf("marshal session %d: %v", i, err)
		}
		temporaryPaths[i] = writeTemporarySession(t, dir, string(encoded))
	}

	type result struct {
		path string
		err  error
	}
	start := make(chan struct{})
	results := make(chan result, publishers)
	for _, temporaryPath := range temporaryPaths {
		go func() {
			<-start
			path, err := publishSessionFile(dir, temporaryPath, now)
			if err == nil {
				_ = os.Remove(temporaryPath)
			}
			results <- result{path: path, err: err}
		}()
	}
	close(start)

	published := make(map[string]struct{}, publishers)
	contents := make(map[string]struct{}, publishers)
	for range publishers {
		result := <-results
		if result.err != nil {
			t.Fatalf("publishSessionFile: %v", result.err)
		}
		if _, duplicate := published[result.path]; duplicate {
			t.Fatalf("duplicate published path %q", result.path)
		}
		published[result.path] = struct{}{}
		stored, err := loadSessionFile(result.path)
		if err != nil {
			t.Fatalf("loadSessionFile(%q): %v", result.path, err)
		}
		if len(stored.Messages) != 1 {
			t.Fatalf("stored session %q has %d messages, want 1", result.path, len(stored.Messages))
		}
		contents[stored.Messages[0].Content] = struct{}{}
	}
	if len(contents) != publishers {
		t.Fatalf("distinct session contents = %d, want %d", len(contents), publishers)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != publishers {
		t.Fatalf("session directory entries = %d, want %d", len(entries), publishers)
	}
	for _, entry := range entries {
		if filepath.Ext(entry.Name()) != ".json" {
			t.Fatalf("leftover temporary file %q", entry.Name())
		}
	}
}

func writeTemporarySession(t *testing.T, dir, content string) string {
	t.Helper()

	f, err := os.CreateTemp(dir, ".session-*")
	if err != nil {
		t.Fatalf("CreateTemp: %v", err)
	}
	path := f.Name()
	if _, err := f.WriteString(content); err != nil {
		_ = f.Close()
		t.Fatalf("write temporary session: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close temporary session: %v", err)
	}
	t.Cleanup(func() { _ = os.Remove(path) })
	return path
}
