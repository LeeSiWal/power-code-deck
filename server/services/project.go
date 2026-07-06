package services

import (
	"database/sql"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type ProjectService struct {
	db            *sql.DB
	workspaceRoot string // default root for the project browser (optional)
}

type RecentProject struct {
	ID              int    `json:"id"`
	Path            string `json:"path"`
	Name            string `json:"name"`
	LastOpenedAt    string `json:"lastOpenedAt"`
	LastAgentPreset string `json:"lastAgentPreset,omitempty"`
	OpenCount       int    `json:"openCount"`
}

type DirEntry struct {
	Name  string `json:"name"`
	Path  string `json:"path"`
	IsDir bool   `json:"isDir"`
}

type ProjectInfo struct {
	Path       string   `json:"path"`
	Name       string   `json:"name"`
	Type       string   `json:"type"`       // git, node, python, go, rust, etc.
	Indicators []string `json:"indicators"` // files that indicate project type
}

func NewProjectService(db *sql.DB) *ProjectService {
	return &ProjectService{db: db}
}

// SetWorkspaceRoot sets the default directory the project browser opens at.
func (s *ProjectService) SetWorkspaceRoot(path string) { s.workspaceRoot = path }

// expandHome expands a leading "~" to the user's home directory. Go does NOT do
// this automatically, so a literal "~/code" would otherwise be treated as a real
// relative path with a "~" segment (creating a bogus "~" folder, or a cwd the
// shell can't enter). Shared with agent working-dir handling.
func expandHome(p string) string {
	p = strings.TrimSpace(p)
	if p == "~" {
		if home, err := os.UserHomeDir(); err == nil {
			return home
		}
	} else if strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, p[2:])
		}
	}
	return p
}

// resolvePath turns a client-supplied path into an absolute one that does not
// depend on the process's working directory: expand ~, and resolve any other
// relative path under the workspace root.
func (s *ProjectService) resolvePath(p string) string {
	p = expandHome(p)
	if p == "" {
		return s.DefaultRoot()
	}
	if !filepath.IsAbs(p) {
		return filepath.Join(s.DefaultRoot(), p)
	}
	return p
}

// DefaultRoot returns the configured workspace root, falling back to the user's
// home directory.
func (s *ProjectService) DefaultRoot() string {
	if s.workspaceRoot != "" {
		return s.workspaceRoot
	}
	if home, err := os.UserHomeDir(); err == nil {
		return home
	}
	return "/"
}

func (s *ProjectService) GetRecent(limit int) ([]RecentProject, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := s.db.Query(
		"SELECT id, path, name, last_opened_at, COALESCE(last_agent_preset, ''), open_count FROM recent_projects ORDER BY last_opened_at DESC LIMIT ?",
		limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var projects []RecentProject
	for rows.Next() {
		var p RecentProject
		if err := rows.Scan(&p.ID, &p.Path, &p.Name, &p.LastOpenedAt, &p.LastAgentPreset, &p.OpenCount); err != nil {
			continue
		}
		projects = append(projects, p)
	}
	return projects, nil
}

func (s *ProjectService) AddRecent(path, name, preset string) error {
	_, err := s.db.Exec(`
		INSERT INTO recent_projects (path, name, last_agent_preset)
		VALUES (?, ?, ?)
		ON CONFLICT(path) DO UPDATE SET
			last_opened_at = datetime('now'),
			last_agent_preset = COALESCE(?, last_agent_preset),
			open_count = open_count + 1
	`, path, name, preset, preset)
	return err
}

func (s *ProjectService) DeleteRecent(id int) error {
	_, err := s.db.Exec("DELETE FROM recent_projects WHERE id = ?", id)
	return err
}

func (s *ProjectService) BrowseDir(dirPath string) ([]DirEntry, error) {
	dirPath = s.resolvePath(dirPath)

	entries, err := os.ReadDir(dirPath)
	if err != nil {
		return nil, err
	}

	var result []DirEntry
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, ".") {
			continue
		}
		result = append(result, DirEntry{
			Name:  name,
			Path:  filepath.Join(dirPath, name),
			IsDir: e.IsDir(),
		})
	}

	sort.Slice(result, func(i, j int) bool {
		if result[i].IsDir != result[j].IsDir {
			return result[i].IsDir
		}
		return strings.ToLower(result[i].Name) < strings.ToLower(result[j].Name)
	})

	return result, nil
}

var projectIndicators = map[string]string{
	".git":           "git",
	"package.json":   "node",
	"go.mod":         "go",
	"Cargo.toml":     "rust",
	"pyproject.toml": "python",
	"requirements.txt": "python",
	"pom.xml":        "java",
	"build.gradle":   "java",
	"Gemfile":        "ruby",
}

func (s *ProjectService) DetectProject(dirPath string) (*ProjectInfo, error) {
	dirPath = s.resolvePath(dirPath)
	info := &ProjectInfo{
		Path: dirPath,
		Name: filepath.Base(dirPath),
	}

	entries, err := os.ReadDir(dirPath)
	if err != nil {
		return nil, err
	}

	for _, e := range entries {
		if pType, ok := projectIndicators[e.Name()]; ok {
			info.Indicators = append(info.Indicators, e.Name())
			if info.Type == "" {
				info.Type = pType
			}
		}
	}

	if info.Type == "" {
		info.Type = "unknown"
	}

	return info, nil
}

func (s *ProjectService) SearchProjects(query string, baseDirs []string) ([]ProjectInfo, error) {
	var results []ProjectInfo

	for _, baseDir := range baseDirs {
		filepath.Walk(baseDir, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return nil
			}
			if !info.IsDir() {
				return nil
			}

			base := filepath.Base(path)
			if ignoredDirs[base] || strings.HasPrefix(base, ".") {
				return filepath.SkipDir
			}

			// Check depth (max 3 levels)
			rel, _ := filepath.Rel(baseDir, path)
			if strings.Count(rel, string(filepath.Separator)) > 3 {
				return filepath.SkipDir
			}

			if strings.Contains(strings.ToLower(base), strings.ToLower(query)) {
				if pi, err := s.DetectProject(path); err == nil && pi.Type != "unknown" {
					results = append(results, *pi)
				}
			}

			return nil
		})
	}

	return results, nil
}

func (s *ProjectService) CreateProject(parentDir, name string) (string, error) {
	parentDir = s.resolvePath(parentDir)
	projectPath := filepath.Join(parentDir, strings.TrimSpace(name))
	if err := os.MkdirAll(projectPath, 0755); err != nil {
		return "", err
	}
	return projectPath, nil
}

func (s *ProjectService) DeleteProject(projectPath string) error {
	return os.RemoveAll(projectPath)
}

func (s *ProjectService) RenameProject(oldPath, newName string) (string, error) {
	newPath := filepath.Join(filepath.Dir(oldPath), newName)
	if err := os.Rename(oldPath, newPath); err != nil {
		return "", err
	}
	// Update in recent_projects
	s.db.Exec("UPDATE recent_projects SET path = ?, name = ? WHERE path = ?", newPath, newName, oldPath)
	return newPath, nil
}
