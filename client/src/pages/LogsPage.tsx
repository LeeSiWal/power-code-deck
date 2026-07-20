import { useState, useEffect, useCallback } from 'react';
import { Link } from 'react-router-dom';
import { api } from '../lib/api';
import { BottomNav } from '../components/layout/BottomNav';
import { IconSearch, IconBack } from '../components/icons';

interface LogEntry {
  id: number;
  agentId: string;
  data: string;
  createdAt: string;
}

export function LogsPage() {
  const [logs, setLogs] = useState<LogEntry[]>([]);
  const [query, setQuery] = useState('');
  const [loading, setLoading] = useState(false);

  const fetchLogs = useCallback(async () => {
    setLoading(true);
    try {
      const data = await api.searchLogs(query || undefined, 200);
      setLogs(data);
    } catch (err) {
      console.error('Failed to fetch logs:', err);
    } finally {
      setLoading(false);
    }
  }, [query]);

  useEffect(() => {
    fetchLogs();
  }, [fetchLogs]);

  return (
    <div className="flex flex-col h-full safe-top bg-deck-bg overflow-hidden">
      <header className="flex items-center gap-2 px-4 py-2 bg-deck-surface border-b border-deck-border shrink-0">
        {/* Back to the dashboard — desktop/iPad have no BottomNav, so without this
            they'd be stranded on this page (PWA has no browser back). */}
        <Link
          to="/dashboard"
          className="hidden md:inline-flex p-1 -ml-1 rounded hover:bg-deck-border/30 text-deck-text-dim"
          title="대시보드로"
        >
          <IconBack size={15} />
        </Link>
        <span className="text-sm font-medium">Logs</span>
      </header>

      <div className="px-4 py-2 border-b border-deck-border shrink-0">
        <div className="flex items-center gap-2 px-3 py-1.5 rounded-lg bg-deck-surface border border-deck-border">
          <IconSearch size={14} color="#8791a4" />
          <input
            type="text"
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            onKeyDown={(e) => e.key === 'Enter' && fetchLogs()}
            placeholder="Search logs..."
            className="flex-1 bg-transparent text-sm outline-none text-deck-text"
          />
        </div>
      </div>

      <main className="flex-1 overflow-y-auto min-h-0">
        {loading ? (
          <div className="p-4 text-center text-sm text-deck-text-dim">Loading...</div>
        ) : logs.length === 0 ? (
          <div className="p-4 text-center text-sm text-deck-text-dim">No logs found</div>
        ) : (
          <div className="divide-y divide-deck-border/50">
            {logs.map((log) => (
              <div key={log.id} className="px-4 py-2">
                <div className="flex items-center gap-2 mb-1">
                  <span className="text-[10px] font-mono text-deck-accent">{log.agentId}</span>
                  <span className="text-[10px] text-deck-text-dim">{new Date(log.createdAt + 'Z').toLocaleString()}</span>
                </div>
                <pre className="text-xs font-mono text-deck-text whitespace-pre-wrap break-words">{log.data}</pre>
              </div>
            ))}
          </div>
        )}
      </main>

      <BottomNav />
    </div>
  );
}
