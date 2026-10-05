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
	mux.Handle("PATCH /api/v1/servers/{id}", s.requireAuth(http.HandlerFunc(s.updateServer)))
	mux.Handle("GET /api/v1/servers/{id}/allocations", s.requireAuth(http.HandlerFunc(s.serverAllocations)))
	mux.Handle("DELETE /api/v1/servers/{id}", s.requireAuth(http.HandlerFunc(s.deleteServer)))
	mux.Handle("POST /api/v1/servers/{id}/power", s.requirePerm("server.power", http.HandlerFunc(s.powerServer)))
	mux.Handle("GET /api/v1/servers/{id}/console/token", s.requirePerm("console.read", http.HandlerFunc(s.consoleToken)))
	// The WS endpoint authenticates via its short-lived token, not sessions.
	mux.Handle("GET /api/v1/servers/{id}/console", http.HandlerFunc(s.consoleWS))
	mux.Handle("GET /api/v1/servers/{id}/stats", s.requirePerm("console.read", http.HandlerFunc(s.latestStats)))

	// Players + moderation
	mux.Handle("GET /api/v1/servers/{id}/players", s.requirePerm("console.read", http.HandlerFunc(s.playerSnapshot)))
	mux.Handle("POST /api/v1/servers/{id}/players/action", s.requirePerm("console.write", http.HandlerFunc(s.playerAction)))
	mux.Handle("GET /api/v1/servers/{id}/bans", s.requirePerm("console.read", http.HandlerFunc(s.listServerBans)))
	mux.Handle("DELETE /api/v1/servers/{id}/bans/{banId}", s.requirePerm("console.write", http.HandlerFunc(s.deleteServerBan)))

	// Whitelist
	mux.Handle("GET /api/v1/servers/{id}/whitelist", s.requirePerm("console.read", http.HandlerFunc(s.whitelistState)))
	mux.Handle("POST /api/v1/servers/{id}/whitelist", s.requirePerm("console.write", http.HandlerFunc(s.whitelistAdd)))
	mux.Handle("DELETE /api/v1/servers/{id}/whitelist", s.requirePerm("console.write", http.HandlerFunc(s.whitelistRemove)))
	mux.Handle("POST /api/v1/servers/{id}/whitelist/toggle", s.requirePerm("console.write", http.HandlerFunc(s.whitelistToggle)))

	// Version switch + public exposure (playit.gg tunnel agent)
	mux.Handle("POST /api/v1/servers/{id}/version", s.requireAuth(http.HandlerFunc(s.switchVersion)))
	mux.Handle("POST /api/v1/servers/{id}/expose", s.requirePerm("plugin.install", http.HandlerFunc(s.exposeStart)))
	mux.Handle("POST /api/v1/servers/{id}/expose/claim", s.requirePerm("plugin.install", http.HandlerFunc(s.exposeClaim)))
	mux.Handle("POST /api/v1/servers/{id}/expose/address", s.requirePerm("plugin.install", http.HandlerFunc(s.exposeAddress)))
	mux.Handle("DELETE /api/v1/servers/{id}/expose", s.requirePerm("plugin.install", http.HandlerFunc(s.exposeStop)))

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
