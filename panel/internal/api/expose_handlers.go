// Public exposure (playit.gg) and version switching endpoints.
package api

import (
	"context"
	"errors"
	"net/http"
	"time"

	"quarry.dev/quarry/panel/internal/audit"
	"quarry.dev/quarry/panel/internal/nodes"
	"quarry.dev/quarry/panel/internal/servers"
	nodesv1 "quarry.dev/quarry/proto/gen/go/quarry/node/v1"
)

// exposeInstall installs the playit.gg companion plugin. After the next
// server start the plugin prints a claim URL on the console; the panel
// captures it (and the later public address) into the server's expose
// state, which the UI renders from server.expose.
func (s *Server) exposeInstall(w http.ResponseWriter, r *http.Request) {
	sv, loader, contentDir, ok := s.loadPluginServer(w, r)
	if !ok {
		return
	}
	if !s.channels.Connected(sv.NodeID) {
		apiError(w, http.StatusConflict, "node is offline")
		return
	}
	dl, err := s.modrinth.Resolve(r.Context(), servers.PlayitProject, sv.McVersion, loader)
	if err != nil {
		apiError(w, http.StatusConflict, "no playit.gg build for "+sv.Runner+" "+sv.McVersion+": "+err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	result, err := s.channels.Send(ctx, sv.NodeID, &nodesv1.Command{
		Action: &nodesv1.Command_InstallPlugin{InstallPlugin: &nodesv1.InstallPlugin{
			ServerId: sv.ID.String(),
			Url:      dl.URL,
			Sha512:   dl.SHA512,
			Filename: contentDir + "/" + servers.PlayitFilename,
		}},
	})
	if err != nil {
		if errors.Is(err, nodes.ErrNodeOffline) {
			apiError(w, http.StatusConflict, "node is offline")
			return
		}
		apiError(w, http.StatusBadGateway, "install failed: "+err.Error())
		return
	}
	if !result.GetOk() {
		apiError(w, http.StatusInternalServerError, result.GetError())
		return
	}
	if err := s.serverSvc.MarkExposeInstalled(r.Context(), sv.ID); err != nil {
		apiError(w, http.StatusInternalServerError, "installed but state not saved")
		return
	}
	s.auditLog.Record(r.Context(), audit.Entry{
		Actor: audit.ActorUser, Action: "server.expose_installed",
		UserID: actorID(r), ResourceType: "server", ResourceID: sv.ID.String(),
		IP: clientIP(r), Metadata: map[string]any{"version": dl.Version},
	})
	writeJSON(w, http.StatusOK, map[string]any{
		"status": "installed",
		"hint":   "restart the server, then open the claim link shown here to activate the tunnel",
	})
}

// switchVersionRequest is the version-switch input.
type switchVersionRequest struct {
	Runner    string `json:"runner"`
	MCVersion string `json:"mc_version"`
}

// switchVersion replaces the server jar while keeping worlds and configs.
func (s *Server) switchVersion(w http.ResponseWriter, r *http.Request) {
	sv, ok := s.loadServer(w, r)
	if !ok {
		return
	}
	req, ok := decode[switchVersionRequest](w, r)
	if !ok {
		return
	}
	updated, notes, err := s.serverSvc.SwitchVersion(r.Context(), sv, servers.SwitchVersionRequest{
		Runner: req.Runner, MCVersion: req.MCVersion,
	})
	if err != nil {
		switch {
		case errors.Is(err, servers.ErrBadState):
			apiError(w, http.StatusConflict, err.Error())
		case errors.Is(err, nodes.ErrNodeOffline):
			apiError(w, http.StatusConflict, err.Error())
		default:
			apiError(w, http.StatusBadRequest, err.Error())
		}
		return
	}
	s.auditLog.Record(r.Context(), audit.Entry{
		Actor: audit.ActorUser, Action: "server.version_switched",
		UserID: actorID(r), ResourceType: "server", ResourceID: sv.ID.String(),
		IP: clientIP(r), Metadata: map[string]any{"runner": req.Runner, "mc_version": req.MCVersion},
	})
	writeJSON(w, http.StatusOK, map[string]any{"notes": notes, "server": s.serverResponse(r.Context(), updated)})
}
