// Public exposure (playit.gg) and version switching endpoints.
package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strings"

	"quarry.dev/quarry/panel/internal/audit"
	"quarry.dev/quarry/panel/internal/db/gen"
	"quarry.dev/quarry/panel/internal/nodes"
	"quarry.dev/quarry/panel/internal/servers"
	nodesv1 "quarry.dev/quarry/proto/gen/go/quarry/node/v1"
)

// Public exposure via the playit.gg tunnel agent: the panel drives the
// daemon's claim flow (start, check claim, stop) and persists the state on
// the server config so the UI can render it.

// exposeAddressRe validates a user-entered public address (the tunnel
// domain playit assigns, e.g. slow-purple-tiger.playit.gg).
var exposeAddressRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)+$`)

// sendExpose runs an expose command on the node and returns its result.
func (s *Server) sendExpose(w http.ResponseWriter, r *http.Request, sv gen.Server, cmd *nodesv1.Command) (*nodesv1.CommandResult, bool) {
	result, err := s.channels.Send(r.Context(), sv.NodeID, cmd)
	if err != nil {
		if errors.Is(err, nodes.ErrNodeOffline) {
			apiError(w, http.StatusConflict, "node is offline")
		} else {
			apiError(w, http.StatusBadGateway, "node error: "+err.Error())
		}
		return nil, false
	}
	if !result.GetOk() {
		apiError(w, http.StatusInternalServerError, result.GetError())
		return nil, false
	}
	return result, true
}

// exposeStart begins tunnel setup. Provider "frp" maps the server to a
// remote port on the configured frps; the default (playit) runs the claim
// flow.
func (s *Server) exposeStart(w http.ResponseWriter, r *http.Request) {
	sv, ok := s.loadServer(w, r)
	if !ok {
		return
	}
	if !s.channels.Connected(sv.NodeID) {
		apiError(w, http.StatusConflict, "node is offline")
		return
	}
	req, _ := decode[exposeStartRequest](w, r)

	switch req.Provider {
	case "bore":
		s.exposeStartBore(w, r, sv)
		return
	case "frp":
		s.exposeStartFrp(w, r, sv, req.RemotePort)
		return
	}

	result, ok := s.sendExpose(w, r, sv, &nodesv1.Command{
		Action: &nodesv1.Command_ExposeStart{ExposeStart: &nodesv1.ExposeStart{
			ServerId: sv.ID.String(),
		}},
	})
	if !ok {
		return
	}
	var st struct {
		Active   bool   `json:"active"`
		ClaimURL string `json:"claim_url"`
	}
	if err := json.Unmarshal(result.GetBody(), &st); err != nil {
		apiError(w, http.StatusBadGateway, "unexpected daemon reply")
		return
	}

	ex := &servers.ExposeConfig{Provider: "playit"}
	switch {
	case st.Active:
		ex.Active = true
	case st.ClaimURL != "":
		ex.Pending = true
		ex.ClaimURL = st.ClaimURL
	}
	if err := s.serverSvc.SetExpose(r.Context(), sv.ID, ex); err != nil {
		apiError(w, http.StatusInternalServerError, "setup started but state not saved")
		return
	}
	s.auditLog.Record(r.Context(), audit.Entry{
		Actor: audit.ActorUser, Action: "server.expose_started",
		UserID: actorID(r), ResourceType: "server", ResourceID: sv.ID.String(), IP: clientIP(r),
	})
	writeJSON(w, http.StatusOK, map[string]any{"expose": ex})
}

type exposeStartRequest struct {
	Provider   string `json:"provider"`
	RemotePort int32  `json:"remote_port"`
}

// exposeStartBore opens a zero-account bore.pub tunnel for this server.
func (s *Server) exposeStartBore(w http.ResponseWriter, r *http.Request, sv gen.Server) {
	alloc, err := s.q.GetAllocationForServer(r.Context(), sv.ID)
	if err != nil {
		apiError(w, http.StatusInternalServerError, "server allocation missing")
		return
	}
	result, ok := s.sendExpose(w, r, sv, &nodesv1.Command{
		Action: &nodesv1.Command_ExposeStart{ExposeStart: &nodesv1.ExposeStart{
			ServerId:  sv.ID.String(),
			Provider:  "bore",
			LocalPort: alloc.Port,
		}},
	})
	if !ok {
		return
	}
	var st struct {
		Active  bool   `json:"active"`
		Address string `json:"address"`
	}
	if err := json.Unmarshal(result.GetBody(), &st); err != nil {
		apiError(w, http.StatusBadGateway, "unexpected daemon reply")
		return
	}
	ex := &servers.ExposeConfig{Provider: "bore", Active: st.Active, Address: st.Address}
	if err := s.serverSvc.SetExpose(r.Context(), sv.ID, ex); err != nil {
		apiError(w, http.StatusInternalServerError, "tunnel started but state not saved")
		return
	}
	s.auditLog.Record(r.Context(), audit.Entry{
		Actor: audit.ActorUser, Action: "server.expose_started",
		UserID: actorID(r), ResourceType: "server", ResourceID: sv.ID.String(),
		IP: clientIP(r), Metadata: map[string]any{"provider": "bore", "address": st.Address},
	})
	writeJSON(w, http.StatusOK, map[string]any{"expose": ex})
}

// exposeStartFrp claims a remote port on the configured frps for this
// server and starts the frpc client on the node.
func (s *Server) exposeStartFrp(w http.ResponseWriter, r *http.Request, sv gen.Server, remotePort int32) {
	frp, err := s.frpForNode(r.Context())
	if err != nil {
		apiError(w, http.StatusConflict, err.Error())
		return
	}
	alloc, err := s.q.GetAllocationForServer(r.Context(), sv.ID)
	if err != nil {
		apiError(w, http.StatusInternalServerError, "server allocation missing")
		return
	}
	result, ok := s.sendExpose(w, r, sv, &nodesv1.Command{
		Action: &nodesv1.Command_ExposeStart{ExposeStart: &nodesv1.ExposeStart{
			ServerId:   sv.ID.String(),
			Provider:   "frp",
			FrpsAddr:   frp.ServerAddr,
			FrpsPort:   frp.ServerPort,
			FrpsToken:  frp.Token,
			RemotePort: remotePort,
			LocalPort:  alloc.Port,
		}},
	})
	if !ok {
		return
	}
	var st struct {
		Active  bool   `json:"active"`
		Address string `json:"address"`
	}
	if err := json.Unmarshal(result.GetBody(), &st); err != nil {
		apiError(w, http.StatusBadGateway, "unexpected daemon reply")
		return
	}
	ex := &servers.ExposeConfig{
		Provider:   "frp",
		Active:     st.Active,
		Address:    st.Address,
		RemotePort: remotePort,
	}
	if err := s.serverSvc.SetExpose(r.Context(), sv.ID, ex); err != nil {
		apiError(w, http.StatusInternalServerError, "tunnel started but state not saved")
		return
	}
	s.auditLog.Record(r.Context(), audit.Entry{
		Actor: audit.ActorUser, Action: "server.expose_started",
		UserID: actorID(r), ResourceType: "server", ResourceID: sv.ID.String(),
		IP: clientIP(r), Metadata: map[string]any{"provider": "frp", "remote_port": remotePort},
	})
	writeJSON(w, http.StatusOK, map[string]any{"expose": ex})
}

// exposeClaim checks whether the user completed the browser claim and, if
// so, starts the tunnel agent.
func (s *Server) exposeClaim(w http.ResponseWriter, r *http.Request) {
	sv, ok := s.loadServer(w, r)
	if !ok {
		return
	}
	result, ok := s.sendExpose(w, r, sv, &nodesv1.Command{
		Action: &nodesv1.Command_ExposeClaim{ExposeClaim: &nodesv1.ExposeClaim{
			ServerId: sv.ID.String(),
		}},
	})
	if !ok {
		return
	}
	var st struct {
		Active   bool   `json:"active"`
		ClaimURL string `json:"claim_url"`
		State    string `json:"state"`
	}
	if err := json.Unmarshal(result.GetBody(), &st); err != nil {
		apiError(w, http.StatusBadGateway, "unexpected daemon reply")
		return
	}

	var ex *servers.ExposeConfig
	if st.Active {
		ex = &servers.ExposeConfig{Active: true}
	} else {
		// Still pending: keep the URL from the current state.
		var cfg servers.Config
		_ = json.Unmarshal(sv.Config, &cfg)
		if cfg.Expose != nil {
			ex = cfg.Expose
		} else {
			ex = &servers.ExposeConfig{Pending: true}
		}
	}
	if err := s.serverSvc.SetExpose(r.Context(), sv.ID, ex); err != nil {
		apiError(w, http.StatusInternalServerError, "state not saved")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"expose": ex, "state": st.State})
}

// exposeStop removes the tunnel and forgets the claim state.
func (s *Server) exposeStop(w http.ResponseWriter, r *http.Request) {
	sv, ok := s.loadServer(w, r)
	if !ok {
		return
	}
	provider := ""
	var cfg servers.Config
	_ = json.Unmarshal(sv.Config, &cfg)
	if cfg.Expose != nil {
		provider = cfg.Expose.Provider
	}
	if _, ok := s.sendExpose(w, r, sv, &nodesv1.Command{
		Action: &nodesv1.Command_ExposeStop{ExposeStop: &nodesv1.ExposeStop{
			ServerId: sv.ID.String(),
			Provider: provider,
		}},
	}); !ok {
		return
	}
	if err := s.serverSvc.SetExpose(r.Context(), sv.ID, nil); err != nil {
		apiError(w, http.StatusInternalServerError, "state not saved")
		return
	}
	s.auditLog.Record(r.Context(), audit.Entry{
		Actor: audit.ActorUser, Action: "server.expose_stopped",
		UserID: actorID(r), ResourceType: "server", ResourceID: sv.ID.String(), IP: clientIP(r),
	})
	writeJSON(w, http.StatusOK, map[string]any{"expose": nil})
}

type exposeAddressRequest struct {
	Address string `json:"address"`
}

// exposeAddress records the public tunnel address (assigned in the playit
// dashboard) for display.
func (s *Server) exposeAddress(w http.ResponseWriter, r *http.Request) {
	sv, ok := s.loadServer(w, r)
	if !ok {
		return
	}
	req, ok := decode[exposeAddressRequest](w, r)
	if !ok {
		return
	}
	addr := strings.Trim(strings.TrimSpace(req.Address), "/")
	if !exposeAddressRe.MatchString(addr) {
		apiError(w, http.StatusBadRequest, "invalid address (expected e.g. my-tunnel.playit.gg)")
		return
	}
	var cfg servers.Config
	_ = json.Unmarshal(sv.Config, &cfg)
	ex := &servers.ExposeConfig{Active: true, Address: addr}
	if cfg.Expose != nil {
		ex.ClaimURL = cfg.Expose.ClaimURL
		ex.Pending = cfg.Expose.Pending
	}
	if err := s.serverSvc.SetExpose(r.Context(), sv.ID, ex); err != nil {
		apiError(w, http.StatusInternalServerError, "state not saved")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"expose": ex})
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
