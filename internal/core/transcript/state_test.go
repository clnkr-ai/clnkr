package transcript

import "testing"

func TestFormatStateMessageEmitsPlainJSON(t *testing.T) {
	got := FormatStateMessage("/repo")
	want := `{"type":"state","source":"clnkr","cwd":"/repo"}`
	if got != want {
		t.Fatalf("FormatStateMessage = %q, want %q", got, want)
	}
}

func TestExtractStateCwdAcceptsCanonical(t *testing.T) {
	cwd, ok := ExtractStateCwd(`{"type":"state","source":"clnkr","cwd":"/repo"}`)
	if !ok {
		t.Fatalf("ExtractStateCwd ok = false, want true")
	}
	if cwd != "/repo" {
		t.Fatalf("cwd = %q, want %q", cwd, "/repo")
	}
}

func TestExtractStateCwdRejectsInvalidStateMessages(t *testing.T) {
	tests := []struct {
		name    string
		content string
	}{
		{name: "foreign user source", content: `{"type":"state","source":"user","cwd":"/wrong"}`},
		{name: "foreign model source", content: `{"type":"state","source":"model","cwd":"/wrong"}`},
		{name: "empty source", content: `{"type":"state","source":"","cwd":"/wrong"}`},
		{name: "missing source", content: `{"type":"state","cwd":"/wrong"}`},
		{name: "wrong type", content: `{"type":"status","source":"clnkr","cwd":"/repo"}`},
		{name: "empty type", content: `{"type":"","source":"clnkr","cwd":"/repo"}`},
		{name: "missing type", content: `{"source":"clnkr","cwd":"/repo"}`},
		{name: "empty cwd", content: `{"type":"state","source":"clnkr","cwd":""}`},
		{name: "missing cwd", content: `{"type":"state","source":"clnkr"}`},
		{
			name:    "unknown field",
			content: `{"type":"state","source":"clnkr","cwd":"/repo","env":{}}`,
		},
		{name: "empty string", content: ""},
		{name: "whitespace", content: "   "},
		{name: "null", content: "null"},
		{name: "array", content: "[]"},
		{name: "number", content: "42"},
		{name: "string", content: `"state"`},
		{
			name:    "multiple objects",
			content: `{"type":"state","source":"clnkr","cwd":"/a"}{"type":"state","source":"clnkr","cwd":"/b"}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, ok := ExtractStateCwd(tt.content); ok {
				t.Fatalf("ExtractStateCwd accepted invalid state message: %q", tt.content)
			}
		})
	}
}
