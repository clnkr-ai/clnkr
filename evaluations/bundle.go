package evaluations

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime/debug"
	"sort"
	"strings"

	"github.com/clnkr-ai/clnkr"
)

const bundleSchemaVersion = "1"
const bundleTimeLayout = "2006-01-02T15:04:05.999999999Z07:00"

// Bundle is the canonical per-trial bundle metadata.
type Bundle struct {
	Root                  string            `json:"-"`
	SchemaVersion         string            `json:"schema_version"`
	ProducerVersion       string            `json:"producer_version"`
	SuiteID               string            `json:"suite_id"`
	TaskID                string            `json:"task_id"`
	TrialID               string            `json:"trial_id"`
	SuiteTaskIndex        int               `json:"suite_task_index"`
	TrialAttempt          int               `json:"trial_attempt"`
	Mode                  Mode              `json:"mode"`
	Provider              BundleProvider    `json:"provider"`
	StartedAt             string            `json:"started_at"`
	FinishedAt            string            `json:"finished_at"`
	TrialPassed           bool              `json:"trial_passed"`
	FailedRequiredGraders []GraderResult    `json:"failed_required_graders,omitempty"`
	Artifacts             BundleArtifacts   `json:"artifacts"`
	Checksums             map[string]string `json:"checksums"`
}

// BundleProvider stores the configured endpoint and model actually used for a trial.
type BundleProvider struct {
	BaseURL string `json:"base_url"`
	Model   string `json:"model"`
}

// BundleArtifacts stores bundle-relative artifact paths.
type BundleArtifacts struct {
	RawTranscript        string `json:"raw_transcript"`
	RawEvents            string `json:"raw_events"`
	RawProviderRequests  string `json:"raw_provider_requests"`
	RawProviderResponses string `json:"raw_provider_responses"`
	NormalizedTranscript string `json:"normalized_transcript"`
	NormalizedOutcome    string `json:"normalized_outcome"`
	NormalizedGraders    string `json:"normalized_graders"`
	OutcomeWorkspace     string `json:"outcome_workspace"`
}

// GraderResult is one normalized grader record.
type GraderResult struct {
	GraderID   string   `json:"grader_id"`
	TargetKind string   `json:"target_kind"`
	Passed     bool     `json:"passed"`
	Score      *float64 `json:"score,omitempty"`
	Evidence   any      `json:"evidence,omitempty"`
	Message    string   `json:"message,omitempty"`
}

