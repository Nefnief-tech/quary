package api

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/google/uuid"

	"quarry.dev/quarry/panel/internal/db/gen"
)

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) ready(w http.ResponseWriter, r *http.Request) {
	if err := s.pool.Ping(r.Context()); err != nil {
		apiError(w, http.StatusServiceUnavailable, "database unreachable")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) listAudit(w http.ResponseWriter, r *http.Request) {
	limit, offset := pagination(r)
	entries, err := s.q.ListAuditEntries(r.Context(), gen.ListAuditEntriesParams{Limit: limit, Offset: offset})
	if err != nil {
		apiError(w, http.StatusInternalServerError, "database error")
		return
	}
	type auditResponse struct {
		ID           int64          `json:"id"`
		UserID       *uuid.UUID     `json:"user_id"`
		Actor        string         `json:"actor"`
		Action       string         `json:"action"`
		ResourceType string         `json:"resource_type"`
		ResourceID   string         `json:"resource_id"`
		IP           string         `json:"ip"`
		Metadata     map[string]any `json:"metadata"`
		CreatedAt    time.Time      `json:"created_at"`
	}
	out := make([]auditResponse, 0, len(entries))
	for _, e := range entries {
		var meta map[string]any
		_ = json.Unmarshal(e.Metadata, &meta)
		out = append(out, auditResponse{
			ID: e.ID, UserID: e.UserID, Actor: e.Actor, Action: e.Action,
			ResourceType: e.ResourceType, ResourceID: e.ResourceID,
			IP: e.Ip, Metadata: meta, CreatedAt: e.CreatedAt,
		})
	}
	writeJSON(w, http.StatusOK, out)
}
