package shellstate

import (
	"bytes"
	"fmt"
	"os"
	"strings"
)

// Snapshot captures the shell state persisted after a command completes.
type Snapshot struct {
	Cwd string
	Env map[string]string
}

// Wrap prefixes a command with the EXIT trap that persists shell state.
func Wrap(command string) (string, string, func(), error) {
	stateFile, err := os.CreateTemp("", "clnkr-shell-state-")
	if err != nil {
		return "", "", nil, fmt.Errorf("create state file: %w", err)
	}
	if err := stateFile.Close(); err != nil {
		_ = os.Remove(stateFile.Name())
		return "", "", nil, fmt.Errorf("close state file: %w", err)
	}

	wrapped := "trap 'clnkr_status=$?; trap - EXIT; { /usr/bin/printf \"%s\\0\" \"$PWD\"; /usr/bin/env -0; } > \"$CLNKR_STATE_FILE\"; exit $clnkr_status' EXIT\n" + command
	return wrapped, stateFile.Name(), func() { _ = os.Remove(stateFile.Name()) }, nil
}

// Load reads a persisted shell snapshot from disk.
func Load(stateFile string) (Snapshot, error) {
	if stateFile == "" {
		return Snapshot{}, nil
	}
	data, err := os.ReadFile(stateFile)
	if err != nil {
		return Snapshot{}, fmt.Errorf("read state file: %w", err)
	}
	if len(data) == 0 {
		return Snapshot{}, nil
	}

	parts := bytes.Split(data, []byte{0})
	snapshot := Snapshot{}
	if len(parts[0]) > 0 {
		snapshot.Cwd = string(parts[0])
	}

	captured := make(map[string]string, len(parts)-1)
	for _, part := range parts[1:] {
		if len(part) == 0 {
			continue
		}
		key, value, ok := strings.Cut(string(part), "=")
		if !ok {
			continue
		}
		captured[key] = value
	}
	delete(captured, "CLNKR_STATE_FILE")
	snapshot.Env = captured
	return snapshot, nil
}

// EnvMapToList converts an environment snapshot into exec-style KEY=VALUE rows.
func EnvMapToList(env map[string]string) []string {
	list := make([]string, 0, len(env))
	for key, value := range env {
		list = append(list, key+"="+value)
	}
	return list
}
