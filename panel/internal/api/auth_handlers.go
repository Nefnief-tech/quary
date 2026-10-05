package api

import (
	"errors"
	"net/http"
	"regexp"
	"time"

	"github.com/google/uuid"

	"quarry.dev/quarry/panel/internal/audit"
	"quarry.dev/quarry/panel/internal/auth"
	"quarry.dev/quarry/panel/internal/db/gen"
	"quarry.dev/quarry/panel/internal/rbac"
)

var usernameRe = regexp.MustCompile(`^[a-zA-Z0-9_-]{3,32}$`)

type userResponse struct {
	ID        uuid.UUID `json:"id"`
	Username  string    `json:"username"`
	Email     *string   `json:"email"`
	RoleName  string    `json:"role_name"`
	Disabled  bool      `json:"disabled"`
	CreatedAt time.Time `json:"created_at"`
}

type credentialsRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
	Email    string `json:"email"`
}

func (s *Server) validateCredentials(username, password string) error {
	if !usernameRe.MatchString(username) {
		return errors.New("username must be 3-32 characters of letters, digits, '_' or '-'")
	}
	if len(password) < 10 {
		return errors.New("password must be at least 10 characters")
	}
	return nil
}

// register: the first ever registration becomes the admin; afterwards this
// endpoint is admin-only (user.create).
func (s *Server) register(w http.ResponseWriter, r *http.Request) {
	req, ok := decode[credentialsRequest](w, r)
	if !ok {
		return
	}
	if err := s.validateCredentials(req.Username, req.Password); err != nil {
		apiError(w, http.StatusBadRequest, err.Error())
		return
	}

	ctx := r.Context()
	count, err := s.q.CountUsers(ctx)
	if err != nil {
		apiError(w, http.StatusInternalServerError, "database error")
		return
	}

	first := count == 0
	if !first {
		id := Identity(r)
		if id == nil {
			apiError(w, http.StatusUnauthorized, "authentication required")
			return
		}
		if !rbac.Has(id.Perms, rbac.UserCreate) {
			apiError(w, http.StatusForbidden, "insufficient permissions")
			return
		}
	}

	roleName := "user"
	if first {
		roleName = "admin"
	}
	role, err := s.q.GetRoleByName(ctx, roleName)
	if err != nil {
		apiError(w, http.StatusInternalServerError, "role lookup failed")
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
	user, err := s.q.CreateUser(ctx, gen.CreateUserParams{
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

	s.auditLog.Record(ctx, audit.Entry{
		Actor: audit.ActorUser, Action: "user.created",
		ResourceType: "user", ResourceID: user.ID.String(),
		IP: clientIP(r), Metadata: map[string]any{"username": user.Username, "first_admin": first},
	})

	if first {
		// Bootstrap convenience: the first admin starts logged in.
		if err := s.startSession(w, r, user.ID); err != nil {
			apiError(w, http.StatusInternalServerError, "session creation failed")
			return
		}
	}
	writeJSON(w, http.StatusCreated, userResponse{
		ID: user.ID, Username: user.Username, Email: email,
		RoleName: roleName, Disabled: user.Disabled, CreatedAt: user.CreatedAt,
	})
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	req, ok := decode[credentialsRequest](w, r)
	if !ok {
		return
	}
	key := clientIP(r) + "|" + req.Username
	if !s.limiter.Allow(key) {
		apiError(w, http.StatusTooManyRequests, "too many login attempts; slow down")
		return
	}

	ctx := r.Context()
	user, err := s.q.GetUserByUsername(ctx, req.Username)
	if err != nil || !auth.VerifyPassword(req.Password, user.PasswordHash) || user.Disabled {
		s.auditLog.Record(ctx, audit.Entry{
			Actor: audit.ActorUser, Action: "auth.login_failed",
			IP: clientIP(r), Metadata: map[string]any{"username": req.Username},
		})
		apiError(w, http.StatusUnauthorized, "invalid credentials")
		return
	}

	s.limiter.Reset(key)
	if err := s.startSession(w, r, user.ID); err != nil {
		apiError(w, http.StatusInternalServerError, "session creation failed")
		return
	}

	s.auditLog.Record(ctx, audit.Entry{
		Actor: audit.ActorUser, Action: "auth.login",
		UserID: &user.ID, IP: clientIP(r),
	})

	role, err := s.q.RoleOfUser(ctx, user.ID)
	if err != nil {
		role.Name = ""
	}
	writeJSON(w, http.StatusOK, userResponse{
		ID: user.ID, Username: user.Username, Email: user.Email,
		RoleName: role.Name, Disabled: user.Disabled, CreatedAt: user.CreatedAt,
	})
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	id := Identity(r)
	if id != nil && id.SessionID != uuid.Nil {
		if err := s.sessions.Delete(r.Context(), id.SessionID); err != nil {
			apiError(w, http.StatusInternalServerError, "logout failed")
			return
		}
		s.auditLog.Record(r.Context(), audit.Entry{
			Actor: audit.ActorUser, Action: "auth.logout", UserID: &id.UserID, IP: clientIP(r),
		})
	}
	s.clearSessionCookie(w)
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) me(w http.ResponseWriter, r *http.Request) {
	id := Identity(r)
	if id == nil {
		apiError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	writeJSON(w, http.StatusOK, userResponse{
		ID: id.UserID, Username: id.Username, Email: id.Email,
		RoleName: id.RoleName, Disabled: id.Disabled,
	})
}

// startSession creates a session row and sets the cookie.
func (s *Server) startSession(w http.ResponseWriter, r *http.Request, userID uuid.UUID) error {
	token, expires, err := s.sessions.Create(r.Context(), userID, clientIP(r), r.UserAgent())
	if err != nil {
		return err
	}
	s.setSessionCookie(w, token, expires)
	return nil
}
