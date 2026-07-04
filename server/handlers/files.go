package handlers

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"strconv"

	"powercodedeck/services"
)

func FileTree(fileSvc *services.FileService, agentSvc *services.AgentService) http.HandlerFunc {
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

		depth := 10
		if depthStr != "" {
			if d, err := strconv.Atoi(depthStr); err == nil && d > 0 && d <= 20 {
				depth = d
			}
		}

		tree, err := fileSvc.GetTree(path, depth)
		if err != nil {
			jsonError(w, err.Error(), http.StatusInternalServerError)
			return
		}

		jsonResponse(w, tree)
	}
}

func ReadFile(fileSvc *services.FileService, agentSvc *services.AgentService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		agentID := r.URL.Query().Get("agentId")
		filePath := r.URL.Query().Get("path")

		if filePath == "" {
			jsonError(w, "path required", http.StatusBadRequest)
			return
		}

		// Validate path if agent-scoped
		if agentID != "" {
			baseDir, err := agentSvc.GetWorkingDir(agentID)
			if err != nil {
				jsonError(w, "agent not found", http.StatusNotFound)
				return
			}
			if _, err := fileSvc.ValidatePath(baseDir, filePath); err != nil {
				jsonError(w, "access denied", http.StatusForbidden)
				return
			}
		}

		content, err := fileSvc.ReadFile(filePath)
		if err != nil {
			jsonError(w, err.Error(), http.StatusNotFound)
			return
		}

		jsonResponse(w, map[string]interface{}{
			"path":    filePath,
			"name":    filepath.Base(filePath),
			"content": content,
		})
	}
}

type writeFileRequest struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

func WriteFile(fileSvc *services.FileService, agentSvc *services.AgentService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req writeFileRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			jsonError(w, "invalid request", http.StatusBadRequest)
			return
		}

		agentID := r.URL.Query().Get("agentId")
		if agentID != "" {
			baseDir, err := agentSvc.GetWorkingDir(agentID)
			if err != nil {
				jsonError(w, "agent not found", http.StatusNotFound)
				return
			}
			if _, err := fileSvc.ValidatePath(baseDir, req.Path); err != nil {
				jsonError(w, "access denied", http.StatusForbidden)
				return
			}
		}

		if err := fileSvc.WriteFile(req.Path, req.Content); err != nil {
			jsonError(w, err.Error(), http.StatusInternalServerError)
			return
		}

		jsonResponse(w, map[string]string{"status": "ok"})
	}
}

type mkdirRequest struct {
	Path string `json:"path"`
}

func Mkdir(fileSvc *services.FileService, agentSvc *services.AgentService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req mkdirRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			jsonError(w, "invalid request", http.StatusBadRequest)
			return
		}

		agentID := r.URL.Query().Get("agentId")
		if agentID != "" {
			baseDir, err := agentSvc.GetWorkingDir(agentID)
			if err != nil {
				jsonError(w, "agent not found", http.StatusNotFound)
				return
			}
			if _, err := fileSvc.ValidatePath(baseDir, req.Path); err != nil {
				jsonError(w, "access denied", http.StatusForbidden)
				return
			}
		}

		if err := fileSvc.Mkdir(req.Path); err != nil {
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

func DeleteFile(fileSvc *services.FileService, agentSvc *services.AgentService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Query().Get("path")
		if path == "" {
			jsonError(w, "path required", http.StatusBadRequest)
			return
		}

		agentID := r.URL.Query().Get("agentId")
		if agentID != "" {
			baseDir, err := agentSvc.GetWorkingDir(agentID)
			if err != nil {
				jsonError(w, "agent not found", http.StatusNotFound)
				return
			}
			if _, err := fileSvc.ValidatePath(baseDir, path); err != nil {
				jsonError(w, "access denied", http.StatusForbidden)
				return
			}
		}

		if err := fileSvc.Delete(path); err != nil {
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

func RenameFile(fileSvc *services.FileService, agentSvc *services.AgentService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req renameFileRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			jsonError(w, "invalid request", http.StatusBadRequest)
			return
		}

		agentID := r.URL.Query().Get("agentId")
		if agentID != "" {
			baseDir, err := agentSvc.GetWorkingDir(agentID)
			if err != nil {
				jsonError(w, "agent not found", http.StatusNotFound)
				return
			}
			if _, err := fileSvc.ValidatePath(baseDir, req.OldPath); err != nil {
				jsonError(w, "access denied", http.StatusForbidden)
				return
			}
			if _, err := fileSvc.ValidatePath(baseDir, req.NewPath); err != nil {
				jsonError(w, "access denied", http.StatusForbidden)
				return
			}
		}

		if err := fileSvc.Rename(req.OldPath, req.NewPath); err != nil {
			jsonError(w, err.Error(), http.StatusInternalServerError)
			return
		}

		jsonResponse(w, map[string]string{"status": "ok"})
	}
}

func FileStat(fileSvc *services.FileService, agentSvc *services.AgentService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Query().Get("path")
		if path == "" {
			jsonError(w, "path required", http.StatusBadRequest)
			return
		}

		stat, err := fileSvc.Stat(path)
		if err != nil {
			jsonError(w, err.Error(), http.StatusNotFound)
			return
		}

		jsonResponse(w, stat)
	}
}
