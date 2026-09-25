package routing

import (
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"
)

// FailureClass separates why an attempt did not succeed. Only QualityFailure
// justifies escalating to a stronger profile.
type FailureClass string

const (
	NoFailure         FailureClass = ""
	QualityFailure    FailureClass = "quality"      // checks/review failed with evidence
	AvailabilityFail  FailureClass = "availability" // 429, quota, provider outage
	AuthFailure       FailureClass = "auth"         // expired/missing login
	EntitlementFail   FailureClass = "entitlement"  // model access denied
	EnvironmentFail   FailureClass = "environment"  // install/path/dependency/git problem
	PermissionFail    FailureClass = "permission"   // denied tool/safety refusal
	UnknownSideEffect FailureClass = "unknown_side_effect"
	UserCanceled      FailureClass = "canceled"
	// ReviewBlocked: the required review had no allowed reviewer profile. It
	// says nothing about the work's quality and never triggers escalation.
	ReviewBlocked FailureClass = "review_blocked"
)

type PriorAttempt struct {
	ExecutionID  string       `json:"executionId"`
	ProfileID    string       `json:"profileId"`
	Tier         Tier         `json:"tier"`
	Class        FailureClass `json:"class"`
	Summary      string       `json:"summary"` // bounded, redacted
	FailedChecks []string     `json:"failedChecks,omitempty"`
}

// Task is the bounded description routing sees. Goal is the verbatim user
// request; everything else is derived deterministically.
type Task struct {
	Goal        string         `json:"goal"`
	Stage       string         `json:"stage"` // initial | retry | escalation | handoff
	Kind        TaskKind       `json:"kind"`
	Files       []string       `json:"files,omitempty"`
	Constraints []string       `json:"constraints,omitempty"`
	Prior       []PriorAttempt `json:"prior,omitempty"`
}

var (
	pathRe       = regexp.MustCompile(`(?:^|[\s("'\x60])((?:[\w.-]+/)*[\w.-]+\.(?:go|ts|tsx|js|jsx|py|rs|java|kt|swift|c|cc|cpp|h|hpp|md|json|yaml|yml|toml|sql|sh|css|html))\b`)
	constraintRe = regexp.MustCompile(`(?i)(반드시|하지\s*마|금지|절대|유지하|must\b|must not|do not|don't|never\b|keep\b|without\b)`)
)

// DescribeTask builds a Task from the Run prompt. It never calls a model.
func DescribeTask(goal string, kind TaskKind, prior []PriorAttempt) Task {
	t := Task{Goal: goal, Kind: kind, Stage: "initial", Prior: prior}
	if len(prior) > 0 {
		t.Stage = "retry"
		if prior[len(prior)-1].Class == QualityFailure {
			t.Stage = "escalation"
		}
	}
	seen := map[string]bool{}
	for _, m := range pathRe.FindAllStringSubmatch(goal, 64) {
		if !seen[m[1]] {
			seen[m[1]] = true
			t.Files = append(t.Files, m[1])
		}
	}
	for _, line := range strings.FieldsFunc(goal, func(r rune) bool { return r == '\n' || r == '.' || r == '。' }) {
		line = strings.TrimSpace(line)
		if line != "" && constraintRe.MatchString(line) && len(t.Constraints) < 16 {
			t.Constraints = append(t.Constraints, clip(line, 240))
		}
	}
	return t
}

var (
	hardWords = regexp.MustCompile(`(?i)(architect|design|redesign|refactor|migration|migrate|concurren|race condition|deadlock|security|vulnerab|performance|optimi[sz]e|distributed|protocol|state machine|아키텍처|설계|리팩터|리팩토링|마이그레이션|동시성|교착|보안|취약|성능|최적화|분산|프로토콜|상태\s*머신)`)
	easyWords = regexp.MustCompile(`(?i)(typo|rename|format|lint|comment|docstring|readme|changelog|spelling|오타|이름\s*변경|포맷|주석|문서|철자)`)
)

