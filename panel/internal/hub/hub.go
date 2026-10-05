// Package hub fans realtime server events out to browser subscribers and
// keeps the latest snapshot for polling clients. It also signs the
// short-lived WebSocket tokens used to authenticate console connections.
package hub

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"

	nodesv1 "quarry.dev/quarry/proto/gen/go/quarry/node/v1"
)

const (
	// EventConsole, EventStats, EventStatus are the published event types.
	EventConsole = "console"
	EventStats   = "stats"
	EventStatus  = "status"
	EventInstall = "install"
	EventPlayers = "players"

	// scrollbackLines are replayed on console connect.
	scrollbackLines = 300

	// statsWindow is how much stats history is kept per server and served
	// to the UI graphs.
	statsWindow = 5 * time.Minute
	// historyCap bounds the ring even if stats arrive faster than expected.
	historyCap = 200
)

// Sample is one point of server activity history.
type Sample struct {
	At       time.Time `json:"t"`
	CPU      float64   `json:"cpu_percent"`
	MemUsed  float64   `json:"memory_used_mb"`
	MemLimit float64   `json:"memory_limit_mb"`
	Players  int       `json:"players"`
}

// Event is one realtime update for a server.
type Event struct {
	Type     string
	ServerID uuid.UUID
	Console  *nodesv1.ConsoleLine
	Stats    *nodesv1.ServerStats
	Status   string
	Detail   string
	Install  *nodesv1.InstallProgress
	Players  *PlayerList
}

// PlayerList is the most recent `list` snapshot for a server.
type PlayerList struct {
	Names  []string `json:"names"`
	Online int      `json:"online"`
	Max    int      `json:"max"`
}

// StatsSnapshot is the pollable latest stats value.
type StatsSnapshot struct {
	CPUPercent    float64   `json:"cpu_percent"`
	MemoryUsedMB  float64   `json:"memory_used_mb"`
	MemoryLimitMB float64   `json:"memory_limit_mb"`
	RxBytesPerSec float64   `json:"network_rx_bps"`
	TxBytesPerSec float64   `json:"network_tx_bps"`
	CollectedAt   time.Time `json:"collected_at"`
}

type subscription struct {
	ch chan Event
}

// Hub fans out events per server.
type Hub struct {
	mu         sync.RWMutex
	subs       map[uuid.UUID]map[*subscription]struct{}
	stats      map[uuid.UUID]*StatsSnapshot
	scrollback map[uuid.UUID][]string
	players    map[uuid.UUID]*PlayerList
	history    map[uuid.UUID][]Sample
}

func New() *Hub {
	return &Hub{
		subs:       map[uuid.UUID]map[*subscription]struct{}{},
		stats:      map[uuid.UUID]*StatsSnapshot{},
		scrollback: map[uuid.UUID][]string{},
		players:    map[uuid.UUID]*PlayerList{},
		history:    map[uuid.UUID][]Sample{},
	}
}

// Subscribe returns a channel of events for a server plus a cancel func.
func (h *Hub) Subscribe(serverID uuid.UUID) (<-chan Event, func()) {
	sub := &subscription{ch: make(chan Event, 256)}
	h.mu.Lock()
	if h.subs[serverID] == nil {
		h.subs[serverID] = map[*subscription]struct{}{}
	}
	h.subs[serverID][sub] = struct{}{}
	h.mu.Unlock()
	return sub.ch, func() {
		h.mu.Lock()
		delete(h.subs[serverID], sub)
		h.mu.Unlock()
	}
}

// Publish delivers an event to subscribers and updates snapshots.
func (h *Hub) Publish(ev Event) {
	switch ev.Type {
	case EventStats:
		if ev.Stats != nil {
			h.mu.Lock()
			h.stats[ev.ServerID] = &StatsSnapshot{
				CPUPercent:    ev.Stats.GetCpuPercent(),
				MemoryUsedMB:  ev.Stats.GetMemoryUsedMb(),
				MemoryLimitMB: ev.Stats.GetMemoryLimitMb(),
				RxBytesPerSec: ev.Stats.GetNetworkRxBytesPerSec(),
				TxBytesPerSec: ev.Stats.GetNetworkTxBytesPerSec(),
				CollectedAt:   time.Now(),
			}
			// History samples ride along with every stats event; the player
			// count is whatever the last poll observed.
			players := 0
			if p := h.players[ev.ServerID]; p != nil {
				players = p.Online
			}
			h.appendHistoryLocked(ev.ServerID, Sample{
				At:       time.Now(),
				CPU:      ev.Stats.GetCpuPercent(),
				MemUsed:  ev.Stats.GetMemoryUsedMb(),
				MemLimit: ev.Stats.GetMemoryLimitMb(),
				Players:  players,
			})
			h.mu.Unlock()
		}
	case EventConsole:
		if ev.Console != nil {
			h.mu.Lock()
			lines := append(h.scrollback[ev.ServerID], ev.Console.GetLine())
			if len(lines) > scrollbackLines {
				lines = lines[len(lines)-scrollbackLines:]
			}
			h.scrollback[ev.ServerID] = lines
			h.mu.Unlock()
		}
	case EventPlayers:
		if ev.Players != nil {
			h.mu.Lock()
			h.players[ev.ServerID] = ev.Players
			h.mu.Unlock()
		}
	}

	h.mu.RLock()
	subs := make([]*subscription, 0, len(h.subs[ev.ServerID]))
	for sub := range h.subs[ev.ServerID] {
		subs = append(subs, sub)
	}
	h.mu.RUnlock()
	for _, sub := range subs {
		select {
		case sub.ch <- ev:
		default: // slow subscriber: drop
		}
	}
}

