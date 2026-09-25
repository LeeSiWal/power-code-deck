package routing

import (
	"regexp"
	"strconv"
	"strings"
	"time"
)

// AttemptReport is what the worker knows when an attempt ends. It is the only
// input to failure classification; nothing is inferred from chat text alone.
type AttemptReport struct {
	RunID             string       `json:"runId"`
	ExecutionID       string       `json:"executionId"`
	ProfileID         string       `json:"profileId"`
	Adapter           string       `json:"adapter"`
	Epoch             int64        `json:"epoch"`
	Canceled          bool         `json:"canceled"`
	ProviderStatus    string       `json:"providerStatus"` // success|failed|interrupted|unknown|not_started
	Detail            string       `json:"detail"`         // bounded provider text + diagnostics
	Denials           int          `json:"denials"`
	FailedChecks      []string     `json:"failedChecks,omitempty"`
	PassedChecks      []string     `json:"passedChecks,omitempty"`
	RunSucceeded      bool         `json:"runSucceeded"`
	EnvironmentErr    bool         `json:"environmentErr"` // failed before the provider ran
	QuiesceVerified   bool         `json:"quiesceVerified"`
	QuiesceDetail     string       `json:"quiesceDetail,omitempty"`
	ObservedModel     string       `json:"observedModel,omitempty"`
	Usage             *UsageRecord `json:"usage,omitempty"`
	Answer            string       `json:"answer,omitempty"` // untrusted model text, bounded
	Workspace         string       `json:"workspace,omitempty"`
	Reviewer          string       `json:"reviewer,omitempty"`
	ReviewerModel     string       `json:"reviewerModel,omitempty"`
	ReviewUsage       *UsageRecord `json:"reviewUsage,omitempty"`
	ReviewUnavailable bool         `json:"reviewUnavailable,omitempty"`
	Timings           Timings      `json:"timings"`
}

// UsageRecord keeps provider semantics: nil = not reported, never 0.
type UsageRecord struct {
	Scope          string `json:"scope"`  // turn | conversation (cumulative)
	Source         string `json:"source"` // reported | estimated
	InputTokens    *int   `json:"inputTokens"`
	OutputTokens   *int   `json:"outputTokens"`
	CacheRead      *int   `json:"cacheReadTokens"`
	CacheCreation  *int   `json:"cacheCreationTokens"`
	ThinkingTokens *int   `json:"thinkingTokens"`
	TotalTokens    *int   `json:"totalTokens"`
	Local          bool   `json:"local"`
}

// Timings decompose completion time; zero means not measured.
type Timings struct {
	RoutingMS int64 `json:"routingMs"`
	QueueMS   int64 `json:"queueMs"`
	HandoffMS int64 `json:"handoffMs"`
	ExecMS    int64 `json:"execMs"`
	VerifyMS  int64 `json:"verifyMs"`
}

var (
	rateRe        = regexp.MustCompile(`(?i)(\b429\b|rate[ _-]?limit|too many requests|usage limit|quota|resource[_ ]exhausted|overloaded)`)
	retryAfterRe  = regexp.MustCompile(`(?i)retry[- ]after[:= ]+(\d{1,6})`)
	authRe        = regexp.MustCompile(`(?i)(\b401\b|unauthori[sz]ed|not logged in|log ?in required|authentication (required|failed)|invalid[_ ]api[_ ]key|token (expired|revoked)|re-?authenticate)`)
	entitlementRe = regexp.MustCompile(`(?i)(model[^\n]{0,40}(not (available|found|supported)|access denied|not allowed|requires)|does not have access to model|\b403\b)`)
	envRe         = regexp.MustCompile(`(?i)(executable file not found|command not found|no such file or directory|git operation failed|workspace must be|uncommitted changes|source revision changed|cannot find module|module not found)`)
	permRe        = regexp.MustCompile(`(?i)(permission[_ ]denied|soft-denied|denied_actions|refus(ed|al) to|safety)`)
)

