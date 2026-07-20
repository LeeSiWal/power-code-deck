import { useState, useCallback } from 'react';
import { useProjects } from '../../hooks/useProjects';
import { useProjectLauncher } from '../../hooks/useProjectLauncher';
import { useDevice } from '../../hooks/useDevice';
import { IconFolder, IconFolderOpen, IconChevronRight, IconPlus, IconBack, IconRocket } from '../icons';
import { CreateProjectSheet } from './CreateProjectSheet';

function timeAgo(dateStr: string): string {
  const diff = Date.now() - new Date(dateStr + 'Z').getTime();
  const mins = Math.floor(diff / 60000);
  if (mins < 1) return 'just now';
  if (mins < 60) return `${mins}m ago`;
  const hrs = Math.floor(mins / 60);
  if (hrs < 24) return `${hrs}h ago`;
  const days = Math.floor(hrs / 24);
  return `${days}d ago`;
}

export function ProjectSelector() {
  const { isMobile } = useDevice();
  const { recentProjects, browseEntries, browsePath, loading, browse, removeRecent } = useProjects();
  const { launchProject } = useProjectLauncher();
  const [pathInput, setPathInput] = useState('');
  const [showBrowser, setShowBrowser] = useState(false);
  const [showCreate, setShowCreate] = useState(false);

  const handlePathSubmit = useCallback(() => {
    if (pathInput.trim()) launchProject(pathInput.trim());
  }, [pathInput, launchProject]);

  return (
    <div className="max-w-2xl mx-auto px-4 py-8">
      <div className="text-center mb-8">
        <h1 className="text-xl font-semibold mb-1">PowerCodeDeck</h1>
        <p className="text-sm text-deck-text-dim">Select a project to start</p>
      </div>

      {recentProjects.length > 0 && (
        <section className="mb-8">
          <h2 className="text-[11px] font-medium uppercase tracking-wider text-deck-text-dim mb-3">Recent Projects</h2>
          <div className={isMobile ? 'space-y-2' : 'grid grid-cols-2 lg:grid-cols-3 gap-2'}>
            {recentProjects.map((p) => (
              <button
                key={p.id}
                onClick={() => launchProject(p.path)}
                className="w-full text-left p-3 rounded-lg transition-colors group hover:bg-deck-border/30 card"
              >
                <div className="flex items-start justify-between">
                  <div className="min-w-0">
                    <div className="font-medium text-sm truncate">{p.name}</div>
                    <div className="text-xs mt-0.5 truncate text-deck-text-dim">{p.path}</div>
                    <div className="text-[10px] mt-1 text-deck-text-dim">{timeAgo(p.lastOpenedAt)}</div>
                  </div>
                  <button
                    onClick={(e) => { e.stopPropagation(); removeRecent(p.id); }}
                    className="text-xs opacity-0 group-hover:opacity-100 ml-2 shrink-0 text-deck-text-dim hover:text-deck-text"
                  >
                    &times;
                  </button>
                </div>
              </button>
            ))}
          </div>
        </section>
      )}

      {recentProjects.length > 0 && (
        <div className="flex items-center gap-3 mb-6">
          <div className="flex-1 h-px bg-deck-border" />
          <span className="text-[10px] uppercase tracking-wider text-deck-text-dim">or</span>
          <div className="flex-1 h-px bg-deck-border" />
        </div>
      )}

      <section className="mb-4">
        <button
          onClick={() => { setShowBrowser(!showBrowser); if (!showBrowser) browse(); }}
          className="w-full p-3 rounded-lg text-left flex items-center gap-3 hover:bg-deck-border/30 transition-colors card"
        >
          <IconFolderOpen size={18} />
          <span className="text-sm">Browse folders...</span>
        </button>

        {showBrowser && (
          <div className="mt-2 rounded-lg max-h-72 overflow-y-auto bg-deck-surface border border-deck-border">
            {/* Current folder bar: go up, and open THIS folder as the project.
                Clicking a row below navigates into it (VSCode "Open Folder"). */}
            <div className="px-3 py-2 flex items-center gap-2 border-b border-deck-border sticky top-0 bg-deck-surface z-10">
              {browsePath && (
                <button onClick={() => browse(browsePath.split('/').slice(0, -1).join('/') || '/')}
                        className="flex items-center gap-1 text-xs px-1.5 py-1 rounded hover:bg-deck-border/30 shrink-0"
                        title="Up one folder">
                  <IconBack size={10} /> Up
                </button>
              )}
              <span className="text-xs truncate flex-1 font-mono text-deck-text-dim">{browsePath || '/'}</span>
              <button
                onClick={() => browsePath && launchProject(browsePath)}
                disabled={!browsePath}
                className="px-2.5 py-1 rounded flex items-center gap-1 text-xs font-medium shrink-0 btn-primary disabled:opacity-40"
                title="현재 폴더를 프로젝트로 열기"
              >
                <IconFolderOpen size={12} color="#fff" /> 이 폴더 열기
              </button>
            </div>
            {loading ? (
              <div className="p-4 text-center text-xs text-deck-text-dim">Loading...</div>
            ) : (
              <div className="py-0.5">
                {browseEntries.filter(e => e.isDir).map((entry) => (
                  <button
                    key={entry.path}
                    onClick={() => browse(entry.path)}
                    className="w-full text-left px-3 py-1.5 text-sm truncate flex items-center gap-2 hover:bg-deck-border/30"
                    title="폴더 안으로 이동"
                  >
                    <IconFolder size={14} />
                    <span className="truncate flex-1">{entry.name}</span>
                    <IconChevronRight size={12} color="#8791a4" />
                  </button>
                ))}
                {browseEntries.filter(e => e.isDir).length === 0 && (
                  <div className="p-3 text-center text-xs text-deck-text-dim">하위 폴더 없음 — 위의 '이 폴더 열기'로 여세요</div>
                )}
              </div>
            )}
          </div>
        )}
      </section>

      <section className="mb-4">
        <button
          onClick={() => setShowCreate(true)}
          className="w-full p-3 rounded-lg text-left flex items-center gap-3 hover:bg-deck-border/30 transition-colors border border-dashed border-deck-border bg-deck-surface"
        >
          <IconPlus size={18} color="#8791a4" />
          <span className="text-sm">Create new project...</span>
        </button>
      </section>

      <section>
        <div className="flex gap-2">
          <input
            type="text"
            value={pathInput}
            onChange={(e) => setPathInput(e.target.value)}
            onKeyDown={(e) => e.key === 'Enter' && handlePathSubmit()}
            placeholder="~/code/my-project"
            className="input flex-1 font-mono"
          />
          <button onClick={handlePathSubmit} disabled={!pathInput.trim()} className="btn-primary shrink-0 disabled:opacity-40">
            <IconRocket size={14} color="#fff" />
          </button>
        </div>
      </section>

      <CreateProjectSheet open={showCreate} onClose={() => setShowCreate(false)} />
    </div>
  );
}
