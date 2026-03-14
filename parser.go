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

func ExtractCommands(output string) ([]string, error) {
	allMatches := actionPattern.FindAllStringSubmatch(output, -1)
	var commands []string
	for _, m := range allMatches {
		if len(m) < 2 {
			continue
		}
		cmd := strings.TrimSpace(m[1])
		if cmd != "" {
			commands = append(commands, cmd)
		}
	}
	if len(commands) == 0 {
		return nil, ErrNoCommand
	}
	return commands, nil
}
