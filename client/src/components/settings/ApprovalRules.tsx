import { useCallback, useEffect, useState } from 'react';
import { api } from '../../lib/api';
import { IconTrash } from '../icons';

/**
 * ApprovalRules — "항상 허용" 규칙 목록 및 삭제.
 *
 * 저장한 권한을 볼 수 없으면 시간이 지나며 위험 요소가 된다. 규칙 하나하나가 승인
 * 프롬프트를 침묵시키기 때문에 목록 조회·삭제는 이 기능의 선택 사항이 아니라 일부다.
 */

interface ApprovalRule {
  id: number;
  workingDir: string;
  toolName: string;
  target: string;
  createdAt: string;
}

export function ApprovalRules() {
  const [rules, setRules] = useState<ApprovalRule[]>([]);
  const [loading, setLoading] = useState(true);

  const load = useCallback(async () => {
    setLoading(true);
    try {
      const data = await api.listApprovalRules();
      // 서버가 500을 반환하면 배열이 아닐 수 있으므로 방어적으로 처리한다.
      setRules(Array.isArray(data) ? (data as ApprovalRule[]) : []);
    } catch (err) {
      console.error('Failed to load approval rules:', err);
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => { load(); }, [load]);

  const remove = useCallback(async (id: number) => {
    try {
      await api.deleteApprovalRule(id);
      // DELETE /api/approval-rules/{id}는 규칙이 없어도 204를 반환한다(SQLite rows-affected
      // 미확인). 서버가 실제로 삭제했다는 보장은 없지만, 낙관적으로 제거하는 것이 옳다:
      // 이미 사라진 규칙을 목록에 남겨두는 것이 더 혼란스럽다.
      setRules((prev) => prev.filter((r) => r.id !== id));
    } catch (err) {
      console.error('Failed to delete approval rule:', err);
    }
  }, []);

  // 프로젝트(workingDir)별로 묶는다: 신뢰는 저장소 단위로 형성되므로 그 단위로 읽히는 게 자연스럽다.
  const byDir = rules.reduce<Record<string, ApprovalRule[]>>((acc, r) => {
    (acc[r.workingDir] ||= []).push(r);
    return acc;
  }, {});

  return (
    <div className="p-3 card space-y-2">
      <div className="text-sm font-medium">항상 허용 규칙</div>
      <div className="text-xs text-deck-text-dim">
        승인 카드에서 "항상 허용"을 누르면 이 목록에 쌓입니다. 규칙은 프로젝트 범위이며 해당
        프로젝트에서 해당 도구의 승인 프롬프트를 생략합니다.
      </div>
      {loading ? (
        <div className="text-xs text-deck-text-dim">불러오는 중…</div>
      ) : rules.length === 0 ? (
        <div className="text-xs text-deck-text-dim">저장된 규칙이 없습니다.</div>
      ) : (
        <div className="space-y-2">
          {Object.entries(byDir).map(([dir, list]) => (
            <div key={dir} className="rounded-lg border border-deck-border">
              {/* 프로젝트 경로 — 규칙이 어느 저장소에 속하는지 한눈에 알 수 있도록 맨 위에 표시 */}
              <div className="px-3 py-1.5 text-[11px] font-mono text-deck-text-dim border-b border-deck-border/50 truncate">
                {dir}
              </div>
              {list.map((r) => (
                <div key={r.id} className="flex items-center gap-2 px-3 py-1.5 text-xs">
                  <span className="font-mono text-deck-accent-light shrink-0">{r.toolName}</span>
                  {/* target이 비어 있으면 도구 전체에 적용되는 규칙이다.
                      NativeChat의 "항상 허용" 설명과 같은 표현을 쓴다:
                      "도구 전체를 다시 묻지 않습니다". */}
                  <span className="font-mono text-deck-text-dim truncate flex-1">
                    {r.target || '도구 전체'}
                  </span>
                  <button
                    onClick={() => remove(r.id)}
                    className="shrink-0 p-1 rounded text-deck-text-dim hover:text-red-400"
                    title="이 규칙 삭제"
                  >
                    <IconTrash size={13} />
                  </button>
                </div>
              ))}
            </div>
          ))}
        </div>
      )}
    </div>
  );
}
