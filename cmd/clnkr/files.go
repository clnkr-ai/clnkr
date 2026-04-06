package main

import (
	"path/filepath"
	"regexp"
	"strings"
)

// fileTracker maintains a deduplicated list of files modified during a session.
type fileTracker struct {
	files []string
	seen  map[string]bool
}

// track adds a file to the tracked list if not already present.
func (ft *fileTracker) track(path string) {
	if ft.seen == nil {
		ft.seen = make(map[string]bool)
	}
	if ft.seen[path] {
		return
	}
	ft.seen[path] = true
	ft.files = append(ft.files, path)
}

func (ft *fileTracker) trackFeedback(paths []string, cwd string) {
	for _, path := range paths {
		if filepath.IsAbs(path) || cwd == "" {
			ft.track(path)
			continue
		}
		ft.track(filepath.Join(cwd, path))
	}
}

func (ft *fileTracker) pathsForCwd(cwd string) []string {
	out := make([]string, 0, len(ft.files))
	seen := make(map[string]bool, len(ft.files))
	for _, path := range ft.files {
		normalized := path
		if !filepath.IsAbs(path) || cwd == "" {
			normalized = filepath.Clean(path)
		} else {
			relative, err := filepath.Rel(cwd, path)
			if err != nil {
				normalized = filepath.Clean(path)
			} else {
				normalized = filepath.Clean(relative)
			}
		}
		if seen[normalized] {
			continue
		}
		seen[normalized] = true
		out = append(out, normalized)
	}
	return out
}

// trackFromCommand parses a command and its output for file modifications.
func (ft *fileTracker) trackFromCommand(command, output string) {
	for _, f := range extractModifiedFiles(command, output) {
		ft.track(f)
	}
}

// Patterns for extracting modified files from command output.
var gitDiffFileRe = regexp.MustCompile(`^diff --git a/.+ b/(.+)$`)

// extractModifiedFiles returns files that were likely modified by a command.
// This is heuristic — good enough for showing relevant diffs, not perfect.
func extractModifiedFiles(command, output string) []string {
	var files []string

	// Parse git diff output for file paths
	for _, line := range strings.Split(output, "\n") {
		if m := gitDiffFileRe.FindStringSubmatch(line); m != nil {
			files = append(files, m[1])
		}
	}
	if len(files) > 0 {
		return files
	}

	// Parse command itself for file-modifying patterns
	files = extractFilesFromCommand(command)
	return files
}

// extractFilesFromCommand uses heuristics on the command string to identify
// files that may have been created or modified.
func extractFilesFromCommand(cmd string) []string {
	// Normalize: strip leading whitespace, handle multi-line
	cmd = strings.TrimSpace(strings.Split(cmd, "\n")[0])

	parts := strings.Fields(cmd)
	if len(parts) == 0 {
		return nil
	}

	var files []string

	switch {
	// touch <file> [file...]
	case parts[0] == "touch" && len(parts) >= 2:
		for _, f := range parts[1:] {
			if !strings.HasPrefix(f, "-") {
				files = append(files, f)
			}
		}

	// tee <file>
	case containsWord(parts, "tee"):
		for i, p := range parts {
			if p == "tee" && i+1 < len(parts) {
				f := parts[i+1]
				if !strings.HasPrefix(f, "-") {
					files = append(files, f)
				}
			}
		}

	// > or >> redirect
	case strings.Contains(cmd, ">"):
		for i, p := range parts {
			if (p == ">" || p == ">>") && i+1 < len(parts) {
				files = append(files, parts[i+1])
			}
		}
		// Also handle foo>bar (no space)
		if len(files) == 0 {
			for _, p := range parts {
				if idx := strings.Index(p, ">"); idx > 0 && idx < len(p)-1 {
					target := strings.TrimLeft(p[idx+1:], ">")
					if target != "" {
						files = append(files, target)
					}
				}
			}
		}

	// sed -i
	case parts[0] == "sed" && containsWord(parts, "-i"):
		// Last non-flag argument is the file
		for i := len(parts) - 1; i >= 1; i-- {
			if !strings.HasPrefix(parts[i], "-") && !strings.HasPrefix(parts[i], "'") && !strings.HasPrefix(parts[i], "\"") {
				files = append(files, parts[i])
				break
			}
		}

	// cp <src> <dst>
	case parts[0] == "cp" && len(parts) >= 3:
		// Last argument is destination
		dst := parts[len(parts)-1]
		if !strings.HasPrefix(dst, "-") {
			files = append(files, dst)
		}

	// mv <src> <dst>
	case parts[0] == "mv" && len(parts) >= 3:
		// Both source (removed) and destination (created) are tracked
		for _, f := range parts[1:] {
			if !strings.HasPrefix(f, "-") {
				files = append(files, f)
			}
		}
	}

	return files
}

func containsWord(parts []string, word string) bool {
	for _, p := range parts {
		if p == word {
			return true
		}
	}
	return false
}
