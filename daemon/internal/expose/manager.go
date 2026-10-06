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
	// playit claim codes are 5 random bytes, hex-encoded (upstream format).
	claimCodeRe = regexp.MustCompile(`^[0-9a-f]{10}$`)
	// agent secrets are hex strings (validated by the agent itself).
	secretRe = regexp.MustCompile(`^[0-9a-f]{32,128}$`)
)

// AgentRuntime manages tunnel containers on this node (playit agent and
// frpc client).
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

	// frpc tunnel client containers
	EnsureFrpcImage(ctx context.Context) error
	StartFrpc(ctx context.Context, id uuid.UUID, configPath string) error
	StopFrpc(ctx context.Context, id uuid.UUID) error
	FrpcRunning(ctx context.Context, id uuid.UUID) (bool, error)
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
	// ServersRoot is the server data root. It must be the host-visible
	// path (the compose contract) so frpc configs written here can be
	// bind-mounted into sibling containers.
	ServersRoot string
}

// Status is the JSON-encoded command result.
type Status struct {
	Active   bool   `json:"active"`
	ClaimURL string `json:"claim_url,omitempty"`
	// State carries playit's claim status when the claim is not done:
	// WaitingForUserVisit (open the link) or WaitingForUser (approve it).
	State string `json:"state,omitempty"`
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

// Pending claim codes survive daemon restarts via a small file, so an open
// claim in the browser is not invalidated by a deploy.
func (m *Manager) pendingCodePath(id uuid.UUID) string {
	return m.cfg.DataDir + "/playit-" + id.String() + ".pending"
}

func (m *Manager) pendingCode(id uuid.UUID) (string, bool) {
	m.mu.Lock()
	code, ok := m.pending[id]
	m.mu.Unlock()
	if ok {
		return code, true
	}
	raw, err := os.ReadFile(m.pendingCodePath(id))
	if err != nil {
		return "", false
	}
	code = strings.TrimSpace(string(raw))
	if !claimCodeRe.MatchString(code) {
		return "", false
	}
	m.mu.Lock()
	m.pending[id] = code
	m.mu.Unlock()
	return code, true
}

func (m *Manager) storePendingCode(id uuid.UUID, code string) {
	m.mu.Lock()
	m.pending[id] = code
	m.mu.Unlock()
	_ = os.WriteFile(m.pendingCodePath(id), []byte(code+"\n"), 0o600)
}

func (m *Manager) clearPendingCode(id uuid.UUID) {
	m.mu.Lock()
	delete(m.pending, id)
	m.mu.Unlock()
	_ = os.Remove(m.pendingCodePath(id))
}

// Start begins setup: with a secret on file it just ensures the agent
// runs; otherwise it generates a claim code and returns the claim URL.
// An already-pending code is reused (its URL is stable), so clicking
// setup twice does not invalidate an open claim.
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

	if code, pending := m.pendingCode(id); pending {
		emitConsole(emit, id, "playit: claim already open at "+claimURL+code)
		return &Status{ClaimURL: claimURL + code}, nil
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
	m.storePendingCode(id, code)
	url := claimURL + code
	emitConsole(emit, id, "playit: claim the tunnel at "+url)
	return &Status{ClaimURL: url}, nil
}

// Claim finishes setup after the user's browser claim: exchanges the code
// for the agent secret, stores it and starts the container. The returned
// State carries playit's own claim status so the UI can guide the user
// when the claim has not gone through yet.
func (m *Manager) Claim(ctx context.Context, id uuid.UUID, emit Emitter) (*Status, error) {
	if m.hasSecret(id) {
		secret, err := m.loadSecret(id)
		if err != nil {
			return nil, err
		}
		if err := m.rt.StartPlayitAgent(ctx, id, secret); err != nil {
			return nil, err
		}
		m.streamLogs(ctx, id, emit)
		return &Status{Active: true, State: "active"}, nil
	}
	code, pending := m.pendingCode(id)
	if !pending {
		return nil, errors.New("no pending tunnel setup; start it first")
	}

	ctxWait, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	// Poll setup until the user accepts (or the deadline passes). Transient
	// network errors are retried within the window, not fatal.
	var lastStatus string
	for {
		status, err := m.callSetup(ctxWait, code)
		if err != nil {
			if ctxWait.Err() != nil {
				return nil, err // window over; surface the network failure
			}
			select {
			case <-ctxWait.Done():
				return nil, err
			case <-time.After(2 * time.Second):
				continue
			}
		}
		lastStatus = status
		if status == "UserAccepted" {
			break
		}
		if status == "UserRejected" {
			return nil, errors.New("claim rejected in browser")
		}
		select {
		case <-ctxWait.Done():
			return &Status{ClaimURL: claimURL + code, State: lastStatus}, nil // still unclaimed
		case <-time.After(2 * time.Second):
		}
	}

	// Exchange right after acceptance; the API can briefly answer with a
	// failure while the claim settles, so retry a few times.
	var secret string
	var lastErr error
	for attempt := 0; attempt < 5; attempt++ {
		secret, lastErr = m.callExchange(ctxWait, code)
		if lastErr == nil {
			break
		}
		select {
		case <-ctxWait.Done():
			return nil, fmt.Errorf("exchange claim: %w", lastErr)
		case <-time.After(2 * time.Second):
		}
	}
	if lastErr != nil {
		return nil, fmt.Errorf("exchange claim: %w", lastErr)
	}
	if err := m.storeSecret(id, secret); err != nil {
		return nil, err
	}
	m.clearPendingCode(id)

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

// Stop removes the agent container and forgets the server's secret and any
// open claim.
func (m *Manager) Stop(ctx context.Context, id uuid.UUID, emit Emitter) error {
	_ = m.rt.StopPlayitAgent(ctx, id)
	_ = os.Remove(m.cfg.secretPath(id))
	m.clearPendingCode(id)
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

// callSetup registers a claim code and reports its state. The API wraps
// responses as {"status":"success","data":"WaitingForUserVisit"|…}.
func (m *Manager) callSetup(ctx context.Context, code string) (string, error) {
	body := fmt.Sprintf(`{"code":%q,"agent_type":"self-managed","version":"quarry 0.1"}`, code)
	data, err := m.postEnvelope(ctx, "/claim/setup", body)
	if err != nil {
		return "", err
	}
	var status string
	if err := json.Unmarshal(data, &status); err != nil {
		return "", fmt.Errorf("unexpected claim setup payload: %s", truncate(string(data)))
	}
	return status, nil
}

// callExchange swaps an accepted claim code for the agent secret. The
// envelope's data holds {"secret_key":"<hex>"}.
func (m *Manager) callExchange(ctx context.Context, code string) (string, error) {
	data, err := m.postEnvelope(ctx, "/claim/exchange", fmt.Sprintf(`{"code":%q}`, code))
	if err != nil {
		return "", err
	}
	var res struct {
		SecretKey string `json:"secret_key"`
	}
	if err := json.Unmarshal(data, &res); err != nil {
		return "", fmt.Errorf("unexpected claim exchange payload: %s", truncate(string(data)))
	}
	secret := strings.ToLower(strings.TrimSpace(res.SecretKey))
	if !secretRe.MatchString(secret) {
		return "", errors.New("playit returned an unexpected secret format")
	}
	return secret, nil
}

// apiEnvelope is playit's JSON-RPC-style response wrapper.
type apiEnvelope struct {
	Status string          `json:"status"`
	Data   json.RawMessage `json:"data"`
	Error  json.RawMessage `json:"error"`
}

// postEnvelope POSTs and unwraps {"status":"success","data":…} envelopes.
func (m *Manager) postEnvelope(ctx context.Context, path, body string) (json.RawMessage, error) {
	raw, err := m.postJSON(ctx, path, body)
	if err != nil {
		return nil, err
	}
	var env apiEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, fmt.Errorf("malformed reply from %s: %s", path, truncate(string(raw)))
	}
	if env.Status != "success" {
		return nil, fmt.Errorf("%s: %s: %s", path, env.Status, truncate(string(env.Error)))
	}
	return env.Data, nil
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
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		// The API reports claim errors (NotSetup, NotAccepted, …) as status
		// codes with a short body; surface it for diagnosis.
		return nil, fmt.Errorf("POST %s: status %d: %s", path, resp.StatusCode, truncate(string(raw)))
	}
	return raw, nil
}

// errTruncLen bounds API error bodies surfaced in messages.
const errTruncLen = 120

func truncate(s string) string {
	if len(s) <= errTruncLen {
		return s
	}
	return s[:errTruncLen] + "…"
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
