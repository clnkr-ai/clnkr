package transcript

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

type commandPayload struct {
	Command     string          `json:"command"`
	Stdout      string          `json:"stdout"`
	Stderr      string          `json:"stderr"`
	Outcome     CommandOutcome  `json:"outcome"`
	Feedback    CommandFeedback `json:"feedback"`
	Guidance    string          `json:"guidance"`
	Observation struct {
		Source  string             `json:"source"`
		Version int                `json:"version"`
		Stdout  *streamObservation `json:"stdout"`
		Stderr  *streamObservation `json:"stderr"`
	} `json:"observation"`
}

type streamObservation struct {
	OriginalBytes int    `json:"original_bytes"`
	ShownBytes    int    `json:"shown_bytes"`
	OmittedBytes  int    `json:"omitted_bytes"`
	Mode          string `json:"mode"`
}

func decodeCommandPayload(t *testing.T, raw string) commandPayload {
	t.Helper()
	var payload commandPayload
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		t.Fatalf("FormatCommandResult() returned invalid JSON: %v\n%s", err, raw)
	}
	return payload
}

func decodeCommandFields(t *testing.T, raw string) map[string]json.RawMessage {
	t.Helper()
	var payload map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		t.Fatalf("FormatCommandResult() returned invalid JSON: %v\n%s", err, raw)
	}
	return payload
}

func assertMessages(t *testing.T, got, want []Message) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %d messages, want %d", len(got), len(want))
	}
	for i := range want {
		if !reflect.DeepEqual(got[i], want[i]) {
			t.Fatalf("message %d = %#v, want %#v", i, got[i], want[i])
		}
	}
}

func compressedRawKept(stream string) int {
	headStart := strings.Index(stream, "[head]\n") + len("[head]\n")
	tailStart := strings.Index(stream, "\n[tail]\n")
	tailEnd := tailStart + len("\n[tail]\n")
	return len(stream[headStart:tailStart]) + len(stream[tailEnd:])
}

func TestFindCompactBoundaryIgnoresHostBlocks(t *testing.T) {
	tests := []struct {
		name            string
		messages        []Message
		keepRecentTurns int
		wantBoundary    int
		wantOK          bool
	}{
		{
			name: "counts only authored user messages",
			messages: []Message{
				{Role: "user", Content: "first task"},
				{Role: "assistant", Content: `{"type":"done","summary":"done"}`},
				{
					Role: "user",
					Content: FormatCommandResult(
						CommandResult{Command: "ls", ExitCode: 0, Stdout: ".\n"},
					),
				},
				{Role: "user", Content: FormatStateMessage("/tmp/old")},
				{Role: "user", Content: FormatCompactMessage("older summary")},
				{Role: "user", Content: "[protocol_error]\nmissing command\n[/protocol_error]"},
				{Role: "user", Content: "second task"},
				{Role: "assistant", Content: `{"type":"done","summary":"done again"}`},
				{Role: "user", Content: "third task"},
				{Role: "assistant", Content: `{"type":"done","summary":"done third"}`},
			},
			keepRecentTurns: 2,
			wantBoundary:    6,
			wantOK:          true,
		},
		{
			name: "requires older authored history beyond kept tail",
			messages: []Message{
				{Role: "user", Content: "first task"},
				{Role: "assistant", Content: `{"type":"done","summary":"done"}`},
				{Role: "user", Content: FormatStateMessage("/tmp/only")},
				{Role: "user", Content: "second task"},
			},
			keepRecentTurns: 2,
			wantOK:          false,
		},
		{
			name: "ignores command feedback blocks",
			messages: []Message{
				{Role: "user", Content: "first task"},
				{Role: "assistant", Content: `{"type":"done","summary":"done first"}`},
				{Role: "user", Content: "second task"},
				{Role: "assistant", Content: `{"type":"done","summary":"done second"}`},
				{Role: "user", Content: FormatCommandResult(CommandResult{
					Command:  "printf next > note.txt",
					ExitCode: 0,
					Feedback: CommandFeedback{
						ChangedFiles: []string{"note.txt"},
						Diff:         "diff --git a/note.txt b/note.txt",
					},
				})},
				{Role: "user", Content: "third task"},
				{Role: "assistant", Content: `{"type":"done","summary":"done third"}`},
			},
			keepRecentTurns: 2,
			wantBoundary:    2,
			wantOK:          true,
		},
		{
			name: "counts user json with outcome but no command streams",
			messages: []Message{
				{Role: "user", Content: "first task"},
				{Role: "assistant", Content: `{"type":"done","summary":"done first"}`},
				{Role: "user", Content: `{"outcome":{"type":"exit"}}`},
				{Role: "assistant", Content: `{"type":"done","summary":"json noted"}`},
				{Role: "user", Content: "second task"},
				{Role: "assistant", Content: `{"type":"done","summary":"done second"}`},
				{Role: "user", Content: "third task"},
				{Role: "assistant", Content: `{"type":"done","summary":"done third"}`},
			},
			keepRecentTurns: 2,
			wantBoundary:    4,
			wantOK:          true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotBoundary, gotOK := FindCompactBoundary(tt.messages, tt.keepRecentTurns)
			if gotBoundary != tt.wantBoundary || gotOK != tt.wantOK {
				t.Fatalf(
					"FindCompactBoundary() = (%d, %v), want (%d, %v)",
					gotBoundary,
					gotOK,
					tt.wantBoundary,
					tt.wantOK,
				)
			}
		})
	}
}

