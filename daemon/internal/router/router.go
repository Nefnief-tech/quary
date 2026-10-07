// Package router implements the handshake-aware TCP relay: it listens on
// the standard Minecraft port and routes each connection by the hostname
// the player typed (carried inside the Minecraft handshake packet) to the
// matching local server port. Every server can therefore sit behind one
// port, addressed by domain.
package router

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"strings"
	"sync"
	"time"

	nodesv1 "quarry.dev/quarry/proto/gen/go/quarry/node/v1"
)

const (
	handshakeTimeout = 10 * time.Second
	// maxHandshakePacket bounds the initial packet (vanilla clients send
	// well under 1 KiB).
	maxHandshakePacket = 16 * 1024
)

// Manager owns the relay listener and the current routing table.
type Manager struct {
	log *slog.Logger
	// dialHost is where local server ports are reached. On a host-networked
	// daemon that is 127.0.0.1; from a bridged container it is the docker
	// host gateway.
	dialHost string

	mu       sync.Mutex
	enabled  bool
	port     int
	entries  map[string]int32 // lowercase domain -> local port
	fallback int32            // port for unmatched hosts; 0 = refuse
	ln       net.Listener
	cancel   context.CancelFunc
}

func NewManager(log *slog.Logger, dialHost string) *Manager {
	if dialHost == "" {
		dialHost = "127.0.0.1"
	}
	return &Manager{log: log, dialHost: dialHost, entries: map[string]int32{}}
}

// ApplyConfig swaps the routing table and (re)binds the listener.
func (m *Manager) ApplyConfig(ctx context.Context, cfg *nodesv1.RouterConfig) {
	m.mu.Lock()
	defer m.mu.Unlock()
	entries := make(map[string]int32, len(cfg.GetEntries()))
	for _, e := range cfg.GetEntries() {
		d := normalizeDomain(e.GetDomain())
		if d == "" || e.GetLocalPort() <= 0 {
			continue
		}
		entries[d] = e.GetLocalPort()
	}
	m.entries = entries
	m.fallback = cfg.GetDefaultPort()
	m.enabled = cfg.GetEnabled()
	wantPort := int(cfg.GetPort())
	if wantPort <= 0 {
		wantPort = 25565
	}

	if !m.enabled {
		m.stopLocked()
		m.log.Info("handshake router disabled")
		return
	}
	if m.ln != nil && m.port == wantPort {
		return // table swapped in place; listener stays
	}
	m.stopLocked()
	addr := fmt.Sprintf("0.0.0.0:%d", wantPort)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		m.log.Error("handshake router bind failed", "addr", addr, "err", err)
		return
	}
	m.ln = ln
	m.port = wantPort
	runCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	m.cancel = cancel
	go m.acceptLoop(runCtx, ln)
	m.log.Info("handshake router listening", "addr", addr, "domains", len(entries))
}

func (m *Manager) stopLocked() {
	if m.cancel != nil {
		m.cancel()
		m.cancel = nil
	}
	if m.ln != nil {
		_ = m.ln.Close()
		m.ln = nil
	}
}

func (m *Manager) acceptLoop(ctx context.Context, ln net.Listener) {
	for {
		conn, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			m.log.Warn("router accept failed", "err", err)
			return
		}
		go m.handle(ctx, conn)
	}
}

// handle reads one client's handshake, routes by hostname and splices.
func (m *Manager) handle(ctx context.Context, conn net.Conn) {
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(handshakeTimeout))

	hs, err := readHandshake(conn)
	if err != nil {
		return // garbage or timeout; drop silently
	}
	_ = conn.SetDeadline(time.Time{}) // established streams live long

	host := normalizeDomain(hs.host)
	m.mu.Lock()
	port := int32(0)
	if p, ok := m.entries[host]; ok {
		port = p
	} else if m.fallback > 0 {
		port = m.fallback
	}
	m.mu.Unlock()

	_ = conn.SetDeadline(time.Now().Add(handshakeTimeout))
	if port == 0 {
		refuse(conn, hs.nextState, "no server for this address")
		return
	}
	_ = conn.SetDeadline(time.Time{})

	upstream, err := net.DialTimeout("tcp", fmt.Sprintf("%s:%d", m.dialHost, port), 3*time.Second)
	if err != nil {
		refuse(conn, hs.nextState, "server is offline")
		return
	}
	defer func() { _ = upstream.Close() }()

	// Forward the already-consumed handshake bytes, then splice both ways.
	if _, err := upstream.Write(hs.consumed); err != nil {
		return
	}
	done := make(chan struct{}, 2)
	go func() { _, _ = io.Copy(upstream, conn); done <- struct{}{} }()
	go func() { _, _ = io.Copy(conn, upstream); done <- struct{}{} }()
	select {
	case <-done:
	case <-ctx.Done():
	}
}

