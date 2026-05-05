package workingmemory

import (
	"context"
	"strings"
	"testing"

	"github.com/clnkr-ai/clnkr"
)

type stubModel struct {
	got      [][]clnkr.Message
	response string
	err      error
}

func (m *stubModel) QueryText(_ context.Context, messages []clnkr.Message) (string, error) {
	m.got = append(m.got, append([]clnkr.Message(nil), messages...))
	if m.err != nil {
		return "", m.err
	}
	return m.response, nil
}

func TestUpdaterUsesFreeformModelAndValidatesJSON(t *testing.T) {
	model := &stubModel{response: `{"source":"clnkr","kind":"working_memory","version":1,"current_state":["done"]}`}
	factory := NewFactory(func() FreeformModel { return model })
	updater := factory()

	got, err := updater.UpdateWorkingMemory(context.Background(), clnkr.WorkingMemoryUpdateInput{
		Reason:   clnkr.WorkingMemoryUpdateReasonPrompt,
		Cwd:      "/repo",
		Messages: []clnkr.Message{{Role: "user", Content: "hello"}},
	})
	if err != nil {
		t.Fatalf("UpdateWorkingMemory: %v", err)
	}
	if !strings.Contains(string(got), `"done"`) {
		t.Fatalf("working memory = %#v, want done state", got)
	}
	if len(model.got) != 1 || len(model.got[0]) != 2 {
		t.Fatalf("model messages = %#v, want source text and request", model.got)
	}
	if !strings.Contains(model.got[0][0].Content, "<source_text>") {
		t.Fatalf("source message missing source_text wrapper: %q", model.got[0][0].Content)
	}
}

func TestUpdaterRejectsInvalidEnvelope(t *testing.T) {
	model := &stubModel{response: `{"source":"user","kind":"working_memory","version":1}`}
	updater := NewFactory(func() FreeformModel { return model })()

	_, err := updater.UpdateWorkingMemory(context.Background(), clnkr.WorkingMemoryUpdateInput{})
	if err == nil || !strings.Contains(err.Error(), "validate working memory") {
		t.Fatalf("UpdateWorkingMemory error = %v, want validation error", err)
	}
}
