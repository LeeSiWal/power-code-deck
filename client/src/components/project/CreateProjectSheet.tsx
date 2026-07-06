import { useState, useEffect } from 'react';
import { useProjects } from '../../hooks/useProjects';
import { useProjectLauncher } from '../../hooks/useProjectLauncher';
import { api } from '../../lib/api';
import { IconClose } from '../icons';

interface CreateProjectSheetProps {
  open: boolean;
  onClose: () => void;
}

export function CreateProjectSheet({ open, onClose }: CreateProjectSheetProps) {
  const [parentDir, setParentDir] = useState('~/code');
  const [name, setName] = useState('');
  const { createProject } = useProjects();
  const { launchProject } = useProjectLauncher();

  // Default the parent to the server's workspace root (POWERCODEDECK_WORKSPACE_ROOT,
  // e.g. ~/PowerCodeDeck/projects) so new projects land in the standard place.
  useEffect(() => {
    if (!open) return;
    let cancelled = false;
    api.browseDir().then((d: any) => { if (!cancelled && d?.path) setParentDir(d.path); }).catch(() => {});
    return () => { cancelled = true; };
  }, [open]);

  if (!open) return null;

  const handleCreate = async () => {
    if (!name.trim()) return;
    const result = await createProject(parentDir, name.trim());
    onClose();
    launchProject(result.path);
  };

  return (
    <div className="fixed inset-0 z-50 flex items-end sm:items-center justify-center bg-black/60" onClick={onClose}>
      <div
        className="w-full sm:max-w-md bg-deck-surface rounded-t-2xl sm:rounded-xl p-4 animate-slide-up"
        onClick={(e) => e.stopPropagation()}
      >
        <div className="flex items-center justify-between mb-4">
          <h3 className="text-sm font-medium">Create New Project</h3>
          <button onClick={onClose} className="p-1 rounded hover:bg-deck-border/50">
            <IconClose size={14} />
          </button>
        </div>

        <div className="space-y-3">
          <div>
            <label className="text-xs text-deck-text-dim mb-1 block">Parent Directory</label>
            <input
              type="text"
              value={parentDir}
              onChange={(e) => setParentDir(e.target.value)}
              className="input font-mono"
            />
          </div>
          <div>
            <label className="text-xs text-deck-text-dim mb-1 block">Project Name</label>
            <input
              type="text"
              value={name}
              onChange={(e) => setName(e.target.value)}
              onKeyDown={(e) => e.key === 'Enter' && handleCreate()}
              className="input"
              autoFocus
              placeholder="my-project"
            />
          </div>
          <button onClick={handleCreate} disabled={!name.trim()} className="btn-primary w-full disabled:opacity-40">
            Create & Open
          </button>
        </div>
      </div>
    </div>
  );
}