func TestRewriteForCompaction(t *testing.T) {
	foreignState := `{"type":"state","source":"user","cwd":"/wrong"}`
	staleCompact := FormatCompactMessage("stale summary")

	t.Run("keeps recent tail", func(t *testing.T) {
		messages := []Message{
			{Role: "user", Content: "first task"},
			{Role: "assistant", Content: `{"type":"done","summary":"done first"}`},
			{
				Role: "user",
				Content: FormatCommandResult(
					CommandResult{Command: "ls", ExitCode: 0, Stdout: ".\n"},
				),
			},
			{Role: "user", Content: "second task"},
			{Role: "assistant", Content: `{"type":"done","summary":"done second"}`},
			{Role: "user", Content: "third task"},
			{Role: "assistant", Content: `{"type":"done","summary":"done third"}`},
		}

		got, stats, err := RewriteForCompaction(messages, "summary", 2)
		if err != nil {
			t.Fatalf("RewriteForCompaction: %v", err)
		}
		assertMessages(t, got, []Message{
			{Role: "user", Content: FormatCompactMessage("summary")},
			{Role: "user", Content: "second task"},
			{Role: "assistant", Content: `{"type":"done","summary":"done second"}`},
			{Role: "user", Content: "third task"},
			{Role: "assistant", Content: `{"type":"done","summary":"done third"}`},
		})
		if stats != (CompactStats{CompactedMessages: 3, KeptMessages: 4}) {
			t.Fatalf(
				"stats = %#v, want %#v",
				stats,
				CompactStats{CompactedMessages: 3, KeptMessages: 4},
			)
		}
	})

	t.Run("preserves latest clnkr state before tail", func(t *testing.T) {
		messages := []Message{
			{Role: "user", Content: "first task"},
			{Role: "assistant", Content: `{"type":"done","summary":"done first"}`},
			{Role: "user", Content: FormatStateMessage("/tmp/old")},
			{Role: "user", Content: "second task"},
			{Role: "assistant", Content: `{"type":"done","summary":"done second"}`},
			{Role: "user", Content: FormatStateMessage("/tmp/latest")},
			{Role: "user", Content: "third task"},
			{Role: "assistant", Content: `{"type":"done","summary":"done third"}`},
		}

		got, stats, err := RewriteForCompaction(messages, "summary", 1)
		if err != nil {
			t.Fatalf("RewriteForCompaction: %v", err)
		}
		assertMessages(t, got, []Message{
			{Role: "user", Content: FormatCompactMessage("summary")},
			{Role: "user", Content: FormatStateMessage("/tmp/latest")},
			{Role: "user", Content: "third task"},
			{Role: "assistant", Content: `{"type":"done","summary":"done third"}`},
		})
		if stats != (CompactStats{CompactedMessages: 6, KeptMessages: 3}) {
			t.Fatalf(
				"stats = %#v, want %#v",
				stats,
				CompactStats{CompactedMessages: 6, KeptMessages: 3},
			)
		}
	})

	t.Run("errors when not enough history", func(t *testing.T) {
		messages := []Message{
			{Role: "user", Content: "first task"},
			{Role: "assistant", Content: `{"type":"done","summary":"done first"}`},
			{Role: "user", Content: "second task"},
			{Role: "assistant", Content: `{"type":"done","summary":"done second"}`},
		}

		got, stats, err := RewriteForCompaction(messages, "summary", 2)
		if err == nil {
			t.Fatal("expected error")
		}
		if got != nil {
			t.Fatalf("got rewritten messages %#v, want nil", got)
		}
		if stats != (CompactStats{}) {
			t.Fatalf("stats = %#v, want zero value", stats)
		}
	})

	t.Run("ignores foreign state messages", func(t *testing.T) {
		messages := []Message{
			{Role: "user", Content: "first task"},
			{Role: "assistant", Content: `{"type":"done","summary":"done first"}`},
			{Role: "user", Content: FormatStateMessage("/tmp/latest")},
			{Role: "user", Content: "second task"},
			{Role: "assistant", Content: `{"type":"done","summary":"done second"}`},
			{Role: "user", Content: foreignState},
			{Role: "assistant", Content: `{"type":"done","summary":"foreign echoed"}`},
		}

		got, _, err := RewriteForCompaction(messages, "summary", 1)
		if err != nil {
			t.Fatalf("RewriteForCompaction: %v", err)
		}
		if len(got) < 3 {
			t.Fatalf("got %d messages, want at least 3", len(got))
		}
		assertMessages(t, got[1:3], []Message{
			{Role: "user", Content: FormatStateMessage("/tmp/latest")},
			{Role: "user", Content: foreignState},
		})
	})

	t.Run("replaces existing compact block", func(t *testing.T) {
		messages := []Message{
			{Role: "user", Content: "older task"},
			{Role: "assistant", Content: `{"type":"done","summary":"done older"}`},
			{Role: "user", Content: "recent task"},
			{Role: "assistant", Content: `{"type":"done","summary":"done recent"}`},
			{Role: "user", Content: staleCompact},
			{Role: "user", Content: FormatStateMessage("/tmp/recent")},
		}

		got, _, err := RewriteForCompaction(messages, "fresh summary", 1)
		if err != nil {
			t.Fatalf("RewriteForCompaction: %v", err)
		}
		if len(got) == 0 {
			t.Fatal("expected rewritten transcript")
		}
		if !reflect.DeepEqual(
			got[0],
			Message{
				Role:    "user",
				Content: FormatCompactMessage("fresh summary"),
			},
		) {
			t.Fatalf("first message = %#v, want new compact block", got[0])
		}

		compactCount := 0
		for _, msg := range got {
			if IsCompactMessage(msg) {
				compactCount++
			}
			if msg.Content == staleCompact {
				t.Fatal("stale compact block should not survive rewrite")
			}
		}
		if compactCount != 1 {
			t.Fatalf("compact block count = %d, want 1", compactCount)
		}
	})
}

