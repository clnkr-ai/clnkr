package clnkr

import "testing"

func TestAnalyzeShell(t *testing.T) {
	t.Run("detects shell state builtins", func(t *testing.T) {
		analysis := analyzeShell(`echo hi && export FOO=bar && cd /tmp && source .venv/bin/activate`)
		if !analysis.CaptureState {
			t.Fatal("expected shell state capture to be enabled")
		}
	})

	t.Run("extracts write targets from redirects", func(t *testing.T) {
		analysis := analyzeShell(`cat > out.txt && printf hi >> out.txt && echo ok > log.txt`)
		if len(analysis.WriteTargets) != 2 {
			t.Fatalf("got %d write targets, want 2", len(analysis.WriteTargets))
		}
		if analysis.WriteTargets[0] != "out.txt" || analysis.WriteTargets[1] != "log.txt" {
			t.Fatalf("got write targets %v", analysis.WriteTargets)
		}
	})
}
