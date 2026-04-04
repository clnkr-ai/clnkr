package evaluations

// Mode selects the evaluation trial mode.
type Mode string

const (
	// ModeMockProvider uses a local mock provider.
	ModeMockProvider Mode = "mock-provider"
	// ModeLiveProvider uses a real provider endpoint.
	ModeLiveProvider Mode = "live-provider"
)

// Suite describes an ordered collection of evaluation tasks.
type Suite struct {
	ID            string
	Description   string
	Mode          Mode
	TrialsPerTask int
	Tasks         []string
	FailurePolicy FailurePolicy
}

// FailurePolicy controls when a suite stops scheduling more tasks.
type FailurePolicy struct {
	StopOnFirstFailure bool
	MaxFailedTasks     int
}

// Task describes one evaluation task plus grading configuration.
type Task struct {
	ID                 string
	InstructionFile    string
	ScriptedTurnsFile  string
	WorkingDirectory   string
	StepLimit          int
	FullSend           bool
	SeedTranscriptFile string
	Mode               Mode
	Graders            GraderConfig
}

// TrialPolicyResult summarizes the first-wave required-grader pass policy.
type TrialPolicyResult struct {
	Passed                bool           `json:"passed"`
	FailedRequiredGraders []GraderResult `json:"failed_required_graders,omitempty"`
}

// GraderConfig groups the supported first-wave graders.
type GraderConfig struct {
	TranscriptCommandTrace   TranscriptCommandTraceConfig
	OutcomeWorkspaceSnapshot OutcomeWorkspaceSnapshotConfig
	OutcomeDiff              OutcomeDiffConfig
	OutcomeCommandOutput     OutcomeCommandOutputConfig
}

// OutcomeDiffConfig validates that the agent produced a non-empty git diff.
type OutcomeDiffConfig struct {
	Enabled  bool
	Required bool
}

// TranscriptCommandTraceConfig validates the command trace for a mock-provider trial.
type TranscriptCommandTraceConfig struct {
	Enabled           bool
	Required          bool
	ExpectedCommands  []string
	ExpectedExitCodes []int
	MaxCommandCount   int
}

// OutcomeWorkspaceSnapshotConfig validates the final workspace snapshot.
type OutcomeWorkspaceSnapshotConfig struct {
	Enabled  bool
	Required bool
}

// OutcomeCommandOutputConfig runs a command against the outcome workspace and checks its output.
type OutcomeCommandOutputConfig struct {
	Enabled              bool
	Required             bool
	Command              []string // e.g. ["go", "vet", "./..."] -- exec'd directly, no shell
	ExpectedExitCode     int      // default 0
	StdoutContains       []string // all must appear in stdout (order-independent)
	StderrMustNotContain []string // none may appear in stderr
	TimeoutSeconds       int      // 0 means use default (30s)
}
