import { useState, useRef, useEffect } from 'react';
import { useAppStore } from '../../stores/appStore';
import { IconBack, IconChevronRight, IconClose, IconExternal, IconRefresh } from '../icons';
import { api } from '../../lib/api';

interface BrowserPanelProps {
  agentId: string;
  onClose: () => void;
}

function isLocalUrl(inputUrl: string): boolean {
  try {
    const parsed = new URL(inputUrl.startsWith('http') ? inputUrl : `http://${inputUrl}`);
    const host = parsed.hostname.toLowerCase();
    return host === 'localhost' || host === '127.0.0.1' || host === '::1';
  } catch {
    return false;
  }
}

export function BrowserPanel({ agentId, onClose }: BrowserPanelProps) {
  const meta = useAppStore((s) => s.agentMeta.get(agentId));
  const ports = meta?.listeningPorts || [];
  const [displayUrl, setDisplayUrl] = useState('');
  const [iframeSrc, setIframeSrc] = useState('');
  const [srcdoc, setSrcdoc] = useState('');
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState('');
  const iframeRef = useRef<HTMLIFrameElement>(null);

  const navigateTo = async (newUrl: string) => {
    let normalized = newUrl.trim();
    if (!normalized) return;
    if (!normalized.startsWith('http')) {
      normalized = `http://${normalized}`;
    }

    setDisplayUrl(normalized);
    setError('');
    setSrcdoc('');
    setIframeSrc('');

    // All URLs go through proxy for iPad Link Preview bypass (JS injection)
    setLoading(true);
    try {
      const token = api.getToken();
      const res = await fetch(`/api/proxy?url=${encodeURIComponent(normalized)}`, {
        headers: token ? { Authorization: `Bearer ${token}` } : {},
      });

      if (!res.ok) {
        // Proxy failed — fallback to direct iframe for localhost
        if (isLocalUrl(normalized)) {
          setIframeSrc(normalized);
        } else {
          setError(`Failed to load (${res.status})`);
        }
        setLoading(false);
        return;
      }

      const contentType = res.headers.get('content-type') || '';

      if (contentType.includes('application/json')) {
        const data = await res.json();
        setSrcdoc(data.html || '');
      } else {
        // Non-HTML — fall back to direct iframe for localhost, error for external
        if (isLocalUrl(normalized)) {
          setIframeSrc(normalized);
        } else {
          setError('이 콘텐츠는 iframe에서 표시할 수 없습니다.');
        }
      }
    } catch (err: any) {
      // Network error — fallback to direct for localhost
      if (isLocalUrl(normalized)) {
        setIframeSrc(normalized);
      } else {
        setError(err.message || 'Fetch failed');
      }
    } finally {
      setLoading(false);
    }
  };

  // Auto-navigate to first detected port
  useEffect(() => {
    if (!displayUrl && ports.length > 0) {
      const autoUrl = `http://localhost:${ports[0]}`;
      navigateTo(autoUrl);
    }
  }, [ports, displayUrl]);

  const isLocal = isLocalUrl(displayUrl);

  return (
    <div className="flex flex-col h-full bg-deck-surface overflow-hidden">
      <div className="flex items-center gap-1.5 px-2 py-1.5 border-b border-deck-border shrink-0">
        <button onClick={() => iframeRef.current?.contentWindow?.history.back()} className="p-1.5 rounded hover:bg-deck-border/50 active:bg-deck-border/50 text-deck-text-dim text-xs"><IconBack size={13} /></button>
        <button onClick={() => iframeRef.current?.contentWindow?.history.forward()} className="p-1.5 rounded hover:bg-deck-border/50 active:bg-deck-border/50 text-deck-text-dim text-xs"><IconChevronRight size={13} /></button>
        <button onClick={() => navigateTo(displayUrl)} className="p-1.5 rounded hover:bg-deck-border/50 active:bg-deck-border/50 text-deck-text-dim text-xs"><IconRefresh size={13} /></button>

        <form onSubmit={(e) => { e.preventDefault(); navigateTo(displayUrl); }} className="flex-1 flex">
          <input
            type="text"
            value={displayUrl}
            onChange={(e) => setDisplayUrl(e.target.value)}
            placeholder="URL (localhost or external)"
            className="flex-1 bg-deck-bg border border-deck-border rounded px-2 py-1 text-xs outline-none text-deck-text"
          />
        </form>

        {displayUrl && (
          <a href={displayUrl} target="_blank" rel="noopener" className="p-1.5 rounded hover:bg-deck-border/50 active:bg-deck-border/50 text-deck-text-dim text-xs" title="새 탭에서 열기"><IconExternal size={13} /></a>
        )}

        <button onClick={onClose} className="p-1.5 rounded hover:bg-deck-border/50 active:bg-deck-border/50">
          <IconClose size={12} />
        </button>
      </div>

      {/* Port shortcuts + proxy indicator */}
      <div className="flex items-center gap-1 px-2 py-1 border-b border-deck-border/50 shrink-0 overflow-x-auto">
        {ports.map((port) => (
          <button
            key={port}
            onClick={() => navigateTo(`http://localhost:${port}`)}
            className={`text-[11px] px-2 py-1 rounded font-mono shrink-0 active:opacity-70 ${
              displayUrl.includes(`:${port}`) ? 'bg-deck-accent/20 text-deck-accent' : 'bg-deck-bg text-deck-text-dim hover:bg-deck-border/30'
            }`}
          >
            :{port}
          </button>
        ))}
        {displayUrl && !isLocal && (
          <span className="text-[10px] text-amber-400 ml-auto shrink-0">proxy</span>
        )}
      </div>

      {/* iframe area */}
      <div
        className="flex-1 min-h-0"
        style={{
          overflow: 'auto',
          WebkitOverflowScrolling: 'touch',
          overscrollBehavior: 'contain',
        }}
      >
        {loading && (
          <div className="flex items-center justify-center h-full text-sm text-deck-text-dim">
            로딩 중...
          </div>
        )}

        {error && (
          <div className="flex flex-col items-center justify-center h-full text-center p-4">
            <p className="text-sm text-deck-text-dim mb-3">{error}</p>
            {displayUrl && (
              <a href={displayUrl} target="_blank" rel="noopener" className="text-sm px-4 py-2 rounded-lg bg-deck-accent text-white active:opacity-80">
                새 탭에서 열기
              </a>
            )}
          </div>
        )}

        {!loading && !error && iframeSrc && (
          <iframe
            ref={iframeRef}
            src={iframeSrc}
            style={{ width: '100%', height: '100%', border: 'none', pointerEvents: 'auto', position: 'relative', zIndex: 1, touchAction: 'manipulation' }}
            sandbox="allow-same-origin allow-scripts allow-forms allow-popups allow-top-navigation"
          />
        )}

        {!loading && !error && srcdoc && (
          <iframe
            ref={iframeRef}
            srcDoc={srcdoc}
            style={{ width: '100%', height: '100%', border: 'none', pointerEvents: 'auto', position: 'relative', zIndex: 1, touchAction: 'manipulation' }}
            sandbox="allow-same-origin allow-scripts allow-forms allow-popups allow-top-navigation"
          />
        )}

        {!loading && !error && !iframeSrc && !srcdoc && (
          <div className="flex flex-col items-center justify-center h-full text-center p-4">
            <p className="text-sm text-deck-text-dim mb-1">
              {ports.length === 0 ? '감지된 포트 없음' : 'URL을 입력하세요'}
            </p>
            <p className="text-[11px] text-deck-text-dim">
              {ports.length === 0
                ? '에이전트가 서버를 시작하면 자동으로 감지됩니다'
                : 'localhost 또는 외부 URL 모두 지원'}
            </p>
          </div>
        )}
      </div>
    </div>
  );
}
