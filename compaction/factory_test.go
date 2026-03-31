package compaction

import (
	"context"
	"errors"
	"testing"

	clnkr "github.com/clnkr-ai/clnkr"
)

type stubModel struct {
	response clnkr.Response
	err      error
	got      [][]clnkr.Message
}

func (m *stubModel) Query(_ context.Context, messages []clnkr.Message) (clnkr.Response, error) {
	cp := append([]clnkr.Message{}, messages...)
	m.got = append(m.got, cp)
	if m.err != nil {
		return clnkr.Response{}, m.err
	}
	return m.response, nil
}

func TestNewFactoryBuildsFreshCompactorPerInstructionSet(t *testing.T) {
	var gotInstructions []string
	var models []*stubModel
	factory := NewFactory(func(instructions string) clnkr.Model {
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

func TestModelCompactorTrimsSummaryText(t *testing.T) {
	model := &stubModel{response: clnkr.Response{
		Message: clnkr.Message{Role: "assistant", Content: "  summarized transcript  \n"},
	}}
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
	if len(model.got[0]) != len(messages) || model.got[0][0] != messages[0] {
		t.Fatalf("model got messages %#v, want %#v", model.got[0], messages)
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
