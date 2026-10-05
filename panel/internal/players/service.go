// Package players keeps an online-player snapshot fresh for every server
// someone is watching. It polls the daemon's PollPlayers command (console
// `list` parsing) on a ticker and publishes results through the hub; only
// servers with live WS subscribers are polled.
package players

import (
	"context"
	"encoding/json"
	"log/slog"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"quarry.dev/quarry/panel/internal/db/gen"
	"quarry.dev/quarry/panel/internal/hub"
	"quarry.dev/quarry/panel/internal/lifecycle"
	"quarry.dev/quarry/panel/internal/nodes"
	nodesv1 "quarry.dev/quarry/proto/gen/go/quarry/node/v1"
)

// listRe tolerates log prefixes ("[12:34:56] [Server thread/INFO]: ").
var listRe = regexp.MustCompile(`There are (\d+) of a max of (\d+) players online(?::\s*(.*))?`)

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
		Action: &nodesv1.Command_RunConsole{RunConsole: &nodesv1.RunConsole{
			ServerId: id.String(),
			Input:    "list",
			Expect:   `There are \d+ of a max of \d+ players online`,
			MaxLines: 1,
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
	var cap struct {
		Lines []string `json:"lines"`
	}
	if err := json.Unmarshal(result.GetBody(), &cap); err != nil || len(cap.Lines) == 0 {
		return
	}
	list := parseList(cap.Lines[len(cap.Lines)-1])
	if list == nil {
		return
	}
	s.hub.SetPlayers(id, list)
}

// parseList extracts the player list from one console `list` reply line.
func parseList(line string) *hub.PlayerList {
	m := listRe.FindStringSubmatch(line)
	if m == nil {
		return nil
	}
	online, _ := strconv.Atoi(m[1])
	max, _ := strconv.Atoi(m[2])
	var names []string
	if raw := strings.TrimSpace(m[3]); raw != "" {
		for _, n := range strings.Split(raw, ", ") {
			if n = strings.TrimSpace(n); n != "" {
				names = append(names, n)
			}
		}
	}
	if names == nil {
		names = []string{}
	}
	return &hub.PlayerList{Names: names, Online: online, Max: max}
}
