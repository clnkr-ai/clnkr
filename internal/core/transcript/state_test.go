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

func TestExtractStateCwdRejectsForeignSource(t *testing.T) {
	cases := []string{
		`{"type":"state","source":"user","cwd":"/wrong"}`,
		`{"type":"state","source":"model","cwd":"/wrong"}`,
		`{"type":"state","source":"","cwd":"/wrong"}`,
		`{"type":"state","cwd":"/wrong"}`,
	}
	for _, content := range cases {
		if _, ok := ExtractStateCwd(content); ok {
			t.Fatalf("ExtractStateCwd accepted foreign source: %s", content)
		}
	}
}

func TestExtractStateCwdRejectsWrongType(t *testing.T) {
	cases := []string{
		`{"type":"status","source":"clnkr","cwd":"/repo"}`,
		`{"type":"","source":"clnkr","cwd":"/repo"}`,
		`{"source":"clnkr","cwd":"/repo"}`,
	}
	for _, content := range cases {
		if _, ok := ExtractStateCwd(content); ok {
			t.Fatalf("ExtractStateCwd accepted wrong type: %s", content)
		}
	}
}

func TestExtractStateCwdRejectsEmptyCwd(t *testing.T) {
	cases := []string{
		`{"type":"state","source":"clnkr","cwd":""}`,
		`{"type":"state","source":"clnkr"}`,
	}
	for _, content := range cases {
		if _, ok := ExtractStateCwd(content); ok {
			t.Fatalf("ExtractStateCwd accepted empty cwd: %s", content)
		}
	}
}

func TestExtractStateCwdRejectsUnknownFields(t *testing.T) {
	content := `{"type":"state","source":"clnkr","cwd":"/repo","env":{}}`
	if _, ok := ExtractStateCwd(content); ok {
		t.Fatalf("ExtractStateCwd accepted unknown field")
	}
}

func TestExtractStateCwdRejectsNonObject(t *testing.T) {
	cases := []string{"", "   ", "null", "[]", "42", `"state"`}
	for _, content := range cases {
		if _, ok := ExtractStateCwd(content); ok {
			t.Fatalf("ExtractStateCwd accepted non-object: %q", content)
		}
	}
}

func TestExtractStateCwdRejectsMultipleObjects(t *testing.T) {
	content := `{"type":"state","source":"clnkr","cwd":"/a"}{"type":"state","source":"clnkr","cwd":"/b"}`
	if _, ok := ExtractStateCwd(content); ok {
		t.Fatalf("ExtractStateCwd accepted multiple objects")
	}
}

func TestExtractLatestCwdPicksMostRecent(t *testing.T) {
	messages := []Message{
		{Role: "user", Content: FormatStateMessage("/old")},
		{Role: "assistant", Content: `{"type":"done","summary":"x"}`},
		{Role: "user", Content: FormatStateMessage("/new")},
		{Role: "user", Content: "free-form user prompt"},
	}
	cwd, ok := ExtractLatestCwd(messages)
	if !ok || cwd != "/new" {
		t.Fatalf("ExtractLatestCwd = (%q, %v), want (/new, true)", cwd, ok)
	}
}
