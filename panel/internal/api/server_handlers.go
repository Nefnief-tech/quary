package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"

	"quarry.dev/quarry/panel/internal/audit"
	"quarry.dev/quarry/panel/internal/auth"
	"quarry.dev/quarry/panel/internal/db/gen"
	"quarry.dev/quarry/panel/internal/hub"
	"quarry.dev/quarry/panel/internal/lifecycle"
	"quarry.dev/quarry/panel/internal/nodes"
	"quarry.dev/quarry/panel/internal/servers"
	nodesv1 "quarry.dev/quarry/proto/gen/go/quarry/node/v1"
)

// serverResponse is the API view of a server.
type serverResponse struct {
	ID         uuid.UUID `json:"id"`
	Name       string    `json:"name"`
	OwnerID    uuid.UUID `json:"owner_id"`
	NodeID     uuid.UUID `json:"node_id"`
	Runner     string    `json:"runner"`
	MCVersion  string    `json:"mc_version"`
	Status     string    `json:"status"`
	MemoryMB   int32     `json:"memory_mb"`
	CPUPercent int32     `json:"cpu_percent"`
	IP         string    `json:"ip"`
	Port       int32     `json:"port"`
	// MapURL is the browser-reachable BlueMap webserver address; empty when
	// the server has no preinstalled map.
	MapURL    string    `json:"map_url,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

func (s *Server) serverResponse(ctx context.Context, sv gen.Server) serverResponse {
	out := serverResponse{
		ID: sv.ID, Name: sv.Name, OwnerID: sv.OwnerID, NodeID: sv.NodeID,
		Runner: sv.Runner, MCVersion: sv.McVersion, Status: sv.Status,
		MemoryMB: sv.MemoryMb, CPUPercent: sv.CpuPercent, CreatedAt: sv.CreatedAt,
	}
	if alloc, err := s.q.GetAllocationForServer(ctx, sv.ID); err == nil {
		out.IP = alloc.Ip
		out.Port = alloc.Port
	}
	if sv.MapAllocationID != nil {
		if mapAlloc, err := s.q.GetAllocation(ctx, *sv.MapAllocationID); err == nil {
			host := ""
			if node, err := s.q.GetNode(ctx, sv.NodeID); err == nil {
				host = node.PublicHost
				if host == "" {
					host = node.Hostname
				}
			}
			if host != "" {
				out.MapURL = fmt.Sprintf("http://%s:%d", host, mapAlloc.Port)
			}
		}
	}
	return out
}

// canAccess allows owners and wildcard admins.
func canAccess(id *auth.Identity, ownerID uuid.UUID) bool {
	return id.UserID == ownerID || rbacHas(id, "*")
}

func rbacHas(id *auth.Identity, perm string) bool {
	for _, p := range id.Perms {
		if p == perm || p == "*" {
			return true
		}
	}
	return false
}

type createServerRequest struct {
	Name      string `json:"name"`
	NodeID    string `json:"node_id"`
	Runner    string `json:"runner"`
	MCVersion string `json:"mc_version"`
	MemoryMB  int32  `json:"memory_mb"`
}

func (s *Server) listServers(w http.ResponseWriter, r *http.Request) {
	id := Identity(r)
	var list []gen.Server
	var err error
	if rbacHas(id, "*") {
		list, err = s.q.ListServers(r.Context())
	} else {
		list, err = s.q.ListServersByOwner(r.Context(), id.UserID)
	}
	if err != nil {
		apiError(w, http.StatusInternalServerError, "database error")
		return
	}
	out := make([]serverResponse, 0, len(list))
	for _, sv := range list {
		out = append(out, s.serverResponse(r.Context(), sv))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) createServer(w http.ResponseWriter, r *http.Request) {
	req, ok := decode[createServerRequest](w, r)
	if !ok {
		return
	}
	nodeID, err := uuid.Parse(req.NodeID)
	if err != nil {
		nodeID = uuid.Nil // let the service pick any online node with a free allocation
	}
	sv, err := s.serverSvc.Create(r.Context(), Identity(r).UserID, servers.CreateRequest{
		Name: req.Name, NodeID: nodeID, Runner: req.Runner,
		MCVersion: req.MCVersion, MemoryMB: req.MemoryMB,
	})
	if err != nil {
		switch {
		case errors.Is(err, nodes.ErrNodeOffline):
			apiError(w, http.StatusConflict, "node is offline")
		case errors.Is(err, servers.ErrNoAllocations):
			apiError(w, http.StatusConflict, err.Error())
		default:
			apiError(w, http.StatusBadRequest, err.Error())
		}
		return
	}
	s.auditLog.Record(r.Context(), audit.Entry{
		Actor: audit.ActorUser, Action: "server.created",
		UserID: actorID(r), ResourceType: "server", ResourceID: sv.ID.String(),
		IP: clientIP(r), Metadata: map[string]any{"name": sv.Name, "runner": sv.Runner, "mc_version": sv.McVersion},
	})
	writeJSON(w, http.StatusCreated, s.serverResponse(r.Context(), sv))
}

func (s *Server) getServer(w http.ResponseWriter, r *http.Request) {
	sv, ok := s.loadServer(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, s.serverResponse(r.Context(), sv))
}

// loadServer parses the id, checks existence and access.
func (s *Server) loadServer(w http.ResponseWriter, r *http.Request) (gen.Server, bool) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		apiError(w, http.StatusBadRequest, "invalid server id")
		return gen.Server{}, false
	}
	sv, err := s.q.GetServer(r.Context(), id)
	if err != nil {
		apiError(w, http.StatusNotFound, "server not found")
		return gen.Server{}, false
	}
	if !canAccess(Identity(r), sv.OwnerID) {
		apiError(w, http.StatusForbidden, "not your server")
		return gen.Server{}, false
	}
	return sv, true
}

type powerRequest struct {
	Action string `json:"action"`
}

func (s *Server) powerServer(w http.ResponseWriter, r *http.Request) {
	sv, ok := s.loadServer(w, r)
	if !ok {
		return
	}
	req, ok := decode[powerRequest](w, r)
	if !ok {
		return
	}
	action := lifecycle.PowerAction(req.Action)
	if err := s.serverSvc.Power(r.Context(), sv, action); err != nil {
		switch {
		case errors.Is(err, servers.ErrBadState):
			apiError(w, http.StatusConflict, err.Error())
		case errors.Is(err, nodes.ErrNodeOffline):
			apiError(w, http.StatusConflict, err.Error())
		default:
			apiError(w, http.StatusInternalServerError, "power action failed")
		}
		return
	}
	s.auditLog.Record(r.Context(), audit.Entry{
		Actor: audit.ActorUser, Action: "server.power",
		UserID: actorID(r), ResourceType: "server", ResourceID: sv.ID.String(),
		IP: clientIP(r), Metadata: map[string]any{"action": req.Action},
	})
	writeJSON(w, http.StatusOK, map[string]string{"status": "dispatched", "action": req.Action})
}

func (s *Server) deleteServer(w http.ResponseWriter, r *http.Request) {
	sv, ok := s.loadServer(w, r)
	if !ok {
		return
	}
	if err := s.serverSvc.Delete(r.Context(), sv); err != nil {
		if errors.Is(err, servers.ErrBadState) {
			apiError(w, http.StatusConflict, err.Error())
			return
		}
		apiError(w, http.StatusInternalServerError, "delete failed")
		return
	}
	s.auditLog.Record(r.Context(), audit.Entry{
		Actor: audit.ActorUser, Action: "server.deleted",
		UserID: actorID(r), ResourceType: "server", ResourceID: sv.ID.String(), IP: clientIP(r),
	})
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// ---------------------------------------------------------------------------
// Console (WebSocket), stats
// ---------------------------------------------------------------------------

// consoleToken mints a short-lived WS token after permission checks.
func (s *Server) consoleToken(w http.ResponseWriter, r *http.Request) {
	sv, ok := s.loadServer(w, r)
	if !ok {
		return
	}
	id := Identity(r)
	if !rbacHas(id, "console.read") || !rbacHas(id, "console.write") {
		apiError(w, http.StatusForbidden, "console permissions required")
		return
	}
	token := hub.SignWSToken(s.sealKey, sv.ID, id.UserID, 60*time.Second)
	writeJSON(w, http.StatusOK, map[string]any{
		"token":      token,
		"url":        "/api/v1/servers/" + sv.ID.String() + "/console?token=" + token,
		"scrollback": s.hub.Scrollback(sv.ID),
	})
}

var wsUpgrader = websocket.Upgrader{
	ReadBufferSize:  4096,
	WriteBufferSize: 4096,
	// Access is gated by the short-lived signed token minted after the
	// permission checks, so origin enforcement adds nothing — and gorilla's
	// default (reject when Origin host != request Host) wrongly rejects
	// browsers behind the dev proxy, where Origin (:5173) and Host (:8080)
	// legitimately differ.
	CheckOrigin: func(*http.Request) bool { return true },
}

// consoleWS streams console lines/status/stats to the browser and forwards
// typed input to the server.
func (s *Server) consoleWS(w http.ResponseWriter, r *http.Request) {
	serverID, _, err := hub.VerifyWSToken(s.sealKey, r.URL.Query().Get("token"))
	if err != nil {
		apiError(w, http.StatusUnauthorized, "invalid console token")
		return
	}
	sv, err := s.q.GetServer(r.Context(), serverID)
	if err != nil {
		apiError(w, http.StatusNotFound, "server not found")
		return
	}
	nodeID := sv.NodeID

	conn, err := wsUpgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer func() { _ = conn.Close() }()

	events, cancel := s.hub.Subscribe(serverID)
	defer cancel()

	writeMu := new(sync.Mutex)
	sendJSON := func(v any) error {
		writeMu.Lock()
		defer writeMu.Unlock()
		_ = conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
		return conn.WriteJSON(v)
	}

	// Replay scrollback so the console isn't blank, plus the latest player
	// snapshot so the Players tab starts populated.
	for _, line := range s.hub.Scrollback(serverID) {
		if err := sendJSON(map[string]any{"type": "console", "line": line}); err != nil {
			return
		}
	}
	if players, ok := s.hub.LatestPlayers(serverID); ok {
		if err := sendJSON(map[string]any{"type": "players", "players": players}); err != nil {
			return
		}
	}

	// Reader: inbound text is console input. If the node channel is briefly
	// offline (panel restart), retry delivery instead of dropping input.
	inputDone := make(chan struct{})
	go func() {
		defer close(inputDone)
		for {
			_, msg, err := conn.ReadMessage()
			if err != nil {
				return
			}
			input := string(msg)
			if len(input) > 1000 {
				continue
			}
			delivered := false
			var lastErr error
			for i := 0; i < 20; i++ {
				ctx, cancelInput := context.WithTimeout(context.Background(), 10*time.Second)
				_, sendErr := s.channels.Send(ctx, nodeID, &nodesv1.Command{
					Action: &nodesv1.Command_SendCommand{SendCommand: &nodesv1.SendCommand{
						ServerId: serverID.String(), Input: input,
					}},
				})
				cancelInput()
				if sendErr == nil {
					delivered = true
					break
				}
				lastErr = sendErr
				if !errors.Is(sendErr, nodes.ErrNodeOffline) {
					break
				}
				time.Sleep(500 * time.Millisecond)
			}
			if !delivered {
				_ = sendJSON(map[string]any{
					"type":    "error",
					"message": "command not delivered: " + lastErr.Error(),
				})
			}
		}
	}()

	ticker := time.NewTicker(20 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case ev := <-events:
			var frame map[string]any
			switch ev.Type {
			case hub.EventConsole:
				frame = map[string]any{"type": "console", "line": ev.Console.GetLine()}
			case hub.EventStatus:
				frame = map[string]any{"type": "status", "status": ev.Status, "detail": ev.Detail}
			case hub.EventStats:
				snap, _ := s.hub.LatestStats(serverID)
				frame = map[string]any{"type": "stats", "stats": snap}
			case hub.EventInstall:
				frame = map[string]any{"type": "install", "step": ev.Install.GetStep(),
					"percent": ev.Install.GetPercent(), "detail": ev.Install.GetDetail()}
			case hub.EventPlayers:
				frame = map[string]any{"type": "players", "players": ev.Players}
			default:
				continue
			}
			if err := sendJSON(frame); err != nil {
				return
			}
		case <-ticker.C:
			writeMu.Lock()
			_ = conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			err := conn.WriteMessage(websocket.PingMessage, nil)
			writeMu.Unlock()
			if err != nil {
				return
			}
		case <-inputDone:
			return
		}
	}
}

// latestStats returns the newest stats snapshot (polling fallback).
func (s *Server) latestStats(w http.ResponseWriter, r *http.Request) {
	sv, ok := s.loadServer(w, r)
	if !ok {
		return
	}
	snap, ok := s.hub.LatestStats(sv.ID)
	if !ok {
		writeJSON(w, http.StatusOK, map[string]any{"stats": nil})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"stats": snap})
}

// ---------------------------------------------------------------------------
// File manager
// ---------------------------------------------------------------------------

func (s *Server) listServerFiles(w http.ResponseWriter, r *http.Request) {
	sv, ok := s.loadServer(w, r)
	if !ok {
		return
	}
	if !rbacHas(Identity(r), "file.read") {
		apiError(w, http.StatusForbidden, "file.read required")
		return
	}
	path := r.URL.Query().Get("path")
	result, err := s.sendServerCommand(r.Context(), sv, &nodesv1.Command{
		Action: &nodesv1.Command_ListFiles{ListFiles: &nodesv1.ListFiles{
			ServerId: sv.ID.String(), Path: path,
		}},
	})
	if err != nil {
		apiError(w, http.StatusBadGateway, "node error: "+err.Error())
		return
	}
	if !result.GetOk() {
		apiError(w, http.StatusInternalServerError, result.GetError())
		return
	}
	writeRawJSON(w, result.GetBody())
}

func (s *Server) readServerFile(w http.ResponseWriter, r *http.Request) {
	sv, ok := s.loadServer(w, r)
	if !ok {
		return
	}
	if !rbacHas(Identity(r), "file.read") {
		apiError(w, http.StatusForbidden, "file.read required")
		return
	}
	path := r.URL.Query().Get("path")
	if path == "" {
		apiError(w, http.StatusBadRequest, "path is required")
		return
	}
	result, err := s.sendServerCommand(r.Context(), sv, &nodesv1.Command{
		Action: &nodesv1.Command_ReadFile{ReadFile: &nodesv1.ReadFile{
			ServerId: sv.ID.String(), Path: path, MaxBytes: 1 << 20,
		}},
	})
	if err != nil {
		apiError(w, http.StatusBadGateway, "node error: "+err.Error())
		return
	}
	if !result.GetOk() {
		apiError(w, http.StatusInternalServerError, result.GetError())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"path":    path,
		"content": string(result.GetBody()),
	})
}

type writeFileRequest struct {
	Path       string `json:"path"`
	Content    string `json:"content"`
	CreateDirs bool   `json:"create_dirs"`
}

func (s *Server) writeServerFile(w http.ResponseWriter, r *http.Request) {
	sv, ok := s.loadServer(w, r)
	if !ok {
		return
	}
	if !rbacHas(Identity(r), "file.write") {
		apiError(w, http.StatusForbidden, "file.write required")
		return
	}
	req, ok := decode[writeFileRequest](w, r)
	if !ok {
		return
	}
	if req.Path == "" {
		apiError(w, http.StatusBadRequest, "path is required")
		return
	}
	result, err := s.sendServerCommand(r.Context(), sv, &nodesv1.Command{
		Action: &nodesv1.Command_WriteFile{WriteFile: &nodesv1.WriteFile{
			ServerId: sv.ID.String(), Path: req.Path,
			Content: []byte(req.Content), CreateDirs: req.CreateDirs,
		}},
	})
	if err != nil {
		apiError(w, http.StatusBadGateway, "node error: "+err.Error())
		return
	}
	if !result.GetOk() {
		apiError(w, http.StatusInternalServerError, result.GetError())
		return
	}
	s.auditLog.Record(r.Context(), audit.Entry{
		Actor: audit.ActorUser, Action: "file.written",
		UserID: actorID(r), ResourceType: "server", ResourceID: sv.ID.String(),
		IP: clientIP(r), Metadata: map[string]any{"path": req.Path},
	})
	writeJSON(w, http.StatusOK, map[string]string{"status": "written"})
}

func (s *Server) deleteServerFile(w http.ResponseWriter, r *http.Request) {
	sv, ok := s.loadServer(w, r)
	if !ok {
		return
	}
	if !rbacHas(Identity(r), "file.write") {
		apiError(w, http.StatusForbidden, "file.write required")
		return
	}
	path := r.URL.Query().Get("path")
	if path == "" {
		apiError(w, http.StatusBadRequest, "path is required")
		return
	}
	recursive := r.URL.Query().Get("recursive") == "true"
	result, err := s.sendServerCommand(r.Context(), sv, &nodesv1.Command{
		Action: &nodesv1.Command_DeleteFile{DeleteFile: &nodesv1.DeleteFile{
			ServerId: sv.ID.String(), Path: path, Recursive: recursive,
		}},
	})
	if err != nil {
		apiError(w, http.StatusBadGateway, "node error: "+err.Error())
		return
	}
	if !result.GetOk() {
		apiError(w, http.StatusInternalServerError, result.GetError())
		return
	}
	s.auditLog.Record(r.Context(), audit.Entry{
		Actor: audit.ActorUser, Action: "file.deleted",
		UserID: actorID(r), ResourceType: "server", ResourceID: sv.ID.String(),
		IP: clientIP(r), Metadata: map[string]any{"path": path, "recursive": recursive},
	})
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// sendServerCommand dispatches a command to the server's node.
func (s *Server) sendServerCommand(ctx context.Context, sv gen.Server, cmd *nodesv1.Command) (*nodesv1.CommandResult, error) {
	return s.channels.Send(ctx, sv.NodeID, cmd)
}

// ---------------------------------------------------------------------------
// Runner catalog + allocations
// ---------------------------------------------------------------------------

func (s *Server) listRunners(w http.ResponseWriter, r *http.Request) {
	type runnerInfo struct {
		ID       string `json:"id"`
		Versions []struct {
			MCVersion string `json:"mc_version"`
			Stable    bool   `json:"stable"`
			Java      int32  `json:"java_version"`
		} `json:"versions"`
	}
	out := make([]runnerInfo, 0)
	for _, id := range s.catalog.IDs() {
		info := runnerInfo{ID: id}
		versions, err := s.catalog.Versions(r.Context(), id)
		if err != nil {
			s.log.Warn("version catalog unavailable", "runner", id, "err", err)
		}
		for _, v := range versions {
			info.Versions = append(info.Versions, struct {
				MCVersion string `json:"mc_version"`
				Stable    bool   `json:"stable"`
				Java      int32  `json:"java_version"`
			}{v.McVersion, v.Stable, v.JavaVersion})
		}
		out = append(out, info)
	}
	writeJSON(w, http.StatusOK, out)
}

type createAllocationRequest struct {
	IP       string `json:"ip"`
	PortFrom int32  `json:"port_from"`
	PortTo   int32  `json:"port_to"`
}

func (s *Server) createAllocations(w http.ResponseWriter, r *http.Request) {
	nodeID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		apiError(w, http.StatusBadRequest, "invalid node id")
		return
	}
	if _, err := s.q.GetNode(r.Context(), nodeID); err != nil {
		apiError(w, http.StatusNotFound, "node not found")
		return
	}
	req, ok := decode[createAllocationRequest](w, r)
	if !ok {
		return
	}
	if req.IP == "" || req.PortFrom <= 0 || req.PortTo < req.PortFrom || req.PortTo > 65535 || req.PortTo-req.PortFrom > 256 {
		apiError(w, http.StatusBadRequest, "allocation range invalid (max 256 ports)")
		return
	}
	created := 0
	for port := req.PortFrom; port <= req.PortTo; port++ {
		if _, err := s.q.CreateAllocation(r.Context(), gen.CreateAllocationParams{
			ID: uuid.New(), NodeID: nodeID, Ip: req.IP, Port: port,
		}); err == nil {
			created++
		}
	}
	s.auditLog.Record(r.Context(), audit.Entry{
		Actor: audit.ActorUser, Action: "node.allocations_added",
		UserID: actorID(r), ResourceType: "node", ResourceID: nodeID.String(),
		IP: clientIP(r), Metadata: map[string]any{"ip": req.IP, "created": created},
	})
	writeJSON(w, http.StatusCreated, map[string]int{"created": created})
}

func (s *Server) listAllocations(w http.ResponseWriter, r *http.Request) {
	nodeID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		apiError(w, http.StatusBadRequest, "invalid node id")
		return
	}
	rows, err := s.q.ListAllocationsByNode(r.Context(), nodeID)
	if err != nil {
		apiError(w, http.StatusInternalServerError, "database error")
		return
	}
	out := make([]map[string]any, 0, len(rows))
	for _, a := range rows {
		entry := map[string]any{"id": a.ID, "ip": a.Ip, "port": a.Port, "created_at": a.CreatedAt}
		if a.ServerID != nil {
			entry["server_id"] = *a.ServerID
		}
		out = append(out, entry)
	}
	writeJSON(w, http.StatusOK, out)
}

// writeRawJSON writes pre-encoded JSON.
func writeRawJSON(w http.ResponseWriter, raw []byte) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(raw)
}
