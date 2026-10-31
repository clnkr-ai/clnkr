//go:build linux

package main

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"
	"unicode"
	"unicode/utf8"
	"unsafe"

	"github.com/clnkr-ai/clnkr"
	"github.com/clnkr-ai/clnkr/cmd/internal/clnkrapp"
)

func captureProgramStatus(t *testing.T, run func(*guardedBuffer, *os.File)) string {
	t.Helper()
	master, err := os.OpenFile("/dev/ptmx", os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		t.Fatalf("open PTY: %v", err)
	}
	defer master.Close() //nolint:errcheck
	var unlock, number uint32
	for _, request := range []struct {
		code uintptr
		arg  *uint32
	}{{syscall.TIOCSPTLCK, &unlock}, {syscall.TIOCGPTN, &number}} {
		if _, _, errno := syscall.Syscall(
			syscall.SYS_IOCTL, master.Fd(), request.code, uintptr(unsafe.Pointer(request.arg)),
		); errno != 0 {
			t.Fatalf("PTY ioctl: %v", errno)
		}
	}
	slave, err := os.OpenFile(fmt.Sprintf("/dev/pts/%d", number), os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		t.Fatalf("open PTY slave: %v", err)
	}
	defer slave.Close() //nolint:errcheck
	previous := os.Stderr
	os.Stderr = slave
	defer func() { os.Stderr = previous }()
	var out guardedBuffer
	readErr := make(chan error, 1)
	go func() {
		_, err := io.Copy(&out, master)
		readErr <- err
	}()
	run(&out, master)
	if err := slave.Close(); err != nil {
		t.Fatalf("close PTY slave: %v", err)
	}
	if err := <-readErr; err != nil && !errors.Is(err, syscall.EIO) {
		t.Fatalf("read PTY: %v", err)
	}
	return out.String()
}

func programStatuses(t *testing.T, output string) []map[string]string {
	t.Helper()
	var reports []map[string]string
	for _, part := range strings.Split(output, "\x1b]7501;")[1:] {
		body, _, ok := strings.Cut(part, "\x1b\\")
		if !ok || strings.ContainsAny(body, "\x1b\r\n") {
			t.Fatalf("incomplete or interleaved OSC: %q", part)
		}
		report := make(map[string]string)
		for _, pair := range strings.Split(body, ":") {
			key, value, ok := strings.Cut(pair, "=")
			if !ok {
				t.Fatalf("invalid pair: %q", pair)
			}
			report[key] = value
		}
		message, err := base64.StdEncoding.DecodeString(report["msg"])
		if err != nil || !utf8.Valid(message) || len(message) > 2048 ||
			strings.IndexFunc(string(message), unicode.IsControl) >= 0 {
			t.Fatalf("invalid message %q: %v", message, err)
		}
		report["msg"] = string(message)
		if report["app"] != "clnkr" || report["id"] != "" || len(body)+9 > 4096 {
			t.Fatalf("invalid root report: %#v", report)
		}
		reports = append(reports, report)
	}
	return reports
}

func requireProgramStates(t *testing.T, output string, want ...string) {
	t.Helper()
	var got []string
	for _, report := range programStatuses(t, output) {
		state := report["state"]
		if kind := report["kind"]; kind != "" {
			state += "/" + kind
		}
		got = append(got, state)
	}
	if !slices.Equal(got, want) {
		t.Fatalf("states = %q, want %q\noutput: %q", got, want, output)
	}
}

func TestProgramStatusWireBytes(t *testing.T) {
	output := captureProgramStatus(t, func(*guardedBuffer, *os.File) {
		writeProgramStatus("done", "Run finished")
		writeProgramStatus("blocked:kind=question", "A\nB\x1bC\tD\x7fE\u0085F")
	})
	want := "\x1b]7501;state=done:app=clnkr:msg=UnVuIGZpbmlzaGVk\x1b\\" +
		"\x1b]7501;state=blocked:kind=question:app=clnkr:msg=QSBCIEMgRCBFIEY=\x1b\\"
	if output != want {
		t.Fatalf("wire = %q, want %q", output, want)
	}
}

