package hew

import "testing"

func TestExtractCommand(t *testing.T) {
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
			got, err := ExtractCommand(tt.input)
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

func TestExtractCommands(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    []string
		wantErr bool
	}{
		{
			name:  "single block",
			input: "Let me list the files\n\n```bash\nls -la\n```",
			want:  []string{"ls -la"},
		},
		{
			name:  "two blocks",
			input: "Step one:\n\n```bash\nls\n```\n\nStep two:\n\n```bash\ncat foo\n```",
			want:  []string{"ls", "cat foo"},
		},
		{
			name:  "three blocks",
			input: "```bash\necho a\n```\n\n```bash\necho b\n```\n\n```bash\necho c\n```",
			want:  []string{"echo a", "echo b", "echo c"},
		},
		{
			name:    "no code block",
			input:   "Just some text without any action",
			wantErr: true,
		},
		{
			name:    "empty code block only",
			input:   "Empty\n\n```bash\n```",
			wantErr: true,
		},
		{
			name:  "multiline command in block",
			input: "```bash\ncd /tmp &&\nls -la\n```\n\n```bash\necho done\n```",
			want:  []string{"cd /tmp &&\nls -la", "echo done"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ExtractCommands(tt.input)
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
			if len(got) != len(tt.want) {
				t.Fatalf("got %d commands, want %d", len(got), len(tt.want))
			}
			for i := range tt.want {
				if got[i] != tt.want[i] {
					t.Errorf("command[%d]: got %q, want %q", i, got[i], tt.want[i])
				}
			}
		})
	}
}
