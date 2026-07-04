package handlers

import (
	"net/http"

	"powercodedeck/services"

	"github.com/gorilla/mux"
)

func ListNotifications(notifSvc *services.NotificationService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		agentID := r.URL.Query().Get("agentId")
		notifs, err := notifSvc.ListUnread(agentID)
		if err != nil {
			jsonError(w, err.Error(), http.StatusInternalServerError)
			return
		}
		jsonResponse(w, notifs)
	}
}

func ClearNotifications(notifSvc *services.NotificationService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		agentID := r.URL.Query().Get("agentId")
		if agentID != "" {
			notifSvc.MarkRead(agentID)
		} else {
			notifSvc.ClearAll()
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func MarkAgentNotificationsRead(notifSvc *services.NotificationService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := mux.Vars(r)["id"]
		notifSvc.MarkRead(id)
		w.WriteHeader(http.StatusNoContent)
	}
}