func TestProgramStatusBoundsUTF8(t *testing.T) {
	output := captureProgramStatus(t, func(*guardedBuffer, *os.File) {
		writeProgramStatus("working", strings.Repeat("😀", 513))
		writeProgramStatus("working", "bad\xfftext")
	})
	reports := programStatuses(t, output)
	if len(reports) != 2 || reports[0]["msg"] != strings.Repeat("😀", 512) ||
		reports[1]["msg"] != "bad�text" {
		t.Fatalf("messages = %#v, want bounded valid UTF-8", reports)
	}
}

func TestProgramStatusDriverTransitions(t *testing.T) {
	act := mustResponse(
		`{"type":"act","bash":{"commands":[{"command":"printf ok","workdir":null}]}}`,
	)
	question := mustResponse(`{"type":"clarify","question":"Which repo?"}`)
	done := mustResponse(`{"type":"done","summary":"done"}`)
	protocolFailure := clnkr.Response{Raw: "invalid", ProtocolErr: errors.New("invalid turn")}
	tests := []struct {
		name          string
		responses     []clnkr.Response
		replies       string
		states        []string
		executorCalls int
		wantError     string
	}{
		{"success", []clnkr.Response{done}, "", []string{"working", "done"}, 0, ""},
		{"approval", []clnkr.Response{act, done}, "y\n",
			[]string{"working", "blocked/permission", "working", "done"}, 1, ""},
		{"approval guidance", []clnkr.Response{act, done}, "inspect instead\n",
			[]string{"working", "blocked/permission", "working", "done"}, 0, ""},
		{"clarification", []clnkr.Response{question, done}, "this repo\n",
			[]string{"working", "blocked/question", "working", "done"}, 0, ""},
		{
			"approval rejects compact",
			[]clnkr.Response{act, done},
			"/compact\ny\n",
			[]string{
				"working",
				"blocked/permission",
				"blocked/permission",
				"working",
				"done",
			},
			1,
			"",
		},
		{"clarification rejects compact", []clnkr.Response{question, done}, "/compact\nthis repo\n",
			[]string{"working", "blocked/question", "blocked/question", "working", "done"}, 0, ""},
		{"protocol retry", []clnkr.Response{protocolFailure, done}, "",
			[]string{"working", "done"}, 0, ""},
		{
			"fatal protocol failure",
			[]clnkr.Response{protocolFailure, protocolFailure, protocolFailure},
			"",
			[]string{"working", "error"},
			0,
			"consecutive protocol failures",
		},
		{"fatal model failure", nil, "", []string{"working", "error"}, 0, "no more responses"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			model := &fakeModel{responses: tt.responses}
			// A failed command remains recoverable by the next model turn.
			code := 1
			executor := &fakeExecutor{results: []clnkr.CommandResult{{
				Outcome: clnkr.CommandOutcome{Type: clnkr.CommandOutcomeExit, ExitCode: &code},
			}}}
			agent := clnkr.NewAgent(model, executor, "/tmp")
			driver := clnkrapp.NewDriver(agent, nil)
			var runErr error
			output := captureProgramStatus(t, func(*guardedBuffer, *os.File) {
				runErr = runDriverPrompt(context.Background(), driver,
					newLineReader(strings.NewReader(tt.replies)), "inspect",
					clnkrapp.PromptModeApproval, nil, func() {})
			})
			if (runErr == nil) != (tt.wantError == "") ||
				(runErr != nil && !strings.Contains(runErr.Error(), tt.wantError)) {
				t.Fatalf("run error = %v, want %q", runErr, tt.wantError)
			}
			if executor.calls != tt.executorCalls || model.calls != len(tt.responses) {
				t.Fatalf("calls = model %d, executor %d", model.calls, executor.calls)
			}
			requireProgramStates(t, output, tt.states...)
		})
	}
}

func TestProgramStatusReplyEOF(t *testing.T) {
	for _, tt := range []struct {
		name, response, blocked string
	}{
		{"approval", `{"type":"act","bash":{"commands":[{"command":"printf ok","workdir":null}]}}`, "blocked/permission"},
		{"clarification", `{"type":"clarify","question":"Which repo?"}`, "blocked/question"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			model := &fakeModel{responses: []clnkr.Response{mustResponse(tt.response)}}
			driver := clnkrapp.NewDriver(clnkr.NewAgent(model, &fakeExecutor{}, "/tmp"), nil)
			output := captureProgramStatus(t, func(*guardedBuffer, *os.File) {
				err := runPromptLoop(driver, newLineReader(strings.NewReader("inspect\n")), nil,
					true, clnkrapp.PromptModeApproval, nil)
				if err != nil {
					t.Fatalf("prompt loop error = %v, want graceful EOF", err)
				}
			})
			requireProgramStates(t, output, "working", tt.blocked, "idle")
		})
	}
}

