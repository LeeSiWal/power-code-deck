const STATUS_CONFIG: Record<string, { color: string; label: string }> = {
  running: { color: '#22c55e', label: 'Running' },
  stopped: { color: '#8791a4', label: 'Stopped' },
  error: { color: '#ef4444', label: 'Error' },
  starting: { color: '#f59e0b', label: 'Starting' },
};

export function StatusBadge({ status }: { status: string }) {
  const config = STATUS_CONFIG[status] || STATUS_CONFIG.stopped;
  return (
    <span className="inline-flex items-center gap-1.5 text-xs text-deck-text-dim">
      <span
        className={`w-2 h-2 rounded-full ${status === 'running' ? 'animate-pulse' : ''}`}
        style={{ background: config.color }}
      />
      {config.label}
    </span>
  );
}
