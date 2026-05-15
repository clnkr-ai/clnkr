package clnkrapp

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/clnkr-ai/clnkr"
	"github.com/clnkr-ai/clnkr/cmd/internal/providerconfig"
	providerdomain "github.com/clnkr-ai/clnkr/internal/providers/providerconfig"
)

func TestWriteTrajectoryWritesIndentedMessages(t *testing.T) {
	path := filepath.Join(t.TempDir(), "trajectory.json")

	if err := WriteTrajectory(path, []clnkr.Message{{Role: "user", Content: "hello"}}); err != nil {
		t.Fatalf("WriteTrajectory: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	want := `[
  {
    "role": "user",
    "content": "hello"
  }
]`
	if string(data) != want {
		t.Fatalf("trajectory = %q, want %q", data, want)
	}
}

func TestRunMetadataDebugEventFormatsJSON(t *testing.T) {
	meta := newRunMetadata("test-version", openAIResponsesConfig("high"), "system prompt")

	event := RunMetadataDebugEvent(meta)
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
	cfg := openAIResponsesConfig("auto")
	cfg.ActProtocol = clnkr.ActProtocolToolCalls
	data, err := json.Marshal(newRunMetadata("test-version", cfg, "system prompt"))
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
				MaxOutputTokens *int `json:"max_output_tokens"`
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
	if got.Requested.Effort.LevelOmitted || got.Requested.Effort.Level == nil || *got.Requested.Effort.Level != "auto" {
		t.Fatalf("requested.effort = %#v, want explicit auto", got.Requested.Effort)
	}
	if !got.Effective.Effort.LevelOmitted || got.Effective.Effort.Level != nil {
		t.Fatalf("effective.effort = %#v, want omitted level for auto", got.Effective.Effort)
	}
	if got.Requested.Output.MaxOutputTokens == nil || *got.Requested.Output.MaxOutputTokens != 8000 {
		t.Fatalf("requested.output.max_output_tokens = %#v, want 8000", got.Requested.Output.MaxOutputTokens)
	}
}

func TestLoadMessages(t *testing.T) {
	for _, tc := range []struct {
		name    string
		input   string
		wantErr string
	}{
		{name: "message array", input: `[{"role":"user","content":"hello"}]`},
		{name: "rejects envelope", input: `{"metadata":{"version":"dev"},"messages":[{"role":"user","content":"hello"}]}`, wantErr: "parse messages: json: cannot unmarshal object into Go value of type []transcript.Message"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := LoadMessages([]byte(tc.input))
			if tc.wantErr != "" {
				if err == nil || err.Error() != tc.wantErr {
					t.Fatalf("LoadMessages error = %v, want %s", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("LoadMessages: %v", err)
			}
			if len(got) != 1 || got[0].Role != "user" || got[0].Content != "hello" {
				t.Fatalf("messages = %#v, want user hello", got)
			}
		})
	}
}

func openAIResponsesConfig(effort string) providerconfig.ResolvedProviderConfig {
	return providerconfig.ResolvedProviderConfig{
		Provider:    providerdomain.ProviderOpenAI,
		ProviderAPI: providerdomain.ProviderAPIOpenAIResponses,
		Model:       "gpt-5.1",
		RequestOptions: providerdomain.ProviderRequestOptions{
			Effort: providerdomain.ProviderEffortOptions{Level: effort, Set: true},
			Output: providerdomain.ProviderOutputOptions{
				MaxOutputTokens: providerdomain.OptionalInt{Value: 8000, Set: true},
			},
		},
	}
}