type statusEOFModel struct{}

func (statusEOFModel) Query(context.Context, []clnkr.Message) (clnkr.Response, error) {
	return clnkr.Response{}, fmt.Errorf("provider connection: %w", io.EOF)
}

func TestProgramStatusProviderEOF(t *testing.T) {
	driver := clnkrapp.NewDriver(clnkr.NewAgent(statusEOFModel{}, &fakeExecutor{}, "/tmp"), nil)
	output := captureProgramStatus(t, func(*guardedBuffer, *os.File) {
		err := runDriverPrompt(context.Background(), driver, newLineReader(strings.NewReader("")),
			"inspect", clnkrapp.PromptModeFullSend, nil, func() {})
		if !errors.Is(err, io.EOF) {
			t.Fatalf("run error = %v, want provider EOF", err)
		}
	})
	requireProgramStates(t, output, "working", "error")
}

func programStatusClarificationCommand(t *testing.T, extraArgs ...string) (*exec.Cmd, func()) {
	t.Helper()
	server, _ := newOpenAIChatServer(t, func(call int) string {
		if call == 1 {
			return `{"turn":{"type":"clarify","bash":null,"question":"Which repo?","summary":null,"reasoning":null}}`
		}
		return openAIWrappedDone("done")
	})
	args := []string{"-test.run=TestMainHelper", "--", "--full-send",
		"--provider", "openai", "--provider-api", "openai-chat-completions",
		"--base-url", server.URL, "--model", "gpt-test"}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	cmd := exec.CommandContext(ctx, os.Args[0], append(args, extraArgs...)...)
	cmd.Env = append(withoutCLNKREnv(os.Environ()), "CLNKR_HELPER_MAIN=1", "CLNKR_API_KEY=test-key")
	cmd.Stdin, cmd.Stderr = os.Stderr, os.Stderr
	return cmd, func() {
		cancel()
		if cmd.Process != nil && cmd.ProcessState == nil {
			_ = cmd.Wait()
		}
		server.Close()
	}
}

func requireProgramStatusExit(t *testing.T, cmd *exec.Cmd, want int) {
	t.Helper()
	err := cmd.Wait()
	var exitErr *exec.ExitError
	if err != nil && !errors.As(err, &exitErr) {
		t.Fatal(err)
	}
	if got := cmd.ProcessState.ExitCode(); got != want {
		t.Fatalf("exit code = %d, want %d", got, want)
	}
}

func waitForNextProgramStatusPrompt(t *testing.T, out *guardedBuffer) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for strings.Count(out.String(), "clnkr> ") != 2 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if strings.Count(out.String(), "clnkr> ") != 2 {
		t.Fatalf("next prompt not shown: %q", out.String())
	}
}

func TestProgramStatusFullSendClarification(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	for _, tt := range []struct {
		name, reply, finalMessage string
		exitKey                   byte
		wantExit                  int
		states                    []string
	}{
		{"reply", "this repo\n", "", 4, 0, []string{"working", "blocked/question", "working", "done"}},
		{"EOF", "", "Cancelled", 4, 0, []string{"working", "blocked/question", "idle"}},
		{"SIGINT", "", "Cancelled", 3, 130, []string{"working", "blocked/question", "idle"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			output := captureProgramStatus(t, func(out *guardedBuffer, input *os.File) {
				cmd, cleanup := programStatusClarificationCommand(t)
				defer cleanup()
				if tt.exitKey == 3 {
					cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0}
				}
				if err := cmd.Start(); err != nil {
					t.Fatal(err)
				}
				waitForOutput(t, out, "clnkr> ")
				if _, err := io.WriteString(input, "inspect\n"); err != nil {
					t.Fatal(err)
				}
				waitForOutput(t, out, "Which repo?")
				waitForNextProgramStatusPrompt(t, out)
				requireProgramStates(t, out.String(), "working", "blocked/question")
				reports := programStatuses(t, out.String())
				if reports[1]["msg"] != "Clarification needed" {
					t.Fatalf("question status = %#v", reports[1])
				}
				if tt.reply != "" {
					if _, err := io.WriteString(input, tt.reply); err != nil {
						t.Fatal(err)
					}
					waitForOutput(t, out, "state=done")
				}
				if _, err := input.Write([]byte{tt.exitKey}); err != nil {
					t.Fatal(err)
				}
				requireProgramStatusExit(t, cmd, tt.wantExit)
			})
			requireProgramStates(t, output, tt.states...)
			reports := programStatuses(t, output)
			if tt.finalMessage != "" && reports[2]["msg"] != tt.finalMessage {
				t.Fatalf("cancellation status = %#v", reports[2])
			}
		})
	}
}

