package orchestration

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// Cleanup removes only registered worktrees. Evidence files and retained Git
// commits stay available for history, retry, integration and branch application.
type CleanupCandidate struct {
	ID          string `json:"id"`
	Kind        string `json:"kind"`
	State       string `json:"state"`
	Eligible    bool   `json:"eligible"`
	Reason      string `json:"reason"`
	Fingerprint string `json:"fingerprint,omitempty"`
	Path        string `json:"-"`
	head        string
	result      string
}
type CleanupPreview struct {
	Candidates []CleanupCandidate `json:"candidates"`
	NextCursor string             `json:"nextCursor"`
}

var ownedAttemptID = regexp.MustCompile(`^[a-z]+_[A-Za-z0-9]+$`)

func (w *Worker) cleanupRecords(run, before string) (CleanupPreview, error) {
	out := CleanupPreview{Candidates: []CleanupCandidate{}}
	if len(before) > 128 {
		return out, ErrInvalid
	}
	// IDs are stable opaque cursors. Ordering need not imply creation time.
	rows, err := w.store.db.Query(`SELECT id,kind,state,path FROM (
	 SELECT e.id,'execution' kind,e.state,a.path FROM v2_executions e JOIN v2_tasks t ON t.id=e.task_id JOIN v2_artifacts a ON a.execution_id=e.id AND a.kind='workspace' WHERE t.run_id=?
	 UNION ALL SELECT e.id,'task',e.state,a.path FROM v2_plan_attempts e JOIN v2_plan_artifacts a ON a.attempt_id=e.id AND a.kind='workspace' WHERE e.run_id=?
	 UNION ALL SELECT e.id,'integration',e.state,a.path FROM v2_integrations e JOIN v2_integration_artifacts a ON a.attempt_id=e.id AND a.kind='workspace' WHERE e.run_id=?
	 UNION ALL SELECT id,'draft',state,workspace FROM v2_plan_drafts WHERE run_id=? AND workspace!=''
	) WHERE id>? ORDER BY id LIMIT 11`, run, run, run, run, before)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var c CleanupCandidate
		if err := rows.Scan(&c.ID, &c.Kind, &c.State, &c.Path); err != nil {
			return out, err
		}
		if len(out.Candidates) == 10 {
			out.NextCursor = out.Candidates[9].ID
			break
		}
		out.Candidates = append(out.Candidates, c)
	}
	return out, rows.Err()
}

func (w *Worker) cleanupIdle(run Run) error {
	if w.closed || w.cancel != nil {
		return ErrConflict
	}
	switch run.State {
	case "planning", "running", "awaiting_checks", "plan_running", "integrating":
		return ErrConflict
	}
	var active int
	err := w.store.db.QueryRow(`SELECT
	 (SELECT COUNT(*) FROM v2_plan_attempts WHERE run_id=? AND state IN ('running','verifying')) +
	 (SELECT COUNT(*) FROM v2_integrations WHERE run_id=? AND state='running') +
	 (SELECT COUNT(*) FROM v2_plan_drafts WHERE run_id=? AND state='running') +
	 (SELECT COUNT(*) FROM v2_result_applications WHERE run_id=? AND state IN ('applying','needs_attention'))`, run.ID, run.ID, run.ID, run.ID).Scan(&active)
	if err != nil {
		return err
	}
	if active != 0 {
		return ErrConflict
	}
	return nil
}

