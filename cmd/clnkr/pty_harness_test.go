package main

import (
	"bytes"
	"errors"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/creack/pty"
)

type terminalSize struct {
	cols uint16
	rows uint16
}

type ptyStartOptions struct {
	binary string
	args   []string
	env    map[string]string
	size   terminalSize
}

type ptySession struct {
	t       *testing.T
	cmd     *exec.Cmd
	ptmx    *os.File
	done    chan struct{}
	mu      sync.Mutex
	raw     bytes.Buffer
	waitErr error
	ended   bool
}

func startPTYSession(t *testing.T, opts ptyStartOptions) *ptySession {
	t.Helper()

	cmd := exec.Command(opts.binary, opts.args...)
	cmd.Env = append(os.Environ(), "TERM=xterm-256color")
	for key, value := range opts.env {
		cmd.Env = append(cmd.Env, key+"="+value)
	}

	size := &pty.Winsize{Cols: opts.size.cols, Rows: opts.size.rows}
	ptmx, err := pty.StartWithSize(cmd, size)
	if err != nil {
		t.Fatalf("start pty session: %v", err)
	}

	session := &ptySession{
		t:    t,
		cmd:  cmd,
		ptmx: ptmx,
		done: make(chan struct{}),
	}
	go session.readOutput()
	go func() {
		err := cmd.Wait()
		session.mu.Lock()
		session.waitErr = err
		session.mu.Unlock()
		close(session.done)
	}()
	t.Cleanup(session.Close)
	return session
}

func (s *ptySession) readOutput() {
	buf := make([]byte, 4096)
	for {
		n, err := s.ptmx.Read(buf)
		if n > 0 {
			s.mu.Lock()
			_, _ = s.raw.Write(buf[:n])
			s.mu.Unlock()
		}
		if err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, os.ErrClosed) {
				return
			}
			return
		}
	}
}

func (s *ptySession) Send(input string) {
	s.t.Helper()
	for _, r := range input {
		if _, err := io.WriteString(s.ptmx, string(r)); err != nil {
			s.t.Fatalf("send input %q: %v", input, err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func (s *ptySession) WaitFor(substr string) {
	s.t.Helper()

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(s.snapshot(), substr) {
			return
		}
		if err, exited := s.exitStatus(); exited {
			finalDeadline := time.Now().Add(100 * time.Millisecond)
			for time.Now().Before(finalDeadline) {
				if strings.Contains(s.snapshot(), substr) {
					return
				}
				time.Sleep(10 * time.Millisecond)
			}
			s.failWithSnapshot("process exited before %q: %v", substr, err)
		}
		time.Sleep(20 * time.Millisecond)
	}
	s.failWithSnapshot("timed out waiting for %q", substr)
}

func (s *ptySession) WaitExit(code int) {
	s.t.Helper()

	timer := time.NewTimer(10 * time.Second)
	defer timer.Stop()

	select {
	case <-s.done:
		err, _ := s.exitStatus()
		if code == 0 && err != nil {
			s.failWithSnapshot("process exited with %v, want success", err)
		}
		if code != 0 {
			var exitErr *exec.ExitError
			if !errors.As(err, &exitErr) {
				s.failWithSnapshot("process exit = %v, want code %d", err, code)
			}
			if exitErr.ExitCode() != code {
				s.failWithSnapshot("exit code = %d, want %d", exitErr.ExitCode(), code)
			}
		}
	case <-timer.C:
		s.failWithSnapshot("timed out waiting for process exit")
	}
}

func (s *ptySession) Close() {
	s.mu.Lock()
	if s.ended {
		s.mu.Unlock()
		return
	}
	s.ended = true
	s.mu.Unlock()

	_ = s.ptmx.Close()
	if s.cmd.Process != nil {
		_ = s.cmd.Process.Signal(syscall.SIGTERM)
		select {
		case <-s.done:
		case <-time.After(250 * time.Millisecond):
			_ = s.cmd.Process.Kill()
			select {
			case <-s.done:
			case <-time.After(250 * time.Millisecond):
			}
		}
	}
}

func (s *ptySession) snapshot() string {
	s.mu.Lock()
	raw := s.raw.String()
	s.mu.Unlock()
	return normalizePTYOutput(raw)
}

func (s *ptySession) rawSnapshot() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.raw.String()
}

func (s *ptySession) exitStatus() (error, bool) {
	select {
	case <-s.done:
		s.mu.Lock()
		defer s.mu.Unlock()
		return s.waitErr, true
	default:
		return nil, false
	}
}

func (s *ptySession) failWithSnapshot(format string, args ...any) {
	s.t.Helper()
	s.t.Fatalf(format+"\n\nscreen snapshot:\n%s", append(args, s.snapshot())...)
}

func (s *ptySession) WaitForRaw(substr string) {
	s.t.Helper()

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(s.rawSnapshot(), substr) {
			return
		}
		if err, exited := s.exitStatus(); exited {
			s.failWithSnapshot("process exited before raw output %q: %v", substr, err)
		}
		time.Sleep(20 * time.Millisecond)
	}
	s.failWithSnapshot("timed out waiting for raw output %q", substr)
}

var (
	csiPattern = regexp.MustCompile(`\x1b\[[0-?]*[ -/]*[@-~]`)
	oscPattern = regexp.MustCompile(`\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)`)
)

func normalizePTYOutput(raw string) string {
	withoutOSC := oscPattern.ReplaceAllString(raw, "")
	withoutCSI := csiPattern.ReplaceAllString(withoutOSC, "")
	withoutCR := strings.ReplaceAll(withoutCSI, "\r", "")
	lines := strings.Split(withoutCR, "\n")
	for i, line := range lines {
		lines[i] = strings.TrimRight(line, " ")
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}
