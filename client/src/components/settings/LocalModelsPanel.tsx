import { useCallback, useEffect, useState } from 'react';
import { api, isFeatureOff, type LocalModelEntry } from '../../lib/api';
import { localModelName } from '../../lib/routingLabels';

/**
 * Settings → 로컬 모델: connect a model server on this machine, the home network
 * or Tailscale (the server refuses anything else), pick its model, and choose
 * what it does — handle easy requests (a local Codex profile) and/or rate
 * request difficulty (the tier judge). Saved to routing.json, applied live.
 */
type Form = { id: string; url: string; kind: string; model: string; useForEasy: boolean; useAsJudge: boolean; editing: boolean };

const emptyForm: Form = { id: '', url: '', kind: 'openai', model: '', useForEasy: true, useAsJudge: true, editing: false };

export function LocalModelsPanel() {
  const [entries, setEntries] = useState<LocalModelEntry[] | null>(null);
  const [state, setState] = useState<'loading' | 'off' | 'error' | 'ok'>('loading');
  const [form, setForm] = useState<Form | null>(null);
  const [models, setModels] = useState<string[]>([]);
  const [busy, setBusy] = useState<'' | 'test' | 'save'>('');
  const [msg, setMsg] = useState<{ ok: boolean; text: string } | null>(null);
  const [confirmDelete, setConfirmDelete] = useState('');

  const load = useCallback(async () => {
    try {
      const r = await api.localModels();
      setEntries(r.endpoints);
      setState('ok');
    } catch (err) {
      setState(isFeatureOff(err) ? 'off' : 'error');
    }
  }, []);
  useEffect(() => { load(); }, [load]);

  const edit = (e?: LocalModelEntry) => {
    setMsg(null);
    setModels(e?.models ?? []);
    // A new server becomes the difficulty judge only if there is none yet:
    // there is one judge, and silently replacing it would be a surprise.
    const hasJudge = (entries ?? []).some((x) => x.useAsJudge);
    setForm(e ? { id: e.id, url: e.url, kind: e.kind, model: e.model, useForEasy: e.useForEasy, useAsJudge: e.useAsJudge, editing: true } : { ...emptyForm, useAsJudge: !hasJudge });
  };

  const test = async () => {
    if (!form) return;
    setBusy('test');
    setMsg(null);
    try {
      const r = await api.testLocalModel(form.url, form.kind);
      setModels(r.models);
      setForm({ ...form, model: form.model && r.models.includes(form.model) ? form.model : (r.models[0] ?? '') });
      setMsg({ ok: true, text: r.models.length ? `연결됨 · 모델 ${r.models.length}개` : '연결됐지만 모델 목록이 비어 있습니다' });
    } catch (err) {
      setMsg({ ok: false, text: errText(err) });
    } finally {
      setBusy('');
    }
  };

  const save = async () => {
    if (!form) return;
    setBusy('save');
    setMsg(null);
    try {
      const r = await api.saveLocalModel(form.id.trim(), { url: form.url, kind: form.kind, model: form.model, useForEasy: form.useForEasy, useAsJudge: form.useAsJudge });
      setEntries(r.endpoints);
      setForm(null);
      setMsg({ ok: true, text: '저장했습니다. 재시작 없이 바로 적용됩니다.' });
    } catch (err) {
      setMsg({ ok: false, text: errText(err) });
    } finally {
      setBusy('');
    }
  };

  const remove = async (id: string) => {
    if (confirmDelete !== id) { setConfirmDelete(id); return; }
    setConfirmDelete('');
    try {
      await api.deleteLocalModel(id);
      await load();
      setMsg({ ok: true, text: `${id}을(를) 삭제했습니다.` });
    } catch (err) {
      setMsg({ ok: false, text: errText(err) });
    }
  };

  const idValid = /^[a-z0-9][a-z0-9-]{0,31}$/.test(form?.id ?? '');
  const canSave = !!form && idValid && form.url.trim() !== '' && (!form.useForEasy || (form.kind === 'openai' && form.model !== ''));

  return (
    <section className="min-w-0 rounded-xl border border-deck-border bg-deck-surface p-4 space-y-3 [overflow-wrap:anywhere]">
      <div className="flex items-center gap-2">
        <h2 className="text-sm font-semibold">로컬 모델</h2>
        {state === 'ok' && !form && <button className="ml-auto shrink-0 text-xs underline" onClick={() => edit()}>+ 추가</button>}
      </div>
      <p className="text-xs text-deck-text-dim">
        이 기기, 집·사무실 네트워크(사설 IP), 테일스케일 주소의 모델 서버만 연결할 수 있습니다. 공개 인터넷 주소는 서버가 거부합니다.
      </p>
      {state === 'loading' && <p className="text-xs text-deck-text-dim">불러오는 중…</p>}
      {state === 'off' && <p className="text-xs text-deck-text-dim">2.0 실행 기능이 꺼져 있습니다(PCD_V2_ENABLED=1로 활성화).</p>}
      {state === 'error' && <p className="text-xs text-red-400">로컬 모델 설정을 불러오지 못했습니다.</p>}
      {msg && <p className={`text-xs ${msg.ok ? 'text-emerald-400' : 'text-red-400'}`}>{msg.text}</p>}

      {state === 'ok' && entries && entries.length === 0 && !form && (
        <p className="text-xs text-deck-text-dim">등록된 로컬 모델이 없습니다.</p>
      )}
      {state === 'ok' && entries && entries.map((e) => (
        <div key={e.id} className="rounded-lg border border-deck-border/60 p-2.5 text-xs space-y-1">
          <div className="flex flex-wrap items-baseline gap-x-2">
            <span className="font-medium">{e.id}</span>
            <span className={e.status === 'installed' ? 'text-emerald-400' : 'text-amber-300'}>{e.status === 'installed' ? '● 연결됨' : '● 응답 없음'}</span>
            <span className="text-deck-text-dim">{e.url}</span>
          </div>
          <div className="text-deck-text-dim">
            모델: {e.model ? <span className="text-deck-text" title={e.model}>{localModelName(e.model)}</span> : '서버 기본값'} · {e.kind === 'ollama' ? 'Ollama' : 'OpenAI 호환'}
          </div>
          <div className="flex flex-wrap gap-1.5">
            <Badge on={e.useForEasy}>쉬운 작업 자동 처리</Badge>
            <Badge on={e.useAsJudge}>난이도 판단</Badge>
          </div>
          {e.status !== 'installed' && e.detail && <div className="text-amber-300 text-[11px]">{e.detail}</div>}
          {!form && (
            <div className="flex gap-3 pt-0.5">
              <button className="underline" onClick={() => edit(e)}>수정</button>
              <button className={`underline ${confirmDelete === e.id ? 'text-red-400' : ''}`} onClick={() => remove(e.id)}>
                {confirmDelete === e.id ? '한 번 더 누르면 삭제' : '삭제'}
              </button>
            </div>
          )}
        </div>
      ))}

      {form && (
        <div className="rounded-lg border border-deck-accent/40 p-3 text-xs space-y-2.5">
          <Field label="이름" hint="영문 소문자·숫자·하이픈, 예: mac-studio">
            <input value={form.id} disabled={form.editing} onChange={(ev) => setForm({ ...form, id: ev.target.value.toLowerCase() })}
              placeholder="mac-studio" className="w-full bg-deck-bg border border-deck-border rounded px-2 py-1.5 disabled:opacity-60" />
            {form.id && !idValid && <div className="text-red-400 mt-1">영문 소문자·숫자·하이픈(-)으로 32자 이내</div>}
          </Field>
          <Field label="주소" hint="예: http://192.168.1.22:8080 · http://100.x.x.x:8080 (테일스케일)">
            <input value={form.url} onChange={(ev) => setForm({ ...form, url: ev.target.value })} placeholder="http://192.168.1.22:8080"
              className="w-full bg-deck-bg border border-deck-border rounded px-2 py-1.5" />
          </Field>
          <Field label="형식">
            <select value={form.kind} onChange={(ev) => setForm({ ...form, kind: ev.target.value, useForEasy: ev.target.value === 'openai' && form.useForEasy })}
              className="w-full bg-deck-bg border border-deck-border rounded px-2 py-1.5">
              <option value="openai">OpenAI 호환 (mlx_lm.server · LM Studio · vLLM · Ollama의 /v1)</option>
              <option value="ollama">Ollama 기본 API (난이도 판단만 가능)</option>
            </select>
          </Field>
          <div className="flex items-center gap-2">
            <button onClick={test} disabled={!form.url.trim() || busy !== ''} className="px-3 py-1.5 rounded-md border border-deck-border disabled:opacity-40">
              {busy === 'test' ? '연결 확인 중…' : '연결 테스트'}
            </button>
            <span className="text-deck-text-dim">모델 목록을 불러옵니다</span>
          </div>
          <Field label="모델">
            {models.length > 0 ? (
              <select value={form.model} onChange={(ev) => setForm({ ...form, model: ev.target.value })}
                className="w-full bg-deck-bg border border-deck-border rounded px-2 py-1.5">
                {models.map((m) => <option key={m} value={m}>{m}</option>)}
              </select>
            ) : (
              <input value={form.model} onChange={(ev) => setForm({ ...form, model: ev.target.value })} placeholder="연결 테스트 후 선택하거나 직접 입력"
                className="w-full bg-deck-bg border border-deck-border rounded px-2 py-1.5" />
            )}
          </Field>
          <label className={`flex items-start gap-2 ${form.kind !== 'openai' ? 'opacity-50' : ''}`}>
            <input type="checkbox" className="mt-0.5" checked={form.useForEasy} disabled={form.kind !== 'openai'} onChange={(ev) => setForm({ ...form, useForEasy: ev.target.checked })} />
            <span><b>쉬운 작업을 이 모델로 자동 처리</b><br /><span className="text-deck-text-dim">'자동' 세션에서 매우 쉬운 요청(오타·값 변경 등)을 Codex가 이 모델로 처리합니다.</span></span>
          </label>
          <label className="flex items-start gap-2">
            <input type="checkbox" className="mt-0.5" checked={form.useAsJudge} onChange={(ev) => setForm({ ...form, useAsJudge: ev.target.checked })} />
            <span><b>난이도 판단에 사용</b><br /><span className="text-deck-text-dim">요청이 쉬운지·어려운지 이 모델이 판단합니다. 한 번에 하나만 쓰며, 응답이 없으면 키워드 규칙으로 판단합니다.</span>
              {(() => { const cur = (entries ?? []).find((x) => x.useAsJudge && x.id !== form.id); return form.useAsJudge && cur ? <><br /><span className="text-amber-300">지금 판단 모델({cur.id}) 대신 이 모델을 씁니다.</span></> : null; })()}</span>
          </label>
          <div className="flex gap-2 pt-1">
            <button onClick={save} disabled={!canSave || busy !== ''} className="px-3 py-1.5 rounded-md bg-deck-accent text-white disabled:opacity-40">
              {busy === 'save' ? '저장 중…' : '저장'}
            </button>
            <button onClick={() => { setForm(null); setMsg(null); }} className="px-3 py-1.5 rounded-md border border-deck-border">취소</button>
          </div>
        </div>
      )}
    </section>
  );
}

function Badge({ on, children }: { on: boolean; children: React.ReactNode }) {
  return <span className={`rounded px-1.5 py-0.5 ${on ? 'bg-deck-accent/20 text-deck-accent' : 'bg-deck-bg text-deck-text-dim line-through'}`}>{children}</span>;
}

function Field({ label, hint, children }: { label: string; hint?: string; children: React.ReactNode }) {
  return (
    <div className="space-y-1">
      <div className="flex items-baseline gap-2"><span className="font-medium">{label}</span>{hint && <span className="text-[11px] text-deck-text-dim">{hint}</span>}</div>
      {children}
    </div>
  );
}

function errText(err: unknown): string {
  return err instanceof Error ? err.message : String(err);
}
