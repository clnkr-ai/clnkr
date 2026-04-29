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
	providerdomain "github.com/clnkr-ai/clnkr/internal/providers/providerconfig"
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

func TestRunMetadataDebugEventFormatsJSON(t *testing.T) {
	meta := NewRunMetadata("test-version", providerconfig.ResolvedProviderConfig{
		Provider:    providerdomain.ProviderOpenAI,
		ProviderAPI: providerdomain.ProviderAPIOpenAIResponses,
		Model:       "gpt-5.1",
		RequestOptions: providerdomain.ProviderRequestOptions{
			Effort: providerdomain.ProviderEffortOptions{Level: "high", Set: true},
			Output: providerdomain.ProviderOutputOptions{
				MaxOutputTokens: providerdomain.OptionalInt{Value: 8000, Set: true},
			},
		},
	}, "system prompt")

	event, err := RunMetadataDebugEvent(meta)
	if err != nil {
		t.Fatalf("RunMetadataDebugEvent: %v", err)
	}
	var got RunMetadata
	if err := json.Unmarshal([]byte(event.Message), &got); err != nil {
		t.Fatalf("debug message is not JSON metadata: %v", err)
	}
	if got.ClnkrVersion != "test-version" || got.Provider != providerdomain.ProviderOpenAI || got.Model != "gpt-5.1" {
		t.Fatalf("metadata = %#v, want version/provider/model", got)
	}
	if got.PromptSHA256 != "e16202309c92180728dd7fd1c59f16004a6d5ee245538c28d2a9a22edf2dd2ab" {
		t.Fatalf("PromptSHA256 = %q, want sha256(system prompt)", got.PromptSHA256)
	}
	if got.Effective.Effort.Level == nil || *got.Effective.Effort.Level != "high" {
		t.Fatalf("Effective.Effort.Level = %#v, want high", got.Effective.Effort.Level)
	}
}

func TestRunMetadataMirrorsProviderRequestShape(t *testing.T) {
	meta := NewRunMetadata("test-version", providerconfig.ResolvedProviderConfig{
		Provider:    providerdomain.ProviderOpenAI,
		ProviderAPI: providerdomain.ProviderAPIOpenAIResponses,
		Model:       "gpt-5.1",
		ActProtocol: clnkr.ActProtocolToolCalls,
		RequestOptions: providerdomain.ProviderRequestOptions{
			Effort: providerdomain.ProviderEffortOptions{Level: "auto", Set: true},
			Output: providerdomain.ProviderOutputOptions{
				MaxOutputTokens: providerdomain.OptionalInt{Value: 8000, Set: true},
			},
		},
	}, "system prompt")

	data, err := json.Marshal(meta)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var got struct {
		ActProtocol  string          `json:"act_protocol"`
		TurnProtocol json.RawMessage `json:"turn_protocol"`
		Requested    struct {
			Effort struct {
				LevelOmitted bool    `json:"level_omitted"`
				Level        *string `json:"level"`
			} `json:"effort"`
			Output struct {
				MaxOutputTokensOmitted bool `json:"max_output_tokens_omitted"`
				MaxOutputTokens        *int `json:"max_output_tokens"`
			} `json:"output"`
		} `json:"requested"`
		Effective struct {
			Effort struct {
				LevelOmitted bool    `json:"level_omitted"`
				Level        *string `json:"level"`
			} `json:"effort"`
		} `json:"effective"`
	}
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if got.ActProtocol != "tool-calls" {
		t.Fatalf("act_protocol = %q, want tool-calls", got.ActProtocol)
	}
	if got.TurnProtocol != nil {
		t.Fatalf("turn_protocol = %s, want omitted", got.TurnProtocol)
	}
	if got.Requested.Effort.LevelOmitted {
		t.Fatalf("requested.effort.level_omitted = true, want false for explicit auto")
	}
	if got.Requested.Effort.Level == nil || *got.Requested.Effort.Level != "auto" {
		t.Fatalf("requested.effort.level = %#v, want auto", got.Requested.Effort.Level)
	}
	if got.Effective.Effort.LevelOmitted != true || got.Effective.Effort.Level != nil {
		t.Fatalf("effective.effort = %#v, want omitted level for auto", got.Effective.Effort)
	}
	if got.Requested.Output.MaxOutputTokens == nil || *got.Requested.Output.MaxOutputTokens != 8000 {
		t.Fatalf("requested.output.max_output_tokens = %#v, want 8000", got.Requested.Output.MaxOutputTokens)
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
