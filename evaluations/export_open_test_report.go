package evaluations

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	openTestReportingCoreNS   = "https://schemas.opentest4j.org/reporting/core/0.2.0"
	openTestReportingEventsNS = "https://schemas.opentest4j.org/reporting/events/0.2.0"
)

// ExportOpenTestReport writes a deterministic Open Test Reporting XML export from a run report.
func ExportOpenTestReport(report RunReport, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return fmt.Errorf("export open test report create dir: %w", err)
	}

	var buf bytes.Buffer
	buf.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	buf.WriteString(`<e:events xmlns:c="` + openTestReportingCoreNS + `" xmlns:e="` + openTestReportingEventsNS + `">` + "\n")

	suiteStartedAt := reportStartedAt(report)
	suiteFinishedAt := reportFinishedAt(report)
	writeOTRStarted(&buf, 1, otrSuiteID(report.SuiteID), "suite "+report.SuiteID, "", suiteStartedAt)
	for _, task := range report.Tasks {
		taskID := otrTaskID(report.SuiteID, task.SuiteTaskIndex, task.TaskID)
		writeOTRStarted(&buf, 2, taskID, "task "+task.TaskID, otrSuiteID(report.SuiteID), taskStartedAt(task))
		for _, trial := range task.Trials {
			trialID := otrTrialID(report.SuiteID, task.SuiteTaskIndex, trial.TrialAttempt, trial.TrialID)
			writeOTRStarted(&buf, 3, trialID, "trial "+trial.TrialID, taskID, trial.StartedAt)
			if trial.Passed {
				writeOTRFinished(&buf, 3, trialID, true, "", trial.FinishedAt)
			} else {
				writeOTRFinished(&buf, 3, trialID, false, trialFailureMessage(trial), trial.FinishedAt)
			}
		}
		taskPassed := task.Failed == 0
		taskMessage := ""
		if !taskPassed {
			taskMessage = fmt.Sprintf("%d of %d trials failed", task.Failed, task.TrialCount)
		}
		writeOTRFinished(&buf, 2, taskID, taskPassed, taskMessage, taskFinishedAt(task))
	}

	suitePassed := report.Failed == 0
	suiteMessage := ""
	if !suitePassed {
		suiteMessage = fmt.Sprintf("%d of %d trials failed", report.Failed, report.TrialCount)
	}
	writeOTRFinished(&buf, 1, otrSuiteID(report.SuiteID), suitePassed, suiteMessage, suiteFinishedAt)
	buf.WriteString(`</e:events>` + "\n")

	if err := os.WriteFile(dst, buf.Bytes(), 0o644); err != nil {
		return fmt.Errorf("export open test report write %q: %w", dst, err)
	}
	return nil
}

func writeOTRStarted(buf *bytes.Buffer, indent int, id, name, parentID, timeValue string) {
	writeIndent(buf, indent)
	buf.WriteString(`<e:started id="` + xmlEscape(id) + `" name="` + xmlEscape(name) + `"`)
	if parentID != "" {
		buf.WriteString(` parentId="` + xmlEscape(parentID) + `"`)
	}
	if timeValue != "" {
		buf.WriteString(` time="` + xmlEscape(normalizeOTRTime(timeValue)) + `"`)
	}
	buf.WriteString("/>\n")
}

func writeOTRFinished(buf *bytes.Buffer, indent int, id string, passed bool, message, timeValue string) {
	writeIndent(buf, indent)
	buf.WriteString(`<e:finished id="` + xmlEscape(id) + `"`)
	if timeValue != "" {
		buf.WriteString(` time="` + xmlEscape(normalizeOTRTime(timeValue)) + `"`)
	}
	buf.WriteString(">\n")
	writeIndent(buf, indent+1)
	status := "FAILED"
	if passed {
		status = "SUCCESSFUL"
	}
	if message == "" {
		buf.WriteString(`<c:result status="` + status + `"/>` + "\n")
	} else {
		buf.WriteString(`<c:result status="` + status + `">`)
		buf.WriteString("\n")
		writeIndent(buf, indent+2)
		buf.WriteString(`<c:message>` + xmlEscape(message) + `</c:message>` + "\n")
		writeIndent(buf, indent+1)
		buf.WriteString(`</c:result>` + "\n")
	}
	writeIndent(buf, indent)
	buf.WriteString(`</e:finished>` + "\n")
}

func otrSuiteID(suiteID string) string {
	return "suite:" + suiteID
}

func otrTaskID(suiteID string, suiteTaskIndex int, taskID string) string {
	return "task:" + suiteID + ":" + fmt.Sprintf("%d", suiteTaskIndex) + ":" + taskID
}

func otrTrialID(suiteID string, suiteTaskIndex, trialAttempt int, trialID string) string {
	return "trial:" + suiteID + ":" + fmt.Sprintf("%d", suiteTaskIndex) + ":" + fmt.Sprintf("%d", trialAttempt) + ":" + trialID
}

func writeIndent(buf *bytes.Buffer, indent int) {
	if indent <= 0 {
		return
	}
	buf.WriteString(strings.Repeat("  ", indent))
}

func reportStartedAt(report RunReport) string {
	for _, task := range report.Tasks {
		if started := taskStartedAt(task); started != "" {
			return started
		}
	}
	return ""
}

func reportFinishedAt(report RunReport) string {
	for i := len(report.Tasks) - 1; i >= 0; i-- {
		if finished := taskFinishedAt(report.Tasks[i]); finished != "" {
			return finished
		}
	}
	return ""
}

func taskStartedAt(task TaskReport) string {
	if len(task.Trials) == 0 {
		return ""
	}
	return task.Trials[0].StartedAt
}

func taskFinishedAt(task TaskReport) string {
	if len(task.Trials) == 0 {
		return ""
	}
	return task.Trials[len(task.Trials)-1].FinishedAt
}

func normalizeOTRTime(value string) string {
	if value == "" {
		return ""
	}
	parsed, err := time.Parse(bundleTimeLayout, value)
	if err != nil {
		return value
	}
	return parsed.UTC().Format(bundleTimeLayout)
}
