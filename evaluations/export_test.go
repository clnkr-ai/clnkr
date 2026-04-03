package evaluations

import (
	"encoding/xml"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestBuildRunReport(t *testing.T) {
	t.Run("aggregates loaded bundles deterministically", func(t *testing.T) {
		bundles := []Bundle{
			mustLoadedTrialBundle(t, trialBundleSpec{
				suiteID:        "suite-a",
				taskID:         "task-b",
				trialID:        "trial-c",
				suiteTaskIndex: 1,
				trialAttempt:   1,
				trialPassed:    true,
			}),
			mustLoadedTrialBundle(t, trialBundleSpec{
				suiteID:        "suite-a",
				taskID:         "task-a",
				trialID:        "trial-z",
				suiteTaskIndex: 0,
				trialAttempt:   0,
				trialPassed:    false,
				failedRequiredGraders: []GraderResult{
					{GraderID: "outcome_workspace_snapshot", TargetKind: "outcome", Passed: false, Message: "missing note.txt"},
				},
			}),
			mustLoadedTrialBundle(t, trialBundleSpec{
				suiteID:        "suite-a",
				taskID:         "task-b",
				trialID:        "trial-a",
				suiteTaskIndex: 1,
				trialAttempt:   0,
				trialPassed:    true,
			}),
			mustLoadedTrialBundle(t, trialBundleSpec{
				suiteID:        "suite-a",
				taskID:         "task-b",
				trialID:        "trial-b",
				suiteTaskIndex: 1,
				trialAttempt:   0,
				trialPassed:    true,
			}),
		}

		report, err := BuildRunReport(bundles)
		if err != nil {
			t.Fatalf("BuildRunReport(): %v", err)
		}
		if report.SuiteID != "suite-a" {
			t.Fatalf("suite id = %q, want %q", report.SuiteID, "suite-a")
		}
		if report.TaskCount != 2 {
			t.Fatalf("task count = %d, want 2", report.TaskCount)
		}
		if report.TrialCount != 4 {
			t.Fatalf("trial count = %d, want 4", report.TrialCount)
		}
		if report.Passed != 3 {
			t.Fatalf("passed count = %d, want 3", report.Passed)
		}
		if report.Failed != 1 {
			t.Fatalf("failed count = %d, want 1", report.Failed)
		}
		if len(report.Tasks) != 2 {
			t.Fatalf("tasks len = %d, want 2", len(report.Tasks))
		}
		if report.Tasks[0].TaskID != "task-a" || report.Tasks[0].SuiteTaskIndex != 0 {
			t.Fatalf("task[0] = %#v, want task-a/index0", report.Tasks[0])
		}
		if report.Tasks[1].TaskID != "task-b" || report.Tasks[1].SuiteTaskIndex != 1 {
			t.Fatalf("task[1] = %#v, want task-b/index1", report.Tasks[1])
		}
		if got := trialIDs(report.Tasks[1].Trials); !reflect.DeepEqual(got, []string{"trial-a", "trial-b", "trial-c"}) {
			t.Fatalf("task[1] trial ids = %#v, want [trial-a trial-b trial-c]", got)
		}
		if len(report.Tasks[0].Trials) != 1 || report.Tasks[0].Trials[0].BundlePath == "" {
			t.Fatalf("task[0] trial bundle path is empty: %#v", report.Tasks[0].Trials)
		}
	})

	t.Run("empty input rejected", func(t *testing.T) {
		if _, err := BuildRunReport(nil); err == nil {
			t.Fatal("BuildRunReport(nil) error = nil, want rejection")
		}
	})

	t.Run("inconsistent suite identity rejected", func(t *testing.T) {
		bundles := []Bundle{
			mustLoadedTrialBundle(t, trialBundleSpec{
				suiteID:        "suite-a",
				taskID:         "task-a",
				trialID:        "trial-a",
				suiteTaskIndex: 0,
				trialAttempt:   0,
				trialPassed:    true,
			}),
			mustLoadedTrialBundle(t, trialBundleSpec{
				suiteID:        "suite-b",
				taskID:         "task-a",
				trialID:        "trial-b",
				suiteTaskIndex: 0,
				trialAttempt:   1,
				trialPassed:    true,
			}),
		}

		if _, err := BuildRunReport(bundles); err == nil || !strings.Contains(err.Error(), "suite identity") {
			t.Fatalf("BuildRunReport() error = %v, want suite identity rejection", err)
		}
	})

	t.Run("counts use trial_passed rather than grader failure presence", func(t *testing.T) {
		bundles := []Bundle{
			mustLoadedTrialBundle(t, trialBundleSpec{
				suiteID:        "suite-a",
				taskID:         "task-a",
				trialID:        "trial-pass",
				suiteTaskIndex: 0,
				trialAttempt:   0,
				trialPassed:    true,
				failedRequiredGraders: []GraderResult{
					{GraderID: "transcript_command_trace", TargetKind: "transcript", Passed: false, Message: "compatibility failure"},
				},
			}),
			mustLoadedTrialBundle(t, trialBundleSpec{
				suiteID:        "suite-a",
				taskID:         "task-a",
				trialID:        "trial-fail",
				suiteTaskIndex: 0,
				trialAttempt:   1,
				trialPassed:    false,
				failedRequiredGraders: []GraderResult{
					{GraderID: "outcome_workspace_snapshot", TargetKind: "outcome", Passed: false, Message: "missing note.txt"},
				},
			}),
		}

		report, err := BuildRunReport(bundles)
		if err != nil {
			t.Fatalf("BuildRunReport(): %v", err)
		}
		if report.Passed != 1 || report.Failed != 1 {
			t.Fatalf("pass/fail counts = %d/%d, want 1/1", report.Passed, report.Failed)
		}
		if len(report.Tasks) != 1 || len(report.Tasks[0].Trials) != 2 {
			t.Fatalf("task/trial counts = %#v, want one task with two trials", report.Tasks)
		}
		if !report.Tasks[0].Trials[0].Passed {
			t.Fatalf("trial[0] passed = false, want true")
		}
		if len(report.Tasks[0].Trials[0].FailedRequiredGraders) != 1 {
			t.Fatalf("trial[0] failed graders = %#v, want preserved failure context", report.Tasks[0].Trials[0].FailedRequiredGraders)
		}
	})
}

func TestExportOpenTestReport(t *testing.T) {
	bundles := []Bundle{
		mustLoadedTrialBundle(t, trialBundleSpec{
			suiteID:        "suite-a",
			taskID:         "task-b",
			trialID:        "trial-c",
			suiteTaskIndex: 1,
			trialAttempt:   1,
			trialPassed:    true,
		}),
		mustLoadedTrialBundle(t, trialBundleSpec{
			suiteID:        "suite-a",
			taskID:         "task-a",
			trialID:        "trial-z",
			suiteTaskIndex: 0,
			trialAttempt:   0,
			trialPassed:    false,
			failedRequiredGraders: []GraderResult{
				{GraderID: "outcome_workspace_snapshot", TargetKind: "outcome", Passed: false, Message: "missing note.txt"},
			},
		}),
		mustLoadedTrialBundle(t, trialBundleSpec{
			suiteID:        "suite-a",
			taskID:         "task-b",
			trialID:        "trial-a",
			suiteTaskIndex: 1,
			trialAttempt:   0,
			trialPassed:    true,
		}),
	}

	report, err := BuildRunReport(bundles)
	if err != nil {
		t.Fatalf("BuildRunReport(): %v", err)
	}

	dst := filepath.Join(t.TempDir(), "nested", "reports", "open-test-report.xml")
	if err := ExportOpenTestReport(report, dst); err != nil {
		t.Fatalf("ExportOpenTestReport(): %v", err)
	}

	data, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("ReadFile(%q): %v", dst, err)
	}
	elems := parseXMLStartElements(t, string(data))
	if len(elems) < 8 {
		t.Fatalf("xml start element count = %d, want at least 8", len(elems))
	}
	assertOTRStartElement(t, elems[0], openTestReportingEventsNS, "events", map[string]string{})
	assertOTRStartElement(t, elems[1], openTestReportingEventsNS, "started", map[string]string{
		"id":   "suite:suite-a",
		"name": "suite suite-a",
		"time": "2026-03-31T10:00:00Z",
	})
	assertOTRStartElement(t, elems[2], openTestReportingEventsNS, "started", map[string]string{
		"id":       "task:suite-a:0:task-a",
		"name":     "task task-a",
		"parentId": "suite:suite-a",
		"time":     "2026-03-31T10:00:00Z",
	})
	assertOTRStartElement(t, elems[3], openTestReportingEventsNS, "started", map[string]string{
		"id":       "trial:suite-a:0:0:trial-z",
		"name":     "trial trial-z",
		"parentId": "task:suite-a:0:task-a",
		"time":     "2026-03-31T10:00:00Z",
	})
	assertOTRStartElement(t, elems[4], openTestReportingEventsNS, "finished", map[string]string{
		"id":   "trial:suite-a:0:0:trial-z",
		"time": "2026-03-31T10:00:02Z",
	})
	assertOTRStartElement(t, elems[5], openTestReportingCoreNS, "result", map[string]string{
		"status": "FAILED",
	})
	assertDeterministicElementOrder(t, elems, "started", "id", []string{"trial:suite-a:0:0:trial-z", "trial:suite-a:1:0:trial-a", "trial:suite-a:1:1:trial-c"})
	if !strings.Contains(string(data), "missing note.txt") || !strings.Contains(string(data), "outcome_workspace_snapshot") {
		t.Fatalf("report missing failure context: %q", string(data))
	}
}

