package evaluations

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/clnkr-ai/clnkr"
)

func TestLoadRunConfigFromEnv(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		env     map[string]string
		want    RunConfig
		wantErr string
	}{
		{
			name: "default mock-provider mode",
			env:  map[string]string{},
			want: RunConfig{
				Mode:   ModeMockProvider,
				APIKey: "dummy-key",
				Model:  "test-model",
			},
		},
		{
			name: "live-provider uses only evaluation env",
			env: map[string]string{
				"CLNKR_EVALUATION_MODE":     string(ModeLiveProvider),
				"CLNKR_EVALUATION_API_KEY":  "eval-key",
				"CLNKR_EVALUATION_BASE_URL": "https://eval.example/v1",
				"CLNKR_EVALUATION_MODEL":    "eval-model",
				"OPENAI_API_KEY":            "openai-key",
				"OPENAI_BASE_URL":           "https://openai.example/v1",
				"CLNKR_API_KEY":             "clnkr-key",
				"CLNKR_BASE_URL":            "https://clnkr.example/v1",
				"CLNKR_MODEL":               "ambient-model",
			},
			want: RunConfig{
				Mode:    ModeLiveProvider,
				APIKey:  "eval-key",
				BaseURL: "https://eval.example/v1",
				Model:   "eval-model",
			},
		},
		{
			name: "live-provider ignores openai fallback",
			env: map[string]string{
				"CLNKR_EVALUATION_MODE": string(ModeLiveProvider),
				"OPENAI_API_KEY":        "openai-key",
				"OPENAI_BASE_URL":       "https://openai.example/v1",
			},
			wantErr: "missing API key",
		},
		{
			name: "live-provider ignores ambient clnkr env",
			env: map[string]string{
				"CLNKR_EVALUATION_MODE": string(ModeLiveProvider),
				"CLNKR_API_KEY":         "ambient-key",
				"CLNKR_BASE_URL":        "https://ambient.example/v1",
				"CLNKR_MODEL":           "ambient-model",
			},
			wantErr: "missing API key",
		},
		{
			name: "live-provider defaults model",
			env: map[string]string{
				"CLNKR_EVALUATION_MODE":     string(ModeLiveProvider),
				"CLNKR_EVALUATION_API_KEY":  "eval-key",
				"CLNKR_EVALUATION_BASE_URL": "https://eval.example/v1",
			},
			want: RunConfig{
				Mode:    ModeLiveProvider,
				APIKey:  "eval-key",
				BaseURL: "https://eval.example/v1",
				Model:   "gpt-5.4-nano",
			},
		},
		{
			name: "invalid mode",
			env: map[string]string{
				"CLNKR_EVALUATION_MODE": "bogus",
			},
			wantErr: "unknown CLNKR_EVALUATION_MODE",
		},
		{
			name: "live-provider requires base url",
			env: map[string]string{
				"CLNKR_EVALUATION_MODE":    string(ModeLiveProvider),
				"CLNKR_EVALUATION_API_KEY": "eval-key",
			},
			wantErr: "missing base URL",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := LoadRunConfigFromEnv(func(key string) string {
				return tt.env[key]
			})
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("error = %v, want substring %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("LoadRunConfigFromEnv(): %v", err)
			}
			if got != tt.want {
				t.Fatalf("config = %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestMockProvider(t *testing.T) {
	t.Run("serves mock turns in order and captures requests", func(t *testing.T) {
		provider := NewMockProvider([]string{
			`{"type":"act","command":"pwd"}`,
			`{"type":"done","summary":"finished"}`,
		})
		defer provider.Close()

		firstRequestBody := `{"messages":[{"content":"system prompt","role":"system"},{"content":"first task","role":"user"}],"model":"mock-model"}`
		first := postChatCompletionBody(t, provider.URL(), firstRequestBody)
		if got := first.Choices[0].Message.Content; got != `{"type":"act","command":"pwd"}` {
			t.Fatalf("first response = %q, want mock turn", got)
		}

		secondRequestBody := `{"model":"mock-model","messages":[{"role":"system","content":"system prompt"},{"role":"user","content":"second task"}]}`
		secondResponse, secondBody := postChatCompletionRawBody(t, provider.URL(), secondRequestBody)
		if secondResponse.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, want %d body=%q", secondResponse.StatusCode, http.StatusOK, secondBody)
		}
		var second chatCompletionResponse
		if err := json.Unmarshal([]byte(secondBody), &second); err != nil {
			t.Fatalf("decode response: %v", err)
		}
		if got := second.Choices[0].Message.Content; got != `{"type":"done","summary":"finished"}` {
			t.Fatalf("second response = %q, want mock turn", got)
		}

		requests := provider.Requests()
		if len(requests) != 2 {
			t.Fatalf("request count = %d, want 2", len(requests))
		}
		if requests[0].Model != "mock-model" {
			t.Fatalf("request model = %q, want mock-model", requests[0].Model)
		}
		if len(requests[0].Messages) != 2 {
			t.Fatalf("request messages = %#v, want two messages", requests[0].Messages)
		}
		if requests[0].Messages[1].Content != "first task" {
			t.Fatalf("request message content = %q, want first task", requests[0].Messages[1].Content)
		}
		if requests[0].RawRequest != firstRequestBody {
			t.Fatalf("raw request = %q, want %q", requests[0].RawRequest, firstRequestBody)
		}
		if requests[1].RawRequest != secondRequestBody {
			t.Fatalf("raw request = %q, want %q", requests[1].RawRequest, secondRequestBody)
		}
		if requests[1].RawResponse != secondBody {
			t.Fatalf("raw response = %q, want %q", requests[1].RawResponse, secondBody)
		}
	})

	t.Run("returns stable error when exhausted", func(t *testing.T) {
		provider := NewMockProvider([]string{`{"type":"done","summary":"only"}`})
		defer provider.Close()

		_ = postChatCompletion(t, provider.URL(), map[string]any{
			"model":    "mock-model",
			"messages": []map[string]string{{"role": "user", "content": "one"}},
		})

		resp, body := postChatCompletionRaw(t, provider.URL(), map[string]any{
			"model":    "mock-model",
			"messages": []map[string]string{{"role": "user", "content": "two"}},
		})
		if resp.StatusCode != http.StatusInternalServerError {
			t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusInternalServerError)
		}
		if !strings.Contains(body, "no more mock turns") {
			t.Fatalf("body = %q, want exhaustion error", body)
		}
		requests := provider.Requests()
		if got := requests[len(requests)-1].RawResponse; got != body {
			t.Fatalf("exhausted raw response = %q, want %q", got, body)
		}
	})

	t.Run("Requests returns a caller-safe copy", func(t *testing.T) {
		provider := NewMockProvider([]string{`{"type":"done","summary":"finished"}`})
		defer provider.Close()

		_ = postChatCompletion(t, provider.URL(), map[string]any{
			"model": "mock-model",
			"messages": []map[string]string{
				{"role": "user", "content": "original"},
			},
		})

		requests := provider.Requests()
		requests[0].Model = "mutated"
		requests[0].Messages[0].Content = "mutated"

		again := provider.Requests()
		if len(again) != 1 {
			t.Fatalf("request count after caller mutation = %d, want 1", len(again))
		}
		if again[0].Model != "mock-model" {
			t.Fatalf("stored model = %q, want mock-model", again[0].Model)
		}
		if again[0].Messages[0].Content != "original" {
			t.Fatalf("stored content = %q, want original", again[0].Messages[0].Content)
		}
	})
}

func TestRunTrial(t *testing.T) {
	t.Run("basic-edit", func(t *testing.T) {
		ctx := context.Background()
		repoRoot := repoRoot(t)
		cleanupGeneratedRunOutput(t, repoRoot)
		t.Cleanup(func() {
			cleanupGeneratedRunOutput(t, repoRoot)
		})
		harness, err := NewHarness(ctx, repoRoot)
		if err != nil {
			t.Fatalf("NewHarness(): %v", err)
		}
		t.Cleanup(func() {
			if err := harness.Close(); err != nil {
				t.Fatalf("Close(): %v", err)
			}
		})

		suite, task := loadDefaultBasicEdit(t)
		artifacts, err := harness.RunTrial(ctx, suite, task, RunConfig{Mode: ModeMockProvider})
		if err != nil {
			t.Fatalf("RunTrial(): %v", err)
		}

		if artifacts.Mode != ModeMockProvider {
			t.Fatalf("mode = %q, want %q", artifacts.Mode, ModeMockProvider)
		}
		if artifacts.ExitCode != 0 {
			t.Fatalf("exit code = %d, want 0", artifacts.ExitCode)
		}
		if artifacts.SystemPrompt == "" {
			t.Fatal("system prompt is empty")
		}
		if !strings.Contains(artifacts.SystemPrompt, "Keep changes tight. Work in the current directory.") {
			t.Fatalf("system prompt missing project AGENTS instructions: %q", artifacts.SystemPrompt)
		}
		if artifacts.Trajectory == "" {
			t.Fatal("trajectory is empty")
		}
		if artifacts.EventLog == "" {
			t.Fatal("event log is empty")
		}
		if len(artifacts.ProviderRequests) == 0 {
			t.Fatal("provider requests = 0, want captured mock-provider requests")
		}
		if len(artifacts.ProviderResponses) == 0 {
			t.Fatal("provider responses = 0, want captured mock-provider responses")
		}
		if !artifacts.TrialPassed {
			t.Fatal("trial_passed = false, want true")
		}
		if len(artifacts.FailedRequiredGraders) != 0 {
			t.Fatalf("failed required graders = %#v, want empty", artifacts.FailedRequiredGraders)
		}
		if len(artifacts.GraderResults) != 2 {
			t.Fatalf("grader count = %d, want 2", len(artifacts.GraderResults))
		}
		entries, err := os.ReadDir(filepath.Join(repoRoot, "evaluations", "trials"))
		if err != nil && !os.IsNotExist(err) {
			t.Fatalf("ReadDir(trials): %v", err)
		}
		if err == nil && len(entries) != 0 {
			t.Fatalf("repo trial output entries = %d, want 0 before RunSuite persistence", len(entries))
		}
		assertScriptedParity(t, artifacts)

		wantWorkspace := map[string]string{
			"AGENTS.md": "Keep changes tight. Work in the current directory.\n",
			"note.txt":  "hello\n",
		}
		if !reflect.DeepEqual(artifacts.Workspace, wantWorkspace) {
			t.Fatalf("workspace = %#v, want %#v", artifacts.Workspace, wantWorkspace)
		}
	})

	t.Run("optional transcript grader failure does not fail the trial", func(t *testing.T) {
		ctx := context.Background()
		repoRoot := repoRoot(t)
		harness, err := NewHarness(ctx, repoRoot)
		if err != nil {
			t.Fatalf("NewHarness(): %v", err)
		}
		t.Cleanup(func() {
			if err := harness.Close(); err != nil {
				t.Fatalf("Close(): %v", err)
			}
		})

		suite, task := writeTempSuiteTask(t, repoRoot, "optional-transcript-fail", map[string]string{
			"input/instruction.txt": "Create note.txt in the repo root with the contents hello and then finish.\n",
			"input/model-turns.json": `[
  "{\"type\":\"act\",\"command\":\"printf 'hello\\\\n' > note.txt\"}",
  "{\"type\":\"done\",\"summary\":\"finished\"}"
]`,
			"input/project/AGENTS.md":      "Keep changes tight. Work in the current directory.\n",
			"expected/workspace/AGENTS.md": "Keep changes tight. Work in the current directory.\n",
			"expected/workspace/note.txt":  "hello\n",
			"task.json": `{
  "id": "optional-transcript-fail",
  "instruction_file": "input/instruction.txt",
  "scripted_turns_file": "input/model-turns.json",
  "working_directory": "workspace",
  "full_send": true,
  "step_limit": 10,
  "graders": {
    "outcome_workspace_snapshot": {
      "enabled": true,
      "required": true
    },
    "transcript_command_trace": {
      "enabled": true,
      "required": false,
      "expected_commands": [
        "pwd"
      ],
      "expected_exit_codes": [
        0
      ],
      "max_command_count": 5
    }
  }
}`,
		})

		artifacts, err := harness.RunTrial(ctx, suite, task, RunConfig{Mode: ModeMockProvider})
		if err != nil {
			t.Fatalf("RunTrial(): %v", err)
		}

		if !artifacts.TrialPassed {
			t.Fatal("trial_passed = false, want true")
		}
		graders := artifacts.GraderResults
		if len(graders) != 2 {
			t.Fatalf("grader count = %d, want 2", len(graders))
		}
		foundTranscriptFailure := false
		for _, grader := range graders {
			if grader.GraderID == "transcript_command_trace" {
				foundTranscriptFailure = !grader.Passed
			}
		}
		if !foundTranscriptFailure {
			t.Fatalf("grader records = %#v, want transcript failure", graders)
		}
	})

	t.Run("required transcript grader failure fails the trial", func(t *testing.T) {
		ctx := context.Background()
		repoRoot := repoRoot(t)
		harness, err := NewHarness(ctx, repoRoot)
		if err != nil {
			t.Fatalf("NewHarness(): %v", err)
		}
		t.Cleanup(func() {
			if err := harness.Close(); err != nil {
				t.Fatalf("Close(): %v", err)
			}
		})

		suite, task := writeTempSuiteTask(t, repoRoot, "required-transcript-fail", map[string]string{
			"input/instruction.txt": "Create note.txt in the repo root with the contents hello and then finish.\n",
			"input/model-turns.json": `[
  "{\"type\":\"act\",\"command\":\"pwd\"}",
  "{\"type\":\"act\",\"command\":\"printf 'hello\\\\n' > note.txt\"}",
  "{\"type\":\"done\",\"summary\":\"finished\"}"
]`,
			"input/project/AGENTS.md":      "Keep changes tight. Work in the current directory.\n",
			"expected/workspace/AGENTS.md": "Keep changes tight. Work in the current directory.\n",
			"expected/workspace/note.txt":  "hello\n",
			"task.json": `{
  "id": "required-transcript-fail",
  "instruction_file": "input/instruction.txt",
  "scripted_turns_file": "input/model-turns.json",
  "working_directory": "workspace",
  "full_send": true,
  "step_limit": 10,
  "graders": {
    "outcome_workspace_snapshot": {
      "enabled": true,
      "required": true
    },
    "transcript_command_trace": {
      "enabled": true,
      "required": true,
      "expected_commands": [
        "printf 'hello\\n' > note.txt"
      ],
      "expected_exit_codes": [
        0
      ],
      "max_command_count": 5
    }
  }
}`,
		})

		artifacts, err := harness.RunTrial(ctx, suite, task, RunConfig{Mode: ModeMockProvider})
		if err != nil {
			t.Fatalf("RunTrial(): %v", err)
		}

		if artifacts.TrialPassed {
			t.Fatal("trial_passed = true, want false")
		}
		if len(artifacts.FailedRequiredGraders) != 1 || artifacts.FailedRequiredGraders[0].GraderID != "transcript_command_trace" {
			t.Fatalf("failed required graders = %#v, want transcript failure", artifacts.FailedRequiredGraders)
		}
		graders := artifacts.GraderResults
		foundTranscriptFailure := false
		for _, grader := range graders {
			if grader.GraderID == "transcript_command_trace" {
				foundTranscriptFailure = !grader.Passed && strings.Contains(grader.Message, "command count")
			}
		}
		if !foundTranscriptFailure {
			t.Fatalf("grader records = %#v, want required transcript command-count failure", graders)
		}
	})

	t.Run("required outcome grader failure fails the trial", func(t *testing.T) {
		ctx := context.Background()
		repoRoot := repoRoot(t)
		harness, err := NewHarness(ctx, repoRoot)
		if err != nil {
			t.Fatalf("NewHarness(): %v", err)
		}
		t.Cleanup(func() {
			if err := harness.Close(); err != nil {
				t.Fatalf("Close(): %v", err)
			}
		})

		suite, task := writeTempSuiteTask(t, repoRoot, "required-outcome-fail", map[string]string{
			"input/instruction.txt": "Create note.txt in the repo root with the contents hello and then finish.\n",
			"input/model-turns.json": `[
  "{\"type\":\"act\",\"command\":\"printf 'hello\\\\n' > note.txt\"}",
  "{\"type\":\"done\",\"summary\":\"finished\"}"
]`,
			"input/project/AGENTS.md":      "Keep changes tight. Work in the current directory.\n",
			"expected/workspace/AGENTS.md": "Keep changes tight. Work in the current directory.\n",
			"expected/workspace/note.txt":  "wrong\n",
			"task.json": `{
  "id": "required-outcome-fail",
  "instruction_file": "input/instruction.txt",
  "scripted_turns_file": "input/model-turns.json",
  "working_directory": "workspace",
  "full_send": true,
  "step_limit": 10,
  "graders": {
    "outcome_workspace_snapshot": {
      "enabled": true,
      "required": true
    },
    "transcript_command_trace": {
      "enabled": false,
      "required": false
    }
  }
}`,
		})

		artifacts, err := harness.RunTrial(ctx, suite, task, RunConfig{Mode: ModeMockProvider})
		if err != nil {
			t.Fatalf("RunTrial(): %v", err)
		}

		if artifacts.TrialPassed {
			t.Fatal("trial_passed = true, want false")
		}
		if len(artifacts.FailedRequiredGraders) != 1 || artifacts.FailedRequiredGraders[0].GraderID != "outcome_workspace_snapshot" {
			t.Fatalf("failed required graders = %#v, want outcome failure", artifacts.FailedRequiredGraders)
		}
	})

	t.Run("live-provider uses configured endpoint and model", func(t *testing.T) {
		ctx := context.Background()
		repoRoot := repoRoot(t)
		harness, err := NewHarness(ctx, repoRoot)
		if err != nil {
			t.Fatalf("NewHarness(): %v", err)
		}
		t.Cleanup(func() {
			if err := harness.Close(); err != nil {
				t.Fatalf("Close(): %v", err)
			}
		})

		var requests []CapturedRequest
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/chat/completions" {
				http.NotFound(w, r)
				return
			}
			var req chatCompletionRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				http.Error(w, "bad request", http.StatusBadRequest)
				return
			}
			requests = append(requests, CapturedRequest{
				Model:    req.Model,
				Messages: append([]clnkr.Message(nil), req.Messages...),
			})
			var content string
			switch len(requests) {
			case 1:
				content = `{"type":"act","command":"printf 'hello\\n' > note.txt"}`
			default:
				content = `{"type":"done","summary":"created note.txt"}`
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"choices": []map[string]any{
					{
						"message": map[string]string{
							"role":    "assistant",
							"content": content,
						},
					},
				},
				"usage": map[string]int{
					"prompt_tokens":     1,
					"completion_tokens": 1,
				},
			})
		}))
		defer server.Close()

		suite, task := loadDefaultBasicEdit(t)
		artifacts, err := harness.RunTrial(ctx, suite, task, RunConfig{
			Mode:    ModeLiveProvider,
			APIKey:  "live-key",
			BaseURL: server.URL,
			Model:   "live-model",
		})
		if err != nil {
			t.Fatalf("RunTrial(): %v", err)
		}

		if artifacts.Mode != ModeLiveProvider {
			t.Fatalf("mode = %q, want %q", artifacts.Mode, ModeLiveProvider)
		}
		if artifacts.ProviderModel != "live-model" {
			t.Fatalf("provider model = %q, want live-model", artifacts.ProviderModel)
		}
		if artifacts.ProviderBaseURL != server.URL {
			t.Fatalf("provider base URL = %q, want %q", artifacts.ProviderBaseURL, server.URL)
		}
		if len(requests) != 2 {
			t.Fatalf("request count = %d, want 2", len(requests))
		}
		for i, req := range requests {
			if req.Model != "live-model" {
				t.Fatalf("request %d model = %q, want live-model", i, req.Model)
			}
		}
	})

	t.Run("preserves prompt layering when home and config trees are present", func(t *testing.T) {
		ctx := context.Background()
		repoRoot := repoRoot(t)
		harness, err := NewHarness(ctx, repoRoot)
		if err != nil {
			t.Fatalf("NewHarness(): %v", err)
		}
		t.Cleanup(func() {
			if err := harness.Close(); err != nil {
				t.Fatalf("Close(): %v", err)
			}
		})

		suite, task := writeTempSuiteTask(t, repoRoot, "layered-prompt", map[string]string{
			"input/instruction.txt":        "Create note.txt in the repo root with the contents hello and then finish.\n",
			"input/model-turns.json":       "[\"{\\\"type\\\":\\\"done\\\",\\\"summary\\\":\\\"finished\\\"}\"]\n",
			"input/project/AGENTS.md":      "project instructions\n",
			"input/home/AGENTS.md":         "home instructions\n",
			"input/config/clnkr/AGENTS.md": "config instructions\n",
			"expected/workspace/.gitkeep":  "",
			"input/workspace/.gitkeep":     "",
			"task.json": `{
  "id": "layered-prompt",
  "instruction_file": "input/instruction.txt",
  "scripted_turns_file": "input/model-turns.json",
  "working_directory": "workspace",
  "full_send": true,
  "step_limit": 5,
  "graders": {
    "outcome_workspace_snapshot": {
      "enabled": true,
      "required": true
    },
    "transcript_command_trace": {
      "enabled": false,
      "required": false
    }
  }
}`,
		})

		artifacts, err := harness.RunTrial(ctx, suite, task, RunConfig{Mode: ModeMockProvider})
		if err != nil {
			t.Fatalf("RunTrial(): %v", err)
		}

		for _, want := range []string{"home instructions", "config instructions", "project instructions"} {
			if !strings.Contains(artifacts.SystemPrompt, want) {
				t.Fatalf("system prompt missing %q: %q", want, artifacts.SystemPrompt)
			}
		}
		if artifacts.Workspace["AGENTS.md"] != "project instructions\n" {
			t.Fatalf("workspace AGENTS = %q, want project instructions", artifacts.Workspace["AGENTS.md"])
		}
	})

	t.Run("reuses built clnku binary across trials", func(t *testing.T) {
		ctx := context.Background()
		repoRoot := repoRoot(t)
		harness, err := NewHarness(ctx, repoRoot)
		if err != nil {
			t.Fatalf("NewHarness(): %v", err)
		}
		t.Cleanup(func() {
			if err := harness.Close(); err != nil {
				t.Fatalf("Close(): %v", err)
			}
		})

		suite, task := loadDefaultBasicEdit(t)
		firstPath := harness.binaryPath
		if firstPath == "" {
			t.Fatal("binary path is empty after NewHarness")
		}

		if _, err := harness.RunTrial(ctx, suite, task, RunConfig{Mode: ModeMockProvider}); err != nil {
			t.Fatalf("first RunTrial(): %v", err)
		}
		if _, err := harness.RunTrial(ctx, suite, task, RunConfig{Mode: ModeMockProvider}); err != nil {
			t.Fatalf("second RunTrial(): %v", err)
		}
		if harness.binaryPath != firstPath {
			t.Fatalf("binary path = %q, want reused path %q", harness.binaryPath, firstPath)
		}
	})

	t.Run("cleanup removes trial dirs and harness temp root", func(t *testing.T) {
		ctx := context.Background()
		repoRoot := repoRoot(t)
		harness, err := NewHarness(ctx, repoRoot)
		if err != nil {
			t.Fatalf("NewHarness(): %v", err)
		}

		suite, task := loadDefaultBasicEdit(t)
		if _, err := harness.RunTrial(ctx, suite, task, RunConfig{Mode: ModeMockProvider}); err != nil {
			t.Fatalf("RunTrial(): %v", err)
		}

		entries, err := os.ReadDir(harness.trialsDir)
		if err != nil {
			t.Fatalf("ReadDir(%q): %v", harness.trialsDir, err)
		}
		if len(entries) != 0 {
			t.Fatalf("trial dirs still present: %v", entries)
		}

		tempRoot := harness.tempRoot
		if err := harness.Close(); err != nil {
			t.Fatalf("Close(): %v", err)
		}
		if _, err := os.Stat(tempRoot); !os.IsNotExist(err) {
			t.Fatalf("temp root stat error = %v, want not exist", err)
		}
	})
}

