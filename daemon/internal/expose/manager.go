// Package expose manages per-server playit.gg tunnel agents on the node.
// The agent runs as a Docker container from the official image pinned by
// digest, on the host network so it can reach the server's published port.
// The claim flow is implemented natively against playit's HTTP API: the
// daemon generates a code, the user claims it in a browser, and the daemon
// exchanges it for the agent secret (stored 0600 on the node).
package expose

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"quarry.dev/quarry/internal/ssrf"
)

// image is the official playit-cloud agent, pinned by digest
// (ghcr.io/playit-cloud/playit-agent tag 1.0.8).
const (
	imageRef  = "ghcr.io/playit-cloud/playit-agent@sha256:2c69d171b12ea907a1bf1f3fcf06d75de4458c3a3792022fe95375ed922e0bf1"
	apiBase   = "https://api.playit.gg"
	claimURL  = "https://playit.gg/claim/"
	userAgent = "quarry-panel/0.1 (quarry.dev)"
)

var (
	// agent secrets are hex strings (validated by the agent itself).
	secretRe = regexp.MustCompile(`^[0-9a-f]{32,128}$`)
)

// AgentRuntime manages the agent container on this node.
type AgentRuntime interface {
	// EnsurePlayitImage pulls the pinned agent image if not present.
	EnsurePlayitImage(ctx context.Context) error
	// StartPlayitAgent runs (or restarts) the agent container for a server
	// with the given secret, on the host network.
	StartPlayitAgent(ctx context.Context, id uuid.UUID, secret string) error
	// StopPlayitAgent removes the agent container for a server.
	StopPlayitAgent(ctx context.Context, id uuid.UUID) error
	// PlayitAgentRunning reports whether the agent container is running.
	PlayitAgentRunning(ctx context.Context, id uuid.UUID) (bool, error)
	// StreamPlayitLogs feeds filtered agent log lines to onLine until the
	// container's logs end or ctx is cancelled.
	StreamPlayitLogs(ctx context.Context, id uuid.UUID, onLine func(line string))
}

// Emitter mirrors setup progress into the server's console stream.
type Emitter interface {
	EmitConsole(serverID uuid.UUID, line string)
}

// Logger is the subset of slog used here.
type Logger interface {
	Info(msg string, args ...any)
	Warn(msg string, args ...any)
}

// Config for the manager.
type Config struct {
	// DataDir is the daemon's persistent state directory; agent secrets
	// are stored here (0600).
	DataDir string
}

// Status is the JSON-encoded command result.
type Status struct {
	Active   bool   `json:"active"`
	ClaimURL string `json:"claim_url,omitempty"`
}

// Manager runs one playit agent per exposed server.
type Manager struct {
	cfg   Config
	rt    AgentRuntime
	guard *ssrf.Guard
	log   Logger
	http  *http.Client

	mu      sync.Mutex
	pending map[uuid.UUID]string // server -> unexchanged claim code
}

func NewManager(cfg Config, rt AgentRuntime, guard *ssrf.Guard, log Logger) (*Manager, error) {
	if err := os.MkdirAll(cfg.DataDir, 0o700); err != nil {
		return nil, fmt.Errorf("create data dir: %w", err)
	}
	return &Manager{
		cfg:     cfg,
		rt:      rt,
		guard:   guard,
		log:     log,
		http:    ssrf.NewHTTPClient(),
		pending: map[uuid.UUID]string{},
	}, nil
}

func (c Config) secretPath(id uuid.UUID) string {
	// DataDir is node-local persistent state (0700); secrets 0600.
	return c.DataDir + "/playit-" + id.String() + ".secret"
}

func (m *Manager) hasSecret(id uuid.UUID) bool {
	_, err := os.Stat(m.cfg.secretPath(id))
	return err == nil
}

// Start begins setup: with a secret on file it just ensures the agent
// runs; otherwise it generates a claim code and returns the claim URL.
func (m *Manager) Start(ctx context.Context, id uuid.UUID, emit Emitter) (*Status, error) {
	if m.hasSecret(id) {
		if err := m.rt.EnsurePlayitImage(ctx); err != nil {
			return nil, err
		}
		secret, err := m.loadSecret(id)
		if err != nil {
			return nil, err
		}
		if err := m.rt.StartPlayitAgent(ctx, id, secret); err != nil {
			return nil, err
		}
		m.streamLogs(ctx, id, emit)
		return &Status{Active: true}, nil
	}

	// Claim codes are 5 random bytes hex-encoded, matching upstream.
	buf := make([]byte, 5)
	if _, err := rand.Read(buf); err != nil {
		return nil, err
	}
	code := hex.EncodeToString(buf)
	status, err := m.callSetup(ctx, code)
	if err != nil {
		return nil, fmt.Errorf("register claim: %w", err)
	}
	if status == "UserRejected" {
		return nil, errors.New("claim rejected by playit")
	}

	m.mu.Lock()
	m.pending[id] = code
	m.mu.Unlock()
	url := claimURL + code
	emitConsole(emit, id, "playit: claim the tunnel at "+url)
	return &Status{ClaimURL: url}, nil
}

