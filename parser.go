package hew

import (
	"errors"
	"regexp"
	"strings"
)

var actionPattern = regexp.MustCompile("(?s)```bash\n(.*?)\n```")

// ErrNoCommand means the LLM response had no bash code block.
var ErrNoCommand = errors.New("no bash command found in response")

// ExtractCommand pulls the first bash command from a fenced code block in LLM output.
func ExtractCommand(output string) (string, error) {
	matches := actionPattern.FindStringSubmatch(output)
	if len(matches) < 2 {
		return "", ErrNoCommand
	}
	action := strings.TrimSpace(matches[1])
	if action == "" {
		return "", ErrNoCommand
	}
	return action, nil
}
