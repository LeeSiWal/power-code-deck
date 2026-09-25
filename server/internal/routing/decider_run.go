package routing

import (
	"context"
	"errors"
	"time"
)

// Consult runs the decider for one decision. It is bounded by the decider
// config: at most one fallback profile, MaxFormatRetries re-asks for invalid
// output, MaxContextRounds evidence top-ups, and an overall deadline of
// TimeoutSeconds × 2. It never chooses a fallback by asking a model, and never
// escalates to a more expensive decider on failure.
func Consult(ctx context.Context, cfg DeciderConfig, deciders []Profile, req DecisionRequest, caller DeciderCaller, more func(needs []string) []EvidenceItem) DeciderRecord {
	lim := cfg.Limits()
	rec := DeciderRecord{Consulted: true, RequestID: req.RequestID}
	if len(deciders) == 0 || caller == nil {
		rec.Consulted, rec.Skip = false, SkipNoDecider
		return rec
	}
	per := time.Duration(lim.TimeoutSeconds) * time.Second
	ctx, cancel := context.WithTimeout(ctx, 2*per)
	defer cancel()
	profile := deciders[0]
	var fallback *Profile
	for i := range deciders {
		if deciders[i].ID == cfg.Fallback && deciders[i].ID != profile.ID {
			fallback = &deciders[i]
		}
	}
	formatLeft, roundsLeft := lim.MaxFormatRetries, lim.MaxContextRounds
	purpose, correction := "initial", ""
	for {
		prompt := DeciderPrompt(req)
		if correction != "" {
			prompt += "\n\nYour previous reply was rejected (" + correction + "). Reply with exactly one valid JSON object."
		}
		cctx, ccancel := context.WithTimeout(ctx, per)
		start := time.Now()
		out, err := caller.Call(cctx, profile, prompt, VerdictSchema(req))
		ccancel()
		call := DeciderCall{ProfileID: profile.ID, Purpose: purpose, LatencyMS: time.Since(start).Milliseconds(), Usage: out.Usage, ObservedModel: out.ObservedModel}
		if err != nil {
			var ce *CallError
			call.ErrorClass, call.Error = "provider_error", Summarize(err.Error(), 300)
			if errors.As(err, &ce) {
				call.ErrorClass = ce.Class
			}
			rec.Calls = append(rec.Calls, call)
			if call.ErrorClass == "canceled" || ctx.Err() != nil {
				rec.Note = "decision budget or request context ended"
				return rec
			}
			if fallback != nil && purpose != "fallback" {
				profile, purpose, fallback = *fallback, "fallback", nil
				continue
			}
			rec.Note = "decider unavailable: " + call.ErrorClass
			return rec
		}
		v, perr := ParseVerdict([]byte(out.Text), req, lim.MaxOutputBytes)
		if perr != nil {
			call.ErrorClass, call.Error = "invalid_output", Summarize(perr.Error(), 300)
			rec.Calls = append(rec.Calls, call)
			if formatLeft > 0 {
				formatLeft--
				purpose, correction = "format_retry", perr.Error()
				continue
			}
			rec.Note = "decider output invalid after retries"
			return rec
		}
		call.Valid = true
		rec.Calls = append(rec.Calls, call)
		if v.Action == ActNeedContext {
			if roundsLeft > 0 && more != nil {
				if extra := more(v.Needs); len(extra) > 0 {
					roundsLeft--
					req.Evidence = append(req.Evidence, extra...)
					purpose, correction = "context_round", ""
					continue
				}
			}
			rec.Verdict = &v
			rec.Note = "decider asked for context that is not available within the budget"
			return rec
		}
		rec.Verdict = &v
		return rec
	}
}
