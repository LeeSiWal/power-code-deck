package routing

import (
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

// Additive tables only. Deleting a Run's routing rows never touches v2_runs.
const storeSchema = `
CREATE TABLE IF NOT EXISTS v2_routing_runs(
  run_id TEXT PRIMARY KEY, mode TEXT NOT NULL, phase TEXT NOT NULL, epoch INTEGER NOT NULL DEFAULT 0,
  current_profile TEXT NOT NULL DEFAULT '', current_execution TEXT NOT NULL DEFAULT '',
  pending_profile TEXT NOT NULL DEFAULT '', pending_when TEXT NOT NULL DEFAULT '',
  pin_profile TEXT NOT NULL DEFAULT '', pin_adapter TEXT NOT NULL DEFAULT '',
  switches INTEGER NOT NULL DEFAULT 0, attempts INTEGER NOT NULL DEFAULT 0,
  started_at TEXT NOT NULL DEFAULT '', last_decision TEXT NOT NULL DEFAULT '', updated_at TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS v2_routing_decisions(
  id TEXT PRIMARY KEY, run_id TEXT NOT NULL, epoch INTEGER NOT NULL, applied INTEGER NOT NULL DEFAULT 0,
  body TEXT NOT NULL, created_at TEXT NOT NULL);
CREATE INDEX IF NOT EXISTS v2_routing_decisions_run ON v2_routing_decisions(run_id, created_at);
CREATE TABLE IF NOT EXISTS v2_routing_transitions(
  seq INTEGER PRIMARY KEY AUTOINCREMENT, run_id TEXT NOT NULL, epoch INTEGER NOT NULL,
  from_phase TEXT NOT NULL, to_phase TEXT NOT NULL, cause TEXT NOT NULL, detail TEXT NOT NULL DEFAULT '',
  execution_id TEXT NOT NULL DEFAULT '', created_at TEXT NOT NULL);
CREATE INDEX IF NOT EXISTS v2_routing_transitions_run ON v2_routing_transitions(run_id, seq);
CREATE TABLE IF NOT EXISTS v2_routing_bindings(
  run_id TEXT NOT NULL, adapter TEXT NOT NULL, account_ref TEXT NOT NULL, workspace TEXT NOT NULL,
  body TEXT NOT NULL, updated_at TEXT NOT NULL, PRIMARY KEY(run_id, adapter, account_ref, workspace));
CREATE TABLE IF NOT EXISTS v2_routing_attempts(
  execution_id TEXT PRIMARY KEY, run_id TEXT NOT NULL, epoch INTEGER NOT NULL, profile_id TEXT NOT NULL,
  adapter TEXT NOT NULL, model TEXT NOT NULL DEFAULT '', effort TEXT NOT NULL DEFAULT '',
  decision_id TEXT NOT NULL DEFAULT '', inherited_from TEXT NOT NULL DEFAULT '', continuation TEXT NOT NULL DEFAULT '',
  report TEXT NOT NULL DEFAULT '', class TEXT NOT NULL DEFAULT '', action TEXT NOT NULL DEFAULT '',
  started_at TEXT NOT NULL, finished_at TEXT NOT NULL DEFAULT '');
CREATE INDEX IF NOT EXISTS v2_routing_attempts_run ON v2_routing_attempts(run_id, started_at);
CREATE TABLE IF NOT EXISTS v2_routing_run_options(
  run_id TEXT PRIMARY KEY, strategy TEXT NOT NULL DEFAULT '', commercial_shadow INTEGER NOT NULL DEFAULT 0, updated_at TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS v2_routing_decider_calls(
  seq INTEGER PRIMARY KEY AUTOINCREMENT, run_id TEXT NOT NULL, decision_id TEXT NOT NULL, request_id TEXT NOT NULL,
  mode TEXT NOT NULL, profile_id TEXT NOT NULL, purpose TEXT NOT NULL, valid INTEGER NOT NULL, error_class TEXT NOT NULL DEFAULT '',
  latency_ms INTEGER NOT NULL, usage TEXT NOT NULL DEFAULT '', created_at TEXT NOT NULL);
CREATE INDEX IF NOT EXISTS v2_routing_decider_calls_profile ON v2_routing_decider_calls(profile_id, seq);
CREATE TABLE IF NOT EXISTS v2_routing_versions(
  adapter TEXT PRIMARY KEY, version TEXT NOT NULL, validated_version TEXT NOT NULL DEFAULT '', seen_at TEXT NOT NULL);
`

type Store struct {
	db  *sql.DB
	now func() time.Time
}

func NewStore(db *sql.DB) (*Store, error) {
	if _, err := db.Exec(storeSchema); err != nil {
		return nil, err
	}
	return &Store{db: db, now: time.Now}, nil
}

type RunState struct {
	RunID            string `json:"runId"`
	Mode             Mode   `json:"mode"`
	Phase            Phase  `json:"phase"`
	Epoch            int64  `json:"epoch"`
	CurrentProfile   string `json:"currentProfile"`
	CurrentExecution string `json:"currentExecution"`
	PendingProfile   string `json:"pendingProfile"`
	PendingWhen      string `json:"pendingWhen"` // boundary | now
	PinProfile       string `json:"pinProfile"`
	PinAdapter       string `json:"pinAdapter"`
	Switches         int    `json:"switches"`
	Attempts         int    `json:"attempts"`
	StartedAt        string `json:"startedAt"`
	LastDecision     string `json:"lastDecision"`
	UpdatedAt        string `json:"updatedAt"`
}

func (s *Store) ts() string { return s.now().UTC().Format(time.RFC3339Nano) }

// Ensure creates the routing row for a Run on first use.
func (s *Store) Ensure(runID string, mode Mode) (RunState, error) {
	_, err := s.db.Exec(`INSERT INTO v2_routing_runs(run_id,mode,phase,updated_at) VALUES(?,?,?,?) ON CONFLICT(run_id) DO NOTHING`, runID, mode, PhaseIdle, s.ts())
	if err != nil {
		return RunState{}, err
	}
	return s.Get(runID)
}

func (s *Store) Get(runID string) (RunState, error) {
	var r RunState
	err := s.db.QueryRow(`SELECT run_id,mode,phase,epoch,current_profile,current_execution,pending_profile,pending_when,pin_profile,pin_adapter,switches,attempts,started_at,last_decision,updated_at FROM v2_routing_runs WHERE run_id=?`, runID).
		Scan(&r.RunID, &r.Mode, &r.Phase, &r.Epoch, &r.CurrentProfile, &r.CurrentExecution, &r.PendingProfile, &r.PendingWhen, &r.PinProfile, &r.PinAdapter, &r.Switches, &r.Attempts, &r.StartedAt, &r.LastDecision, &r.UpdatedAt)
	return r, err
}

// Settings changes mode and pins. It bumps the epoch so an in-flight router
// answer computed under the old settings is discarded.
func (s *Store) Settings(runID string, mode Mode, pinProfile, pinAdapter string) (RunState, error) {
	if !ValidMode(mode) {
		return RunState{}, errors.New("invalid mode")
	}
	if _, err := s.Ensure(runID, mode); err != nil {
		return RunState{}, err
	}
	if _, err := s.db.Exec(`UPDATE v2_routing_runs SET mode=?,pin_profile=?,pin_adapter=?,epoch=epoch+1,updated_at=? WHERE run_id=?`, mode, pinProfile, pinAdapter, s.ts(), runID); err != nil {
		return RunState{}, err
	}
	return s.Get(runID)
}

// RequestSwitch records the user's intent. "boundary" applies at the next
// attempt; "now" is acted on by the coordinator after quiescence.
func (s *Store) RequestSwitch(runID, profile, when string) (RunState, error) {
	if when != "boundary" && when != "now" && when != "" {
		return RunState{}, errors.New("invalid switch timing")
	}
	res, err := s.db.Exec(`UPDATE v2_routing_runs SET pending_profile=?,pending_when=?,epoch=epoch+1,updated_at=? WHERE run_id=? AND phase NOT IN ('succeeded','canceled')`, profile, when, s.ts(), runID)
	if err := changedOne(res, err); err != nil {
		return RunState{}, err
	}
	return s.Get(runID)
}

// Transition moves the machine one edge, fenced by epoch. The transition log is
// written in the same transaction so recovery sees a consistent history.
func (s *Store) Transition(runID string, epoch int64, to Phase, cause, detail, execution string) (RunState, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return RunState{}, err
	}
	defer tx.Rollback()
	var from Phase
	var cur int64
	if err := tx.QueryRow(`SELECT phase,epoch FROM v2_routing_runs WHERE run_id=?`, runID).Scan(&from, &cur); err != nil {
		return RunState{}, err
	}
	if epoch >= 0 && cur != epoch {
		return RunState{}, ErrStaleEpoch
	}
	if !CanTransition(from, to) {
		return RunState{}, ErrBadTransition{from, to}
	}
	now := s.ts()
	if _, err := tx.Exec(`UPDATE v2_routing_runs SET phase=?,epoch=epoch+1,updated_at=? WHERE run_id=? AND epoch=?`, to, now, runID, cur); err != nil {
		return RunState{}, err
	}
	if _, err := tx.Exec(`INSERT INTO v2_routing_transitions(run_id,epoch,from_phase,to_phase,cause,detail,execution_id,created_at) VALUES(?,?,?,?,?,?,?,?)`, runID, cur+1, from, to, cause, Summarize(detail, 2000), execution, now); err != nil {
		return RunState{}, err
	}
	if err := tx.Commit(); err != nil {
		return RunState{}, err
	}
	return s.Get(runID)
}

