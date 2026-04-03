package evaluations

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/clnkr-ai/clnkr"
	ctranscript "github.com/clnkr-ai/clnkr/transcript"
)

type normalizationRoots struct {
	Workdir string
	Home    string
	Config  string
	State   string
	Temp    string
}

type pathReplacement struct {
	base        string
	placeholder string
	priority    int
}

// NormalizedTranscriptRecord is one stable transcript record derived from raw trial data.
type NormalizedTranscriptRecord struct {
	Index    int    `json:"index"`
	Kind     string `json:"kind"`
	Role     string `json:"role,omitempty"`
	TurnType string `json:"turn_type,omitempty"`
	Content  string `json:"content,omitempty"`
	Command  string `json:"command,omitempty"`
	Cwd      string `json:"cwd,omitempty"`
	Stdout   string `json:"stdout,omitempty"`
	Stderr   string `json:"stderr,omitempty"`
	ExitCode int    `json:"exit_code"`
}

// NormalizedOutcome summarizes the final trial state for grading and export.
type NormalizedOutcome struct {
	FinalExitCode     int                       `json:"final_exit_code"`
	FinalCwd          string                    `json:"final_cwd"`
	WorkspaceFiles    []NormalizedWorkspaceFile `json:"workspace_files"`
	MaterializedPaths []string                  `json:"materialized_paths"`
}

// NormalizedWorkspaceFile describes one materialized workspace file.
type NormalizedWorkspaceFile struct {
	Path      string `json:"path"`
	SizeBytes int64  `json:"size_bytes"`
	SHA256    string `json:"sha256"`
}

type eventEnvelope struct {
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload"`
}

type commandStartEvent struct {
	Command string `json:"command"`
	Dir     string `json:"dir"`
}

type commandDoneEvent struct {
	Command  string `json:"command"`
	Stdout   string `json:"stdout"`
	Stderr   string `json:"stderr"`
	ExitCode int    `json:"exit_code"`
	Err      string `json:"err,omitempty"`
}

type commandResultEnvelope struct {
	Command  string
	Stdout   string
	Stderr   string
	ExitCode int
}

// NormalizeTranscript projects a stable transcript record sequence from raw trial artifacts.
func NormalizeTranscript(artifacts RunArtifacts) ([]NormalizedTranscriptRecord, error) {
	messages, err := parseRawTranscript(artifacts.Trajectory)
	if err != nil {
		return nil, fmt.Errorf("normalize transcript: %w", err)
	}
	starts, dones, err := parseCommandLifecycleEvents(artifacts.EventLog)
	if err != nil {
		return nil, fmt.Errorf("normalize transcript: %w", err)
	}

	roots := artifacts.normalizationRoots()
	records := make([]NormalizedTranscriptRecord, 0, len(messages)+len(starts)+len(dones))
	startIndex := 0
	doneIndex := 0

	for _, message := range messages {
		switch message.Role {
		case "system":
			records = append(records, NormalizedTranscriptRecord{
				Index:   len(records),
				Kind:    "system_prompt",
				Role:    "system",
				Content: normalizeText(message.Content, roots),
			})
		case "assistant":
			record, startConsumed, err := normalizeAssistantMessage(message.Content, roots, starts, startIndex, len(records))
			if err != nil {
				return nil, fmt.Errorf("normalize assistant message: %w", err)
			}
			records = append(records, record...)
			if startConsumed {
				startIndex++
			}
		case "user":
			switch {
			case isStateUpdateMessage(message.Content):
				cwd, _ := ctranscript.ExtractStateCwd(message.Content)
				records = append(records, NormalizedTranscriptRecord{
					Index: len(records),
					Kind:  "state_update",
					Role:  "user",
					Cwd:   normalizePath(cwd, roots),
				})
			case isCommandResultMessage(message.Content):
				record, consumed, err := normalizeCommandResultMessage(message.Content, roots, dones, doneIndex, len(records))
				if err != nil {
					return nil, fmt.Errorf("normalize command result: %w", err)
				}
				records = append(records, record)
				if consumed {
					doneIndex++
				}
			default:
				records = append(records, NormalizedTranscriptRecord{
					Index:   len(records),
					Kind:    "user_instruction",
					Role:    "user",
					Content: normalizeText(message.Content, roots),
				})
			}
		default:
			records = append(records, NormalizedTranscriptRecord{
				Index:   len(records),
				Kind:    "assistant_turn",
				Role:    message.Role,
				Content: normalizeText(message.Content, roots),
			})
		}
	}

	return records, nil
}

