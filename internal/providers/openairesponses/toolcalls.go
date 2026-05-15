package openairesponses

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/clnkr-ai/clnkr"
	"github.com/clnkr-ai/clnkr/internal/providers/openaiwire"
)

type toolCallResponseParts struct {
	calls      []outputItem
	text       string
	replay     []clnkr.ProviderReplayItem
	rawBody    string
	usage      clnkr.Usage
	protocolOK bool
}

func parseToolCallResponse(respBody []byte) (clnkr.Response, error) {
	apiResp, err := unmarshalResponse(respBody)
	if err != nil {
		return clnkr.Response{}, err
	}

	parts, err := collectToolCallResponseParts(apiResp, string(respBody))
	if err != nil {
		return clnkr.Response{}, err
	}
	if len(parts.calls) > 0 {
		return responseFromFunctionCalls(parts), nil
	}
	return responseFromToolCallText(parts)
}

func collectToolCallResponseParts(apiResp response, rawBody string) (toolCallResponseParts, error) {
	parts := toolCallResponseParts{
		rawBody: rawBody,
		usage: clnkr.Usage{
			InputTokens:  apiResp.Usage.InputTokens,
			OutputTokens: apiResp.Usage.OutputTokens,
		},
		replay: make([]clnkr.ProviderReplayItem, 0),
	}
	var text strings.Builder

	for i, item := range apiResp.Output {
		switch item.Type {
		case "function_call":
			parts.calls = append(parts.calls, item)
		case "message":
			if item.Role != "assistant" {
				continue
			}
			for _, content := range item.Content {
				switch content.Type {
				case "output_text":
					text.WriteString(content.Text)
				case "refusal":
					if strings.TrimSpace(content.Refusal) != "" {
						return toolCallResponseParts{}, fmt.Errorf("tool-call refusal: %s", content.Refusal)
					}
				}
			}
		default:
			if i < len(apiResp.RawOutput) {
				parts.replay = append(parts.replay, clnkr.ProviderReplayItem{
					Provider:    replayProvider,
					ProviderAPI: replayProviderAPI,
					Type:        item.Type,
					JSON:        append(json.RawMessage(nil), apiResp.RawOutput[i]...),
				})
			}
		}
	}

	parts.text = text.String()
	if len(parts.calls) > 0 && strings.TrimSpace(parts.text) != "" {
		parts.protocolOK = false
		return parts, nil
	}
	parts.protocolOK = true
	return parts, nil
}

func responseFromFunctionCalls(parts toolCallResponseParts) clnkr.Response {
	if !parts.protocolOK {
		return clnkr.Response{
			Raw:         parts.rawBody,
			Usage:       parts.usage,
			ProtocolErr: fmt.Errorf("%w: mixed bash tool call and structured text", clnkr.ErrInvalidJSON),
		}
	}

	actions := make([]clnkr.BashAction, 0, len(parts.calls))
	toolCalls := make([]clnkr.BashToolCall, 0, len(parts.calls))
	for _, item := range parts.calls {
		action, call, err := turnFromFunctionCall(item)
		if err != nil {
			return clnkr.Response{Raw: parts.rawBody, Usage: parts.usage, ProtocolErr: err}
		}
		actions = append(actions, action)
		toolCalls = append(toolCalls, call)
	}
	return clnkr.Response{
		Turn:           &clnkr.ActTurn{Bash: clnkr.BashBatch{Commands: actions}},
		Raw:            parts.rawBody,
		Usage:          parts.usage,
		BashToolCalls:  toolCalls,
		ProviderReplay: parts.replay,
	}
}

func responseFromToolCallText(parts toolCallResponseParts) (clnkr.Response, error) {
	if strings.TrimSpace(parts.text) == "" {
		return clnkr.Response{}, fmt.Errorf("tool-call response: no usable output_text or bash tool call")
	}
	turn, err := openaiwire.ParseProviderTurn(parts.text)
	if err != nil {
		return clnkr.Response{Raw: parts.text, Usage: parts.usage, ProtocolErr: err}, nil
	}
	if _, ok := turn.(*clnkr.ActTurn); ok {
		return clnkr.Response{
			Raw:         parts.text,
			Usage:       parts.usage,
			ProtocolErr: fmt.Errorf("%w: tool-call mode does not accept text act turns", clnkr.ErrInvalidJSON),
		}, nil
	}
	if _, err := clnkr.CanonicalTurnJSON(turn); err != nil {
		return clnkr.Response{}, fmt.Errorf("canonicalize tool-call payload: %w", err)
	}
	return clnkr.Response{Turn: turn, Raw: parts.text, Usage: parts.usage}, nil
}

func turnFromFunctionCall(item outputItem) (clnkr.BashAction, clnkr.BashToolCall, error) {
	if strings.TrimSpace(item.CallID) == "" {
		return clnkr.BashAction{}, clnkr.BashToolCall{}, fmt.Errorf("%w: bash tool call missing call_id", clnkr.ErrInvalidJSON)
	}
	if item.Name != "bash" {
		return clnkr.BashAction{}, clnkr.BashToolCall{}, fmt.Errorf("%w: unsupported tool %q", clnkr.ErrInvalidJSON, item.Name)
	}
	var args struct {
		Command string  `json:"command"`
		Workdir *string `json:"workdir"`
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal([]byte(item.Arguments), &fields); err != nil {
		return clnkr.BashAction{}, clnkr.BashToolCall{}, fmt.Errorf("%w: malformed bash tool arguments: %v", clnkr.ErrInvalidJSON, err)
	}
	if _, ok := fields["workdir"]; !ok {
		return clnkr.BashAction{}, clnkr.BashToolCall{}, fmt.Errorf("%w: bash tool arguments missing workdir", clnkr.ErrInvalidJSON)
	}
	dec := json.NewDecoder(strings.NewReader(item.Arguments))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&args); err != nil {
		return clnkr.BashAction{}, clnkr.BashToolCall{}, fmt.Errorf("%w: malformed bash tool arguments: %v", clnkr.ErrInvalidJSON, err)
	}
	if strings.TrimSpace(args.Command) == "" {
		return clnkr.BashAction{}, clnkr.BashToolCall{}, clnkr.ErrMissingCommand
	}
	call := clnkr.BashToolCall{ID: item.CallID, Command: args.Command}
	action := clnkr.BashAction{ID: item.CallID, Command: args.Command}
	if args.Workdir != nil {
		call.Workdir = *args.Workdir
		action.Workdir = *args.Workdir
	}
	return action, call, nil
}
