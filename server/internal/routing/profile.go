package routing

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"sort"
	"strings"
)

// Adapter IDs. "gemini" is the optional enterprise/API compatibility path and
// is intentionally distinct from "antigravity"; they share no data identity.
const (
	AdapterClaude      = "claude"
	AdapterCodex       = "codex"
	AdapterAntigravity = "antigravity"
	AdapterGemini      = "gemini"
	AdapterLocal       = "local"
)

// Capabilities describe what PowerCodeDeck can actually drive through its
// adapter for this path, not everything the upstream CLI can do.
type Capabilities struct {
	Tools            bool   `json:"tools"`
	EditFiles        bool   `json:"editFiles"`
	ReadOnlyEnforced bool   `json:"readOnlyEnforced"` // OS/CLI-enforced, not prompt-requested
	MultiTurn        bool   `json:"multiTurn"`
	NativeResume     bool   `json:"nativeResume"`
	ApprovalBroker   bool   `json:"approvalBroker"`
	Interrupt        bool   `json:"interrupt"`
	ForceModel       bool   `json:"forceModel"`
	ForceEffort      bool   `json:"forceEffort"`
	ObservedModel    string `json:"observedModel"`   // "reported" | "none"
	SubcallsVisible  bool   `json:"subcallsVisible"` // can we see fallback/subagent models?
	ContextTokens    int    `json:"contextTokens,omitempty"`
}

// adapterFacts is the ceiling for each adapter, derived from the code in
// internal/providers and services/*_driver.go at the versions recorded in
// multimodel-routing-evidence.md. Config may only narrow these.
var adapterFacts = map[string]Capabilities{
	AdapterClaude:      {Tools: true, EditFiles: true, MultiTurn: true, NativeResume: true, ApprovalBroker: true, Interrupt: true, ForceModel: true, ForceEffort: true, ObservedModel: "reported"},
	AdapterCodex:       {Tools: true, EditFiles: true, MultiTurn: true, NativeResume: true, ApprovalBroker: true, Interrupt: true, ForceModel: true, ForceEffort: true, ObservedModel: "reported"},
	AdapterAntigravity: {Tools: true, EditFiles: true, NativeResume: true, Interrupt: true, ForceModel: true, ForceEffort: true, ObservedModel: "none"},
	AdapterGemini:      {},
	AdapterLocal:       {ForceModel: true, ObservedModel: "reported"},
}

// driverEfforts lists efforts an adapter's driver passes through unchanged when
// the CLI offers no model catalog. services.normalizeEffort coerces anything else
// for Claude, so a profile outside this list would silently run at another level.
var driverEfforts = map[string][]string{
	AdapterClaude:      {"low", "medium", "high", "xhigh", "max"},
	AdapterAntigravity: {"low", "medium", "high"},
}

// AdapterFacts returns the adapter ceiling (zero value for unknown adapters).
func AdapterFacts(adapter string) Capabilities { return adapterFacts[adapter] }

type QualityRecord struct {
	Status   Status `json:"status"`
	Evidence string `json:"evidence,omitempty"`
}

// Profile is one concrete way to run a model: adapter + model + effort + the
// account/billing path it runs under. The same model at two efforts is two
// profiles.
type Profile struct {
	ID               string                     `json:"id"`
	Adapter          string                     `json:"adapter"`
	Model            string                     `json:"model,omitempty"` // "" = the CLI's configured default
	ModelVendor      string                     `json:"modelVendor,omitempty"`
	Effort           string                     `json:"effort,omitempty"`
	AccountRef       string                     `json:"accountRef,omitempty"`
	AuthMethod       string                     `json:"authMethod,omitempty"` // expected: subscription | api_key | enterprise | local
	EntitlementOwner string                     `json:"entitlementOwner,omitempty"`
	BillingProvider  string                     `json:"billingProvider,omitempty"`
	Billing          Status                     `json:"billing,omitempty"`
	ExtraUsageRisk   bool                       `json:"extraUsageRisk,omitempty"` // may draw credits/extra usage without an interactive prompt
	QuotaBucket      string                     `json:"quotaBucket,omitempty"`
	Tiers            []Tier                     `json:"tiers,omitempty"`
	Capabilities     *Capabilities              `json:"capabilities,omitempty"` // optional narrowing
	Quality          map[TaskKind]QualityRecord `json:"quality,omitempty"`
	Allow            bool                       `json:"allow"` // user allowlist for automatic selection
	EndpointRef      string                     `json:"endpointRef,omitempty"`
	// Generated marks a profile synthesized from discovery rather than config.
	Generated bool `json:"generated,omitempty"`
	// Roles this profile may serve. Empty means executor+reviewer (the meaning
	// profiles had before roles existed). "decider" is never implied.
	Roles []Role `json:"roles,omitempty"`
}

