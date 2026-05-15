package compaction

import (
	"context"
	"errors"
	"strings"
	"testing"

	clnkr "github.com/clnkr-ai/clnkr"
)

type stubModel struct {
	summary string
	err     error
	got     [][]clnkr.Message
}

func (m *stubModel) QueryText(_ context.Context, messages []clnkr.Message) (string, error) {
	m.got = append(m.got, append([]clnkr.Message{}, messages...))
	if m.err != nil {
		return "", m.err
	}
	return m.summary, nil
}

func TestNewFactoryBuildsFreshCompactorPerInstructionSet(t *testing.T) {
	var gotInstructions []string
	var models []*stubModel
	factory := NewFactory(func(instructions string) FreeformModel {
		gotInstructions = append(gotInstructions, instructions)
		model := &stubModel{summary: "  summary for " + instructions + "  "}
		models = append(models, model)
		return model
	})

	first := factory("focus tests")
	second := factory("focus files")

	if len(gotInstructions) != 2 {
		t.Fatalf("factory built %d models, want 2", len(gotInstructions))
	}
	if gotInstructions[0] != "focus tests" || gotInstructions[1] != "focus files" {
		t.Fatalf("instructions = %#v, want [focus tests focus files]", gotInstructions)
	}

	firstSummary, err := first.Summarize(context.Background(), []clnkr.Message{{Role: "user", Content: "first task"}})
	if err != nil {
		t.Fatalf("first Summarize: %v", err)
	}
	secondSummary, err := second.Summarize(context.Background(), []clnkr.Message{{Role: "user", Content: "second task"}})
	if err != nil {
		t.Fatalf("second Summarize: %v", err)
	}
	if firstSummary != "summary for focus tests" {
		t.Fatalf("first summary = %q, want %q", firstSummary, "summary for focus tests")
	}
	if secondSummary != "summary for focus files" {
		t.Fatalf("second summary = %q, want %q", secondSummary, "summary for focus files")
	}
	if len(models[0].got) != 1 || len(models[1].got) != 1 {
		t.Fatalf("model calls = [%d %d], want [1 1]", len(models[0].got), len(models[1].got))
	}
	if !strings.Contains(models[0].got[0][0].Content, "first task") {
		t.Fatalf("first model query = %#v, want first task", models[0].got[0])
	}
	if !strings.Contains(models[1].got[0][0].Content, "second task") {
		t.Fatalf("second model query = %#v, want second task", models[1].got[0])
	}
}

func TestModelCompactorSummarizesSerializedTranscript(t *testing.T) {
	model := &stubModel{summary: "  summarized transcript  \n"}
	compactor := modelCompactor{model: model}
	messages := []clnkr.Message{
		{Role: "user", Content: "first task"},
		{Role: "assistant", Content: `{"type":"done","summary":"done first"}`},
	}

	got, err := compactor.Summarize(context.Background(), messages)
	if err != nil {
		t.Fatalf("Summarize: %v", err)
	}
	if got != "summarized transcript" {
		t.Fatalf("summary = %q, want %q", got, "summarized transcript")
	}
	if len(model.got) != 1 {
		t.Fatalf("model got %d calls, want 1", len(model.got))
	}

	query := model.got[0]
	if len(query) != 2 {
		t.Fatalf("model got %d messages, want 2", len(query))
	}
	if query[0].Role != "user" {
		t.Fatalf("first query role = %q, want user", query[0].Role)
	}
	for _, want := range []string{
		"<source_text>",
		"</source_text>",
		"[user]\nfirst task",
		"[assistant]\n{\"type\":\"done\",\"summary\":\"done first\"}",
	} {
		if !strings.Contains(query[0].Content, want) {
			t.Fatalf("first query missing %q in %q", want, query[0].Content)
		}
	}
	if query[1].Role != "user" || query[1].Content != summarizeRequest {
		t.Fatalf("last query = %#v, want trailing summarize request", query[1])
	}
	if !strings.Contains(query[1].Content, "Goal:\nConstraints:\nKey decisions:") {
		t.Fatalf("last query content = %q, want handoff format request", query[1].Content)
	}
	if !strings.Contains(query[1].Content, "Open questions / next steps:") {
		t.Fatalf("last query content = %q, want handoff section list", query[1].Content)
	}
}

func TestModelCompactorTruncatesOversizedPrefixBeforeQuery(t *testing.T) {
	model := &stubModel{summary: "summary"}
	compactor := modelCompactor{model: model}
	messages := []clnkr.Message{
		{Role: "user", Content: "small opener"},
		{Role: "assistant", Content: strings.Repeat("x", summarizeInputCharBudget+1)},
	}

	if _, err := compactor.Summarize(context.Background(), messages); err != nil {
		t.Fatalf("Summarize: %v", err)
	}
	if len(model.got) != 1 {
		t.Fatalf("model got %d calls, want 1", len(model.got))
	}
	query := model.got[0]
	if len(query) != 2 {
		t.Fatalf("model got %d messages, want 2", len(query))
	}
	if query[0].Role != "user" {
		t.Fatalf("first query role = %q, want user", query[0].Role)
	}
	if !strings.HasPrefix(query[0].Content, "<source_text>\n[compact_context_truncated]\n") {
		t.Fatalf("first query content = %q, want fenced truncation block prefix", query[0].Content)
	}
	if len(query[0].Content) > summarizeInputCharBudget {
		t.Fatalf("truncation block length = %d, want <= %d", len(query[0].Content), summarizeInputCharBudget)
	}
	if !strings.HasSuffix(query[0].Content, "\n</source_text>") {
		t.Fatalf("first query content = %q, want source_text close tag", query[0].Content)
	}
	if query[1].Role != "user" || query[1].Content != summarizeRequest {
		t.Fatalf("last query = %#v, want trailing summarize request", query[1])
	}
}

func TestModelCompactorReturnsModelError(t *testing.T) {
	wantErr := errors.New("query failed")
	compactor := modelCompactor{model: &stubModel{err: wantErr}}

	_, err := compactor.Summarize(context.Background(), []clnkr.Message{{Role: "user", Content: "first task"}})
	if !errors.Is(err, wantErr) {
		t.Fatalf("error = %v, want %v", err, wantErr)
	}
}

func TestTailStringWithinByteBudgetPreservesUTF8(t *testing.T) {
	got := tailStringWithinByteBudget("ab界", len("b界"))
	if got != "b界" {
		t.Fatalf("tailStringWithinByteBudget = %q, want %q", got, "b界")
	}
}
