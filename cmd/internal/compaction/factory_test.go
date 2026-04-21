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
	cp := append([]clnkr.Message{}, messages...)
	m.got = append(m.got, cp)
	if m.err != nil {
		return "", m.err
	}
	return m.summary, nil
}

func TestCompactionUsesFreeformModelInterface(t *testing.T) {
	var gotInstructions []string
	var models []FreeformModel
	factory := NewFactory(func(instructions string) FreeformModel {
		gotInstructions = append(gotInstructions, instructions)
		model := &stubModel{}
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

	firstCompactor, ok := first.(modelCompactor)
	if !ok {
		t.Fatalf("first compactor type = %T, want modelCompactor", first)
	}
	secondCompactor, ok := second.(modelCompactor)
	if !ok {
		t.Fatalf("second compactor type = %T, want modelCompactor", second)
	}
	if firstCompactor.model != models[0] {
		t.Fatal("first compactor should wrap the first model instance")
	}
	if secondCompactor.model != models[1] {
		t.Fatal("second compactor should wrap the second model instance")
	}
	if firstCompactor.model == secondCompactor.model {
		t.Fatal("factory should build a fresh model per instruction set")
	}
}

func TestCompactionDoesNotRequireRuntimeTurnModel(t *testing.T) {
	model := &stubModel{summary: "  summarized transcript  \n"}
	compactor := modelCompactor{model: model}
	messages := []clnkr.Message{{Role: "user", Content: "first task"}}

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
	if len(model.got[0]) != len(messages)+1 {
		t.Fatalf("model got %d messages, want %d", len(model.got[0]), len(messages)+1)
	}
	for i := range messages {
		if model.got[0][i] != messages[i] {
			t.Fatalf("model got message %d = %#v, want %#v", i, model.got[0][i], messages[i])
		}
	}
	last := model.got[0][len(model.got[0])-1]
	if last.Role != "user" {
		t.Fatalf("last query role = %q, want user", last.Role)
	}
	if last.Content == "" {
		t.Fatal("last query content should contain a summarize request")
	}
}

func TestNewFactoryBuildsFreshCompactorPerInstructionSet(t *testing.T) {
	TestCompactionUsesFreeformModelInterface(t)
}

func TestModelCompactorTrimsSummaryText(t *testing.T) {
	TestCompactionDoesNotRequireRuntimeTurnModel(t)
}

func TestModelCompactorAppendsSummarizeRequestAfterAssistantTail(t *testing.T) {
	model := &stubModel{summary: "summary"}
	compactor := modelCompactor{model: model}
	messages := []clnkr.Message{
		{Role: "user", Content: "first task"},
		{Role: "assistant", Content: `{"type":"done","summary":"done first"}`},
	}

	if _, err := compactor.Summarize(context.Background(), messages); err != nil {
		t.Fatalf("Summarize: %v", err)
	}
	if len(model.got) != 1 {
		t.Fatalf("model got %d calls, want 1", len(model.got))
	}
	got := model.got[0]
	if len(got) != len(messages)+1 {
		t.Fatalf("model got %d messages, want %d", len(got), len(messages)+1)
	}
	for i := range messages {
		if got[i] != messages[i] {
			t.Fatalf("model got message %d = %#v, want %#v", i, got[i], messages[i])
		}
	}
	last := got[len(got)-1]
	if last.Role != "user" {
		t.Fatalf("last query role = %q, want user", last.Role)
	}
	if last.Content == "" {
		t.Fatal("last query content should contain a summarize request")
	}
}

func TestModelCompactorTruncatesOversizedPrefixBeforeQuery(t *testing.T) {
	model := &stubModel{summary: "summary"}
	compactor := modelCompactor{model: model}
	oversized := strings.Repeat("x", summarizeInputCharBudget+1)
	messages := []clnkr.Message{
		{Role: "user", Content: "small opener"},
		{Role: "assistant", Content: oversized},
	}

	if _, err := compactor.Summarize(context.Background(), messages); err != nil {
		t.Fatalf("Summarize: %v", err)
	}
	if len(model.got) != 1 {
		t.Fatalf("model got %d calls, want 1", len(model.got))
	}
	got := model.got[0]
	if len(got) != 2 {
		t.Fatalf("model got %d messages, want 2", len(got))
	}
	if got[0].Role != "user" {
		t.Fatalf("first query role = %q, want user", got[0].Role)
	}
	if !strings.HasPrefix(got[0].Content, "[compact_context_truncated]\n") {
		t.Fatalf("first query content = %q, want truncation block prefix", got[0].Content)
	}
	if len(got[0].Content) > summarizeInputCharBudget {
		t.Fatalf("truncation block length = %d, want <= %d", len(got[0].Content), summarizeInputCharBudget)
	}
	if got[1].Role != "user" || got[1].Content != summarizeRequest {
		t.Fatalf("last query = %#v, want trailing summarize request", got[1])
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

func TestTailWithinBudgetPreservesUTF8(t *testing.T) {
	got := tailWithinBudget([]clnkr.Message{{Role: "assistant", Content: "ab界"}}, len("b界"))
	if got != "b界" {
		t.Fatalf("tailWithinBudget = %q, want %q", got, "b界")
	}
}