// WriteTrialBundle writes the canonical per-trial bundle rooted at root.
func WriteTrialBundle(root string, artifacts RunArtifacts, graderResults []GraderResult) (Bundle, error) {
	if err := os.RemoveAll(root); err != nil {
		return Bundle{}, fmt.Errorf("remove existing bundle root %q: %w", root, err)
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return Bundle{}, fmt.Errorf("create bundle root %q: %w", root, err)
	}

	normalizedTranscript, err := NormalizeTranscript(artifacts)
	if err != nil {
		return Bundle{}, fmt.Errorf("write bundle normalize transcript: %w", err)
	}
	normalizedOutcome, err := NormalizeOutcome(artifacts, filepath.ToSlash("outcome/workspace"))
	if err != nil {
		return Bundle{}, fmt.Errorf("write bundle normalize outcome: %w", err)
	}

	artifactPaths := BundleArtifacts{
		RawTranscript:        "raw/transcript.json",
		RawEvents:            "raw/events.jsonl",
		RawProviderRequests:  "raw/provider-requests.jsonl",
		RawProviderResponses: "raw/provider-responses.jsonl",
		NormalizedTranscript: "normalized/transcript.jsonl",
		NormalizedOutcome:    "normalized/outcome.json",
		NormalizedGraders:    "normalized/graders.jsonl",
		OutcomeWorkspace:     "outcome/workspace",
	}

	writtenChecksums := map[string]string{}
	if err := writeFile(root, artifactPaths.RawTranscript, []byte(artifacts.Trajectory), writtenChecksums, true); err != nil {
		return Bundle{}, err
	}
	if err := writeFile(root, artifactPaths.RawEvents, []byte(artifacts.EventLog), writtenChecksums, true); err != nil {
		return Bundle{}, err
	}

	requestLines := make([]string, 0, len(artifacts.ProviderRequests))
	for _, request := range artifacts.ProviderRequests {
		if request.RawRequest == "" {
			continue
		}
		requestLines = append(requestLines, request.RawRequest)
	}
	if err := writeRawJSONL(root, artifactPaths.RawProviderRequests, requestLines); err != nil {
		return Bundle{}, fmt.Errorf("write provider requests: %w", err)
	}

	responseLines := make([]string, 0, len(artifacts.ProviderResponses))
	for _, response := range artifacts.ProviderResponses {
		if response == "" {
			continue
		}
		responseLines = append(responseLines, response)
	}
	if err := writeRawJSONL(root, artifactPaths.RawProviderResponses, responseLines); err != nil {
		return Bundle{}, fmt.Errorf("write provider responses: %w", err)
	}

	if err := writeJSONL(root, artifactPaths.NormalizedTranscript, normalizedTranscript, writtenChecksums, true); err != nil {
		return Bundle{}, fmt.Errorf("write normalized transcript: %w", err)
	}
	if err := writeJSON(root, artifactPaths.NormalizedOutcome, normalizedOutcome, writtenChecksums, true); err != nil {
		return Bundle{}, fmt.Errorf("write normalized outcome: %w", err)
	}
	if err := writeJSONL(root, artifactPaths.NormalizedGraders, graderResults, nil, false); err != nil {
		return Bundle{}, fmt.Errorf("write normalized graders: %w", err)
	}
	if err := materializeWorkspace(root, artifactPaths.OutcomeWorkspace, artifacts.Workspace); err != nil {
		return Bundle{}, fmt.Errorf("materialize workspace: %w", err)
	}

	bundle := Bundle{
		Root:            root,
		SchemaVersion:   bundleSchemaVersion,
		ProducerVersion: producerVersion(),
		SuiteID:         artifacts.SuiteID,
		TaskID:          artifacts.TaskID,
		TrialID:         artifacts.TrialID,
		SuiteTaskIndex:  artifacts.SuiteTaskIndex,
		TrialAttempt:    artifacts.TrialAttempt,
		Mode:            artifacts.Mode,
		Provider: BundleProvider{
			BaseURL: artifacts.ProviderBaseURL,
			Model:   artifacts.ProviderModel,
		},
		StartedAt:             artifacts.StartedAt.UTC().Format(bundleTimeLayout),
		FinishedAt:            artifacts.FinishedAt.UTC().Format(bundleTimeLayout),
		TrialPassed:           artifacts.TrialPassed,
		FailedRequiredGraders: append([]GraderResult(nil), artifacts.FailedRequiredGraders...),
		Artifacts:             artifactPaths,
		Checksums:             writtenChecksums,
	}
	if err := writeJSON(root, "bundle.json", bundle, nil, false); err != nil {
		return Bundle{}, fmt.Errorf("write bundle metadata: %w", err)
	}
	return bundle, nil
}

// LoadBundle eagerly parses bundle metadata and keeps artifact contents path-backed.
func LoadBundle(root string) (Bundle, error) {
	var bundle Bundle
	if err := decodeStrictJSONFile(filepath.Join(root, "bundle.json"), &bundle); err != nil {
		return Bundle{}, fmt.Errorf("load bundle metadata: %w", err)
	}
	bundle.Root = root

	for _, rel := range []string{
		bundle.Artifacts.RawTranscript,
		bundle.Artifacts.RawEvents,
		bundle.Artifacts.RawProviderRequests,
		bundle.Artifacts.RawProviderResponses,
		bundle.Artifacts.NormalizedTranscript,
		bundle.Artifacts.NormalizedOutcome,
		bundle.Artifacts.NormalizedGraders,
		bundle.Artifacts.OutcomeWorkspace,
	} {
		if err := validateBundleRelativePath(root, rel); err != nil {
			return Bundle{}, fmt.Errorf("load bundle validate artifact path %q: %w", rel, err)
		}
	}

	return bundle, nil
}

