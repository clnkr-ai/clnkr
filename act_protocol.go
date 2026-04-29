package clnkr

import (
	"fmt"
	"strings"
)

// ActProtocol selects the model act protocol.
type ActProtocol string

const (
	ActProtocolClnkrInline ActProtocol = "clnkr-inline"
	ActProtocolToolCalls   ActProtocol = "tool-calls"
)

// ParseActProtocol validates an act protocol name.
func ParseActProtocol(raw string) (ActProtocol, error) {
	protocol := ActProtocol(strings.ToLower(strings.TrimSpace(raw)))
	if protocol == "" {
		return ActProtocolClnkrInline, nil
	}
	switch protocol {
	case ActProtocolClnkrInline, ActProtocolToolCalls:
		return protocol, nil
	default:
		return "", fmt.Errorf(`invalid act-protocol %q (allowed: clnkr-inline, tool-calls)`, raw)
	}
}

func normalizeActProtocol(protocol ActProtocol) ActProtocol {
	if protocol == "" {
		return ActProtocolClnkrInline
	}
	return protocol
}