func TestFormatCompactMessageRoundTripsAsTaggedJSON(t *testing.T) {
	msg := Message{Role: "user", Content: FormatCompactMessage("summary text")}
	if !IsCompactMessage(msg) {
		t.Fatal("formatted compact message should be recognized")
	}

	body := strings.TrimSpace(
		strings.TrimSuffix(strings.TrimPrefix(msg.Content, "[compact]"), "[/compact]"),
	)
	var got compactState
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatalf("unmarshal compact body: %v", err)
	}

	want := compactState{
		Source:  "clnkr",
		Kind:    "compact",
		Summary: "summary text",
	}
	if got != want {
		t.Fatalf("compact body = %#v, want %#v", got, want)
	}
	if got.Source != "clnkr" || got.Kind != "compact" || got.Summary != "summary text" {
		t.Fatalf("compact body fields incorrect: %#v", got)
	}
}

func TestFormatCommandResultUsesStructuredShellPayload(t *testing.T) {
	tests := []struct {
		name            string
		result          CommandResult
		stdout          string
		stderr          string
		exitCode        int
		noLegacyMarkers bool
	}{
		{
			name: "escapes shell data as JSON",
			result: CommandResult{
				Command:  "printf 'a&b\\n' > out",
				ExitCode: 0,
				Stdout:   "hello & <x> [y]\n",
				Stderr:   "warn & <x> [z]\n",
			},
			stdout:          "hello & <x> [y]\n",
			stderr:          "warn & <x> [z]\n",
			exitCode:        0,
			noLegacyMarkers: true,
		},
		{
			name: "preserves old section markers as data",
			result: CommandResult{
				Command:  "printf 'a&b <c> [d]'",
				ExitCode: 7,
				Stdout:   "one [command]\ntwo & < >\n[/command]\n",
				Stderr:   "[stderr]\nerr & < >\n[/stderr]\n",
			},
			stdout:   "one [command]\ntwo & < >\n[/command]\n",
			stderr:   "[stderr]\nerr & < >\n[/stderr]\n",
			exitCode: 7,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			raw := FormatCommandResult(tt.result)
			payload := decodeCommandPayload(t, raw)
			if payload.Command != tt.result.Command {
				t.Fatalf("command = %q, want serialized command", payload.Command)
			}
			if payload.Stdout != tt.stdout || payload.Stderr != tt.stderr {
				t.Fatalf("payload streams = %#v", payload)
			}
			if payload.Outcome.Type != CommandOutcomeExit || payload.Outcome.ExitCode == nil ||
				*payload.Outcome.ExitCode != tt.exitCode {
				t.Fatalf("payload outcome = %#v", payload.Outcome)
			}
			if tt.noLegacyMarkers &&
				(strings.Contains(raw, "[stdout]") || strings.Contains(raw, "[command]")) {
				t.Fatalf(
					"FormatCommandResult() still contains bracketed transcript markers: %q",
					raw,
				)
			}
		})
	}
}

