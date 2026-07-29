package handlers

import (
	"net/http"
	"strconv"

	"github.com/gorilla/mux"

	"powercodedeck/services"
)

// ListApprovalRules returns every saved "항상 허용" rule. Saved permissions the user
// cannot see become a liability over time, so this list is part of the feature, not
// an extra.
func ListApprovalRules(store *services.ApprovalRuleStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		rules, err := store.List()
		if err != nil {
			http.Error(w, "failed to list rules", http.StatusInternalServerError)
			return
		}
		jsonResponse(w, rules)
	}
}

func DeleteApprovalRule(store *services.ApprovalRuleStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(mux.Vars(r)["id"], 10, 64)
		if err != nil {
			http.Error(w, "bad id", http.StatusBadRequest)
			return
		}
		if err := store.Delete(id); err != nil {
			http.Error(w, "failed to delete rule", http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}
