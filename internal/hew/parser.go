package hew

import (
	"errors"
	"regexp"
	"strings"
)

var actionPattern = regexp.MustCompile("(?s)```bash\n(.*?)\n```")

// ErrNoAction indicates the LLM response contained no bash code block.
var ErrNoAction = errors.New("no bash action found in response")

// ParseAction extracts the first bash command from a fenced code block in LLM output.
func ParseAction(output string) (string, error) {
	matches := actionPattern.FindStringSubmatch(output)
	if len(matches) < 2 {
		return "", ErrNoAction
	}
	action := strings.TrimSpace(matches[1])
	if action == "" {
		return "", ErrNoAction
	}
	return action, nil
}
