// Player moderation: the online list comes from the hub snapshot (fed by
// the players poller); actions are console commands sent through the node,
// with panel-issued bans mirrored into server_bans so they can be listed
// and lifted without parsing console output.
package api

import (
	"errors"
	"net"
	"net/http"
	"regexp"

	"github.com/google/uuid"

	"quarry.dev/quarry/panel/internal/audit"
	"quarry.dev/quarry/panel/internal/db/gen"
	"quarry.dev/quarry/panel/internal/nodes"
	nodesv1 "quarry.dev/quarry/proto/gen/go/quarry/node/v1"
)

// mcName is a legal Minecraft player name.
var mcName = regexp.MustCompile(`^[A-Za-z0-9_]{1,16}$`)

// sendConsoleLine writes one console line to the server's node. Input is
// assembled only from validated parts, so no newline injection is possible.
func (s *Server) sendConsoleLine(w http.ResponseWriter, r *http.Request, sv gen.Server, line string) bool {
	result, err := s.channels.Send(r.Context(), sv.NodeID, &nodesv1.Command{
		Action: &nodesv1.Command_SendCommand{SendCommand: &nodesv1.SendCommand{
			ServerId: sv.ID.String(), Input: line,
		}},
	})
	if err != nil {
		if errors.Is(err, nodes.ErrNodeOffline) {
			apiError(w, http.StatusConflict, "node is offline")
		} else {
			apiError(w, http.StatusBadGateway, "node error: "+err.Error())
		}
		return false
	}
	if !result.GetOk() {
		apiError(w, http.StatusInternalServerError, result.GetError())
		return false
	}
	return true
}

// playerSnapshot returns the latest online-player list.
func (s *Server) playerSnapshot(w http.ResponseWriter, r *http.Request) {
	sv, ok := s.loadServer(w, r)
	if !ok {
		return
	}
	if !rbacHas(Identity(r), "console.read") {
		apiError(w, http.StatusForbidden, "console.read required")
		return
	}
	list, ok := s.hub.LatestPlayers(sv.ID)
	if !ok {
		writeJSON(w, http.StatusOK, map[string]any{"names": []string{}, "online": 0, "max": 0})
		return
	}
	writeJSON(w, http.StatusOK, list)
}

type playerActionRequest struct {
	Action string `json:"action"` // kick | ban | ban_ip | op | deop
	Name   string `json:"name"`
	IP     string `json:"ip"`
	Reason string `json:"reason"`
}

// playerAction applies a moderation action to a player.
func (s *Server) playerAction(w http.ResponseWriter, r *http.Request) {
	sv, ok := s.loadServer(w, r)
	if !ok {
		return
	}
	if !rbacHas(Identity(r), "console.write") {
		apiError(w, http.StatusForbidden, "console.write required")
		return
	}
	req, ok := decode[playerActionRequest](w, r)
	if !ok {
		return
	}
	if len(req.Reason) > 200 {
		apiError(w, http.StatusBadRequest, "reason must be at most 200 characters")
		return
	}
	// Reasons join a console line; control characters would inject commands.
	for _, r := range req.Reason {
		if r < 0x20 {
			apiError(w, http.StatusBadRequest, "reason must not contain control characters")
			return
		}
	}

	var line string
	ban := false
	switch req.Action {
	case "kick", "ban":
		if !mcName.MatchString(req.Name) {
			apiError(w, http.StatusBadRequest, "invalid player name")
			return
		}
		line = req.Action + " " + req.Name
		if req.Reason != "" {
			line += " " + req.Reason
		}
		ban = req.Action == "ban"
	case "ban_ip":
		if net.ParseIP(req.IP) == nil {
			apiError(w, http.StatusBadRequest, "invalid ip address")
			return
		}
		line = "ban-ip " + req.IP
		if req.Reason != "" {
			line += " " + req.Reason
		}
		ban = true
	case "op", "deop":
		if !mcName.MatchString(req.Name) {
			apiError(w, http.StatusBadRequest, "invalid player name")
			return
		}
		line = req.Action + " " + req.Name
	default:
		apiError(w, http.StatusBadRequest, "action must be kick, ban, ban_ip, op or deop")
		return
	}

	if !s.sendConsoleLine(w, r, sv, line) {
		return
	}

	if ban {
		name, ip := req.Name, ""
		if req.Action == "ban_ip" {
			name, ip = "", req.IP
		}
		if _, err := s.q.CreateServerBan(r.Context(), gen.CreateServerBanParams{
			ID: uuid.New(), ServerID: sv.ID, Name: name, Ip: ip,
			Reason: req.Reason, BannedBy: actorID(r),
		}); err != nil {
			apiError(w, http.StatusInternalServerError, "ban issued but not recorded")
			return
		}
	}
	s.auditLog.Record(r.Context(), audit.Entry{
		Actor: audit.ActorUser, Action: "player." + req.Action,
		UserID: actorID(r), ResourceType: "server", ResourceID: sv.ID.String(),
		IP: clientIP(r), Metadata: map[string]any{"name": req.Name, "ip": req.IP, "reason": req.Reason},
	})
	writeJSON(w, http.StatusOK, map[string]string{"status": "done", "action": req.Action})
}