// ReadRawTranscript loads the raw transcript messages array on demand.
func (b Bundle) ReadRawTranscript() ([]clnkr.Message, error) {
	data, err := b.readArtifactFile(b.Artifacts.RawTranscript)
	if err != nil {
		return nil, fmt.Errorf("read raw transcript: %w", err)
	}
	var messages []clnkr.Message
	if err := json.Unmarshal(data, &messages); err != nil {
		return nil, fmt.Errorf("read raw transcript decode: %w", err)
	}
	return messages, nil
}

// ReadNormalizedTranscript loads normalized transcript records on demand.
func (b Bundle) ReadNormalizedTranscript() ([]NormalizedTranscriptRecord, error) {
	var records []NormalizedTranscriptRecord
	if err := readJSONLFile(b.Root, b.Artifacts.NormalizedTranscript, &records); err != nil {
		return nil, fmt.Errorf("read normalized transcript: %w", err)
	}
	return records, nil
}

// ReadNormalizedOutcome loads the normalized outcome on demand.
func (b Bundle) ReadNormalizedOutcome() (NormalizedOutcome, error) {
	data, err := b.readArtifactFile(b.Artifacts.NormalizedOutcome)
	if err != nil {
		return NormalizedOutcome{}, fmt.Errorf("read normalized outcome: %w", err)
	}
	var outcome NormalizedOutcome
	if err := json.Unmarshal(data, &outcome); err != nil {
		return NormalizedOutcome{}, fmt.Errorf("read normalized outcome decode: %w", err)
	}
	return outcome, nil
}

// ReadGraders loads normalized grader records on demand.
func (b Bundle) ReadGraders() ([]GraderResult, error) {
	var graders []GraderResult
	if err := readJSONLFile(b.Root, b.Artifacts.NormalizedGraders, &graders); err != nil {
		return nil, fmt.Errorf("read graders: %w", err)
	}
	return graders, nil
}

func (b Bundle) readArtifactFile(rel string) ([]byte, error) {
	path, err := bundleArtifactPath(b.Root, rel)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %q: %w", path, err)
	}
	return data, nil
}

func bundleArtifactPath(root, rel string) (string, error) {
	if err := validateBundleRelativePath(root, rel); err != nil {
		return "", err
	}
	return filepath.Join(root, filepath.FromSlash(rel)), nil
}

func validateBundleRelativePath(root, rel string) error {
	if strings.TrimSpace(rel) == "" {
		return fmt.Errorf("empty relative path")
	}
	if filepath.IsAbs(rel) {
		return fmt.Errorf("absolute path %q", rel)
	}

	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return fmt.Errorf("resolve bundle root %q: %w", root, err)
	}
	rootResolved, err := resolveExistingPath(rootAbs)
	if err != nil {
		return fmt.Errorf("resolve bundle root symlinks %q: %w", rootAbs, err)
	}
	targetAbs, err := filepath.Abs(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		return fmt.Errorf("resolve artifact path %q: %w", rel, err)
	}
	targetResolved, err := resolveExistingPath(targetAbs)
	if err != nil {
		return fmt.Errorf("resolve artifact symlinks %q: %w", rel, err)
	}
	if targetResolved != rootResolved && !strings.HasPrefix(targetResolved, rootResolved+string(filepath.Separator)) {
		return fmt.Errorf("path %q escapes bundle root", rel)
	}
	return nil
}

