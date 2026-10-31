package clnkr

import "strings"

type completionDecision string

const (
	completionAccept    completionDecision = "accept"
	completionReject    completionDecision = "reject"
	completionChallenge completionDecision = "challenge"
)

type completionGate struct{ challengesUsed int }

func (g *completionGate) decide(
	done DoneTurn,
	commandsUsed, maxSteps int,
) (completionDecision, []string, string) {
	if reasons := completionRejectReasons(done, commandsUsed, maxSteps); len(reasons) > 0 {
		return completionReject, reasons, completionRejectGuidance(reasons[0])
	}
	if reasons := completionChallengeReasons(done, commandsUsed); len(reasons) > 0 {
		if g.challengesUsed > 0 {
			return completionAccept, []string{"challenge_limit_reached"}, ""
		}
		g.challengesUsed++
		return completionChallenge, reasons, completionChallengeGuidance()
	}
	return completionAccept, nil, ""
}

func completionRejectReasons(done DoneTurn, commandsUsed, maxSteps int) []string {
	var reasons []string
	reasons = appendReason(
		reasons,
		containsAny(strings.ToLower(done.Summary), incompleteSummaryPhrases),
		"incomplete_summary",
	)
	if done.Verification.Status == VerificationNotVerified {
		reasons = appendReason(
			reasons,
			maxSteps <= 0 || commandsUsed < maxSteps,
			"not_verified_with_budget_remaining",
		)
	}
	return reasons
}

var incompleteSummaryPhrases = []string{
	"protocol correction",
	"cannot proceed",
	"need to continue",
	"ready to run",
	"no file changes have been made",
	"need create",
}

type completionChallengeRule struct {
	claims, checks []string
	reason         string
}

var completionChallengeRules = []completionChallengeRule{
	{
		[]string{"created ", "wrote ", "saved ", "/", ".go", ".md", ".json", ".txt"},
		[]string{"test -f", "cat ", "ls ", "stat ", "grep ", "exists", "contains"},
		"artifact_claim_without_check",
	},
	{
		[]string{"server", "service", "daemon", "listening"},
		[]string{"curl ", "nc ", "ss ", "lsof ", "listening", "responded"},
		"service_claim_without_liveness_check",
	},
	{
		[]string{"implemented", "fixed", "updated", "changed"},
		[]string{"test", "go test", "pytest", "npm test", "make check", "passed"},
		"implementation_claim_without_behavior_check",
	},
}

func completionChallengeReasons(done DoneTurn, commandsUsed int) []string {
	summary := strings.ToLower(done.Summary)
	checks := checksText(done.Verification.Checks)
	var reasons []string
	for _, rule := range completionChallengeRules {
		if containsAny(summary, rule.claims) && !containsAny(checks, rule.checks) {
			reasons = append(reasons, rule.reason)
		}
	}
	if commandsUsed == 0 && thinChecks(done.Verification.Checks) {
		reasons = append(reasons, "early_completion_thin_evidence")
	}
	return reasons
}

func checksText(checks []VerificationCheck) string {
	var b strings.Builder
	for _, check := range checks {
		b.WriteString(check.Command)
		b.WriteByte(' ')
		b.WriteString(check.Evidence)
		b.WriteByte(' ')
	}
	return strings.ToLower(b.String())
}

func containsAny(haystack string, needles []string) bool {
	for _, needle := range needles {
		if strings.Contains(haystack, needle) {
			return true
		}
	}
	return false
}

func appendReason(reasons []string, ok bool, reason string) []string {
	if ok {
		return append(reasons, reason)
	}
	return reasons
}

func thinChecks(checks []VerificationCheck) bool {
	if len(checks) != 1 {
		return len(checks) == 0
	}
	check := checks[0]
	combined := strings.TrimSpace(check.Command + " " + check.Outcome + " " + check.Evidence)
	return len(combined) < 40 || strings.EqualFold(strings.TrimSpace(check.Command), "true")
}

func completionChallengeGuidance() string {
	return "Completion challenged: verification evidence is too thin.\nRun one cheap check of the exact deliverable: file path/content, executable behavior, API signature, service liveness, or numeric threshold. If no meaningful check is possible, finish with partially_verified and list known_risks."
}

func completionRejectGuidance(reason string) string {
	return "Completion rejected: " + reason + ".\nRun a concrete verification check before finishing, or finish with partially_verified and known_risks when full verification is impossible."
}
