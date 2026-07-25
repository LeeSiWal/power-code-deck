import { useAuth } from '../hooks/useAuth';
import { SoundSettings } from '../components/settings/SoundSettings';
import { NotificationSettings } from '../components/settings/NotificationSettings';
import { UiScaleSettings } from '../components/settings/UiScaleSettings';
import { BottomNav } from '../components/layout/BottomNav';
import { IconBack } from '../components/icons';
import { useAppStore } from '../stores/appStore';
import { useGoBack } from '../hooks/useGoBack';
import { APP_VERSION } from '../version';

export function SettingsPage() {
  const { logout } = useAuth();
  const { authConfig } = useAppStore();
  const goBack = useGoBack();

  return (
    <div className="flex flex-col h-full safe-top bg-deck-bg overflow-hidden">
      <header className="flex items-center gap-2 px-4 py-2 bg-deck-surface border-b border-deck-border">
        {/* Back to the previous page — desktop/iPad have no BottomNav, so without this
            they'd be stranded here (PWA has no browser back). Falls back to the
            dashboard when there's no history to pop. */}
        <button
          onClick={goBack}
          className="hidden md:inline-flex p-1 -ml-1 rounded hover:bg-deck-border/30 text-deck-text-dim"
          title="뒤로"
        >
          <IconBack size={15} />
        </button>
        <span className="text-sm font-medium">Settings</span>
      </header>

      <main className="flex-1 overflow-y-auto min-h-0 p-4 space-y-4 max-w-lg mx-auto w-full">
        <UiScaleSettings />
        <NotificationSettings />
        <SoundSettings />

        <div className="card p-3">
          <div className="text-sm font-medium mb-1">About</div>
          <div className="text-xs text-deck-text-dim">
            {authConfig?.appName || 'PowerCodeDeck'} - AI Coding Terminal Console
          </div>
          <div className="text-xs text-deck-text-dim mt-1">
            Version {authConfig?.version || APP_VERSION} · Auth: {authConfig?.authMethod ?? 'none'}
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
