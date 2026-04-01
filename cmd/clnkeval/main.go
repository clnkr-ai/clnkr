package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/clnkr-ai/clnkr/evaluations"
)

func main() {
	os.Exit(run(os.Args[1:], mustGetwd(), os.Stdout, os.Stderr, os.Getenv))
}

func run(args []string, cwd string, stdout, stderr io.Writer, getenv func(string) string) int {
	if stdout == nil {
		stdout = io.Discard
	}
	if stderr == nil {
		stderr = io.Discard
	}
	if getenv == nil {
		getenv = os.Getenv
	}
	if cwd == "" {
		cwd = "."
	}

	if len(args) == 0 {
		_, _ = fmt.Fprintln(stderr, "Error: missing subcommand. Supported subcommand: run")
		return 1
	}

	switch args[0] {
	case "run":
		return runSuite(args[1:], cwd, stdout, stderr, getenv)
	case "list-suites", "list-tasks", "validate":
		_, _ = fmt.Fprintf(stderr, "Error: subcommand %q is not available in the first wave; use 'run'\n", args[0])
		return 1
	default:
		_, _ = fmt.Fprintf(stderr, "Error: unsupported subcommand %q. Supported subcommand: run\n", args[0])
		return 1
	}
}

func runSuite(args []string, cwd string, stdout, stderr io.Writer, getenv func(string) string) int {
	flags := flag.NewFlagSet("clnkeval run", flag.ContinueOnError)
	flags.SetOutput(stderr)
	suiteID := flags.String("suite", "default", "")
	if err := flags.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return 0
		}
		_, _ = fmt.Fprintf(stderr, "Error: %v\n", err)
		return 1
	}
	if flags.NArg() != 0 {
		_, _ = fmt.Fprintf(stderr, "Error: unexpected arguments: %s\n", strings.Join(flags.Args(), " "))
		return 1
	}

	cfg, err := evaluations.LoadRunConfigFromEnv(getenv)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "Error: %v\n", err)
		return 1
	}

	report, err := evaluations.RunSuite(context.Background(), cwd, *suiteID, cfg)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "Error: %v\n", err)
		return 1
	}

	_, _ = fmt.Fprintf(stdout, "suite=%s tasks=%d trials=%d passed=%d failed=%d\n", report.SuiteID, report.TaskCount, report.TrialCount, report.Passed, report.Failed)
	for _, task := range report.Tasks {
		for _, trial := range task.Trials {
			if trial.Passed {
				continue
			}
			_, _ = fmt.Fprintf(stderr, "task=%s trial=%s %s\n", trial.TaskID, trial.TrialID, trialFailureMessage(trial))
		}
	}
	if report.Failed > 0 {
		return 1
	}
	return 0
}

func mustGetwd() string {
	cwd, err := os.Getwd()
	if err != nil {
		return "."
	}
	return cwd
}

func trialFailureMessage(trial evaluations.TrialReport) string {
	if len(trial.FailedRequiredGraders) == 0 {
		return "trial failed"
	}

	parts := make([]string, 0, len(trial.FailedRequiredGraders))
	for _, grader := range trial.FailedRequiredGraders {
		part := grader.GraderID
		if grader.TargetKind != "" {
			part += " (" + grader.TargetKind + ")"
		}
		if grader.Message != "" {
			part += ": " + grader.Message
		}
		parts = append(parts, part)
	}
	return "required graders failed: " + strings.Join(parts, "; ")
}
