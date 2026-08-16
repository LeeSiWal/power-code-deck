import { useEffect, useState } from 'react';
import { api, type LocalProvider, type ProviderHealth } from '../../lib/api';

const emptyProvider: LocalProvider = {
  name: 'Mac Studio', type: 'ollama', baseUrl: '', model: '', timeoutMs: 30000, enabled: true,
};

export function LocalIntelligenceSettings() {
  const [providers, setProviders] = useState<LocalProvider[]>([]);
  const [form, setForm] = useState<LocalProvider>(emptyProvider);
  const [health, setHealth] = useState<ProviderHealth | null>(null);
  const [message, setMessage] = useState('');
  const [busy, setBusy] = useState(false);

  const reload = () => api.listLocalProviders().then(setProviders).catch((e) => setMessage(e.message));
  useEffect(() => { reload(); }, []);

  const select = (p: LocalProvider) => {
    setForm(p);
    setHealth(null);
    setMessage('');
  };

  const save = async () => {
    setBusy(true); setMessage(''); setHealth(null);
    try {
      const stored = await api.putLocalProvider(form);
      setForm(stored);
      await reload();
      setMessage('Saved. No rebuild required.');
    } catch (e: any) { setMessage(e.message); }
    finally { setBusy(false); }
  };

  const check = async () => {
    setBusy(true); setMessage('');
    try { setHealth(await api.localProviderHealth(form.name)); }
    catch (e: any) { setMessage(e.message); }
    finally { setBusy(false); }
  };

  const field = 'input w-full text-xs';
  return (
    <div className="card p-3 space-y-3">
      <div>
        <div className="text-sm font-medium">Local Intelligence (POC)</div>
        <div className="text-xs text-deck-text-dim mt-1">Remote Ollama endpoint used only by explicit intelligence runs.</div>
      </div>

      {providers.length > 0 && (
        <div className="flex flex-wrap gap-1">
          {providers.map((p) => <button key={p.name} onClick={() => select(p)} className="px-2 py-1 rounded bg-deck-border/40 text-xs">{p.name}</button>)}
          <button onClick={() => select(emptyProvider)} className="px-2 py-1 rounded bg-deck-border/40 text-xs">+ New</button>
        </div>
      )}

      <label className="block text-xs text-deck-text-dim">Name
        <input className={field} value={form.name} onChange={(e) => setForm({ ...form, name: e.target.value })} />
      </label>
      <label className="block text-xs text-deck-text-dim">Provider
        <select className={field} value={form.type} onChange={(e) => setForm({ ...form, type: e.target.value })}>
          <option value="ollama">Ollama</option>
        </select>
      </label>
      <label className="block text-xs text-deck-text-dim">Endpoint
        <input className={field} placeholder="http://100.82.14.21:11434" value={form.baseUrl} onChange={(e) => setForm({ ...form, baseUrl: e.target.value })} />
      </label>
      <label className="block text-xs text-deck-text-dim">Model
        <input className={field} placeholder="qwen-coder" value={form.model} onChange={(e) => setForm({ ...form, model: e.target.value })} />
      </label>
      <label className="block text-xs text-deck-text-dim">Timeout (ms)
        <input className={field} type="number" value={form.timeoutMs} onChange={(e) => setForm({ ...form, timeoutMs: Number(e.target.value) })} />
      </label>
      <label className="flex items-center gap-2 text-xs"><input type="checkbox" checked={form.enabled} onChange={(e) => setForm({ ...form, enabled: e.target.checked })} /> Enabled</label>

      <div className="flex gap-2">
        <button disabled={busy} onClick={save} className="btn-primary flex-1">Save</button>
        <button disabled={busy || !providers.some((p) => p.name === form.name)} onClick={check} className="px-3 rounded border border-deck-border text-xs">Health check</button>
      </div>
      {message && <div className="text-xs text-deck-text-dim">{message}</div>}
      {health && (
        <div className="grid grid-cols-[1fr_auto] gap-x-3 gap-y-1 text-xs">
          <span>Network</span><span>{health.reachable ? '●' : '○'}</span>
          <span>Provider API</span><span>{health.apiHealthy ? '●' : '○'}</span>
          <span>Model</span><span>{health.modelAvailable ? '●' : '○'}</span>
          <span>Generation</span><span>{health.generationTest ? '●' : '○'}</span>
          <span>Latency</span><span>{health.latencyMs} ms</span>
          {health.errorCode && <><span className="text-red-400">{health.errorCode}</span><span className="text-red-400">{health.error}</span></>}
        </div>
      )}
    </div>
  );
}
