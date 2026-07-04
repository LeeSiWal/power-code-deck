import { useEffect, useState } from 'react';
import { useNavigate, useSearchParams } from 'react-router-dom';
import { ProjectSelector } from '../components/project/ProjectSelector';
import { IconLogout } from '../components/icons';
import { useAuth } from '../hooks/useAuth';
import { api } from '../lib/api';

export function ProjectSelectPage() {
  const { logout } = useAuth();
  const navigate = useNavigate();
  const [searchParams] = useSearchParams();
  const [ready, setReady] = useState(false);

  const forceNew = searchParams.get('new') === '1';

  // Auto-redirect to dashboard if agents are running
  useEffect(() => {
    if (forceNew) {
      setReady(true);
      return;
    }

    api.listAgents()
      .then((agents) => {
        if (Array.isArray(agents) && agents.some((a: any) => a.status === 'running')) {
          navigate('/dashboard', { replace: true });
        } else {
          setReady(true);
        }
      })
      .catch(() => setReady(true));
  }, [navigate, forceNew]);

  if (!ready) {
    return (
      <div className="flex items-center justify-center h-[100dvh] bg-deck-bg text-deck-text-dim">
        Loading...
      </div>
    );
  }

  return (
    <div className="flex flex-col h-full safe-top bg-deck-bg overflow-hidden">
      <header className="flex items-center justify-between px-4 py-2 bg-deck-surface border-b border-deck-border shrink-0">
        <span className="text-sm font-medium">PowerCodeDeck</span>
        <button onClick={logout} className="p-1.5 rounded hover:bg-deck-border/30" title="Logout">
          <IconLogout size={14} color="#64748b" />
        </button>
      </header>
      <main className="flex-1 overflow-y-auto min-h-0">
        <ProjectSelector />
      </main>
    </div>
  );
}
