package api

import (
	"net/http"
	"strconv"

	"github.com/google/uuid"

	"quarry.dev/quarry/panel/internal/audit"
	"quarry.dev/quarry/panel/internal/auth"
	"quarry.dev/quarry/panel/internal/db/gen"
)

type createUserRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
	Email    string `json:"email"`
	Role     string `json:"role"`
}

func (s *Server) listUsers(w http.ResponseWriter, r *http.Request) {
	limit, offset := pagination(r)
	users, err := s.q.ListUsers(r.Context(), gen.ListUsersParams{Limit: limit, Offset: offset})
	if err != nil {
		apiError(w, http.StatusInternalServerError, "database error")
		return
	}
	out := make([]userResponse, 0, len(users))
	for _, u := range users {
		role, err := s.q.RoleOfUser(r.Context(), u.ID)
		roleName := ""
		if err == nil {
			roleName = role.Name
		}
		email := u.Email
		out = append(out, userResponse{
			ID: u.ID, Username: u.Username, Email: email,
			RoleName: roleName, Disabled: u.Disabled, CreatedAt: u.CreatedAt,
		})
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) createUser(w http.ResponseWriter, r *http.Request) {
	req, ok := decode[createUserRequest](w, r)
	if !ok {
		return
	}
	if err := s.validateCredentials(req.Username, req.Password); err != nil {
		apiError(w, http.StatusBadRequest, err.Error())
		return
	}
	if req.Role == "" {
		req.Role = "user"
	}
	role, err := s.q.GetRoleByName(r.Context(), req.Role)
	if err != nil {
		apiError(w, http.StatusBadRequest, "unknown role")
		return
	}

	hash, err := auth.HashPassword(req.Password)
	if err != nil {
		apiError(w, http.StatusInternalServerError, "hashing failed")
		return
	}
	var email *string
	if req.Email != "" {
		email = &req.Email
	}
	user, err := s.q.CreateUser(r.Context(), gen.CreateUserParams{
		ID:           uuid.New(),
		Username:     req.Username,
		Email:        email,
		PasswordHash: hash,
		RoleID:       role.ID,
	})
	if err != nil {
		apiError(w, http.StatusConflict, "username or email already taken")
		return
	}

	s.auditLog.Record(r.Context(), audit.Entry{
		Actor: audit.ActorUser, Action: "user.created",
		UserID:       actorID(r),
		ResourceType: "user", ResourceID: user.ID.String(),
		IP: clientIP(r), Metadata: map[string]any{"username": user.Username, "role": req.Role},
	})
	writeJSON(w, http.StatusCreated, userResponse{
		ID: user.ID, Username: user.Username, Email: email,
		RoleName: role.Name, Disabled: user.Disabled, CreatedAt: user.CreatedAt,
	})
}

type setDisabledRequest struct {
	Disabled bool `json:"disabled"`
}

func (s *Server) setUserDisabled(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		apiError(w, http.StatusBadRequest, "invalid user id")
		return
	}
	req, ok := decode[setDisabledRequest](w, r)
	if !ok {
		return
	}
	if actor := actorID(r); req.Disabled && actor != nil && id == *actor {
		apiError(w, http.StatusBadRequest, "you cannot disable yourself")
		return
	}
	if err := s.q.SetUserDisabled(r.Context(), gen.SetUserDisabledParams{
		ID: id, Disabled: req.Disabled,
	}); err != nil {
		apiError(w, http.StatusNotFound, "user not found")
		return
	}
	// Disabling a user kills their sessions immediately.
	if req.Disabled {
		_ = s.q.DeleteSessionsOfUser(r.Context(), id)
	}

	s.auditLog.Record(r.Context(), audit.Entry{
		Actor: audit.ActorUser, Action: "user.disabled",
		UserID: actorID(r), ResourceType: "user", ResourceID: id.String(),
		IP: clientIP(r), Metadata: map[string]any{"disabled": req.Disabled},
	})
	writeJSON(w, http.StatusOK, map[string]bool{"disabled": req.Disabled})
}

func (s *Server) listRoles(w http.ResponseWriter, r *http.Request) {
	roles, err := s.q.ListRoles(r.Context())
	if err != nil {
		apiError(w, http.StatusInternalServerError, "database error")
		return
	}
	type roleResponse struct {
		ID          uuid.UUID `json:"id"`
		Name        string    `json:"name"`
		Description string    `json:"description"`
		IsSystem    bool      `json:"is_system"`
		Permissions []string  `json:"permissions"`
	}
	out := make([]roleResponse, 0, len(roles))
	for _, role := range roles {
		perms, err := s.q.PermissionsForRole(r.Context(), role.ID)
		if err != nil {
			perms = nil
		}
		out = append(out, roleResponse{
			ID: role.ID, Name: role.Name, Description: role.Description,
			IsSystem: role.IsSystem, Permissions: perms,
		})
	}
	writeJSON(w, http.StatusOK, out)
}

func pagination(r *http.Request) (limit, offset int32) {
	limit = 50
	if v, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && v > 0 && v <= 200 {
		limit = int32(v)
	}
	if v, err := strconv.Atoi(r.URL.Query().Get("offset")); err == nil && v >= 0 {
		offset = int32(v)
	}
	return limit, offset
}

func actorID(r *http.Request) *uuid.UUID {
	if id := Identity(r); id != nil {
		return &id.UserID
	}
	return nil
}
