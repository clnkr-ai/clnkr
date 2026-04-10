package turnjson

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
)

var escapedListMarker = regexp.MustCompile(`(?m)^\\([*-])\s`)
var inlineEscapedListMarker = regexp.MustCompile(`\\([*-])\s`)

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

type WireCommand struct {
	Command string  `json:"command"`
	Workdir *string `json:"workdir"`
}

type wireBash struct {
	Commands []WireCommand `json:"commands"`
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

func RequireNestedArrayObjectFields(fields map[string]json.RawMessage, field, nested string, required ...string) error {
	raw, ok := fields[field]
	if !ok {
		return fmt.Errorf("missing required field %q", field)
	}

	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil || obj == nil {
		return fmt.Errorf("field %q must be object", field)
	}

	arrayRaw, ok := obj[nested]
	if !ok {
		return fmt.Errorf("missing required field %q", field+"."+nested)
	}

	var items []map[string]json.RawMessage
	if err := json.Unmarshal(arrayRaw, &items); err != nil {
		return fmt.Errorf("field %q must be array", field+"."+nested)
	}

	for i, item := range items {
		if item == nil {
			return fmt.Errorf("field %q must be object", fmt.Sprintf("%s.%s[%d]", field, nested, i))
		}
		for _, name := range required {
			if _, ok := item[name]; !ok {
				return fmt.Errorf("missing required field %q", fmt.Sprintf("%s.%s[%d].%s", field, nested, i, name))
			}
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

func WireActJSON(commands []WireCommand, reasoning *string) (string, error) {
	return marshalWireEnvelope(wireEnvelope{
		Turn: wireTurn{
			Type:      "act",
			Bash:      &wireBash{Commands: commands},
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

func MustWireActJSON(commands []WireCommand, reasoning *string) string {
	return mustWireJSON(WireActJSON(commands, reasoning))
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

// NormalizeHumanText repairs model outputs that accidentally serialize
// user-facing turn text twice, leaving literal escape sequences like \n or \-.
func NormalizeHumanText(value string) string {
	if value == "" || !strings.Contains(value, `\`) {
		return value
	}

	normalized := value
	for i := 0; i < 2; i++ {
		decoded, ok := decodeEscapedHumanText(normalized)
		if !ok || decoded == normalized {
			break
		}
		normalized = decoded
	}

	normalized = escapedListMarker.ReplaceAllString(normalized, `$1 `)
	if strings.Contains(normalized, "\n- ") ||
		strings.Contains(normalized, "\n* ") ||
		strings.HasPrefix(normalized, "- ") ||
		strings.HasPrefix(normalized, "* ") {
		normalized = inlineEscapedListMarker.ReplaceAllString(normalized, "\n$1 ")
	}

	return normalized
}

func decodeEscapedHumanText(value string) (string, bool) {
	if !strings.Contains(value, `\n`) &&
		!strings.Contains(value, `\r`) &&
		!strings.Contains(value, `\\-`) &&
		!strings.Contains(value, `\\*`) &&
		!strings.Contains(value, `\"`) {
		return "", false
	}

	quoted := `"` + strings.ReplaceAll(value, `"`, `\"`) + `"`
	decoded, err := strconv.Unquote(quoted)
	if err != nil {
		return "", false
	}
	return decoded, true
}