// NormalizeOutcome derives a stable end-state summary from raw trial artifacts.
func NormalizeOutcome(artifacts RunArtifacts, outcomeWorkspaceRel string) (NormalizedOutcome, error) {
	messages, err := parseRawTranscript(artifacts.Trajectory)
	if err != nil {
		return NormalizedOutcome{}, fmt.Errorf("normalize outcome: %w", err)
	}

	transcriptMessages := make([]ctranscript.Message, 0, len(messages))
	for _, message := range messages {
		transcriptMessages = append(transcriptMessages, ctranscript.Message{
			Role:    message.Role,
			Content: message.Content,
		})
	}

	finalCwd := ""
	if cwd, ok := ctranscript.ExtractLatestCwd(transcriptMessages); ok {
		finalCwd = normalizePath(cwd, artifacts.normalizationRoots())
	}

	workspacePaths := make([]string, 0, len(artifacts.Workspace))
	for path := range artifacts.Workspace {
		workspacePaths = append(workspacePaths, path)
	}
	sort.Strings(workspacePaths)

	files := make([]NormalizedWorkspaceFile, 0, len(workspacePaths))
	materialized := make([]string, 0, len(workspacePaths))
	for _, path := range workspacePaths {
		content := artifacts.Workspace[path]
		files = append(files, NormalizedWorkspaceFile{
			Path:      filepath.ToSlash(path),
			SizeBytes: int64(len(content)),
			SHA256:    checksumSHA256String(content),
		})
		materialized = append(materialized, filepath.ToSlash(filepath.Join(outcomeWorkspaceRel, path)))
	}

	return NormalizedOutcome{
		FinalExitCode:     artifacts.ExitCode,
		FinalCwd:          finalCwd,
		WorkspaceFiles:    files,
		MaterializedPaths: materialized,
	}, nil
}

func normalizeAssistantMessage(content string, roots normalizationRoots, starts []commandStartEvent, startIndex, recordIndex int) ([]NormalizedTranscriptRecord, bool, error) {
	turn, err := clnkr.ParseTurn(content)
	if err != nil {
		return []NormalizedTranscriptRecord{{
			Index:   recordIndex,
			Kind:    "assistant_turn",
			Role:    "assistant",
			Content: normalizeText(content, roots),
		}}, false, nil
	}

	switch turn := turn.(type) {
	case *clnkr.ActTurn:
		records := []NormalizedTranscriptRecord{{
			Index:    recordIndex,
			Kind:     "assistant_turn",
			Role:     "assistant",
			TurnType: "act",
			Content:  normalizeText(content, roots),
		}}
		if startIndex < len(starts) {
			records = append(records, NormalizedTranscriptRecord{
				Index:    recordIndex + 1,
				Kind:     "command_start",
				Role:     "system",
				TurnType: "act",
				Command:  normalizeText(starts[startIndex].Command, roots),
				Cwd:      normalizePath(starts[startIndex].Dir, roots),
			})
			return records, true, nil
		}
		return records, false, nil
	case *clnkr.ClarifyTurn:
		return []NormalizedTranscriptRecord{{
			Index:    recordIndex,
			Kind:     "clarification",
			Role:     "assistant",
			TurnType: "clarify",
			Content:  normalizeText(turn.Question, roots),
		}}, false, nil
	case *clnkr.DoneTurn:
		return []NormalizedTranscriptRecord{{
			Index:    recordIndex,
			Kind:     "completion",
			Role:     "assistant",
			TurnType: "done",
			Content:  normalizeText(turn.Summary, roots),
		}}, false, nil
	default:
		return nil, false, fmt.Errorf("unexpected assistant turn type %T", turn)
	}
}

