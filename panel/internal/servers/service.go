// Package servers orchestrates the panel side of server management: the
// create flow (resolve artifact, pick allocation, dispatch install), power
// actions over the node channel, and the daemon event handling that keeps
// server statuses truthful.
package servers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	nodesv1 "quarry.dev/quarry/proto/gen/go/quarry/node/v1"
	"quarry.dev/quarry/panel/internal/db/gen"
	"quarry.dev/quarry/panel/internal/hub"
	"quarry.dev/quarry/panel/internal/lifecycle"
	"quarry.dev/quarry/panel/internal/nodes"
	"quarry.dev/quarry/panel/internal/runners"
)

const defaultStopGraceSeconds = 30

var (
	ErrNodeOffline   = nodes.ErrNodeOffline
	ErrNotPermitted  = errors.New("not permitted")
	ErrBadState      = errors.New("server is not in a state that allows this action")
	ErrNoAllocations = errors.New("node has no free allocation; add an allocation range first")
)

// Config is the runtime install snapshot persisted in servers.config.
type Config struct {
	Image  string   `json:"image"`
	Launch []string `json:"launch"`
	BindIP string   `json:"bind_ip"`
	BindPort int32  `json:"bind_port"`
	Artifact struct {
		URL    string `json:"url"`
		SHA256 string `json:"sha256,omitempty"`
		SHA1   string `json:"sha1,omitempty"`
	} `json:"artifact"`
}

type Service struct {
	pool     *pgxpool.Pool
	q        *gen.Queries
	hub      *hub.Hub
	channels *nodes.ChannelManager
	catalog  *runners.Catalog
	log      *slog.Logger

	mu              sync.Mutex
	pendingRestarts map[uuid.UUID]bool
}

func NewService(pool *pgxpool.Pool, q *gen.Queries, hb *hub.Hub, channels *nodes.ChannelManager, catalog *runners.Catalog, log *slog.Logger) *Service {
	return &Service{
		pool:            pool,
		q:               q,
		hub:             hb,
		channels:        channels,
		catalog:         catalog,
		log:             log,
		pendingRestarts: map[uuid.UUID]bool{},
	}
}

// CreateRequest is the create-server input.
type CreateRequest struct {
	Name      string    `json:"name"`
	NodeID    uuid.UUID `json:"node_id"`
	Runner    string    `json:"runner"`
	MCVersion string    `json:"mc_version"`
	MemoryMB  int32     `json:"memory_mb"`
}

