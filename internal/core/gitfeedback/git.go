package gitfeedback

import (
	"bytes"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
)

const (
	maxFiles            = 50
	maxDiffSize         = 12_000
	diffTruncatedMarker = "... diff truncated ..."
)

// Summary captures git-backed host feedback for the last command.
type Summary struct {
	ChangedFiles []string
	Diff         string
}

// Baseline records whether a command started from a clean git worktree.
type Baseline struct {
	root  string
	clean bool
}

// Detect records the git baseline for a working directory.
func Detect(dir string) Baseline {
	root, ok := gitTopLevel(dir)
	if !ok {
		return Baseline{}
	}
	status, ok := gitStatusPorcelain(root)
	if !ok {
		return Baseline{}
	}
	return Baseline{root: root, clean: len(status) == 0}
}

// Collect returns rename-aware feedback relative to finalCwd.
func (b Baseline) Collect(finalCwd string) (Summary, bool) {
	if b.root == "" || !b.clean {
		return Summary{}, false
	}

	status, ok := gitStatusPorcelain(b.root)
	if !ok {
		return Summary{}, false
	}
	diff, ok := gitCombinedDiff(b.root)
	if !ok {
		return Summary{}, false
	}

	files := parseStatusChangedFiles(status, b.root, finalCwd)
	files, diff = truncateSummary(files, diff)

	if len(files) == 0 && diff == "" {
		return Summary{}, false
	}
	return Summary{ChangedFiles: files, Diff: diff}, true
}

func gitTopLevel(dir string) (string, bool) {
	out, ok := gitOutput(dir, "rev-parse", "--show-toplevel")
	if !ok {
		return "", false
	}
	return strings.TrimSpace(string(out)), true
}

func gitStatusPorcelain(dir string) ([]byte, bool) {
	return gitOutput(dir, "status", "--porcelain", "-z", "--untracked-files=all")
}

func gitCombinedDiff(dir string) (string, bool) {
	unstaged, ok := gitOutput(dir, "diff", "--no-ext-diff", "--unified=3")
	if !ok {
		return "", false
	}
	cached, ok := gitOutput(dir, "diff", "--cached", "--no-ext-diff", "--unified=3")
	if !ok {
		return "", false
	}

	var parts []string
	if len(unstaged) > 0 {
		parts = append(parts, string(unstaged))
	}
	if len(cached) > 0 {
		parts = append(parts, string(cached))
	}
	return strings.Join(parts, "\n"), true
}

func gitOutput(dir string, args ...string) ([]byte, bool) {
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).Output()
	return out, err == nil
}

func parseStatusChangedFiles(status []byte, repoRoot, finalCwd string) []string {
	var files []string
	seen := make(map[string]bool)
	entries := bytes.Split(status, []byte{0})

	for i := 0; i < len(entries); i++ {
		entry := entries[i]
		if len(entry) == 0 || len(entry) < 4 {
			continue
		}

		code := string(entry[:2])
		paths := []string{string(entry[3:])}
		if isRenameOrCopyStatus(code) && i+1 < len(entries) && len(entries[i+1]) > 0 {
			paths = append(paths, string(entries[i+1]))
			i++
		}

		for _, repoRel := range paths {
			normalized := normalizePath(repoRoot, finalCwd, repoRel)
			if normalized == "" || seen[normalized] {
				continue
			}
			seen[normalized] = true
			files = append(files, normalized)
		}
	}

	return files
}

func isRenameOrCopyStatus(code string) bool {
	return strings.Contains(code, "R") || strings.Contains(code, "C")
}

func normalizePath(repoRoot, finalCwd, repoRel string) string {
	absolute := filepath.Join(repoRoot, filepath.FromSlash(repoRel))
	relative, err := filepath.Rel(finalCwd, absolute)
	if err != nil {
		return filepath.Clean(absolute)
	}
	return filepath.Clean(relative)
}

func truncateSummary(files []string, diff string) ([]string, string) {
	note := ""
	if len(files) > maxFiles {
		remaining := len(files) - maxFiles
		files = append([]string(nil), files[:maxFiles]...)
		note = fmt.Sprintf("... (%d more files omitted)", remaining)
	}
	if note != "" {
		diff = appendDiffNote(diff, note)
	} else if len(diff) > maxDiffSize {
		diff = diff[:maxDiffSize] + "\n" + diffTruncatedMarker
	}
	return files, diff
}

func appendDiffNote(diff, note string) string {
	if diff == "" {
		return note
	}

	suffix := "\n" + note
	if len(diff)+len(suffix) <= maxDiffSize {
		return diff + suffix
	}

	budget := maxDiffSize - len("\n"+diffTruncatedMarker+suffix)
	if budget < 0 {
		budget = 0
	}
	diff = strings.TrimRight(diff[:budget], "\n")
	if diff == "" {
		return diffTruncatedMarker + suffix
	}
	return diff + "\n" + diffTruncatedMarker + suffix
}
