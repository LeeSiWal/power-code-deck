import { useEffect } from 'react';
import { BrowserRouter, Routes, Route, Navigate, Outlet } from 'react-router-dom';
import { useAppStore } from './stores/appStore';
import { useWebSocket } from './hooks/useWebSocket';
import { api } from './lib/api';

import { LoginPage } from './pages/LoginPage';
import { ProjectSelectPage } from './pages/ProjectSelectPage';
import { AgentLauncherPage } from './pages/AgentLauncherPage';
import { DashboardPage } from './pages/DashboardPage';
import { ControlRoomPage } from './pages/ControlRoomPage';
import { TerminalPage } from './pages/TerminalPage';
import { LogsPage } from './pages/LogsPage';
import { SettingsPage } from './pages/SettingsPage';
import { CommandPalette } from './components/CommandPalette';
import { ConnectionBanner } from './components/ConnectionBanner';
import { NotificationToaster } from './components/notification/NotificationToaster';

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
      if (cfg.authEnabled) {
        if (fromHandoff && !localStorage.getItem('accessToken')) {
          // Redeemed a QR: trade the httpOnly handoff cookie for real tokens.
          await api.handoffExchange().catch(() => {});
        }
      } else {
        // No-auth mode still needs a token for the WebSocket. ALWAYS mint a fresh
        // one on boot instead of reusing a stored token: the no-auth JWT secret is
        // an in-memory random value regenerated on every server start, so a token
        // minted against a previous server process fails verification and leaves
        // the WebSocket stuck in a 401 reconnect loop ("연결이 되지 않았습니다").
        // A fresh mint always matches the current server. Ignore failures so the
        // UI still renders (the WS layer retries).
        await api.getAnonymousToken().catch(() => {});
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
          // apply() handles both: correct to a login if auth is on, or mint the
          // anonymous token the WebSocket needs if it's off.
          await apply(cfg);
          return;
        } catch { /* keep trying */ }
      }
    };
    load();
    return () => { cancelled = true; };
  }, [setAuthConfig]);

  // Mobile keyboard: size the app to the visible viewport AND pin it to the top.
  //
  // On iOS the keyboard doesn't shrink the layout viewport (100dvh stays full);
  // instead it shrinks the VISUAL viewport and — to reveal the focused input — often
  // scrolls the whole layout viewport up. That scroll dragged the chat header +
  // composer up under the notch/status bar and left a black gap below (the reported
  // "터미널이 위로 올라가는" bug). Because iOS signals that scroll with a visualViewport
  // `scroll` event, NOT a `resize`, listening to resize alone missed it — so it was
  // intermittent, firing only when the scroll happened without a size change.
  //
  // Fix: react to scroll too, and whenever the keyboard is open force the layout
  // viewport back to the top (scrollTo(0,0)). #root is sized to vv.height, so the
  // composer already sits right above the keyboard — we never need iOS's scroll, and
  // pinning it keeps the header where it belongs.
  useEffect(() => {
    const vv = window.visualViewport;
    if (!vv) return;

    const apply = () => {
      const root = document.getElementById('root');
      if (!root) return;
      // Keyboard visible when the visual viewport is notably shorter than layout.
      if (vv.height < window.innerHeight * 0.85) {
        if (window.scrollY !== 0 || vv.offsetTop !== 0) window.scrollTo(0, 0);
        document.documentElement.style.setProperty('--kb-height', `${vv.height}px`);
        root.style.height = `${vv.height}px`;
      } else {
        document.documentElement.style.removeProperty('--kb-height');
        root.style.height = '';
      }
    };

    apply();
    vv.addEventListener('resize', apply);
    vv.addEventListener('scroll', apply);
    return () => {
      vv.removeEventListener('resize', apply);
      vv.removeEventListener('scroll', apply);
    };
  }, []);

  return (
    <BrowserRouter>
      <WebSocketProvider>
        <CommandPalette />
        <ConnectionBanner />
        <NotificationToaster />
        <Routes>
          <Route path="/login" element={<LoginPage />} />

          <Route element={<AuthGuard />}>
            <Route path="/" element={<ProjectSelectPage />} />
            <Route path="/dashboard" element={<DashboardPage />} />
            <Route path="/control" element={<ControlRoomPage />} />
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