func (s *Store) SaveDecision(runID string, epoch int64, d Decision) error {
	body, err := json.Marshal(d)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`INSERT INTO v2_routing_decisions(id,run_id,epoch,body,created_at) VALUES(?,?,?,?,?)`, d.ID, runID, epoch, string(body), d.CreatedAt.Format(time.RFC3339Nano))
	if err == nil {
		_, err = s.db.Exec(`UPDATE v2_routing_runs SET last_decision=?,updated_at=? WHERE run_id=?`, d.ID, s.ts(), runID)
	}
	return err
}

// BeginAttempt binds a new execution to the Run as its only writer. It fails if
// another attempt is recorded as unfinished.
func (s *Store) BeginAttempt(runID string, epoch int64, execution string, p Profile, decisionID, inheritedFrom string, cont Continuation) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var open int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM v2_routing_attempts WHERE run_id=? AND finished_at=''`, runID).Scan(&open); err != nil {
		return err
	}
	if open > 0 {
		return errors.New("routing: another attempt is still the writer for this run")
	}
	var prevProfile string
	var attempts int
	if err := tx.QueryRow(`SELECT current_profile,attempts FROM v2_routing_runs WHERE run_id=?`, runID).Scan(&prevProfile, &attempts); err != nil {
		return err
	}
	switchInc := 0
	if prevProfile != "" && prevProfile != p.ID {
		switchInc = 1
	}
	contJSON, _ := json.Marshal(cont)
	now := s.ts()
	if _, err := tx.Exec(`INSERT INTO v2_routing_attempts(execution_id,run_id,epoch,profile_id,adapter,model,effort,decision_id,inherited_from,continuation,started_at) VALUES(?,?,?,?,?,?,?,?,?,?,?)`,
		execution, runID, epoch, p.ID, p.Adapter, p.Model, p.Effort, decisionID, inheritedFrom, string(contJSON), now); err != nil {
		return err
	}
	started := ""
	if attempts == 0 {
		started = now
	}
	if _, err := tx.Exec(`UPDATE v2_routing_runs SET current_profile=?,current_execution=?,pending_profile=CASE WHEN pending_profile=? THEN '' ELSE pending_profile END,pending_when=CASE WHEN pending_profile=? THEN '' ELSE pending_when END,switches=switches+?,attempts=attempts+1,started_at=CASE WHEN started_at='' THEN ? ELSE started_at END,updated_at=? WHERE run_id=?`,
		p.ID, execution, p.ID, p.ID, switchInc, started, now, runID); err != nil {
		return err
	}
	if decisionID != "" {
		if _, err := tx.Exec(`UPDATE v2_routing_decisions SET applied=1 WHERE id=?`, decisionID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// AbortAttempt removes an attempt that never launched (the worker refused it),
// so it does not block the next writer. Launched attempts are finished instead.
func (s *Store) AbortAttempt(execution string) error {
	_, err := s.db.Exec(`DELETE FROM v2_routing_attempts WHERE execution_id=? AND finished_at=''`, execution)
	return err
}

// FinishAttempt stores the report exactly once. A duplicate or late report for
// an already-finished attempt returns ErrStaleEpoch and changes nothing.
func (s *Store) FinishAttempt(r AttemptReport, class FailureClass, act Action) error {
	body, _ := json.Marshal(r)
	actJSON, _ := json.Marshal(act)
	res, err := s.db.Exec(`UPDATE v2_routing_attempts SET report=?,class=?,action=?,finished_at=? WHERE execution_id=? AND finished_at=''`, string(body), class, string(actJSON), s.ts(), r.ExecutionID)
	if err := changedOne(res, err); err != nil {
		return ErrStaleEpoch
	}
	return nil
}

type AttemptRecord struct {
	ExecutionID   string         `json:"executionId"`
	RunID         string         `json:"runId"`
	Epoch         int64          `json:"epoch"`
	ProfileID     string         `json:"profileId"`
	Adapter       string         `json:"adapter"`
	Model         string         `json:"model"`
	Effort        string         `json:"effort"`
	DecisionID    string         `json:"decisionId"`
	InheritedFrom string         `json:"inheritedFrom"`
	Continuation  *Continuation  `json:"continuation,omitempty"`
	Report        *AttemptReport `json:"report,omitempty"`
	Class         FailureClass   `json:"class"`
	Action        *Action        `json:"action,omitempty"`
	StartedAt     string         `json:"startedAt"`
	FinishedAt    string         `json:"finishedAt"`
}

type Transition struct {
	Seq         int64  `json:"seq"`
	Epoch       int64  `json:"epoch"`
	From        Phase  `json:"from"`
	To          Phase  `json:"to"`
	Cause       string `json:"cause"`
	Detail      string `json:"detail"`
	ExecutionID string `json:"executionId"`
	CreatedAt   string `json:"createdAt"`
}

// Timeline is the single ordered history the UI renders for one Run.
type Timeline struct {
	State       RunState        `json:"state"`
	Attempts    []AttemptRecord `json:"attempts"`
	Decisions   []Decision      `json:"decisions"`
	Transitions []Transition    `json:"transitions"`
	Bindings    []Binding       `json:"bindings"`
}

func (s *Store) Timeline(runID string) (Timeline, error) {
	t := Timeline{Attempts: []AttemptRecord{}, Decisions: []Decision{}, Transitions: []Transition{}, Bindings: []Binding{}}
	st, err := s.Get(runID)
	if errors.Is(err, sql.ErrNoRows) {
		t.State = RunState{RunID: runID, Mode: ModeOff, Phase: PhaseIdle}
		return t, nil
	}
	if err != nil {
		return t, err
	}
	t.State = st
	rows, err := s.db.Query(`SELECT execution_id,run_id,epoch,profile_id,adapter,model,effort,decision_id,inherited_from,continuation,report,class,action,started_at,finished_at FROM v2_routing_attempts WHERE run_id=? ORDER BY started_at`, runID)
	if err != nil {
		return t, err
	}
	for rows.Next() {
		var a AttemptRecord
		var cont, rep, act string
		if err := rows.Scan(&a.ExecutionID, &a.RunID, &a.Epoch, &a.ProfileID, &a.Adapter, &a.Model, &a.Effort, &a.DecisionID, &a.InheritedFrom, &cont, &rep, &a.Class, &act, &a.StartedAt, &a.FinishedAt); err != nil {
			rows.Close()
			return t, err
		}
		if cont != "" {
			a.Continuation = new(Continuation)
			_ = json.Unmarshal([]byte(cont), a.Continuation)
		}
		if rep != "" {
			a.Report = new(AttemptReport)
			_ = json.Unmarshal([]byte(rep), a.Report)
		}
		if act != "" {
			a.Action = new(Action)
			_ = json.Unmarshal([]byte(act), a.Action)
		}
		t.Attempts = append(t.Attempts, a)
	}
	rows.Close()
	rows, err = s.db.Query(`SELECT body FROM v2_routing_decisions WHERE run_id=? ORDER BY created_at LIMIT 200`, runID)
	if err != nil {
		return t, err
	}
	for rows.Next() {
		var body string
		var d Decision
		if rows.Scan(&body) == nil && json.Unmarshal([]byte(body), &d) == nil {
			t.Decisions = append(t.Decisions, d)
		}
	}
	rows.Close()
	rows, err = s.db.Query(`SELECT seq,epoch,from_phase,to_phase,cause,detail,execution_id,created_at FROM v2_routing_transitions WHERE run_id=? ORDER BY seq LIMIT 500`, runID)
	if err != nil {
		return t, err
	}
	for rows.Next() {
		var x Transition
		if err := rows.Scan(&x.Seq, &x.Epoch, &x.From, &x.To, &x.Cause, &x.Detail, &x.ExecutionID, &x.CreatedAt); err != nil {
			rows.Close()
			return t, err
		}
		t.Transitions = append(t.Transitions, x)
	}
	rows.Close()
	t.Bindings, err = s.Bindings(runID)
	return t, err
}

func (s *Store) PutBinding(b Binding) error {
	b.UpdatedAt = s.now().UTC()
	body, _ := json.Marshal(b)
	_, err := s.db.Exec(`INSERT INTO v2_routing_bindings(run_id,adapter,account_ref,workspace,body,updated_at) VALUES(?,?,?,?,?,?) ON CONFLICT(run_id,adapter,account_ref,workspace) DO UPDATE SET body=excluded.body,updated_at=excluded.updated_at`,
		b.RunID, b.Adapter, b.AccountRef, b.Workspace, string(body), s.ts())
	return err
}

func (s *Store) Bindings(runID string) ([]Binding, error) {
	out := []Binding{}
	rows, err := s.db.Query(`SELECT body FROM v2_routing_bindings WHERE run_id=?`, runID)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var body string
		var b Binding
		if rows.Scan(&body) == nil && json.Unmarshal([]byte(body), &b) == nil {
			out = append(out, b)
		}
	}
	return out, rows.Err()
}

// Recover runs before any worker starts. Switches caught mid-flight become
// reconcile: nobody is sure whether the old writer finished, so no new writer
// starts until a person looks. Unfinished attempts are closed as unknown.
func (s *Store) Recover() error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := s.ts()
	rows, err := tx.Query(`SELECT run_id,phase,epoch FROM v2_routing_runs WHERE phase IN ('running','quiescing','checkpointed','routing','handoff','waiting_approval')`)
	if err != nil {
		return err
	}
	type pending struct {
		id    string
		phase Phase
		epoch int64
	}
	var list []pending
	for rows.Next() {
		var p pending
		if err := rows.Scan(&p.id, &p.phase, &p.epoch); err != nil {
			rows.Close()
			return err
		}
		list = append(list, p)
	}
	rows.Close()
	for _, p := range list {
		if _, err := tx.Exec(`UPDATE v2_routing_runs SET phase='reconcile',epoch=epoch+1,updated_at=? WHERE run_id=?`, now, p.id); err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT INTO v2_routing_transitions(run_id,epoch,from_phase,to_phase,cause,detail,created_at) VALUES(?,?,?,?,?,?,?)`, p.id, p.epoch+1, p.phase, PhaseReconcile, "server_restart", "server restarted during "+string(p.phase)+"; inspect the workspace before continuing", now); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(`UPDATE v2_routing_attempts SET class='unknown_side_effect',finished_at=? WHERE finished_at=''`, now); err != nil {
		return err
	}
	return tx.Commit()
}

