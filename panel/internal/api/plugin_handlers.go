package api

import (
	"context"
	"errors"
	"net/http"
	"path"
	"strconv"
	"strings"
	"time"

	"quarry.dev/quarry/panel/internal/audit"
	"quarry.dev/quarry/panel/internal/db/gen"
	"quarry.dev/quarry/panel/internal/nodes"
	nodesv1 "quarry.dev/quarry/proto/gen/go/quarry/node/v1"
)

// modrinthLoader maps a runner ID to its Modrinth loader facet and the
// content directory inside the server data dir.
func modrinthLoader(runnerID string) (loader, contentDir string, ok bool) {
	switch runnerID {
	case "paper":
		return "paper", "plugins", true
	case "fabric":
		return "fabric", "mods", true
	default:
		return "", "", false
	}
}

// loadPluginServer loads the server, checks ownership, and verifies the
// runner has a plugin marketplace. Returns the row, its loader, and content
// directory.
func (s *Server) loadPluginServer(w http.ResponseWriter, r *http.Request) (gen.Server, string, string, bool) {
	sv, ok := s.loadServer(w, r)
	if !ok {
		return gen.Server{}, "", "", false
	}
	if _, known := s.catalog.Get(sv.Runner); !known {
		apiError(w, http.StatusBadRequest, "unknown runner")
		return gen.Server{}, "", "", false
	}
	loader, contentDir, supported := modrinthLoader(sv.Runner)
	if !supported {
		apiError(w, http.StatusBadRequest, "this runner has no plugin marketplace")
		return gen.Server{}, "", "", false
	}
	return sv, loader, contentDir, true
}

func (s *Server) searchPlugins(w http.ResponseWriter, r *http.Request) {
	sv, loader, _, ok := s.loadPluginServer(w, r)
	if !ok {
		return
	}
	query := queryValue(r, "q")
	offset := 0
	if v := queryValue(r, "offset"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			apiError(w, http.StatusBadRequest, "invalid offset")
			return
		}
		offset = n
	}
	// Bound the free-text query before it becomes an upstream request.
	query = strings.TrimSpace(query)
	if len(query) > 100 {
		query = query[:100]
	}
	hits, total, err := s.modrinth.Search(r.Context(), query, sv.McVersion, loader, 20, offset)
	if err != nil {
		apiError(w, http.StatusBadGateway, "marketplace unavailable")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"hits": hits, "total": total})
}

type installPluginRequest struct {
	ProjectID string `json:"project_id"`
}

// installPlugin resolves the newest compatible version and downloads it to
// the server's content directory via the node.
func (s *Server) installPlugin(w http.ResponseWriter, r *http.Request) {
	sv, loader, contentDir, ok := s.loadPluginServer(w, r)
	if !ok {
		return
	}
	if !s.channels.Connected(sv.NodeID) {
		apiError(w, http.StatusConflict, "node is offline")
		return
	}
	req, ok := decode[installPluginRequest](w, r)
	if !ok {
		return
	}

	dl, err := s.modrinth.Resolve(r.Context(), req.ProjectID, sv.McVersion, loader)
	if err != nil {
		apiError(w, http.StatusConflict, err.Error())
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	result, err := s.channels.Send(ctx, sv.NodeID, &nodesv1.Command{
		Action: &nodesv1.Command_InstallPlugin{InstallPlugin: &nodesv1.InstallPlugin{
			ServerId: sv.ID.String(),
			Url:      dl.URL,
			Sha512:   dl.SHA512,
			Filename: path.Join(contentDir, dl.Filename),
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

	s.auditLog.Record(r.Context(), audit.Entry{
		Actor: audit.ActorUser, Action: "plugin.installed",
		UserID: actorID(r), ResourceType: "server", ResourceID: sv.ID.String(),
		IP: clientIP(r), Metadata: map[string]any{
			"project": req.ProjectID, "file": dl.Filename, "version": dl.Version,
		},
	})
	writeJSON(w, http.StatusOK, map[string]any{
		"status":   "installed",
		"filename": dl.Filename,
		"version":  dl.Version,
		"hint":     "restart the server to load it",
	})
}

func (s *Server) listServerPlugins(w http.ResponseWriter, r *http.Request) {
	sv, _, contentDir, ok := s.loadPluginServer(w, r)
	if !ok {
		return
	}
	result, err := s.channels.Send(r.Context(), sv.NodeID, &nodesv1.Command{
		Action: &nodesv1.Command_ListFiles{ListFiles: &nodesv1.ListFiles{
			ServerId: sv.ID.String(), Path: contentDir,
		}},
	})
	if err != nil {
		apiError(w, http.StatusBadGateway, "node error: "+err.Error())
		return
	}
	if !result.GetOk() {
		// No content directory yet means zero installed plugins.
		writeJSON(w, http.StatusOK, []any{})
		return
	}
	writeRawJSON(w, result.GetBody())
}

func (s *Server) deleteServerPlugin(w http.ResponseWriter, r *http.Request) {
	sv, _, contentDir, ok := s.loadPluginServer(w, r)
	if !ok {
		return
	}
	name := path.Base(queryValue(r, "name"))
	if name == "" || name == "." || name == "/" {
		apiError(w, http.StatusBadRequest, "name is required")
		return
	}
	result, err := s.channels.Send(r.Context(), sv.NodeID, &nodesv1.Command{
		Action: &nodesv1.Command_DeleteFile{DeleteFile: &nodesv1.DeleteFile{
			ServerId: sv.ID.String(), Path: path.Join(contentDir, name),
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
		Actor: audit.ActorUser, Action: "plugin.removed",
		UserID: actorID(r), ResourceType: "server", ResourceID: sv.ID.String(),
		IP: clientIP(r), Metadata: map[string]any{"name": name},
	})
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}
