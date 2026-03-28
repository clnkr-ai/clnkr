package clnkr

import (
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

type shellAnalysis struct {
	CaptureState bool
}

// analyzeShell inspects a shell command for state-mutating builtins and sets
// CaptureState when post-execution env/cwd capture is needed.
//
// Detection scope: ".", "cd", "export", "source", "unset". Indirect forms such
// as `eval "export FOO=bar"` and `declare -x VAR=val` are not detected; those
// would require evaluating the argument, not just the callee name.
func analyzeShell(command string) shellAnalysis {
	file, err := syntax.NewParser().Parse(strings.NewReader(command), "command")
	if err != nil {
		return shellAnalysis{}
	}
	analysis := shellAnalysis{}
	syntax.Walk(file, func(node syntax.Node) bool {
		switch x := node.(type) {
		case *syntax.CallExpr:
			if len(x.Args) == 0 {
				return true
			}
			switch x.Args[0].Lit() {
			case ".", "cd", "export", "source", "unset":
				analysis.CaptureState = true
			}
		}
		return true
	})
	return analysis
}
