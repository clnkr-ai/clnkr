package clnkr

import "strings"

type CompletionDecisionKind string

const (
	CompletionAccept    CompletionDecisionKind = "accept"
	CompletionReject    CompletionDecisionKind = "reject"
	CompletionChallenge CompletionDecisionKind = "challenge"
)

type CompletionDecision struct {
	Kind     CompletionDecisionKind
	Reasons  []string
	Guidance string
}

type CompletionGate struct{ challengesUsed int }

func (g *CompletionGate) Decide(done *DoneTurn, commandsUsed, maxSteps int) CompletionDecision {
	if reasons := completionRejectReasons(done, commandsUsed, maxSteps); len(reasons) > 0 {
		return CompletionDecision{Kind: CompletionReject, Reasons: reasons, Guidance: completionRejectGuidance(reasons[0])}
	}
	if reasons := completionChallengeReasons(done, commandsUsed); len(reasons) > 0 {
		if g.challengesUsed > 0 {
			return CompletionDecision{Kind: CompletionAccept, Reasons: []string{"challenge_limit_reached"}}
		}
		g.challengesUsed++
		return CompletionDecision{Kind: CompletionChallenge, Reasons: reasons, Guidance: completionChallengeGuidance()}
	}
	return CompletionDecision{Kind: CompletionAccept}
}

func completionRejectReasons(done *DoneTurn, commandsUsed, maxSteps int) []string {
	if done == nil {
		return []string{"missing_done_turn"}
	}
	reasons := reasonIf(strings.TrimSpace(done.Summary) == "", "empty_summary")
	reasons = append(reasons, reasonIf(containsAny(strings.ToLower(done.Summary), incompleteSummaryPhrases), "incomplete_summary")...)
	switch done.Verification.Status {
	case VerificationVerified:
		reasons = append(reasons, reasonIf(len(done.Verification.Checks) == 0, "verified_without_checks")...)
	case VerificationPartiallyVerified:
		reasons = append(reasons, reasonIf(len(done.KnownRisks) == 0, "partial_without_risks")...)
	case VerificationNotVerified:
		reasons = append(reasons, reasonIf(maxSteps <= 0 || commandsUsed < maxSteps, "not_verified_with_budget_remaining")...)
	default:
		reasons = append(reasons, "invalid_verification_status")
	}
	return reasons
}

var incompleteSummaryPhrases = []string{"protocol correction", "cannot proceed", "need to continue", "ready to run", "no file changes have been made", "need create"}

func completionChallengeReasons(done *DoneTurn, commandsUsed int) []string {
	if done == nil {
		return nil
	}
	summary := strings.ToLower(done.Summary)
	checks := checksText(done.Verification.Checks)
	var reasons []string
	if containsAny(summary, []string{"created ", "wrote ", "saved ", "/", ".go", ".md", ".json", ".txt"}) &&
		!containsAny(checks, []string{"test -f", "cat ", "ls ", "stat ", "grep ", "exists", "contains"}) {
		reasons = append(reasons, "artifact_claim_without_check")
	}
	if containsAny(summary, []string{"server", "service", "daemon", "listening"}) &&
		!containsAny(checks, []string{"curl ", "nc ", "ss ", "lsof ", "listening", "responded"}) {
		reasons = append(reasons, "service_claim_without_liveness_check")
	}
	if containsAny(summary, []string{"implemented", "fixed", "updated", "changed"}) &&
		!containsAny(checks, []string{"test", "go test", "pytest", "npm test", "make check", "passed"}) {
		reasons = append(reasons, "implementation_claim_without_behavior_check")
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

func reasonIf(ok bool, reason string) []string {
	if ok {
		return []string{reason}
	}
	return nil
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
