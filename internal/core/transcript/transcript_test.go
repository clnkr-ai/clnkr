package transcript

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

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
				{Role: "user", Content: FormatCommandResult(CommandResult{Command: "ls", ExitCode: 0, Stdout: ".\n"})},
				{Role: "user", Content: FormatStateMessage("/tmp/old")},
				{Role: "user", Content: FormatCompactMessage("older summary", "")},
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
			wantBoundary:    0,
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
				t.Fatalf("FindCompactBoundary() = (%d, %v), want (%d, %v)", gotBoundary, gotOK, tt.wantBoundary, tt.wantOK)
			}
		})
	}
}

func TestRewriteForCompactionKeepsRecentTail(t *testing.T) {
	messages := []Message{
		{Role: "user", Content: "first task"},
		{Role: "assistant", Content: `{"type":"done","summary":"done first"}`},
		{Role: "user", Content: FormatCommandResult(CommandResult{Command: "ls", ExitCode: 0, Stdout: ".\n"})},
		{Role: "user", Content: "second task"},
		{Role: "assistant", Content: `{"type":"done","summary":"done second"}`},
		{Role: "user", Content: "third task"},
		{Role: "assistant", Content: `{"type":"done","summary":"done third"}`},
	}

	got, stats, err := RewriteForCompaction(messages, "summary", "focus on recent changes", 2)
	if err != nil {
		t.Fatalf("RewriteForCompaction: %v", err)
	}

	want := []Message{
		{Role: "user", Content: FormatCompactMessage("summary", "focus on recent changes")},
		{Role: "user", Content: "second task"},
		{Role: "assistant", Content: `{"type":"done","summary":"done second"}`},
		{Role: "user", Content: "third task"},
		{Role: "assistant", Content: `{"type":"done","summary":"done third"}`},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d messages, want %d", len(got), len(want))
	}
	for i := range want {
		if !reflect.DeepEqual(got[i], want[i]) {
			t.Fatalf("message %d = %#v, want %#v", i, got[i], want[i])
		}
	}

	if stats != (CompactStats{CompactedMessages: 3, KeptMessages: 4}) {
		t.Fatalf("stats = %#v, want %#v", stats, CompactStats{CompactedMessages: 3, KeptMessages: 4})
	}
}

func TestRewriteForCompactionPreservesLatestState(t *testing.T) {
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

	got, stats, err := RewriteForCompaction(messages, "summary", "", 1)
	if err != nil {
		t.Fatalf("RewriteForCompaction: %v", err)
	}

	want := []Message{
		{Role: "user", Content: FormatCompactMessage("summary", "")},
		{Role: "user", Content: FormatStateMessage("/tmp/latest")},
		{Role: "user", Content: "third task"},
		{Role: "assistant", Content: `{"type":"done","summary":"done third"}`},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d messages, want %d", len(got), len(want))
	}
	for i := range want {
		if !reflect.DeepEqual(got[i], want[i]) {
			t.Fatalf("message %d = %#v, want %#v", i, got[i], want[i])
		}
	}

	if stats != (CompactStats{CompactedMessages: 6, KeptMessages: 3}) {
		t.Fatalf("stats = %#v, want %#v", stats, CompactStats{CompactedMessages: 6, KeptMessages: 3})
	}
}

func TestRewriteForCompactionErrorsWhenNotEnoughHistory(t *testing.T) {
	messages := []Message{
		{Role: "user", Content: "first task"},
		{Role: "assistant", Content: `{"type":"done","summary":"done first"}`},
		{Role: "user", Content: "second task"},
		{Role: "assistant", Content: `{"type":"done","summary":"done second"}`},
	}

	got, stats, err := RewriteForCompaction(messages, "summary", "", 2)
	if err == nil {
		t.Fatal("expected error")
	}
	if got != nil {
		t.Fatalf("got rewritten messages %#v, want nil", got)
	}
	if stats != (CompactStats{}) {
		t.Fatalf("stats = %#v, want zero value", stats)
	}
}

