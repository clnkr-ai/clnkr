package clnkr

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/clnkr-ai/clnkr/internal/core/transcript"
)

const (
	WorkingMemorySource  = "clnkr"
	WorkingMemoryKind    = "working_memory"
	WorkingMemoryVersion = 1

	WorkingMemoryUpdateReasonPrompt  = "prompt"
	WorkingMemoryUpdateReasonCommand = "command"
	WorkingMemoryUpdateReasonCompact = "compact"
	WorkingMemoryUpdateReasonSave    = "save"
)

const maxWorkingMemoryBytes = 64 * 1024

type WorkingMemory []byte

type WorkingMemoryStats struct {
	PreviousBytes int
	UpdatedBytes  int
	DeltaMessages int
	Rejected      bool
}

type WorkingMemoryUpdateInput struct {
	Previous      WorkingMemory
	Messages      []Message
	Cwd           string
	Reason        string
	DeltaMessages int
}

type WorkingMemoryUpdater interface {
	UpdateWorkingMemory(context.Context, WorkingMemoryUpdateInput) (WorkingMemory, error)
}

func (m WorkingMemory) IsZero() bool {
	return len(m) == 0
}

func (m WorkingMemory) Clone() WorkingMemory {
	return append(WorkingMemory(nil), m...)
}

func (m WorkingMemory) Validate() error {
	if m.IsZero() {
		return nil
	}
	var envelope struct {
		Source  string `json:"source"`
		Kind    string `json:"kind"`
		Version int    `json:"version"`
	}
	if len(m) > maxWorkingMemoryBytes {
		return fmt.Errorf("size = %d bytes, max %d", len(m), maxWorkingMemoryBytes)
	}
	if err := json.Unmarshal(m, &envelope); err != nil {
		return fmt.Errorf("decode envelope: %w", err)
	}
	if envelope.Source != WorkingMemorySource {
		return fmt.Errorf("source = %q, want %q", envelope.Source, WorkingMemorySource)
	}
	if envelope.Kind != WorkingMemoryKind {
		return fmt.Errorf("kind = %q, want %q", envelope.Kind, WorkingMemoryKind)
	}
	if envelope.Version != WorkingMemoryVersion {
		return fmt.Errorf("version = %d, want %d", envelope.Version, WorkingMemoryVersion)
	}
	return nil
}

func (m WorkingMemory) MarshalJSON() ([]byte, error) {
	if m.IsZero() {
		return []byte("null"), nil
	}
	if err := m.Validate(); err != nil {
		return nil, err
	}
	return append([]byte(nil), m...), nil
}

func (m *WorkingMemory) UnmarshalJSON(data []byte) error {
	if string(data) == "null" {
		*m = nil
		return nil
	}
	var compacted []byte
	if !json.Valid(data) {
		return fmt.Errorf("invalid JSON")
	}
	compacted = append(compacted, data...)
	memory := WorkingMemory(compacted)
	if err := memory.Validate(); err != nil {
		return err
	}
	*m = memory
	return nil
}

func (in WorkingMemoryUpdateInput) Clone() WorkingMemoryUpdateInput {
	return WorkingMemoryUpdateInput{
		Previous:      in.Previous.Clone(),
		Messages:      transcript.CloneMessages(in.Messages),
		Cwd:           in.Cwd,
		Reason:        in.Reason,
		DeltaMessages: in.DeltaMessages,
	}
}

func WorkingMemoryUpdateStats(previous, updated WorkingMemory, deltaMessages int, rejected bool) WorkingMemoryStats {
	return WorkingMemoryStats{
		PreviousBytes: len(previous),
		UpdatedBytes:  len(updated),
		DeltaMessages: deltaMessages,
		Rejected:      rejected,
	}
}
