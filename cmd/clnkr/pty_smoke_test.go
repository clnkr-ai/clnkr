package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
)

func TestPTYSingleTaskShowsApprovalPrompt(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{
				{"message": map[string]string{
					"role":    "assistant",
					"content": `{"turn":{"type":"act","bash":{"command":"printf 'hello from test\\n'","workdir":null},"question":null,"summary":null,"reasoning":"emit test output"}}`,
				}},
			},
			"usage": map[string]int{"prompt_tokens": 1, "completion_tokens": 1},
		})
	}))
	defer server.Close()

	bin := buildCLNKRBinary(t)
	app := startPTYSession(t, ptyStartOptions{
		binary: bin,
		args: []string{
			"-p", "say hello",
			"--model", "test-model",
			"--base-url", server.URL,
			"-S",
		},
		env: map[string]string{
			"CLNKR_API_KEY": "test-key",
		},
		size: terminalSize{cols: 80, rows: 24},
	})
	defer app.Close()

	app.WaitFor("● proposed: printf 'hello from test\\n'")
	app.WaitFor("Send 'y' to approve")
}

func TestPTYRequestsTerminalFocusReports(t *testing.T) {
	bin := buildCLNKRBinary(t)
	app := startPTYSession(t, ptyStartOptions{
		binary: bin,
		args: []string{
			"--model", "test-model",
			"-S",
		},
		env: map[string]string{
			"CLNKR_API_KEY": "test-key",
		},
		size: terminalSize{cols: 80, rows: 24},
	})
	defer app.Close()

	app.WaitFor("Type a task...")
	app.WaitForRaw("\x1b[?1004h")
}

var (
	buildOnce   sync.Once
	buildBinary string
	buildErr    error
)

func buildCLNKRBinary(t *testing.T) string {
	t.Helper()

	buildOnce.Do(func() {
		tmpDir, err := os.MkdirTemp("", "clnkr-pty-binary-*")
		if err != nil {
			buildErr = err
			return
		}
		goTool, err := exec.LookPath("go")
		if err != nil {
			buildErr = err
			return
		}
		buildBinary = filepath.Join(tmpDir, "clnkr-test")
		cmd := exec.Command(goTool, "build", "-o", buildBinary, ".")
		cmd.Dir = "."
		cmd.Env = os.Environ()
		out, err := cmd.CombinedOutput()
		if err != nil {
			buildErr = &buildBinaryError{err: err, output: string(out)}
		}
	})
	if buildErr != nil {
		t.Fatal(buildErr)
	}
	return buildBinary
}

type buildBinaryError struct {
	err    error
	output string
}

func (e *buildBinaryError) Error() string {
	return e.err.Error() + "\n" + e.output
}