func (w *Worker) inspectCleanup(ctx context.Context, run Run, c CleanupCandidate) CleanupCandidate {
	block := func(reason string) CleanupCandidate { c.Eligible = false; c.Reason = reason; return c }
	if !ownedAttemptID.MatchString(c.ID) || c.Path != filepath.Join(w.root, c.ID, "workspace") {
		return block("소유 경로 확인 필요")
	}
	if c.State != "succeeded" && c.State != "failed" && c.State != "canceled" && c.State != "interrupted" {
		return block("실행이 끝나지 않음")
	}
	canonical, err := filepath.EvalSymlinks(c.Path)
	if err != nil {
		return block("작업 공간이 없거나 접근할 수 없음")
	}
	if canonical != c.Path {
		return block("경로가 다른 위치를 가리킴")
	}
	info, err := os.Lstat(filepath.Join(c.Path, ".git"))
	if err != nil || !info.Mode().IsRegular() {
		return block("별도 Git 작업 공간 확인 필요")
	}
	common, err := git(ctx, run.Path, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return block("원본 저장소 확인 실패")
	}
	other, err := git(ctx, c.Path, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil || strings.TrimSpace(other) != strings.TrimSpace(common) {
		return block("저장소가 일치하지 않음")
	}
	registered, err := git(ctx, run.Path, "worktree", "list", "--porcelain", "-z")
	if err != nil {
		return block("작업 공간 등록 확인 실패")
	}
	found := false
	for _, entry := range strings.Split(registered, "\x00\x00") {
		fields := strings.Split(entry, "\x00")
		if len(fields) == 0 || fields[0] != "worktree "+c.Path {
			continue
		}
		found = true
		for _, field := range fields {
			if strings.HasPrefix(field, "locked") || strings.HasPrefix(field, "branch ") || field == "bare" {
				return block("잠겼거나 브랜치가 연결된 작업 공간")
			}
		}
	}
	if !found {
		return block("작업 공간 등록이 일치하지 않음")
	}
	for _, name := range []string{"MERGE_HEAD", "CHERRY_PICK_HEAD", "REVERT_HEAD", "rebase-apply", "rebase-merge", "sequencer", "index.lock"} {
		p, err := git(ctx, c.Path, "rev-parse", "--git-path", name)
		if err != nil {
			return block("Git 작업 상태 확인 실패")
		}
		p = strings.TrimSpace(p)
		if !filepath.IsAbs(p) {
			p = filepath.Join(c.Path, p)
		}
		if _, err := os.Lstat(p); !os.IsNotExist(err) {
			return block("진행 중인 Git 작업 또는 충돌이 있음")
		}
	}
	index, err := git(ctx, c.Path, "ls-files", "--stage", "-z")
	if err != nil {
		return block("파일 목록 확인 실패")
	}
	for _, entry := range strings.Split(index, "\x00") {
		if entry == "" {
			continue
		}
		header, _, ok := strings.Cut(entry, "\t")
		fields := strings.Fields(header)
		if !ok || len(fields) != 3 || fields[2] != "0" || fields[0] == "160000" {
			return block("미해결 충돌 또는 하위 저장소가 있음")
		}
	}
	flags, err := git(ctx, c.Path, "ls-files", "-v", "-z")
	if err != nil {
		return block("파일 상태 확인 실패")
	}
	for _, entry := range strings.Split(flags, "\x00") {
		if entry != "" && (entry[0] == 'S' || entry[0] >= 'a' && entry[0] <= 'z') {
			return block("Git 변경 감지 제외 파일이 있음")
		}
	}
	// Without excludes, this includes ignored build products and local files too.
	extra, err := git(ctx, c.Path, "ls-files", "--others", "-z")
	if err != nil || extra != "" {
		return block("저장되지 않은 추가 파일이 있음")
	}
	head, err := git(ctx, c.Path, "rev-parse", "HEAD")
	if err != nil {
		return block("기준 커밋 확인 실패")
	}
	c.head = strings.TrimSpace(head)
	c.result = c.head
	if c.Kind != "draft" {
		artifact, err := w.store.Artifact(run.ID, c.ID, "result_commit")
		if err != nil && err != sql.ErrNoRows {
			return block("보관 결과 확인 실패")
		}
		if err == nil && artifact.BaseCommit != "" {
			c.result = artifact.BaseCommit
		}
	}
	// Compare index and worktree separately; opposing staged/local edits must
	// never cancel each other out and appear safe to remove.
	for _, args := range [][]string{{"diff", "--no-ext-diff", "--no-textconv", "--binary", "--cached", c.result, "--"}, {"diff", "--no-ext-diff", "--no-textconv", "--binary", "--"}} {
		args = append([]string{"-c", "core.fileMode=true", "-c", "core.fsmonitor=false", "-c", "core.ignoreStat=false"}, args...)
		diff, err := git(ctx, c.Path, args...)
		if err != nil || diff != "" {
			return block("저장된 커밋과 다른 변경이 있음")
		}
	}
	encoded, _ := json.Marshal([]string{run.ID, c.ID, c.Path, c.State, c.head, c.result, index})
	c.Fingerprint = fmt.Sprintf("%x", sha256.Sum256(encoded))
	c.Eligible = true
	c.Reason = "정리 가능 · 로그와 커밋 보존"
	return c
}

func (w *Worker) PreviewCleanup(id, before string) (CleanupPreview, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	run, err := w.store.Get(id)
	if err != nil {
		return CleanupPreview{}, err
	}
	if err := w.cleanupIdle(run); err != nil {
		return CleanupPreview{}, err
	}
	out, err := w.cleanupRecords(id, before)
	if err != nil {
		return out, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	for i, c := range out.Candidates {
		out.Candidates[i] = w.inspectCleanup(ctx, run, c)
	}
	return out, nil
}

func (w *Worker) CleanupWorkspace(id, attempt, fingerprint string) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if !ownedAttemptID.MatchString(attempt) || len(fingerprint) != 64 {
		return ErrInvalid
	}
	run, err := w.store.Get(id)
	if err != nil {
		return err
	}
	if err := w.cleanupIdle(run); err != nil {
		return err
	}
	// Reuse the scoped registry; never accept a filesystem path from the client.
	var candidate *CleanupCandidate
	cursor := ""
	for {
		page, err := w.cleanupRecords(id, cursor)
		if err != nil {
			return err
		}
		for _, c := range page.Candidates {
			if c.ID == attempt {
				copy := c
				candidate = &copy
				break
			}
		}
		if candidate != nil || page.NextCursor == "" {
			break
		}
		cursor = page.NextCursor
	}
	if candidate == nil {
		return ErrInvalid
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	c := w.inspectCleanup(ctx, run, *candidate)
	if !c.Eligible || c.Fingerprint != fingerprint {
		return fmt.Errorf("%w: cleanup preview changed; refresh before retrying", ErrConflict)
	}
	// Retain both input HEAD and result before removing their worktree reachability.
	for name, commit := range map[string]string{"head": c.head, "result": c.result} {
		ref := "refs/powercodedeck/retained/" + c.ID + "/" + name
		current, err := git(ctx, run.Path, "rev-parse", "--verify", ref)
		if err == nil {
			if strings.TrimSpace(current) != commit {
				return ErrConflict
			}
			continue
		}
		if _, err := git(ctx, run.Path, "update-ref", ref, commit, ""); err != nil {
			return err
		}
	}
	// --force permits staged changes already proven identical to a retained
	// result. Never use a second force to override a locked worktree.
	_, err = git(ctx, run.Path, "worktree", "remove", "--force", c.Path)
	return err
}
