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

    api.getAuthConfig()
      .then(async (cfg) => {
        if (fromHandoff && cfg.authEnabled && !localStorage.getItem('accessToken')) {
          // Redeemed a QR: trade the httpOnly handoff cookie for real tokens.
          await api.handoffExchange().catch(() => {});
        }
        setAuthConfig({
          appName: cfg.appName,
          version: cfg.version,
          authEnabled: cfg.authEnabled,
          authMethod: cfg.authMethod,
          handoffEnabled: cfg.handoffEnabled ?? true,
        });
      })
      .catch(() =>
        // If health is unreachable, fall back to auth-enabled so we don't
        // accidentally expose an unauthenticated app.
        setAuthConfig({ appName: 'PowerCodeDeck', version: '', authEnabled: true, authMethod: 'pin', handoffEnabled: true }),
      );
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