func TestFormatCompactMessageRoundTripsAsTaggedJSON(t *testing.T) {
	msg := Message{Role: "user", Content: FormatCompactMessage("summary text", "focus on tests")}
	if !IsCompactMessage(msg) {
		t.Fatal("formatted compact message should be recognized")
	}

	body := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(msg.Content, "[compact]"), "[/compact]"))
	var got compactState
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatalf("unmarshal compact body: %v", err)
	}

	want := compactState{
		Source:       "clnkr",
		Kind:         "compact",
		Instructions: "focus on tests",
		Summary:      "summary text",
	}
	if got != want {
		t.Fatalf("compact body = %#v, want %#v", got, want)
	}
}

func TestRewriteForCompactionIgnoresForeignStateMessages(t *testing.T) {
	foreignState := `{"type":"state","source":"user","cwd":"/wrong"}`
	messages := []Message{
		{Role: "user", Content: "first task"},
		{Role: "assistant", Content: `{"type":"done","summary":"done first"}`},
		{Role: "user", Content: FormatStateMessage("/tmp/latest")},
		{Role: "user", Content: "second task"},
		{Role: "assistant", Content: `{"type":"done","summary":"done second"}`},
		{Role: "user", Content: foreignState},
		{Role: "assistant", Content: `{"type":"done","summary":"foreign echoed"}`},
	}

	got, _, err := RewriteForCompaction(messages, "summary", "", 1)
	if err != nil {
		t.Fatalf("RewriteForCompaction: %v", err)
	}

	if len(got) < 3 {
		t.Fatalf("got %d messages, want at least 3", len(got))
	}
	if !reflect.DeepEqual(got[1], Message{Role: "user", Content: FormatStateMessage("/tmp/latest")}) {
		t.Fatalf("preserved state = %#v, want latest clnkr state", got[1])
	}
	if !reflect.DeepEqual(got[2], Message{Role: "user", Content: foreignState}) {
		t.Fatalf("foreign state tail message = %#v, want %#v", got[2], Message{Role: "user", Content: foreignState})
	}
}

