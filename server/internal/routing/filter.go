package routing

import (
	"sync"
	"time"
)

// Reason codes are stable identifiers the UI translates. Each names exactly one
// gate so "not installed" is never shown as "policy blocked".
type Reason string

const (
	RNotInstalled        Reason = "not_installed"
	RWrongBinary         Reason = "wrong_binary"
	RNotAuthenticated    Reason = "not_authenticated"
	RAuthExpired         Reason = "auth_expired"
	RConsumerAuthEnded   Reason = "consumer_auth_discontinued"
	RAuthMethodMismatch  Reason = "auth_method_mismatch"
	RModelNotEntitled    Reason = "model_not_entitled"
	RModelNotListed      Reason = "model_not_listed"
	REffortUnsupported   Reason = "effort_unsupported"
	RBillingUnknown      Reason = "billing_unknown"
	RBillingNotAllowed   Reason = "billing_not_allowed"
	RExtraUsageRisk      Reason = "extra_usage_risk"
	RInheritedBillingEnv Reason = "inherited_billing_env"
	RNotImplemented      Reason = "not_implemented"
	RTechUnsupported     Reason = "technical_unsupported"
	RPolicyReview        Reason = "policy_review_required"
	RPolicyBlocked       Reason = "policy_blocked"
	RRateLimited         Reason = "rate_limited"
	RQuotaExhausted      Reason = "quota_exhausted"
	RHealthDegraded      Reason = "health_degraded"
	RQualityUnverified   Reason = "quality_unverified"
	RQualityFailed       Reason = "quality_failed"
	RNotAllowlisted      Reason = "not_allowlisted"
	RTierNotMapped       Reason = "tier_not_mapped"
	RNeedsTools          Reason = "needs_tools"
	RNeedsEdit           Reason = "needs_edit"
	RNeedsReadOnly       Reason = "needs_enforced_read_only"
	RContextTooSmall     Reason = "context_too_small"
	RPinned              Reason = "pinned_elsewhere"
	REndpointUnavailable Reason = "endpoint_unavailable"
)

// Exclusion is one failed gate with the evidence behind it.
type Exclusion struct {
	Reason Reason `json:"reason"`
	Detail string `json:"detail,omitempty"`
}

type Candidate struct {
	Profile  Profile     `json:"profile"`
	Excluded []Exclusion `json:"excluded,omitempty"`
	// Warnings are shown for an explicit user choice but do not block it
	// (unknown billing, extra-usage risk, policy review pending). The same
	// conditions are exclusions for automatic selection.
	Warnings   []Exclusion  `json:"warnings,omitempty"`
	Capability Capabilities `json:"capabilities"`
}

func (c Candidate) Eligible() bool { return len(c.Excluded) == 0 }

// Need is what the next attempt requires from any profile.
type Need struct {
	Kind          TaskKind
	MinTier       Tier
	ContextTokens int  // estimated input incl. handoff; 0 = unknown
	Automatic     bool // routing picks (ScopePersonalAutomatic) vs user picks
	PinProfile    string
	PinAdapter    string
}

// Health records transient per-bucket conditions learned from failures. Quota is
// shared across models in the same bucket: cooling one profile cools all of them.
type Health struct {
	mu      sync.Mutex
	buckets map[string]healthEntry
	now     func() time.Time
}

type healthEntry struct {
	status Status
	until  time.Time
	detail string
}

func NewHealth() *Health { return &Health{buckets: map[string]healthEntry{}, now: time.Now} }

func bucketOf(p Profile) string {
	if p.QuotaBucket != "" {
		return p.QuotaBucket
	}
	return p.Adapter + ":" + p.AccountRef
}

// Mark records a condition until the given time. Zero until means "until the
// next successful probe" (auth expiry, model denial).
func (h *Health) Mark(p Profile, status Status, until time.Time, detail string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.buckets[bucketOf(p)] = healthEntry{status: status, until: until, detail: detail}
}

func (h *Health) Clear(p Profile) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.buckets, bucketOf(p))
}

