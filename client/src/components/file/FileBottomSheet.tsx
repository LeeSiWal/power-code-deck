import { useState } from 'react';
import { useFileExplorer } from '../../hooks/useFileExplorer';
import { FileExplorer } from './FileExplorer';
import { FilePreview } from './FilePreview';
import { FileEditor } from './FileEditor';

interface FileBottomSheetProps {
  open: boolean;
  onClose: () => void;
  agentId: string;
  workingDir: string;
}

export function FileBottomSheet({ open, onClose, agentId, workingDir }: FileBottomSheetProps) {
  const {
    tree, selectedFile, fileContent, changedFiles,
    fetchTree, openFile, saveFile, createDir, createFile, deleteFile, renameFile,
    setSelectedFile,
  } = useFileExplorer(agentId);
  const [editing, setEditing] = useState(false);

  if (!open) return null;

  return (
    <>
      <div className="fixed inset-0 bg-black/40 z-40" onClick={onClose} />

      <div className="fixed bottom-0 left-0 right-0 z-50 rounded-t-xl safe-bottom bg-deck-surface border-t border-deck-border"
           style={{ height: '70dvh' }}>
        <div className="flex justify-center py-2" onClick={onClose}>
          <div className="w-10 h-1 rounded-full bg-deck-border" />
        </div>

        <div className="h-[calc(100%-28px)] overflow-hidden flex flex-col min-h-0">
          {selectedFile && fileContent !== null ? (
            editing ? (
              <FileEditor
                path={selectedFile}
                content={fileContent}
                onSave={async (content) => { await saveFile(selectedFile, content); setEditing(false); }}
                onCancel={() => setEditing(false)}
              />
            ) : (
              <div className="flex flex-col h-full">
                <div className="flex items-center justify-between px-3 py-1.5 border-b border-deck-border">
                  <button onClick={() => setSelectedFile(null)} className="text-xs text-deck-text-dim">&larr; Back</button>
                </div>
                <FilePreview
                  path={selectedFile}
                  content={fileContent}
                  agentId={agentId}
                  onEdit={() => setEditing(true)}
                />
              </div>
            )
          ) : (
            <FileExplorer
              tree={tree}
              changedFiles={changedFiles}
              onSelect={openFile}
              onRefresh={fetchTree}
              onMkdir={createDir}
              onNewFile={createFile}
              onRename={renameFile}
              onDelete={deleteFile}
              workingDir={workingDir}
            />
          )}
        </div>
      </div>
    </>
  );
}
