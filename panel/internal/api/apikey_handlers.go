package api

import (
	"errors"
	"net/http"
	"time"

	"github.com/google/uuid"

	"quarry.dev/quarry/panel/internal/audit"
	"quarry.dev/quarry/panel/internal/auth"
	"quarry.dev/quarry/panel/internal/db/gen"
	"quarry.dev/quarry/panel/internal/rbac"
)

type createKeyRequest struct {
	Name    string   `json:"name"`
	Scopes  []string `json:"scopes"`
	Expires string   `json:"expires"` // RFC3339, optional
}

// Scopes are constrained to the requesting identity's effective permissions,
// so a key can never out-power its creator.
func (s *Server) validateScopes(id *auth.Identity, scopes []string) error {
	if len(scopes) == 0 {
		return errors.New("at least one scope is required")
	}
	for _, sc := range scopes {
		if !rbac.Valid(sc) {
			return errors.New("unknown scope: " + sc)
		}
		if !rbac.Has(id.Perms, rbac.Permission(sc)) {
			return errors.New("scope exceeds your permissions: " + sc)
		}
	}
	return nil
}

func (s *Server) createAPIKey(w http.ResponseWriter, r *http.Request) {
	id := Identity(r)
	req, ok := decode[createKeyRequest](w, r)
	if !ok {
		return
	}
	if req.Name == "" {
		apiError(w, http.StatusBadRequest, "name is required")
		return
	}
	if err := s.validateScopes(id, req.Scopes); err != nil {
		apiError(w, http.StatusBadRequest, err.Error())
		return
	}

	var expires *time.Time
	if req.Expires != "" {
		parsed, err := time.Parse(time.RFC3339, req.Expires)
		if err != nil {
			apiError(w, http.StatusBadRequest, "expires must be RFC3339")
			return
		}
		if parsed.Before(time.Now()) {
			apiError(w, http.StatusBadRequest, "expires is in the past")
			return
		}
		expires = &parsed
	}

	fullKey, row, err := s.keys.Create(r.Context(), id.UserID, req.Name, req.Scopes, expires)
	if err != nil {
		apiError(w, http.StatusInternalServerError, "key creation failed")
		return
	}

	s.auditLog.Record(r.Context(), audit.Entry{
		Actor: audit.ActorUser, Action: "apikey.created",
		UserID: &id.UserID, ResourceType: "api_key", ResourceID: row.ID.String(),
		IP: clientIP(r), Metadata: map[string]any{"name": req.Name},
		// Never include the key itself here.
	})
	writeJSON(w, http.StatusCreated, map[string]any{
		"id":         row.ID,
		"name":       row.Name,
		"scopes":     row.Scopes,
		"created_at": row.CreatedAt,
		// The only time the full key is ever returned.
		"key": fullKey,
	})
}

type apiKeyResponse struct {
	ID         uuid.UUID  `json:"id"`
	Name       string     `json:"name"`
	Prefix     string     `json:"prefix"`
	Scopes     []string   `json:"scopes"`
	CreatedAt  time.Time  `json:"created_at"`
	LastUsedAt *time.Time `json:"last_used_at"`
	ExpiresAt  *time.Time `json:"expires_at"`
}

func (s *Server) listAPIKeys(w http.ResponseWriter, r *http.Request) {
	rows, err := s.q.ListAPIKeysByUser(r.Context(), Identity(r).UserID)
	if err != nil {
		apiError(w, http.StatusInternalServerError, "database error")
		return
	}
	out := make([]apiKeyResponse, 0, len(rows))
	for _, k := range rows {
		out = append(out, apiKeyResponse{
			ID: k.ID, Name: k.Name, Prefix: k.Prefix, Scopes: k.Scopes,
			CreatedAt: k.CreatedAt, LastUsedAt: k.LastUsedAt, ExpiresAt: k.ExpiresAt,
		})
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) deleteAPIKey(w http.ResponseWriter, r *http.Request) {
	id := Identity(r)
	keyID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		apiError(w, http.StatusBadRequest, "invalid key id")
		return
	}
	if err := s.q.DeleteAPIKey(r.Context(), gen.DeleteAPIKeyParams{
		ID: keyID, UserID: id.UserID,
	}); err != nil {
		apiError(w, http.StatusNotFound, "key not found")
		return
	}
	s.auditLog.Record(r.Context(), audit.Entry{
		Actor: audit.ActorUser, Action: "apikey.deleted",
		UserID: &id.UserID, ResourceType: "api_key", ResourceID: keyID.String(), IP: clientIP(r),
	})
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}
