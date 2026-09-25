package routing

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"time"
)

// Scope is the integration form a policy status applies to. The same CLI can
// be acceptable when a user runs it directly and unresolved when a product
// drives it automatically.
type Scope string

const (
	ScopePersonalInteractive Scope = "personal_interactive" // user explicitly picks the profile
	ScopePersonalAutomatic   Scope = "personal_automatic"   // PCD picks/escalates inside a user-started Run
	ScopeUnattended          Scope = "unattended"           // scheduler/CI without a user
	ScopeHostedMultiUser     Scope = "hosted_multi_user"
	ScopeOAuthReuseClient    Scope = "oauth_reuse_client"
)

type PolicyEntry struct {
	Status Status   `json:"status"`
	Refs   []string `json:"refs"`
	Note   string   `json:"note"`
}

type PolicyEvidence struct {
	ReviewedAt string                           `json:"reviewedAt"`
	Note       string                           `json:"note"`
	Adapters   map[string]map[Scope]PolicyEntry `json:"adapters"`
	Sources    map[string]string                `json:"sources"`
}

//go:embed policy_evidence.json
var policyJSON []byte

// LoadPolicyEvidence returns the maintainer-reviewed registry compiled into the
// binary. Users cannot override it from routing.json: a consent checkbox does
// not create an exception to a provider's terms.
func LoadPolicyEvidence() (PolicyEvidence, error) {
	var p PolicyEvidence
	if err := json.Unmarshal(policyJSON, &p); err != nil {
		return p, fmt.Errorf("policy evidence: %w", err)
	}
	for adapter, scopes := range p.Adapters {
		for scope, e := range scopes {
			if e.Status != PolicyAllowed && e.Status != PolicyReviewRequired && e.Status != PolicyBlocked {
				return p, fmt.Errorf("policy evidence: %s/%s has invalid status %q", adapter, scope, e.Status)
			}
		}
	}
	return p, nil
}

// For returns the reviewed status; anything missing is review-required.
func (p PolicyEvidence) For(adapter string, scope Scope) Observation {
	at, _ := time.Parse("2006-01-02", p.ReviewedAt)
	e, ok := p.Adapters[adapter][scope]
	if !ok {
		return Observation{Value: PolicyReviewRequired, Source: "policy_evidence", Detail: "no reviewed entry", Scope: string(scope), CheckedAt: at}
	}
	src := "policy_evidence"
	for _, r := range e.Refs {
		src += " " + r
	}
	return Observation{Value: e.Status, Source: src, Detail: e.Note, Scope: string(scope), CheckedAt: at}
}
