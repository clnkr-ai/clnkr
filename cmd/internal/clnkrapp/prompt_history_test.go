package clnkrapp

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestPromptHistoryConcurrentWriters(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	cwd := t.TempDir()
	const writers = 10
	results := make(chan error, writers)
	long := strings.Repeat("界", 32768)
	for i := range writers {
		go func() {
			h, err := OpenPromptHistory(cwd)
			if err == nil {
				err = h.Add(fmt.Sprintf("%d\n%s", i, long))
			}
			results <- err
		}()
	}
	for range writers {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	h, err := OpenPromptHistory(cwd)
	if err != nil || len(h.Entries) != writers {
		t.Fatalf("got %d entries, %v", len(h.Entries), err)
	}
	for i := range writers {
		if !slices.Contains(h.Entries, fmt.Sprintf("%d\n%s", i, long)) {
			t.Fatalf("missing entry %d", i)
		}
	}
	for path, perm := range map[string]os.FileMode{h.path: 0600, filepath.Dir(h.path): 0700} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != perm {
			t.Errorf("%s: mode %v", path, info.Mode())
		}
	}
	sessions, err := ListSessions(cwd)
	if err != nil || len(sessions) != 0 {
		t.Fatalf("session list: %#v, %v", sessions, err)
	}
}

func TestPromptHistoryRecoversAfterIncompleteWrite(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	cwd := t.TempDir()
	h, err := OpenPromptHistory(cwd)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.Add("first"); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(h.path, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("\x1e\"interrupted"); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	if err := h.Add("second"); err != nil {
		t.Fatal(err)
	}
	h, err = OpenPromptHistory(cwd)
	if err == nil || !slices.Equal(h.Entries, []string{"first", "second"}) {
		t.Fatalf("history: %#v, %v", h.Entries, err)
	}
}
