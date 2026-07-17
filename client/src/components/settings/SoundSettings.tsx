import { useAppStore } from '../../stores/appStore';

export function SoundSettings() {
  const { soundEnabled, setSoundEnabled } = useAppStore();

  return (
    <div className="flex items-center justify-between p-3 card">
      <div>
        <div className="text-sm font-medium">Sound Effects</div>
        <div className="text-xs text-deck-text-dim">Play sounds for sub-agent activity</div>
      </div>
      <button
        onClick={() => setSoundEnabled(!soundEnabled)}
        role="switch"
        aria-checked={soundEnabled}
        className={`shrink-0 w-11 h-6 rounded-full transition-colors relative ${
          soundEnabled ? 'bg-deck-accent' : 'bg-deck-border'
        }`}
      >
        <span
          className={`absolute top-0.5 left-0.5 w-5 h-5 rounded-full bg-white shadow-sm transition-transform ${
            soundEnabled ? 'translate-x-5' : 'translate-x-0'
          }`}
        />
      </button>
    </div>
  );
}
