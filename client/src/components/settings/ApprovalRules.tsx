import { useCallback, useEffect, useRef, useState } from 'react';
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
  // 불러오기 실패 여부 — "규칙 없음"과 구별하기 위해 별도 상태로 관리한다.
  // 에러 상태에서 빈 배열을 렌더하면 사용자가 실제로 규칙이 없다고 오해한다.
  const [loadError, setLoadError] = useState(false);
  // 삭제 실패한 규칙 id 집합 — 행 단위로 에러를 표시하고 성공하면 지운다.
  const [deleteErrors, setDeleteErrors] = useState<Set<number>>(new Set());
  // 언마운트 후 setState 호출을 막는다.
  const mountedRef = useRef(true);
  useEffect(() => {
    mountedRef.current = true;
    return () => { mountedRef.current = false; };
  }, []);

  const load = useCallback(async () => {
    setLoading(true);
    setLoadError(false);
    try {
      const data = await api.listApprovalRules();
      if (!mountedRef.current) return;
      // 서버가 500을 반환하면 배열이 아닐 수 있으므로 방어적으로 처리한다.
      // (네트워크 에러로 reject된 경우는 아래 catch에서 처리한다.)
      setRules(Array.isArray(data) ? (data as ApprovalRule[]) : []);
    } catch (err) {
      console.error('Failed to load approval rules:', err);
      if (!mountedRef.current) return;
      // 불러오기 실패는 명시적으로 표시한다: 규칙이 없는 것처럼 보이면 안 된다.
      setLoadError(true);
    } finally {
      if (mountedRef.current) setLoading(false);
    }
  }, []);

  useEffect(() => { load(); }, [load]);

  const remove = useCallback(async (id: number) => {
    // 이전 에러가 있으면 재시도로 보고 에러 표시를 지운다.
    setDeleteErrors((prev) => {
      if (!prev.has(id)) return prev;
      const next = new Set(prev);
      next.delete(id);
      return next;
    });
    try {
      await api.deleteApprovalRule(id);
      if (!mountedRef.current) return;
      // DELETE /api/approval-rules/{id}는 규칙이 없어도 204를 반환한다(SQLite rows-affected
      // 미확인). 서버가 실제로 삭제했다는 보장은 없지만, 낙관적으로 제거하는 것이 옳다:
      // 이미 사라진 규칙을 목록에 남겨두는 것이 더 혼란스럽다.
      // 성공 토스트는 없다 — 204가 "실제 삭제됨"을 보장하지 않으므로 확인 메시지는 거짓말이 된다.
      setRules((prev) => prev.filter((r) => r.id !== id));
    } catch (err) {
      console.error('Failed to delete approval rule:', err);
      if (!mountedRef.current) return;
      // 삭제 실패는 해당 행에 표시한다: 행이 사라지지 않았으니 재시도할 수 있다.
      setDeleteErrors((prev) => new Set(prev).add(id));
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
      ) : loadError ? (
        // 불러오기 실패 — "저장된 규칙이 없습니다"처럼 보이면 감사 목적과 정반대가 된다.
        <div className="text-xs text-deck-text-dim">
          규칙을 불러오지 못했습니다.{' '}
          <button
            onClick={load}
            className="underline underline-offset-2 hover:text-deck-text"
          >
            다시 시도
          </button>
        </div>
      ) : rules.length === 0 ? (
        <div className="text-xs text-deck-text-dim">저장된 규칙이 없습니다.</div>
      ) : (
        <div className="space-y-2">
          {Object.entries(byDir).map(([dir, list]) => (
            <div key={dir} className="rounded-lg border border-deck-border">
              {/* 프로젝트 경로 — 좁은 화면에서는 잘리므로 title로 전체 경로를 노출한다.
                  마지막 경로 세그먼트(프로젝트 폴더명)를 앞에 굵게 표시하고, 상위 경로는
                  흐리게 줄여 375px 컬럼에서도 핵심 정보가 보이게 했다. */}
              <div
                className="px-3 py-1.5 text-[11px] font-mono text-deck-text-dim border-b border-deck-border/50 truncate"
                title={dir}
              >
                <span className="text-deck-text font-semibold">
                  {dir.split('/').pop() || dir}
                </span>
                <span className="opacity-60">{' · '}{dir}</span>
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
                  {/* 삭제 실패 시 행 우측에 짧은 안내 + 재시도 버튼을 표시한다.
                      성공 피드백은 없다 — 204가 실제 삭제를 보장하지 않으므로. */}
                  {deleteErrors.has(r.id) && (
                    <span className="text-[11px] text-red-400 shrink-0">삭제 실패</span>
                  )}
                  <button
                    onClick={() => remove(r.id)}
                    className="shrink-0 p-1 rounded text-deck-text-dim hover:text-red-400"
                    title={deleteErrors.has(r.id) ? '다시 시도' : '이 규칙 삭제'}
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