func TestExportJUnit(t *testing.T) {
	bundles := []Bundle{
		mustLoadedTrialBundle(t, trialBundleSpec{
			suiteID:        "suite-a",
			taskID:         "task-b",
			trialID:        "trial-c",
			suiteTaskIndex: 1,
			trialAttempt:   1,
			trialPassed:    true,
		}),
		mustLoadedTrialBundle(t, trialBundleSpec{
			suiteID:        "suite-a",
			taskID:         "task-a",
			trialID:        "trial-z",
			suiteTaskIndex: 0,
			trialAttempt:   0,
			trialPassed:    false,
			failedRequiredGraders: []GraderResult{
				{GraderID: "outcome_workspace_snapshot", TargetKind: "outcome", Passed: false, Message: "missing note.txt"},
			},
		}),
		mustLoadedTrialBundle(t, trialBundleSpec{
			suiteID:        "suite-a",
			taskID:         "task-b",
			trialID:        "trial-a",
			suiteTaskIndex: 1,
			trialAttempt:   0,
			trialPassed:    true,
		}),
	}

	report, err := BuildRunReport(bundles)
	if err != nil {
		t.Fatalf("BuildRunReport(): %v", err)
	}

	dst := filepath.Join(t.TempDir(), "nested", "reports", "junit.xml")
	if err := ExportJUnit(report, dst); err != nil {
		t.Fatalf("ExportJUnit(): %v", err)
	}

	data, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("ReadFile(%q): %v", dst, err)
	}
	content := string(data)
	if !strings.Contains(content, "<testsuites") || !strings.Contains(content, "<testsuite") || !strings.Contains(content, "<testcase") {
		t.Fatalf("JUnit output missing expected elements: %q", content)
	}
	if !strings.Contains(content, "missing note.txt") || !strings.Contains(content, "outcome_workspace_snapshot") {
		t.Fatalf("JUnit output missing failure context: %q", content)
	}
	assertDeterministicElementOrder(t, parseXMLStartElements(t, content), "testcase", "name", []string{"trial-z", "trial-a", "trial-c"})
}