func loadDefaultBasicEdit(t *testing.T) (Suite, Task) {
	t.Helper()

	suite, err := LoadSuite(filepath.Join("suites", "default", "suite.json"))
	if err != nil {
		t.Fatalf("LoadSuite(): %v", err)
	}
	tasks, err := LoadSuiteTasks(filepath.Join("suites", "default"), suite)
	if err != nil {
		t.Fatalf("LoadSuiteTasks(): %v", err)
	}
	if len(tasks) != 1 {
		t.Fatalf("task count = %d, want 1", len(tasks))
	}
	return suite, tasks[0]
}

func writeTempSuiteTask(t *testing.T, repoRoot, taskID string, files map[string]string) (Suite, Task) {
	t.Helper()

	suitesRoot := filepath.Join(repoRoot, "evaluations", "suites")
	suiteDir, err := os.MkdirTemp(suitesRoot, "task2-*")
	if err != nil {
		t.Fatalf("MkdirTemp(): %v", err)
	}
	t.Cleanup(func() {
		_ = os.RemoveAll(suiteDir)
	})

	taskDir := filepath.Join(suiteDir, "tasks", taskID)
	for rel, content := range files {
		target := filepath.Join(taskDir, rel)
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			t.Fatalf("MkdirAll(%q): %v", target, err)
		}
		if err := os.WriteFile(target, []byte(content), 0o644); err != nil {
			t.Fatalf("WriteFile(%q): %v", target, err)
		}
	}

	suiteID := filepath.Base(suiteDir)
	suiteJSON := `{
  "id": "` + suiteID + `",
  "description": "temp suite",
  "mode": "mock-provider",
  "trials_per_task": 1,
  "failure_policy": {
    "stop_on_first_failure": true,
    "max_failed_tasks": 1
  },
  "tasks": ["` + taskID + `"]
}`
	if err := os.WriteFile(filepath.Join(suiteDir, "suite.json"), []byte(suiteJSON), 0o644); err != nil {
		t.Fatalf("WriteFile(suite.json): %v", err)
	}

	suite, err := LoadSuite(filepath.Join(suiteDir, "suite.json"))
	if err != nil {
		t.Fatalf("LoadSuite(): %v", err)
	}
	tasks, err := LoadSuiteTasks(suiteDir, suite)
	if err != nil {
		t.Fatalf("LoadSuiteTasks(): %v", err)
	}
	if len(tasks) != 1 {
		t.Fatalf("task count = %d, want 1", len(tasks))
	}
	return suite, tasks[0]
}

type chatCompletionResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
}

func postChatCompletion(t *testing.T, baseURL string, payload map[string]any) chatCompletionResponse {
	t.Helper()

	resp, body := postChatCompletionRaw(t, baseURL, payload)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%q", resp.StatusCode, http.StatusOK, body)
	}

	var decoded chatCompletionResponse
	if err := json.Unmarshal([]byte(body), &decoded); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return decoded
}

func postChatCompletionRaw(t *testing.T, baseURL string, payload map[string]any) (*http.Response, string) {
	t.Helper()

	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	resp, err := http.Post(baseURL+"/chat/completions", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("post chat completions: %v", err)
	}
	t.Cleanup(func() {
		_ = resp.Body.Close()
	})
	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read response body: %v", err)
	}
	return resp, string(responseBody)
}

func postChatCompletionBody(t *testing.T, baseURL, body string) chatCompletionResponse {
	t.Helper()

	resp, responseBody := postChatCompletionRawBody(t, baseURL, body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%q", resp.StatusCode, http.StatusOK, responseBody)
	}

	var decoded chatCompletionResponse
	if err := json.Unmarshal([]byte(responseBody), &decoded); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return decoded
}

func postChatCompletionRawBody(t *testing.T, baseURL, body string) (*http.Response, string) {
	t.Helper()

	resp, err := http.Post(baseURL+"/chat/completions", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("post chat completions: %v", err)
	}
	t.Cleanup(func() {
		_ = resp.Body.Close()
	})
	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read response body: %v", err)
	}
	return resp, string(responseBody)
}

