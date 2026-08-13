import { useEffect, useState } from 'react';
import { agentDeckWS } from '../../lib/ws';
import { IconClose, IconPlus, IconWarning } from '../icons';

/** Mirrors the server's NativeSessionOptions wire shape. */
export interface SessionOptions {
  addDirs: string[];
  maxBudgetUsd: number;
  autocompact: string;
  fallbackModel: string;
}

export const EMPTY_OPTIONS: SessionOptions = {
  addDirs: [],
  maxBudgetUsd: 0,
  autocompact: '',
  fallbackModel: '',
};

/** Tolerant of a server that hasn't been taught a field yet, or an older payload. */
export function parseSessionOptions(raw: unknown): SessionOptions {
  const o = (raw ?? {}) as Partial<SessionOptions>;
  return {
    addDirs: Array.isArray(o.addDirs) ? o.addDirs.filter((d) => typeof d === 'string') : [],
    maxBudgetUsd: typeof o.maxBudgetUsd === 'number' ? o.maxBudgetUsd : 0,
    autocompact: typeof o.autocompact === 'string' ? o.autocompact : '',
    fallbackModel: typeof o.fallbackModel === 'string' ? o.fallbackModel : '',
  };
}

/** True when nothing is configured — lets the toolbar show a dot only when it matters. */
export function hasSessionOptions(o: SessionOptions): boolean {
  return o.addDirs.length > 0 || o.maxBudgetUsd > 0 || o.autocompact !== '' || o.fallbackModel !== '';
}

interface Props {
  agentId: string;
  /** What the session is actually running with — the server is the source of truth. */
  current: SessionOptions;
  /** Whatever the last save dropped in validation, so the user learns WHICH value failed. */
  dropped: string[];
  onClose: () => void;
}

/**
 * SessionOptionsSheet — the set-once session settings, in one place.
 *
 * Everything here is a CLI spawn flag, so saving restarts the session on the same
 * conversation. That's said out loud in the footer rather than discovered: a restart
 * that drops an in-flight turn is a surprise worth spending a sentence on.
 */
