import { useState, useRef, useMemo, useEffect, useCallback } from 'react';
import {
  IconFile, IconFolder, IconFolderOpen, IconRefresh, IconNewFolder,
  IconPlay, IconTrash, IconChevronRight, IconChevronDown, IconSearch,
  IconFilePlus, IconEdit,
  FILE_ICON_MAP,
} from '../icons';

interface FileNode {
  name: string;
  path: string;
  isDir: boolean;
  size?: number;
  modTime?: string;
  children?: FileNode[];
}

function buildMatchSet(node: FileNode, search: string): Set<string> {
  const matches = new Set<string>();
  if (!search) return matches;
  const visit = (n: FileNode): boolean => {
    if (n.name.toLowerCase().includes(search)) {
      matches.add(n.path);
      const markAll = (c: FileNode) => { matches.add(c.path); c.children?.forEach(markAll); };
      markAll(n);
      return true;
    }
    if (n.children) {
      let childMatch = false;
      for (const child of n.children) {
        if (visit(child)) childMatch = true;
      }
      if (childMatch) { matches.add(n.path); return true; }
    }
    return false;
  };
  visit(node);
  return matches;
}

function getFileIcon(name: string, size = 23) {
  const lower = name.toLowerCase();
  // Check full filename first (e.g. Dockerfile, .env, .gitignore)
  const ByName = FILE_ICON_MAP[lower.replace(/^\./, '')];
  if (ByName) return <ByName size={size} />;
  // Then check extension
  const ext = lower.split('.').pop();
  const ByExt = ext ? FILE_ICON_MAP[ext] : undefined;
  if (ByExt) return <ByExt size={size} />;
  return <IconFile size={size} />;
}

// Expand/collapse persistence — a JSON array of expanded folder paths per project.
function loadExpanded(key: string): Set<string> {
  try {
    const raw = localStorage.getItem(key);
    if (raw) return new Set<string>(JSON.parse(raw));
  } catch { /* corrupt entry → start empty */ }
  return new Set<string>();
}

function saveExpanded(key: string, set: Set<string>): void {
  try {
    localStorage.setItem(key, JSON.stringify([...set]));
  } catch { /* storage full / disabled → keep in-memory only */ }
}

interface FileExplorerProps {
  tree: FileNode | null;
  changedFiles: Set<string>;
  onSelect: (path: string) => void;
  onRefresh: () => void;
  onMkdir?: (path: string) => void;
  onNewFile?: (path: string) => void;
  onRename?: (oldPath: string, newPath: string) => void;
  onDelete?: (path: string) => void;
  workingDir?: string;
}

