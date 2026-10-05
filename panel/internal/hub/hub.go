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

	// scrollbackLines are replayed on console connect.
	scrollbackLines = 300
)

// Event is one realtime update for a server.
type Event struct {
	Type       string
	ServerID   uuid.UUID
	Console    *nodesv1.ConsoleLine
	Stats      *nodesv1.ServerStats
	Status     string
	Detail     string
	Install    *nodesv1.InstallProgress
}

// StatsSnapshot is the pollable latest stats value.
type StatsSnapshot struct {
	CPUPercent     float64   `json:"cpu_percent"`
	MemoryUsedMB   float64   `json:"memory_used_mb"`
	MemoryLimitMB  float64   `json:"memory_limit_mb"`
	RxBytesPerSec  float64   `json:"network_rx_bps"`
	TxBytesPerSec  float64   `json:"network_tx_bps"`
	CollectedAt    time.Time `json:"collected_at"`
}

type subscription struct {
	ch chan Event
}

// Hub fans out events per server.
type Hub struct {
	mu          sync.RWMutex
	subs        map[uuid.UUID]map[*subscription]struct{}
	stats       map[uuid.UUID]*StatsSnapshot
	scrollback  map[uuid.UUID][]string
}

func New() *Hub {
	return &Hub{
		subs:       map[uuid.UUID]map[*subscription]struct{}{},
		stats:      map[uuid.UUID]*StatsSnapshot{},
		scrollback: map[uuid.UUID][]string{},
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
				CPUPercent: ev.Stats.GetCpuPercent(),
				MemoryUsedMB: ev.Stats.GetMemoryUsedMb(),
				MemoryLimitMB: ev.Stats.GetMemoryLimitMb(),
				RxBytesPerSec: ev.Stats.GetNetworkRxBytesPerSec(),
				TxBytesPerSec: ev.Stats.GetNetworkTxBytesPerSec(),
				CollectedAt: time.Now(),
			}
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
