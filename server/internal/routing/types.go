// Package routing selects an execution profile for a Run attempt, decides when a
// switch/escalation is allowed, and builds cross-provider handoff evidence.
//
// It is a policy package: it launches no process, reads no credentials and owns
// no HTTP. Provider processes stay in internal/providers; Run state stays in
// internal/orchestration. Tier names are PowerCodeDeck policy labels, not
// vendor rankings or price classes.
package routing

import (
	"fmt"
	"strings"
	"time"
)

// Tier is the minimum processing level a task needs. It is not a model name.
type Tier int

const (
	TierUnset Tier = iota
	VeryEasy
	Easy
	Medium
	High
	Ultra
)

var tierNames = [...]string{"", "VERY_EASY", "EASY", "MEDIUM", "HIGH", "ULTRA"}

func (t Tier) String() string {
	if t < TierUnset || t > Ultra {
		return "INVALID"
	}
	return tierNames[t]
}

func ParseTier(s string) (Tier, error) {
	s = strings.ToUpper(strings.TrimSpace(s))
	for i, name := range tierNames {
		if i > 0 && name == s {
			return Tier(i), nil
		}
	}
	return TierUnset, fmt.Errorf("unknown tier %q", s)
}

func (t Tier) MarshalText() ([]byte, error) { return []byte(t.String()), nil }
func (t *Tier) UnmarshalText(b []byte) error {
	if len(b) == 0 {
		*t = TierUnset
		return nil
	}
	v, err := ParseTier(string(b))
	*t = v
	return err
}

// Status values are deliberately strings: the UI and logs show them verbatim.
// "unknown" is always a legal value and is never coerced to ok/0/unlimited.
type Status string

const (
	Unknown Status = "unknown"

	// installation
	Installed    Status = "installed"
	NotInstalled Status = "not_installed"
	WrongBinary  Status = "wrong_binary" // e.g. an IDE launcher without agent mode

	// auth
	Authenticated            Status = "authenticated"
	NotAuthenticated         Status = "not_authenticated"
	AuthExpired              Status = "auth_expired"
	ConsumerAuthDiscontinued Status = "consumer_auth_discontinued"

	// entitlement
	Entitled         Status = "entitled"
	ModelNotEntitled Status = "model_not_entitled"

	// billing (what pays for a request on this path)
	BillingSubscription     Status = "subscription_included"
	BillingPlanCredits      Status = "plan_credits"
	BillingPurchasedCredits Status = "purchased_credits"
	BillingPromoCredits     Status = "promo_credits"
	BillingAPIMetered       Status = "api_metered"
	BillingLocal            Status = "local"

	// technical
	Supported      Status = "supported"
	Partial        Status = "partial"
	Unsupported    Status = "unsupported"
	NotImplemented Status = "not_implemented"

	// policy (reviewed integration scope, never an automated legal verdict)
	PolicyAllowed        Status = "allowed_for_scope"
	PolicyReviewRequired Status = "policy_review_required"
	PolicyBlocked        Status = "blocked"

	// health
	Healthy        Status = "healthy"
	RateLimited    Status = "rate_limited"
	QuotaExhausted Status = "quota_exhausted"
	Degraded       Status = "degraded"

	// quality (per task kind)
	QualityUnverified Status = "unverified"
	QualityVerified   Status = "verified"
	QualityFailed     Status = "failed"
)

// Observation is one dimension's value plus where it came from. CheckedAt zero
// means it was never observed in this process.
type Observation struct {
	Value     Status    `json:"value"`
	Source    string    `json:"source,omitempty"`
	Detail    string    `json:"detail,omitempty"`
	Scope     string    `json:"scope,omitempty"` // account/version/deployment the value applies to
	CheckedAt time.Time `json:"checkedAt,omitempty"`
}

func Observed(v Status, source, detail string, at time.Time) Observation {
	return Observation{Value: v, Source: source, Detail: detail, CheckedAt: at}
}

// AdapterStatus keeps every dimension separate. A single "available" boolean
// cannot distinguish "not installed" from "policy review pending".
type AdapterStatus struct {
	AdapterID    string      `json:"adapterId"`
	Executable   string      `json:"executable,omitempty"`
	Version      string      `json:"version,omitempty"`
	Installation Observation `json:"installation"`
	Auth         Observation `json:"auth"`
	AuthMethod   string      `json:"authMethod,omitempty"` // as reported by the CLI's own status command
	Entitlement  Observation `json:"entitlement"`
	Billing      Observation `json:"billing"`
	Technical    Observation `json:"technical"`
	Policy       Observation `json:"policy"`
	Health       Observation `json:"health"`
	// Models are what the CLI lists. Listing is not entitlement or inclusion
	// in a subscription.
	Models []ModelInfo `json:"models,omitempty"`
	// InheritedEnv names (never values) of environment variables that can
	// silently switch this CLI onto a metered billing path.
	InheritedEnv []string `json:"inheritedEnv,omitempty"`
	// ValidatedVersion is the version last marked re-verified. When it differs
	// from Version, capability/quality claims for this adapter are stale.
	ValidatedVersion  string `json:"validatedVersion,omitempty"`
	NeedsRevalidation bool   `json:"needsRevalidation,omitempty"`
}

type ModelInfo struct {
	ID            string   `json:"id"`
	DisplayName   string   `json:"displayName,omitempty"`
	Efforts       []string `json:"efforts,omitempty"`
	DefaultEffort string   `json:"defaultEffort,omitempty"`
	IsDefault     bool     `json:"isDefault,omitempty"`
	Vendor        string   `json:"vendor,omitempty"`
}

// TaskKind groups work by what it needs from a profile.
type TaskKind string

const (
	KindCode   TaskKind = "code"   // read/edit files, run tools
	KindReview TaskKind = "review" // read-only inspection
	KindText   TaskKind = "text"   // summarize/extract/classify/format, no tools
)
