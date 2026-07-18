package handlers

import (
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strconv"

	"github.com/gorilla/mux"

	"powercodedeck/config"
	"powercodedeck/services"
)

// fileGuard authorizes every file operation. A path is allowed only if it lives
// under one of the permitted bases and is not a sensitive credential directory.
// This runs on ALL requests — previously validation only happened when an
// agentId was supplied, so omitting agentId allowed arbitrary absolute-path
// read/write/delete.
type fileGuard struct {
	fileSvc    *services.FileService
	agentSvc   *services.AgentService
	projectSvc *services.ProjectService
	cfg        *config.Config
}

func newFileGuard(fileSvc *services.FileService, agentSvc *services.AgentService, projectSvc *services.ProjectService, cfg *config.Config) *fileGuard {
	return &fileGuard{fileSvc: fileSvc, agentSvc: agentSvc, projectSvc: projectSvc, cfg: cfg}
}

// authorize validates requestedPath and returns the cleaned absolute path. On
// failure it returns an HTTP status + error so the handler can respond
// consistently (404 for a missing agent, 403 for a denied path).
func (g *fileGuard) authorize(agentID, requestedPath string) (string, int, error) {
	if requestedPath == "" {
		return "", http.StatusBadRequest, fmt.Errorf("path required")
	}

	if agentID != "" {
		baseDir, err := g.agentSvc.GetWorkingDir(agentID)
		if err != nil {
			return "", http.StatusNotFound, fmt.Errorf("agent not found")
		}
		abs, err := g.fileSvc.ValidatePath(baseDir, requestedPath)
		if err != nil || g.fileSvc.IsSensitivePath(abs) {
			return "", http.StatusForbidden, fmt.Errorf("access denied")
		}
		return abs, 0, nil
	}

	// No agent scope: the path must fall under a known base (workspace root or
	// home, plus any recent project) and must not touch a sensitive directory.
	for _, base := range g.allowedBases() {
		if abs, err := g.fileSvc.ValidatePath(base, requestedPath); err == nil {
			if g.fileSvc.IsSensitivePath(abs) {
				return "", http.StatusForbidden, fmt.Errorf("access denied")
			}
			return abs, 0, nil
		}
	}
	return "", http.StatusForbidden, fmt.Errorf("access denied")
}

// allowedBases is the set of directories the file API may serve without an
// agent scope: the configured workspace root (or the home directory when unset)
// plus every recent project path.
func (g *fileGuard) allowedBases() []string {
	var bases []string
	if g.cfg != nil && g.cfg.WorkspaceRoot != "" {
		bases = append(bases, g.cfg.WorkspaceRoot)
	} else if home, err := os.UserHomeDir(); err == nil {
		bases = append(bases, home)
	}
	if g.projectSvc != nil {
		if recents, err := g.projectSvc.GetRecent(200); err == nil {
			for _, p := range recents {
				if p.Path != "" {
					bases = append(bases, p.Path)
				}
			}
		}
	}
	return bases
}

func FileTree(fileSvc *services.FileService, agentSvc *services.AgentService, projectSvc *services.ProjectService, cfg *config.Config) http.HandlerFunc {
	guard := newFileGuard(fileSvc, agentSvc, projectSvc, cfg)
	return func(w http.ResponseWriter, r *http.Request) {
		agentID := r.URL.Query().Get("agentId")
		path := r.URL.Query().Get("path")
		depthStr := r.URL.Query().Get("depth")

		if path == "" && agentID != "" {
			dir, err := agentSvc.GetWorkingDir(agentID)
			if err != nil {
				jsonError(w, "agent not found", http.StatusNotFound)
				return
			}
			path = dir
		}

		if path == "" {
			jsonError(w, "path or agentId required", http.StatusBadRequest)
			return
		}

		abs, status, err := guard.authorize(agentID, path)
		if err != nil {
			jsonError(w, err.Error(), status)
			return
		}

		depth := 10
		if depthStr != "" {
			if d, err := strconv.Atoi(depthStr); err == nil && d > 0 && d <= 20 {
				depth = d
			}
		}

		tree, err := fileSvc.GetTree(abs, depth)
		if err != nil {
			jsonError(w, err.Error(), http.StatusInternalServerError)
			return
		}

		jsonResponse(w, tree)
	}
}