// Claim finishes setup after the user's browser claim: exchanges the code
// for the agent secret, stores it and starts the container.
func (m *Manager) Claim(ctx context.Context, id uuid.UUID, emit Emitter) (*Status, error) {
	if m.hasSecret(id) {
		secret, err := m.loadSecret(id)
		if err != nil {
			return nil, err
		}
		if err := m.rt.StartPlayitAgent(ctx, id, secret); err != nil {
			return nil, err
		}
		return &Status{Active: true}, nil
	}
	m.mu.Lock()
	code, pending := m.pending[id]
	m.mu.Unlock()
	if !pending {
		return nil, errors.New("no pending tunnel setup; start it first")
	}

	ctxWait, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	// Poll setup until the user accepts (or the deadline passes).
	for {
		status, err := m.callSetup(ctxWait, code)
		if err != nil {
			return nil, err
		}
		if status == "UserAccepted" {
			break
		}
		if status == "UserRejected" {
			return nil, errors.New("claim rejected in browser")
		}
		select {
		case <-ctxWait.Done():
			return &Status{ClaimURL: claimURL + code}, nil // still unclaimed
		case <-time.After(2 * time.Second):
		}
	}

	secret, err := m.callExchange(ctxWait, code)
	if err != nil {
		return nil, fmt.Errorf("exchange claim: %w", err)
	}
	if err := m.storeSecret(id, secret); err != nil {
		return nil, err
	}
	m.mu.Lock()
	delete(m.pending, id)
	m.mu.Unlock()

	if err := m.rt.EnsurePlayitImage(ctxWait); err != nil {
		return nil, err
	}
	if err := m.rt.StartPlayitAgent(ctxWait, id, secret); err != nil {
		return nil, err
	}
	m.streamLogs(ctx, id, emit)
	emitConsole(emit, id, "playit: tunnel claimed and agent started")
	return &Status{Active: true}, nil
}

// streamLogs mirrors filtered agent log lines into the server console,
// which is where the assigned public address shows up.
func (m *Manager) streamLogs(ctx context.Context, id uuid.UUID, emit Emitter) {
	if ctx == nil {
		return
	}
	go m.rt.StreamPlayitLogs(context.WithoutCancel(ctx), id, func(line string) {
		emitConsole(emit, id, "[playit] "+line)
	})
}

// Stop removes the agent container and forgets the server's secret.
func (m *Manager) Stop(ctx context.Context, id uuid.UUID, emit Emitter) error {
	_ = m.rt.StopPlayitAgent(ctx, id)
	_ = os.Remove(m.cfg.secretPath(id))
	m.mu.Lock()
	delete(m.pending, id)
	m.mu.Unlock()
	emitConsole(emit, id, "playit: tunnel stopped")
	return nil
}

// Reconcile restarts agents for secrets left behind by a daemon restart.
func (m *Manager) Reconcile(ctx context.Context) {
	for _, p := range globSecrets(m.cfg.DataDir) {
		base := filepath.Base(p)
		base = strings.TrimSuffix(base, ".secret")
		id, err := uuid.Parse(base)
		if err != nil {
			continue
		}
		running, err := m.rt.PlayitAgentRunning(ctx, id)
		if err == nil && running {
			continue
		}
		secret, err := m.loadSecret(id)
		if err != nil {
			continue
		}
		if err := m.rt.StartPlayitAgent(ctx, id, secret); err != nil {
			m.log.Warn("playit agent restart failed", "server_id", id, "err", err)
		}
	}
}

// ---------------------------------------------------------------------------
// playit claim API (https://api.playit.gg)
// ---------------------------------------------------------------------------

// callSetup registers a claim code and reports its state. Responses are
// bare strings: WaitingForUserVisit | WaitingForUser | UserAccepted |
// UserRejected.
func (m *Manager) callSetup(ctx context.Context, code string) (string, error) {
	body := fmt.Sprintf(`{"code":%q,"agent_type":"self-managed","version":"quarry 0.1"}`, code)
	return m.postString(ctx, "/claim/setup", body)
}

// callExchange swaps an accepted claim code for the agent secret.
func (m *Manager) callExchange(ctx context.Context, code string) (string, error) {
	raw, err := m.postJSON(ctx, "/claim/exchange", fmt.Sprintf(`{"code":%q}`, code))
	if err != nil {
		return "", err
	}
	var res struct {
		SecretKey string `json:"secret_key"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return "", err
	}
	secret := strings.ToLower(strings.TrimSpace(res.SecretKey))
	if !secretRe.MatchString(secret) {
		return "", errors.New("playit returned an unexpected secret format")
	}
	return secret, nil
}

func (m *Manager) postString(ctx context.Context, path, body string) (string, error) {
	raw, err := m.postJSON(ctx, path, body)
	if err != nil {
		return "", err
	}
	return strings.Trim(strings.TrimSpace(string(raw)), `"`), nil
}

func (m *Manager) postJSON(ctx context.Context, path, body string) ([]byte, error) {
	url := apiBase + path
	if err := m.guard.Check(ctx, url); err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", userAgent)
	resp, err := m.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("POST %s: status %d", path, resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 1<<20))
}

// ---------------------------------------------------------------------------
// secrets
// ---------------------------------------------------------------------------

func (m *Manager) loadSecret(id uuid.UUID) (string, error) {
	raw, err := os.ReadFile(m.cfg.secretPath(id))
	if err != nil {
		return "", err
	}
	secret := strings.TrimSpace(string(raw))
	if !secretRe.MatchString(secret) {
		return "", errors.New("stored playit secret is invalid")
	}
	return secret, nil
}

func (m *Manager) storeSecret(id uuid.UUID, secret string) error {
	return os.WriteFile(m.cfg.secretPath(id), []byte(secret+"\n"), 0o600)
}

func emitConsole(emit Emitter, id uuid.UUID, line string) {
	if emit != nil {
		emit.EmitConsole(id, line)
	}
}

// globSecrets lists stored agent secret paths in the data dir.
func globSecrets(dir string) []string {
	matches, err := filepath.Glob(filepath.Join(dir, "playit-*.secret"))
	if err != nil {
		return nil
	}
	// Verify readability as a cheap sanity check.
	out := matches[:0]
	for _, p := range matches {
		if _, err := os.Stat(p); err == nil {
			out = append(out, p)
		}
	}
	return out
}
