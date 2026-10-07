// Panel-side handshake-router service: builds the per-node routing table
// from the domain mappings and pushes RouterConfig to daemons whenever it
// changes (and after every daemon reconnect).
package router

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"

	"quarry.dev/quarry/panel/internal/db/gen"
	"quarry.dev/quarry/panel/internal/nodes"
	nodesv1 "quarry.dev/quarry/proto/gen/go/quarry/node/v1"
)

const settingsKey = "router"

// Config is the admin-controlled router configuration.
type Config struct {
	Enabled         bool   `json:"enabled"`
	Port            int32  `json:"port"`
	DefaultServerID string `json:"default_server_id"` // empty = refuse unmatched
}

// Service owns router settings and pushes config to nodes.
type Service struct {
	q        *gen.Queries
	channels *nodes.ChannelManager
	log      *slog.Logger

	mu   sync.Mutex
	last map[uuid.UUID][]byte // last pushed config (serialized), to skip redundant pushes
}

func NewService(q *gen.Queries, channels *nodes.ChannelManager, log *slog.Logger) *Service {
	return &Service{q: q, channels: channels, log: log, last: map[uuid.UUID][]byte{}}
}

// Get returns the router configuration (defaults when unset).
func (s *Service) Get(ctx context.Context) (Config, error) {
	raw, err := s.q.GetSetting(ctx, settingsKey)
	if err != nil {
		return Config{Port: 25565}, nil
	}
	var cfg Config
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return Config{Port: 25565}, nil
	}
	if cfg.Port == 0 {
		cfg.Port = 25565
	}
	return cfg, nil
}

// Set stores the configuration and pushes the new table to every node.
func (s *Service) Set(ctx context.Context, cfg Config) error {
	if cfg.Port == 0 {
		cfg.Port = 25565
	}
	raw, _ := json.Marshal(cfg)
	if err := s.q.UpsertSetting(ctx, gen.UpsertSettingParams{Key: settingsKey, Value: raw}); err != nil {
		return err
	}
	s.PushAll(ctx)
	return nil
}

// buildConfig renders the RouterConfig command payload for a node.
func (s *Service) buildConfig(ctx context.Context, nodeID uuid.UUID) (*nodesv1.RouterConfig, error) {
	cfg, err := s.Get(ctx)
	if err != nil {
		return nil, err
	}
	out := &nodesv1.RouterConfig{Enabled: cfg.Enabled, Port: cfg.Port}
	if !cfg.Enabled {
		return out, nil
	}
	if cfg.DefaultServerID != "" {
		if id, err := uuid.Parse(cfg.DefaultServerID); err == nil {
			if sv, err := s.q.GetServer(ctx, id); err == nil && sv.NodeID == nodeID {
				if a, err := s.q.GetAllocationForServer(ctx, id); err == nil {
					out.DefaultPort = a.Port
				}
			}
		}
	}
	domains, err := s.q.ListRouterDomains(ctx)
	if err != nil {
		return nil, err
	}
	for _, d := range domains {
		if d.NodeID != nodeID {
			continue
		}
		out.Entries = append(out.Entries, &nodesv1.RouterEntry{
			Domain:    d.Domain,
			LocalPort: d.LocalPort,
		})
	}
	return out, nil
}

// Push sends the current table to one node.
func (s *Service) Push(ctx context.Context, nodeID uuid.UUID) {
	cfg, err := s.buildConfig(ctx, nodeID)
	if err != nil {
		s.log.Warn("router config build failed", "node_id", nodeID, "err", err)
		return
	}
	sent, _ := json.Marshal(cfg)
	s.mu.Lock()
	prev, seen := s.last[nodeID]
	s.mu.Unlock()
	if seen && string(prev) == string(sent) {
		return
	}
	sendCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if _, err := s.channels.Send(sendCtx, nodeID, &nodesv1.Command{
		Action: &nodesv1.Command_RouterConfig{RouterConfig: cfg},
	}); err != nil {
		s.log.Warn("router config push failed", "node_id", nodeID, "err", err)
		return
	}
	s.mu.Lock()
	s.last[nodeID] = sent
	s.mu.Unlock()
}

// PushAll pushes to every connected node.
func (s *Service) PushAll(ctx context.Context) {
	rows, err := s.q.ListNodes(ctx)
	if err != nil {
		return
	}
	for _, n := range rows {
		if s.channels.Connected(n.ID) {
			s.Push(ctx, n.ID)
		}
	}
}

// NodeOnline is called when a daemon channel opens: push fresh config.
func (s *Service) NodeOnline(nodeID uuid.UUID) {
	go s.Push(context.Background(), nodeID)
}
