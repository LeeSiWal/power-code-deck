import { useState, useRef, useMemo } from 'react';
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
              <IconFilePlus size={14} color="#64748b" />
            </button>
          )}
          {onMkdir && (
            <button onClick={() => { setNewFolderParent(workingDir || '.'); setNewFolderName(''); }}
                    className="p-1 rounded hover:bg-deck-border/50" title="New Folder">
              <IconNewFolder size={14} color="#64748b" />
            </button>
          )}
          <button onClick={onRefresh} className="p-1 rounded hover:bg-deck-border/50" title="Refresh">
            <IconRefresh size={14} color="#64748b" />
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
          <IconSearch size={12} color="#64748b" />
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
}) {
  const [expanded, setExpanded] = useState(depth < 1);
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
          if (isDir) setExpanded(!expanded);
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
          color: isChanged ? '#6366f1' : '#e2e8f0',
        }}
      >
        {isDir ? (
          <span className="w-4 flex items-center justify-center shrink-0">
            {expanded ? <IconChevronDown size={12} color="#64748b" /> : <IconChevronRight size={12} color="#64748b" />}
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
          <div className="fixed z-50 rounded shadow-xl py-1 min-w-[160px] bg-deck-surface border border-deck-border"
               style={{ left: contextMenu.x, top: contextMenu.y }}>
            {isDir && onNewFile && (
              <button
                onClick={() => {
                  setContextMenu(null); setExpanded(true);
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
                onClick={() => { setContextMenu(null); setExpanded(true); onNewFolder(node.path); }}
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
            />
          ))}
        </div>
      )}
    </div>
  );
}