func repoRoot(t *testing.T) string {
	t.Helper()

	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd(): %v", err)
	}
	return filepath.Dir(cwd)
}

func assertScriptedParity(t *testing.T, artifacts RunArtifacts) {
	t.Helper()

	var trajectory []clnkr.Message
	if err := json.Unmarshal([]byte(artifacts.Trajectory), &trajectory); err != nil {
		t.Fatalf("parse trajectory: %v", err)
	}

	assistantIndices := make([]int, 0, len(artifacts.ProviderRequests))
	for i, message := range trajectory {
		if message.Role == "assistant" {
			assistantIndices = append(assistantIndices, i)
		}
	}
	if len(assistantIndices) != len(artifacts.ProviderRequests) {
		t.Fatalf("assistant count = %d, want %d", len(assistantIndices), len(artifacts.ProviderRequests))
	}

	for i, request := range artifacts.ProviderRequests {
		if request.Model != artifacts.ProviderModel {
			t.Fatalf("request %d model = %q, want %q", i, request.Model, artifacts.ProviderModel)
		}
		if len(request.Messages) == 0 {
			t.Fatalf("request %d has no messages", i)
		}
		if request.Messages[0].Role != "system" {
			t.Fatalf("request %d first role = %q, want system", i, request.Messages[0].Role)
		}
		if request.Messages[0].Content != artifacts.SystemPrompt {
			t.Fatalf("request %d system prompt mismatch", i)
		}

		wantMessages := trajectory[:assistantIndices[i]]
		if !reflect.DeepEqual(request.Messages[1:], wantMessages) {
			t.Fatalf("request %d transcript mismatch\nactual: %#v\nexpected: %#v", i, request.Messages[1:], wantMessages)
		}
		if request.RawResponse != artifacts.ProviderResponses[i] {
			t.Fatalf("request %d raw response mismatch", i)
		}
	}
}
