import { useState, useCallback } from 'react';

interface FileEditorProps {
  path: string;
  content: string;
  onSave: (content: string) => Promise<void>;
  onCancel: () => void;
}

export function FileEditor({ path, content, onSave, onCancel }: FileEditorProps) {
  const [value, setValue] = useState(content);
  const [saving, setSaving] = useState(false);
  const fileName = path.split('/').pop() || path;

  const handleSave = useCallback(async () => {
    setSaving(true);
    try {
      await onSave(value);
    } finally {
      setSaving(false);
    }
  }, [value, onSave]);

  return (
    <div className="flex flex-col h-full bg-deck-bg">
      <div className="flex items-center justify-between px-3 py-1.5 border-b border-deck-border">
        <span className="text-xs font-mono text-deck-text-dim truncate">{fileName}</span>
        <div className="flex gap-2">
          <button onClick={onCancel} className="btn-ghost text-xs">Cancel</button>
          <button onClick={handleSave} disabled={saving} className="btn-primary text-xs">
            {saving ? 'Saving...' : 'Save'}
          </button>
        </div>
      </div>
      <textarea
        value={value}
        onChange={(e) => setValue(e.target.value)}
        onKeyDown={(e) => {
          if ((e.metaKey || e.ctrlKey) && e.key === 's') {
            e.preventDefault();
            handleSave();
          }
        }}
        className="selectable flex-1 w-full p-3 bg-transparent text-xs font-mono text-deck-text resize-none outline-none leading-relaxed"
        spellCheck={false}
      />
    </div>
  );
}