func writeFile(root, rel string, data []byte, checksums map[string]string, checksum bool) error {
	path, err := bundleArtifactPath(root, rel)
	if err != nil {
		return fmt.Errorf("write file path %q: %w", rel, err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("mkdir %q: %w", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write %q: %w", path, err)
	}
	if checksum && checksums != nil {
		checksums[rel] = checksumSHA256Bytes(data)
	}
	return nil
}

func writeJSON(root, rel string, value any, checksums map[string]string, checksum bool) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal %q: %w", rel, err)
	}
	data = append(data, '\n')
	return writeFile(root, rel, data, checksums, checksum)
}

func writeJSONL(root, rel string, records any, checksums map[string]string, checksum bool) error {
	var lines []string
	switch records := records.(type) {
	case []NormalizedTranscriptRecord:
		for _, record := range records {
			data, err := json.Marshal(record)
			if err != nil {
				return fmt.Errorf("marshal normalized transcript record: %w", err)
			}
			lines = append(lines, string(data))
		}
	case []GraderResult:
		for _, record := range records {
			data, err := json.Marshal(record)
			if err != nil {
				return fmt.Errorf("marshal grader record: %w", err)
			}
			lines = append(lines, string(data))
		}
	default:
		return fmt.Errorf("unsupported JSONL record type %T", records)
	}

	data := []byte(strings.Join(lines, "\n"))
	if len(lines) > 0 {
		data = append(data, '\n')
	}
	return writeFile(root, rel, data, checksums, checksum)
}

func materializeWorkspace(root, rel string, workspace map[string]string) error {
	workspaceRoot, err := bundleArtifactPath(root, rel)
	if err != nil {
		return fmt.Errorf("materialize workspace root %q: %w", rel, err)
	}
	if err := os.MkdirAll(workspaceRoot, 0o755); err != nil {
		return fmt.Errorf("mkdir workspace root %q: %w", workspaceRoot, err)
	}

	paths := make([]string, 0, len(workspace))
	for path := range workspace {
		paths = append(paths, path)
	}
	sort.Strings(paths)

	for _, path := range paths {
		targetRel := filepath.ToSlash(filepath.Join(rel, path))
		if err := writeFile(root, targetRel, []byte(workspace[path]), nil, false); err != nil {
			return err
		}
	}
	return nil
}

func writeRawJSONL(root, rel string, lines []string) error {
	var builder strings.Builder
	for _, line := range lines {
		builder.WriteString(line)
		if !strings.HasSuffix(line, "\n") {
			builder.WriteByte('\n')
		}
	}
	return writeFile(root, rel, []byte(builder.String()), nil, false)
}

func readJSONLFile(root, rel string, dst any) error {
	path, err := bundleArtifactPath(root, rel)
	if err != nil {
		return err
	}
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open %q: %w", path, err)
	}
	defer file.Close() //nolint:errcheck

	scanner := bufio.NewScanner(file)
	lines := [][]byte{}
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		lines = append(lines, []byte(line))
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("scan %q: %w", path, err)
	}

	switch dst := dst.(type) {
	case *[]NormalizedTranscriptRecord:
		records := make([]NormalizedTranscriptRecord, 0, len(lines))
		for _, line := range lines {
			var record NormalizedTranscriptRecord
			if err := json.Unmarshal(line, &record); err != nil {
				return fmt.Errorf("decode normalized transcript line: %w", err)
			}
			records = append(records, record)
		}
		*dst = records
	case *[]GraderResult:
		records := make([]GraderResult, 0, len(lines))
		for _, line := range lines {
			var record GraderResult
			if err := json.Unmarshal(line, &record); err != nil {
				return fmt.Errorf("decode grader line: %w", err)
			}
			records = append(records, record)
		}
		*dst = records
	default:
		return fmt.Errorf("unsupported JSONL destination %T", dst)
	}
	return nil
}

func producerVersion() string {
	info, ok := debug.ReadBuildInfo()
	if ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}
	return "dev"
}