// Role separates what a profile is used for. One profile may hold several
// roles; roles never require separate subscriptions.
type Role string

const (
	RoleDecider    Role = "decider"
	RoleExecutor   Role = "executor"
	RoleReviewer   Role = "reviewer"
	RolePlanner    Role = "planner"
	RoleSummarizer Role = "summarizer"
)

func validRole(r Role) bool {
	switch r {
	case RoleDecider, RoleExecutor, RoleReviewer, RolePlanner, RoleSummarizer:
		return true
	}
	return false
}

// Has reports whether the profile may serve role r.
func (p Profile) Has(r Role) bool {
	if len(p.Roles) == 0 {
		return r == RoleExecutor || r == RoleReviewer
	}
	for _, x := range p.Roles {
		if x == r {
			return true
		}
	}
	return false
}

// Identity is what makes two profiles the same executor. Profiles that differ
// only by id are one candidate, not two.
func (p Profile) Identity() string {
	bucket := p.QuotaBucket
	if bucket == "" {
		bucket = p.Adapter + ":" + p.AccountRef
	}
	return p.Adapter + "|" + p.Model + "|" + p.Effort + "|" + bucket + "|" + p.EndpointRef
}

// Effective returns adapter facts narrowed by the profile's own declaration.
func (p Profile) Effective() Capabilities {
	c := adapterFacts[p.Adapter]
	if p.Capabilities == nil {
		return c
	}
	n := *p.Capabilities
	and := func(a, b bool) bool { return a && b }
	c.Tools, c.EditFiles = and(c.Tools, n.Tools), and(c.EditFiles, n.EditFiles)
	c.ReadOnlyEnforced = and(c.ReadOnlyEnforced, n.ReadOnlyEnforced)
	c.MultiTurn, c.NativeResume = and(c.MultiTurn, n.MultiTurn), and(c.NativeResume, n.NativeResume)
	c.ApprovalBroker, c.Interrupt = and(c.ApprovalBroker, n.ApprovalBroker), and(c.Interrupt, n.Interrupt)
	c.ForceModel, c.ForceEffort = and(c.ForceModel, n.ForceModel), and(c.ForceEffort, n.ForceEffort)
	c.SubcallsVisible = and(c.SubcallsVisible, n.SubcallsVisible)
	if n.ContextTokens > 0 && (c.ContextTokens == 0 || n.ContextTokens < c.ContextTokens) {
		c.ContextTokens = n.ContextTokens
	}
	return c
}

func (p Profile) Serves(t Tier) bool {
	for _, x := range p.Tiers {
		if x == t {
			return true
		}
	}
	return false
}

// MaxTier is the highest tier the profile is mapped to (TierUnset if none).
func (p Profile) MaxTier() Tier {
	m := TierUnset
	for _, t := range p.Tiers {
		if t > m {
			m = t
		}
	}
	return m
}

type Mode string

const (
	ModeOff    Mode = "off"
	ModeManual Mode = "manual"
	ModeShadow Mode = "shadow"
	ModeAuto   Mode = "auto"
)

func ValidMode(m Mode) bool {
	return m == ModeOff || m == ModeManual || m == ModeShadow || m == ModeAuto
}

