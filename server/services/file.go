package services

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type FileService struct{}

func NewFileService() *FileService {
	return &FileService{}
}

type FileNode struct {
	Name     string      `json:"name"`
	Path     string      `json:"path"`
	IsDir    bool        `json:"isDir"`
	Size     int64       `json:"size,omitempty"`
	ModTime  string      `json:"modTime,omitempty"`
	Children []*FileNode `json:"children,omitempty"`
}

type FileStat struct {
	Name    string `json:"name"`
	Path    string `json:"path"`
	IsDir   bool   `json:"isDir"`
	Size    int64  `json:"size"`
	ModTime string `json:"modTime"`
	Mode    string `json:"mode"`
}

var ignoredDirs = map[string]bool{}

// sensitiveHomeDirs are credential/secret directories that the file API must
// never expose, even when the user's home directory is an allowed base.
var sensitiveHomeDirs = []string{
	".ssh", ".aws", ".gnupg", ".kube", ".docker",
	".config/gcloud", ".config/gh", ".azure", ".gcloud",
}

// ValidatePath resolves requestedPath to an absolute path and verifies it stays
// within baseDir. It defends against ".." traversal, absolute-path escapes,
// symlink escapes, and the "prefix sibling" bug (base=/home/u/proj must NOT
// admit /home/u/proj-evil). For paths that don't exist yet (writes / mkdir) the
// nearest existing ancestor is resolved through symlinks and checked, so a
// symlinked parent can't be used to escape.
func (s *FileService) ValidatePath(baseDir, requestedPath string) (string, error) {
	if requestedPath == "" {
		return "", fmt.Errorf("empty path")
	}

	absBase, err := filepath.Abs(baseDir)
	if err != nil {
		return "", err
	}
	absPath, err := filepath.Abs(requestedPath)
	if err != nil {
		return "", err
	}

	realBase, err := filepath.EvalSymlinks(absBase)
	if err != nil {
		realBase = absBase
	}

	realPath, err := filepath.EvalSymlinks(absPath)
	if err != nil {
		// Doesn't exist yet: resolve the nearest existing ancestor (through
		// symlinks) and re-append the non-existent tail.
		realPath = resolveNearestParent(absPath)
	}

	if !withinBase(realBase, realPath) {
		return "", fmt.Errorf("path traversal detected")
	}

	return absPath, nil
}

// withinBase reports whether target is base itself or lives under it. It uses
// filepath.Rel so that /home/u/proj-evil is NOT treated as inside /home/u/proj.
func withinBase(base, target string) bool {
	rel, err := filepath.Rel(base, target)
	if err != nil {
		return false
	}
	if rel == "." {
		return true
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// resolveNearestParent walks up from absPath until it finds an existing
// ancestor, resolves that ancestor's symlinks, then re-joins the non-existent
// remainder. This makes symlink escapes visible even for paths being created.
func resolveNearestParent(absPath string) string {
	var tail []string
	cur := absPath
	for {
		parent := filepath.Dir(cur)
		if parent == cur {
			return absPath // reached root; nothing on the path exists
		}
		tail = append([]string{filepath.Base(cur)}, tail...)
		if resolved, err := filepath.EvalSymlinks(parent); err == nil {
			return filepath.Join(append([]string{resolved}, tail...)...)
		}
		cur = parent
	}
}

// IsSensitivePath reports whether p resolves inside a well-known credential
// directory under the user's home (~/.ssh, ~/.aws, ...). Callers use this to
// keep secrets out of the file API even when home is an allowed base.
func (s *FileService) IsSensitivePath(p string) bool {
	home, err := os.UserHomeDir()
	if err != nil {
		return false
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return false
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		abs = resolved
	} else {
		abs = resolveNearestParent(abs)
	}
	for _, d := range sensitiveHomeDirs {
		if withinBase(filepath.Join(home, d), abs) {
			return true
		}
	}
	return false
}

func (s *FileService) GetTree(baseDir string, maxDepth int) (*FileNode, error) {
	absBase, err := filepath.Abs(baseDir)
	if err != nil {
		return nil, err
	}

	info, err := os.Stat(absBase)
	if err != nil {
		return nil, err
	}

	root := &FileNode{
		Name:  info.Name(),
		Path:  absBase,
		IsDir: true,
	}

	s.buildTree(root, absBase, 0, maxDepth)
	return root, nil
}

func (s *FileService) buildTree(node *FileNode, dir string, depth, maxDepth int) {
	if depth >= maxDepth {
		return
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}

	// Sort: dirs first, then files, both alphabetical
	sort.Slice(entries, func(i, j int) bool {
		iDir := entries[i].IsDir()
		jDir := entries[j].IsDir()
		if iDir != jDir {
			return iDir
		}
		return strings.ToLower(entries[i].Name()) < strings.ToLower(entries[j].Name())
	})

	for _, entry := range entries {
		name := entry.Name()
		if ignoredDirs[name] {
			continue
		}

		childPath := filepath.Join(dir, name)
		child := &FileNode{
			Name:  name,
			Path:  childPath,
			IsDir: entry.IsDir(),
		}

		if !entry.IsDir() {
			if info, err := entry.Info(); err == nil {
				child.Size = info.Size()
				child.ModTime = info.ModTime().Format("2006-01-02T15:04:05Z")
			}
		}

		if entry.IsDir() {
			s.buildTree(child, childPath, depth+1, maxDepth)
		}

		node.Children = append(node.Children, child)
	}
}

func (s *FileService) ReadFile(filePath string) (string, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func (s *FileService) WriteFile(filePath, content string) error {
	dir := filepath.Dir(filePath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	return os.WriteFile(filePath, []byte(content), 0644)
}

func (s *FileService) Mkdir(dirPath string) error {
	return os.MkdirAll(dirPath, 0755)
}

func (s *FileService) Delete(path string) error {
	return os.RemoveAll(path)
}

func (s *FileService) Rename(oldPath, newPath string) error {
	return os.Rename(oldPath, newPath)
}

func (s *FileService) Stat(filePath string) (*FileStat, error) {
	info, err := os.Stat(filePath)
	if err != nil {
		return nil, err
	}
	return &FileStat{
		Name:    info.Name(),
		Path:    filePath,
		IsDir:   info.IsDir(),
		Size:    info.Size(),
		ModTime: info.ModTime().Format("2006-01-02T15:04:05Z"),
		Mode:    info.Mode().String(),
	}, nil
}
