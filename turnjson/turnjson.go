package turnjson

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

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