export function SessionOptionsSheet({ agentId, current, dropped, onClose }: Props) {
  // Edited locally and only pushed on 저장 — a per-keystroke save would restart the
  // session on every character typed into the budget box.
  const [dirs, setDirs] = useState<string[]>(current.addDirs);
  const [newDir, setNewDir] = useState('');
  const [budget, setBudget] = useState(current.maxBudgetUsd ? String(current.maxBudgetUsd) : '');
  const [compactMode, setCompactMode] = useState(
    current.autocompact === '' ? 'default' : current.autocompact === 'auto' ? 'auto' : 'tokens',
  );
  const [compactTokens, setCompactTokens] = useState(
    current.autocompact && current.autocompact !== 'auto' ? current.autocompact : '200000',
  );
  const [fallback, setFallback] = useState(current.fallbackModel);
  const [saving, setSaving] = useState(false);

  // The server confirms by broadcasting the stored options back. Close only then, so a
  // rejected value is still on screen next to the reason it was rejected.
  useEffect(() => {
    if (!saving) return;
    setSaving(false);
    if (!dropped.length) onClose();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [current, dropped]);

  const addDir = () => {
    const d = newDir.trim();
    if (!d || dirs.includes(d)) return;
    setDirs([...dirs, d]);
    setNewDir('');
  };

  const save = () => {
    setSaving(true);
    agentDeckWS.send('native:setOptions', {
      agentId,
      options: {
        addDirs: dirs,
        maxBudgetUsd: Number(budget) || 0,
        autocompact: compactMode === 'default' ? '' : compactMode === 'auto' ? 'auto' : compactTokens.trim(),
        fallbackModel: fallback.trim(),
      },
    });
  };

  const field = 'w-full bg-deck-bg border border-deck-border rounded-lg px-2.5 py-1.5 text-sm text-deck-text';
  const label = 'block text-xs font-medium text-deck-text mb-1';
  const hint = 'block text-[11px] text-deck-text-dim leading-snug mt-1';

  return (
    <>
      <div className="fixed inset-0 bg-black/50 z-40" onClick={onClose} />
      <div className="fixed inset-x-0 bottom-0 sm:inset-0 sm:m-auto sm:h-fit sm:max-h-[85dvh] sm:w-[30rem] z-50 max-h-[85dvh] overflow-y-auto rounded-t-xl sm:rounded-xl bg-deck-surface border border-deck-border shadow-xl safe-bottom">
        <div className="sticky top-0 flex items-center justify-between px-4 py-3 bg-deck-surface border-b border-deck-border">
          <span className="text-sm font-medium text-deck-text">세션 옵션</span>
          <button onClick={onClose} className="p-1 rounded hover:bg-deck-border/50 text-deck-text-dim">
            <IconClose size={14} />
          </button>
        </div>

        <div className="p-4 space-y-5">
          {dropped.length > 0 && (
            <div className="rounded-lg border border-amber-400/40 bg-amber-400/10 p-2.5 space-y-1">
              <div className="flex items-center gap-1.5 text-xs font-medium text-amber-300">
                <IconWarning size={13} /> 적용되지 않은 값
              </div>
              {dropped.map((d, i) => (
                <div key={i} className="text-[11px] text-amber-200/90 leading-snug">{d}</div>
              ))}
            </div>
          )}

          <div>
            <label className={label}>추가 작업 경로</label>
            <div className="space-y-1.5">
              {dirs.map((d) => (
                <div key={d} className="flex items-center gap-1.5">
                  <span className="flex-1 min-w-0 truncate text-xs text-deck-text-dim bg-deck-bg border border-deck-border rounded-lg px-2.5 py-1.5">{d}</span>
                  <button
                    onClick={() => setDirs(dirs.filter((x) => x !== d))}
                    className="shrink-0 p-1.5 rounded-lg border border-deck-border text-deck-text-dim hover:text-red-400"
                    title="제거"
                  >
                    <IconClose size={12} />
                  </button>
                </div>
              ))}
              <div className="flex items-center gap-1.5">
                <input
                  value={newDir}
                  onChange={(e) => setNewDir(e.target.value)}
                  onKeyDown={(e) => { if (e.key === 'Enter') { e.preventDefault(); addDir(); } }}
                  placeholder="/home/me/other-repo"
                  className={field}
                />
                <button onClick={addDir} className="shrink-0 p-1.5 rounded-lg border border-deck-border text-deck-text-dim" title="추가">
                  <IconPlus size={14} />
                </button>
              </div>
            </div>
            <span className={hint}>
              작업 디렉터리 밖에서 읽고 쓸 수 있는 경로입니다. 절대 경로만 가능하고, 저장할 때 서버가 존재 여부를 확인합니다.
            </span>
          </div>

          <div>
            <label className={label}>예산 상한 (USD)</label>
            <input
              value={budget}
              onChange={(e) => setBudget(e.target.value.replace(/[^0-9.]/g, ''))}
              inputMode="decimal"
              placeholder="없음"
              className={field}
            />
            <span className={hint}>이 세션이 API에 쓸 수 있는 최대 금액입니다. 비워두면 상한이 없습니다.</span>
          </div>

          <div>
            <label className={label}>자동 압축</label>
            <div className="flex gap-1.5">
              {[
                { id: 'default', label: '기본' },
                { id: 'auto', label: 'Auto' },
                { id: 'tokens', label: '직접 지정' },
              ].map((o) => (
                <button
                  key={o.id}
                  onClick={() => setCompactMode(o.id)}
                  className={`flex-1 py-1.5 rounded-lg border text-xs ${
                    compactMode === o.id
                      ? 'border-deck-accent bg-deck-accent/10 text-deck-accent'
                      : 'border-deck-border text-deck-text-dim'
                  }`}
                >
                  {o.label}
                </button>
              ))}
            </div>
            {compactMode === 'tokens' && (
              <input
                value={compactTokens}
                onChange={(e) => setCompactTokens(e.target.value.replace(/[^0-9]/g, ''))}
                inputMode="numeric"
                className={`${field} mt-1.5`}
              />
            )}
            <span className={hint}>대화가 길어질 때 이전 맥락을 요약할 시점입니다. 직접 지정은 100,000~1,000,000 토큰입니다.</span>
          </div>

          <div>
            <label className={label}>폴백 모델</label>
            <input
              value={fallback}
              onChange={(e) => setFallback(e.target.value)}
              placeholder="claude-sonnet-5"
              className={field}
            />
            <span className={hint}>
              기본 모델이 과부하일 때 대신 쓸 모델입니다. 쉼표로 여러 개를 적으면 순서대로 시도합니다.
            </span>
          </div>
        </div>

        <div className="sticky bottom-0 bg-deck-surface border-t border-deck-border px-4 py-3 space-y-2">
          <p className="text-[11px] text-deck-text-dim leading-snug">
            저장하면 세션이 재시작됩니다. 대화는 이어지지만 진행 중인 작업은 중단됩니다.
          </p>
          <div className="flex gap-2">
            <button onClick={onClose} className="flex-1 py-2 rounded-lg border border-deck-border text-sm text-deck-text-dim">
              취소
            </button>
            <button onClick={save} className="flex-1 py-2 rounded-lg bg-deck-accent text-sm font-medium text-deck-bg">
              저장하고 재시작
            </button>
          </div>
        </div>
      </div>
    </>
  );
}
