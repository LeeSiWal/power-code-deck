import React from 'react';
import type { LiveState } from './liveState';

export function dot(hue: number, hollow = false) {
  const color = `hsl(${hue}, 55%, 55%)`;
  return (
    <span
      className="inline-block w-2.5 h-2.5 rounded-full shrink-0"
      style={hollow ? { border: `1.5px solid ${color}` } : { background: color }}
    />
  );
}

// LiveDot: hollow for stopped, a steady dot for idle, and a pulsing (ping) dot for
// working — so "is it doing anything right now" reads instantly, without parsing text.
export function LiveDot({ hue, state }: { hue: number; state: LiveState }) {
  const color = `hsl(${hue}, 60%, 58%)`;
  if (state === 'stopped') {
    return <span className="inline-block w-2.5 h-2.5 rounded-full shrink-0 border-[1.5px] border-deck-text-faint" />;
  }
  return (
    <span className="relative inline-flex w-2.5 h-2.5 shrink-0">
      {state === 'working' && (
        <span className="absolute inline-flex h-full w-full rounded-full animate-ping" style={{ background: color, opacity: 0.55 }} />
      )}
      <span className="relative inline-flex w-2.5 h-2.5 rounded-full" style={{ background: color }} />
    </span>
  );
}

// WorkingBar: an indeterminate sweep shown only while an agent is actively working —
// motion is the clearest "this one is live" signal on a wall of tiles.
export function WorkingBar() {
  return (
    <div className="h-0.5 rounded-full overflow-hidden bg-deck-accent/10 mt-2">
      <div className="h-full w-1/3 bg-deck-accent/70 animate-working-bar" />
    </div>
  );
}

export function ActBtn({
  children,
  onClick,
  danger,
  disabled,
}: {
  children: React.ReactNode;
  onClick: () => void;
  danger?: boolean;
  disabled?: boolean;
}) {
  return (
    <button
      onClick={onClick}
      disabled={disabled}
      className={`px-2.5 py-1 rounded text-[10px] font-mono border active:opacity-80 ${
        disabled
          ? 'border-deck-border-soft text-deck-text-faint opacity-50 cursor-not-allowed'
          : danger
            ? 'border-deck-danger text-deck-danger'
            : 'border-deck-border text-deck-text'
      }`}
    >
      {children}
    </button>
  );
}

export function Badge({ children }: { children: React.ReactNode }) {
  return (
    <span className="font-mono text-[9px] px-1.5 py-0.5 rounded-full border border-deck-border text-deck-text-dim">
      {children}
    </span>
  );
}
