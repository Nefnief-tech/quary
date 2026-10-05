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

	// Runners (version catalog for the create form)
	mux.Handle("GET /api/v1/runners", s.requireAuth(http.HandlerFunc(s.listRunners)))

	// Node allocations (admin)
	mux.Handle("POST /api/v1/nodes/{id}/allocations", s.requirePerm("node.create", http.HandlerFunc(s.createAllocations)))
	mux.Handle("GET /api/v1/nodes/{id}/allocations", s.requirePerm("node.read", http.HandlerFunc(s.listAllocations)))

	// Servers
	mux.Handle("GET /api/v1/servers", s.requireAuth(http.HandlerFunc(s.listServers)))
	mux.Handle("POST /api/v1/servers", s.requirePerm("server.create", http.HandlerFunc(s.createServer)))
	mux.Handle("GET /api/v1/servers/{id}", s.requireAuth(http.HandlerFunc(s.getServer)))
	mux.Handle("DELETE /api/v1/servers/{id}", s.requireAuth(http.HandlerFunc(s.deleteServer)))
	mux.Handle("POST /api/v1/servers/{id}/power", s.requirePerm("server.power", http.HandlerFunc(s.powerServer)))
	mux.Handle("GET /api/v1/servers/{id}/console/token", s.requirePerm("console.read", http.HandlerFunc(s.consoleToken)))
	// The WS endpoint authenticates via its short-lived token, not sessions.
	mux.Handle("GET /api/v1/servers/{id}/console", http.HandlerFunc(s.consoleWS))
	mux.Handle("GET /api/v1/servers/{id}/stats", s.requirePerm("console.read", http.HandlerFunc(s.latestStats)))

	// Server files
	mux.Handle("GET /api/v1/servers/{id}/files", s.requirePerm("file.read", http.HandlerFunc(s.listServerFiles)))
	mux.Handle("GET /api/v1/servers/{id}/files/content", s.requirePerm("file.read", http.HandlerFunc(s.readServerFile)))
	mux.Handle("PUT /api/v1/servers/{id}/files/content", s.requirePerm("file.write", http.HandlerFunc(s.writeServerFile)))
	mux.Handle("DELETE /api/v1/servers/{id}/files", s.requirePerm("file.write", http.HandlerFunc(s.deleteServerFile)))

	// Plugins (marketplace)
	mux.Handle("GET /api/v1/servers/{id}/plugins", s.requirePerm("plugin.install", http.HandlerFunc(s.listServerPlugins)))
	mux.Handle("DELETE /api/v1/servers/{id}/plugins", s.requirePerm("plugin.install", http.HandlerFunc(s.deleteServerPlugin)))
	mux.Handle("GET /api/v1/servers/{id}/plugins/search", s.requirePerm("plugin.install", http.HandlerFunc(s.searchPlugins)))
	mux.Handle("POST /api/v1/servers/{id}/plugins/install", s.requirePerm("plugin.install", http.HandlerFunc(s.installPlugin)))

	return s.recoverPanics(s.requestLog(s.authenticate(mux)))
}
