package hew

import "testing"

func TestParseAction(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{
			name:  "simple command",
			input: "Let me list the files\n\n```bash\nls -la\n```",
			want:  "ls -la",
		},
		{
			name:  "exit command",
			input: "All done!\n\n```bash\nexit\n```",
			want:  "exit",
		},
		{
			name:  "multiline command",
			input: "Running tests\n\n```bash\ncd /tmp &&\nls -la\n```",
			want:  "cd /tmp &&\nls -la",
		},
		{
			name:    "no code block",
			input:   "Just some text without any action",
			wantErr: true,
		},
		{
			name:    "empty code block",
			input:   "Empty\n\n```bash\n```",
			wantErr: true,
		},
		{
			name:  "text before and after",
			input: "I'll check.\n\n```bash\ncat main.go\n```\n\nThis shows the contents.",
			want:  "cat main.go",
		},
		{
			name:  "multiple blocks returns first",
			input: "Step one:\n\n```bash\nls\n```\n\nStep two:\n\n```bash\ncat foo\n```",
			want:  "ls",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseAction(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Error("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}