// Classify maps an attempt to one failure class. Ordering matters: a user
// cancel wins over everything, and an unverified quiescence is never treated as
// a clean quality failure.
func Classify(r AttemptReport) FailureClass {
	switch {
	case r.Canceled:
		return UserCanceled
	case r.RunSucceeded:
		return NoFailure
	case r.EnvironmentErr:
		return EnvironmentFail
	}
	if r.ReviewUnavailable {
		return ReviewBlocked
	}
	// The provider finished its turn and host checks judged the result: that is
	// quality evidence. Detail is not scanned here because it may contain the
	// model's own answer, which can mention "quota" or "401" legitimately.
	if r.ProviderStatus == "success" && r.Denials == 0 && len(r.FailedChecks) > 0 {
		if !r.QuiesceVerified {
			return UnknownSideEffect
		}
		return QualityFailure
	}
	d := r.Detail
	switch {
	case rateRe.MatchString(d):
		return AvailabilityFail
	case authRe.MatchString(d):
		return AuthFailure
	case entitlementRe.MatchString(d):
		return EntitlementFail
	case r.Denials > 0 || permRe.MatchString(d):
		return PermissionFail
	case envRe.MatchString(d):
		return EnvironmentFail
	}
	if r.ProviderStatus == "interrupted" || r.ProviderStatus == "unknown" || !r.QuiesceVerified {
		return UnknownSideEffect
	}
	if len(r.FailedChecks) > 0 {
		return QualityFailure
	}
	if r.ProviderStatus == "failed" {
		// A provider-reported failure without checks is not evidence that a
		// stronger model would succeed.
		return UnknownSideEffect
	}
	return UnknownSideEffect
}

// RetryAfter extracts a Retry-After hint in seconds, bounded to one day.
func RetryAfter(detail string) time.Duration {
	m := retryAfterRe.FindStringSubmatch(detail)
	if m == nil {
		return 0
	}
	n, _ := strconv.Atoi(m[1])
	if n > 86400 {
		n = 86400
	}
	return time.Duration(n) * time.Second
}

type ActionKind string

const (
	ActNone      ActionKind = "none"      // succeeded
	ActEscalate  ActionKind = "escalate"  // new attempt, higher floor
	ActFailover  ActionKind = "failover"  // new attempt, other allowed bucket
	ActWaitUser  ActionKind = "wait_user" // needs an explicit choice
	ActReconcile ActionKind = "reconcile" // inspect side effects first
	ActStop      ActionKind = "stop"      // no automatic continuation
)

type Action struct {
	Kind   ActionKind `json:"kind"`
	Reason string     `json:"reason"`
}

// Budget is the Run's routing history relevant to limits.
type Budget struct {
	Switches  int
	Attempts  int
	StartedAt time.Time
	Pinned    bool
}

// NextAction applies the switch policy. Only Auto mode with AutoEscalate
// continues without the user; every limit stops rather than loops.
func NextAction(mode Mode, pol SwitchPolicy, b Budget, class FailureClass, now time.Time) Action {
	switch class {
	case NoFailure:
		return Action{ActNone, "attempt succeeded"}
	case UserCanceled:
		return Action{ActStop, "canceled by user; never resumed or escalated automatically"}
	case PermissionFail:
		return Action{ActStop, "permission/safety denial; another model is not a way around it"}
	case EnvironmentFail:
		return Action{ActStop, "environment problem; fix installation/paths/dependencies before retrying"}
	case AuthFailure, EntitlementFail:
		return Action{ActWaitUser, "sign-in or model access must be fixed through the official CLI"}
	case ReviewBlocked:
		return Action{ActWaitUser, "no allowed reviewer profile; configure one or review manually — the review is not skipped"}
	case UnknownSideEffect:
		return Action{ActReconcile, "outcome or process shutdown is unverified; inspect workspace before any new writer"}
	}
	if mode != ModeAuto || !pol.AutoEscalate {
		return Action{ActWaitUser, "automatic continuation is disabled"}
	}
	if b.Pinned {
		return Action{ActWaitUser, "run is pinned; switching needs explicit confirmation"}
	}
	if b.Attempts >= pol.MaxAttemptsPerRun {
		return Action{ActWaitUser, "attempt budget exhausted"}
	}
	if b.Switches >= pol.MaxSwitchesPerRun {
		return Action{ActWaitUser, "switch budget exhausted"}
	}
	if !b.StartedAt.IsZero() && now.Sub(b.StartedAt) > time.Duration(pol.MaxRunMinutes)*time.Minute {
		return Action{ActWaitUser, "run time budget exhausted"}
	}
	if class == AvailabilityFail {
		return Action{ActFailover, "provider limit/outage; move to another allowed profile outside the exhausted quota bucket"}
	}
	return Action{ActEscalate, "checks failed with evidence; raise the tier floor"}
}

// Summarize bounds free text for records and router input.
func Summarize(s string, n int) string { return clip(Redact(strings.TrimSpace(s)), n) }
