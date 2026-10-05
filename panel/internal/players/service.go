// Package players keeps an online-player snapshot fresh for every server
// someone is watching. It polls the daemon's PollPlayers command (console
// `list` parsing) on a ticker and publishes results through the hub; only
// servers with live WS subscribers are polled.
package players

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"quarry.dev/quarry/panel/internal/db/gen"
	"quarry.dev/quarry/panel/internal/hub"
	"quarry.dev/quarry/panel/internal/lifecycle"
	"quarry.dev/quarry/panel/internal/nodes"
	nodesv1 "quarry.dev/quarry/proto/gen/go/quarry/node/v1"
)

type Service struct {
	q        *gen.Queries
	channels *nodes.ChannelManager
	hub      *hub.Hub
	log      *slog.Logger
	interval time.Duration
}

func NewService(q *gen.Queries, channels *nodes.ChannelManager, hb *hub.Hub, log *slog.Logger) *Service {
	return &Service{q: q, channels: channels, hub: hb, log: log, interval: 10 * time.Second}
}

// Run polls watched servers until ctx is cancelled. One goroutine, one
// ticker: polling cost scales with attention, not with server count.
func (s *Service) Run(ctx context.Context) {
	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			for _, id := range s.hub.ActiveServers() {
				s.pollServer(ctx, id)
			}
		}
	}
}

func (s *Service) pollServer(ctx context.Context, id uuid.UUID) {
	sv, err := s.q.GetServer(ctx, id)
	if err != nil {
		return
	}
	if lifecycle.Status(sv.Status) != lifecycle.Running {
		// Clear a stale list as soon as the server is not running.
		if _, ok := s.hub.LatestPlayers(id); ok {
			s.hub.SetPlayers(id, &hub.PlayerList{Names: []string{}})
		}
		return
	}
	sendCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
	result, err := s.channels.Send(sendCtx, sv.NodeID, &nodesv1.Command{
		Action: &nodesv1.Command_PollPlayers{PollPlayers: &nodesv1.PollPlayers{
			ServerId: id.String(),
		}},
	})
	cancel()
	if err != nil || !result.GetOk() {
		// Offline node or non-graceful reply: leave the last snapshot up.
		if err != nil && err != nodes.ErrNodeOffline {
			s.log.Warn("player poll failed", "server_id", id, "err", err)
		}
		return
	}
	var list hub.PlayerList
	if err := json.Unmarshal(result.GetBody(), &list); err != nil {
		s.log.Warn("player poll body undecodable", "server_id", id, "err", err)
		return
	}
	if list.Names == nil {
		list.Names = []string{}
	}
	s.hub.SetPlayers(id, &list)
}
