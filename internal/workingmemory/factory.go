package workingmemory

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/clnkr-ai/clnkr"
)

type FreeformModel interface {
	QueryText(ctx context.Context, messages []clnkr.Message) (string, error)
}

type Factory func() clnkr.WorkingMemoryUpdater

type ModelFactory func() FreeformModel

func NewFactory(makeModel ModelFactory) Factory {
	return func() clnkr.WorkingMemoryUpdater {
		return modelUpdater{model: makeModel()}
	}
}

type modelUpdater struct {
	model FreeformModel
}

type Memory struct {
	Source        string            `json:"source"`
	Kind          string            `json:"kind"`
	Version       int               `json:"version"`
	UpdatedAt     string            `json:"updated_at,omitempty"`
	Task          MemoryTask        `json:"task,omitempty"`
	Constraints   []MemoryEntry     `json:"constraints,omitempty"`
	Decisions     []MemoryEntry     `json:"decisions,omitempty"`
	Discoveries   []MemoryDiscovery `json:"discoveries,omitempty"`
	Files         []MemoryFile      `json:"files,omitempty"`
	Attempts      []MemoryAttempt   `json:"attempts,omitempty"`
	CurrentState  []string          `json:"current_state,omitempty"`
	OpenQuestions []string          `json:"open_questions,omitempty"`
	NextSteps     []string          `json:"next_steps,omitempty"`
}

type MemoryTask struct {
	Summary string `json:"summary,omitempty"`
	Status  string `json:"status,omitempty"`
}

type MemoryEntry struct {
	Text       string `json:"text"`
	Source     string `json:"source,omitempty"`
	Turn       int    `json:"turn,omitempty"`
	Confidence string `json:"confidence,omitempty"`
}

type MemoryDiscovery struct {
	Text       string   `json:"text"`
	Files      []string `json:"files,omitempty"`
	Source     string   `json:"source,omitempty"`
	Turn       int      `json:"turn,omitempty"`
	Confidence string   `json:"confidence,omitempty"`
}

type MemoryFile struct {
	Path   string `json:"path"`
	Reason string `json:"reason,omitempty"`
	Status string `json:"status,omitempty"`
}

type MemoryAttempt struct {
	Text    string `json:"text"`
	Outcome string `json:"outcome,omitempty"`
	Turn    int    `json:"turn,omitempty"`
}

const (
	sourceTextOpen  = "<source_text>\n"
	sourceTextClose = "\n</source_text>"
	updateRequest   = "Return updated working-memory JSON only."
	inputCharBudget = 120000
)

func (u modelUpdater) UpdateWorkingMemory(ctx context.Context, input clnkr.WorkingMemoryUpdateInput) (clnkr.WorkingMemory, error) {
	text, err := u.model.QueryText(ctx, buildUpdateMessages(input))
	if err != nil {
		return clnkr.WorkingMemory{}, err
	}
	memory, err := Decode([]byte(strings.TrimSpace(text)))
	if err != nil {
		return clnkr.WorkingMemory{}, err
	}
	return memory, nil
}

func Encode(memory Memory) (clnkr.WorkingMemory, error) {
	if err := validate(memory); err != nil {
		return nil, err
	}
	data, err := json.Marshal(memory)
	if err != nil {
		return nil, fmt.Errorf("encode working memory: %w", err)
	}
	result := clnkr.WorkingMemory(data)
	if err := result.Validate(); err != nil {
		return nil, err
	}
	return result, nil
}

func Decode(data []byte) (clnkr.WorkingMemory, error) {
	var memory Memory
	if err := json.Unmarshal(data, &memory); err != nil {
		return clnkr.WorkingMemory{}, fmt.Errorf("decode working memory: %w", err)
	}
	result, err := Encode(memory)
	if err != nil {
		return clnkr.WorkingMemory{}, fmt.Errorf("validate working memory: %w", err)
	}
	return result, nil
}

func validate(memory Memory) error {
	if memory.Source != clnkr.WorkingMemorySource {
		return fmt.Errorf("source = %q, want %q", memory.Source, clnkr.WorkingMemorySource)
	}
	if memory.Kind != clnkr.WorkingMemoryKind {
		return fmt.Errorf("kind = %q, want %q", memory.Kind, clnkr.WorkingMemoryKind)
	}
	if memory.Version != clnkr.WorkingMemoryVersion {
		return fmt.Errorf("version = %d, want %d", memory.Version, clnkr.WorkingMemoryVersion)
	}
	for _, entry := range memory.Constraints {
		if err := validateConfidence(entry.Confidence); err != nil {
			return fmt.Errorf("constraints: %w", err)
		}
	}
	for _, entry := range memory.Decisions {
		if err := validateConfidence(entry.Confidence); err != nil {
			return fmt.Errorf("decisions: %w", err)
		}
	}
	for _, entry := range memory.Discoveries {
		if err := validateConfidence(entry.Confidence); err != nil {
			return fmt.Errorf("discoveries: %w", err)
		}
	}
	return nil
}

func validateConfidence(value string) error {
	switch value {
	case "", "low", "medium", "high":
		return nil
	default:
		return fmt.Errorf("confidence = %q, want low, medium, or high", value)
	}
}

func buildUpdateMessages(input clnkr.WorkingMemoryUpdateInput) []clnkr.Message {
	source := formatSourceText(input)
	available := inputCharBudget - len(sourceTextOpen) - len(sourceTextClose)
	if len(source) > available {
		source = source[len(source)-available:]
	}
	return []clnkr.Message{
		{Role: "user", Content: sourceTextOpen + source + sourceTextClose},
		{Role: "user", Content: updateRequest},
	}
}

func formatSourceText(input clnkr.WorkingMemoryUpdateInput) string {
	var b strings.Builder
	fmt.Fprintf(&b, "reason: %s\ncwd: %s\n\nprevious_working_memory:\n", input.Reason, input.Cwd)
	prev, _ := json.Marshal(input.Previous)
	b.Write(prev)
	b.WriteString("\n\nmessages:\n")
	for _, msg := range input.Messages {
		b.WriteString("[")
		b.WriteString(msg.Role)
		b.WriteString("]\n")
		b.WriteString(msg.Content)
		b.WriteString("\n\n")
	}
	return b.String()
}
