import type { IntelligenceMode, LocalProvider } from '../../lib/api';
import { cloudTargetName, type LocalOperation, type NativeDriverName } from './executionRouting';

// Hybrid sits last and carries a marker, because measurement did not support
// presenting it as a peer of the other two: on this repository it produced no cost
// reduction against Cloud while adding 10-46 s of local inference per turn, and its
// spread reached above the cost of doing nothing at all
// (docs/local-intelligence-poc-report.md §7a-§7d). It stays selectable — the finding
// is one repo and one task class — but it should not look like a recommendation.
const MODES: { value: IntelligenceMode; label: string; experimental?: boolean }[] = [
  { value: 'CLOUD_ONLY', label: 'Cloud' },
  { value: 'LOCAL_ONLY', label: 'Local' },
  { value: 'LOCAL_PREPROCESS_CLOUD', label: 'Hybrid', experimental: true },
];

const OPERATIONS: { value: LocalOperation; label: string }[] = [
  { value: 'repository_question', label: 'Repository question' },
  { value: 'summarize', label: 'Summarize' },
  { value: 'explain', label: 'Explain' },
  { value: 'classify', label: 'Classify' },
  { value: 'log_analysis', label: 'Log analysis' },
];

interface ExecutionModeControlProps {
  mode: IntelligenceMode;
  onModeChange: (mode: IntelligenceMode) => void;
  providers: LocalProvider[];
  providersLoading: boolean;
  providersError: boolean;
  provider: string;
  onProviderChange: (provider: string) => void;
  operation: LocalOperation;
  onOperationChange: (operation: LocalOperation) => void;
  driver: NativeDriverName;
  onOpenSettings: () => void;
}

export function ExecutionModeControl({
  mode, onModeChange, providers, providersLoading, providersError, provider, onProviderChange,
  operation, onOperationChange, driver, onOpenSettings,
}: ExecutionModeControlProps) {
  const usesLocal = mode !== 'CLOUD_ONLY';
  const selectedProvider = providers.find((item) => item.name === provider);
  const target = cloudTargetName(driver);

  return (
    <div className="rounded-lg border border-deck-border bg-deck-bg/40 p-2 space-y-2">
      <div className="flex min-w-0 flex-wrap items-center justify-between gap-2">
        <div className="flex items-center gap-2">
          <span className="text-[10px] uppercase tracking-wide text-deck-text-faint">Execution</span>
          <div className="inline-flex rounded-lg border border-deck-border bg-deck-surface p-0.5" role="group" aria-label="Execution mode">
            {MODES.map((item) => (
              <button
                key={item.value}
                type="button"
                onClick={() => onModeChange(item.value)}
                aria-pressed={mode === item.value}
                className={`min-h-7 rounded-md px-2.5 text-[11px] transition-colors ${
                  mode === item.value ? 'bg-deck-accent text-white' : 'text-deck-text-dim hover:text-deck-text'
                }`}
                title={item.experimental
                  ? 'Experimental: measured on this repository, Hybrid showed no cost reduction versus Cloud and added 10-46 s per turn'
                  : undefined}
              >
                {item.label}
                {item.experimental && (
                  <span aria-hidden="true" className="ml-1 opacity-60">·exp</span>
                )}
              </button>
            ))}
          </div>
        </div>
        <span className="min-w-0 truncate text-[10px] text-deck-text-dim">
          {mode === 'CLOUD_ONLY' ? `${target} · local optimization not used`
            : selectedProvider ? `${selectedProvider.name} → ${mode === 'LOCAL_ONLY' ? 'Local result' : target}`
              : mode === 'LOCAL_ONLY' ? 'Local result' : `Local provider → ${target}`}
        </span>
      </div>

      {usesLocal && (
        <div className="flex min-w-0 flex-wrap items-center gap-2">
          {providersLoading ? (
            <span className="text-[11px] text-deck-text-dim">Loading local providers…</span>
          ) : providers.length > 0 ? (
            <label className="min-w-0 flex-1 text-[10px] text-deck-text-faint">
              Provider
              <select
                value={provider}
                onChange={(event) => onProviderChange(event.target.value)}
                className="mt-0.5 min-h-8 w-full rounded-lg border border-deck-border bg-deck-surface px-2 text-xs text-deck-text"
              >
                {providers.length > 1 && <option value="">Select provider…</option>}
                {providers.map((item) => <option key={item.name} value={item.name}>{item.name} / {item.model}</option>)}
              </select>
            </label>
          ) : (
            <div className="min-w-0 flex-1 text-[11px] text-deck-warning">
              {providersError ? 'Unable to load Local Intelligence providers.' : 'No Local Intelligence provider configured.'}
            </div>
          )}

          {mode === 'LOCAL_ONLY' && (
            <label className="min-w-[10rem] flex-1 text-[10px] text-deck-text-faint">
              Local task
              <select
                value={operation}
                onChange={(event) => onOperationChange(event.target.value as LocalOperation)}
                className="mt-0.5 min-h-8 w-full rounded-lg border border-deck-border bg-deck-surface px-2 text-xs text-deck-text"
              >
                {OPERATIONS.map((item) => <option key={item.value} value={item.value}>{item.label}</option>)}
              </select>
            </label>
          )}

          {!providersLoading && providers.length === 0 && (
            <button type="button" onClick={onOpenSettings} className="min-h-9 shrink-0 rounded-lg border border-deck-border px-3 text-[11px] text-deck-accent-light">
              Open Settings
            </button>
          )}
        </div>
      )}

      {mode === 'LOCAL_ONLY' && (
        <div className="text-[10px] leading-relaxed text-deck-text-dim">
          Local Only supports repository analysis tasks. Use Cloud for code changes.
        </div>
      )}

      {mode === 'LOCAL_PREPROCESS_CLOUD' && (
        <div className="text-[10px] leading-relaxed text-deck-warning">
          Experimental. Measured on this repository: no cost reduction versus Cloud, and 10-46 s
          added per turn while the local model builds the context pack.
        </div>
      )}
    </div>
  );
}