func normalizeCommandResultMessage(content string, roots normalizationRoots, dones []commandDoneEvent, doneIndex, recordIndex int) (NormalizedTranscriptRecord, bool, error) {
	if doneIndex < len(dones) {
		done := dones[doneIndex]
		return NormalizedTranscriptRecord{
			Index:    recordIndex,
			Kind:     "command_result",
			Role:     "user",
			Command:  normalizeText(done.Command, roots),
			Stdout:   normalizeText(done.Stdout, roots),
			Stderr:   normalizeText(done.Stderr, roots),
			ExitCode: done.ExitCode,
		}, true, nil
	}

	envelope, ok := parseCommandResultEnvelope(content)
	if !ok {
		return NormalizedTranscriptRecord{}, false, fmt.Errorf("parse command result transcript payload")
	}
	return NormalizedTranscriptRecord{
		Index:    recordIndex,
		Kind:     "command_result",
		Role:     "user",
		Command:  normalizeText(envelope.Command, roots),
		Stdout:   normalizeText(envelope.Stdout, roots),
		Stderr:   normalizeText(envelope.Stderr, roots),
		ExitCode: envelope.ExitCode,
	}, false, nil
}

func parseRawTranscript(raw string) ([]clnkr.Message, error) {
	var messages []clnkr.Message
	if err := json.Unmarshal([]byte(raw), &messages); err != nil {
		return nil, fmt.Errorf("parse raw transcript: %w", err)
	}
	return messages, nil
}

func parseCommandLifecycleEvents(raw string) ([]commandStartEvent, []commandDoneEvent, error) {
	starts := []commandStartEvent{}
	dones := []commandDoneEvent{}

	scanner := bufio.NewScanner(strings.NewReader(raw))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		var envelope eventEnvelope
		if err := json.Unmarshal([]byte(line), &envelope); err != nil {
			return nil, nil, fmt.Errorf("parse event log line: %w", err)
		}

		switch envelope.Type {
		case "command_start":
			var start commandStartEvent
			if err := json.Unmarshal(envelope.Payload, &start); err != nil {
				return nil, nil, fmt.Errorf("parse command_start payload: %w", err)
			}
			starts = append(starts, start)
		case "command_done":
			var done commandDoneEvent
			if err := json.Unmarshal(envelope.Payload, &done); err != nil {
				return nil, nil, fmt.Errorf("parse command_done payload: %w", err)
			}
			dones = append(dones, done)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, nil, fmt.Errorf("scan event log: %w", err)
	}

	return starts, dones, nil
}

func normalizeText(value string, roots normalizationRoots) string {
	if value == "" {
		return ""
	}

	replacements := buildPathReplacements(roots)
	normalized := value
	for _, replacement := range replacements {
		normalized = strings.ReplaceAll(normalized, replacement.base, replacement.placeholder)
	}

	resolved, err := filepath.EvalSymlinks(value)
	if err == nil && resolved != value {
		normalized = resolved
		for _, replacement := range replacements {
			normalized = strings.ReplaceAll(normalized, replacement.base, replacement.placeholder)
		}
	}

	return filepath.ToSlash(normalized)
}

func normalizePath(value string, roots normalizationRoots) string {
	if value == "" {
		return ""
	}

	cleaned := filepath.Clean(value)
	normalized := normalizeText(cleaned, roots)
	if normalized != filepath.ToSlash(cleaned) {
		return normalized
	}

	resolved, err := filepath.EvalSymlinks(cleaned)
	if err == nil {
		normalized = normalizeText(resolved, roots)
		if normalized != filepath.ToSlash(resolved) {
			return normalized
		}
	}

	return filepath.ToSlash(cleaned)
}

