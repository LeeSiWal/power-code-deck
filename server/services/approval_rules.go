package services

import (
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
)

// ApprovalRuleStore는 "앞으로도 허용" 규칙을 보관한다.
//
// 규칙은 (작업 디렉토리, 도구, 대상) 완전 일치다. 패턴을 쓰지 않는 이유는 예측
// 가능성이다 — 규칙이 무엇을 허용할지 예측할 수 없으면 감사할 수도 없다. 실제
// 성가심의 대부분은 반복되는 동일 명령(go test ./..., npm run build)이고, 인자가
// 매번 다른 호출(git commit -m "…")은 오히려 확인해야 하는 쪽이다.
type ApprovalRuleStore struct {
	db *sql.DB
}

type ApprovalRule struct {
	ID         int64  `json:"id"`
	WorkingDir string `json:"workingDir"`
	ToolName   string `json:"toolName"`
	Target     string `json:"target"`
	CreatedAt  string `json:"createdAt"`
}

// ErrUnsafeRule은 위험 판정된 호출을 규칙으로 저장하려 할 때 반환된다.
var ErrUnsafeRule = errors.New("위험한 호출은 규칙으로 저장할 수 없습니다")

// ErrNoRuleTarget은 입력에서 대상을 뽑지 못했을 때 반환된다.
var ErrNoRuleTarget = errors.New("규칙 대상을 확인할 수 없습니다")

func NewApprovalRuleStore(db *sql.DB) *ApprovalRuleStore {
	return &ApprovalRuleStore{db: db}
}

// RuleTarget은 이 호출을 식별하는 문자열을 뽑는다. 두 번째 반환값이 false면 규칙을
// 만들 수 없다 — 빈 대상으로 저장하면 그 도구 전체를 허용하는 규칙이 되어 사용자가
// 의도한 것보다 훨씬 넓어진다.
//
// 정규화는 최소한만 한다: 셸 명령은 공백 정리, 경로는 절대경로 + Clean. 그 이상
// 영리해질수록 무엇이 허용되는지 예측하기 어려워진다.
func RuleTarget(tool string, input json.RawMessage, cwd string) (string, bool) {
	switch tool {
	case "Bash", "BashOutput":
		cmd := firstStringField(input, "command")
		cmd = strings.Join(strings.Fields(cmd), " ")
		if cmd == "" {
			return "", false
		}
		return cmd, true
	case "Write", "Edit", "MultiEdit", "NotebookEdit":
		p := firstStringField(input, "file_path", "notebook_path", "path")
		if p == "" {
			return "", false
		}
		if !filepath.IsAbs(p) {
			p = filepath.Join(cwd, p)
		}
		return filepath.Clean(p), true
	}
	// 대상 개념이 없는 도구는 도구 이름 자체가 최소 단위다.
	// 주의: 이 분기는 IsSafeToolCall에는 있지만 이 switch에는 없는 도구를 처리한다.
	// 빈 target으로 저장하면 그 도구 전체를 허용하는 규칙이 되어 사용자가 의도한
	// 것보다 훨씬 넓어진다. IsSafeToolCall에 새 도구를 추가할 때는 반드시 이 switch
	// 에도 추가해야 한다. 한쪽만 고치면 의도보다 훨씬 넓은 규칙이 조용히 만들어진다.
	return "", true
}

// Allows는 저장된 규칙이 이 호출을 허용하는지 본다.
//
// 규칙이 있어도 안전 판정을 다시 한다. 저장 시점 검사만 두면, 나중에 위험 목록이
// 넓어졌을 때 이미 저장된 규칙이 그 확장을 조용히 우회한다.
func (s *ApprovalRuleStore) Allows(workingDir, tool string, input json.RawMessage) bool {
	dir := filepath.Clean(workingDir)
	if !IsSafeToolCall(tool, input, dir) {
		return false
	}
	target, ok := RuleTarget(tool, input, dir)
	if !ok {
		return false
	}
	var n int
	err := s.db.QueryRow(
		`SELECT COUNT(*) FROM approval_rules WHERE working_dir = ? AND tool_name = ? AND target = ?`,
		dir, tool, target,
	).Scan(&n)
	// 조회 실패는 "규칙 없음"으로 처리한다 — 사람에게 묻는 쪽으로 실패한다.
	return err == nil && n > 0
}

func (s *ApprovalRuleStore) Save(workingDir, tool string, input json.RawMessage) error {
	dir := filepath.Clean(workingDir)
	if !IsSafeToolCall(tool, input, dir) {
		return ErrUnsafeRule
	}
	target, ok := RuleTarget(tool, input, dir)
	if !ok {
		return ErrNoRuleTarget
	}
	_, err := s.db.Exec(
		`INSERT OR IGNORE INTO approval_rules (working_dir, tool_name, target) VALUES (?, ?, ?)`,
		dir, tool, target,
	)
	return err
}

func (s *ApprovalRuleStore) List() ([]ApprovalRule, error) {
	rows, err := s.db.Query(
		`SELECT id, working_dir, tool_name, target, COALESCE(created_at, '')
		   FROM approval_rules ORDER BY working_dir, tool_name, target`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ApprovalRule{}
	for rows.Next() {
		var r ApprovalRule
		if err := rows.Scan(&r.ID, &r.WorkingDir, &r.ToolName, &r.Target, &r.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *ApprovalRuleStore) Delete(id int64) error {
	_, err := s.db.Exec(`DELETE FROM approval_rules WHERE id = ?`, id)
	return err
}
