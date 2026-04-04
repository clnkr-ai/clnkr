package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/clnkr-ai/clnkr/evaluations"
)

var version = "dev"

const topLevelUsage = `clnkeval - evaluation runner for clnkr

Usage:
  clnkeval <command> [flags]

Commands:
  run     Run an evaluation suite
  init    Scaffold an evaluations/ directory

Flags:
  --help      Show this help
  --version   Print version

Run 'clnkeval <command> --help' for details on a specific command.
`

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
		_, _ = fmt.Fprint(stderr, topLevelUsage)
		return 1
	}

	switch args[0] {
	case "--help", "-h", "help":
		_, _ = fmt.Fprint(stdout, topLevelUsage)
		return 0
	case "--version", "-V", "version":
		_, _ = fmt.Fprintln(stdout, "clnkeval "+version)
		return 0
	case "run":
		return runSuite(args[1:], cwd, stdout, stderr, getenv)
	case "init":
		return runInit(args[1:], cwd, stdout, stderr)
	default:
		_, _ = fmt.Fprintf(stderr, "Error: unknown command %q\n\n", args[0])
		_, _ = fmt.Fprint(stderr, topLevelUsage)
		return 1
	}
}

func runSuite(args []string, cwd string, stdout, stderr io.Writer, getenv func(string) string) int {
	flags := flag.NewFlagSet("clnkeval run", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.Usage = func() {
		fmt.Fprint(stderr, `Usage: clnkeval run [flags]

Run an evaluation suite against the current directory.

Flags:
`)
		flags.PrintDefaults()
	}
	suiteID := flags.String("suite", "default", "suite id to run")
	binaryPath := flags.String("binary", "", "path to clnku binary (default: build from source, or resolve from PATH)")
	evalsDir := flags.String("evals-dir", "", "evaluations directory (default: <cwd>/evaluations)")
	outputDir := flags.String("output-dir", "", "output directory for trials and reports (default: evals dir)")
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

	var suiteOpts []evaluations.RunSuiteOption
	if *binaryPath != "" {
		suiteOpts = append(suiteOpts, evaluations.WithSuiteBinary(*binaryPath))
	}
	if *evalsDir != "" {
		abs, err := filepath.Abs(*evalsDir)
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "Error: resolving --evals-dir: %v\n", err)
			return 1
		}
		suiteOpts = append(suiteOpts, evaluations.WithSuiteEvalsDir(abs))
	}
	if *outputDir != "" {
		abs, err := filepath.Abs(*outputDir)
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "Error: resolving --output-dir: %v\n", err)
			return 1
		}
		suiteOpts = append(suiteOpts, evaluations.WithSuiteOutputDir(abs))
	}

	report, err := evaluations.RunSuite(context.Background(), cwd, *suiteID, cfg, suiteOpts...)
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

func runInit(args []string, cwd string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("clnkeval init", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.Usage = func() {
		fmt.Fprint(stderr, `Usage: clnkeval init

Scaffold an evaluations/ directory with a default suite and example task.
The directory must not already exist.
`)
	}
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

	evalsDir := filepath.Join(cwd, "evaluations")
	if _, err := os.Stat(evalsDir); err == nil {
		_, _ = fmt.Fprintf(stderr, "Error: evaluations/ directory already exists\n")
		return 1
	} else if !os.IsNotExist(err) {
		_, _ = fmt.Fprintf(stderr, "Error: %v\n", err)
		return 1
	}

	if err := evaluations.Init(evalsDir); err != nil {
		_, _ = fmt.Fprintf(stderr, "Error: %v\n", err)
		return 1
	}

	_, _ = fmt.Fprintf(stdout, "initialized evaluations/ with default suite and example task\n")
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
