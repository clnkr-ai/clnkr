package clnkr

import (
	"strings"
	"testing"
)

func TestParseActProtocol(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want ActProtocol
	}{
		{name: "default", want: ActProtocolClnkrInline},
		{name: "trim lower", raw: " TOOL-CALLS ", want: ActProtocolToolCalls},
		{name: "inline", raw: "clnkr-inline", want: ActProtocolClnkrInline},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseActProtocol(tc.raw)
			if err != nil {
				t.Fatalf("ParseActProtocol(%q): %v", tc.raw, err)
			}
			if got != tc.want {
				t.Fatalf("ParseActProtocol(%q) = %q, want %q", tc.raw, got, tc.want)
			}
		})
	}
}

func TestParseActProtocolRejectsOldValues(t *testing.T) {
	for _, raw := range []string{"structured-json", "native-bash-tools", "text-json"} {
		t.Run(raw, func(t *testing.T) {
			_, err := ParseActProtocol(raw)
			if err == nil {
				t.Fatalf("ParseActProtocol(%q) succeeded", raw)
			}
			if !strings.Contains(err.Error(), `invalid act-protocol`) {
				t.Fatalf("error = %q, want act-protocol validation", err)
			}
			if !strings.Contains(err.Error(), "clnkr-inline, tool-calls") {
				t.Fatalf("error = %q, want allowed act protocol values", err)
			}
		})
	}
}
