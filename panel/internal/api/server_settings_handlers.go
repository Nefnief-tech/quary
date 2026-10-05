package api

import (
	"errors"
	"net/http"

	"github.com/google/uuid"

	"quarry.dev/quarry/panel/internal/audit"
	"quarry.dev/quarry/panel/internal/servers"
)

type updateServerRequest struct {
	Name       string `json:"name"`
	MemoryMB   int32  `json:"memory_mb"`
	CPUPercent int32  `json:"cpu_percent"`
	// AllocationID moves the server to a different free allocation.
	AllocationID string `json:"allocation_id"`
}

// updateServer handles the Settings tab: rename, resource limits, port move.
func (s *Server) updateServer(w http.ResponseWriter, r *http.Request) {
	sv, ok := s.loadServer(w, r)
	if !ok {
		return
	}
	req, ok := decode[updateServerRequest](w, r)
	if !ok {
		return
	}

	req2 := servers.UpdateRequest{Name: req.Name, MemoryMB: req.MemoryMB, CPUPercent: req.CPUPercent}
	if req.AllocationID != "" {
		allocID, err := uuid.Parse(req.AllocationID)
		if err != nil {
			apiError(w, http.StatusBadRequest, "invalid allocation_id")
			return
		}
		req2.Allocation = &allocID
	}

	updated, notes, err := s.serverSvc.Update(r.Context(), sv, req2)
	if err != nil {
		switch {
		case errors.Is(err, servers.ErrBadState):
			apiError(w, http.StatusConflict, err.Error())
		case errors.Is(err, servers.ErrNoAllocations):
			apiError(w, http.StatusConflict, err.Error())
		default:
			apiError(w, http.StatusBadRequest, err.Error())
		}
		return
	}

	s.auditLog.Record(r.Context(), audit.Entry{
		Actor: audit.ActorUser, Action: "server.updated",
		UserID: actorID(r), ResourceType: "server", ResourceID: sv.ID.String(),
		IP: clientIP(r), Metadata: map[string]any{"changes": notes},
	})
	resp := struct {
		serverResponse
		Notes []string `json:"notes"`
	}{serverResponse: s.serverResponse(r.Context(), updated), Notes: notes}
	writeJSON(w, http.StatusOK, resp)
}

// serverAllocations lists every allocation on the server's node with its
// assignment state, so the Settings tab can offer a port move.
func (s *Server) serverAllocations(w http.ResponseWriter, r *http.Request) {
	sv, ok := s.loadServer(w, r)
	if !ok {
		return
	}
	rows, err := s.q.ListAllocationsByNode(r.Context(), sv.NodeID)
	if err != nil {
		apiError(w, http.StatusInternalServerError, "database error")
		return
	}
	type allocRow struct {
		ID        uuid.UUID  `json:"id"`
		IP        string     `json:"ip"`
		Port      int32      `json:"port"`
		InUseBy   *uuid.UUID `json:"in_use_by"`
		IsCurrent bool       `json:"is_current"`
	}
	out := make([]allocRow, 0, len(rows))
	for _, a := range rows {
		out = append(out, allocRow{
			ID: a.ID, IP: a.Ip, Port: a.Port,
			InUseBy:   a.ServerID,
			IsCurrent: a.ID == sv.AllocationID,
		})
	}
	writeJSON(w, http.StatusOK, out)
}
