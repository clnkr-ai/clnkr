package turnjson

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

type wireEnvelope struct {
	Turn wireTurn `json:"turn"`
}

type wireTurn struct {
	Type      string    `json:"type"`
	Bash      *wireBash `json:"bash"`
	Question  any       `json:"question"`
	Summary   any       `json:"summary"`
	Reasoning any       `json:"reasoning"`
}

type wireBash struct {
	Command string  `json:"command"`
	Workdir *string `json:"workdir"`
}

func EnsureSingleJSONObject(dec *json.Decoder) error {
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		if err == nil {
			return fmt.Errorf("unexpected trailing JSON value")
		}
		return err
	}
	return nil
}

func RequireObjectFields(fields map[string]json.RawMessage, field string, required ...string) error {
	raw, ok := fields[field]
	if !ok {
		return fmt.Errorf("missing required field %q", field)
	}
	var nested map[string]json.RawMessage
	if err := json.Unmarshal(raw, &nested); err != nil || nested == nil {
		return fmt.Errorf("field %q must be object", field)
	}
	for _, name := range required {
		if _, ok := nested[name]; !ok {
			return fmt.Errorf("missing required field %q", field+"."+name)
		}
	}
	return nil
}

func RejectPresentNonNullField(fields map[string]json.RawMessage, field, detail string) error {
	raw, ok := fields[field]
	if !ok || string(raw) == "null" {
		return nil
	}
	return errors.New(detail)
}

func ExtractTurnEnvelope(raw string) (string, bool, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &fields); err != nil {
		return "", false, err
	}
	turnRaw, ok := fields["turn"]
	if !ok {
		return raw, false, nil
	}
	if len(fields) != 1 {
		for field := range fields {
			if field != "turn" {
				return "", true, fmt.Errorf("unknown field %q", field)
			}
		}
	}
	var nested map[string]json.RawMessage
	if err := json.Unmarshal(turnRaw, &nested); err != nil || nested == nil {
		return "", true, fmt.Errorf("field %q must be object", "turn")
	}
	return string(turnRaw), true, nil
}

func WireActJSON(command string, workdir *string, reasoning *string) (string, error) {
	return marshalWireEnvelope(wireEnvelope{
		Turn: wireTurn{
			Type:      "act",
			Bash:      &wireBash{Command: command, Workdir: workdir},
			Question:  nil,
			Summary:   nil,
			Reasoning: derefString(reasoning),
		},
	})
}

func WireClarifyJSON(question string, reasoning *string) (string, error) {
	return marshalWireEnvelope(wireEnvelope{
		Turn: wireTurn{
			Type:      "clarify",
			Bash:      nil,
			Question:  question,
			Summary:   nil,
			Reasoning: derefString(reasoning),
		},
	})
}

func WireDoneJSON(summary string, reasoning *string) (string, error) {
	return marshalWireEnvelope(wireEnvelope{
		Turn: wireTurn{
			Type:      "done",
			Bash:      nil,
			Question:  nil,
			Summary:   summary,
			Reasoning: derefString(reasoning),
		},
	})
}

func MustWireActJSON(command string, workdir *string, reasoning *string) string {
	return mustWireJSON(WireActJSON(command, workdir, reasoning))
}

func MustWireClarifyJSON(question string, reasoning *string) string {
	return mustWireJSON(WireClarifyJSON(question, reasoning))
}

func MustWireDoneJSON(summary string, reasoning *string) string {
	return mustWireJSON(WireDoneJSON(summary, reasoning))
}

func marshalWireEnvelope(env wireEnvelope) (string, error) {
	body, err := json.Marshal(env)
	if err != nil {
		return "", err
	}
	return string(body), nil
}

func mustWireJSON(raw string, err error) string {
	if err != nil {
		panic(err)
	}
	return raw
}

func derefString(value *string) any {
	if value == nil {
		return nil
	}
	return *value
}