type trialBundleSpec struct {
	suiteID               string
	taskID                string
	trialID               string
	suiteTaskIndex        int
	trialAttempt          int
	trialPassed           bool
	failedRequiredGraders []GraderResult
}

func mustLoadedTrialBundle(t *testing.T, spec trialBundleSpec) Bundle {
	t.Helper()

	artifacts := sampleRunArtifacts(t)
	artifacts.SuiteID = spec.suiteID
	artifacts.TaskID = spec.taskID
	artifacts.TrialID = spec.trialID
	artifacts.SuiteTaskIndex = spec.suiteTaskIndex
	artifacts.TrialAttempt = spec.trialAttempt
	artifacts.TrialPassed = spec.trialPassed
	artifacts.FailedRequiredGraders = append([]GraderResult(nil), spec.failedRequiredGraders...)

	root := filepath.Join(t.TempDir(), spec.trialID)
	if _, err := WriteTrialBundle(root, artifacts, spec.failedRequiredGraders); err != nil {
		t.Fatalf("WriteTrialBundle(): %v", err)
	}
	bundle, err := LoadBundle(root)
	if err != nil {
		t.Fatalf("LoadBundle(): %v", err)
	}
	return bundle
}

func trialIDs(trials []TrialReport) []string {
	ids := make([]string, 0, len(trials))
	for _, trial := range trials {
		ids = append(ids, trial.TrialID)
	}
	return ids
}

func assertDeterministicElementOrder(t *testing.T, elems []xml.StartElement, elementLocal, attrName string, values []string) {
	t.Helper()

	last := -1
	for _, want := range values {
		idx := -1
		for i, elem := range elems {
			if elem.Name.Local != elementLocal {
				continue
			}
			if attrValue(elem.Attr, attrName) == want {
				idx = i
				break
			}
		}
		if idx < 0 {
			t.Fatalf("xml missing %s %q", elementLocal, want)
		}
		if idx < last {
			t.Fatalf("%s %q appeared out of order", elementLocal, want)
		}
		last = idx
	}
}

func parseXMLStartElements(t *testing.T, content string) []xml.StartElement {
	t.Helper()

	dec := xml.NewDecoder(strings.NewReader(content))
	var elems []xml.StartElement
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("parse xml token: %v", err)
		}
		if start, ok := tok.(xml.StartElement); ok {
			elems = append(elems, start)
		}
	}
	return elems
}

func assertOTRStartElement(t *testing.T, elem xml.StartElement, wantSpace, wantLocal string, wantAttrs map[string]string) {
	t.Helper()

	if elem.Name.Space != wantSpace || elem.Name.Local != wantLocal {
		t.Fatalf("element = {%q %q}, want {%q %q}", elem.Name.Space, elem.Name.Local, wantSpace, wantLocal)
	}
	for key, want := range wantAttrs {
		if got := attrValue(elem.Attr, key); got != want {
			t.Fatalf("attr %q = %q, want %q on element {%q %q}", key, got, want, elem.Name.Space, elem.Name.Local)
		}
	}
}

func attrValue(attrs []xml.Attr, name string) string {
	for _, attr := range attrs {
		if attr.Name.Local == name {
			return attr.Value
		}
	}
	return ""
}
