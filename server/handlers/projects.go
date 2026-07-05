package handlers

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"powercodedeck/services"

	"github.com/gorilla/mux"
)

func RecentProjects(projectSvc *services.ProjectService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		limitStr := r.URL.Query().Get("limit")
		limit := 20
		if limitStr != "" {
			if l, err := strconv.Atoi(limitStr); err == nil {
				limit = l
			}
		}

		projects, err := projectSvc.GetRecent(limit)
		if err != nil {
			jsonError(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if projects == nil {
			projects = []services.RecentProject{}
		}
		jsonResponse(w, projects)
	}
}

func DeleteRecentProject(projectSvc *services.ProjectService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		idStr := mux.Vars(r)["id"]
		id, err := strconv.Atoi(idStr)
		if err != nil {
			jsonError(w, "invalid id", http.StatusBadRequest)
			return
		}

		if err := projectSvc.DeleteRecent(id); err != nil {
			jsonError(w, err.Error(), http.StatusInternalServerError)
			return
		}

		w.WriteHeader(http.StatusNoContent)
	}
}

func BrowseDir(projectSvc *services.ProjectService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		dirPath := r.URL.Query().Get("path")
		if dirPath == "" {
			dirPath = projectSvc.DefaultRoot() // e.g. POWERCODEDECK_WORKSPACE_ROOT
		}
		entries, err := projectSvc.BrowseDir(dirPath)
		if err != nil {
			jsonError(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if entries == nil {
			entries = []services.DirEntry{}
		}
		jsonResponse(w, map[string]interface{}{
			"path":    dirPath,
			"entries": entries,
		})
	}
}

func DetectProject(projectSvc *services.ProjectService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Query().Get("path")
		if path == "" {
			jsonError(w, "path required", http.StatusBadRequest)
			return
		}

		info, err := projectSvc.DetectProject(path)
		if err != nil {
			jsonError(w, err.Error(), http.StatusInternalServerError)
			return
		}

		jsonResponse(w, info)
	}
}

func SearchProjects(projectSvc *services.ProjectService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query().Get("q")
		if query == "" {
			jsonError(w, "query required", http.StatusBadRequest)
			return
		}

		root := projectSvc.DefaultRoot()
		baseDirs := []string{root, root + "/code", root + "/projects", root + "/Documents"}

		// Also search custom dirs from query param
		if extra := r.URL.Query().Get("dirs"); extra != "" {
			baseDirs = append(baseDirs, strings.Split(extra, ",")...)
		}

		results, err := projectSvc.SearchProjects(query, baseDirs)
		if err != nil {
			jsonError(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if results == nil {
			results = []services.ProjectInfo{}
		}
		jsonResponse(w, results)
	}
}

type createProjectRequest struct {
	ParentDir string `json:"parentDir"`
	Name      string `json:"name"`
}

func CreateProject(projectSvc *services.ProjectService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req createProjectRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			jsonError(w, "invalid request", http.StatusBadRequest)
			return
		}

		path, err := projectSvc.CreateProject(req.ParentDir, req.Name)
		if err != nil {
			jsonError(w, err.Error(), http.StatusInternalServerError)
			return
		}

		w.WriteHeader(http.StatusCreated)
		jsonResponse(w, map[string]string{"path": path})
	}
}

type deleteProjectRequest struct {
	Path string `json:"path"`
}

func DeleteProject(projectSvc *services.ProjectService, agentSvc *services.AgentService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Query().Get("path")
		if path == "" {
			jsonError(w, "path required", http.StatusBadRequest)
			return
		}

		if err := projectSvc.DeleteProject(path); err != nil {
			jsonError(w, err.Error(), http.StatusInternalServerError)
			return
		}

		w.WriteHeader(http.StatusNoContent)
	}
}

type renameProjectRequest struct {
	OldPath string `json:"oldPath"`
	NewName string `json:"newName"`
}

func RenameProject(projectSvc *services.ProjectService, agentSvc *services.AgentService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req renameProjectRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			jsonError(w, "invalid request", http.StatusBadRequest)
			return
		}

		newPath, err := projectSvc.RenameProject(req.OldPath, req.NewName)
		if err != nil {
			jsonError(w, err.Error(), http.StatusInternalServerError)
			return
		}

		jsonResponse(w, map[string]string{"path": newPath})
	}
}
