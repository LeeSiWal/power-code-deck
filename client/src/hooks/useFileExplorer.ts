import { useState, useCallback, useEffect } from 'react';
import { api } from '../lib/api';
import { agentDeckWS } from '../lib/ws';

interface FileNode {
  name: string;
  path: string;
  isDir: boolean;
  size?: number;
  modTime?: string;
  children?: FileNode[];
}

export function useFileExplorer(agentId: string | null) {
  const [tree, setTree] = useState<FileNode | null>(null);
  const [loading, setLoading] = useState(false);
  const [selectedFile, setSelectedFile] = useState<string | null>(null);
  const [fileContent, setFileContent] = useState<string | null>(null);
  const [expandedDirs, setExpandedDirs] = useState<Set<string>>(new Set());
  const [changedFiles, setChangedFiles] = useState<Set<string>>(new Set());

  const fetchTree = useCallback(async () => {
    if (!agentId) return;
    setLoading(true);
    try {
      const data = await api.fileTree(agentId);
      setTree(data);
    } catch (err) {
      console.error('Failed to fetch file tree:', err);
    } finally {
      setLoading(false);
    }
  }, [agentId]);

  useEffect(() => {
    fetchTree();
  }, [fetchTree]);

  // Ask the server to watch this agent's project dir (recursively) and re-arm
  // on reconnect. Without this the file tree only reflects client-initiated
  // changes — files an agent (or anything else) creates never appear live.
  useEffect(() => {
    if (!agentId) return;
    const start = () => agentDeckWS.send('file:watch', { agentId });
    start();
    const unsubOpen = agentDeckWS.on('open', start);
    return () => {
      agentDeckWS.send('file:unwatch', { agentId });
      unsubOpen();
    };
  }, [agentId]);

  // Refresh the tree on file changes. Debounce so a burst (e.g. a code-gen or
  // many files at once) coalesces into a single refetch.
  useEffect(() => {
    if (!agentId) return;
    let timer: number | undefined;
    const unsub = agentDeckWS.on('file:changed', (change: any) => {
      setChangedFiles((prev) => new Set([...prev, change.path]));
      if (change.operation === 'create' || change.operation === 'remove' || change.operation === 'rename') {
        window.clearTimeout(timer);
        timer = window.setTimeout(() => fetchTree(), 300);
      }
    });
    return () => {
      window.clearTimeout(timer);
      unsub();
    };
  }, [agentId, fetchTree]);

  const openFile = useCallback(
    async (path: string) => {
      setSelectedFile(path);
      try {
        const data = await api.readFile(path, agentId || undefined);
        setFileContent(data.content);
        setChangedFiles((prev) => {
          const next = new Set(prev);
          next.delete(path);
          return next;
        });
      } catch (err) {
        console.error('Failed to read file:', err);
        setFileContent(null);
      }
    },
    [agentId]
  );

  const saveFile = useCallback(
    async (path: string, content: string) => {
      await api.writeFile(path, content, agentId || undefined);
      setFileContent(content);
    },
    [agentId]
  );

  const createDir = useCallback(
    async (path: string) => {
      await api.mkdir(path, agentId || undefined);
      fetchTree();
    },
    [agentId, fetchTree]
  );

  const createFile = useCallback(
    async (path: string) => {
      // WriteFile creates the file (and parent dirs) — empty content is fine.
      await api.writeFile(path, '', agentId || undefined);
      fetchTree();
    },
    [agentId, fetchTree]
  );

  const deleteFile = useCallback(
    async (path: string) => {
      await api.deleteFile(path, agentId || undefined);
      if (selectedFile === path) {
        setSelectedFile(null);
        setFileContent(null);
      }
      fetchTree();
    },
    [agentId, selectedFile, fetchTree]
  );

  const renameFile = useCallback(
    async (oldPath: string, newPath: string) => {
      await api.renameFile(oldPath, newPath, agentId || undefined);
      if (selectedFile === oldPath) {
        setSelectedFile(newPath);
      }
      fetchTree();
    },
    [agentId, selectedFile, fetchTree]
  );

  const toggleDir = useCallback((path: string) => {
    setExpandedDirs((prev) => {
      const next = new Set(prev);
      if (next.has(path)) next.delete(path);
      else next.add(path);
      return next;
    });
  }, []);

  return {
    tree,
    loading,
    selectedFile,
    fileContent,
    expandedDirs,
    changedFiles,
    fetchTree,
    openFile,
    saveFile,
    createDir,
    createFile,
    deleteFile,
    renameFile,
    toggleDir,
    setSelectedFile,
  };
}