func TestProgramStatusFullSendUnattendedClarification(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	trajectory := t.TempDir()
	trajectoryError := fmt.Sprintf(
		"cannot write trajectory %q: open %s: is a directory",
		trajectory,
		trajectory,
	)
	for _, tt := range []struct {
		name, input, message, diagnostics string
		args                              []string
		wantExit                          int
	}{
		{name: "terminal stdin", args: []string{"-p", "inspect"}, wantExit: 2},
		{name: "trajectory failure", args: []string{"-p", "inspect", "--trajectory", trajectory}, wantExit: 1,
			message: trajectoryError, diagnostics: "Error: " + trajectoryError + "\r\nError: clarify: clarification needed\r\n"},
		{name: "piped stdin", input: "inspect\n", wantExit: 2},
	} {
		t.Run(tt.name, func(t *testing.T) {
			output := captureProgramStatus(t, func(*guardedBuffer, *os.File) {
				cmd, cleanup := programStatusClarificationCommand(t, tt.args...)
				defer cleanup()
				if tt.input != "" {
					cmd.Stdin = strings.NewReader(tt.input)
				}
				if err := cmd.Start(); err != nil {
					t.Fatal(err)
				}
				requireProgramStatusExit(t, cmd, tt.wantExit)
			})
			requireProgramStates(t, output, "working", "error")
			if report := programStatuses(t, output)[1]; tt.message != "" &&
				report["msg"] != tt.message {
				t.Fatalf("error status = %#v, want %q", report, tt.message)
			}
			if !strings.HasSuffix(output, tt.diagnostics) {
				t.Fatalf("output = %q, want final diagnostics %q", output, tt.diagnostics)
			}
		})
	}
}

func TestProgramStatusFullSendClarificationInputFailure(t *testing.T) {
	for _, tt := range []struct {
		name     string
		queued   bool
		inputErr error
	}{
		{"prompt", false, errors.New("clarification input failed")},
		{"queued prompt", true, errors.New("clarification input failed")},
		{"wrapped EOF", false, fmt.Errorf("terminal read: %w", io.EOF)},
	} {
		t.Run(tt.name, func(t *testing.T) {
			model := &fakeModel{responses: []clnkr.Response{mustResponse(
				`{"type":"clarify","question":"Which repo?"}`,
			)}}
			agent := clnkr.NewAgent(model, &fakeExecutor{}, "/tmp")
			agent.Notify = func(event clnkr.Event) {
				if response, ok := event.(clnkr.EventResponse); ok {
					handleResponseEvent(response, notifyOptions{fullSend: true})
				}
			}
			driver := clnkrapp.NewDriver(agent, nil)
			reader := &lineReader{lines: make(chan lineResult, 2)}
			defer close(reader.lines)
			reader.lines <- lineResult{text: "inspect"}
			output := captureProgramStatus(t, func(out *guardedBuffer, _ *os.File) {
				finished := make(chan error, 1)
				go func() {
					finished <- runPromptLoop(driver, reader, nil, true, clnkrapp.PromptModeFullSend, nil)
				}()
				waitForOutput(t, out, "state=blocked")
				wantError := "read prompt: " + tt.inputErr.Error()
				if tt.queued {
					reader.lines <- lineResult{text: "this repo"}
					wantError = "read queued prompt lines: " + tt.inputErr.Error()
				}
				reader.lines <- lineResult{err: &terminalInputError{err: tt.inputErr}}
				err := <-finished
				if !errors.Is(err, tt.inputErr) || !isTerminalInputError(err) ||
					err.Error() != wantError {
					t.Fatalf("prompt loop error = %v, want %s", err, wantError)
				}
			})
			requireProgramStates(t, output, "working", "blocked/question", "error")
			reports := programStatuses(t, output)
			if !strings.Contains(reports[2]["msg"], tt.inputErr.Error()) {
				t.Fatalf("error status = %#v", reports[2])
			}
		})
	}
}

