// Whitelist management and version switching. Whitelist operations go
// through the generic RunConsole command (console commands remain the
// loader-agnostic path); the enabled flag is read from server.properties.
package api

import (
	"encoding/json"
	"net/http"
	"regexp"
	"strings"

	"quarry.dev/quarry/panel/internal/audit"
	"quarry.dev/quarry/panel/internal/db/gen"
	"quarry.dev/quarry/panel/internal/nodes"
	nodesv1 "quarry.dev/quarry/proto/gen/go/quarry/node/v1"
)

// whitelistRe matches Vanilla/Paper/Fabric `whitelist list` output. Paper
// prints "player(s)", Vanilla "player"/"players"; the empty case is
// "There are no whitelisted players".
var whitelistRe = regexp.MustCompile(`There are \d+ whitelisted players?(?:\(s\))?(?::\s*(.*))?\s*$`)

// propsWhitelistRe reads the white-list flag out of server.properties.
var propsWhitelistRe = regexp.MustCompile(`(?m)^\s*white-list\s*=\s*(true|false)\s*$`)

// runConsoleCapture sends one console command through RunConsole and
// returns the captured lines.
func (s *Server) runConsoleCapture(w http.ResponseWriter, r *http.Request, sv gen.Server, input, expect string) ([]string, bool) {
	result, err := s.channels.Send(r.Context(), sv.NodeID, &nodesv1.Command{
		Action: &nodesv1.Command_RunConsole{RunConsole: &nodesv1.RunConsole{
			ServerId: sv.ID.String(), Input: input, Expect: expect, MaxLines: 1,
		}},
	})
	if err != nil {
		if err == nodes.ErrNodeOffline {
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
	var cap struct {
		Lines []string `json:"lines"`
	}
	if err := json.Unmarshal(result.GetBody(), &cap); err != nil {
		apiError(w, http.StatusBadGateway, "console reply undecodable")
		return nil, false
	}
	return cap.Lines, true
}

// whitelistState returns names + enabled flag for the whitelist card.
func (s *Server) whitelistState(w http.ResponseWriter, r *http.Request) {
	sv, ok := s.loadServer(w, r)
	if !ok {
		return
	}
	if !rbacHas(Identity(r), "console.read") {
		apiError(w, http.StatusForbidden, "console.read required")
		return
	}

	enabled := false
	if result, err := s.channels.Send(r.Context(), sv.NodeID, &nodesv1.Command{
		Action: &nodesv1.Command_ReadFile{ReadFile: &nodesv1.ReadFile{
			ServerId: sv.ID.String(), Path: "server.properties", MaxBytes: 1 << 20,
		}},
	}); err == nil && result.GetOk() {
		if m := propsWhitelistRe.FindStringSubmatch(string(result.GetBody())); m != nil {
			enabled = m[1] == "true"
		}
	}

	names := []string{}
	if lines, ok := s.runConsoleCapture(w, r, sv, "whitelist list", `There are (\d+|no) whitelisted player`); ok {
		for _, line := range lines {
			if m := whitelistRe.FindStringSubmatch(line); m != nil && strings.TrimSpace(m[1]) != "" {
				names = nil
				for _, n := range strings.Split(m[1], ", ") {
					if n = strings.TrimSpace(n); n != "" {
						names = append(names, n)
					}
				}
			}
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"enabled": enabled, "names": names})
}

type whitelistAddRequest struct {
	Name string `json:"name"`
}

// whitelistAdd adds a player to the whitelist.
func (s *Server) whitelistAdd(w http.ResponseWriter, r *http.Request) {
	s.whitelistMutation(w, r, "add")
}

// whitelistRemove removes a player from the whitelist.
func (s *Server) whitelistRemove(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("name")
	if !mcName.MatchString(name) {
		apiError(w, http.StatusBadRequest, "invalid player name")
		return
	}
	s.whitelistMutation(w, r, "remove", name)
}

type whitelistToggleRequest struct {
	Enabled bool `json:"enabled"`
}

// whitelistToggle turns the whitelist on or off (takes effect immediately).
func (s *Server) whitelistToggle(w http.ResponseWriter, r *http.Request) {
	req, ok := decode[whitelistToggleRequest](w, r)
	if !ok {
		return
	}
	state := "off"
	if req.Enabled {
		state = "on"
	}
	s.whitelistMutation(w, r, state)
}

// whitelistMutation validates and sends a whitelist console command.
func (s *Server) whitelistMutation(w http.ResponseWriter, r *http.Request, action string, args ...string) {
	sv, ok := s.loadServer(w, r)
	if !ok {
		return
	}
	if !rbacHas(Identity(r), "console.write") {
		apiError(w, http.StatusForbidden, "console.write required")
		return
	}
	line := "whitelist " + action
	for _, a := range args {
		line += " " + a
	}
	if action == "add" {
		req, ok := decode[whitelistAddRequest](w, r)
		if !ok {
			return
		}
		if !mcName.MatchString(req.Name) {
			apiError(w, http.StatusBadRequest, "invalid player name")
			return
		}
		line = "whitelist add " + req.Name
	}
	if !s.sendConsoleLine(w, r, sv, line) {
		return
	}
	s.auditLog.Record(r.Context(), audit.Entry{
		Actor: audit.ActorUser, Action: "whitelist." + action,
		UserID: actorID(r), ResourceType: "server", ResourceID: sv.ID.String(),
		IP: clientIP(r), Metadata: map[string]any{"args": args},
	})
	writeJSON(w, http.StatusOK, map[string]string{"status": "done"})
}
