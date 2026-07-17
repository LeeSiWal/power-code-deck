import { useState, useCallback } from 'react';
import { IconClaude, IconAntigravity, IconCodex, IconCustom, IconRocket } from '../icons';

const PRESETS = [
  { id: 'claude-code', name: 'Claude Code', Icon: IconClaude, command: 'claude', args: [], color: '#D97706' },
  { id: 'antigravity', name: 'Antigravity', Icon: IconAntigravity, command: 'agy', args: [], color: '#7C3AED' },
  { id: 'codex-cli', name: 'Codex CLI', Icon: IconCodex, command: 'codex', args: [], color: '#16A34A' },
  { id: 'custom', name: 'Custom', Icon: IconCustom, command: '', args: [], color: '#9333EA' },
];

interface AgentLauncherProps {
  workingDir: string;
  onLaunch: (preset: string, name: string, command: string, args: string[]) => void;
}

export function AgentLauncher({ workingDir, onLaunch }: AgentLauncherProps) {
  const [selected, setSelected] = useState(PRESETS[0]);
  const [customCommand, setCustomCommand] = useState('');
  const [customArgs, setCustomArgs] = useState('');
  const [name, setName] = useState('');
  const [bypassPermissions, setBypassPermissions] = useState(false);

  const dirName = workingDir.split('/').pop() || workingDir;

  const handleLaunch = useCallback(() => {
    const cmd = selected.id === 'custom' ? customCommand : selected.command;
    let args = selected.id === 'custom'
      ? customArgs.split(' ').filter(Boolean)
      : [...selected.args];
    const agentName = name || `${selected.name} - ${dirName}`;

    if (!cmd) return;

    if (selected.id === 'claude-code' && bypassPermissions) {
      args = ['--permission-mode', 'bypassPermissions', ...args];
    }

    onLaunch(selected.id, agentName, cmd, args);
  }, [selected, customCommand, customArgs, name, dirName, onLaunch, bypassPermissions]);

  return (
    <div className="space-y-6">
      {/* Preset selection */}
      <div>
        <h3 className="text-xs font-medium uppercase tracking-wider text-deck-text-dim mb-3">
          Select Agent
        </h3>
        <div className="grid grid-cols-2 gap-2">
          {PRESETS.map((preset) => (
            <button
              key={preset.id}
              onClick={() => setSelected(preset)}
              className={`p-3 rounded-lg text-left flex items-center gap-3 transition-all border ${
                selected.id === preset.id
                  ? 'border-deck-accent bg-deck-accent/10'
                  : 'border-deck-border bg-deck-surface hover:bg-deck-border/30'
              }`}
            >
              <preset.Icon size={24} />
              <span className="text-sm font-medium">{preset.name}</span>
            </button>
          ))}
        </div>
      </div>

      {/* Bypass Permissions (Claude Code only) */}
      {selected.id === 'claude-code' && (
        <label className="flex items-center gap-3 px-3 py-2.5 rounded-lg border border-deck-border bg-deck-surface cursor-pointer hover:bg-deck-border/30 transition-colors">
          <input
            type="checkbox"
            checked={bypassPermissions}
            onChange={(e) => setBypassPermissions(e.target.checked)}
            className="w-4 h-4 accent-deck-accent"
          />
          <div>
            <span className="text-sm font-medium text-deck-text">Bypass Permissions</span>
            <p className="text-xs text-deck-text-dim mt-0.5">--permission-mode bypassPermissions</p>
          </div>
        </label>
      )}

      {/* Custom command */}
      {selected.id === 'custom' && (
        <div className="space-y-2">
          <input
            type="text"
            value={customCommand}
            onChange={(e) => setCustomCommand(e.target.value)}
            placeholder="Command (e.g., python agent.py)"
            className="input"
          />
          <input
            type="text"
            value={customArgs}
            onChange={(e) => setCustomArgs(e.target.value)}
            placeholder="Arguments (space-separated)"
            className="input"
          />
        </div>
      )}

      {/* Name */}
      <div>
        <input
          type="text"
          value={name}
          onChange={(e) => setName(e.target.value)}
          placeholder={`${selected.name} - ${dirName}`}
          className="input"
        />
      </div>

      {/* Working dir */}
      <div className="text-xs text-deck-text-dim font-mono truncate">
        Working directory: {workingDir}
      </div>

      {/* Launch */}
      <button onClick={handleLaunch} className="btn-primary w-full flex items-center justify-center gap-2">
        <IconRocket size={16} color="#fff" />
        Launch Agent
      </button>
    </div>
  );
}
