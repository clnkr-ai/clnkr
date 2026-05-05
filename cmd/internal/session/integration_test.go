package session_test

import (
	"context"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/clnkr-ai/clnkr"
	"github.com/clnkr-ai/clnkr/cmd/internal/session"
)

type fakeCompactor struct {
	summary string
	err     error
}

func (c fakeCompactor) Summarize(_ context.Context, _ []clnkr.Message) (string, error) {
	if c.err != nil {
		return "", c.err
	}
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

func compactTranscript(t *testing.T, cwd string, messages []clnkr.Message) []clnkr.Message {
	t.Helper()

	agent := clnkr.NewAgent(nil, nil, cwd)
	if err := agent.AddMessages(messages); err != nil {
		t.Fatalf("AddMessages before compact: %v", err)
	}

	_, err := agent.Compact(context.Background(), fakeCompactor{summary: "Older work summarized."}, clnkr.CompactOptions{
		Instructions:    "focus on failing tests",
		KeepRecentTurns: 2,
	})
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

	testMsgs := []clnkr.Message{
		{Role: "user", Content: "test prompt"},
		{Role: "assistant", Content: "test response"},
	}
	if err := session.SaveSession(projdir, testMsgs); err != nil {
		t.Fatalf("SaveSession: %v", err)
	}

	loaded, err := session.LoadLatestSession(projdir)
	if err != nil {
		t.Fatalf("LoadLatestSession: %v", err)
	}
	if len(loaded) != 2 {
		t.Fatalf("Expected 2 messages, got %d", len(loaded))
	}
	if loaded[0].Content != "test prompt" {
		t.Errorf("First message wrong: %q", loaded[0].Content)
	}
	if loaded[1].Content != "test response" {
		t.Errorf("Second message wrong: %q", loaded[1].Content)
	}

	sessions, err = session.ListSessions(projdir)
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(sessions) != 1 {
		t.Fatalf("Expected 1 session, got %d", len(sessions))
	}
	if sessions[0].Messages != 2 {
		t.Errorf("Expected 2 messages in session info, got %d", sessions[0].Messages)
	}
}

func TestSessionIsolationBetweenProjects(t *testing.T) {
	tmpdir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", tmpdir)

	proj1 := "/tmp/project-alpha"
	proj2 := "/tmp/project-beta"

	if err := session.SaveSession(proj1, []clnkr.Message{
		{Role: "user", Content: "alpha"},
	}); err != nil {
		t.Fatalf("SaveSession proj1: %v", err)
	}

	if err := session.SaveSession(proj2, []clnkr.Message{
		{Role: "user", Content: "beta"},
	}); err != nil {
		t.Fatalf("SaveSession proj2: %v", err)
	}

	s1, _ := session.ListSessions(proj1)
	s2, _ := session.ListSessions(proj2)

	if len(s1) != 1 || len(s2) != 1 {
		t.Fatalf("Expected 1 session each, got proj1=%d, proj2=%d", len(s1), len(s2))
	}

	m1, _ := session.LoadLatestSession(proj1)
	m2, _ := session.LoadLatestSession(proj2)

	if m1[0].Content != "alpha" {
		t.Errorf("proj1 loaded wrong content: %q", m1[0].Content)
	}
	if m2[0].Content != "beta" {
		t.Errorf("proj2 loaded wrong content: %q", m2[0].Content)
	}
}

func TestContinueRestoresCanonicalAssistantTurn(t *testing.T) {
	tmpdir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", tmpdir)

	projdir := filepath.Join(tmpdir, "continue-proj")
	doneText, err := clnkr.CanonicalTurnJSON(verifiedDone("saved summary"))
	if err != nil {
		t.Fatalf("CanonicalTurnJSON: %v", err)
	}
	want := []clnkr.Message{
		{Role: "user", Content: "first task"},
		{Role: "assistant", Content: doneText},
		{Role: "user", Content: stateMessage("clnkr", "/restored")},
	}

	if err := session.SaveSession(projdir, want); err != nil {
		t.Fatalf("SaveSession: %v", err)
	}

	loaded, err := session.LoadLatestSession(projdir)
	if err != nil {
		t.Fatalf("LoadLatestSession: %v", err)
	}
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
	if done.Summary != "saved summary" {
		t.Fatalf("done summary = %q, want %q", done.Summary, "saved summary")
	}
	if agent.Cwd() != "/restored" {
		t.Fatalf("agent cwd = %q, want %q", agent.Cwd(), "/restored")
	}
}

func TestSessionRoundTripWithCompactionState(t *testing.T) {
	tmpdir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", tmpdir)

	t.Run("round trips compact block and restores cwd from trailing valid state", func(t *testing.T) {
		projdir := filepath.Join(tmpdir, "testproj-valid")
		compacted := compactTranscript(t, "/wrong", []clnkr.Message{
			{Role: "user", Content: "first task"},
			{Role: "assistant", Content: `{"type":"done","summary":"done first","verification":{"status":"verified","checks":[{"command":"go test ./...","outcome":"passed","evidence":"go test ./... passed and ls output showed current directory entries for completion"}]},"known_risks":[]}`},
			{Role: "user", Content: stateMessage("clnkr", "/old")},
			{Role: "user", Content: "second task"},
			{Role: "assistant", Content: `{"type":"done","summary":"done second","verification":{"status":"verified","checks":[{"command":"go test ./...","outcome":"passed","evidence":"go test ./... passed and ls output showed current directory entries for completion"}]},"known_risks":[]}`},
			{Role: "user", Content: "third task"},
			{Role: "assistant", Content: `{"type":"done","summary":"done third","verification":{"status":"verified","checks":[{"command":"go test ./...","outcome":"passed","evidence":"go test ./... passed and ls output showed current directory entries for completion"}]},"known_risks":[]}`},
			{Role: "user", Content: stateMessage("clnkr", "/restored")},
		})

		if err := session.SaveSession(projdir, compacted); err != nil {
			t.Fatalf("SaveSession: %v", err)
		}

		loaded, err := session.LoadLatestSession(projdir)
		if err != nil {
			t.Fatalf("LoadLatestSession: %v", err)
		}

		if len(loaded) != len(compacted) {
			t.Fatalf("loaded %d messages, want %d", len(loaded), len(compacted))
		}
		for i := range compacted {
			if !reflect.DeepEqual(loaded[i], compacted[i]) {
				t.Fatalf("loaded[%d] = %#v, want %#v", i, loaded[i], compacted[i])
			}
		}
		if loaded[0].Content != compacted[0].Content {
			t.Fatalf("compact block changed during round-trip: got %q want %q", loaded[0].Content, compacted[0].Content)
		}

		agent := clnkr.NewAgent(nil, nil, "/wrong")
		if err := agent.AddMessages(loaded); err != nil {
			t.Fatalf("AddMessages: %v", err)
		}
		if agent.Cwd() != "/restored" {
			t.Fatalf("agent cwd = %q, want %q", agent.Cwd(), "/restored")
		}
	})

	t.Run("foreign state messages still do not restore cwd after compaction", func(t *testing.T) {
		projdir := filepath.Join(tmpdir, "testproj-foreign")
		compacted := compactTranscript(t, "/original", []clnkr.Message{
			{Role: "user", Content: "first task"},
			{Role: "assistant", Content: `{"type":"done","summary":"done first","verification":{"status":"verified","checks":[{"command":"go test ./...","outcome":"passed","evidence":"go test ./... passed and ls output showed current directory entries for completion"}]},"known_risks":[]}`},
			{Role: "user", Content: "second task"},
			{Role: "assistant", Content: `{"type":"done","summary":"done second","verification":{"status":"verified","checks":[{"command":"go test ./...","outcome":"passed","evidence":"go test ./... passed and ls output showed current directory entries for completion"}]},"known_risks":[]}`},
			{Role: "user", Content: stateMessage("user", "/wrong")},
			{Role: "assistant", Content: `{"type":"done","summary":"foreign state tail","verification":{"status":"verified","checks":[{"command":"go test ./...","outcome":"passed","evidence":"go test ./... passed and ls output showed current directory entries for completion"}]},"known_risks":[]}`},
			{Role: "user", Content: "third task"},
			{Role: "assistant", Content: `{"type":"done","summary":"done third","verification":{"status":"verified","checks":[{"command":"go test ./...","outcome":"passed","evidence":"go test ./... passed and ls output showed current directory entries for completion"}]},"known_risks":[]}`},
		})

		if err := session.SaveSession(projdir, compacted); err != nil {
			t.Fatalf("SaveSession foreign: %v", err)
		}

		loaded, err := session.LoadLatestSession(projdir)
		if err != nil {
			t.Fatalf("LoadLatestSession foreign: %v", err)
		}

		foreignAgent := clnkr.NewAgent(nil, nil, "/original")
		if err := foreignAgent.AddMessages(loaded); err != nil {
			t.Fatalf("AddMessages foreign: %v", err)
		}
		if foreignAgent.Cwd() != "/original" {
			t.Fatalf("foreign agent cwd = %q, want %q", foreignAgent.Cwd(), "/original")
		}
	})
}
