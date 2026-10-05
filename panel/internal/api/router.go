package api

import (
	"net/http"
)

// Router assembles the mux. Every protected route declares its permission
// here — the single authz layer for the whole API.
func (s *Server) Router() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", s.health)
	mux.HandleFunc("GET /readyz", s.ready)

	// Auth
	mux.HandleFunc("POST /api/v1/auth/register", s.register)
	mux.HandleFunc("POST /api/v1/auth/login", s.login)
	mux.HandleFunc("POST /api/v1/auth/logout", s.logout)
	mux.HandleFunc("GET /api/v1/auth/me", s.me)

	// Users (admin)
	mux.Handle("GET /api/v1/users", s.requirePerm("user.read", http.HandlerFunc(s.listUsers)))
	mux.Handle("POST /api/v1/users", s.requirePerm("user.create", http.HandlerFunc(s.createUser)))
	mux.Handle("POST /api/v1/users/{id}/disabled", s.requirePerm("user.disable", http.HandlerFunc(s.setUserDisabled)))

	// Roles (admin, read-only in Phase 0)
	mux.Handle("GET /api/v1/roles", s.requirePerm("role.read", http.HandlerFunc(s.listRoles)))

	// API keys (own)
	mux.Handle("GET /api/v1/keys", s.requireAuth(http.HandlerFunc(s.listAPIKeys)))
	mux.Handle("POST /api/v1/keys", s.requireAuth(http.HandlerFunc(s.createAPIKey)))
	mux.Handle("DELETE /api/v1/keys/{id}", s.requireAuth(http.HandlerFunc(s.deleteAPIKey)))

	// Nodes (admin)
	mux.Handle("GET /api/v1/nodes", s.requirePerm("node.read", http.HandlerFunc(s.listNodes)))
	mux.Handle("POST /api/v1/nodes", s.requirePerm("node.create", http.HandlerFunc(s.createNode)))
	mux.Handle("GET /api/v1/nodes/{id}", s.requirePerm("node.read", http.HandlerFunc(s.getNode)))
	mux.Handle("DELETE /api/v1/nodes/{id}", s.requirePerm("node.delete", http.HandlerFunc(s.deleteNode)))
	mux.Handle("POST /api/v1/nodes/{id}/token", s.requirePerm("node.create", http.HandlerFunc(s.rotateNodeToken)))

	// Audit log (admin)
	mux.Handle("GET /api/v1/audit", s.requirePerm("audit.read", http.HandlerFunc(s.listAudit)))

	return s.recoverPanics(s.requestLog(s.authenticate(mux)))
}
