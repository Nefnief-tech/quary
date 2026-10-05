package api

import (
	"context"
	"net/http"
	"time"

	"github.com/google/uuid"

	"quarry.dev/quarry/panel/internal/audit"
	"quarry.dev/quarry/panel/internal/db/gen"
)

// nodeIsOnlineAfter is the heartbeat staleness window. Daemons beat every
// 10s; three missed beats mean offline.
const nodeIsOnlineAfter = 35 * time.Second

type nodeResponse struct {
	ID             uuid.UUID  `json:"id"`
	Name           string     `json:"name"`
	Description    string     `json:"description"`
	Status         string     `json:"status"` // online | offline | never
	DaemonVersion  string     `json:"daemon_version"`
	Hostname       string     `json:"hostname"`
	DockerVersion  string     `json:"docker_version"`
	OS             string     `json:"os"`
	Arch           string     `json:"arch"`
	LastSeenAt     *time.Time `json:"last_seen_at"`
	CreatedAt      time.Time  `json:"created_at"`
	HasActiveToken bool       `json:"has_active_token"`
}

func nodeStatus(lastSeen *time.Time) string {
	if lastSeen == nil {
		return "never"
	}
	if time.Since(*lastSeen) <= nodeIsOnlineAfter {
		return "online"
	}
	return "offline"
}

func (s *Server) nodeResponse(ctx context.Context, n gen.Node) nodeResponse {
	resp := nodeResponse{
		ID: n.ID, Name: n.Name, Description: n.Description,
		Status:        nodeStatus(n.LastSeenAt),
		DaemonVersion: n.DaemonVersion, Hostname: n.Hostname,
		DockerVersion: n.DockerVersion, OS: n.Os, Arch: n.Arch,
		LastSeenAt: n.LastSeenAt, CreatedAt: n.CreatedAt,
	}
	if exists, err := s.q.NodeTokenExists(ctx, n.ID); err == nil && exists > 0 {
		resp.HasActiveToken = true
	}
	return resp
}

type createNodeRequest struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

func (s *Server) createNode(w http.ResponseWriter, r *http.Request) {
	req, ok := decode[createNodeRequest](w, r)
	if !ok {
		return
	}
	if len(req.Name) < 3 || len(req.Name) > 64 {
		apiError(w, http.StatusBadRequest, "node name must be 3-64 characters")
		return
	}

	ctx := r.Context()
	node, err := s.q.CreateNode(ctx, gen.CreateNodeParams{
		ID: uuid.New(), Name: req.Name, Description: req.Description,
	})
	if err != nil {
		apiError(w, http.StatusConflict, "node name already taken")
		return
	}

	token, err := s.issueNodeToken(ctx, node.ID)
	if err != nil {
		apiError(w, http.StatusInternalServerError, "token generation failed")
		return
	}

	s.auditLog.Record(ctx, audit.Entry{
		Actor: audit.ActorUser, Action: "node.created",
		UserID: actorID(r), ResourceType: "node", ResourceID: node.ID.String(),
		IP: clientIP(r), Metadata: map[string]any{"name": node.Name},
	})

	resp := s.nodeResponse(ctx, node)
	resp.HasActiveToken = true
	out := struct {
		nodeResponse
		// EnrollmentToken is returned exactly once, here.
		EnrollmentToken string `json:"enrollment_token"`
	}{nodeResponse: resp, EnrollmentToken: token}
	writeJSON(w, http.StatusCreated, out)
}

// issueNodeToken delegates to the nodes service (single source of truth for
// token issuance).
func (s *Server) issueNodeToken(ctx context.Context, nodeID uuid.UUID) (string, error) {
	return s.nodeSvc.IssueToken(ctx, nodeID)
}

func (s *Server) listNodes(w http.ResponseWriter, r *http.Request) {
	nodes, err := s.q.ListNodes(r.Context())
	if err != nil {
		apiError(w, http.StatusInternalServerError, "database error")
		return
	}
	out := make([]nodeResponse, 0, len(nodes))
	for _, n := range nodes {
		out = append(out, s.nodeResponse(r.Context(), n))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) getNode(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		apiError(w, http.StatusBadRequest, "invalid node id")
		return
	}
	node, err := s.q.GetNode(r.Context(), id)
	if err != nil {
		apiError(w, http.StatusNotFound, "node not found")
		return
	}
	writeJSON(w, http.StatusOK, s.nodeResponse(r.Context(), node))
}

func (s *Server) deleteNode(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		apiError(w, http.StatusBadRequest, "invalid node id")
		return
	}
	node, err := s.q.GetNode(r.Context(), id)
	if err != nil {
		apiError(w, http.StatusNotFound, "node not found")
		return
	}
	if err := s.q.DeleteNode(r.Context(), id); err != nil {
		apiError(w, http.StatusInternalServerError, "delete failed")
		return
	}
	s.auditLog.Record(r.Context(), audit.Entry{
		Actor: audit.ActorUser, Action: "node.deleted",
		UserID: actorID(r), ResourceType: "node", ResourceID: id.String(),
		IP: clientIP(r), Metadata: map[string]any{"name": node.Name},
	})
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// rotateNodeToken revokes outstanding tokens and issues a fresh one.
func (s *Server) rotateNodeToken(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		apiError(w, http.StatusBadRequest, "invalid node id")
		return
	}
	ctx := r.Context()
	if _, err := s.q.GetNode(ctx, id); err != nil {
		apiError(w, http.StatusNotFound, "node not found")
		return
	}
	if err := s.q.RevokeNodeTokens(ctx, id); err != nil {
		apiError(w, http.StatusInternalServerError, "revocation failed")
		return
	}
	token, err := s.issueNodeToken(ctx, id)
	if err != nil {
		apiError(w, http.StatusInternalServerError, "token generation failed")
		return
	}
	s.auditLog.Record(ctx, audit.Entry{
		Actor: audit.ActorUser, Action: "node.token_rotated",
		UserID: actorID(r), ResourceType: "node", ResourceID: id.String(), IP: clientIP(r),
	})
	writeJSON(w, http.StatusCreated, map[string]string{"enrollment_token": token})
}