func buildPathReplacements(roots normalizationRoots) []pathReplacement {
	type namedRoot struct {
		value       string
		placeholder string
		priority    int
	}

	named := []namedRoot{
		{value: roots.Workdir, placeholder: "<WORKDIR>", priority: 0},
		{value: roots.Home, placeholder: "<HOME>", priority: 1},
		{value: filepath.Join(roots.Config, "clnkr"), placeholder: "<CONFIG>/clnkr", priority: 2},
		{value: roots.Config, placeholder: "<CONFIG>", priority: 3},
		{value: roots.State, placeholder: "<STATE>", priority: 4},
		{value: roots.Temp, placeholder: "<TMP>", priority: 5},
	}

	seen := map[string]pathReplacement{}
	for _, root := range named {
		if strings.TrimSpace(root.value) == "" {
			continue
		}
		candidates := []string{filepath.Clean(root.value)}
		if resolved, err := filepath.EvalSymlinks(root.value); err == nil {
			candidates = append(candidates, filepath.Clean(resolved))
		}
		for _, candidate := range candidates {
			if candidate == "." || candidate == "" {
				continue
			}
			if existing, ok := seen[candidate]; ok && existing.priority <= root.priority {
				continue
			}
			seen[candidate] = pathReplacement{
				base:        filepath.ToSlash(candidate),
				placeholder: root.placeholder,
				priority:    root.priority,
			}
		}
	}

	replacements := make([]pathReplacement, 0, len(seen))
	for _, replacement := range seen {
		replacements = append(replacements, replacement)
	}
	sort.SliceStable(replacements, func(i, j int) bool {
		if len(replacements[i].base) != len(replacements[j].base) {
			return len(replacements[i].base) > len(replacements[j].base)
		}
		return replacements[i].priority < replacements[j].priority
	})
	return replacements
}

func isStateUpdateMessage(content string) bool {
	_, ok := ctranscript.ExtractStateCwd(content)
	return ok
}

func isCommandResultMessage(content string) bool {
	_, ok := parseCommandResultEnvelope(content)
	return ok
}

func parseCommandResultEnvelope(content string) (commandResultEnvelope, bool) {
	command, ok := extractTaggedSection(content, "[command]", "[/command]")
	if !ok {
		return commandResultEnvelope{}, false
	}
	exitCodeStr, ok := extractTaggedSection(content, "[exit_code]", "[/exit_code]")
	if !ok {
		return commandResultEnvelope{}, false
	}
	stdout, ok := extractTaggedSection(content, "[stdout]", "[/stdout]")
	if !ok {
		return commandResultEnvelope{}, false
	}
	stderr, ok := extractTaggedSection(content, "[stderr]", "[/stderr]")
	if !ok {
		return commandResultEnvelope{}, false
	}

	exitCode, err := strconv.Atoi(strings.TrimSpace(exitCodeStr))
	if err != nil {
		return commandResultEnvelope{}, false
	}

	return commandResultEnvelope{
		Command:  html.UnescapeString(command),
		Stdout:   html.UnescapeString(stdout),
		Stderr:   html.UnescapeString(stderr),
		ExitCode: exitCode,
	}, true
}

func extractTaggedSection(content, openTag, closeTag string) (string, bool) {
	start := strings.Index(content, openTag)
	if start < 0 {
		return "", false
	}
	start += len(openTag)
	end := strings.Index(content[start:], closeTag)
	if end < 0 {
		return "", false
	}
	return strings.Trim(strings.TrimSpace(content[start:start+end]), "\n"), true
}

func checksumSHA256String(value string) string {
	return checksumSHA256Bytes([]byte(value))
}

func checksumSHA256Bytes(value []byte) string {
	sum := sha256.Sum256(value)
	return hex.EncodeToString(sum[:])
}

func resolveExistingPath(path string) (string, error) {
	cleaned := filepath.Clean(path)
	current := cleaned
	missingParts := []string{}
	for {
		resolved, err := filepath.EvalSymlinks(current)
		if err == nil {
			for i := len(missingParts) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, missingParts[i])
			}
			return resolved, nil
		}
		if !os.IsNotExist(err) {
			return "", fmt.Errorf("resolve %q: %w", current, err)
		}
		parent := filepath.Dir(current)
		if parent == current {
			for i := len(missingParts) - 1; i >= 0; i-- {
				current = filepath.Join(current, missingParts[i])
			}
			return current, nil
		}
		missingParts = append(missingParts, filepath.Base(current))
		current = parent
	}
}
