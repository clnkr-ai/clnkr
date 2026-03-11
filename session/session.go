package session

import (
	"crypto/sha256"
	"encoding/base32"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/cosgroveb/hew"
)

// SessionInfo holds metadata about a saved session.
type SessionInfo struct {
	Filename string
	Created  time.Time
	Messages int
}

type sessionFile struct {
	Created  string        `json:"created"`
	Messages []hew.Message `json:"messages"`
}

// NormalizeProjectPath converts an absolute working directory to a unique,
// collision-free normalized identifier for session storage.
// Returns lowercase base32(sha256(path))[:16].
func NormalizeProjectPath(path string) (string, error) {
	if path == "" {
		return "", fmt.Errorf("normalize project path: empty path")
	}
	if !filepath.IsAbs(path) {
		return "", fmt.Errorf("normalize project path: path %q is not absolute", path)
	}

	hash := sha256.Sum256([]byte(path))
	normalized := base32.StdEncoding.EncodeToString(hash[:])
	return strings.ToLower(normalized[:16]), nil
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

	return filepath.Join(stateDir, "hew", "projects", normalized), nil
}

// SaveSession writes the message history to an atomic session file.
func SaveSession(pwd string, messages []hew.Message) error {
	dir, err := SessionDir(pwd)
	if err != nil {
		return fmt.Errorf("save session: %w", err)
	}

	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("save session: mkdir: %w", err)
	}

	// Find next session number by scanning existing files
	nextNum := 0
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		name := e.Name()
		if filepath.Ext(name) != ".json" || e.IsDir() {
			continue
		}
		// Parse "N-timestamp.json" to find highest N
		if idx := strings.IndexByte(name, '-'); idx > 0 {
			if n, err := strconv.Atoi(name[:idx]); err == nil && n >= nextNum {
				nextNum = n + 1
			}
		}
	}

	timestamp := time.Now().UTC().Format("2006-01-02T150405.000000Z")
	filename := fmt.Sprintf("%d-%s.json", nextNum, timestamp)
	fpath := filepath.Join(dir, filename)

	f, err := os.OpenFile(fpath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("save session: open: %w", err)
	}
	defer f.Close() //nolint:errcheck

	data := sessionFile{
		Created:  time.Now().UTC().Format(time.RFC3339Nano),
		Messages: messages,
	}

	if err := json.NewEncoder(f).Encode(data); err != nil {
		return fmt.Errorf("save session: encode: %w", err)
	}

	return nil
}

// LoadLatestSession loads the most recent session for the given working directory.
// Returns nil, nil if no sessions exist.
func LoadLatestSession(pwd string) ([]hew.Message, error) {
	dir, err := SessionDir(pwd)
	if err != nil {
		return nil, fmt.Errorf("load latest session: %w", err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("load latest session: readdir: %w", err)
	}

	// Find file with highest sequence number
	var latestFile string
	latestNum := -1
	for _, e := range entries {
		name := e.Name()
		if filepath.Ext(name) != ".json" || e.IsDir() {
			continue
		}
		if idx := strings.IndexByte(name, '-'); idx > 0 {
			if n, err := strconv.Atoi(name[:idx]); err == nil && n > latestNum {
				latestNum = n
				latestFile = name
			}
		}
	}

	if latestFile == "" {
		return nil, nil
	}

	data, err := os.ReadFile(filepath.Join(dir, latestFile))
	if err != nil {
		return nil, fmt.Errorf("load latest session: read: %w", err)
	}

	var sf sessionFile
	if err := json.Unmarshal(data, &sf); err != nil {
		return nil, fmt.Errorf("load latest session: parse: %w", err)
	}

	return sf.Messages, nil
}

// ListSessions lists all sessions for the given working directory,
// sorted by creation time (newest first).
func ListSessions(pwd string) ([]SessionInfo, error) {
	dir, err := SessionDir(pwd)
	if err != nil {
		return nil, fmt.Errorf("list sessions: %w", err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("list sessions: %w", err)
	}

	var sessions []SessionInfo
	for _, e := range entries {
		name := e.Name()
		if filepath.Ext(name) != ".json" || e.IsDir() {
			continue
		}

		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			continue
		}
		var sf sessionFile
		if json.Unmarshal(data, &sf) != nil {
			continue
		}

		created, _ := time.Parse(time.RFC3339Nano, sf.Created)
		sessions = append(sessions, SessionInfo{
			Filename: name,
			Created:  created,
			Messages: len(sf.Messages),
		})
	}

	sort.Slice(sessions, func(i, j int) bool {
		return sessions[i].Created.After(sessions[j].Created)
	})

	return sessions, nil
}
