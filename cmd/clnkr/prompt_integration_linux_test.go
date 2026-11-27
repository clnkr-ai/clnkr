package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
	"unsafe"
)

// Run one conversation in its own controlling terminal. Check the model request
// independently of the terminal display, where drafts can appear before submission.
func testTerminalSubmission(
	t *testing.T,
	cwd, want string,
	typeDraft func(*guardedBuffer, func(string)),
) {
	t.Helper()
	input := make(chan string, 4)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Messages []struct {
				Role    string
				Content json.RawMessage
			}
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
		}
		var last string
		for _, m := range request.Messages {
			if m.Role == "user" {
				var content string
				if err := json.Unmarshal(m.Content, &content); err != nil {
					t.Error(err)
				}
				if !strings.Contains(content, `"source":"clnkr"`) {
					last = content
				}
			}
		}
		input <- last
		_ = json.NewEncoder(w).
			Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]string{"role": "assistant", "content": openAIWrappedDone("finished")}}}})
	}))
	defer server.Close()
	output := captureProgramStatus(t, func(out *guardedBuffer, master *os.File) {
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()
		cmd := exec.CommandContext(
			ctx,
			os.Args[0],
			"-test.run=TestMainHelper",
			"--",
			"--full-send",
			"--provider",
			"openai",
			"--provider-api",
			"openai-chat-completions",
			"--base-url",
			server.URL,
			"--model",
			"gpt-test",
		)
		cmd.Dir = cwd
		cmd.Env = append(
			withoutCLNKREnv(os.Environ()),
			"CLNKR_HELPER_MAIN=1",
			"CLNKR_API_KEY=test-key",
			"TERM=xterm",
		)
		var stdout bytes.Buffer
		cmd.Stdin, cmd.Stderr, cmd.Stdout = os.Stderr, os.Stderr, &stdout
		cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0}
		attributes := func() syscall.Termios {
			var term syscall.Termios
			if errno := ioctl(os.Stderr.Fd(), syscall.TCGETS, unsafe.Pointer(&term)); errno != 0 {
				t.Fatal(errno)
			}
			return term
		}
		original := attributes()
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		defer func() {
			if cmd.ProcessState == nil {
				_ = cmd.Process.Kill()
				_ = cmd.Wait()
			}
		}()
		send := func(keys string) {
			t.Helper()
			if _, err := io.WriteString(master, keys); err != nil {
				t.Fatal(err)
			}
		}
		waitForOutput(t, out, "clnkr> ")
		typeDraft(out, send)
		select {
		case early := <-input:
			t.Fatalf("submitted before Enter: %q", early)
		default:
		}
		send("\r")
		select {
		case got := <-input:
			if got != want {
				t.Fatalf("submitted %q, want %q", got, want)
			}
		case <-ctx.Done():
			t.Fatalf("no submission: %q", out.String())
		}
		waitForOutput(t, out, "state=done")
		send("\x04")
		requireProgramStatusExit(t, cmd, 0)
		if got := attributes(); got != original {
			t.Fatalf("terminal not restored: %+v, want %+v", got, original)
		}
		if strings.TrimSpace(stdout.String()) != "finished" {
			t.Fatalf("stdout: %q", stdout.String())
		}
	})
	for _, forbidden := range []string{"\x1b[?1049h", "\x1b[2J", "\x1b[3J"} {
		if strings.Contains(output, forbidden) {
			t.Fatalf("screen clear %q in terminal output", forbidden)
		}
	}
}

func TestPromptHistoryAcrossTerminals(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	cwd := t.TempDir()
	run := func(dir, want, keys string) {
		t.Helper()
		testTerminalSubmission(
			t,
			dir,
			want,
			func(_ *guardedBuffer, send func(string)) { send(keys) },
		)
	}
	run(cwd, "previous", "previous")
	run(cwd, "previous!", "\x1b[A!")
	run(cwd, "unsent", "unsent\x10\x0e")
	run(t.TempDir(), "isolated", "isolated\x1b[A")
	run(cwd, "pasted\nlines", "\x1b[200~pasted\nlines\x1b[201~")
	run(cwd, "pasted\nlines", "\x10")
	testTerminalSubmission(t, cwd, "plain\npaste", func(out *guardedBuffer, send func(string)) {
		send("plain\npaste\n")
		waitForOutput(t, out, "(3 lines)")
	})
}