func ReadFile(fileSvc *services.FileService, agentSvc *services.AgentService, projectSvc *services.ProjectService, cfg *config.Config) http.HandlerFunc {
	guard := newFileGuard(fileSvc, agentSvc, projectSvc, cfg)
	return func(w http.ResponseWriter, r *http.Request) {
		agentID := r.URL.Query().Get("agentId")
		filePath := r.URL.Query().Get("path")

		abs, status, err := guard.authorize(agentID, filePath)
		if err != nil {
			jsonError(w, err.Error(), status)
			return
		}

		content, err := fileSvc.ReadFile(abs)
		if err != nil {
			jsonError(w, err.Error(), http.StatusNotFound)
			return
		}

		jsonResponse(w, map[string]interface{}{
			"path":    abs,
			"name":    filepath.Base(abs),
			"content": content,
		})
	}
}

type writeFileRequest struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

func WriteFile(fileSvc *services.FileService, agentSvc *services.AgentService, projectSvc *services.ProjectService, cfg *config.Config) http.HandlerFunc {
	guard := newFileGuard(fileSvc, agentSvc, projectSvc, cfg)
	return func(w http.ResponseWriter, r *http.Request) {
		var req writeFileRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			jsonError(w, "invalid request", http.StatusBadRequest)
			return
		}

		abs, status, err := guard.authorize(r.URL.Query().Get("agentId"), req.Path)
		if err != nil {
			jsonError(w, err.Error(), status)
			return
		}

		if err := fileSvc.WriteFile(abs, req.Content); err != nil {
			jsonError(w, err.Error(), http.StatusInternalServerError)
			return
		}

		jsonResponse(w, map[string]string{"status": "ok"})
	}
}

// AttachFile saves an uploaded file into the agent's working dir (a hidden
// .pcd-attachments/ subdir) so the native chat can reference it by path and Claude
// can Read it. Reuses the same path guard as writes, so the destination is always
// inside the agent's project and never a sensitive dir.
func AttachFile(fileSvc *services.FileService, agentSvc *services.AgentService, projectSvc *services.ProjectService, cfg *config.Config) http.HandlerFunc {
	guard := newFileGuard(fileSvc, agentSvc, projectSvc, cfg)
	return func(w http.ResponseWriter, r *http.Request) {
		agentID := mux.Vars(r)["id"]
		if err := r.ParseMultipartForm(64 << 20); err != nil { // 64MB cap
			jsonError(w, "업로드가 너무 크거나 잘못되었습니다", http.StatusBadRequest)
			return
		}
		file, header, err := r.FormFile("file")
		if err != nil {
			jsonError(w, "파일이 없습니다", http.StatusBadRequest)
			return
		}
		defer file.Close()

		// filepath.Base strips any directory components — no traversal via the name.
		name := filepath.Base(header.Filename)
		if name == "" || name == "." || name == string(filepath.Separator) {
			jsonError(w, "잘못된 파일 이름", http.StatusBadRequest)
			return
		}
		// Build the ABSOLUTE destination inside the agent's project. authorize's
		// ValidatePath resolves a relative path against the server's cwd (not the
		// agent's), so a relative ".pcd-attachments/…" would read as traversal — pass
		// the absolute path and let the guard verify it's within-base + not sensitive.
		baseDir, err := agentSvc.GetWorkingDir(agentID)
		if err != nil {
			jsonError(w, "agent not found", http.StatusNotFound)
			return
		}
		abs, status, err := guard.authorize(agentID, filepath.Join(baseDir, ".pcd-attachments", name))
		if err != nil {
			jsonError(w, err.Error(), status)
			return
		}
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			jsonError(w, err.Error(), http.StatusInternalServerError)
			return
		}
		out, err := os.Create(abs)
		if err != nil {
			jsonError(w, err.Error(), http.StatusInternalServerError)
			return
		}
		defer out.Close()
		if _, err := io.Copy(out, file); err != nil {
			jsonError(w, err.Error(), http.StatusInternalServerError)
			return
		}
		jsonResponse(w, map[string]string{"path": ".pcd-attachments/" + name, "name": name})
	}
}

