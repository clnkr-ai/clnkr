package evaluations

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// ExportJUnit writes a deterministic JUnit XML export from a run report.
func ExportJUnit(report RunReport, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return fmt.Errorf("export junit create dir: %w", err)
	}

	var buf bytes.Buffer
	buf.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	buf.WriteString(fmt.Sprintf(`<testsuites tests="%d" failures="%d" errors="0" skipped="0">`+"\n", report.TrialCount, report.Failed))
	for _, task := range report.Tasks {
		buf.WriteString(fmt.Sprintf(`  <testsuite name="%s" tests="%d" failures="%d" errors="0" skipped="0" time="%0.3f">`+"\n",
			xmlEscape(task.TaskID), task.TrialCount, task.Failed, taskDurationSeconds(task)))
		for _, trial := range task.Trials {
			buf.WriteString(fmt.Sprintf(`    <testcase classname="%s" name="%s" time="%0.3f">`+"\n",
				xmlEscape(report.SuiteID+"."+task.TaskID), xmlEscape(trial.TrialID), trialDurationSeconds(trial)))
			if !trial.Passed {
				message := trialFailureMessage(trial)
				buf.WriteString(fmt.Sprintf(`      <failure message="%s">`, xmlEscape(message)))
				buf.WriteString(xmlEscape(message))
				buf.WriteString(`</failure>` + "\n")
			}
			buf.WriteString("    </testcase>\n")
		}
		buf.WriteString("  </testsuite>\n")
	}
	buf.WriteString("</testsuites>\n")

	if err := os.WriteFile(dst, buf.Bytes(), 0o644); err != nil {
		return fmt.Errorf("export junit write %q: %w", dst, err)
	}
	return nil
}

func taskDurationSeconds(task TaskReport) float64 {
	var total float64
	for _, trial := range task.Trials {
		total += trialDurationSeconds(trial)
	}
	return total
}

func trialDurationSeconds(trial TrialReport) float64 {
	startedAt, err := time.Parse(bundleTimeLayout, trial.StartedAt)
	if err != nil {
		return 0
	}
	finishedAt, err := time.Parse(bundleTimeLayout, trial.FinishedAt)
	if err != nil || finishedAt.Before(startedAt) {
		return 0
	}
	return finishedAt.Sub(startedAt).Seconds()
}