// SpendPolicy defaults to "subscription only": no metered API, no credits, no
// overage. Every relaxation is an explicit, separately named opt-in.
type SpendPolicy struct {
	AllowAPIMetered       bool `json:"allowApiMetered"`
	AllowPlanCredits      bool `json:"allowPlanCredits"`
	AllowPurchasedCredits bool `json:"allowPurchasedCredits"`
	AllowPromoCredits     bool `json:"allowPromoCredits"`
	AllowExtraUsageRisk   bool `json:"allowExtraUsageRisk"`
	AllowUnknownBilling   bool `json:"allowUnknownBilling"`
	// BudgetKind names what the budget can actually guarantee. PowerCodeDeck can
	// only stop launching work (best_effort/soft); it cannot reverse usage a
	// provider already recorded.
	BudgetKind string `json:"budgetKind,omitempty"` // best_effort | soft
}

type SwitchPolicy struct {
	MaxSwitchesPerRun int  `json:"maxSwitchesPerRun"`
	MaxAttemptsPerRun int  `json:"maxAttemptsPerRun"`
	MaxRunMinutes     int  `json:"maxRunMinutes"`
	AutoEscalate      bool `json:"autoEscalate"`
	// Stickiness is added to the current profile's score so near-ties do not
	// flip providers every attempt.
	Stickiness float64 `json:"stickiness"`
}

type RouteLLMConfig struct {
	Enabled   bool         `json:"enabled"`
	URL       string       `json:"url,omitempty"`
	Router    string       `json:"router,omitempty"`
	TimeoutMS int          `json:"timeoutMs,omitempty"`
	TokenEnv  string       `json:"tokenEnv,omitempty"` // only for an explicitly configured non-loopback sidecar
	Pairs     []RouterPair `json:"pairs,omitempty"`
}

// RouterPair binds a RouteLLM threshold to one concrete (weak, strong) profile
// pair. Scores from different pairs are never compared with each other.
type RouterPair struct {
	Weak        string     `json:"weak"`
	Strong      string     `json:"strong"`
	Threshold   float64    `json:"threshold"`
	Calibration string     `json:"calibration,omitempty"` // "uncalibrated" unless an eval backs it
	Kinds       []TaskKind `json:"kinds,omitempty"`
}

type Config struct {
	Version        int             `json:"version"`
	Mode           Mode            `json:"mode"`
	Preference     string          `json:"preference,omitempty"` // quality_first (default)
	Spend          SpendPolicy     `json:"spend"`
	Switching      SwitchPolicy    `json:"switching"`
	Profiles       []Profile       `json:"profiles"`
	LocalEndpoints []LocalEndpoint `json:"localEndpoints,omitempty"`
	// TierJudge (optional): a local model rates request difficulty; the
	// keyword rules remain the fallback. See judge.go.
	TierJudge *TierJudgeConfig `json:"tierJudge,omitempty"`
	// FreshStart (optional): after a pause, a long auto session continues in
	// a new conversation that starts from a handoff memo. See fresh.go.
	FreshStart *FreshStartConfig `json:"freshStart,omitempty"`
	RouteLLM   RouteLLMConfig    `json:"routellm"`
	// Strategy settles ambiguous choices. Empty keeps the old meaning:
	// routellm when routellm.enabled, otherwise rules.
	Strategy Strategy `json:"strategy,omitempty"`
	// RolePins fixes the profile for an auxiliary role (reviewer, planner,
	// decider is pinned via decider.profile).
	RolePins map[Role]string `json:"rolePins,omitempty"`
	Decider  DeciderConfig   `json:"decider,omitempty"`
}

// EffectiveStrategy resolves the default.
func (c Config) EffectiveStrategy() Strategy {
	if c.Strategy != "" {
		return c.Strategy
	}
	if c.RouteLLM.Enabled {
		return StrategyRouteLLM
	}
	return StrategyRules
}

var idPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)
var modelPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/\[\]-]{0,127}$`)

// DefaultConfig is what an absent routing.json means: routing off, strict spend.
func DefaultConfig() Config {
	return Config{Version: 1, Mode: ModeOff, Preference: "quality_first",
		Spend:     SpendPolicy{BudgetKind: "best_effort"},
		Switching: SwitchPolicy{MaxSwitchesPerRun: 2, MaxAttemptsPerRun: 3, MaxRunMinutes: 90, Stickiness: 0.5}}
}

func LoadConfig(path string) (Config, error) {
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return DefaultConfig(), nil
	}
	if err != nil {
		return Config{}, err
	}
	defer f.Close()
	return ParseConfig(io.LimitReader(f, 1<<20))
}

func ParseConfig(r io.Reader) (Config, error) {
	c := DefaultConfig()
	d := json.NewDecoder(r)
	d.DisallowUnknownFields()
	if err := d.Decode(&c); err != nil {
		return Config{}, fmt.Errorf("routing config: %w", err)
	}
	return c, c.Validate()
}

func (c Config) Validate() error {
	if c.Version != 1 {
		return fmt.Errorf("routing config: version must be 1")
	}
	if !ValidMode(c.Mode) {
		return fmt.Errorf("routing config: unknown mode %q", c.Mode)
	}
	if c.Preference != "" && c.Preference != "quality_first" {
		return fmt.Errorf("routing config: only quality_first preference is implemented")
	}
	if c.Spend.BudgetKind != "" && c.Spend.BudgetKind != "best_effort" && c.Spend.BudgetKind != "soft" {
		// hard caps are not something a CLI wrapper can promise
		return fmt.Errorf("routing config: budgetKind must be best_effort or soft")
	}
	s := c.Switching
	if s.MaxSwitchesPerRun < 0 || s.MaxSwitchesPerRun > 10 || s.MaxAttemptsPerRun < 1 || s.MaxAttemptsPerRun > 10 || s.MaxRunMinutes < 1 || s.MaxRunMinutes > 24*60 || s.Stickiness < 0 {
		return fmt.Errorf("routing config: switching limits out of range")
	}
	seen := map[string]bool{}
	for _, p := range c.Profiles {
		if err := p.validate(); err != nil {
			return err
		}
		if seen[p.ID] {
			return fmt.Errorf("routing config: duplicate profile %q", p.ID)
		}
		seen[p.ID] = true
	}
	eps := map[string]bool{}
	kinds := map[string]string{}
	for _, e := range c.LocalEndpoints {
		if err := e.Validate(); err != nil {
			return err
		}
		eps[e.ID], kinds[e.ID] = true, e.Kind
	}
	for _, p := range c.Profiles {
		if p.Adapter == AdapterLocal && !eps[p.EndpointRef] {
			return fmt.Errorf("routing config: profile %q references unknown endpoint %q", p.ID, p.EndpointRef)
		}
		if p.EndpointRef != "" && p.Adapter != AdapterLocal {
			// Codex on a local model, through the Responses→Chat bridge.
			if p.Adapter != AdapterCodex {
				return fmt.Errorf("routing config: profile %q: endpointRef is supported for local and codex profiles only", p.ID)
			}
			if kinds[p.EndpointRef] != "openai" {
				return fmt.Errorf("routing config: profile %q needs an endpoint of kind \"openai\" (got %q)", p.ID, p.EndpointRef)
			}
			if p.Model == "" {
				return fmt.Errorf("routing config: profile %q: a local Codex profile needs its model", p.ID)
			}
		}
	}
	if j := c.TierJudge; j != nil {
		if !eps[j.EndpointRef] {
			return fmt.Errorf("routing config: tierJudge references unknown endpoint %q", j.EndpointRef)
		}
	}
	if f := c.FreshStart; f != nil {
		if err := f.Validate(); err != nil {
			return err
		}
	}
	if c.Strategy != "" && !ValidStrategy(c.Strategy) {
		return fmt.Errorf("routing config: unknown strategy %q", c.Strategy)
	}
	if c.Strategy == StrategyRouteLLM && !c.RouteLLM.Enabled {
		return fmt.Errorf("routing config: strategy routellm needs routellm.enabled")
	}
	for r, id := range c.RolePins {
		if !validRole(r) || r == RoleDecider || (id != "" && !seen[id]) {
			return fmt.Errorf("routing config: invalid role pin %s=%s", r, id)
		}
	}
	if err := c.Decider.validate(seen); err != nil {
		return err
	}
	if r := c.RouteLLM; r.Enabled {
		if r.Router == "" {
			return fmt.Errorf("routing config: routellm.router is required")
		}
		for _, pair := range r.Pairs {
			if !seen[pair.Weak] || !seen[pair.Strong] || pair.Weak == pair.Strong {
				return fmt.Errorf("routing config: routellm pair %s/%s must name two configured profiles", pair.Weak, pair.Strong)
			}
			if pair.Threshold < 0 || pair.Threshold > 1 {
				return fmt.Errorf("routing config: routellm threshold must be within [0,1]")
			}
		}
	}
	return nil
}

func (p Profile) validate() error {
	if !idPattern.MatchString(p.ID) {
		return fmt.Errorf("routing config: invalid profile id %q", p.ID)
	}
	if _, ok := adapterFacts[p.Adapter]; !ok {
		return fmt.Errorf("routing config: profile %q has unknown adapter %q", p.ID, p.Adapter)
	}
	if p.Model != "" && !modelPattern.MatchString(p.Model) {
		return fmt.Errorf("routing config: profile %q has invalid model", p.ID)
	}
	if p.Effort != "" && !idPattern.MatchString(p.Effort) {
		return fmt.Errorf("routing config: profile %q has invalid effort", p.ID)
	}
	for _, t := range p.Tiers {
		if t <= TierUnset || t > Ultra {
			return fmt.Errorf("routing config: profile %q has invalid tier", p.ID)
		}
	}
	for _, r := range p.Roles {
		if !validRole(r) {
			return fmt.Errorf("routing config: profile %q has unknown role %q", p.ID, r)
		}
	}
	return nil
}

// Fingerprint changes whenever anything that affects a decision changes, so
// cached router answers and quality results can be invalidated.
func (c Config) Fingerprint() string {
	b, _ := json.Marshal(c)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:8])
}

// ProfileByID returns the configured profile.
func (c Config) ProfileByID(id string) (Profile, bool) {
	for _, p := range c.Profiles {
		if p.ID == id {
			return p, true
		}
	}
	return Profile{}, false
}

// WithDiscoveredDefaults adds one "CLI default model" profile per adapter that
// has no configured profile. Generated profiles are manual-only (Allow=false)
// and claim no tiers: no fake models or efforts are invented to fill tiers.
func (c Config) WithDiscoveredDefaults(statuses []AdapterStatus) Config {
	have := map[string]bool{}
	for _, p := range c.Profiles {
		have[p.Adapter] = true
	}
	out := c
	out.Profiles = append([]Profile(nil), c.Profiles...)
	for _, s := range statuses {
		if have[s.AdapterID] || s.Installation.Value != Installed || s.AdapterID == AdapterLocal {
			continue
		}
		out.Profiles = append(out.Profiles, Profile{ID: s.AdapterID + "-default", Adapter: s.AdapterID, Generated: true, Billing: Unknown, Roles: []Role{RoleExecutor, RoleReviewer, RolePlanner}})
	}
	sort.SliceStable(out.Profiles, func(i, j int) bool { return out.Profiles[i].MaxTier() < out.Profiles[j].MaxTier() })
	return out
}

func tiersString(ts []Tier) string {
	parts := make([]string, len(ts))
	for i, t := range ts {
		parts[i] = t.String()
	}
	return strings.Join(parts, ",")
}

// IsLocalCodex reports a Codex profile that runs on a local model server.
func (p Profile) IsLocalCodex() bool { return p.Adapter == AdapterCodex && p.EndpointRef != "" }

// LaunchModel is the model string a session is started with. A local Codex
// profile becomes "oss:<endpoint>:<model>" (see internal/ossbridge.Model).
func (p Profile) LaunchModel() string {
	if p.IsLocalCodex() {
		return "oss:" + p.EndpointRef + ":" + p.Model
	}
	return p.Model
}