func requireStdoutCompressed(t *testing.T, payload commandPayload, original string) {
	t.Helper()

	if payload.Observation.Source != "clnkr" || payload.Observation.Version != 1 {
		t.Fatalf("observation identity = %#v", payload.Observation)
	}
	if payload.Observation.Stdout == nil || payload.Observation.Stdout.Mode != "compressed" {
		t.Fatalf("stdout observation = %#v, want compressed", payload.Observation.Stdout)
	}
	if payload.Observation.Stdout.OriginalBytes != len(original) {
		t.Fatalf(
			"original bytes = %d, want %d",
			payload.Observation.Stdout.OriginalBytes,
			len(original),
		)
	}
	if payload.Observation.Stdout.ShownBytes != len(payload.Stdout) {
		t.Fatalf(
			"shown bytes = %d, want %d",
			payload.Observation.Stdout.ShownBytes,
			len(payload.Stdout),
		)
	}
}

func TestFormatCommandResultCompressedStdoutRecordsMetadata(t *testing.T) {
	stdout := "stdout-head\n" +
		strings.Repeat("progress line\n", 80*1024/len("progress line\n")) +
		"build completed\nstdout-tail\n"
	payload := decodeCommandPayload(
		t,
		FormatCommandResult(CommandResult{Stdout: stdout, ExitCode: 0}),
	)

	for _, want := range []string{"[clnkr: stdout compressed; original ", "[head]", "[tail]", "stdout-head", "stdout-tail"} {
		if !strings.Contains(payload.Stdout, want) {
			t.Fatalf("stdout missing %q: %q", want, payload.Stdout)
		}
	}
	if strings.Contains(payload.Stdout, "omitted about ") {
		t.Fatalf("stdout contains omitted estimate: %q", payload.Stdout)
	}
	requireStdoutCompressed(t, payload, stdout)
	if payload.Observation.Stdout.OmittedBytes != len(stdout)-compressedRawKept(payload.Stdout) {
		t.Fatalf(
			"omitted bytes = %d, want %d",
			payload.Observation.Stdout.OmittedBytes,
			len(stdout)-compressedRawKept(payload.Stdout),
		)
	}
}

func TestFormatCommandResultLeavesShortStreamsUnchangedWithoutObservation(t *testing.T) {
	raw := FormatCommandResult(CommandResult{
		Command:  "printf hi",
		ExitCode: 0,
		Stdout:   "hello\n",
		Stderr:   "warn\n",
	})
	payload := decodeCommandPayload(t, raw)

	if payload.Stdout != "hello\n" || payload.Stderr != "warn\n" {
		t.Fatalf("payload streams = %#v", payload)
	}
	if _, ok := decodeCommandFields(t, raw)["observation"]; ok {
		t.Fatalf("result should not emit observation metadata: %s", raw)
	}
}

