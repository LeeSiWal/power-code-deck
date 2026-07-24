import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { api } from '../../lib/api';
import { IconClose, IconCheck, IconSpinner, IconRefresh, IconWarning } from '../icons';

type Plugin = {
  ref: string;
  name: string;
  marketplace: string;
  description: string;
  category?: string;
  installed: boolean;
  enabled: boolean;
  supported: boolean;
  skills?: number;
  commands?: number;
};

interface PluginsPanelProps {
  agentId: string;
  onClose: () => void;
  /** Prefill the search box — e.g. the text after "/plugin " the user typed. */
  initialQuery?: string;
}

/**
 * PluginsPanel — the deck's answer to `/plugin`, which the CLI refuses over the
 * stream protocol. Lists installed plugins with an enable/disable toggle, and lets
 * any plugin in the checked-out marketplaces be searched and installed. Because the
 * CLI only loads plugins at session start, a change here needs a session restart to
 * take effect — the banner makes that one tap.
 */
export function PluginsPanel({ agentId, onClose, initialQuery = '' }: PluginsPanelProps) {
  const [all, setAll] = useState<Plugin[] | null>(null);
  const [query, setQuery] = useState(initialQuery.trim());
  const [busyRef, setBusyRef] = useState<string | null>(null);
  const [dirty, setDirty] = useState(false); // a change was made → restart to apply
  const [restarting, setRestarting] = useState(false);
  const [err, setErr] = useState('');
  const searchRef = useRef<HTMLInputElement>(null);

  const load = useCallback(() => {
    api.listPlugins().then(setAll).catch((e) => setErr(String(e)));
  }, []);
  useEffect(() => { load(); }, [load]);
  useEffect(() => { searchRef.current?.focus(); }, []);

  const installed = useMemo(() => (all ?? []).filter((p) => p.installed), [all]);

  // Search over everything, but only once the user has typed something — the full
  // marketplace can be hundreds of entries and rendering it all is pointless.
  const matches = useMemo(() => {
    const q = query.trim().toLowerCase();
    if (q.length < 2 || !all) return [];
    return all
      .filter((p) => p.name.toLowerCase().includes(q) || p.description.toLowerCase().includes(q))
      .slice(0, 40);
  }, [query, all]);

  const install = useCallback(async (ref: string) => {
    setBusyRef(ref);
    setErr('');
    try {
      await api.installPlugin(ref);
      setDirty(true);
      load();
    } catch (e) {
      setErr(ref + ' 설치 실패: ' + String(e));
    } finally {
      setBusyRef(null);
    }
  }, [load]);

  const toggle = useCallback(async (p: Plugin) => {
    setBusyRef(p.ref);
    setErr('');
    try {
      await api.togglePlugin(p.ref, !p.enabled);
      setDirty(true);
      load();
    } catch (e) {
      setErr(p.ref + ' 변경 실패: ' + String(e));
    } finally {
      setBusyRef(null);
    }
  }, [load]);

  const restart = useCallback(async () => {
    setRestarting(true);
    try {
      await api.restartAgent(agentId);
      setDirty(false);
    } catch (e) {
      setErr('재시작 실패: ' + String(e));
    } finally {
      setRestarting(false);
    }
  }, [agentId]);

  const row = (p: Plugin, mode: 'installed' | 'search') => (
    <div key={p.ref} className="flex items-start gap-2 px-3 py-2 border-b border-deck-border/60">
      <div className="min-w-0 flex-1">
        <div className="flex items-center gap-1.5">
          <span className="text-sm text-deck-text truncate">{p.name}</span>
          <span className="text-[10px] text-deck-text-dim shrink-0">@{p.marketplace}</span>
          {p.enabled && (
            <span className="text-[10px] px-1 rounded bg-emerald-400/15 text-emerald-300 shrink-0">활성</span>
          )}
        </div>
        {p.description && <div className="text-xs text-deck-text-dim line-clamp-2">{p.description}</div>}
        {p.installed && (p.skills || p.commands) ? (
          <div className="text-[10px] text-deck-text-dim mt-0.5">
            {p.skills ? `스킬 ${p.skills}` : ''}{p.skills && p.commands ? ' · ' : ''}
            {p.commands ? `명령 ${p.commands}` : ''}
          </div>
        ) : null}
      </div>
      <div className="shrink-0 pt-0.5">
        {busyRef === p.ref ? (
          <span className="text-deck-text-dim"><IconSpinner size={16} className="animate-spin" /></span>
        ) : p.installed ? (
          // Installed → an enable/disable switch.
          <button
            onClick={() => toggle(p)}
            className={`text-xs px-2 py-1 rounded transition-colors ${
              p.enabled
                ? 'bg-emerald-400/15 text-emerald-300 hover:bg-emerald-400/25'
                : 'bg-deck-bg text-deck-text-dim hover:bg-deck-border/40'
            }`}
          >
            {p.enabled ? '비활성화' : '활성화'}
          </button>
        ) : mode === 'search' ? (
          <button
            onClick={() => install(p.ref)}
            disabled={!p.supported}
            className="text-xs px-2 py-1 rounded bg-deck-accent/20 text-deck-accent hover:bg-deck-accent/30 disabled:opacity-40"
            title={p.supported ? '설치 후 활성화' : '이 소스 형식은 아직 지원되지 않습니다'}
          >
            설치
          </button>
        ) : null}
      </div>
    </div>
  );

  return (
    <>
      <div className="fixed inset-0 bg-black/50 z-[60]" onClick={onClose} />
      <div className="fixed left-1/2 top-1/2 -translate-x-1/2 -translate-y-1/2 z-[61] w-[min(92vw,560px)] max-h-[85dvh] flex flex-col bg-deck-surface border border-deck-border rounded-xl shadow-2xl overflow-hidden">
        {/* Header */}
        <div className="flex items-center gap-2 px-3 py-2 border-b border-deck-border shrink-0">
          <span className="font-medium text-sm">플러그인</span>
          <span className="text-[11px] text-deck-text-dim">설치 · 활성화 · 검색</span>
          <button onClick={onClose} className="ml-auto p-1 rounded hover:bg-deck-border/30 text-deck-text-dim">
            <IconClose size={16} />
          </button>
        </div>

        {/* Restart-to-apply banner — plugins load at session start. */}
        {dirty && (
          <div className="flex items-center gap-2 px-3 py-2 bg-amber-400/10 text-amber-300 text-xs border-b border-amber-400/20 shrink-0">
            <IconWarning size={14} className="shrink-0" />
            <span className="flex-1">변경사항은 세션을 재시작해야 적용됩니다.</span>
            <button
              onClick={restart}
              disabled={restarting}
              className="px-2 py-1 rounded bg-amber-400/20 hover:bg-amber-400/30 inline-flex items-center gap-1 disabled:opacity-50"
            >
              {restarting ? <IconSpinner size={13} className="animate-spin" /> : <IconRefresh size={13} />}
              세션 재시작
            </button>
          </div>
        )}

        {err && (
          <div className="px-3 py-2 bg-red-500/15 text-red-400 text-xs border-b border-red-500/20 shrink-0 flex items-start gap-2">
            <span className="flex-1">{err}</span>
            <button onClick={() => setErr('')} className="opacity-60 shrink-0">닫기</button>
          </div>
        )}

        {/* Search */}
        <div className="px-3 py-2 border-b border-deck-border shrink-0">
          <input
            ref={searchRef}
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            placeholder="플러그인 검색 — 이름 또는 설명 (2글자 이상)"
            className="w-full px-2.5 py-1.5 rounded-md bg-deck-bg border border-deck-border text-sm text-deck-text placeholder:text-deck-text-dim focus:outline-none focus:border-deck-accent/60"
          />
        </div>

        <div className="flex-1 min-h-0 overflow-y-auto">
          {all === null ? (
            <div className="text-center text-deck-text-dim text-sm py-10">
              <IconSpinner size={18} className="animate-spin inline" /> 불러오는 중…
            </div>
          ) : query.trim().length >= 2 ? (
            matches.length ? (
              matches.map((p) => row(p, 'search'))
            ) : (
              <div className="text-center text-deck-text-dim text-sm py-10">일치하는 플러그인이 없습니다.</div>
            )
          ) : (
            <>
              <div className="px-3 py-1.5 text-[10px] uppercase tracking-wide text-deck-text-dim bg-deck-bg/40">
                설치됨 ({installed.length})
              </div>
              {installed.length ? (
                installed.map((p) => row(p, 'installed'))
              ) : (
                <div className="text-center text-deck-text-dim text-sm py-8 px-4">
                  설치된 플러그인이 없습니다. 위에서 검색해 설치하세요.
                </div>
              )}
            </>
          )}
        </div>
      </div>
    </>
  );
}