func (h *Health) Get(p Profile) (Status, string) {
	if h == nil {
		return Unknown, ""
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	e, ok := h.buckets[bucketOf(p)]
	if !ok {
		return Unknown, ""
	}
	if !e.until.IsZero() && h.now().After(e.until) {
		delete(h.buckets, bucketOf(p))
		return Unknown, ""
	}
	return e.status, e.detail
}

// Filter evaluates every gate for every profile and never short-circuits, so
// the UI can show all the reasons a profile is unavailable.
func Filter(cfg Config, statuses map[string]AdapterStatus, policy PolicyEvidence, health *Health, need Need) []Candidate {
	out := make([]Candidate, 0, len(cfg.Profiles))
	for _, p := range cfg.Profiles {
		c := Candidate{Profile: p, Capability: p.Effective()}
		add := func(r Reason, d string) { c.Excluded = append(c.Excluded, Exclusion{r, d}) }
		// autoOnly blocks automatic selection and warns on an explicit choice.
		autoOnly := func(r Reason, d string) {
			if need.Automatic {
				add(r, d)
			} else {
				c.Warnings = append(c.Warnings, Exclusion{r, d})
			}
		}
		st, known := statuses[p.Adapter]
		if p.Adapter == AdapterLocal {
			// Local endpoints are probed per endpoint; statuses are keyed "local:<id>".
			st, known = statuses[AdapterLocal+":"+p.EndpointRef]
		}
		if !known {
			st = AdapterStatus{AdapterID: p.Adapter, Installation: Observation{Value: Unknown}, Auth: Observation{Value: Unknown}}
		}
		switch st.Installation.Value {
		case Installed:
		case WrongBinary:
			add(RWrongBinary, st.Installation.Detail)
		case NotInstalled:
			add(RNotInstalled, st.Installation.Detail)
		default:
			if p.Adapter == AdapterLocal {
				add(REndpointUnavailable, st.Installation.Detail)
			} else {
				add(RNotInstalled, "installation not verified")
			}
		}
		switch st.Auth.Value {
		case Authenticated:
		case ConsumerAuthDiscontinued:
			add(RConsumerAuthEnded, st.Auth.Detail)
		case AuthExpired:
			add(RAuthExpired, st.Auth.Detail)
		case NotAuthenticated:
			add(RNotAuthenticated, st.Auth.Detail)
		default:
			if p.Adapter != AdapterLocal {
				add(RNotAuthenticated, "authentication not verified")
			}
		}
		if p.AuthMethod != "" && st.AuthMethod != "" && p.AuthMethod != st.AuthMethod {
			add(RAuthMethodMismatch, "profile expects "+p.AuthMethod+", CLI reports "+st.AuthMethod)
		}
		if st.Entitlement.Value == ModelNotEntitled {
			add(RModelNotEntitled, st.Entitlement.Detail)
		}
		if p.Model != "" && len(st.Models) > 0 {
			var found *ModelInfo
			for i := range st.Models {
				if st.Models[i].ID == p.Model {
					found = &st.Models[i]
				}
			}
			if found == nil {
				add(RModelNotListed, p.Model+" is not in the CLI's model list")
			} else if p.Effort != "" && len(found.Efforts) > 0 && !contains(found.Efforts, p.Effort) {
				add(REffortUnsupported, p.Effort+" is not supported by "+p.Model)
			}
		}
		if allowed := driverEfforts[p.Adapter]; p.Effort != "" && len(allowed) > 0 && !contains(allowed, p.Effort) {
			add(REffortUnsupported, p.Effort+" is not an effort the "+p.Adapter+" driver passes through")
		}
		caps := c.Capability
		if st.Technical.Value == NotImplemented || p.Adapter == AdapterGemini {
			add(RNotImplemented, "no execution adapter is connected for this path")
		} else if st.Technical.Value == Unsupported {
			add(RTechUnsupported, st.Technical.Detail)
		}
		if (p.Effort != "" && !caps.ForceEffort) || (p.Model != "" && !caps.ForceModel) {
			add(RTechUnsupported, "adapter cannot force the requested model/effort")
		}

		// Spend policy. Unknown billing is never assumed to be included.
		billing := p.Billing
		if billing == "" {
			billing = Unknown
		}
		if st.Billing.Value != "" && st.Billing.Value != Unknown && billing == Unknown {
			billing = st.Billing.Value
		}
		sp := cfg.Spend
		switch billing {
		case BillingSubscription, BillingLocal:
		case BillingAPIMetered:
			if !sp.AllowAPIMetered {
				add(RBillingNotAllowed, "metered API billing requires an explicit opt-in")
			}
		case BillingPlanCredits:
			if !sp.AllowPlanCredits {
				add(RBillingNotAllowed, "plan credits require an explicit opt-in")
			}
		case BillingPurchasedCredits:
			if !sp.AllowPurchasedCredits {
				add(RBillingNotAllowed, "purchased credits require an explicit opt-in")
			}
		case BillingPromoCredits:
			if !sp.AllowPromoCredits {
				add(RBillingNotAllowed, "promotional credits require an explicit opt-in")
			}
		default:
			if !sp.AllowUnknownBilling {
				autoOnly(RBillingUnknown, "billing path is not confirmed")
			}
		}
		if p.ExtraUsageRisk && !sp.AllowExtraUsageRisk {
			autoOnly(RExtraUsageRisk, "this path can bill extra usage/credits without a prompt")
		}
		if len(st.InheritedEnv) > 0 && billing != BillingAPIMetered {
			add(RInheritedBillingEnv, "server environment sets "+joinComma(st.InheritedEnv))
		}

		scope := ScopePersonalInteractive
		if need.Automatic {
			scope = ScopePersonalAutomatic
		}
		pol := policy.For(p.Adapter, scope)
		switch pol.Value {
		case PolicyAllowed:
		case PolicyBlocked:
			add(RPolicyBlocked, pol.Detail)
		default:
			// Manual selection of a review-pending path is preserved (existing
			// behavior) but still shown; automatic selection is not.
			autoOnly(RPolicyReview, pol.Detail)
		}

		if hs, d := health.Get(p); hs != Unknown {
			switch hs {
			case RateLimited:
				add(RRateLimited, d)
			case QuotaExhausted:
				add(RQuotaExhausted, d)
			case AuthExpired:
				add(RAuthExpired, d)
			case ModelNotEntitled:
				add(RModelNotEntitled, d)
			default:
				add(RHealthDegraded, d)
			}
		}

		switch need.Kind {
		case KindCode:
			if !caps.Tools {
				add(RNeedsTools, "task needs file/tool access")
			} else if !caps.EditFiles {
				add(RNeedsEdit, "task edits files")
			}
		case KindReview:
			if !caps.Tools {
				add(RNeedsTools, "review reads repository files")
			}
		}
		if need.ContextTokens > 0 && caps.ContextTokens > 0 && need.ContextTokens > caps.ContextTokens {
			add(RContextTooSmall, "required context does not fit")
		}
		q := p.Quality[need.Kind]
		if q.Status == QualityFailed {
			add(RQualityFailed, q.Evidence)
		}
		if need.Automatic {
			if !p.Allow {
				add(RNotAllowlisted, "not in the automatic-selection allowlist")
			}
			// Local/no-tool paths are enabled automatically only for task kinds with
			// recorded evaluation evidence.
			if p.Adapter == AdapterLocal && q.Status != QualityVerified {
				add(RQualityUnverified, "local path has no verified evaluation for "+string(need.Kind))
			}
			if need.MinTier != TierUnset && p.MaxTier() < need.MinTier {
				add(RTierNotMapped, "highest mapped tier "+p.MaxTier().String()+" < required "+need.MinTier.String())
			}
		}
		if need.PinProfile != "" && p.ID != need.PinProfile {
			add(RPinned, "run is pinned to profile "+need.PinProfile)
		} else if need.PinAdapter != "" && p.Adapter != need.PinAdapter {
			add(RPinned, "run is pinned to provider "+need.PinAdapter)
		}
		out = append(out, c)
	}
	return out
}

func contains(xs []string, v string) bool {
	for _, x := range xs {
		if x == v {
			return true
		}
	}
	return false
}

func joinComma(xs []string) string {
	s := ""
	for i, x := range xs {
		if i > 0 {
			s += ", "
		}
		s += x
	}
	return s
}