// LatestStats returns the most recent stats snapshot for a server.
func (h *Hub) LatestStats(serverID uuid.UUID) (*StatsSnapshot, bool) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	s, ok := h.stats[serverID]
	return s, ok
}

// Scrollback returns the buffered recent console lines.
func (h *Hub) Scrollback(serverID uuid.UUID) []string {
	h.mu.RLock()
	defer h.mu.RUnlock()
	out := make([]string, len(h.scrollback[serverID]))
	copy(out, h.scrollback[serverID])
	return out
}

// LatestPlayers returns the most recent player list snapshot.
func (h *Hub) LatestPlayers(serverID uuid.UUID) (*PlayerList, bool) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	p, ok := h.players[serverID]
	return p, ok
}

// appendHistoryLocked adds a sample and trims the ring to the window.
// Callers hold h.mu for writing.
func (h *Hub) appendHistoryLocked(serverID uuid.UUID, s Sample) {
	hist := append(h.history[serverID], s)
	cutoff := time.Now().Add(-statsWindow)
	drop := 0
	for drop < len(hist) && hist[drop].At.Before(cutoff) {
		drop++
	}
	hist = hist[drop:]
	if len(hist) > historyCap {
		hist = hist[len(hist)-historyCap:]
	}
	h.history[serverID] = hist
}

// History returns the stats samples of the last statsWindow for a server,
// oldest first. Stale samples (nothing arrived recently) age out.
func (h *Hub) History(serverID uuid.UUID) []Sample {
	h.mu.Lock()
	defer h.mu.Unlock()
	hist := h.history[serverID]
	cutoff := time.Now().Add(-statsWindow)
	drop := 0
	for drop < len(hist) && hist[drop].At.Before(cutoff) {
		drop++
	}
	hist = hist[drop:]
	if len(hist) == 0 {
		if h.history[serverID] != nil {
			h.history[serverID] = nil
		}
		return nil
	}
	out := make([]Sample, len(hist))
	copy(out, hist)
	h.history[serverID] = hist
	return out
}

// SetPlayers stores a snapshot (used to clear the list on stop).
func (h *Hub) SetPlayers(serverID uuid.UUID, p *PlayerList) {
	h.Publish(Event{Type: EventPlayers, ServerID: serverID, Players: p})
}

// ActiveServers returns the servers that currently have subscribers (used
// to decide which servers are worth polling for players).
func (h *Hub) ActiveServers() []uuid.UUID {
	h.mu.RLock()
	defer h.mu.RUnlock()
	out := make([]uuid.UUID, 0, len(h.subs))
	for id, subs := range h.subs {
		if len(subs) > 0 {
			out = append(out, id)
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// WebSocket tokens: HMAC-signed, 60-second validity, server+user bound.
// ---------------------------------------------------------------------------

type wsClaims struct {
	ServerID string `json:"s"`
	UserID   string `json:"u"`
	Expires  int64  `json:"e"`
}

var ErrWSToken = errors.New("invalid or expired websocket token")

func SignWSToken(key []byte, serverID, userID uuid.UUID, ttl time.Duration) string {
	claims := wsClaims{ServerID: serverID.String(), UserID: userID.String(), Expires: time.Now().Add(ttl).Unix()}
	payload, _ := json.Marshal(claims)
	body := base64.RawURLEncoding.EncodeToString(payload)
	return body + "." + base64.RawURLEncoding.EncodeToString(hmacSHA256(key, body))
}

func VerifyWSToken(key []byte, token string) (serverID, userID uuid.UUID, err error) {
	body, sig, found := cut(token, ".")
	if !found {
		return uuid.Nil, uuid.Nil, ErrWSToken
	}
	if !hmac.Equal(hmacSHA256(key, body), sigBytes(sig)) {
		return uuid.Nil, uuid.Nil, ErrWSToken
	}
	payload, err := base64.RawURLEncoding.DecodeString(body)
	if err != nil {
		return uuid.Nil, uuid.Nil, ErrWSToken
	}
	var claims wsClaims
	if err := json.Unmarshal(payload, &claims); err != nil {
		return uuid.Nil, uuid.Nil, ErrWSToken
	}
	if time.Now().Unix() > claims.Expires {
		return uuid.Nil, uuid.Nil, fmt.Errorf("%w: expired", ErrWSToken)
	}
	sid, err := uuid.Parse(claims.ServerID)
	if err != nil {
		return uuid.Nil, uuid.Nil, ErrWSToken
	}
	uid, err := uuid.Parse(claims.UserID)
	if err != nil {
		return uuid.Nil, uuid.Nil, ErrWSToken
	}
	return sid, uid, nil
}

func hmacSHA256(key []byte, data string) []byte {
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(data))
	return mac.Sum(nil)
}

func sigBytes(s string) []byte {
	b, _ := base64.RawURLEncoding.DecodeString(s)
	return b
}

func cut(s, sep string) (before, after string, found bool) {
	for i := 0; i+len(sep) <= len(s); i++ {
		if s[i:i+len(sep)] == sep {
			return s[:i], s[i+len(sep):], true
		}
	}
	return s, "", false
}
