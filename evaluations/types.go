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
