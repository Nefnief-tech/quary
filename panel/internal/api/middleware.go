package api

import (
	"context"
	"net/http"
	"strings"
	"time"

	"quarry.dev/quarry/panel/internal/auth"
	"quarry.dev/quarry/panel/internal/rbac"
)

type ctxKey int

const identityKey ctxKey = 1

const sessionCookie = "quarry_session"

// Identity returns the authenticated identity, or nil for anonymous requests.
func Identity(r *http.Request) *auth.Identity {
	id, _ := r.Context().Value(identityKey).(*auth.Identity)
	return id
}

// statusRecorder captures the response status for request logging.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

// requestLog logs method, path, status, duration and user — never headers
// (they carry cookies/tokens) and never bodies.
func (s *Server) requestLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		user := ""
		if id := Identity(r); id != nil {
			user = id.Username
		}
		s.log.Info("request",
			"method", r.Method,
			"path", r.URL.Path,
			"status", rec.status,
			"duration_ms", time.Since(start).Milliseconds(),
			"remote", clientIP(r),
			"user", user,
		)
	})
}

// recover converts handler panics into 500s instead of killed connections.
func (s *Server) recoverPanics(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if err := recover(); err != nil {
				s.log.Error("panic in handler", "path", r.URL.Path, "err", err)
				apiError(w, http.StatusInternalServerError, "internal server error")
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// authenticate resolves the caller: session cookie first, then API key.
// Invalid credentials fall through as anonymous; protected routes answer 401.
func (s *Server) authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if c, err := r.Cookie(sessionCookie); err == nil && c.Value != "" {
			if id, err := s.sessions.Resolve(r.Context(), c.Value); err == nil {
				next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), identityKey, id)))
				return
			}
		}

		if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
			presented := strings.TrimSpace(strings.TrimPrefix(h, "Bearer "))
			if row, err := s.keys.Resolve(r.Context(), presented); err == nil {
				s.keys.Touch(r.Context(), row.ID)
				id := &auth.Identity{
					UserID:   row.UserID,
					RoleName: "api-key",
					Perms:    row.Scopes,
				}
				next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), identityKey, id)))
				return
			}
		}

		next.ServeHTTP(w, r)
	})
}

// requireAuth only demands a valid identity (own-resource routes).
func (s *Server) requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if Identity(r) == nil {
			apiError(w, http.StatusUnauthorized, "authentication required")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// requirePerm gates a route behind a single permission check.
func (s *Server) requirePerm(p rbac.Permission, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := Identity(r)
		if id == nil {
			apiError(w, http.StatusUnauthorized, "authentication required")
			return
		}
		if !rbac.Has(id.Perms, p) {
			apiError(w, http.StatusForbidden, "insufficient permissions")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) setSessionCookie(w http.ResponseWriter, token string, expires time.Time) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    token,
		Path:     "/",
		Expires:  expires,
		HttpOnly: true,
		Secure:   s.cfg.Production(),
		SameSite: http.SameSiteLaxMode,
	})
}

func (s *Server) clearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   s.cfg.Production(),
		SameSite: http.SameSiteLaxMode,
	})
}
