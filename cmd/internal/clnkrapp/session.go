package clnkrapp

import (
	"fmt"
	"time"

	"github.com/clnkr-ai/clnkr"
	"github.com/clnkr-ai/clnkr/internal/session"
)

type SessionInfo struct {
	Filename string
	Created  time.Time
	Messages int
}

func ListSessions(cwd string) ([]SessionInfo, error) {
	sessions, err := session.ListSessions(cwd)
	if err != nil {
		return nil, err
	}
	infos := make([]SessionInfo, 0, len(sessions))
	for _, s := range sessions {
		infos = append(infos, SessionInfo{
			Filename: s.Filename,
			Created:  s.Created,
			Messages: s.Messages,
		})
	}
	return infos, nil
}

func SaveSession(cwd string, messages []clnkr.Message, metadata RunMetadata) (string, error) {
	dir, err := session.SessionDir(cwd)
	if err != nil {
		return "", fmt.Errorf("save session: %w", err)
	}
	if err := session.SaveSessionWithMetadata(cwd, messages, metadata); err != nil {
		return "", err
	}
	return dir, nil
}

func ResumeLatestSession(agent *clnkr.Agent, cwd string) (int, bool, error) {
	msgs, err := session.LoadLatestSession(cwd)
	if err != nil {
		return 0, false, fmt.Errorf("cannot load session: %w", err)
	}
	if msgs == nil {
		return 0, false, nil
	}
	if err := agent.AddMessages(msgs); err != nil {
		return 0, false, fmt.Errorf("cannot resume session: %w", err)
	}
	return len(msgs), true, nil
}
