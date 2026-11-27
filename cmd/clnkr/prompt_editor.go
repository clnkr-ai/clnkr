package main

import (
	"cmp"
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"
)

func editPrompt(ctx context.Context, draft string) (string, error) {
	f, err := os.CreateTemp("", "clnkr-prompt-*.txt")
	if err != nil {
		return draft, err
	}
	defer os.Remove(f.Name()) //nolint:errcheck
	_, err = f.WriteString(draft + "\n")
	if err := errors.Join(err, f.Close()); err != nil {
		return draft, err
	}
	editor := cmp.Or(strings.TrimSpace(os.Getenv("EDITOR")), "vi")
	args := []string{editor}
	if _, err := exec.LookPath(editor); err != nil {
		args = strings.Fields(editor)
	}
	cmd := exec.CommandContext(ctx, args[0], append(args[1:], f.Name())...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stderr, os.Stderr
	if err := cmd.Run(); err != nil {
		return draft, err
	}
	data, err := os.ReadFile(f.Name())
	if err != nil {
		return draft, err
	}
	return strings.TrimSuffix(string(data), "\n"), nil
}