// DeleteRun removes routing history for a Run (retention/deletion control).
func (s *Store) DeleteRun(runID string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, q := range []string{`DELETE FROM v2_routing_run_options WHERE run_id=?`, `DELETE FROM v2_routing_decider_calls WHERE run_id=?`, `DELETE FROM v2_routing_attempts WHERE run_id=?`, `DELETE FROM v2_routing_decisions WHERE run_id=?`, `DELETE FROM v2_routing_transitions WHERE run_id=?`, `DELETE FROM v2_routing_bindings WHERE run_id=?`, `DELETE FROM v2_routing_runs WHERE run_id=?`} {
		if _, err := tx.Exec(q, runID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func changedOne(res sql.Result, err error) error {
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return sql.ErrNoRows
	}
	return nil
}

// NoteVersion records the CLI version seen by discovery and returns the version
// that was last marked validated ("" if never). A difference means capability,
// quality and cached router answers must be re-verified for that adapter.
func (s *Store) NoteVersion(adapter, version string) (validated string, err error) {
	if version == "" {
		return "", nil
	}
	_, err = s.db.Exec(`INSERT INTO v2_routing_versions(adapter,version,seen_at) VALUES(?,?,?) ON CONFLICT(adapter) DO UPDATE SET version=excluded.version,seen_at=excluded.seen_at`, adapter, version, s.ts())
	if err != nil {
		return "", err
	}
	err = s.db.QueryRow(`SELECT validated_version FROM v2_routing_versions WHERE adapter=?`, adapter).Scan(&validated)
	return validated, err
}

// MarkValidated records that a person (or an opt-in live test) re-verified the
// adapter at its current version.
func (s *Store) MarkValidated(adapter string) error {
	res, err := s.db.Exec(`UPDATE v2_routing_versions SET validated_version=version WHERE adapter=?`, adapter)
	return changedOne(res, err)
}

// RunOptions are per-Run strategy settings the browser may change.
type RunOptions struct {
	Strategy         Strategy `json:"strategy"` // "" = config default
	CommercialShadow bool     `json:"commercialShadow"`
}

func (s *Store) Options(runID string) (RunOptions, error) {
	var o RunOptions
	var shadow int
	err := s.db.QueryRow(`SELECT strategy,commercial_shadow FROM v2_routing_run_options WHERE run_id=?`, runID).Scan(&o.Strategy, &shadow)
	if err == sql.ErrNoRows {
		return RunOptions{}, nil
	}
	o.CommercialShadow = shadow == 1
	return o, err
}

func (s *Store) SetOptions(runID string, o RunOptions) error {
	if o.Strategy != "" && !ValidStrategy(o.Strategy) {
		return errors.New("invalid strategy")
	}
	shadow := 0
	if o.CommercialShadow {
		shadow = 1
	}
	_, err := s.db.Exec(`INSERT INTO v2_routing_run_options(run_id,strategy,commercial_shadow,updated_at) VALUES(?,?,?,?) ON CONFLICT(run_id) DO UPDATE SET strategy=excluded.strategy,commercial_shadow=excluded.commercial_shadow,updated_at=excluded.updated_at`, runID, o.Strategy, shadow, s.ts())
	if err == nil {
		// Settings changed: an in-flight decision computed under the old ones is stale.
		_, err = s.db.Exec(`UPDATE v2_routing_runs SET epoch=epoch+1,updated_at=? WHERE run_id=?`, s.ts(), runID)
	}
	return err
}

// SaveDeciderCalls logs every decider invocation, including ones whose
// decision was later discarded (the usage happened regardless).
func (s *Store) SaveDeciderCalls(runID, decisionID string, mode Mode, rec DeciderRecord) error {
	for _, c := range rec.Calls {
		u, _ := json.Marshal(c.Usage)
		valid := 0
		if c.Valid {
			valid = 1
		}
		if _, err := s.db.Exec(`INSERT INTO v2_routing_decider_calls(run_id,decision_id,request_id,mode,profile_id,purpose,valid,error_class,latency_ms,usage,created_at) VALUES(?,?,?,?,?,?,?,?,?,?,?)`,
			runID, decisionID, rec.RequestID, mode, c.ProfileID, c.Purpose, valid, c.ErrorClass, c.LatencyMS, string(u), s.ts()); err != nil {
			return err
		}
	}
	return nil
}

// DeciderStats summarizes the last 200 calls per profile (measured only).
func (s *Store) DeciderStats() (map[string]DeciderStats, error) {
	rows, err := s.db.Query(`SELECT profile_id,valid,latency_ms,usage FROM v2_routing_decider_calls WHERE seq > (SELECT COALESCE(MAX(seq),0)-2000 FROM v2_routing_decider_calls) ORDER BY seq`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	type acc struct {
		lat           []int64
		valid, n      int
		tokens, known int64
	}
	m := map[string]*acc{}
	for rows.Next() {
		var id, usage string
		var valid int
		var lat int64
		if err := rows.Scan(&id, &valid, &lat, &usage); err != nil {
			return nil, err
		}
		a := m[id]
		if a == nil {
			a = &acc{}
			m[id] = a
		}
		a.n++
		a.valid += valid
		a.lat = append(a.lat, lat)
		var u UsageRecord
		if json.Unmarshal([]byte(usage), &u) == nil && u.InputTokens != nil && u.OutputTokens != nil {
			a.tokens += int64(*u.InputTokens + *u.OutputTokens)
			a.known++
		}
	}
	out := map[string]DeciderStats{}
	for id, a := range m {
		sortInt64(a.lat)
		st := DeciderStats{Calls: a.n, Valid: a.valid, P50MS: a.lat[len(a.lat)/2], P95MS: a.lat[(len(a.lat)*95+99)/100-1], MeanTokens: -1}
		if a.known > 0 {
			st.MeanTokens = a.tokens / a.known
		}
		out[id] = st
	}
	return out, rows.Err()
}

// ShadowCallsSince counts commercial decider calls made in Shadow mode.
func (s *Store) ShadowCallsSince(t time.Time) (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM v2_routing_decider_calls WHERE mode='shadow' AND created_at>=?`, t.UTC().Format(time.RFC3339Nano)).Scan(&n)
	return n, err
}

func sortInt64(a []int64) {
	for i := 1; i < len(a); i++ {
		for j := i; j > 0 && a[j] < a[j-1]; j-- {
			a[j], a[j-1] = a[j-1], a[j]
		}
	}
}
