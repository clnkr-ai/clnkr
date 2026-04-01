package delegate

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	clnkr "github.com/clnkr-ai/clnkr"
)

type Request struct {
	Task       string
	WorkingDir string
	Parent     []clnkr.Message
	MaxSteps   int
}

type Result struct {
	Summary        string
	Messages       []clnkr.Message
	TrajectoryPath string
}

type Runner struct {
	Binary string
}

func (r Runner) Run(ctx context.Context, req Request) (Result, error) {
	if strings.TrimSpace(req.Task) == "" {
		return Result{}, fmt.Errorf("delegate run: empty task")
	}
	if strings.TrimSpace(req.WorkingDir) == "" {
		return Result{}, fmt.Errorf("delegate run: empty working dir")
	}

	seedFile, err := os.CreateTemp("", "clnkr-delegate-seed-*.json")
	if err != nil {
		return Result{}, fmt.Errorf("delegate run: create seed file: %w", err)
	}
	seedPath := seedFile.Name()
	defer os.Remove(seedPath)

	enc := json.NewEncoder(seedFile)
	if err := enc.Encode(req.Parent); err != nil {
		seedFile.Close()
		return Result{}, fmt.Errorf("delegate run: write seed file: %w", err)
	}
	if err := seedFile.Close(); err != nil {
		return Result{}, fmt.Errorf("delegate run: close seed file: %w", err)
	}

	outFile, err := os.CreateTemp("", "clnkr-delegate-trajectory-*.json")
	if err != nil {
		return Result{}, fmt.Errorf("delegate run: create trajectory file: %w", err)
	}
	outPath := outFile.Name()
	if err := outFile.Close(); err != nil {
		return Result{}, fmt.Errorf("delegate run: close trajectory file: %w", err)
	}

	binary := r.Binary
	if binary == "" {
		binary = "clnku"
	}

	args := []string{"--load-messages", seedPath, "-p", req.Task, "--trajectory", outPath, "--full-send"}
	if req.MaxSteps > 0 {
		args = append(args, "--max-steps", fmt.Sprintf("%d", req.MaxSteps))
	}

	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.Dir = req.WorkingDir
	output, err := cmd.CombinedOutput()
	if err != nil {
		return Result{}, fmt.Errorf("delegate run: %s %s in %s: %w\n%s", binary, strings.Join(args, " "), req.WorkingDir, err, strings.TrimSpace(string(output)))
	}

	data, err := os.ReadFile(outPath)
	if err != nil {
		return Result{}, fmt.Errorf("delegate run: read trajectory %s: %w", filepath.Base(outPath), err)
	}

	var messages []clnkr.Message
	if err := json.Unmarshal(data, &messages); err != nil {
		return Result{}, fmt.Errorf("delegate run: parse trajectory %s: %w", filepath.Base(outPath), err)
	}

	summary := extractSummary(messages)
	if summary == "" {
		return Result{}, fmt.Errorf("delegate run: empty child summary")
	}

	return Result{Summary: summary, Messages: messages, TrajectoryPath: outPath}, nil
}

func extractSummary(messages []clnkr.Message) string {
	for i := len(messages) - 1; i >= 0; i-- {
		msg := messages[i]
		if msg.Role != "assistant" {
			continue
		}
		turn, err := clnkr.ParseTurn(msg.Content)
		if err == nil {
			if done, ok := turn.(*clnkr.DoneTurn); ok {
				return strings.TrimSpace(done.Summary)
			}
		}
		content := strings.TrimSpace(msg.Content)
		if content != "" {
			return content
		}
	}
	return ""
}

func FormatArtifact(task, summary string) string {
	payload := struct {
		Source  string `json:"source"`
		Kind    string `json:"kind"`
		Task    string `json:"task"`
		Summary string `json:"summary"`
	}{
		Source:  "clnkr",
		Kind:    "delegate",
		Task:    task,
		Summary: summary,
	}
	data, _ := json.Marshal(payload)
	return "[delegate]\n" + string(data) + "\n[/delegate]"
}