// RuleTier is the conservative baseline ("B" in the comparison plan). It sets
// a floor; RouteLLM may only raise it within a configured pair.
func RuleTier(t Task) (Tier, []string) {
	tier, why := Medium, []string{"default MEDIUM"}
	n := utf8.RuneCountInString(t.Goal)
	hard := len(hardWords.FindAllString(t.Goal, -1))
	switch {
	case hard >= 2 && (n > 4000 || len(t.Files) >= 8):
		tier, why = Ultra, []string{fmt.Sprintf("%d hard signals with large scope", hard)}
	case hard >= 1 || n > 1500 || len(t.Files) >= 4:
		tier, why = High, []string{fmt.Sprintf("hard signals=%d chars=%d files=%d", hard, n, len(t.Files))}
	case easyWords.MatchString(t.Goal) && n < 80 && len(t.Files) <= 1:
		tier, why = VeryEasy, []string{"trivial edit keywords, very short request"}
	case easyWords.MatchString(t.Goal) && n < 300 && len(t.Files) <= 2:
		tier, why = Easy, []string{"easy edit keywords, short request"}
	}
	if t.Kind == KindText && tier > Medium {
		tier, why = Medium, append(why, "text-only task capped at MEDIUM")
	}
	// Evidence-based escalation: a quality failure raises the floor above the
	// tier that failed. Availability/env/permission failures never do.
	for _, p := range t.Prior {
		if p.Class == QualityFailure && p.Tier >= tier && p.Tier < Ultra {
			tier = p.Tier + 1
			why = append(why, "quality failure at "+p.Tier.String()+" ("+p.ExecutionID+")")
		}
	}
	return tier, why
}

// RouterText is the bounded input sent to RouteLLM: goal, stage, capabilities,
// files, constraints and prior failure evidence, not only the last sentence.
// Secrets are redacted and the result is capped at maxBytes.
func RouterText(t Task, maxBytes int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Task kind: %s. Stage: %s.\n", t.Kind, t.Stage)
	if len(t.Files) > 0 {
		fmt.Fprintf(&b, "Files: %s\n", strings.Join(t.Files, ", "))
	}
	for _, c := range t.Constraints {
		fmt.Fprintf(&b, "Constraint: %s\n", c)
	}
	for _, p := range t.Prior {
		fmt.Fprintf(&b, "Previous attempt at %s failed (%s): %s\n", p.Tier, p.Class, clip(p.Summary, 300))
	}
	b.WriteString("Goal:\n")
	b.WriteString(t.Goal)
	return clip(Redact(b.String()), maxBytes)
}

var secretPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\b(sk-[A-Za-z0-9_-]{16,}|sk-ant-[A-Za-z0-9_-]{16,}|AIza[0-9A-Za-z_-]{30,}|gh[pousr]_[A-Za-z0-9]{30,}|xox[baprs]-[A-Za-z0-9-]{10,}|AKIA[0-9A-Z]{16})\b`),
	regexp.MustCompile(`(?i)\b(api[_-]?key|secret|token|password|passwd|authorization)\s*[:=]\s*["']?[^\s"']{8,}`),
	regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----[\s\S]*?-----END [A-Z ]*PRIVATE KEY-----`),
	regexp.MustCompile(`(?i)\bbearer\s+[A-Za-z0-9._~+/-]{16,}=*`),
}

// Redact removes common credential shapes. It is a defense in depth for router
// input, handoff text and logs, not a guarantee that no secret remains.
func Redact(s string) string {
	for _, re := range secretPatterns {
		s = re.ReplaceAllString(s, "[REDACTED]")
	}
	return s
}

// clip truncates on a rune boundary and marks the cut.
func clip(s string, max int) string {
	if max <= 0 || len(s) <= max {
		return s
	}
	cut := max - len("…[truncated]")
	if cut < 0 {
		cut = 0
	}
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + "…[truncated]"
}

// followUpRe finds anything that names a target (a file, number, identifier or
// quoted text); a request without one cannot be rated on its own.
var followUpRe = regexp.MustCompile("[A-Za-z0-9._/'\"`]")

// IsFollowUp reports a short request that only refers to the previous one
// ("한번더 붙여줘", "다시 해줘", "계속 진행해줘"). Measured on 12 follow-up and
// scope cases: judging such a fragment alone rated "한번더 붙여줘" HARD; taking
// the previous request's rating got all 8 follow-ups right.
func IsFollowUp(goal string) bool {
	g := strings.TrimSpace(goal)
	return g != "" && utf8.RuneCountInString(g) <= 20 && !followUpRe.MatchString(g)
}
