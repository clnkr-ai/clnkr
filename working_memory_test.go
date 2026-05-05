package clnkr

import (
	"strings"
	"testing"
)

func TestWorkingMemoryValidateRequiresEnvelope(t *testing.T) {
	memory := WorkingMemory(`{"source":"clnkr","kind":"working_memory","version":1,"current_state":["green"]}`)
	if err := memory.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}

	memory = WorkingMemory(`{"source":"clnkr","kind":"summary","version":1}`)
	if err := memory.Validate(); err == nil || !strings.Contains(err.Error(), "kind") {
		t.Fatalf("Validate wrong kind error = %v, want kind error", err)
	}
}

func TestWorkingMemoryCloneDeepCopiesSlices(t *testing.T) {
	original := WorkingMemory(`{"source":"clnkr","kind":"working_memory","version":1,"current_state":["tests written"]}`)

	cloned := original.Clone()
	cloned[0] = '{'

	if string(original) != `{"source":"clnkr","kind":"working_memory","version":1,"current_state":["tests written"]}` {
		t.Fatalf("original mutated through clone: %s", original)
	}
}

func TestWorkingMemoryStatsUseJSONSize(t *testing.T) {
	previous := WorkingMemory(`{"source":"clnkr","kind":"working_memory","version":1,"current_state":["old"]}`)
	updated := WorkingMemory(`{"source":"clnkr","kind":"working_memory","version":1,"current_state":["new"]}`)

	stats := WorkingMemoryUpdateStats(previous, updated, 3, false)
	if stats.PreviousBytes == 0 || stats.UpdatedBytes == 0 {
		t.Fatalf("stats byte counts = %#v, want nonzero", stats)
	}
	if stats.DeltaMessages != 3 || stats.Rejected {
		t.Fatalf("stats = %#v, want delta 3 and accepted", stats)
	}
}
