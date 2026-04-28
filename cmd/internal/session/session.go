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

	"github.com/clnkr-ai/clnkr"
)

// SessionInfo holds metadata about a saved session.
type SessionInfo struct {
	Filename string
	Created  time.Time
	Messages int
}

type sessionFile struct {
	Created  string          `json:"created"`
	Metadata json.RawMessage `json:"metadata,omitempty"`
	Messages []clnkr.Message `json:"messages"`
}

type loadedSession struct {
	SessionInfo
	messages []clnkr.Message
}

type sessionFileRef struct {
	dir  string
	name string
}

type filenameOrder struct {
	at     time.Time
	seq    int
	legacy bool
}

// NormalizeProjectPath converts an absolute working directory to a stable,
// low-collision normalized identifier for session storage.
// Returns lowercase base32(sha256(path))[:16].
func NormalizeProjectPath(path string) (string, error) {
	if path == "" {
		return "", fmt.Errorf("normalize project path: empty path")
	}
	if !filepath.IsAbs(path) {
		return "", fmt.Errorf("normalize project path: path %q is not absolute", path)
	}

	hash := sha256.Sum256([]byte(path))
	return strings.ToLower(base32.StdEncoding.EncodeToString(hash[:])[:16]), nil
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
	return SaveSessionWithMetadata(pwd, messages, nil)
}

// SaveSessionWithMetadata writes the message history and optional metadata to an atomic session file.
func SaveSessionWithMetadata(pwd string, messages []clnkr.Message, metadata any) error {
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

	data := sessionFile{
		Created:  now.Format(time.RFC3339Nano),
		Messages: messages,
	}
	if metadata != nil {
		encoded, err := json.Marshal(metadata)
		if err != nil {
			return fmt.Errorf("save session: marshal metadata: %w", err)
		}
		data.Metadata = encoded
	}

	if err := json.NewEncoder(f).Encode(data); err != nil {
		return fmt.Errorf("save session: encode: %w", err)
	}

	return nil
}

// LoadLatestSession loads the most recent session for the given working directory.
// Returns nil, nil if no sessions exist.
func LoadLatestSession(pwd string) ([]clnkr.Message, error) {
	files, err := sessionFileRefs(pwd)
	if err != nil {
		return nil, fmt.Errorf("load latest session: %w", err)
	}
	if len(files) == 0 {
		return nil, nil
	}

	sort.Slice(files, func(i, j int) bool {
		return newerFilename(files[i].name, files[j].name)
	})

	sf, err := loadSessionFile(filepath.Join(files[0].dir, files[0].name))
	if err != nil {
		return nil, fmt.Errorf("load latest session %s: %w", files[0].name, err)
	}
	latest := loadedSession{
		SessionInfo: sessionInfo(files[0].name, sf),
		messages:    sf.Messages,
	}
	for _, e := range files[1:] {
		sf, err := loadSessionFile(filepath.Join(e.dir, e.name))
		if err != nil {
			continue
		}
		candidate := loadedSession{
			SessionInfo: sessionInfo(e.name, sf),
			messages:    sf.Messages,
		}
		if newerSession(candidate.SessionInfo, latest.SessionInfo) {
			latest = candidate
		}
	}
	return latest.messages, nil
}

// ListSessions lists all sessions for the given working directory.
// Session filenames define recency before Created metadata.
func ListSessions(pwd string) ([]SessionInfo, error) {
	files, err := sessionFileRefs(pwd)
	if err != nil {
		return nil, fmt.Errorf("list sessions: %w", err)
	}

	var sessions []SessionInfo
	for _, e := range files {
		sf, err := loadSessionFile(filepath.Join(e.dir, e.name))
		if err != nil {
			continue
		}
		sessions = append(sessions, sessionInfo(e.name, sf))
	}
	sort.Slice(sessions, func(i, j int) bool {
		return newerSession(sessions[i], sessions[j])
	})
	return sessions, nil
}

func sessionInfo(filename string, sf sessionFile) SessionInfo {
	created, _ := time.Parse(time.RFC3339Nano, sf.Created)
	return SessionInfo{
		Filename: filename,
		Created:  created,
		Messages: len(sf.Messages),
	}
}

func sessionFileRefs(pwd string) ([]sessionFileRef, error) {
	canonical, err := SessionDir(pwd)
	if err != nil {
		return nil, err
	}
	hash := sha256.Sum256([]byte(pwd))
	dirs := []string{canonical, filepath.Join(filepath.Dir(canonical), fmt.Sprintf("%x", hash[:8]))}
	var refs []sessionFileRef
	for i, dir := range dirs {
		if i > 0 && dir == canonical {
			continue
		}
		files, err := sessionFiles(dir)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, fmt.Errorf("readdir: %w", err)
		}
		for _, f := range files {
			refs = append(refs, sessionFileRef{dir: dir, name: f.Name()})
		}
	}
	return refs, nil
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
	return files, nil
}

func newerSession(a, b SessionInfo) bool {
	if newer, ok := newerParsedFilename(a.Filename, b.Filename); ok {
		return newer
	}
	if !a.Created.Equal(b.Created) {
		return a.Created.After(b.Created)
	}
	return a.Filename > b.Filename
}

func newerFilename(a, b string) bool {
	if newer, ok := newerParsedFilename(a, b); ok {
		return newer
	}
	return a > b
}

func newerParsedFilename(a, b string) (bool, bool) {
	aInfo, aOK := parseSessionFilename(a)
	bInfo, bOK := parseSessionFilename(b)
	if aOK != bOK {
		return aOK, true
	}
	if aOK && bOK {
		if !aInfo.at.Equal(bInfo.at) {
			return aInfo.at.After(bInfo.at), true
		}
		if aInfo.legacy != bInfo.legacy {
			return !aInfo.legacy, true
		}
		if aInfo.seq != bInfo.seq {
			return aInfo.seq > bInfo.seq, true
		}
		return a > b, true
	}
	return false, false
}

func parseSessionFilename(name string) (filenameOrder, bool) {
	base, ok := strings.CutSuffix(name, ".json")
	if !ok {
		return filenameOrder{}, false
	}
	prefix, rest, ok := strings.Cut(base, "-")
	if ok {
		if seq, err := strconv.Atoi(prefix); err == nil {
			if at, ok := parseSessionFilenameTime(rest, "2006-01-02T150405.000000Z"); ok {
				return filenameOrder{at: at, seq: seq, legacy: true}, true
			}
		}
	}
	idx := strings.LastIndexByte(base, '-')
	if idx <= 0 {
		return filenameOrder{}, false
	}
	seq, err := strconv.Atoi(base[idx+1:])
	if err != nil {
		return filenameOrder{}, false
	}
	at, ok := parseSessionFilenameTime(base[:idx], "2006-01-02T150405.000000000Z")
	if !ok {
		return filenameOrder{}, false
	}
	return filenameOrder{at: at, seq: seq}, true
}

func parseSessionFilenameTime(value, layout string) (time.Time, bool) {
	at, err := time.Parse(layout, value)
	return at, err == nil
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
		filename := fmt.Sprintf("%s-%03d.json", prefix, i)
		path := filepath.Join(dir, filename)
		f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err == nil || !os.IsExist(err) {
			return f, err
		}
	}
	return nil, fmt.Errorf("too many sessions for timestamp %s", prefix)
}
