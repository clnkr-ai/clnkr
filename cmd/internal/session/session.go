package session

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/clnkr-ai/clnkr"
)

// SessionInfo holds metadata about a saved session.
type SessionInfo struct {
	Filename string
	Created  time.Time
	Messages int
}

type sessionFile struct {
	Created  time.Time       `json:"created"`
	Messages []clnkr.Message `json:"messages"`
}

// NormalizeProjectPath converts an absolute working directory to a stable
// 16-character hex identifier for session storage.
func NormalizeProjectPath(path string) (string, error) {
	if path == "" {
		return "", fmt.Errorf("normalize project path: empty path")
	}
	if !filepath.IsAbs(path) {
		return "", fmt.Errorf("normalize project path: path %q is not absolute", path)
	}

	hash := sha256.Sum256([]byte(path))
	return fmt.Sprintf("%x", hash[:8]), nil
}

// SessionDir returns the session directory for the given working directory.
// Uses $XDG_STATE_HOME or ~/.local/state if unset.
func SessionDir(pwd string) (string, error) {
	stateDir := os.Getenv("XDG_STATE_HOME")
	if stateDir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("session dir: %w", err)
		}
		stateDir = filepath.Join(home, ".local", "state")
	}

	normalized, err := NormalizeProjectPath(pwd)
	if err != nil {
		return "", fmt.Errorf("session dir: %w", err)
	}

	return filepath.Join(stateDir, "clnkr", "projects", normalized), nil
}

// SaveSession writes the message history to an atomic session file.
func SaveSession(pwd string, messages []clnkr.Message) error {
	dir, err := SessionDir(pwd)
	if err != nil {
		return fmt.Errorf("save session: %w", err)
	}

	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("save session: mkdir: %w", err)
	}

	now := time.Now().UTC()
	f, err := openSessionFile(dir, now)
	if err != nil {
		return fmt.Errorf("save session: open: %w", err)
	}
	defer f.Close() //nolint:errcheck

	if err := json.NewEncoder(f).Encode(sessionFile{Created: now, Messages: messages}); err != nil {
		return fmt.Errorf("save session: encode: %w", err)
	}

	return nil
}

// LoadLatestSession loads the most recent session for the given working directory.
// Returns nil, nil if no sessions exist.
func LoadLatestSession(pwd string) ([]clnkr.Message, error) {
	dir, err := SessionDir(pwd)
	if err != nil {
		return nil, fmt.Errorf("load latest session: %w", err)
	}

	files, err := sessionFiles(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("load latest session: readdir: %w", err)
	}
	if len(files) == 0 {
		return nil, nil
	}
	sf, err := loadSessionFile(filepath.Join(dir, files[0].Name()))
	if err != nil {
		return nil, fmt.Errorf("load latest session: %w", err)
	}

	return sf.Messages, nil
}

// ListSessions lists all sessions for the given working directory,
// sorted by sortable filename (newest first).
func ListSessions(pwd string) ([]SessionInfo, error) {
	dir, err := SessionDir(pwd)
	if err != nil {
		return nil, fmt.Errorf("list sessions: %w", err)
	}

	files, err := sessionFiles(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("list sessions: %w", err)
	}

	var sessions []SessionInfo
	for _, e := range files {
		sf, err := loadSessionFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		sessions = append(sessions, SessionInfo{
			Filename: e.Name(),
			Created:  sf.Created,
			Messages: len(sf.Messages),
		})
	}

	return sessions, nil
}

func sessionFiles(dir string) ([]os.DirEntry, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	files := entries[:0]
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".json" && !e.IsDir() {
			files = append(files, e)
		}
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Name() > files[j].Name() })
	return files, nil
}

func loadSessionFile(path string) (sessionFile, error) {
	var sf sessionFile
	data, err := os.ReadFile(path)
	if err == nil {
		err = json.Unmarshal(data, &sf)
	}
	return sf, err
}

func openSessionFile(dir string, now time.Time) (*os.File, error) {
	prefix := now.Format("2006-01-02T150405.000000000Z")
	for i := 0; i < 1000; i++ {
		f, err := os.OpenFile(filepath.Join(dir, fmt.Sprintf("%s-%03d.json", prefix, i)), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err == nil || !os.IsExist(err) {
			return f, err
		}
	}
	return nil, fmt.Errorf("too many sessions for timestamp %s", prefix)
}
