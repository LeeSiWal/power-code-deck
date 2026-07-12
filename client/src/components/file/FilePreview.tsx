import { useState } from 'react';
import { MarkdownPreview } from './MarkdownPreview';

interface FilePreviewProps {
  path: string;
  content: string;
  onEdit?: () => void;
}

export function FilePreview({ path, content, onEdit }: FilePreviewProps) {
  const fileName = path.split('/').pop() || path;
  const isMarkdown = /\.(md|markdown|mdx)$/i.test(fileName);
  const [viewMode, setViewMode] = useState<'raw' | 'preview'>(isMarkdown ? 'preview' : 'raw');

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
          {onEdit && (
            <button onClick={onEdit} className="text-xs px-2 py-0.5 rounded bg-deck-surface text-deck-text hover:bg-deck-border">
              Edit
            </button>
          )}
        </div>
      </div>
      <div className="flex-1 overflow-y-auto min-h-0 selectable">
        {isMarkdown && viewMode === 'preview' ? (
          <MarkdownPreview content={content} />
        ) : (
          <pre className="p-3 text-xs font-mono text-deck-text leading-relaxed whitespace-pre-wrap break-words">
            {content}
          </pre>
        )}
      </div>
    </div>
  );
}