func TestProgramStatusFullSendClarificationAfterCancellation(t *testing.T) {
	model := &fakeModel{responses: []clnkr.Response{mustResponse(
		`{"type":"clarify","question":"Which repo?"}`,
	)}}
	agent := clnkr.NewAgent(model, &fakeExecutor{}, "/tmp")
	responseReady := make(chan struct{})
	releaseResponse := make(chan struct{})
	responseHandled := make(chan struct{})
	agent.Notify = func(event clnkr.Event) {
		if response, ok := event.(clnkr.EventResponse); ok {
			close(responseReady)
			<-releaseResponse
			handleResponseEvent(response, notifyOptions{fullSend: true})
			close(responseHandled)
		}
	}
	driver := clnkrapp.NewDriver(agent, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { <-responseReady; cancel() }()
	output := captureProgramStatus(t, func(*guardedBuffer, *os.File) {
		err := runDriverPrompt(ctx, driver, newLineReader(strings.NewReader("")), "inspect",
			clnkrapp.PromptModeFullSend, nil, func() {})
		close(releaseResponse)
		<-responseHandled
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("run error = %v, want cancellation", err)
		}
	})
	requireProgramStates(t, output, "working", "idle")
}

type statusCompactor struct{ calls int }

func (c *statusCompactor) Summarize(context.Context, []clnkr.Message) (string, error) {
	c.calls++
	return "Older work summarized.", nil
}

func TestProgramStatusTopLevelCompact(t *testing.T) {
	model := &fakeModel{}
	agent := clnkr.NewAgent(model, &fakeExecutor{}, "/tmp")
	for range 3 {
		if err := agent.AddMessages([]clnkr.Message{
			{Role: "user", Content: "task"},
			{Role: "assistant", Content: `{"type":"done","summary":"done"}`},
		}); err != nil {
			t.Fatal(err)
		}
	}
	compactor := &statusCompactor{}
	driver := clnkrapp.NewDriver(agent, func() clnkr.Compactor { return compactor })
	var events strings.Builder
	output := captureProgramStatus(t, func(*guardedBuffer, *os.File) {
		if err := runDriverPrompt(
			context.Background(),
			driver,
			newLineReader(strings.NewReader("")),
			"/compact",
			clnkrapp.PromptModeApproval,
			&events,
			func() {},
		); err != nil {
			t.Fatal(err)
		}
	})
	requireProgramStates(t, output, "working", "done")
	if compactor.calls != 1 || model.calls != 0 ||
		!strings.Contains(output, "Session compacted:") ||
		strings.Contains(events.String(), `"type":"done"`) {
		t.Fatalf(
			"compact calls=%d, model calls=%d, events=%s",
			compactor.calls,
			model.calls,
			&events,
		)
	}
}

type statusCancelledModel struct {
	queried, finished chan struct{}
}

func (m *statusCancelledModel) Query(
	ctx context.Context,
	_ []clnkr.Message,
) (clnkr.Response, error) {
	close(m.queried)
	<-ctx.Done()
	defer close(m.finished)
	return clnkr.Response{}, ctx.Err()
}

func TestProgramStatusCancellation(t *testing.T) {
	model := &statusCancelledModel{queried: make(chan struct{}), finished: make(chan struct{})}
	driver := clnkrapp.NewDriver(clnkr.NewAgent(model, &fakeExecutor{}, "/tmp"), nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { <-model.queried; cancel() }()
	output := captureProgramStatus(t, func(*guardedBuffer, *os.File) {
		err := runDriverPrompt(ctx, driver, newLineReader(strings.NewReader("")), "inspect",
			clnkrapp.PromptModeApproval, nil, func() {})
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("run error = %v, want cancellation", err)
		}
		<-model.finished
	})
	requireProgramStates(t, output, "working", "idle")
}

