package handlers

import (
	"database/sql"
	"net/http"
	"strconv"

	"github.com/gorilla/mux"
)

type LogEntry struct {
	ID        int    `json:"id"`
	AgentID   string `json:"agentId"`
	Data      string `json:"data"`
	CreatedAt string `json:"createdAt"`
}

// scanLogRows collects a rows cursor into a LogEntry slice (never nil).
func scanLogRows(rows *sql.Rows) []LogEntry {
	defer rows.Close()
	logs := []LogEntry{}
	for rows.Next() {
		var l LogEntry
		if err := rows.Scan(&l.ID, &l.AgentID, &l.Data, &l.CreatedAt); err != nil {
			continue
		}
		logs = append(logs, l)
	}
	return logs
}

func SearchLogs(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query().Get("q")
		limit := 100
		if l, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && l > 0 && l <= 1000 {
			limit = l
		}

		if query == "" {
			rows, err := db.Query(
				"SELECT id, agent_id, data, created_at FROM logs ORDER BY created_at DESC LIMIT ?",
				limit,
			)
			if err != nil {
				jsonError(w, err.Error(), http.StatusInternalServerError)
				return
			}
			jsonResponse(w, scanLogRows(rows))
			return
		}

		// FTS5 first (fast, whole-token). Fall back to LIKE on error OR when FTS
		// finds nothing: the default tokenizer is word-based, so a substring or a
		// partial Korean token (e.g. "종료" inside "종료됨") only matches via LIKE.
		var logs []LogEntry
		if rows, err := db.Query(
			"SELECT l.id, l.agent_id, l.data, l.created_at FROM logs l JOIN logs_fts f ON l.id = f.rowid WHERE logs_fts MATCH ? ORDER BY l.created_at DESC LIMIT ?",
			query, limit,
		); err == nil {
			logs = scanLogRows(rows)
		}
		if len(logs) == 0 {
			rows, err := db.Query(
				"SELECT id, agent_id, data, created_at FROM logs WHERE data LIKE ? ORDER BY created_at DESC LIMIT ?",
				"%"+query+"%", limit,
			)
			if err != nil {
				jsonError(w, err.Error(), http.StatusInternalServerError)
				return
			}
			logs = scanLogRows(rows)
		}
		jsonResponse(w, logs)
	}
}

func AgentLogs(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		agentID := mux.Vars(r)["agentId"]
		limitStr := r.URL.Query().Get("limit")
		limit := 100
		if limitStr != "" {
			if l, err := strconv.Atoi(limitStr); err == nil && l > 0 && l <= 1000 {
				limit = l
			}
		}

		rows, err := db.Query(
			"SELECT id, agent_id, data, created_at FROM logs WHERE agent_id = ? ORDER BY created_at DESC LIMIT ?",
			agentID, limit,
		)
		if err != nil {
			jsonError(w, err.Error(), http.StatusInternalServerError)
			return
		}
		defer rows.Close()

		var logs []LogEntry
		for rows.Next() {
			var l LogEntry
			if err := rows.Scan(&l.ID, &l.AgentID, &l.Data, &l.CreatedAt); err != nil {
				continue
			}
			logs = append(logs, l)
		}
		if logs == nil {
			logs = []LogEntry{}
		}
		jsonResponse(w, logs)
	}
}
