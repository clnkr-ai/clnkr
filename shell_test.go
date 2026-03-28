package clnkr

import "testing"

func TestAnalyzeShell(t *testing.T) {
	t.Run("detects shell state builtins", func(t *testing.T) {
		analysis := analyzeShell(`echo hi && export FOO=bar && cd /tmp && source .venv/bin/activate`)
		if !analysis.CaptureState {
			t.Fatal("expected shell state capture to be enabled")
		}
	})
}