func TestPromptFragmentedPasteAndWordErase(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	cwd := t.TempDir()
	testTerminalSubmission(t, cwd, "one 三", func(out *guardedBuffer, send func(string)) {
		send("one two\x17三")
		waitForOutput(t, out, "one 三")
	})
	testTerminalSubmission(t, cwd, "kept\nwhole", func(out *guardedBuffer, send func(string)) {
		send("\x1b[")
		time.Sleep(250 * time.Millisecond)
		send("200~kept\n")
		time.Sleep(250 * time.Millisecond)
		send("whole\x1b[201~")
		waitForOutput(t, out, "(2 lines)")
	})
}

func TestInterruptedPasteRestoresTerminal(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	captureProgramStatus(t, func(out *guardedBuffer, master *os.File) {
		cmd, cleanup := programStatusClarificationCommand(t)
		defer cleanup()
		cmd.Env = append(cmd.Env, "TERM=xterm")
		cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0}
		var original syscall.Termios
		if errno := ioctl(os.Stderr.Fd(), syscall.TCGETS, unsafe.Pointer(&original)); errno != 0 {
			t.Fatal(errno)
		}
		original.Cc[syscall.VINTR] = 24 // Ctrl-X
		if errno := ioctl(os.Stderr.Fd(), syscall.TCSETS, unsafe.Pointer(&original)); errno != 0 {
			t.Fatal(errno)
		}
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		waitForOutput(t, out, "clnkr> ")
		if _, err := io.WriteString(master, "\x1b[200~incomplete"); err != nil {
			t.Fatal(err)
		}
		time.Sleep(400 * time.Millisecond)
		if _, err := io.WriteString(master, "\x18"); err != nil {
			t.Fatal(err)
		}
		requireProgramStatusExit(t, cmd, 130)
		var got syscall.Termios
		if errno := ioctl(
			os.Stderr.Fd(),
			syscall.TCGETS,
			unsafe.Pointer(&got),
		); errno != 0 ||
			got != original {
			t.Fatalf("terminal restoration failed: %v", errno)
		}
	})
}

func TestPromptResizeStartsFreshRow(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	testTerminalSubmission(t, t.TempDir(), "short", func(out *guardedBuffer, send func(string)) {
		send(strings.Repeat("a", 55))
		waitForOutput(t, out, strings.Repeat("a", 55))
		size := [4]uint16{24, 25}
		if errno := ioctl(os.Stderr.Fd(), syscall.TIOCSWINSZ, unsafe.Pointer(&size)); errno != 0 {
			t.Fatal(errno)
		}
		send("\x15short")
		waitForOutput(t, out, "\nclnkr> ")
	})
}

func TestPromptExternalEditorInTerminal(t *testing.T) {
	for _, mode := range []string{"save", "fail", "interrupt"} {
		t.Run(mode, func(t *testing.T) {
			dir := t.TempDir()
			t.Setenv("XDG_STATE_HOME", dir)
			editor := filepath.Join(dir, "editor")
			body := `#!/bin/sh
printf 'EDITOR-READY\n' >&2
IFS= read -r reply || exit 1
if [ "$reply" != editor-input ]; then exit 2; fi
printf '%s\n' 'edited second line' >> "$1"
`
			if mode == "fail" {
				body += "stty -echo -icanon\nexit 3\n"
			}
			if mode == "interrupt" {
				body += "printf 'INTERRUPT-ME\\n' >&2\nIFS= read -r reply\n"
			}
			if err := os.WriteFile(editor, []byte(body), 0700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("EDITOR", editor)
			want := "original"
			if mode == "save" {
				want += "\nedited second line"
			}
			testTerminalSubmission(t, dir, want, func(out *guardedBuffer, send func(string)) {
				send("original\x07editor-input\n")
				waitForOutput(t, out, "EDITOR-READY")
				if mode == "interrupt" {
					waitForOutput(t, out, "INTERRUPT-ME")
					send("\x03")
				}
				deadline := time.Now().Add(2 * time.Second)
				for time.Now().Before(deadline) {
					_, tail, _ := strings.Cut(out.String(), "EDITOR-READY")
					if strings.Contains(tail, "original") {
						return
					}
					time.Sleep(time.Millisecond)
				}
				t.Fatalf("draft did not return after editor: %q", out.String())
			})
		})
	}
}
