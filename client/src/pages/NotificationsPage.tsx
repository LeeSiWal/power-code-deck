import { useCallback, useEffect, useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { api } from '../lib/api';
import { useAppStore } from '../stores/appStore';
import { BottomNav } from '../components/layout/BottomNav';
import { IconBack, IconTrash } from '../components/icons';
import { useGoUp } from '../hooks/useGoUp';
import { notificationAccent, notificationLabel } from '../components/notification/reasons';

/**
 * NotificationsPage — the history the badge and the toaster never had.
 *
 * Before this screen the only ways an event reached you were a transient toast and
 * a count on a tab: miss the toast and the event was gone. The server has always
 * recorded notifications; this just shows them.
 *
 * Tapping one opens its session. Jumping to the exact tool call that caused it is a
 * separate, larger piece of work — chat items carry synthetic ids regenerated on
 * every fold, so there is no stable anchor to scroll to yet. The server now stores
 * refType/refId for when that lands.
 */

interface NotificationRow {
  id: number;
  agentId: string;
  reason: string;
  message: string;
  read: boolean;
  createdAt: string;
  refType?: string;
  refId?: string;
}

export function NotificationsPage() {
  const [rows, setRows] = useState<NotificationRow[]>([]);
  const [loading, setLoading] = useState(true);
  const [clearing, setClearing] = useState(false);
  const navigate = useNavigate();
  const goUp = useGoUp('/control');
  // Notifications carry only an agentId; the control-room summaries hold the names.
  const summaries = useAppStore((s) => s.summaries);
  const clearAllNotifications = useAppStore((s) => s.clearAllNotifications);

  const load = useCallback(async () => {
    setLoading(true);
    try {
      const data = await api.listNotifications();
      setRows(Array.isArray(data) ? (data as NotificationRow[]) : []);
    } catch (err) {
      console.error('Failed to fetch notifications:', err);
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => { load(); }, [load]);

  // "지우기" marks everything read server-side. We reload rather than mutate locally
  // so the list reflects what the server actually did — the badge reads the same
  // source, and a silent disagreement between them is worse than a brief spinner.
  const clearAll = useCallback(async () => {
    if (clearing || !rows.length) return;
    setClearing(true);
    try {
      await api.clearNotifications();
      // Mirror it locally: the badge counts the live WS map, not the server rows.
      clearAllNotifications();
      await load();
    } catch (err) {
      console.error('Failed to clear notifications:', err);
    } finally {
      setClearing(false);
    }
  }, [clearing, rows.length, load, clearAllNotifications]);

  const unread = rows.filter((r) => !r.read).length;

  return (
    <div className="flex flex-col h-full safe-top bg-deck-bg overflow-hidden">
      <header className="flex items-center gap-2 px-4 py-2 bg-deck-surface border-b border-deck-border shrink-0">
        {/* Desktop/iPad have no BottomNav, so without this they'd be stranded here
            (the PWA has no browser back). Always goes to /control. */}
        <button
          onClick={goUp}
          className="hidden md:inline-flex p-1 -ml-1 rounded hover:bg-deck-border/30 text-deck-text-dim"
          title="뒤로"
        >
          <IconBack size={15} />
        </button>
        <span className="text-sm font-medium">알림</span>
        {unread > 0 && (
          <span className="text-[10px] font-mono px-1.5 py-0.5 rounded-full border border-deck-accent/50 text-deck-accent-light">
            {unread}
          </span>
        )}
        <div className="flex-1" />
        <button
          onClick={clearAll}
          disabled={clearing || rows.length === 0}
          className="flex items-center gap-1.5 px-2.5 py-1 rounded-lg border border-deck-border text-xs text-deck-text-dim disabled:opacity-40"
        >
          <IconTrash size={13} />
          <span>지우기</span>
        </button>
      </header>

      <main className="flex-1 overflow-y-auto min-h-0">
        {loading ? (
          <div className="p-4 text-center text-sm text-deck-text-dim">불러오는 중…</div>
        ) : rows.length === 0 ? (
          <div className="p-6 text-center text-sm text-deck-text-dim">알림이 없습니다</div>
        ) : (
          <div className="divide-y divide-deck-border/50">
            {rows.map((n) => (
              <button
                key={n.id}
                onClick={() => navigate(`/agents/${n.agentId}`)}
                className={`w-full text-left px-4 py-2.5 border-l-4 ${notificationAccent(n.reason)} ${
                  n.read ? 'opacity-55' : ''
                }`}
              >
                <div className="flex items-center gap-2">
                  {!n.read && <span className="w-1.5 h-1.5 rounded-full bg-deck-accent shrink-0" />}
                  <span className="text-xs font-semibold">{notificationLabel(n.reason)}</span>
                  <span className="text-[11px] font-mono text-deck-text-dim truncate">
                    {summaries[n.agentId]?.name || n.agentId}
                  </span>
                  <span className="ml-auto text-[10px] text-deck-text-faint shrink-0">
                    {new Date(n.createdAt.endsWith('Z') ? n.createdAt : n.createdAt + 'Z').toLocaleString()}
                  </span>
                </div>
                <div className="text-xs text-deck-text-dim mt-0.5 break-words">{n.message}</div>
              </button>
            ))}
          </div>
        )}
      </main>

      <BottomNav />
    </div>
  );
}
