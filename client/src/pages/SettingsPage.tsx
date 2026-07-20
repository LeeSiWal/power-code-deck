import { useAuth } from '../hooks/useAuth';
import { SoundSettings } from '../components/settings/SoundSettings';
import { NotificationSettings } from '../components/settings/NotificationSettings';
import { BottomNav } from '../components/layout/BottomNav';
import { useAppStore } from '../stores/appStore';

export function SettingsPage() {
  const { logout } = useAuth();
  const { authConfig } = useAppStore();

  return (
    <div className="flex flex-col h-full safe-top bg-deck-bg overflow-hidden">
      <header className="px-4 py-2 bg-deck-surface border-b border-deck-border">
        <span className="text-sm font-medium">Settings</span>
      </header>

      <main className="flex-1 overflow-y-auto min-h-0 p-4 space-y-4 max-w-lg mx-auto w-full">
        <NotificationSettings />
        <SoundSettings />

        <div className="card p-3">
          <div className="text-sm font-medium mb-1">About</div>
          <div className="text-xs text-deck-text-dim">
            {authConfig?.appName || 'PowerCodeDeck'} - AI Coding Terminal Console
          </div>
          <div className="text-xs text-deck-text-dim mt-1">
            Version {authConfig?.version || '0.2.0'} · Auth: {authConfig?.authMethod ?? 'none'}
          </div>
          <div className="text-xs text-deck-text-dim mt-1">
            Single binary, zero dependencies
          </div>
        </div>

        {authConfig?.authEnabled && (
          <button onClick={logout} className="btn-danger w-full">
            Logout
          </button>
        )}
      </main>

      <BottomNav />
    </div>
  );
}
