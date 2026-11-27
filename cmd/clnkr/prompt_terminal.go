//go:build linux || darwin

package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
	"unicode"
	"unicode/utf8"
	"unsafe"

	"github.com/clnkr-ai/clnkr/cmd/internal/clnkrapp"
)

type terminalPrompt struct{ history clnkrapp.PromptHistory }

func interactiveInput(cwd string, tty bool) promptReader {
	if !tty || os.Getenv("TERM") == "dumb" {
		return newLineReader(os.Stdin)
	}
	history, err := clnkrapp.OpenPromptHistory(cwd)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Warning: reading prompt history:", err) //nolint:errcheck
	}
	return &terminalPrompt{history}
}

// All terminal reads use stdin (fd 0). Wait before reading so cancellation
// never leaves a background reader competing with an editor or an approval.
func stdinReady(timeout time.Duration) (bool, error) {
	for {
		var set syscall.FdSet
		set.Bits[0] = 1
		tv := syscall.NsecToTimeval(int64(timeout))
		if err := selectStdin(&set, &tv); err != syscall.EINTR {
			return set.Bits[0] != 0, err
		}
	}
}

func readTerminal(ctx context.Context, buf []byte) (int, error) {
	for ctx.Err() == nil {
		ready, err := stdinReady(100 * time.Millisecond)
		if err != nil {
			return 0, err
		}
		if ready {
			return os.Stdin.Read(buf)
		}
	}
	return 0, ctx.Err()
}

func (*terminalPrompt) ReadLine(ctx context.Context) (string, error) {
	var buf [64 * 1024]byte
	n, err := readTerminal(ctx, buf[:])
	if err != nil && err != io.EOF && ctx.Err() == nil {
		err = &terminalInputError{err}
	}
	return strings.TrimSuffix(string(buf[:n]), "\n"), err
}

func termAttributes(request uintptr, settings *syscall.Termios) syscall.Errno {
	return ioctl(0, request, unsafe.Pointer(settings))
}

var pasteNewlines = strings.NewReplacer("\r\n", "\n", "\r", "\n")

type promptDraft struct {
	text    []byte
	history []string
	index   int
	width   int
	cooked  syscall.Termios
}

func (d *promptDraft) recall(offset int) {
	next := d.index + offset
	if next < 0 || next >= len(d.history) {
		return
	}
	if d.index == len(d.history)-1 {
		d.history[d.index] = string(d.text)
	}
	d.text, d.index = []byte(d.history[next]), next
}

func displayText(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) && r != '\n' {
			return '�'
		}
		return r
	}, s)
}

func (d *promptDraft) redraw() {
	width, _ := terminalSize(os.Stderr.Fd())
	if width == 0 {
		width = 80
	}
	if d.width != 0 && d.width != width {
		_, _ = fmt.Fprint(os.Stderr, "\nclnkr> ")
	}
	d.width = width
	line := summarizeCommand(string(d.text))
	if bytes.IndexByte(d.text, '\n') >= 0 {
		line += "  [Ctrl-G edit, Enter send]"
	}
	// Reserve a column to prevent autowrap. UTF-8 byte length provides a
	// conservative bound on the displayed width of printable characters.
	tail := line[max(0, len(line)-max(0, width-8)):]
	for len(tail) > 0 && !utf8.RuneStart(tail[0]) {
		tail = tail[1:]
	}
	_, _ = fmt.Fprint(os.Stderr, "\r\x1b[7C\x1b[K", displayText(tail))
}

// Consume only one key. Bytes following Ctrl-G must remain queued for the editor.
func readKey(ctx context.Context) (string, error) {
	var seq []byte
	for {
		var key [1]byte
		if _, err := readTerminal(ctx, key[:]); err != nil {
			return "", err
		}
		b := key[0]
		seq = append(seq, b)
		if seq[0] != 27 || (len(seq) > 2 && b >= 0x40 && b <= 0x7e) {
			return string(seq), nil
		}
		if len(seq) == 2 && b != '[' && b != 'O' {
			return string(key[:]), nil
		}
		if len(seq) > 32 {
			return "", nil
		}
	}
}

func (d *promptDraft) paste(ctx context.Context, marked bool) (bool, error) {
	var data []byte
	last := time.Now()
	for {
		if !marked {
			ready, err := stdinReady(10 * time.Millisecond)
			if err != nil {
				return false, err
			}
			if !ready {
				break
			}
		}
		var buf [1]byte
		if _, err := readTerminal(ctx, buf[:]); err != nil {
			return false, err
		}
		if marked && buf[0] == d.cooked.Cc[syscall.VINTR] &&
			time.Since(last) > 250*time.Millisecond {
			return false, context.Canceled
		}
		last = time.Now()
		data = append(data, buf[0])
		if marked && bytes.HasSuffix(data, []byte("\x1b[201~")) {
			data = data[:len(data)-6]
			break
		}
	}
	if !marked && len(data) > 0 {
		d.text = append(d.text, '\n')
	}
	d.text = append(d.text, pasteNewlines.Replace(string(data))...)
	return marked || len(data) > 0, nil
}

