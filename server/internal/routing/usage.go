package routing

// RoleUsage totals reported usage for one role. Each reported value is counted
// in exactly one role; Complete is false when any call in the role did not
// report usage, so a partial sum is never shown as the whole.
type RoleUsage struct {
	Role      string `json:"role"` // decision | execution | retry | review | local
	Calls     int    `json:"calls"`
	Reported  int    `json:"reported"`
	Input     int64  `json:"inputTokens"`
	Output    int64  `json:"outputTokens"`
	CacheRead int64  `json:"cacheReadTokens"`
	Complete  bool   `json:"complete"`
}

func (r *RoleUsage) add(u *UsageRecord) {
	r.Calls++
	if u == nil || u.InputTokens == nil || u.OutputTokens == nil {
		return
	}
	r.Reported++
	r.Input += int64(*u.InputTokens)
	r.Output += int64(*u.OutputTokens)
	if u.CacheRead != nil {
		r.CacheRead += int64(*u.CacheRead)
	}
}

// UsageByRole splits a Run's recorded usage. The first attempt is execution,
// later attempts are retry; local-endpoint usage is reported separately and
// excluded from the commercial total. Cache-read semantics stay per provider
// (Claude reports it separately from input; Codex includes it in input), so
// CacheRead is informational and never added to Input.
func UsageByRole(t Timeline) (roles []RoleUsage, commercial RoleUsage) {
	by := map[string]*RoleUsage{}
	get := func(name string) *RoleUsage {
		if by[name] == nil {
			by[name] = &RoleUsage{Role: name}
		}
		return by[name]
	}
	for _, d := range t.Decisions {
		if d.Decider == nil {
			continue
		}
		for _, c := range d.Decider.Calls {
			role := "decision"
			if c.Usage != nil && c.Usage.Local {
				role = "local"
			}
			get(role).add(c.Usage)
		}
	}
	for i, a := range t.Attempts {
		if a.Report == nil {
			continue
		}
		role := "execution"
		if i > 0 {
			role = "retry"
		}
		if a.Adapter == AdapterLocal {
			role = "local"
		}
		get(role).add(a.Report.Usage)
		if a.Report.Reviewer != "" {
			get("review").add(a.Report.ReviewUsage)
		}
	}
	commercial = RoleUsage{Role: "commercial_total", Complete: true}
	for _, name := range []string{"decision", "execution", "retry", "review", "local"} {
		r := by[name]
		if r == nil {
			continue
		}
		r.Complete = r.Reported == r.Calls
		roles = append(roles, *r)
		if name == "local" {
			continue
		}
		commercial.Calls += r.Calls
		commercial.Reported += r.Reported
		commercial.Input += r.Input
		commercial.Output += r.Output
		commercial.CacheRead += r.CacheRead
		commercial.Complete = commercial.Complete && r.Complete
	}
	return roles, commercial
}