export function FileExplorer({ tree, changedFiles, onSelect, onRefresh, onMkdir, onNewFile, onRename, onDelete, workingDir }: FileExplorerProps) {
  const [search, setSearch] = useState('');
  const [newFolderParent, setNewFolderParent] = useState<string | null>(null);
  const [newFolderName, setNewFolderName] = useState('');
  const searchLower = search.toLowerCase();
  const searchMatchSet = useMemo(
    () => tree ? buildMatchSet(tree, searchLower) : new Set<string>(),
    [tree, searchLower],
  );

  // Persisted folder expand/collapse state, per project. Folders whose path is in the
  // set are expanded; the toggle is remembered across reloads and navigation, so the
  // tree opens back up exactly where you left it. Keyed by workingDir so each project
  // keeps its own layout.
  const storageKey = `pcd:filetree:${workingDir || 'default'}`;
  const [expandedSet, setExpandedSet] = useState<Set<string>>(() => loadExpanded(storageKey));
  const seededRef = useRef(false);

  // Reload when the project changes (storageKey change); the useState initializer
  // only runs on first mount.
  useEffect(() => {
    setExpandedSet(loadExpanded(storageKey));
    seededRef.current = localStorage.getItem(storageKey) != null;
  }, [storageKey]);

  // First-time seed: with nothing saved yet, expand the top-level folders (matching
  // the prior default) once the tree arrives, then persist so later toggles stick.
  useEffect(() => {
    if (seededRef.current || !tree?.children) return;
    const seed = new Set<string>();
    for (const c of tree.children) if (c.isDir) seed.add(c.path);
    seededRef.current = true;
    setExpandedSet(seed);
    saveExpanded(storageKey, seed);
  }, [tree, storageKey]);

  const toggleExpanded = useCallback((path: string) => {
    setExpandedSet((prev) => {
      const next = new Set(prev);
      if (next.has(path)) next.delete(path);
      else next.add(path);
      saveExpanded(storageKey, next);
      return next;
    });
  }, [storageKey]);

  const expandPath = useCallback((path: string) => {
    setExpandedSet((prev) => {
      if (prev.has(path)) return prev;
      const next = new Set(prev).add(path);
      saveExpanded(storageKey, next);
      return next;
    });
  }, [storageKey]);

  const handleCreateFolder = () => {
    if (newFolderParent && newFolderName.trim() && onMkdir) {
      const fullPath = `${newFolderParent}/${newFolderName.trim()}`;
      onMkdir(fullPath);
      setNewFolderParent(null);
      setNewFolderName('');
    }
  };

  return (
    <div className="flex flex-col h-full text-sm bg-deck-surface overflow-hidden">
      <div className="flex items-center justify-between px-3 py-1.5 border-b border-deck-border">
        <span className="text-[11px] font-medium uppercase tracking-wider text-deck-text-dim">Explorer</span>
        <div className="flex items-center gap-0.5">
          {onNewFile && (
            <button onClick={() => {
                      const name = window.prompt('새 파일 이름 (New file name)');
                      if (name && name.trim()) onNewFile(`${workingDir || '.'}/${name.trim()}`);
                    }}
                    className="p-1 rounded hover:bg-deck-border/50" title="New File">
              <IconFilePlus size={14} color="#8791a4" />
            </button>
          )}
          {onMkdir && (
            <button onClick={() => { setNewFolderParent(workingDir || '.'); setNewFolderName(''); }}
                    className="p-1 rounded hover:bg-deck-border/50" title="New Folder">
              <IconNewFolder size={14} color="#8791a4" />
            </button>
          )}
          <button onClick={onRefresh} className="p-1 rounded hover:bg-deck-border/50" title="Refresh">
            <IconRefresh size={14} color="#8791a4" />
          </button>
        </div>
      </div>

      {workingDir && (
        <div className="px-3 py-1 border-b border-deck-border/50">
          <span className="text-[11px] font-medium text-deck-text">{workingDir.split('/').pop()}</span>
        </div>
      )}

      <div className="px-2 py-1.5 border-b border-deck-border/50">
        <div className="flex items-center gap-1.5 px-2 py-1 rounded bg-deck-bg border border-deck-border/50">
          <IconSearch size={12} color="#8791a4" />
          <input
            type="text"
            placeholder="Search files..."
            value={search}
            onChange={(e) => setSearch(e.target.value)}
            className="flex-1 bg-transparent text-xs outline-none text-deck-text"
          />
        </div>
      </div>

      <div className="flex-1 overflow-y-auto min-h-0 py-0.5">
        {tree ? (
          tree.children?.map((child) => (
            <TreeNode
              key={child.path}
              node={child}
              depth={0}
              search={searchLower}
              searchMatchSet={searchMatchSet}
              changedFiles={changedFiles}
              onSelect={onSelect}
              onDelete={onDelete}
              onNewFile={onNewFile}
              onRename={onRename}
              onNewFolder={onMkdir ? (p) => { setNewFolderParent(p); setNewFolderName(''); } : undefined}
              newFolderParent={newFolderParent}
              newFolderName={newFolderName}
              onNewFolderNameChange={setNewFolderName}
              onNewFolderSubmit={handleCreateFolder}
              onNewFolderCancel={() => setNewFolderParent(null)}
              expandedSet={expandedSet}
              onToggleExpand={toggleExpanded}
              onExpand={expandPath}
            />
          ))
        ) : (
          <div className="px-3 py-4 text-center text-xs text-deck-text-dim">Loading...</div>
        )}
      </div>
    </div>
  );
}

