package evaluations

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
)

// RunSuiteOption configures RunSuite behavior.
type RunSuiteOption func(*runSuiteOptions)

type runSuiteOptions struct {
	evalsDir   string
	outputDir  string
	binaryPath string
}

// WithSuiteEvalsDir overrides the default evaluations directory
// (repoRoot/evaluations).
func WithSuiteEvalsDir(path string) RunSuiteOption {
	return func(o *runSuiteOptions) {
		o.evalsDir = path
	}
}

// WithSuiteOutputDir overrides the default output directory for trials and
// reports (defaults to the evals dir).
func WithSuiteOutputDir(path string) RunSuiteOption {
	return func(o *runSuiteOptions) {
		o.outputDir = path
	}
}

// WithSuiteBinary overrides the clnku binary path for the harness.
func WithSuiteBinary(path string) RunSuiteOption {
	return func(o *runSuiteOptions) {
		o.binaryPath = path
	}
}

// RunSuite executes one evaluation suite and writes canonical trial bundles and run exports.
func RunSuite(ctx context.Context, repoRoot, suiteID string, cfg RunConfig, opts ...RunSuiteOption) (RunReport, error) {
	var o runSuiteOptions
	for _, opt := range opts {
		opt(&o)
	}

	evalsDir := o.evalsDir
	if evalsDir == "" {
		evalsDir = filepath.Join(repoRoot, "evaluations")
	}
	outputDir := o.outputDir
	if outputDir == "" {
		outputDir = evalsDir
	}

	suiteRoot := filepath.Join(evalsDir, "suites", suiteID)
	suite, err := LoadSuite(suiteRoot)
	if err != nil {
		return RunReport{}, fmt.Errorf("run suite load suite %q: %w", suiteID, err)
	}
	tasks, err := LoadSuiteTasks(suiteRoot, suite)
	if err != nil {
		return RunReport{}, fmt.Errorf("run suite load tasks for %q: %w", suiteID, err)
	}

	if err := resetOutputDir(filepath.Join(outputDir, "trials")); err != nil {
		return RunReport{}, fmt.Errorf("run suite reset trial output: %w", err)
	}
	if err := resetOutputDir(filepath.Join(outputDir, "reports")); err != nil {
		return RunReport{}, fmt.Errorf("run suite reset report output: %w", err)
	}

	var harnessOpts []HarnessOption
	if o.binaryPath != "" {
		harnessOpts = append(harnessOpts, WithBinary(o.binaryPath))
	}
	harnessOpts = append(harnessOpts, WithEvalsDir(evalsDir))
	harness, err := NewHarness(ctx, repoRoot, harnessOpts...)
	if err != nil {
		return RunReport{}, fmt.Errorf("run suite create harness: %w", err)
	}
	defer func() {
		_ = harness.Close()
	}()

	trialsRoot := filepath.Join(outputDir, "trials")
	reportsRoot := filepath.Join(outputDir, "reports")

	bundles := make([]Bundle, 0, len(tasks)*suite.TrialsPerTask)
	failedTasks := 0
	for taskIndex, task := range tasks {
		taskFailed := false
		for trialAttempt := 0; trialAttempt < suite.TrialsPerTask; trialAttempt++ {
			artifacts, err := harness.RunTrial(ctx, suite, task, cfg)
			if err != nil {
				return RunReport{}, fmt.Errorf("run suite trial task %q attempt %d: %w", task.ID, trialAttempt, err)
			}

			deterministicTrialID := canonicalTrialID(suite.ID, taskIndex, trialAttempt, task.ID)
			canonicalBundleRoot := filepath.Join(trialsRoot, deterministicTrialID)
			artifacts.TrialID = deterministicTrialID
			artifacts.TrialAttempt = trialAttempt

			bundle, err := WriteTrialBundle(canonicalBundleRoot, artifacts, artifacts.GraderResults)
			if err != nil {
				_ = os.RemoveAll(canonicalBundleRoot)
				return RunReport{}, fmt.Errorf("run suite write canonical bundle %q: %w", canonicalBundleRoot, err)
			}

			loaded, err := LoadBundle(canonicalBundleRoot)
			if err != nil {
				return RunReport{}, fmt.Errorf("run suite load canonical bundle %q: %w", canonicalBundleRoot, err)
			}
			bundles = append(bundles, loaded)

			if !bundle.TrialPassed {
				taskFailed = true
			}
		}

		if taskFailed {
			failedTasks++
		}
		if taskFailed && suite.FailurePolicy.StopOnFirstFailure {
			break
		}
		if failedTasks >= suite.FailurePolicy.MaxFailedTasks {
			break
		}
	}

	report, err := BuildRunReport(bundles)
	if err != nil {
		return RunReport{}, fmt.Errorf("run suite build report: %w", err)
	}
	if err := ExportOpenTestReport(report, filepath.Join(reportsRoot, "open-test-report.xml")); err != nil {
		return RunReport{}, fmt.Errorf("run suite export open test report: %w", err)
	}
	if err := ExportJUnit(report, filepath.Join(reportsRoot, "junit.xml")); err != nil {
		return RunReport{}, fmt.Errorf("run suite export junit: %w", err)
	}
	return report, nil
}

func resetOutputDir(path string) error {
	if err := os.RemoveAll(path); err != nil {
		return err
	}
	return os.MkdirAll(path, 0o755)
}

func canonicalTrialID(suiteID string, suiteTaskIndex, trialAttempt int, taskID string) string {
	return fmt.Sprintf("trial-%s-%03d-%02d-%s", suiteID, suiteTaskIndex, trialAttempt, taskID)
}
