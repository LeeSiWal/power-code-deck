import { useEffect, useState } from 'react';
import { MarkdownPreview } from './MarkdownPreview';
import { api } from '../../lib/api';

const IMAGE = /\.(png|jpe?g|gif|webp|bmp|ico|avif|svg)$/i;
const PDF = /\.pdf$/i;
const VIDEO = /\.(mp4|webm|mov|m4v|ogv)$/i;
const AUDIO = /\.(mp3|wav|ogg|m4a|flac|aac)$/i;

export type FileKind = 'image' | 'pdf' | 'video' | 'audio' | 'text';

/** How to render a file, by extension. Everything non-media is treated as text. */
export function fileKind(name: string): FileKind {
  if (IMAGE.test(name)) return 'image';
  if (PDF.test(name)) return 'pdf';
  if (VIDEO.test(name)) return 'video';
  if (AUDIO.test(name)) return 'audio';
  return 'text';
}

interface FilePreviewProps {
  path: string;
  content: string;
  agentId?: string;
  onEdit?: () => void;
}

export function FilePreview({ path, content, agentId, onEdit }: FilePreviewProps) {
  const fileName = path.split('/').pop() || path;
  const kind = fileKind(fileName);
  const isMarkdown = /\.(md|markdown|mdx)$/i.test(fileName);
  const [viewMode, setViewMode] = useState<'raw' | 'preview'>(isMarkdown ? 'preview' : 'raw');
  const [url, setUrl] = useState<string | null>(null);
  const [err, setErr] = useState('');

  // Media (image/pdf/video/audio): fetch the raw bytes as an object URL the
  // browser renders natively. Text files skip this and use `content`.
  useEffect(() => {
    if (kind === 'text') { setUrl(null); return; }
    setUrl(null); setErr('');
    let made: string | null = null;
    let alive = true;
    api.rawFileObjectURL(path, agentId)
      .then((u) => { made = u; if (alive) setUrl(u); else URL.revokeObjectURL(u); })
      .catch(() => { if (alive) setErr('파일을 불러오지 못했습니다'); });
    return () => { alive = false; if (made) URL.revokeObjectURL(made); };
  }, [path, agentId, kind]);

  return (
    <div className="flex flex-col h-full bg-deck-bg">
      <div className="flex items-center justify-between px-3 py-1.5 border-b border-deck-border shrink-0">
        <span className="text-xs font-mono text-deck-text-dim truncate">{fileName}</span>
        <div className="flex items-center gap-1.5">
          {isMarkdown && (
            <button
              onClick={() => setViewMode(viewMode === 'raw' ? 'preview' : 'raw')}
              className="text-xs px-2 py-0.5 rounded bg-deck-surface text-deck-text-dim hover:bg-deck-border"
            >
              {viewMode === 'raw' ? 'Preview' : 'Raw'}
            </button>
          )}
          {onEdit && kind === 'text' && (
            <button onClick={onEdit} className="text-xs px-2 py-0.5 rounded bg-deck-surface text-deck-text hover:bg-deck-border">
              Edit
            </button>
          )}
        </div>
      </div>

      <div className={`flex-1 overflow-auto min-h-0 ${kind === 'text' ? 'selectable' : ''}`}>
        {kind === 'text' ? (
          isMarkdown && viewMode === 'preview' ? (
            <MarkdownPreview content={content} />
          ) : (
            <pre className="p-3 text-xs font-mono text-deck-text leading-relaxed whitespace-pre-wrap break-words selectable">
              {content}
            </pre>
          )
        ) : err ? (
          <div className="p-4 text-xs text-deck-danger">{err}</div>
        ) : !url ? (
          <div className="p-4 text-xs text-deck-text-dim">불러오는 중…</div>
        ) : kind === 'image' ? (
          <div className="flex items-center justify-center p-4 min-h-full">
            <img src={url} alt={fileName} className="max-w-full max-h-full object-contain" />
          </div>
        ) : kind === 'pdf' ? (
          <iframe src={url} title={fileName} className="w-full h-full border-0" />
        ) : kind === 'video' ? (
          <div className="flex items-center justify-center p-4 min-h-full">
            <video src={url} controls className="max-w-full max-h-full" />
          </div>
        ) : (
          <div className="p-4">
            <audio src={url} controls className="w-full" />
          </div>
        )}
      </div>
    </div>
  );
}