function TreeNode({
  node, depth, search, searchMatchSet, changedFiles, onSelect, onDelete, onNewFile, onRename, onNewFolder,
  newFolderParent, newFolderName, onNewFolderNameChange, onNewFolderSubmit, onNewFolderCancel,
  expandedSet, onToggleExpand, onExpand,
}: {
  node: FileNode; depth: number; search: string; searchMatchSet: Set<string>;
  changedFiles: Set<string>; onSelect: (path: string) => void;
  onDelete?: (path: string) => void;
  onNewFile?: (path: string) => void;
  onRename?: (oldPath: string, newPath: string) => void;
  onNewFolder?: (parentPath: string) => void;
  newFolderParent: string | null; newFolderName: string;
  onNewFolderNameChange: (v: string) => void;
  onNewFolderSubmit: () => void; onNewFolderCancel: () => void;
  expandedSet: Set<string>;
  onToggleExpand: (path: string) => void;
  onExpand: (path: string) => void;
}) {
  // While searching, force-open matching folders so results are visible regardless of
  // the persisted collapsed state.
  const expanded = expandedSet.has(node.path) || (!!search && searchMatchSet.has(node.path));
  const [contextMenu, setContextMenu] = useState<{ x: number; y: number } | null>(null);
  const longPressRef = useRef<ReturnType<typeof setTimeout> | null>(null);

  if (search && !searchMatchSet.has(node.path)) return null;

  const isDir = node.isDir;
  const isChanged = changedFiles.has(node.path);

  return (
    <div>
      <button
        onClick={() => {
          setContextMenu(null);
          if (isDir) onToggleExpand(node.path);
          else onSelect(node.path);
        }}
        onContextMenu={(e) => { e.preventDefault(); setContextMenu({ x: e.clientX, y: e.clientY }); }}
        onTouchStart={() => { longPressRef.current = setTimeout(() => setContextMenu({ x: 100, y: 200 }), 500); }}
        onTouchEnd={() => { if (longPressRef.current) clearTimeout(longPressRef.current); }}
        onTouchCancel={() => { if (longPressRef.current) clearTimeout(longPressRef.current); }}
        className="w-full text-left flex items-center gap-1 py-[2px] text-[13px] group hover:bg-deck-border/30 transition-colors"
        style={{
          paddingLeft: `${depth * 12 + 8}px`,
          paddingRight: '8px',
          color: isChanged ? '#6366f1' : '#e7e9f0',
        }}
      >
        {isDir ? (
          <span className="w-4 flex items-center justify-center shrink-0">
            {expanded ? <IconChevronDown size={12} color="#8791a4" /> : <IconChevronRight size={12} color="#8791a4" />}
          </span>
        ) : <span className="w-4" />}
        <span className="shrink-0">
          {isDir ? (expanded ? <IconFolderOpen size={18} /> : <IconFolder size={18} />) : getFileIcon(node.name)}
        </span>
        <span className="truncate ml-0.5">{node.name}</span>
        {isChanged && <span className="w-1.5 h-1.5 rounded-full shrink-0 ml-auto bg-deck-accent" />}
      </button>

      {contextMenu && (
        <>
          <div className="fixed inset-0 z-40" onClick={() => setContextMenu(null)} />
          <div className="fixed z-50 rounded shadow-xl py-1 min-w-[160px] bg-deck-raised border border-deck-border"
               style={{ left: contextMenu.x, top: contextMenu.y }}>
            {isDir && onNewFile && (
              <button
                onClick={() => {
                  setContextMenu(null); onExpand(node.path);
                  const name = window.prompt('새 파일 이름 (New file name)');
                  if (name && name.trim()) onNewFile(`${node.path}/${name.trim()}`);
                }}
                className="w-full text-left px-3 py-1.5 text-xs flex items-center gap-2 hover:bg-deck-border/30 text-deck-text"
              >
                <IconFilePlus size={12} /> New File
              </button>
            )}
            {isDir && onNewFolder && (
              <button
                onClick={() => { setContextMenu(null); onExpand(node.path); onNewFolder(node.path); }}
                className="w-full text-left px-3 py-1.5 text-xs flex items-center gap-2 hover:bg-deck-border/30 text-deck-text"
              >
                <IconNewFolder size={12} /> New Folder
              </button>
            )}
            {onRename && (
              <button
                onClick={() => {
                  setContextMenu(null);
                  const newName = window.prompt('이름 변경 (Rename)', node.name);
                  const trimmed = newName?.trim();
                  if (trimmed && trimmed !== node.name) {
                    const parent = node.path.slice(0, node.path.lastIndexOf('/'));
                    onRename(node.path, `${parent}/${trimmed}`);
                  }
                }}
                className="w-full text-left px-3 py-1.5 text-xs flex items-center gap-2 hover:bg-deck-border/30 text-deck-text"
              >
                <IconEdit size={12} /> Rename
              </button>
            )}
            {onDelete && (
              <button
                onClick={() => { setContextMenu(null); onDelete(node.path); }}
                className="w-full text-left px-3 py-1.5 text-xs flex items-center gap-2 hover:bg-deck-border/30 text-deck-danger"
              >
                <IconTrash size={12} color="#ef4444" /> Delete
              </button>
            )}
          </div>
        </>
      )}

      {isDir && expanded && (
        <div>
          {newFolderParent === node.path && (
            <div style={{ paddingLeft: `${(depth + 1) * 12 + 8}px` }} className="py-0.5">
              <input
                type="text"
                value={newFolderName}
                onChange={(e) => onNewFolderNameChange(e.target.value)}
                onKeyDown={(e) => {
                  if (e.key === 'Enter') onNewFolderSubmit();
                  if (e.key === 'Escape') onNewFolderCancel();
                }}
                onBlur={() => { if (!newFolderName.trim()) onNewFolderCancel(); }}
                autoFocus
                placeholder="folder name"
                className="w-full px-2 py-0.5 rounded text-xs outline-none bg-deck-bg border border-deck-accent text-deck-text"
              />
            </div>
          )}
          {node.children?.map((child) => (
            <TreeNode
              key={child.path} node={child} depth={depth + 1} search={search} searchMatchSet={searchMatchSet}
              changedFiles={changedFiles} onSelect={onSelect} onDelete={onDelete}
              onNewFile={onNewFile} onRename={onRename} onNewFolder={onNewFolder}
              newFolderParent={newFolderParent} newFolderName={newFolderName}
              onNewFolderNameChange={onNewFolderNameChange} onNewFolderSubmit={onNewFolderSubmit}
              onNewFolderCancel={onNewFolderCancel}
              expandedSet={expandedSet} onToggleExpand={onToggleExpand} onExpand={onExpand}
            />
          ))}
        </div>
      )}
    </div>
  );
}
