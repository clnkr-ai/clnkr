package clnkr

import (
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

type shellAnalysis struct {
	CaptureState bool
	WriteTargets []string
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
		case *syntax.Redirect:
			switch x.Op.String() {
			case ">", ">>", "&>", "&>>":
				if lit := x.Word.Lit(); lit != "" {
					analysis.WriteTargets = appendUnique(analysis.WriteTargets, lit)
				}
			}
		}
		return true
	})
	return analysis
}

func appendUnique(list []string, value string) []string {
	for _, existing := range list {
		if existing == value {
			return list
		}
	}
	return append(list, value)
}
