package orchestration

import (
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

func TestDraftHistoryPagesAndScopesWithoutChangingPlans(t *testing.T) {
	s := openTest(t, filepath.Join(t.TempDir(), "db"))
	r := createTest(t, s, "draft-history")
	empty, err := s.DraftHistory(r.ID, "")
	if err != nil || empty.Drafts == nil || len(empty.Drafts) != 0 || empty.NextCursor != "" {
		t.Fatal(empty, err)
	}
	var plan TaskPlan
	if err := json.Unmarshal([]byte(validDraft), &plan); err != nil {
		t.Fatal(err)
	}
	ids := []string{}
	for i := 0; i < 12; i++ {
		id, err := s.beginDraft(r.ID, "pinned-base")
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
		if i%2 == 0 {
			err = s.finishDraft(id, &plan, "ready")
		} else {
			err = s.finishDraft(id, nil, "planner failed")
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.db.Exec(`UPDATE v2_plan_drafts SET workspace='/private/hidden-draft-workspace' WHERE run_id=?`, r.ID); err != nil {
		t.Fatal(err)
	}
	first, err := s.DraftHistory(r.ID, "")
	if err != nil || len(first.Drafts) != 10 || first.NextCursor != ids[2] {
		t.Fatal(first, err)
	}
	for i, d := range first.Drafts {
		if d.ID != ids[11-i] {
			t.Fatal("wrong order", d)
		}
		if (11-i)%2 == 0 {
			if d.State != "succeeded" || d.Plan == nil || len(d.Plan.Tasks) != 2 || d.Plan.Tasks[1].DependsOn[0] != "api" {
				t.Fatal(d)
			}
		} else if d.State != "failed" || d.Plan != nil || d.Detail != "planner failed" {
			t.Fatal(d)
		}
	}
	// A new generation between page requests must not shift older pages.
	latest, err := s.beginDraft(r.ID, "pinned-base")
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.DraftHistory(r.ID, first.NextCursor)
	if err != nil || len(second.Drafts) != 2 || second.NextCursor != "" || second.Drafts[0].ID != ids[1] || second.Drafts[1].ID != ids[0] {
		t.Fatal(second, err)
	}
	running, err := s.DraftHistory(r.ID, "")
	if err != nil || running.Drafts[0].ID != latest || running.Drafts[0].State != "running" {
		t.Fatal(running, err)
	}
	current, err := s.Get(r.ID)
	if err != nil || current.State != "planning" || len(current.Executions) != 0 {
		t.Fatal("read mutated Run", current, err)
	}
	if err := s.finishDraft(latest, &plan, "ready"); err != nil {
		t.Fatal(err)
	}
	// Finalization may edit the draft; history must retain its original content.
	plan.Tasks[0].Prompt = "edited before confirmation"
	if err := s.SavePlan(r.ID, plan); err != nil {
		t.Fatal(err)
	}
	frozen, err := s.DraftHistory(r.ID, "")
	if err != nil || frozen.Drafts[0].Plan.Tasks[0].Prompt != "implement API" {
		t.Fatal(frozen, err)
	}
	saved, err := s.GetPlan(r.ID)
	if err != nil || saved.Tasks[0].Prompt != "edited before confirmation" {
		t.Fatal("history changed frozen plan", saved, err)
	}
	raw, err := json.Marshal(frozen)
	if err != nil || strings.Contains(string(raw), "hidden-draft-workspace") || strings.Contains(string(raw), `"workspace"`) {
		t.Fatal("workspace exposed", err)
	}
	other := createTest(t, s, "other-draft-history")
	for _, cursor := range []string{ids[0], "missing", strings.Repeat("x", 129)} {
		if _, err := s.DraftHistory(other.ID, cursor); !errors.Is(err, ErrInvalid) {
			t.Fatal("foreign/invalid cursor accepted", err)
		}
	}
	if _, err := s.DraftHistory("missing-run", ""); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal(err)
	}
}

func TestDraftHistoryRetainsCanceledAndInterruptedGenerations(t *testing.T) {
	s := openTest(t, filepath.Join(t.TempDir(), "db"))
	for _, state := range []string{"canceled", "interrupted"} {
		r := createTest(t, s, state)
		id, err := s.beginDraft(r.ID, "base")
		if err != nil {
			t.Fatal(err)
		}
		if state == "canceled" {
			err = s.Cancel(r.ID)
		} else {
			err = s.Recover()
		}
		if err != nil {
			t.Fatal(err)
		}
		history, err := s.DraftHistory(r.ID, "")
		if err != nil || len(history.Drafts) != 1 || history.Drafts[0].ID != id || history.Drafts[0].State != state || history.Drafts[0].Plan != nil || history.Drafts[0].Detail == "" {
			t.Fatal(history, err)
		}
		if err := s.finishDraft(id, nil, "late"); !errors.Is(err, ErrConflict) {
			t.Fatal("late update accepted", err)
		}
	}
}