// RawFile serves a file's bytes verbatim with a proper Content-Type, so the
// browser can render images / PDFs / video / audio the JSON /files/read path
// (which stringifies content) can't. Same path guard as every file op. Supports
// range requests via http.ServeContent.
func RawFile(fileSvc *services.FileService, agentSvc *services.AgentService, projectSvc *services.ProjectService, cfg *config.Config) http.HandlerFunc {
	guard := newFileGuard(fileSvc, agentSvc, projectSvc, cfg)
	return func(w http.ResponseWriter, r *http.Request) {
		abs, status, err := guard.authorize(r.URL.Query().Get("agentId"), r.URL.Query().Get("path"))
		if err != nil {
			jsonError(w, err.Error(), status)
			return
		}
		f, err := os.Open(abs)
		if err != nil {
			jsonError(w, "파일을 찾을 수 없습니다", http.StatusNotFound)
			return
		}
		defer f.Close()
		st, err := f.Stat()
		if err != nil || st.IsDir() {
			jsonError(w, "파일이 아닙니다", http.StatusBadRequest)
			return
		}
		if ct := mime.TypeByExtension(filepath.Ext(abs)); ct != "" {
			w.Header().Set("Content-Type", ct)
		}
		http.ServeContent(w, r, filepath.Base(abs), st.ModTime(), f)
	}
}

type mkdirRequest struct {
	Path string `json:"path"`
}

func Mkdir(fileSvc *services.FileService, agentSvc *services.AgentService, projectSvc *services.ProjectService, cfg *config.Config) http.HandlerFunc {
	guard := newFileGuard(fileSvc, agentSvc, projectSvc, cfg)
	return func(w http.ResponseWriter, r *http.Request) {
		var req mkdirRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			jsonError(w, "invalid request", http.StatusBadRequest)
			return
		}

		abs, status, err := guard.authorize(r.URL.Query().Get("agentId"), req.Path)
		if err != nil {
			jsonError(w, err.Error(), status)
			return
		}

		if err := fileSvc.Mkdir(abs); err != nil {
			jsonError(w, err.Error(), http.StatusInternalServerError)
			return
		}

		w.WriteHeader(http.StatusCreated)
		jsonResponse(w, map[string]string{"status": "ok"})
	}
}

type deleteFileRequest struct {
	Path string `json:"path"`
}

func DeleteFile(fileSvc *services.FileService, agentSvc *services.AgentService, projectSvc *services.ProjectService, cfg *config.Config) http.HandlerFunc {
	guard := newFileGuard(fileSvc, agentSvc, projectSvc, cfg)
	return func(w http.ResponseWriter, r *http.Request) {
		abs, status, err := guard.authorize(r.URL.Query().Get("agentId"), r.URL.Query().Get("path"))
		if err != nil {
			jsonError(w, err.Error(), status)
			return
		}

		if err := fileSvc.Delete(abs); err != nil {
			jsonError(w, err.Error(), http.StatusInternalServerError)
			return
		}

		w.WriteHeader(http.StatusNoContent)
	}
}

type renameFileRequest struct {
	OldPath string `json:"oldPath"`
	NewPath string `json:"newPath"`
}

func RenameFile(fileSvc *services.FileService, agentSvc *services.AgentService, projectSvc *services.ProjectService, cfg *config.Config) http.HandlerFunc {
	guard := newFileGuard(fileSvc, agentSvc, projectSvc, cfg)
	return func(w http.ResponseWriter, r *http.Request) {
		var req renameFileRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			jsonError(w, "invalid request", http.StatusBadRequest)
			return
		}

		agentID := r.URL.Query().Get("agentId")
		oldAbs, status, err := guard.authorize(agentID, req.OldPath)
		if err != nil {
			jsonError(w, err.Error(), status)
			return
		}
		newAbs, status, err := guard.authorize(agentID, req.NewPath)
		if err != nil {
			jsonError(w, err.Error(), status)
			return
		}

		if err := fileSvc.Rename(oldAbs, newAbs); err != nil {
			jsonError(w, err.Error(), http.StatusInternalServerError)
			return
		}

		jsonResponse(w, map[string]string{"status": "ok"})
	}
}

func FileStat(fileSvc *services.FileService, agentSvc *services.AgentService, projectSvc *services.ProjectService, cfg *config.Config) http.HandlerFunc {
	guard := newFileGuard(fileSvc, agentSvc, projectSvc, cfg)
	return func(w http.ResponseWriter, r *http.Request) {
		abs, status, err := guard.authorize(r.URL.Query().Get("agentId"), r.URL.Query().Get("path"))
		if err != nil {
			jsonError(w, err.Error(), status)
			return
		}

		stat, err := fileSvc.Stat(abs)
		if err != nil {
			jsonError(w, err.Error(), http.StatusNotFound)
			return
		}

		jsonResponse(w, stat)
	}
}
