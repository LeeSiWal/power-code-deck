package services

import (
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/fsnotify/fsnotify"
)

// Directories never descended into when arming the recursive watch. Watching
// node_modules/.git/etc would blow past the inotify per-instance limit and spam
// events during installs/builds. This only bounds the WATCH — it does not change
// what the file tree shows.
var watchSkipDirs = map[string]bool{
	"node_modules": true, ".git": true, "dist": true, "build": true,
	".next": true, ".cache": true, "vendor": true, "target": true,
	"__pycache__": true, ".venv": true, "venv": true, ".turbo": true,
}

const watchMaxDepth = 8

// addDirsRecursive adds root and its (non-skipped) subdirectories to the watcher.
// fsnotify is not recursive on its own, so without this, changes in subfolders
// (and files in newly-created folders) are never reported.
func addDirsRecursive(watcher *fsnotify.Watcher, root string) {
	rootDepth := strings.Count(filepath.Clean(root), string(filepath.Separator))
	filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info == nil || !info.IsDir() {
			return nil
		}
		base := filepath.Base(path)
		if path != root && (watchSkipDirs[base] || strings.HasPrefix(base, ".")) {
			return filepath.SkipDir
		}
		if strings.Count(filepath.Clean(path), string(filepath.Separator))-rootDepth > watchMaxDepth {
			return filepath.SkipDir
		}
		_ = watcher.Add(path)
		return nil
	})
}

type FileChange struct {
	Path      string `json:"path"`
	Operation string `json:"operation"` // create, write, remove, rename
}

type WatcherService struct {
	watchers   sync.Map // map[string]*fsnotify.Watcher (agentID -> watcher)
	onChangeFn func(agentID string, change FileChange)
}

func NewWatcherService() *WatcherService {
	return &WatcherService{}
}

func (s *WatcherService) SetOnChange(fn func(agentID string, change FileChange)) {
	s.onChangeFn = fn
}

func (s *WatcherService) Watch(agentID, dirPath string) error {
	// Stop existing watcher for this agent
	s.Unwatch(agentID)

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}

	s.watchers.Store(agentID, watcher)

	go func() {
		for {
			select {
			case event, ok := <-watcher.Events:
				if !ok {
					return
				}
				// Skip hidden files and editor swap noise.
				base := filepath.Base(event.Name)
				if strings.HasPrefix(base, ".") || strings.HasSuffix(base, "~") || watchSkipDirs[base] {
					continue
				}

				var op string
				switch {
				case event.Has(fsnotify.Create):
					op = "create"
					// A new directory must be watched too, or files created
					// inside it later would never be reported.
					if fi, err := os.Stat(event.Name); err == nil && fi.IsDir() {
						addDirsRecursive(watcher, event.Name)
					}
				case event.Has(fsnotify.Write):
					op = "write"
				case event.Has(fsnotify.Remove):
					op = "remove"
				case event.Has(fsnotify.Rename):
					op = "rename"
				default:
					continue
				}

				if s.onChangeFn != nil {
					s.onChangeFn(agentID, FileChange{
						Path:      event.Name,
						Operation: op,
					})
				}

			case err, ok := <-watcher.Errors:
				if !ok {
					return
				}
				log.Printf("Watcher error for %s: %v", agentID, err)
			}
		}
	}()

	// Watch the whole tree (fsnotify is not recursive on its own).
	addDirsRecursive(watcher, dirPath)
	return nil
}

func (s *WatcherService) Unwatch(agentID string) {
	if val, ok := s.watchers.LoadAndDelete(agentID); ok {
		watcher := val.(*fsnotify.Watcher)
		watcher.Close()
	}
}