func TestRewriteForCompactionHandlesExistingCompactBlock(t *testing.T) {
	staleCompact := FormatCompactMessage("stale summary", "old instructions")
	messages := []Message{
		{Role: "user", Content: "older task"},
		{Role: "assistant", Content: `{"type":"done","summary":"done older"}`},
		{Role: "user", Content: "recent task"},
		{Role: "assistant", Content: `{"type":"done","summary":"done recent"}`},
		{Role: "user", Content: staleCompact},
		{Role: "user", Content: FormatStateMessage("/tmp/recent")},
	}

	got, _, err := RewriteForCompaction(messages, "fresh summary", "new instructions", 1)
	if err != nil {
		t.Fatalf("RewriteForCompaction: %v", err)
	}

	if len(got) == 0 {
		t.Fatal("expected rewritten transcript")
	}
	if !reflect.DeepEqual(got[0], Message{Role: "user", Content: FormatCompactMessage("fresh summary", "new instructions")}) {
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
}

func TestFormatCommandResultUsesStructuredShellPayload(t *testing.T) {
	got := FormatCommandResult(CommandResult{
		Command:  "printf 'a&b\\n' > out",
		ExitCode: 0,
		Stdout:   "hello & <x> [y]\n",
		Stderr:   "warn & <x> [z]\n",
	})
	var payload struct {
		Command string `json:"command"`
		Stdout  string `json:"stdout"`
		Stderr  string `json:"stderr"`
		Outcome struct {
			Type     string `json:"type"`
			ExitCode int    `json:"exit_code"`
		} `json:"outcome"`
	}
	if err := json.Unmarshal([]byte(got), &payload); err != nil {
		t.Fatalf("FormatCommandResult() returned invalid JSON: %v\n%s", err, got)
	}
	if payload.Command != "printf 'a&b\\n' > out" {
		t.Fatalf("command = %q, want serialized command", payload.Command)
	}
	if payload.Stdout != "hello & <x> [y]\n" || payload.Stderr != "warn & <x> [z]\n" {
		t.Fatalf("payload streams = %#v", payload)
	}
	if payload.Outcome.Type != "exit" || payload.Outcome.ExitCode != 0 {
		t.Fatalf("payload outcome = %#v", payload.Outcome)
	}
	if strings.Contains(got, "[stdout]") || strings.Contains(got, "[command]") {
		t.Fatalf("FormatCommandResult() still contains bracketed transcript markers: %q", got)
	}
}

func TestFormatCommandResultPreservesSectionMarkersAsData(t *testing.T) {
	got := FormatCommandResult(CommandResult{
		Command:  "printf 'a&b <c> [d]'",
		ExitCode: 7,
		Stdout:   "one [command]\ntwo & < >\n[/command]\n",
		Stderr:   "[stderr]\nerr & < >\n[/stderr]\n",
	})

	var payload struct {
		Stdout  string `json:"stdout"`
		Stderr  string `json:"stderr"`
		Outcome struct {
			Type     string `json:"type"`
			ExitCode int    `json:"exit_code"`
		} `json:"outcome"`
	}
	if err := json.Unmarshal([]byte(got), &payload); err != nil {
		t.Fatalf("FormatCommandResult() returned invalid JSON: %v\n%s", err, got)
	}
	if payload.Stdout != "one [command]\ntwo & < >\n[/command]\n" {
		t.Fatalf("stdout = %q", payload.Stdout)
	}
	if payload.Stderr != "[stderr]\nerr & < >\n[/stderr]\n" {
		t.Fatalf("stderr = %q", payload.Stderr)
	}
	if payload.Outcome.Type != "exit" || payload.Outcome.ExitCode != 7 {
		t.Fatalf("outcome = %#v", payload.Outcome)
	}
}

func TestFormatCommandResultReportsActualOmittedBytes(t *testing.T) {
	stream := "head-" + strings.Repeat("x", 70*1024) + "-tail"
	got := FormatCommandResult(CommandResult{Stdout: stream, ExitCode: 0})

	var payload struct {
		Stdout      string `json:"stdout"`
		Observation struct {
			Stdout struct {
				OriginalBytes int    `json:"original_bytes"`
				ShownBytes    int    `json:"shown_bytes"`
				OmittedBytes  int    `json:"omitted_bytes"`
				Mode          string `json:"mode"`
			} `json:"stdout"`
		} `json:"observation"`
	}
	if err := json.Unmarshal([]byte(got), &payload); err != nil {
		t.Fatalf("FormatCommandResult() returned invalid JSON: %v\n%s", err, got)
	}
	if !strings.Contains(payload.Stdout, "[clnkr: stdout compressed; original ") {
		t.Fatalf("stdout missing compression marker: %q", payload.Stdout)
	}
	if strings.Contains(payload.Stdout, "omitted about ") {
		t.Fatalf("stdout marker has estimated omitted byte count: %q", payload.Stdout)
	}
	if payload.Observation.Stdout.Mode != "compressed" {
		t.Fatalf("stdout mode = %q, want compressed", payload.Observation.Stdout.Mode)
	}
	if payload.Observation.Stdout.OriginalBytes != len(stream) {
		t.Fatalf("original bytes = %d, want %d", payload.Observation.Stdout.OriginalBytes, len(stream))
	}
	if payload.Observation.Stdout.ShownBytes != len(payload.Stdout) {
		t.Fatalf("shown bytes = %d, want %d", payload.Observation.Stdout.ShownBytes, len(payload.Stdout))
	}
	headStart := strings.Index(payload.Stdout, "[head]\n") + len("[head]\n")
	tailStart := strings.Index(payload.Stdout, "\n[tail]\n")
	tailEnd := tailStart + len("\n[tail]\n")
	rawKept := len(payload.Stdout[headStart:tailStart]) + len(payload.Stdout[tailEnd:])
	if payload.Observation.Stdout.OmittedBytes != len(stream)-rawKept {
		t.Fatalf("omitted bytes = %d, want %d", payload.Observation.Stdout.OmittedBytes, len(stream)-rawKept)
	}
}

func TestFormatCommandResultLeavesShortStreamsUnchangedWithoutObservation(t *testing.T) {
	got := FormatCommandResult(CommandResult{
		Command:  "printf hi",
		ExitCode: 0,
		Stdout:   "hello\n",
		Stderr:   "warn\n",
	})

	var payload map[string]json.RawMessage
	if err := json.Unmarshal([]byte(got), &payload); err != nil {
		t.Fatalf("FormatCommandResult() returned invalid JSON: %v\n%s", err, got)
	}
	var stdout, stderr string
	if err := json.Unmarshal(payload["stdout"], &stdout); err != nil {
		t.Fatalf("stdout is not a string: %v", err)
	}
	if err := json.Unmarshal(payload["stderr"], &stderr); err != nil {
		t.Fatalf("stderr is not a string: %v", err)
	}
	if stdout != "hello\n" || stderr != "warn\n" {
		t.Fatalf("streams = stdout %q stderr %q", stdout, stderr)
	}
	if _, ok := payload["observation"]; ok {
		t.Fatalf("short streams should not emit observation metadata: %s", got)
	}
}

func TestFormatCommandResultCompressesOversizedSuccessOutputWithMetadata(t *testing.T) {
	stream := "stdout-head\n" + strings.Repeat("progress line\n", 80*1024/len("progress line\n")) + "build completed\nstdout-tail\n"
	got := FormatCommandResult(CommandResult{Stdout: stream, ExitCode: 0})

	var payload struct {
		Stdout      string `json:"stdout"`
		Observation struct {
			Source  string `json:"source"`
			Version int    `json:"version"`
			Stdout  struct {
				OriginalBytes int    `json:"original_bytes"`
				ShownBytes    int    `json:"shown_bytes"`
				OmittedBytes  int    `json:"omitted_bytes"`
				Mode          string `json:"mode"`
			} `json:"stdout"`
		} `json:"observation"`
	}
	if err := json.Unmarshal([]byte(got), &payload); err != nil {
		t.Fatalf("FormatCommandResult() returned invalid JSON: %v\n%s", err, got)
	}
	if !strings.Contains(payload.Stdout, "[clnkr: stdout compressed; original ") {
		t.Fatalf("stdout missing compression marker: %q", payload.Stdout[:128])
	}
	if !strings.Contains(payload.Stdout, "[head]") || !strings.Contains(payload.Stdout, "[tail]") {
		t.Fatalf("stdout missing head/tail labels: %q", payload.Stdout[:256])
	}
	if !strings.Contains(payload.Stdout, "stdout-head") || !strings.Contains(payload.Stdout, "stdout-tail") {
		t.Fatalf("stdout should preserve head and tail: %q", payload.Stdout)
	}
	if payload.Observation.Source != "clnkr" || payload.Observation.Version != 1 {
		t.Fatalf("observation identity = %#v", payload.Observation)
	}
	if payload.Observation.Stdout.Mode != "compressed" {
		t.Fatalf("stdout mode = %q, want compressed", payload.Observation.Stdout.Mode)
	}
	if payload.Observation.Stdout.OriginalBytes != len(stream) {
		t.Fatalf("original bytes = %d, want %d", payload.Observation.Stdout.OriginalBytes, len(stream))
	}
	if payload.Observation.Stdout.ShownBytes != len(payload.Stdout) {
		t.Fatalf("shown bytes = %d, want %d", payload.Observation.Stdout.ShownBytes, len(payload.Stdout))
	}
	headStart := strings.Index(payload.Stdout, "[head]\n") + len("[head]\n")
	tailStart := strings.Index(payload.Stdout, "\n[tail]\n")
	tailEnd := tailStart + len("\n[tail]\n")
	rawKept := len(payload.Stdout[headStart:tailStart]) + len(payload.Stdout[tailEnd:])
	if payload.Observation.Stdout.OmittedBytes != len(stream)-rawKept {
		t.Fatalf("omitted bytes = %d, want %d", payload.Observation.Stdout.OmittedBytes, len(stream)-rawKept)
	}
}

func TestFormatCommandResultDoesNotExtractSalientLinesForSuccess(t *testing.T) {
	stream := "stdout-head\n" + strings.Repeat("error: noisy success log\n", 80*1024/len("error: noisy success log\n")) + "stdout-tail\n"
	got := FormatCommandResult(CommandResult{Stdout: stream, ExitCode: 0})

	var payload struct {
		Stdout string `json:"stdout"`
	}
	if err := json.Unmarshal([]byte(got), &payload); err != nil {
		t.Fatalf("FormatCommandResult() returned invalid JSON: %v\n%s", err, got)
	}
	if strings.Contains(payload.Stdout, "[salient]") {
		t.Fatalf("successful stdout should not include salient section: %q", payload.Stdout)
	}
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

	got := FormatCommandResult(CommandResult{
		Stderr:  stderr,
		Outcome: CommandOutcome{Type: CommandOutcomeExit, ExitCode: &code},
	})

	var payload struct {
		Stderr      string `json:"stderr"`
		Observation struct {
			Stderr struct {
				Mode string `json:"mode"`
			} `json:"stderr"`
		} `json:"observation"`
	}
	if err := json.Unmarshal([]byte(got), &payload); err != nil {
		t.Fatalf("FormatCommandResult() returned invalid JSON: %v\n%s", err, got)
	}
	if !strings.Contains(payload.Stderr, "[salient]") {
		t.Fatalf("stderr missing salient section: %q", payload.Stderr)
	}
	if !strings.Contains(payload.Stderr, "pkg/service.go:42:13: undefined: missingThing") {
		t.Fatalf("stderr omitted compiler diagnostic: %q", payload.Stderr)
	}
	if !strings.Contains(payload.Stderr, "--- FAIL: TestServiceBuild") {
		t.Fatalf("stderr omitted Go test failure: %q", payload.Stderr)
	}
	salientStart := strings.Index(payload.Stderr, "[salient]\n") + len("[salient]\n")
	salientEnd := strings.Index(payload.Stderr, "\n[tail]\n")
	if strings.Count(payload.Stderr[salientStart:salientEnd], "error: repeated noise") != 1 {
		t.Fatalf("stderr should deduplicate exact salient lines: %q", payload.Stderr[salientStart:salientEnd])
	}
	if strings.Count(payload.Stderr[salientStart:salientEnd], "expected actual\n") != 2 {
		t.Fatalf("stderr should preserve distinct salient lines that contain each other: %q", payload.Stderr[salientStart:salientEnd])
	}
	if strings.Contains(payload.Stderr[salientStart:salientEnd], "error: head diagnostic") {
		t.Fatalf("stderr should not duplicate salient lines already kept in the head: %q", payload.Stderr[salientStart:salientEnd])
	}
	if !strings.Contains(payload.Stderr, "stderr-head") || !strings.Contains(payload.Stderr, "stderr-tail") {
		t.Fatalf("stderr should preserve head and tail: %q", payload.Stderr)
	}
	if payload.Observation.Stderr.Mode != "compressed" {
		t.Fatalf("stderr mode = %q, want compressed", payload.Observation.Stderr.Mode)
	}
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

	got := FormatCommandResult(CommandResult{
		Stdout:  stdout,
		Outcome: CommandOutcome{Type: CommandOutcomeExit, ExitCode: &code},
	})

	var payload struct {
		Stdout string `json:"stdout"`
		Stderr string `json:"stderr"`
	}
	if err := json.Unmarshal([]byte(got), &payload); err != nil {
		t.Fatalf("FormatCommandResult() returned invalid JSON: %v\n%s", err, got)
	}
	if payload.Stderr != "" {
		t.Fatalf("stderr = %q, want empty", payload.Stderr)
	}
	if !strings.Contains(payload.Stdout, "[salient]") {
		t.Fatalf("stdout missing salient section: %q", payload.Stdout)
	}
	if !strings.Contains(payload.Stdout, "Traceback (most recent call last):") {
		t.Fatalf("stdout omitted traceback header: %q", payload.Stdout)
	}
	if !strings.Contains(payload.Stdout, "AssertionError: expected 3 got 4") {
		t.Fatalf("stdout omitted assertion: %q", payload.Stdout)
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
		var payload map[string]json.RawMessage
		if err := json.Unmarshal([]byte(raw), &payload); err != nil {
			t.Fatalf("%s result is not JSON: %v\n%s", name, err, raw)
		}
		if _, ok := payload["observation"]; ok {
			t.Fatalf("%s result should not emit observation metadata: %s", name, raw)
		}
	}
}

func TestFormatCommandResultDoesNotSplitUTF8AtCompressionBoundary(t *testing.T) {
	stream := strings.Repeat("å", 40*1024) + "tail"
	got := FormatCommandResult(CommandResult{Stdout: stream, ExitCode: 0})

	var payload struct {
		Stdout string `json:"stdout"`
	}
	if err := json.Unmarshal([]byte(got), &payload); err != nil {
		t.Fatalf("FormatCommandResult() returned invalid JSON: %v\n%s", err, got)
	}
	if strings.ContainsRune(payload.Stdout, '\uFFFD') {
		t.Fatalf("compressed stdout contains replacement rune: %q", payload.Stdout[len(payload.Stdout)-128:])
	}
	if !strings.Contains(payload.Stdout, "tail") {
		t.Fatalf("compressed stdout omitted tail: %q", payload.Stdout[len(payload.Stdout)-128:])
	}
}

func TestFormatCommandResultIncludesStructuredFeedback(t *testing.T) {
	got := FormatCommandResult(CommandResult{
		Command:  "printf hi",
		ExitCode: 0,
		Feedback: CommandFeedback{
			ChangedFiles: []string{"note.txt"},
			Diff:         "diff --git a/note.txt b/note.txt",
		},
	})

	var payload struct {
		Feedback CommandFeedback `json:"feedback"`
	}
	if err := json.Unmarshal([]byte(got), &payload); err != nil {
		t.Fatalf("FormatCommandResult() returned invalid JSON: %v\n%s", err, got)
	}
	if !reflect.DeepEqual(payload.Feedback.ChangedFiles, []string{"note.txt"}) {
		t.Fatalf("feedback changed files = %#v", payload.Feedback.ChangedFiles)
	}
	if payload.Feedback.Diff != "diff --git a/note.txt b/note.txt" {
		t.Fatalf("feedback diff = %q", payload.Feedback.Diff)
	}
}

func TestFormatCommandResultOmitsFeedbackWhenEmpty(t *testing.T) {
	got := FormatCommandResult(CommandResult{
		Command:  "printf hi",
		ExitCode: 0,
	})

	var payload map[string]json.RawMessage
	if err := json.Unmarshal([]byte(got), &payload); err != nil {
		t.Fatalf("FormatCommandResult() returned invalid JSON: %v\n%s", err, got)
	}
	if _, ok := payload["feedback"]; ok {
		t.Fatalf("unexpected feedback field: %q", got)
	}
}

func TestFormatCommandResultPreservesFeedbackBodiesAsData(t *testing.T) {
	got := FormatCommandResult(CommandResult{
		Command:  "printf hi",
		ExitCode: 0,
		Feedback: CommandFeedback{
			ChangedFiles: []string{"note.txt"},
			Diff:         "@@ -1 +1 @@\n+[/command_feedback]\n",
		},
	})

	var payload struct {
		Feedback CommandFeedback `json:"feedback"`
	}
	if err := json.Unmarshal([]byte(got), &payload); err != nil {
		t.Fatalf("FormatCommandResult() returned invalid JSON: %v\n%s", err, got)
	}
	if payload.Feedback.Diff != "@@ -1 +1 @@\n+[/command_feedback]\n" {
		t.Fatalf("feedback diff = %q", payload.Feedback.Diff)
	}
}
