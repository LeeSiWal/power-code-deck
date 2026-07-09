import { AgentActivity, useAppStore } from '../../stores/appStore';
import { AgentActivityTree } from './AgentActivityTree';
import { ActivityStrip } from './ActivityStrip';
import { PixelSprite } from './PixelSprite';
import { getSpritePreset, type CharacterTheme } from './sprites/presets';
import { generatePalette } from '../../lib/paletteGenerator';
import { IconClose } from '../icons';

const THEMES: { id: CharacterTheme; name: string }[] = [
  { id: 'default', name: 'Default' },
  { id: 'cat', name: 'Cat' },
];

const PREVIEW_TYPES = ['read', 'write', 'bash', 'search', 'think'];
const PREVIEW_PALETTE = generatePalette(220);

interface SubAgentPanelProps {
  activity?: AgentActivity;
  palette: string[];
  onClose: () => void;
}

export function SubAgentPanel({ activity, palette, onClose }: SubAgentPanelProps) {
  const { characterTheme, setCharacterTheme, soundEnabled, setSoundEnabled } = useAppStore();
  const hasActivity = !!activity && activity.nodes.length > 0;

  return (
    <div className="flex flex-col h-full bg-deck-surface overflow-hidden">
      <div className="flex items-center justify-between px-4 py-2 border-b border-deck-border shrink-0">
        <span className="text-xs font-medium uppercase tracking-wider text-deck-text-dim">Animation</span>
        <button onClick={onClose} className="p-1 rounded hover:bg-deck-border/50">
          <IconClose size={14} />
        </button>
      </div>

      <div className="flex-1 overflow-y-auto min-h-0 p-4 space-y-4">
        {hasActivity && activity ? (
          <>
            <AgentActivityTree activity={activity} palette={palette} />
            <div className="border-t border-deck-border pt-3">
              <ActivityStrip activity={activity} palette={palette} />
            </div>
          </>
        ) : (
          <div className="text-center text-xs text-deck-text-dim py-8">
            No activity detected yet.
            <br />
            <span className="text-[10px] mt-1 block">The tree appears when the agent starts using tools.</span>
          </div>
        )}

        {/* Settings */}
        <div className="border-t border-deck-border pt-4 space-y-3">
          <h4 className="text-[11px] font-medium uppercase tracking-wider text-deck-text-dim">Settings</h4>

          {/* Character theme */}
          <div className="space-y-2">
            <span className="text-xs text-deck-text-dim">Character</span>
            <div className="flex gap-2">
              {THEMES.map((theme) => (
                <button
                  key={theme.id}
                  onClick={() => setCharacterTheme(theme.id)}
                  className={`flex-1 p-2 rounded-lg border text-center transition-all ${
                    characterTheme === theme.id
                      ? 'border-deck-accent bg-deck-accent/10'
                      : 'border-deck-border bg-deck-bg hover:bg-deck-border/30'
                  }`}
                >
                  <div className="flex justify-center gap-1 mb-1">
                    {PREVIEW_TYPES.slice(0, 3).map((type) => (
                      <PixelSprite
                        key={type}
                        preset={getSpritePreset(type, theme.id)}
                        palette={PREVIEW_PALETTE}
                        state="idle"
                        size={18}
                      />
                    ))}
                  </div>
                  <span className="text-[10px] text-deck-text-dim">{theme.name}</span>
                </button>
              ))}
            </div>
          </div>

          {/* Sound toggle */}
          <div className="flex items-center justify-between">
            <span className="text-xs text-deck-text-dim">Sound effects</span>
            <button
              onClick={() => setSoundEnabled(!soundEnabled)}
              className={`text-xs px-2.5 py-1 rounded transition-colors ${
                soundEnabled
                  ? 'bg-deck-accent/20 text-deck-accent'
                  : 'bg-deck-bg text-deck-text-dim'
              }`}
            >
              {soundEnabled ? 'ON' : 'OFF'}
            </button>
          </div>
        </div>
      </div>
    </div>
  );
}
