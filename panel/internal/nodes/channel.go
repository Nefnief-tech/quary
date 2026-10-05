// Command dispatch over the daemons' Channel streams. Daemons dial out, so
// the panel pushes commands down each daemon's open stream and correlates
// the results by id.
package nodes

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"

	nodesv1 "quarry.dev/quarry/proto/gen/go/quarry/node/v1"
)

var (
	ErrNodeOffline     = errors.New("node is not connected")
	ErrCommandTimeout  = errors.New("command timed out")
)

// resultTimeout bounds ordinary commands; long-running ones get more.
const (
	resultTimeout   = 45 * time.Second
	stopTimeout     = 3 * time.Minute
	installTimeout  = 30 * time.Minute
)

func timeoutFor(cmd *nodesv1.Command) time.Duration {
	switch {
	case cmd.GetInstallServer() != nil:
		return installTimeout
	case cmd.GetStopServer() != nil:
		return stopTimeout
	default:
		return resultTimeout
	}
}

// Conn is one daemon's open command channel.
type Conn struct {
	send    chan *nodesv1.ChannelResponse
	mu      sync.Mutex
	pending map[string]chan *nodesv1.CommandResult
}

func newConn() *Conn {
	return &Conn{
		send:    make(chan *nodesv1.ChannelResponse, 64),
		pending: map[string]chan *nodesv1.CommandResult{},
	}
}

func (c *Conn) deliver(result *nodesv1.CommandResult) {
	c.mu.Lock()
	ch, ok := c.pending[result.GetCorrelationId()]
	delete(c.pending, result.GetCorrelationId())
	c.mu.Unlock()
	if ok {
		select {
		case ch <- result:
		default:
		}
	}
}

// ChannelManager tracks connected daemons and dispatches commands.
type ChannelManager struct {
	mu    sync.RWMutex
	conns map[uuid.UUID]*Conn
	log   *slog.Logger
}

func NewChannelManager(log *slog.Logger) *ChannelManager {
	return &ChannelManager{conns: map[uuid.UUID]*Conn{}, log: log}
}

// Register attaches a freshly connected daemon channel. It returns the conn
// plus a done func the stream handler calls on teardown.
func (m *ChannelManager) Register(nodeID uuid.UUID) (*Conn, func()) {
	conn := newConn()
	m.mu.Lock()
	m.conns[nodeID] = conn
	m.mu.Unlock()
	return conn, func() {
		m.mu.Lock()
		if m.conns[nodeID] == conn {
			delete(m.conns, nodeID)
		}
		m.mu.Unlock()
	}
}

// Connected reports whether the node has an open channel.
func (m *ChannelManager) Connected(nodeID uuid.UUID) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	_, ok := m.conns[nodeID]
	return ok
}

// Send pushes a command down the daemon's channel and awaits its result.
// call with a longer context for long-running commands (installs).
func (m *ChannelManager) Send(ctx context.Context, nodeID uuid.UUID, cmd *nodesv1.Command) (*nodesv1.CommandResult, error) {
	m.mu.RLock()
	conn := m.conns[nodeID]
	m.mu.RUnlock()
	if conn == nil {
		return nil, ErrNodeOffline
	}

	correlation := uuid.NewString()
	cmd.CorrelationId = correlation
	ch := make(chan *nodesv1.CommandResult, 1)
	conn.mu.Lock()
	conn.pending[correlation] = ch
	conn.mu.Unlock()
	defer func() {
		conn.mu.Lock()
		delete(conn.pending, correlation)
		conn.mu.Unlock()
	}()

	select {
	case conn.send <- &nodesv1.ChannelResponse{Command: cmd}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	timer := time.NewTimer(timeoutFor(cmd))
	defer timer.Stop()
	select {
	case result := <-ch:
		return result, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-timer.C:
		return nil, fmt.Errorf("%w after %s", ErrCommandTimeout, timeoutFor(cmd))
	}
}
