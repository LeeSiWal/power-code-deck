import { useState } from 'react';
import { IconClaude, IconCodex, IconCustom } from '../icons';

const PRESETS = [
  { id: 'claude-code', name: 'Claude Code', Icon: IconClaude },
  { id: 'codex-cli', name: 'Codex CLI', Icon: IconCodex },
  { id: 'custom', name: 'Custom', Icon: IconCustom },
];

interface CreateAgentSheetProps {
  open: boolean;
  onClose: () => void;
  onCreate: (data: { preset: string; name: string; workingDir: string; command?: string; args?: string[] }) => void;
}

export function CreateAgentSheet({ open, onClose, onCreate }: CreateAgentSheetProps) {
  const [preset, setPreset] = useState(PRESETS[0]);
  const [name, setName] = useState('');
  const [workingDir, setWorkingDir] = useState('~/code');
  const [command, setCommand] = useState('');
  const [bypassPermissions, setBypassPermissions] = useState(false);

  if (!open) return null;

  const handleSubmit = () => {
    const finalName = name || preset.name;
    const args: string[] = [];
    if (preset.id === 'claude-code' && bypassPermissions) {
      args.push('--permission-mode', 'bypassPermissions');
    }
    onCreate({
      preset: preset.id,
      name: finalName,
      workingDir,
      command: preset.id === 'custom' ? command : undefined,
      args: args.length > 0 ? args : undefined,
    });
    setName('');
    setCommand('');
    setBypassPermissions(false);
    onClose();
  };

  return (
    <>
      <div className="fixed inset-0 bg-black/60 z-40" onClick={onClose} />
      <div className="fixed bottom-0 left-0 right-0 z-50 rounded-t-xl safe-bottom animate-slide-up bg-deck-surface border-t border-deck-border">
        <div className="flex justify-center py-2">
          <div className="w-10 h-1 rounded-full bg-deck-border" />
        </div>

        <div className="px-6 pb-6 space-y-4">
          <h2 className="text-base font-medium text-center text-deck-text">New Agent</h2>

          <div className="flex gap-2 justify-center flex-wrap">
            {PRESETS.map((p) => (
              <button
                key={p.id}
                onClick={() => setPreset(p)}
                className={`px-3 py-2 rounded text-sm flex items-center gap-1.5 transition-colors ${
                  preset.id === p.id
                    ? 'bg-deck-accent text-white'
                    : 'bg-deck-bg text-deck-text hover:bg-deck-border/50'
                }`}
              >
                <p.Icon size={14} />
                <span>{p.name}</span>
              </button>
            ))}
          </div>

          <input
            type="text"
            placeholder={preset.name}
            value={name}
            onChange={(e) => setName(e.target.value)}
            className="input"
          />

          <input
            type="text"
            placeholder="~/code"
            value={workingDir}
            onChange={(e) => setWorkingDir(e.target.value)}
            className="input"
          />

          {preset.id === 'claude-code' && (
            <label className="flex items-center gap-3 px-3 py-2.5 rounded-lg border border-deck-border bg-deck-bg cursor-pointer">
              <input
                type="checkbox"
                checked={bypassPermissions}
                onChange={(e) => setBypassPermissions(e.target.checked)}
                className="w-4 h-4 accent-deck-accent"
              />
              <div>
                <span className="text-sm text-deck-text">Bypass Permissions</span>
                <p className="text-xs text-deck-text-dim">--permission-mode bypassPermissions</p>
              </div>
            </label>
          )}

          {preset.id === 'custom' && (
            <input
              type="text"
              placeholder="Command (e.g. python bot.py)"
              value={command}
              onChange={(e) => setCommand(e.target.value)}
              className="input"
            />
          )}

          <button onClick={handleSubmit} className="btn-primary w-full py-2.5 rounded text-sm font-medium">
            Create Agent
          </button>
        </div>
      </div>
    </>
  );
}