func TestProgramStatusBufferedInputFailureOverridesDriverResult(t *testing.T) {
	for _, response := range []clnkr.Response{
		mustResponse(`{"type":"act","bash":{"commands":[{"command":"printf ok","workdir":null}]}}`),
		mustResponse(`{"type":"clarify","question":"Which repo?"}`),
	} {
		t.Run(fmt.Sprintf("%T", response.Turn), func(t *testing.T) {
			model := &fakeModel{responses: []clnkr.Response{response}}
			driver := clnkrapp.NewDriver(clnkr.NewAgent(model, &fakeExecutor{}, "/tmp"), nil)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			finished := make(chan error, 1)
			go func() { finished <- driver.Prompt(ctx, "inspect", clnkrapp.PromptModeApproval) }()
			deadline := time.Now().Add(time.Second)
			for len(driver.Events()) == 0 && time.Now().Before(deadline) {
				runtime.Gosched()
			}
			if len(driver.Events()) == 0 {
				t.Fatal("request was not buffered")
			}
			cancel()
			if err := <-finished; !errors.Is(err, context.Canceled) {
				t.Fatalf("first run = %v, want cancellation", err)
			}
			inputErr := errors.New("buffered reply input failed")
			reader := &lineReader{lines: make(chan lineResult, 1)}
			reader.lines <- lineResult{err: &terminalInputError{err: inputErr}}
			close(reader.lines)
			output := captureProgramStatus(t, func(*guardedBuffer, *os.File) {
				err := runDriverPrompt(context.Background(), driver, reader, "next",
					clnkrapp.PromptModeApproval, nil, func() {})
				if !errors.Is(err, inputErr) || !isTerminalInputError(err) {
					t.Fatalf("run error = %v, want buffered input failure", err)
				}
			})
			reports := programStatuses(t, output)
			if len(reports) != 3 || reports[2]["state"] != "error" ||
				!strings.Contains(reports[2]["msg"], inputErr.Error()) {
				t.Fatalf("statuses = %#v, want final input failure", reports)
			}
		})
	}
}

func TestProgramStatusPreservesOutcomeAtNextPrompt(t *testing.T) {
	for _, final := range []string{"done", "error"} {
		for _, ending := range []struct {
			name string
			err  error
		}{
			{"EOF", io.EOF},
			{"input failure", &terminalInputError{err: errors.New("end test")}},
		} {
			t.Run(final+"/"+ending.name, func(t *testing.T) {
				model := &fakeModel{}
				if final == "done" {
					model.responses = []clnkr.Response{
						mustResponse(`{"type":"done","summary":"done"}`),
					}
				}
				driver := clnkrapp.NewDriver(clnkr.NewAgent(model, &fakeExecutor{}, "/tmp"), nil)
				reader := &lineReader{lines: make(chan lineResult, 1)}
				reader.lines <- lineResult{text: "inspect"}
				output := captureProgramStatus(t, func(out *guardedBuffer, _ *os.File) {
					finished := make(chan error, 1)
					go func() {
						finished <- runPromptLoop(driver, reader, nil, true, clnkrapp.PromptModeApproval, nil)
					}()
					defer close(reader.lines)
					waitForOutput(t, out, "state="+final)
					waitForNextProgramStatusPrompt(t, out)
					requireProgramStates(t, out.String(), "working", final)
					reader.lines <- lineResult{err: ending.err}
					wantErr := ending.err
					if wantErr == io.EOF {
						wantErr = nil
					}
					if err := <-finished; !errors.Is(err, wantErr) {
						t.Fatalf("prompt loop error = %v, want %v", err, wantErr)
					}
				})
				requireProgramStates(t, output, "working", final)
			})
		}
	}
}

func TestProgramStatusDoesNotInterleaveSpinner(t *testing.T) {
	output := captureProgramStatus(t, func(out *guardedBuffer, _ *os.File) {
		indicator := testModelWaitIndicator(true, os.Stderr, 0, time.Microsecond, time.Now)
		indicator.Start()
		defer indicator.Stop()
		waitForOutput(t, out, "waiting for model")
		for range 100 {
			writeProgramStatus("working", strings.Repeat("😀", 512))
		}
	})
	if reports := programStatuses(t, output); len(reports) != 100 {
		t.Fatalf("reports = %d, want 100 intact sequences", len(reports))
	}
}