func (d *promptDraft) collect(ctx context.Context) (string, error) {
	mode := d.cooked
	mode.Lflag &^= syscall.ICANON | syscall.ISIG | syscall.ECHO | syscall.IEXTEN
	mode.Iflag &^= syscall.IXON | syscall.ICRNL | syscall.INLCR | syscall.IGNCR
	mode.Cc[syscall.VMIN], mode.Cc[syscall.VTIME] = 1, 0
	if err := termAttributes(setTerm, &mode); err != 0 {
		return "", err
	}
	_, _ = fmt.Fprint(os.Stderr, "\x1b[?2004h\rclnkr> ")
	d.redraw()
	action, err := d.keys(ctx)
	d.redraw()
	restoreErr := termAttributes(setTerm, &d.cooked)
	_, _ = fmt.Fprint(os.Stderr, "\x1b[?2004l")
	if restoreErr != 0 {
		return "", fmt.Errorf("restore terminal: %w", restoreErr)
	}
	return action, err
}

func (d *promptDraft) keys(ctx context.Context) (string, error) {
	cc := d.cooked.Cc
	for {
		key, err := readKey(ctx)
		if err != nil {
			return "", err
		}
		switch key {
		case "\r", "\n", "\x1b[200~":
			pasted, err := d.paste(ctx, key == "\x1b[200~")
			if err != nil || !pasted {
				return key, err
			}
		case string(cc[syscall.VINTR]):
			return "", context.Canceled
		case "\a", string(cc[syscall.VSUSP]):
			return key, nil
		case "\x00", string(cc[syscall.VEOF]): // Linux exposes a queued canonical EOF as NUL.
			if len(d.text) == 0 {
				return "", io.EOF
			}
		case "\x10", "\x1b[A", "\x1bOA":
			d.recall(-1)
		case "\x0e", "\x1b[B", "\x1bOB":
			d.recall(1)
		case string(cc[syscall.VKILL]):
			d.text = nil
		default:
			if bytes.IndexByte(d.text, '\n') >= 0 {
				continue
			}
			switch key {
			case "\b", string(cc[syscall.VERASE]):
				_, n := utf8.DecodeLastRune(d.text)
				d.text = d.text[:len(d.text)-n]
			case string(cc[syscall.VWERASE]):
				word := bytes.TrimRightFunc(d.text, unicode.IsSpace)
				d.text = bytes.TrimRightFunc(word, func(r rune) bool { return !unicode.IsSpace(r) })
			default:
				if len(key) == 1 && key[0] >= 32 {
					d.text = append(d.text, key[0])
				}
			}
		}
		d.redraw()
	}
}

func (t *terminalPrompt) ReadPrompt(_ bool) (string, error) {
	d := promptDraft{history: append(t.history.Entries, ""), index: len(t.history.Entries)}
	if err := termAttributes(getTerm, &d.cooked); err != 0 {
		return "", err
	}
	parent := context.Background()
	lifetime, shutdown := signal.NotifyContext(parent, syscall.SIGTERM, syscall.SIGHUP)
	defer shutdown()
	for {
		ctx, stop := signal.NotifyContext(lifetime, os.Interrupt)
		action, err := d.collect(ctx)
		if err != nil {
			stop()
			_, _ = fmt.Fprintln(os.Stderr)
			return "", err
		}
		switch action {
		case "\a":
			_, _ = fmt.Fprint(os.Stderr, "\r\x1b[K")
			// The editor shares the foreground process group. Consume its SIGINT
			// here and start the next prompt read with a fresh interrupt context.
			edited, err := editPrompt(lifetime, string(d.text))
			stop()
			if err != nil {
				_, _ = fmt.Fprintln(os.Stderr, "Editor:", err)
			}
			d.text = []byte(edited)
		case string(d.cooked.Cc[syscall.VSUSP]):
			stop()
			_ = syscall.Kill(os.Getpid(), syscall.SIGTSTP)
		default:
			stop()
			text := strings.TrimSpace(string(d.text))
			_, _ = fmt.Fprint(os.Stderr, "\r\x1b[7C\x1b[K", displayText(string(d.text)), "\n")
			if text != "" {
				if err := t.history.Add(text); err != nil {
					_, _ = fmt.Fprintln(os.Stderr, "Warning: saving prompt history:", err)
				}
			}
			return text, nil
		}
	}
}