// handshake is the parsed initial packet.
type handshake struct {
	host      string
	nextState int32
	consumed  []byte // raw bytes already read from the connection
}

// readHandshake reads and parses the first packet from the connection.
func readHandshake(conn net.Conn) (*handshake, error) {
	lenBuf := make([]byte, 0, 5)
	for {
		b := make([]byte, 1)
		if _, err := io.ReadFull(conn, b); err != nil {
			return nil, err
		}
		lenBuf = append(lenBuf, b[0])
		if b[0]&0x80 == 0 {
			break
		}
		if len(lenBuf) > 5 {
			return nil, fmt.Errorf("packet length varint too long")
		}
	}
	plen, _, err := decodeVarint(lenBuf)
	if err != nil {
		return nil, err
	}
	if plen <= 0 || plen > maxHandshakePacket {
		return nil, fmt.Errorf("packet length out of range")
	}
	packet := make([]byte, plen)
	if _, err := io.ReadFull(conn, packet); err != nil {
		return nil, err
	}
	consumed := append(lenBuf, packet...)

	pos := 0
	id, n, err := decodeVarint(packet[pos:])
	if err != nil || id != 0 {
		return nil, fmt.Errorf("not a handshake packet")
	}
	pos += n
	_, n, err = decodeVarint(packet[pos:]) // protocol version
	if err != nil {
		return nil, err
	}
	pos += n
	addrLen, n, err := decodeVarint(packet[pos:])
	if err != nil || addrLen <= 0 || addrLen > 1024 {
		return nil, fmt.Errorf("bad handshake address")
	}
	pos += n
	if int(addrLen) > len(packet)-pos {
		return nil, fmt.Errorf("handshake address truncated")
	}
	host := string(packet[pos : pos+int(addrLen)])
	pos += int(addrLen)
	if pos+2 > len(packet) {
		return nil, fmt.Errorf("handshake truncated before port")
	}
	pos += 2 // handshake port; routing uses the panel table instead
	nextState := int32(0)
	if pos < len(packet) {
		ns, _, err := decodeVarint(packet[pos:])
		if err == nil {
			nextState = int32(ns)
		}
	}
	// Forge/modded clients append a marker after the hostname.
	if i := strings.IndexByte(host, 0); i >= 0 {
		host = host[:i]
	}
	return &handshake{host: host, nextState: nextState, consumed: consumed}, nil
}

func decodeVarint(b []byte) (uint64, int, error) {
	var n uint64
	for i, c := range b {
		if i >= 5 {
			return 0, 0, fmt.Errorf("varint too long")
		}
		n |= uint64(c&0x7F) << (7 * i)
		if c&0x80 == 0 {
			return n, i + 1, nil
		}
	}
	return 0, 0, fmt.Errorf("varint truncated")
}

// writePacket frames and writes one server-bound packet.
func writePacket(conn net.Conn, payload []byte) error {
	framed := append(varint(len(payload)), payload...)
	_, err := conn.Write(framed)
	return err
}

func varint(n int) []byte {
	out := make([]byte, 0, 5)
	for {
		b := byte(n & 0x7F)
		n >>= 7
		if n != 0 {
			out = append(out, b|0x80)
		} else {
			return append(out, b)
		}
	}
}

// refuse answers a refused client with a proper protocol reply: a status
// JSON for pings, a disconnect chat message for join attempts. Both carry
// the JSON as a varint-length-prefixed string field, per the protocol.
func refuse(conn net.Conn, nextState int32, msg string) {
	if nextState == 2 {
		chat, _ := json.Marshal(map[string]any{"text": "✘ " + msg})
		payload := append([]byte{0x00}, append(varint(len(chat)), chat...)...)
		_ = writePacket(conn, payload)
		return
	}
	status, _ := json.Marshal(map[string]any{
		"version":     map[string]any{"name": "Quarry", "protocol": -1},
		"players":     map[string]any{"online": 0, "max": 0},
		"description": map[string]any{"text": "✘ " + msg},
	})
	payload := append([]byte{0x00}, append(varint(len(status)), status...)...)
	_ = writePacket(conn, payload)
}

// normalizeDomain lowercases, strips port suffixes/trailing dots and the
// forge suffix, so table lookups are stable.
func normalizeDomain(d string) string {
	d = strings.ToLower(strings.TrimSpace(d))
	d = strings.TrimSuffix(d, ".")
	if i := strings.LastIndex(d, ":"); i > 0 {
		d = d[:i]
	}
	return d
}
