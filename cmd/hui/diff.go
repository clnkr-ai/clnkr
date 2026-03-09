package main

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"charm.land/bubbles/v2/viewport"
)

// diffModel is a modal overlay that shows git diff of tracked files.
type diffModel struct {
	viewport viewport.Model
	visible  bool
	content  string
	styles   *styles
}

func newDiffModel(s *styles) diffModel {
	vp := viewport.New()
	return diffModel{
		viewport: vp,
		styles:   s,
	}
}

// toggle shows or hides the diff overlay.
func (d *diffModel) toggle(files []string, cwd string, width, height int) {
	if d.visible {
		d.visible = false
		return
	}

	d.visible = true
	d.viewport.SetWidth(width)
	d.viewport.SetHeight(height)

	if len(files) == 0 {
		d.content = "No files modified during this session."
		d.viewport.SetContent(d.content)
		d.viewport.GotoTop()
		return
	}

	d.content = d.buildContent(files, cwd)
	d.viewport.SetContent(d.content)
	d.viewport.GotoTop()
}

// buildContent runs git diff for tracked files and builds the overlay content.
func (d *diffModel) buildContent(files []string, cwd string) string {
	var b strings.Builder

	b.WriteString("Modified files:\n")
	for _, f := range files {
		fmt.Fprintf(&b, "  • %s\n", f)
	}
	b.WriteString("\n")

	// Run git diff with a short timeout
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	args := append([]string{"diff", "--"}, files...)
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = cwd
	out, err := cmd.Output()
	if err != nil {
		fmt.Fprintf(&b, "(git diff error: %s)\n", err)
		return b.String()
	}

	if len(out) == 0 {
		b.WriteString("(no uncommitted changes in tracked files)\n")
	} else {
		b.Write(out)
	}

	return b.String()
}

func (d *diffModel) resize(width, height int) {
	d.viewport.SetWidth(width)
	d.viewport.SetHeight(height)
}

func (d *diffModel) view() string {
	if !d.visible {
		return ""
	}
	return d.viewport.View()
}
