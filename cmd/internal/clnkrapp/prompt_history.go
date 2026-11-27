package clnkrapp

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/clnkr-ai/clnkr/internal/session"
)

type PromptHistory struct {
	path    string
	Entries []string
}

func OpenPromptHistory(cwd string) (PromptHistory, error) {
	dir, err := session.SessionDir(cwd)
	var h PromptHistory
	if err != nil {
		return h, err
	}
	h.path = filepath.Join(dir, "prompt-history")
	data, err := os.ReadFile(h.path)
	if errors.Is(err, os.ErrNotExist) {
		return h, nil
	}
	if err != nil {
		return h, err
	}
	// JSON text sequences delimit each record at the start. A truncated write
	// cannot hide the next complete prompt, even when another process appends it.
	for record := range bytes.SplitSeq(data, []byte{0x1e}) {
		if len(record) == 0 {
			continue
		}
		var prompt string
		if json.Unmarshal(record, &prompt) != nil {
			err = fmt.Errorf("incomplete or invalid prompt history record in %s", h.path)
			continue
		}
		h.Entries = append(h.Entries, prompt)
	}
	return h, err
}

func (h *PromptHistory) Add(text string) error {
	h.Entries = append(h.Entries, text)
	if h.path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(h.path), 0700); err != nil {
		return err
	}
	f, err := os.OpenFile(h.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	record, _ := json.Marshal(text)
	_, err = f.Write(append(append([]byte{0x1e}, record...), '\n'))
	return errors.Join(err, f.Close())
}