// Create resolves the artifact, claims a free allocation, persists the
// server (status installing) and dispatches the install to the node.
func (s *Service) Create(ctx context.Context, ownerID uuid.UUID, req CreateRequest) (gen.Server, error) {
	if req.Name == "" || len(req.Name) > 64 {
		return gen.Server{}, fmt.Errorf("name must be 1-64 characters")
	}
	if req.MemoryMB == 0 {
		req.MemoryMB = 2048
	}

	runner, artifact, err := s.catalog.Resolve(ctx, req.Runner, req.MCVersion)
	if err != nil {
		return gen.Server{}, err
	}
	java := runner.JavaVersion(req.MCVersion)

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return gen.Server{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	qtx := s.q.WithTx(tx)

	alloc, err := qtx.PickFreeAllocation(ctx, req.NodeID)
	if err != nil {
		return gen.Server{}, ErrNoAllocations
	}

	serverID := uuid.New()
	cfg := Config{
		Image:    runners.ImageForJava(java),
		Launch:   runner.Launch(int(req.MemoryMB)),
		BindIP:   alloc.Ip,
		BindPort: alloc.Port,
	}
	cfg.Artifact.URL = artifact.URL
	cfg.Artifact.SHA256 = artifact.SHA256
	cfg.Artifact.SHA1 = artifact.SHA1
	cfgRaw, _ := json.Marshal(cfg)

	server, err := qtx.CreateServer(ctx, gen.CreateServerParams{
		ID:           serverID,
		Name:         req.Name,
		OwnerID:      ownerID,
		NodeID:       req.NodeID,
		AllocationID: alloc.ID,
		Runner:       req.Runner,
		McVersion:    req.MCVersion,
		Status:       string(lifecycle.Installing),
		MemoryMb:     req.MemoryMB,
		DiskMb:       10240,
		CpuPercent:   200,
		Config:       cfgRaw,
	})
	if err != nil {
		return gen.Server{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return gen.Server{}, err
	}

	// Install runs asynchronously on the node; progress streams as events.
	go func() {
		bg, cancel := context.WithTimeout(context.Background(), 45*time.Minute)
		defer cancel()
		result, err := s.channels.Send(bg, req.NodeID, &nodesv1.Command{
			Action: &nodesv1.Command_InstallServer{InstallServer: &nodesv1.InstallServer{
				ServerId:       serverID.String(),
				Runner:         req.Runner,
				McVersion:      req.MCVersion,
				ArtifactUrl:    artifact.URL,
				ArtifactSha256: artifact.SHA256,
				ArtifactName:   artifact.Name,
				Image:          cfg.Image,
				Launch:         cfg.Launch,
				MemoryMb:       req.MemoryMB,
				BindIp:         cfg.BindIP,
				BindPort:       cfg.BindPort,
			}},
		})
		if err != nil || !result.GetOk() {
			detail := "install failed"
			if err != nil {
				detail = err.Error()
			} else {
				detail = result.GetError()
			}
			s.log.Error("server install failed", "server_id", serverID, "err", detail)
			s.hub.Publish(hub.Event{Type: hub.EventStatus, ServerID: serverID, Status: "crashed", Detail: detail})
		}
	}()

	return server, nil
}

// Power performs a validated power action. Restart is a composite: stop,
// then start once the daemon reports offline.
func (s *Service) Power(ctx context.Context, server gen.Server, action lifecycle.PowerAction) error {
	current := lifecycle.Status(server.Status)
	if !lifecycle.ActionAllowed(action, current) {
		return fmt.Errorf("%w: cannot %s while %s", ErrBadState, action, current)
	}

	nodeID := server.NodeID
	if !s.channels.Connected(nodeID) {
		return ErrNodeOffline
	}

	switch action {
	case lifecycle.ActionStart:
		if err := s.setStatus(server.ID, current, lifecycle.Starting); err != nil {
			return err
		}
		_, err := s.channels.Send(ctx, nodeID, s.startCommand(server))
		return err
	case lifecycle.ActionStop:
		if err := s.setStatus(server.ID, current, lifecycle.Stopping); err != nil {
			return err
		}
		_, err := s.channels.Send(ctx, nodeID, &nodesv1.Command{
			Action: &nodesv1.Command_StopServer{StopServer: &nodesv1.StopServer{
				ServerId: server.ID.String(), TimeoutSeconds: defaultStopGraceSeconds,
			}},
		})
		return err
	case lifecycle.ActionKill:
		if err := s.setStatus(server.ID, current, lifecycle.Offline); err != nil {
			return err
		}
		_, err := s.channels.Send(ctx, nodeID, &nodesv1.Command{
			Action: &nodesv1.Command_KillServer{KillServer: &nodesv1.KillServer{ServerId: server.ID.String()}},
		})
		return err
	case lifecycle.ActionRestart:
		s.mu.Lock()
		s.pendingRestarts[server.ID] = true
		s.mu.Unlock()
		if err := s.setStatus(server.ID, current, lifecycle.Stopping); err != nil {
			return err
		}
		_, err := s.channels.Send(ctx, nodeID, &nodesv1.Command{
			Action: &nodesv1.Command_StopServer{StopServer: &nodesv1.StopServer{
				ServerId: server.ID.String(), TimeoutSeconds: defaultStopGraceSeconds,
			}},
		})
		return err
	}
	return fmt.Errorf("unknown action")
}

func (s *Service) startCommand(server gen.Server) *nodesv1.Command {
	var cfg Config
	_ = json.Unmarshal(server.Config, &cfg)
	return &nodesv1.Command{
		Action: &nodesv1.Command_StartServer{StartServer: &nodesv1.StartServer{
			ServerId:   server.ID.String(),
			Image:      cfg.Image,
			Launch:     cfg.Launch,
			MemoryMb:   server.MemoryMb,
			CpuPercent: server.CpuPercent,
			BindIp:     cfg.BindIP,
			BindPort:   cfg.BindPort,
		}},
	}
}

func (s *Service) setStatus(serverID uuid.UUID, from, to lifecycle.Status) error {
	if err := lifecycle.Transition(from, to); err != nil {
		return err
	}
	return s.q.SetServerStatus(context.Background(), gen.SetServerStatusParams{
		ID: serverID, Status: string(to),
	})
}

// HandleEvent consumes daemon events: status transitions are validated and
// persisted; console/stats/install events fan out to the hub.
func (s *Service) HandleEvent(ev *nodesv1.Event) {
	serverID, err := uuid.Parse(ev.GetServerId())
	if err != nil {
		return
	}
	switch payload := ev.GetPayload().(type) {
	case *nodesv1.Event_Console:
		s.hub.Publish(hub.Event{Type: hub.EventConsole, ServerID: serverID, Console: payload.Console})
	case *nodesv1.Event_Stats:
		s.hub.Publish(hub.Event{Type: hub.EventStats, ServerID: serverID, Stats: payload.Stats})
	case *nodesv1.Event_InstallProgress:
		s.hub.Publish(hub.Event{Type: hub.EventInstall, ServerID: serverID, Install: payload.InstallProgress})
	case *nodesv1.Event_Status:
		s.handleStatus(serverID, payload.Status)
	}
}

func (s *Service) handleStatus(serverID uuid.UUID, change *nodesv1.StatusChange) {
	to := lifecycle.Status(change.GetStatus())
	if !lifecycle.Valid(string(to)) {
		return
	}
	server, err := s.q.GetServer(context.Background(), serverID)
	if err != nil {
		return
	}
	from := lifecycle.Status(server.Status)
	if from == to {
		return
	}
	if err := lifecycle.Transition(from, to); err != nil {
		// Daemon observations must obey the same graph; log and accept the
		// daemon's truth when it reports a state the graph allows from
		// anywhere (crashed/offline are reachable recovery states).
		s.log.Warn("ignoring out-of-graph status", "server_id", serverID, "from", from, "to", to, "detail", change.GetDetail())
		if to != lifecycle.Crashed && to != lifecycle.Offline {
			return
		}
	}
	if err := s.q.SetServerStatus(context.Background(), gen.SetServerStatusParams{
		ID: serverID, Status: string(to),
	}); err != nil {
		s.log.Error("persist status failed", "server_id", serverID, "err", err)
	}
	s.hub.Publish(hub.Event{Type: hub.EventStatus, ServerID: serverID, Status: string(to), Detail: change.GetDetail()})

	// Composite restarts kick off once the stop settles.
	if to == lifecycle.Offline {
		s.mu.Lock()
		restart := s.pendingRestarts[serverID]
		delete(s.pendingRestarts, serverID)
		s.mu.Unlock()
		if restart {
			if err := s.q.SetServerStatus(context.Background(), gen.SetServerStatusParams{
				ID: serverID, Status: string(lifecycle.Starting),
			}); err == nil {
				if _, err := s.channels.Send(context.Background(), server.NodeID, s.startCommand(server)); err != nil {
					s.log.Error("restart dispatch failed", "server_id", serverID, "err", err)
					_ = s.q.SetServerStatus(context.Background(), gen.SetServerStatusParams{
						ID: serverID, Status: string(lifecycle.Crashed),
					})
				}
			}
		}
	}
}

// Delete removes an offline server and wipes its data directory on the node.
func (s *Service) Delete(ctx context.Context, server gen.Server) error {
	status := lifecycle.Status(server.Status)
	if status != lifecycle.Offline && status != lifecycle.Crashed {
		return fmt.Errorf("%w: stop the server before deleting", ErrBadState)
	}
	// Best-effort data wipe; the row goes regardless.
	if s.channels.Connected(server.NodeID) {
		sendCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
		_, _ = s.channels.Send(sendCtx, server.NodeID, &nodesv1.Command{
			Action: &nodesv1.Command_DeleteFile{DeleteFile: &nodesv1.DeleteFile{
				ServerId: server.ID.String(), Path: ".", Recursive: true,
			}},
		})
		cancel()
	}
	return s.q.DeleteServer(ctx, server.ID)
}
