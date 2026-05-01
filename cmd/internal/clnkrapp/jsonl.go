package clnkrapp

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/clnkr-ai/clnkr"
)

type JSONLCommand struct {
	Type         string `json:"type"`
	Text         string `json:"text,omitempty"`
	Mode         string `json:"mode,omitempty"`
	Instructions string `json:"instructions,omitempty"`
}

func DecodeJSONLCommand(line []byte) (JSONLCommand, error) {
	var command JSONLCommand
	if err := json.Unmarshal(line, &command); err != nil {
		return JSONLCommand{}, fmt.Errorf("decode JSONL command: %w", err)
	}
	switch command.Type {
	case "prompt":
		if strings.TrimSpace(command.Text) == "" {
			return JSONLCommand{}, fmt.Errorf("JSONL prompt text is required")
		}
		switch command.Mode {
		case PromptModeApproval, PromptModeFullSend:
			return command, nil
		default:
			return JSONLCommand{}, fmt.Errorf("unknown JSONL prompt mode %q", command.Mode)
		}
	case "reply", "compact", "shutdown":
		return command, nil
	default:
		return JSONLCommand{}, fmt.Errorf("unknown JSONL command type %q", command.Type)
	}
}

func WriteJSONL(w io.Writer, event any) error {
	if event, ok := event.(clnkr.Event); ok {
		return WriteEventLog(w, event)
	}
	switch event := event.(type) {
	case EventClarificationRequest:
		return writeEvent(w, "clarify", map[string]string{"question": event.Question})
	case EventApprovalRequest:
		return writeEvent(w, "approval_request", map[string]any{"prompt": event.Prompt, "commands": event.Commands})
	case EventDone:
		return writeEvent(w, "done", map[string]string{"summary": event.Summary})
	case EventCompacted:
		return writeEvent(w, "compacted", map[string]int{
			"compacted_messages": event.Stats.CompactedMessages,
			"kept_messages":      event.Stats.KeptMessages,
		})
	case EventError:
		return writeEvent(w, "error", map[string]string{"message": errorMessage(event.Err)})
	default:
		return fmt.Errorf("write JSONL: unsupported event %T", event)
	}
}

func errorMessage(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
