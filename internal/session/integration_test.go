package session_test

import (
	"context"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/clnkr-ai/clnkr"
	"github.com/clnkr-ai/clnkr/internal/session"
)

type fakeCompactor struct{ summary string }

func (c fakeCompactor) Summarize(_ context.Context, _ []clnkr.Message) (string, error) {
	return c.summary, nil
}

func stateMessage(source, cwd string) string {
	return `{"type":"state","source":"` + source + `","cwd":"` + cwd + `"}`
}

func verifiedDone(summary string) *clnkr.DoneTurn {
	return &clnkr.DoneTurn{
		Summary: summary,
		Verification: clnkr.CompletionVerification{
			Status: clnkr.VerificationVerified,
			Checks: []clnkr.VerificationCheck{{
				Command:  "test -d .",
				Outcome:  "passed",
				Evidence: "current working directory was available for completion",
			}},
		},
		KnownRisks: []string{},
	}
}

func doneMessage(t *testing.T, summary string) clnkr.Message {
	t.Helper()

	content, err := clnkr.CanonicalTurnJSON(verifiedDone(summary))
	if err != nil {
		t.Fatalf("CanonicalTurnJSON: %v", err)
	}
	return clnkr.Message{Role: "assistant", Content: content}
}

func saveAndLoad(t *testing.T, pwd string, messages []clnkr.Message) []clnkr.Message {
	t.Helper()

	if err := session.SaveSession(pwd, messages); err != nil {
		t.Fatalf("SaveSession: %v", err)
	}
	loaded, err := session.LoadLatestSession(pwd)
	if err != nil {
		t.Fatalf("LoadLatestSession: %v", err)
	}
	return loaded
}

func compactTranscript(t *testing.T, cwd string, messages []clnkr.Message) []clnkr.Message {
	t.Helper()

	agent := clnkr.NewAgent(nil, nil, cwd)
	if err := agent.AddMessages(messages); err != nil {
		t.Fatalf("AddMessages before compact: %v", err)
	}

	_, err := agent.Compact(
		context.Background(),
		fakeCompactor{summary: "Older work summarized."},
		clnkr.CompactOptions{
			Instructions:    "focus on failing tests",
			KeepRecentTurns: 2,
		},
	)
	if err != nil {
		t.Fatalf("Compact: %v", err)
	}

	compacted := agent.Messages()
	if len(compacted) == 0 {
		t.Fatal("compact transcript produced no messages")
	}
	if compacted[0].Role != "user" {
		t.Fatalf("compact block role = %q, want user", compacted[0].Role)
	}
	if !strings.HasPrefix(compacted[0].Content, "[compact]\n") {
		t.Fatalf("first message is not a compact block: %q", compacted[0].Content)
	}
	return compacted
}

func TestSessionRoundTrip(t *testing.T) {
	tmpdir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", tmpdir)
	projdir := filepath.Join(tmpdir, "testproj")

	sessions, err := session.ListSessions(projdir)
	if err != nil {
		t.Fatalf("ListSessions (empty): %v", err)
	}
	if len(sessions) != 0 {
		t.Fatalf("Expected no sessions, got %d", len(sessions))
	}

	want := []clnkr.Message{
		{Role: "user", Content: "test prompt"},
		{Role: "assistant", Content: "test response"},
	}
	if loaded := saveAndLoad(t, projdir, want); !reflect.DeepEqual(loaded, want) {
		t.Fatalf("loaded = %#v, want %#v", loaded, want)
	}

	sessions, err = session.ListSessions(projdir)
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(sessions) != 1 || sessions[0].Messages != len(want) {
		t.Fatalf("sessions = %#v, want one session with %d messages", sessions, len(want))
	}
}