// listServerBans returns the panel-issued bans for a server.
func (s *Server) listServerBans(w http.ResponseWriter, r *http.Request) {
	sv, ok := s.loadServer(w, r)
	if !ok {
		return
	}
	if !rbacHas(Identity(r), "console.read") {
		apiError(w, http.StatusForbidden, "console.read required")
		return
	}
	bans, err := s.q.ListServerBans(r.Context(), sv.ID)
	if err != nil {
		apiError(w, http.StatusInternalServerError, "database error")
		return
	}
	type banView struct {
		ID        uuid.UUID `json:"id"`
		Name      string    `json:"name"`
		IP        string    `json:"ip"`
		Reason    string    `json:"reason"`
		CreatedAt string    `json:"created_at"`
	}
	out := make([]banView, 0, len(bans))
	for _, b := range bans {
		out = append(out, banView{b.ID, b.Name, b.Ip, b.Reason, b.CreatedAt.Format("2006-01-02 15:04")})
	}
	writeJSON(w, http.StatusOK, out)
}

// deleteServerBan lifts a ban: it sends pardon/pardon-ip and drops the row.
func (s *Server) deleteServerBan(w http.ResponseWriter, r *http.Request) {
	sv, ok := s.loadServer(w, r)
	if !ok {
		return
	}
	if !rbacHas(Identity(r), "console.write") {
		apiError(w, http.StatusForbidden, "console.write required")
		return
	}
	banID, err := uuid.Parse(r.PathValue("banId"))
	if err != nil {
		apiError(w, http.StatusBadRequest, "invalid ban id")
		return
	}
	ban, err := s.q.GetServerBan(r.Context(), gen.GetServerBanParams{ID: banID, ServerID: sv.ID})
	if err != nil {
		apiError(w, http.StatusNotFound, "ban not found")
		return
	}
	line := "pardon " + ban.Name
	if ban.Name == "" {
		line = "pardon-ip " + ban.Ip
	}
	if !s.sendConsoleLine(w, r, sv, line) {
		return
	}
	if err := s.q.DeleteServerBan(r.Context(), gen.DeleteServerBanParams{ID: banID, ServerID: sv.ID}); err != nil {
		apiError(w, http.StatusInternalServerError, "database error")
		return
	}
	s.auditLog.Record(r.Context(), audit.Entry{
		Actor: audit.ActorUser, Action: "player.pardon",
		UserID: actorID(r), ResourceType: "server", ResourceID: sv.ID.String(),
		IP: clientIP(r), Metadata: map[string]any{"name": ban.Name, "ip": ban.Ip},
	})
	writeJSON(w, http.StatusOK, map[string]string{"status": "pardoned"})
}
