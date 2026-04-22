package main

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

type delegateRequest struct {
	Task       string
	WorkingDir string
	Parent     []clnkr.Message
	MaxSteps   int
}

type delegateResult struct {
	Summary        string
	Messages       []clnkr.Message
	TrajectoryPath string
}

type delegateProcessRunner struct {
	Binary      string
	Provider    string
	ProviderAPI string
	BaseURL     string
	Model       string
}

func (r delegateProcessRunner) Run(ctx context.Context, req delegateRequest) (delegateResult, error) {
	if strings.TrimSpace(req.Task) == "" {
		return delegateResult{}, fmt.Errorf("delegate run: empty task")
	}
	if strings.TrimSpace(req.WorkingDir) == "" {
		return delegateResult{}, fmt.Errorf("delegate run: empty working dir")
	}

	seedFile, err := os.CreateTemp("", "clnkr-delegate-seed-*.json")
	if err != nil {
		return delegateResult{}, fmt.Errorf("delegate run: create seed file: %w", err)
	}
	seedPath := seedFile.Name()
	defer func() { _ = os.Remove(seedPath) }()

	enc := json.NewEncoder(seedFile)
	if err := enc.Encode(req.Parent); err != nil {
		_ = seedFile.Close()
		return delegateResult{}, fmt.Errorf("delegate run: write seed file: %w", err)
	}
	if err := seedFile.Close(); err != nil {
		return delegateResult{}, fmt.Errorf("delegate run: close seed file: %w", err)
	}

	outFile, err := os.CreateTemp("", "clnkr-delegate-trajectory-*.json")
	if err != nil {
		return delegateResult{}, fmt.Errorf("delegate run: create trajectory file: %w", err)
	}
	outPath := outFile.Name()
	if err := outFile.Close(); err != nil {
		return delegateResult{}, fmt.Errorf("delegate run: close trajectory file: %w", err)
	}

	binary := r.Binary
	if binary == "" {
		binary = "clnku"
	}

	args := []string{"--load-messages", seedPath, "-p", req.Task, "--trajectory", outPath, "--full-send"}
	if strings.TrimSpace(r.Provider) != "" {
		args = append(args, "--provider", r.Provider)
	}
	if strings.TrimSpace(r.Provider) == "openai" && strings.TrimSpace(r.ProviderAPI) != "" {
		args = append(args, "--provider-api", r.ProviderAPI)
	}
	if strings.TrimSpace(r.BaseURL) != "" {
		args = append(args, "--base-url", r.BaseURL)
	}
	if strings.TrimSpace(r.Model) != "" {
		args = append(args, "--model", r.Model)
	}
	if req.MaxSteps > 0 {
		args = append(args, "--max-steps", fmt.Sprintf("%d", req.MaxSteps))
	}

	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.Dir = req.WorkingDir
	output, err := cmd.CombinedOutput()
	if err != nil {
		return delegateResult{}, fmt.Errorf("delegate run: %s %s in %s: %w\n%s", binary, strings.Join(args, " "), req.WorkingDir, err, strings.TrimSpace(string(output)))
	}

	data, err := os.ReadFile(outPath)
	if err != nil {
		return delegateResult{}, fmt.Errorf("delegate run: read trajectory %s: %w", filepath.Base(outPath), err)
	}

	var messages []clnkr.Message
	if err := json.Unmarshal(data, &messages); err != nil {
		return delegateResult{}, fmt.Errorf("delegate run: parse trajectory %s: %w", filepath.Base(outPath), err)
	}

	summary := extractDelegateSummary(messages)
	if summary == "" {
		return delegateResult{}, fmt.Errorf("delegate run: empty child summary")
	}

	return delegateResult{Summary: summary, Messages: messages, TrajectoryPath: outPath}, nil
}

func extractDelegateSummary(messages []clnkr.Message) string {
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

func formatDelegateArtifact(task, summary string) string {
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