func TestSessionIsolationBetweenProjects(t *testing.T) {
	tmpdir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", tmpdir)

	projects := map[string]string{
		"/tmp/project-alpha": "alpha",
		"/tmp/project-beta":  "beta",
	}
	for project, content := range projects {
		if err := session.SaveSession(
			project,
			[]clnkr.Message{{Role: "user", Content: content}},
		); err != nil {
			t.Fatalf("SaveSession %s: %v", project, err)
		}
	}
	for project, want := range projects {
		sessions, err := session.ListSessions(project)
		if err != nil {
			t.Fatalf("ListSessions %s: %v", project, err)
		}
		if len(sessions) != 1 {
			t.Fatalf("ListSessions %s got %d sessions, want 1", project, len(sessions))
		}

		loaded, err := session.LoadLatestSession(project)
		if err != nil {
			t.Fatalf("LoadLatestSession %s: %v", project, err)
		}
		if len(loaded) != 1 || loaded[0].Content != want {
			t.Fatalf("LoadLatestSession %s = %#v, want content %q", project, loaded, want)
		}
	}
}

func TestContinueRestoresCanonicalAssistantTurn(t *testing.T) {
	tmpdir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", tmpdir)

	want := []clnkr.Message{
		{Role: "user", Content: "first task"},
		doneMessage(t, "saved summary"),
		{Role: "user", Content: stateMessage("clnkr", "/restored")},
	}
	loaded := saveAndLoad(t, filepath.Join(tmpdir, "continue-proj"), want)
	if !reflect.DeepEqual(loaded, want) {
		t.Fatalf("loaded = %#v, want %#v", loaded, want)
	}

	agent := clnkr.NewAgent(nil, nil, "/wrong")
	if err := agent.AddMessages(loaded); err != nil {
		t.Fatalf("AddMessages: %v", err)
	}
	got := agent.Messages()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("messages = %#v, want %#v", got, want)
	}
	turn, err := clnkr.ParseTurn(got[1].Content)
	if err != nil {
		t.Fatalf("ParseTurn(assistant): %v", err)
	}
	done, ok := turn.(*clnkr.DoneTurn)
	if !ok {
		t.Fatalf("assistant turn = %T, want *clnkr.DoneTurn", turn)
	}
	if done.Summary != "saved summary" || agent.Cwd() != "/restored" {
		t.Fatalf(
			"summary=%q cwd=%q, want summary %q cwd %q",
			done.Summary,
			agent.Cwd(),
			"saved summary",
			"/restored",
		)
	}
}

func TestSessionRoundTripWithCompactionState(t *testing.T) {
	tmpdir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", tmpdir)

	tests := []struct {
		name     string
		cwd      string
		messages []clnkr.Message
		wantCwd  string
	}{
		{
			name: "round trips compact block and restores cwd from trailing valid state",
			cwd:  "/wrong",
			messages: []clnkr.Message{
				{Role: "user", Content: "first task"},
				doneMessage(t, "done first"),
				{Role: "user", Content: stateMessage("clnkr", "/old")},
				{Role: "user", Content: "second task"},
				doneMessage(t, "done second"),
				{Role: "user", Content: "third task"},
				doneMessage(t, "done third"),
				{Role: "user", Content: stateMessage("clnkr", "/restored")},
			},
			wantCwd: "/restored",
		},
		{
			name: "foreign state messages still do not restore cwd after compaction",
			cwd:  "/original",
			messages: []clnkr.Message{
				{Role: "user", Content: "first task"},
				doneMessage(t, "done first"),
				{Role: "user", Content: "second task"},
				doneMessage(t, "done second"),
				{Role: "user", Content: stateMessage("user", "/wrong")},
				doneMessage(t, "foreign state tail"),
				{Role: "user", Content: "third task"},
				doneMessage(t, "done third"),
			},
			wantCwd: "/original",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			compacted := compactTranscript(t, tt.cwd, tt.messages)
			loaded := saveAndLoad(
				t,
				filepath.Join(tmpdir, strings.ReplaceAll(tt.name, " ", "-")),
				compacted,
			)
			if !reflect.DeepEqual(loaded, compacted) {
				t.Fatalf("loaded = %#v, want %#v", loaded, compacted)
			}

			agent := clnkr.NewAgent(nil, nil, tt.cwd)
			if err := agent.AddMessages(loaded); err != nil {
				t.Fatalf("AddMessages: %v", err)
			}
			if agent.Cwd() != tt.wantCwd {
				t.Fatalf("agent cwd = %q, want %q", agent.Cwd(), tt.wantCwd)
			}
		})
	}
}
