package clnkrapp

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/clnkr-ai/clnkr"
	"github.com/clnkr-ai/clnkr/cmd/internal/providerconfig"
)

func TestWriteTrajectoryWritesIndentedMessages(t *testing.T) {
	path := filepath.Join(t.TempDir(), "trajectory.json")
	messages := []clnkr.Message{{Role: "user", Content: "hello"}}

	if err := WriteTrajectory(path, messages); err != nil {
		t.Fatalf("WriteTrajectory: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !bytes.Contains(data, []byte("\n  ")) {
		t.Fatalf("trajectory was not indented: %q", data)
	}

	var got []clnkr.Message
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if !reflect.DeepEqual(got, messages) {
		t.Fatalf("messages = %#v, want %#v", got, messages)
	}
}

func TestHarnessMetadataDebugEventFormatsJSON(t *testing.T) {
	meta := NewHarnessMetadata("test-version", providerconfig.ResolvedProviderConfig{
		Provider:    providerconfig.ProviderOpenAI,
		ProviderAPI: providerconfig.ProviderAPIOpenAIResponses,
		Model:       "gpt-5.1",
		HarnessOptions: providerconfig.HarnessOptions{
			ReasoningEffort: "high",
			MaxOutputTokens: providerconfig.OptionalInt{Value: 8000, Set: true},
		},
	}, "system prompt")

	event, err := HarnessMetadataDebugEvent(meta)
	if err != nil {
		t.Fatalf("HarnessMetadataDebugEvent: %v", err)
	}
	var got HarnessMetadata
	if err := json.Unmarshal([]byte(event.Message), &got); err != nil {
		t.Fatalf("debug message is not JSON metadata: %v", err)
	}
	if got.ClnkrVersion != "test-version" || got.Provider != providerconfig.ProviderOpenAI || got.Model != "gpt-5.1" {
		t.Fatalf("metadata = %#v, want version/provider/model", got)
	}
	if got.PromptSHA256 != "e16202309c92180728dd7fd1c59f16004a6d5ee245538c28d2a9a22edf2dd2ab" {
		t.Fatalf("PromptSHA256 = %q, want sha256(system prompt)", got.PromptSHA256)
	}
	if got.Effective.ReasoningEffort == nil || *got.Effective.ReasoningEffort != "high" {
		t.Fatalf("Effective.ReasoningEffort = %#v, want high", got.Effective.ReasoningEffort)
	}
}

func TestLoadMessagesAcceptsLegacyArrayAndEnvelope(t *testing.T) {
	messages := []clnkr.Message{{Role: "user", Content: "hello"}}
	legacy, err := json.Marshal(messages)
	if err != nil {
		t.Fatalf("Marshal legacy: %v", err)
	}
	got, err := LoadMessages(legacy)
	if err != nil {
		t.Fatalf("LoadMessages legacy: %v", err)
	}
	if !reflect.DeepEqual(got, messages) {
		t.Fatalf("legacy messages = %#v, want %#v", got, messages)
	}

	envelope, err := json.Marshal(struct {
		Metadata map[string]string `json:"metadata"`
		Messages []clnkr.Message   `json:"messages"`
	}{Metadata: map[string]string{"version": "dev"}, Messages: messages})
	if err != nil {
		t.Fatalf("Marshal envelope: %v", err)
	}
	got, err = LoadMessages(envelope)
	if err != nil {
		t.Fatalf("LoadMessages envelope: %v", err)
	}
	if !reflect.DeepEqual(got, messages) {
		t.Fatalf("envelope messages = %#v, want %#v", got, messages)
	}
}

func TestLoadMessagesRejectsEnvelopeWithoutMessages(t *testing.T) {
	_, err := LoadMessages([]byte(`{"metadata":{"version":"dev"}}`))
	if err == nil || err.Error() != "parse messages: missing messages" {
		t.Fatalf("LoadMessages error = %v, want missing messages", err)
	}
}
