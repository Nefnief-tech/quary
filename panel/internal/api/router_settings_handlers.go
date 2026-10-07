// Handshake-router endpoints: admin configuration (enable, port, default
// server) and per-server domain mappings. Every change pushes the new
// routing table to the affected nodes.
package api

import (
	"net/http"
	"strings"

	"github.com/google/uuid"

	"quarry.dev/quarry/panel/internal/audit"
	"quarry.dev/quarry/panel/internal/db/gen"
)

// getRouterSettings returns the router configuration for the admin UI.
func (s *Server) getRouterSettings(w http.ResponseWriter, r *http.Request) {
	if !rbacHas(Identity(r), "*") {
		apiError(w, http.StatusForbidden, "admin only")
		return
	}
	cfg, err := s.routerSvc.Get(r.Context())
	if err != nil {
		apiError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, cfg)
}

type routerSettingsRequest struct {
	Enabled         *bool   `json:"enabled"`
	Port            *int32  `json:"port"`
	DefaultServerID *string `json:"default_server_id"`
}

// setRouterSettings updates the router configuration and repushes tables.
func (s *Server) setRouterSettings(w http.ResponseWriter, r *http.Request) {
	if !rbacHas(Identity(r), "*") {
		apiError(w, http.StatusForbidden, "admin only")
		return
	}
	req, ok := decode[routerSettingsRequest](w, r)
	if !ok {
		return
	}
	cfg, err := s.routerSvc.Get(r.Context())
	if err != nil {
		apiError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if req.Enabled != nil {
		cfg.Enabled = *req.Enabled
	}
	if req.Port != nil {
		cfg.Port = *req.Port
	}
	if req.DefaultServerID != nil {
		cfg.DefaultServerID = strings.TrimSpace(*req.DefaultServerID)
	}
	if cfg.Enabled {
		if cfg.Port < 1024 || cfg.Port > 65535 {
			apiError(w, http.StatusBadRequest, "router port must be 1024-65535")
			return
		}
		if cfg.DefaultServerID != "" {
			if _, err := uuid.Parse(cfg.DefaultServerID); err != nil {
				apiError(w, http.StatusBadRequest, "default server id is not a uuid")
				return
			}
		}
	}
	if err := s.routerSvc.Set(r.Context(), cfg); err != nil {
		apiError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.auditLog.Record(r.Context(), audit.Entry{
		Actor: audit.ActorUser, Action: "settings.router_updated",
		UserID: actorID(r), ResourceType: "settings", ResourceID: "router",
		IP: clientIP(r), Metadata: map[string]any{"enabled": cfg.Enabled, "port": cfg.Port},
	})
	writeJSON(w, http.StatusOK, cfg)
}

// listServerDomains returns the domains routed to this server.
func (s *Server) listServerDomains(w http.ResponseWriter, r *http.Request) {
	sv, ok := s.loadServer(w, r)
	if !ok {
		return
	}
	if !rbacHas(Identity(r), "console.read") {
		apiError(w, http.StatusForbidden, "console.read required")
		return
	}
	domains, err := s.q.ListRouterDomainsByServer(r.Context(), sv.ID)
	if err != nil {
		apiError(w, http.StatusInternalServerError, "database error")
		return
	}
	type domainView struct {
		ID     string `json:"id"`
		Domain string `json:"domain"`
	}
	out := make([]domainView, 0, len(domains))
	for _, d := range domains {
		out = append(out, domainView{ID: d.ID.String(), Domain: d.Domain})
	}
	writeJSON(w, http.StatusOK, out)
}

type createDomainRequest struct {
	Domain string `json:"domain"`
}

// createServerDomain maps a hostname to this server in the router.
func (s *Server) createServerDomain(w http.ResponseWriter, r *http.Request) {
	sv, ok := s.loadServer(w, r)
	if !ok {
		return
	}
	if !rbacHas(Identity(r), "console.write") {
		apiError(w, http.StatusForbidden, "console.write required")
		return
	}
	req, ok := decode[createDomainRequest](w, r)
	if !ok {
		return
	}
	domain := normalizeDomainName(req.Domain)
	if domain == "" {
		apiError(w, http.StatusBadRequest, "a hostname like survival.example.com is required")
		return
	}
	created, err := s.q.CreateRouterDomain(r.Context(), gen.CreateRouterDomainParams{
		ID: uuid.New(), ServerID: sv.ID, Domain: domain,
	})
	if err != nil {
		apiError(w, http.StatusConflict, "domain already routed (or database error)")
		return
	}
	s.routerSvc.PushAll(r.Context())
	s.auditLog.Record(r.Context(), audit.Entry{
		Actor: audit.ActorUser, Action: "router.domain_added",
		UserID: actorID(r), ResourceType: "server", ResourceID: sv.ID.String(),
		IP: clientIP(r), Metadata: map[string]any{"domain": domain},
	})
	writeJSON(w, http.StatusCreated, map[string]string{"id": created.ID.String(), "domain": created.Domain})
}

// deleteServerDomain removes a mapping and repushes the table.
func (s *Server) deleteServerDomain(w http.ResponseWriter, r *http.Request) {
	sv, ok := s.loadServer(w, r)
	if !ok {
		return
	}
	if !rbacHas(Identity(r), "console.write") {
		apiError(w, http.StatusForbidden, "console.write required")
		return
	}
	id, err := uuid.Parse(queryValue(r, "id"))
	if err != nil {
		apiError(w, http.StatusBadRequest, "invalid domain id")
		return
	}
	if err := s.q.DeleteRouterDomain(r.Context(), gen.DeleteRouterDomainParams{ID: id, ServerID: sv.ID}); err != nil {
		apiError(w, http.StatusNotFound, "domain not found")
		return
	}
	s.routerSvc.PushAll(r.Context())
	s.auditLog.Record(r.Context(), audit.Entry{
		Actor: audit.ActorUser, Action: "router.domain_removed",
		UserID: actorID(r), ResourceType: "server", ResourceID: sv.ID.String(),
		IP: clientIP(r), Metadata: map[string]any{"domain_id": id},
	})
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// normalizeDomainName lowercases and strips scheme/port/trailing dots so
// the routing table holds plain hostnames.
func normalizeDomainName(d string) string {
	d = strings.ToLower(strings.TrimSpace(d))
	d = strings.TrimPrefix(d, "https://")
	d = strings.TrimPrefix(d, "http://")
	d = strings.TrimSuffix(d, "/")
	d = strings.TrimSuffix(d, ".")
	if i := strings.LastIndex(d, ":"); i > 0 {
		d = d[:i]
	}
	if len(d) > 253 || strings.ContainsAny(d, " \t\r\n/_") {
		return ""
	}
	if d == "" || !strings.Contains(d, ".") {
		// bare single labels are allowed only for wildcard-ish local play
		return d
	}
	return d
}