func TestFormatCommandResultDoesNotExtractSalientLinesForSuccess(t *testing.T) {
	stdout := "stdout-head\n" +
		strings.Repeat("error: noisy success log\n", 80*1024/len("error: noisy success log\n")) +
		"stdout-tail\n"
	payload := decodeCommandPayload(
		t,
		FormatCommandResult(CommandResult{Stdout: stdout, ExitCode: 0}),
	)

	if strings.Contains(payload.Stdout, "[salient]") {
		t.Fatalf("successful stdout contains salient section: %q", payload.Stdout)
	}
	requireStdoutCompressed(t, payload, stdout)
}

func TestFormatCommandResultUsesStdoutSalienceWhenFailureStderrEmpty(t *testing.T) {
	code := 1
	stdout := "stdout-head\n" +
		strings.Repeat("progress line\n", 40*1024/len("progress line\n")) +
		"Traceback (most recent call last):\n" +
		`  File "app.py", line 12, in <module>` + "\n" +
		"AssertionError: expected 3 got 4\n" +
		strings.Repeat("cleanup line\n", 50*1024/len("cleanup line\n")) +
		"stdout-tail\n"
	payload := decodeCommandPayload(t, FormatCommandResult(CommandResult{
		Stdout:  stdout,
		Outcome: CommandOutcome{Type: CommandOutcomeExit, ExitCode: &code},
	}))

	if payload.Stderr != "" {
		t.Fatalf("stderr = %q, want empty", payload.Stderr)
	}
	for _, want := range []string{"[salient]", "Traceback (most recent call last):", "AssertionError: expected 3 got 4"} {
		if !strings.Contains(payload.Stdout, want) {
			t.Fatalf("stdout missing %q: %q", want, payload.Stdout)
		}
	}
	requireStdoutCompressed(t, payload, stdout)
}

func TestFormatCommandResultCompressesFailureStderrWithSalientLines(t *testing.T) {
	code := 1
	stderr := "stderr-head\n" +
		"error: head diagnostic\n" +
		strings.Repeat("downloaded package\n", 40*1024/len("downloaded package\n")) +
		strings.Repeat("error: repeated noise\n", 3) +
		"pkg/foo.go:12: expected actual\n" +
		"expected actual\n" +
		"pkg/service.go:42:13: undefined: missingThing\n" +
		"--- FAIL: TestServiceBuild (0.01s)\n" +
		strings.Repeat("cleanup line\n", 50*1024/len("cleanup line\n")) +
		"stderr-tail\n"

	payload := decodeCommandPayload(t, FormatCommandResult(CommandResult{
		Stderr:  stderr,
		Outcome: CommandOutcome{Type: CommandOutcomeExit, ExitCode: &code},
	}))
	if !strings.Contains(payload.Stderr, "[salient]") {
		t.Fatalf("stderr missing salient section: %q", payload.Stderr)
	}
	for _, want := range []string{"pkg/service.go:42:13: undefined: missingThing", "--- FAIL: TestServiceBuild"} {
		if !strings.Contains(payload.Stderr, want) {
			t.Fatalf("stderr omitted %q: %q", want, payload.Stderr)
		}
	}
	salientStart := strings.Index(payload.Stderr, "[salient]\n") + len("[salient]\n")
	salientEnd := strings.Index(payload.Stderr, "\n[tail]\n")
	salient := payload.Stderr[salientStart:salientEnd]
	if strings.Count(salient, "error: repeated noise") != 1 {
		t.Fatalf("stderr should deduplicate exact salient lines: %q", salient)
	}
	if strings.Count(salient, "expected actual\n") != 2 {
		t.Fatalf(
			"stderr should preserve distinct salient lines that contain each other: %q",
			salient,
		)
	}
	if strings.Contains(salient, "error: head diagnostic") {
		t.Fatalf("stderr should not duplicate salient lines already kept in the head: %q", salient)
	}
	if !strings.Contains(payload.Stderr, "stderr-head") ||
		!strings.Contains(payload.Stderr, "stderr-tail") {
		t.Fatalf("stderr should preserve head and tail: %q", payload.Stderr)
	}
	if payload.Observation.Stderr == nil || payload.Observation.Stderr.Mode != "compressed" {
		t.Fatalf("stderr observation = %#v, want compressed", payload.Observation.Stderr)
	}
}

