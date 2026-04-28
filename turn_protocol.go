package clnkr

import (
	"fmt"
	"strings"
)

// TurnProtocol selects the model turn contract.
type TurnProtocol string

const (
	TurnProtocolStructuredJSON  TurnProtocol = "structured-json"
	TurnProtocolNativeBashTools TurnProtocol = "native-bash-tools"
)

// ParseTurnProtocol validates a turn protocol name.
func ParseTurnProtocol(raw string) (TurnProtocol, error) {
	protocol := TurnProtocol(strings.ToLower(strings.TrimSpace(raw)))
	if protocol == "" {
		return TurnProtocolStructuredJSON, nil
	}
	switch protocol {
	case TurnProtocolStructuredJSON, TurnProtocolNativeBashTools:
		return protocol, nil
	default:
		return "", fmt.Errorf(`invalid turn-protocol %q (allowed: structured-json, native-bash-tools)`, raw)
	}
}

func normalizeTurnProtocol(protocol TurnProtocol) TurnProtocol {
	if protocol == "" {
		return TurnProtocolStructuredJSON
	}
	return protocol
}
