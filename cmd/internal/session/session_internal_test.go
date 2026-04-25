package session

import (
	"path/filepath"
	"testing"
	"time"
)

func TestOpenSessionFileRetriesTimestampCollisions(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 4, 25, 12, 0, 0, 1, time.UTC)

	first, err := openSessionFile(dir, now)
	if err != nil {
		t.Fatalf("first open: %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("first close: %v", err)
	}

	second, err := openSessionFile(dir, now)
	if err != nil {
		t.Fatalf("second open: %v", err)
	}
	if err := second.Close(); err != nil {
		t.Fatalf("second close: %v", err)
	}

	files, err := filepath.Glob(filepath.Join(dir, "*.json"))
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	if len(files) != 2 {
		t.Fatalf("files = %d, want 2", len(files))
	}
	if filepath.Base(files[0]) == filepath.Base(files[1]) {
		t.Fatalf("session filenames collided: %v", files)
	}
}