func TestCommandResultMessageDetectsCompressedObservationPayload(t *testing.T) {
	got := FormatCommandResult(CommandResult{
		Stdout:   "head\n" + strings.Repeat("noise\n", 80*1024/len("noise\n")) + "tail\n",
		ExitCode: 0,
	})
	if !commandResultMessage(got) {
		t.Fatalf("compressed command result was not classified as command: %s", got)
	}
}

func TestFormatDeniedAndSkippedResultsDoNotEmitObservationMetadata(t *testing.T) {
	for name, raw := range map[string]string{
		"denied":  FormatDeniedCommandResult("not this one"),
		"skipped": FormatSkippedCommandResult("previous command failed"),
	} {
		if _, ok := decodeCommandFields(t, raw)["observation"]; ok {
			t.Fatalf("%s result should not emit observation metadata: %s", name, raw)
		}
	}
}

func TestFormatCommandResultDoesNotSplitUTF8AtCompressionBoundary(t *testing.T) {
	stream := strings.Repeat("å", 40*1024) + "tail"
	payload := decodeCommandPayload(
		t,
		FormatCommandResult(CommandResult{Stdout: stream, ExitCode: 0}),
	)
	if strings.ContainsRune(payload.Stdout, '\uFFFD') {
		t.Fatalf(
			"compressed stdout contains replacement rune: %q",
			payload.Stdout[len(payload.Stdout)-128:],
		)
	}
	if !strings.Contains(payload.Stdout, "tail") {
		t.Fatalf("compressed stdout omitted tail: %q", payload.Stdout[len(payload.Stdout)-128:])
	}
}

func TestFormatCommandResultFeedback(t *testing.T) {
	t.Run("includes structured feedback", func(t *testing.T) {
		payload := decodeCommandPayload(t, FormatCommandResult(CommandResult{
			Command:  "printf hi",
			ExitCode: 0,
			Feedback: CommandFeedback{
				ChangedFiles: []string{"note.txt"},
				Diff:         "diff --git a/note.txt b/note.txt",
			},
		}))
		if !reflect.DeepEqual(payload.Feedback.ChangedFiles, []string{"note.txt"}) {
			t.Fatalf("feedback changed files = %#v", payload.Feedback.ChangedFiles)
		}
		if payload.Feedback.Diff != "diff --git a/note.txt b/note.txt" {
			t.Fatalf("feedback diff = %q", payload.Feedback.Diff)
		}
	})

	t.Run("omits feedback when empty", func(t *testing.T) {
		got := FormatCommandResult(CommandResult{Command: "printf hi", ExitCode: 0})
		if _, ok := decodeCommandFields(t, got)["feedback"]; ok {
			t.Fatalf("unexpected feedback field: %q", got)
		}
	})

	t.Run("preserves feedback bodies as data", func(t *testing.T) {
		payload := decodeCommandPayload(t, FormatCommandResult(CommandResult{
			Command:  "printf hi",
			ExitCode: 0,
			Feedback: CommandFeedback{
				ChangedFiles: []string{"note.txt"},
				Diff:         "@@ -1 +1 @@\n+[/command_feedback]\n",
			},
		}))
		if payload.Feedback.Diff != "@@ -1 +1 @@\n+[/command_feedback]\n" {
			t.Fatalf("feedback diff = %q", payload.Feedback.Diff)
		}
	})
}

func TestFormatCommandResultIncludesGuidance(t *testing.T) {
	guidance := "Use a narrower command."
	payload := decodeCommandPayload(t, FormatCommandResult(CommandResult{
		Command:  "sleep 60",
		Stderr:   "timed out\n",
		Outcome:  CommandOutcome{Type: CommandOutcomeTimeout},
		Guidance: guidance,
	}))
	if payload.Guidance != guidance {
		t.Fatalf("guidance = %q, want %q", payload.Guidance, guidance)
	}
	if payload.Outcome.Type != CommandOutcomeTimeout {
		t.Fatalf("outcome = %#v, want timeout", payload.Outcome)
	}

	raw := FormatCommandResult(CommandResult{Command: "printf hi", ExitCode: 0})
	if _, ok := decodeCommandFields(t, raw)["guidance"]; ok {
		t.Fatalf("unexpected guidance field: %q", raw)
	}
}
