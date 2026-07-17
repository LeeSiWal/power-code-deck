package services

import (
	"database/sql"
	"time"
)

type Notification struct {
	ID        int    `json:"id"`
	AgentID   string `json:"agentId"`
	Reason    string `json:"reason"`
	Message   string `json:"message"`
	Read      bool   `json:"read"`
	CreatedAt string `json:"createdAt"`
}

type NotificationService struct {
	db *sql.DB
}

func NewNotificationService(db *sql.DB) *NotificationService {
	return &NotificationService{db: db}
}

func (s *NotificationService) Create(agentID, reason, message string) (*Notification, error) {
	now := time.Now().Format("2006-01-02T15:04:05Z")
	result, err := s.db.Exec(
		"INSERT INTO notifications (agent_id, reason, message, created_at) VALUES (?, ?, ?, ?)",
		agentID, reason, message, now,
	)
	if err != nil {
		return nil, err
	}
	id, _ := result.LastInsertId()
	insertAgentLog(s.db, agentID, "🔔 "+reason+": "+message)
	return &Notification{
		ID:        int(id),
		AgentID:   agentID,
		Reason:    reason,
		Message:   message,
		Read:      false,
		CreatedAt: now,
	}, nil
}

func (s *NotificationService) ListUnread(agentID string) ([]Notification, error) {
	query := "SELECT id, agent_id, reason, message, read, created_at FROM notifications WHERE read = FALSE"
	args := []interface{}{}
	if agentID != "" {
		query += " AND agent_id = ?"
		args = append(args, agentID)
	}
	query += " ORDER BY created_at DESC LIMIT 100"

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var notifications []Notification
	for rows.Next() {
		var n Notification
		if err := rows.Scan(&n.ID, &n.AgentID, &n.Reason, &n.Message, &n.Read, &n.CreatedAt); err != nil {
			continue
		}
		notifications = append(notifications, n)
	}
	if notifications == nil {
		notifications = []Notification{}
	}
	return notifications, nil
}

func (s *NotificationService) MarkRead(agentID string) error {
	_, err := s.db.Exec("UPDATE notifications SET read = TRUE WHERE agent_id = ? AND read = FALSE", agentID)
	return err
}

func (s *NotificationService) ClearAll() error {
	_, err := s.db.Exec("UPDATE notifications SET read = TRUE WHERE read = FALSE")
	return err
}
