package clnkr

import (
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

type shellAnalysis struct {
	CaptureState bool
}

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
