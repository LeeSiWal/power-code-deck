import { useEffect } from 'react';
import { BrowserRouter, Routes, Route, Navigate, Outlet } from 'react-router-dom';
import { useAppStore } from './stores/appStore';
import { useWebSocket } from './hooks/useWebSocket';
import { api } from './lib/api';

import { LoginPage } from './pages/LoginPage';
import { ProjectSelectPage } from './pages/ProjectSelectPage';
import { AgentLauncherPage } from './pages/AgentLauncherPage';
import { DashboardPage } from './pages/DashboardPage';
import { TerminalPage } from './pages/TerminalPage';
import { LogsPage } from './pages/LogsPage';
import { SettingsPage } from './pages/SettingsPage';
import { CommandPalette } from './components/CommandPalette';

function AuthGuard() {
  const { isAuthenticated, authReady } = useAppStore();

  // Wait for the auth config before deciding, so no-auth mode doesn't flash the
  // login page on first paint.
  if (!authReady) return null;

  if (!isAuthenticated) {
    return <Navigate to="/login" replace />;
  }

  return <Outlet />;
}

function WebSocketProvider({ children }: { children: React.ReactNode }) {
  useWebSocket();
  return <>{children}</>;
}

export default function App() {
  const setAuthConfig = useAppStore((s) => s.setAuthConfig);

  // Fetch auth config once on boot. In no-auth mode this marks the user as
  // authenticated so the login page is skipped. When arriving from a handoff QR
  // with auth enabled and no local token, exchange the handoff cookie for one.
  useEffect(() => {
    const params = new URLSearchParams(window.location.search);
    const fromHandoff = params.get('from') === 'handoff';
    let cancelled = false;

    const apply = async (cfg: any) => {
      if (fromHandoff && cfg.authEnabled && !localStorage.getItem('accessToken')) {
        // Redeemed a QR: trade the httpOnly handoff cookie for real tokens.
        await api.handoffExchange().catch(() => {});
      }
      if (cancelled) return;
      setAuthConfig({
        appName: cfg.appName,
        version: cfg.version,
        authEnabled: cfg.authEnabled,
        authMethod: cfg.authMethod,
        handoffEnabled: cfg.handoffEnabled ?? true,
      });
    };

    const withTimeout = <T,>(p: Promise<T>, ms: number) =>
      Promise.race([p, new Promise<never>((_, rej) => setTimeout(() => rej(new Error('timeout')), ms))]);

    const load = async () => {
      // Fast path: one quick probe. If the server answers promptly we render with
      // the true config immediately and there's no flash.
      try {
        const cfg = await withTimeout(api.getAuthConfig(), 1000);
        await apply(cfg);
        return;
      } catch {
        // Server slow / not up yet — DON'T block the UI on it (that was the long
        // white screen). Render immediately as no-auth; the server still enforces
        // auth on protected endpoints, so this exposes nothing.
      }
      if (cancelled) return;
      setAuthConfig({ appName: 'PowerCodeDeck', version: '', authEnabled: false, authMethod: 'none', handoffEnabled: true });
      // Keep probing in the background and correct to a login only if auth is
      // actually enabled once the server becomes reachable.
      for (let i = 0; i < 15 && !cancelled; i++) {
        await new Promise((r) => setTimeout(r, 1000));
        try {
          const cfg = await api.getAuthConfig();
          if (cancelled) return;
          if (cfg.authEnabled) await apply(cfg);
          return;
        } catch { /* keep trying */ }
      }
    };
    load();
    return () => { cancelled = true; };
  }, [setAuthConfig]);

  // Mobile keyboard: override height only when virtual keyboard shrinks the viewport
  useEffect(() => {
    const vv = window.visualViewport;
    if (!vv) return;

    const onResize = () => {
      // Only override when keyboard is actually visible (viewport significantly smaller)
      if (vv.height < window.innerHeight * 0.85) {
        document.documentElement.style.setProperty('--kb-height', `${vv.height}px`);
        document.getElementById('root')!.style.height = `${vv.height}px`;
      } else {
        document.documentElement.style.removeProperty('--kb-height');
        document.getElementById('root')!.style.height = '';
      }
    };

    vv.addEventListener('resize', onResize);
    return () => vv.removeEventListener('resize', onResize);
  }, []);

  return (
    <BrowserRouter>
      <WebSocketProvider>
        <CommandPalette />
        <Routes>
          <Route path="/login" element={<LoginPage />} />

          <Route element={<AuthGuard />}>
            <Route path="/" element={<ProjectSelectPage />} />
            <Route path="/dashboard" element={<DashboardPage />} />
            <Route path="/launch/:encodedPath" element={<AgentLauncherPage />} />
            <Route path="/agents/:id" element={<TerminalPage />} />
            <Route path="/logs" element={<LogsPage />} />
            <Route path="/settings" element={<SettingsPage />} />
          </Route>

          <Route path="*" element={<Navigate to="/" />} />
        </Routes>
      </WebSocketProvider>
    </BrowserRouter>
  );
}
