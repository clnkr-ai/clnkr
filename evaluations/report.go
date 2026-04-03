package evaluations

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"sort"
)

// TrialReport preserves the per-trial details needed by run-level exports.
type TrialReport struct {
	SuiteID               string         `json:"suite_id"`
	TaskID                string         `json:"task_id"`
	TrialID               string         `json:"trial_id"`
	BundlePath            string         `json:"bundle_path"`
	SuiteTaskIndex        int            `json:"suite_task_index"`
	TrialAttempt          int            `json:"trial_attempt"`
	Mode                  Mode           `json:"mode"`
	StartedAt             string         `json:"started_at"`
	FinishedAt            string         `json:"finished_at"`
	Passed                bool           `json:"passed"`
	FailedRequiredGraders []GraderResult `json:"failed_required_graders,omitempty"`
}

// TaskReport groups the trials for one suite task.
type TaskReport struct {
	SuiteTaskIndex int           `json:"suite_task_index"`
	TaskID         string        `json:"task_id"`
	TrialCount     int           `json:"trial_count"`
	Passed         int           `json:"passed"`
	Failed         int           `json:"failed"`
	Trials         []TrialReport `json:"trials"`
}

// RunReport summarizes one suite execution from loaded bundles only.
type RunReport struct {
	SuiteID    string       `json:"suite_id"`
	TaskCount  int          `json:"task_count"`
	TrialCount int          `json:"trial_count"`
	Passed     int          `json:"passed"`
	Failed     int          `json:"failed"`
	Tasks      []TaskReport `json:"tasks"`
}

// BuildRunReport assembles a deterministic run report from loaded bundles.
func BuildRunReport(bundles []Bundle) (RunReport, error) {
	if len(bundles) == 0 {
		return RunReport{}, fmt.Errorf("build run report: no trial bundles provided")
	}

	ordered := append([]Bundle(nil), bundles...)
	sort.SliceStable(ordered, func(i, j int) bool {
		if ordered[i].SuiteTaskIndex != ordered[j].SuiteTaskIndex {
			return ordered[i].SuiteTaskIndex < ordered[j].SuiteTaskIndex
		}
		if ordered[i].TrialAttempt != ordered[j].TrialAttempt {
			return ordered[i].TrialAttempt < ordered[j].TrialAttempt
		}
		return ordered[i].TrialID < ordered[j].TrialID
	})

	suiteID := ordered[0].SuiteID
	if suiteID == "" {
		return RunReport{}, fmt.Errorf("build run report: bundle %q missing suite id", ordered[0].Root)
	}

	report := RunReport{
		SuiteID: suiteID,
	}
	taskReports := make(map[int]*TaskReport)
	taskIndexByID := make(map[string]int)
	taskOrder := make([]int, 0, len(ordered))

	for _, bundle := range ordered {
		if bundle.SuiteID != suiteID {
			return RunReport{}, fmt.Errorf("build run report: inconsistent suite identity %q vs %q", bundle.SuiteID, suiteID)
		}
		if bundle.TaskID == "" {
			return RunReport{}, fmt.Errorf("build run report: bundle %q missing task id", bundle.Root)
		}
		if bundle.TrialID == "" {
			return RunReport{}, fmt.Errorf("build run report: bundle %q missing trial id", bundle.Root)
		}
		if index, ok := taskIndexByID[bundle.TaskID]; ok && index != bundle.SuiteTaskIndex {
			return RunReport{}, fmt.Errorf("build run report: task id %q maps to suite task indices %d and %d", bundle.TaskID, index, bundle.SuiteTaskIndex)
		}
		taskIndexByID[bundle.TaskID] = bundle.SuiteTaskIndex

		trial := TrialReport{
			SuiteID:               bundle.SuiteID,
			TaskID:                bundle.TaskID,
			TrialID:               bundle.TrialID,
			BundlePath:            bundle.Root,
			SuiteTaskIndex:        bundle.SuiteTaskIndex,
			TrialAttempt:          bundle.TrialAttempt,
			Mode:                  bundle.Mode,
			StartedAt:             bundle.StartedAt,
			FinishedAt:            bundle.FinishedAt,
			Passed:                bundle.TrialPassed,
			FailedRequiredGraders: append([]GraderResult(nil), bundle.FailedRequiredGraders...),
		}

		task, ok := taskReports[bundle.SuiteTaskIndex]
		if !ok {
			task = &TaskReport{
				SuiteTaskIndex: bundle.SuiteTaskIndex,
				TaskID:         bundle.TaskID,
			}
			taskReports[bundle.SuiteTaskIndex] = task
			taskOrder = append(taskOrder, bundle.SuiteTaskIndex)
		} else if task.TaskID != bundle.TaskID {
			return RunReport{}, fmt.Errorf("build run report: suite task index %d maps to %q and %q", bundle.SuiteTaskIndex, task.TaskID, bundle.TaskID)
		}

		task.Trials = append(task.Trials, trial)
	}

	sort.Ints(taskOrder)
	report.Tasks = make([]TaskReport, 0, len(taskOrder))
	for _, index := range taskOrder {
		task := taskReports[index]
		task.TrialCount = len(task.Trials)
		sort.SliceStable(task.Trials, func(i, j int) bool {
			if task.Trials[i].TrialAttempt != task.Trials[j].TrialAttempt {
				return task.Trials[i].TrialAttempt < task.Trials[j].TrialAttempt
			}
			return task.Trials[i].TrialID < task.Trials[j].TrialID
		})
		for _, trial := range task.Trials {
			report.TrialCount++
			if trial.Passed {
				task.Passed++
				report.Passed++
			} else {
				task.Failed++
				report.Failed++
			}
		}
		report.Tasks = append(report.Tasks, *task)
	}

	report.TaskCount = len(report.Tasks)
	return report, nil
}

func trialFailureMessage(trial TrialReport) string {
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
	return "required graders failed: " + joinParts(parts)
}

func joinParts(parts []string) string {
	switch len(parts) {
	case 0:
		return ""
	case 1:
		return parts[0]
	default:
		out := parts[0]
		for _, part := range parts[1:] {
			out += "; " + part
		}
		return out
	}
}

func xmlEscape(value string) string {
	var buf bytes.Buffer
	if err := xml.EscapeText(&buf, []byte(value)); err != nil {
		return value
	}
	return buf.String()
}
