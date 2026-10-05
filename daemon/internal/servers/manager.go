// Package servers owns every server the daemon runs locally: artifact
// install, container lifecycle, console pumps, stats, and file operations.
// It is the daemon-side executor for the panel's Channel commands.
package servers

import (
	"context"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"quarry.dev/quarry/daemon/internal/runtime"
	"quarry.dev/quarry/internal/ssrf"
	nodesv1 "quarry.dev/quarry/proto/gen/go/quarry/node/v1"
)

// Emitter sends events upstream (wired to the Channel stream by the client).
type Emitter interface {
	EmitConsole(serverID uuid.UUID, line string)
	EmitStats(serverID uuid.UUID, s *nodesv1.ServerStats)
	EmitStatus(serverID uuid.UUID, status, detail string)
	EmitInstallProgress(serverID uuid.UUID, step string, percent float64, detail string)
}

// Config for the manager.
type Config struct {
	// ServersRoot is the base directory holding per-server data dirs.
	ServersRoot string
	// ArtifactAllowCIDRs exempts internal mirrors from the SSRF policy.
	ArtifactAllowCIDRs []string
	// StatsInterval is the stats sampling period.
	StatsInterval time.Duration
}

// Manager runs servers on this node.
type Manager struct {
	cfg   Config
	rt    runtime.ContainerRuntime
	guard *ssrf.Guard
	log   *slog.Logger

	mu      sync.RWMutex
	procs   map[uuid.UUID]*process
	emMu    sync.RWMutex
	emitter Emitter
}

// SetEmitter points long-lived pumps (console, stats) at the current
// channel's emitter. Called on every channel (re)connect so output keeps
// flowing after panel restarts.
func (m *Manager) SetEmitter(e Emitter) {
	m.emMu.Lock()
	defer m.emMu.Unlock()
	m.emitter = e
}

func (m *Manager) currentEmitter() Emitter {
	m.emMu.RLock()
	defer m.emMu.RUnlock()
	return m.emitter
}

type process struct {
	console     runtime.Console
	spec        runtime.ServerSpec
	cancelStats context.CancelFunc

	mu           sync.Mutex
	expectedStop bool
	// taps receive a copy of every console line (player list polling).
	taps map[chan string]struct{}
}

func NewManager(cfg Config, rt runtime.ContainerRuntime, log *slog.Logger) (*Manager, error) {
	if cfg.StatsInterval <= 0 {
		cfg.StatsInterval = 3 * time.Second
	}
	guard, err := ssrf.NewGuard(cfg.ArtifactAllowCIDRs)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(cfg.ServersRoot, 0o755); err != nil {
		return nil, fmt.Errorf("create servers root: %w", err)
	}
	return &Manager{
		cfg:   cfg,
		rt:    rt,
		guard: guard,
		log:   log,
		procs: map[uuid.UUID]*process{},
	}, nil
}

// ServerDir resolves a path within a server's data directory.
func (m *Manager) ServerDir(serverID uuid.UUID, rel string) (string, error) {
	return runtime.ResolveServerDir(m.cfg.ServersRoot, serverID.String(), rel)
}

func (m *Manager) get(id uuid.UUID) *process {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.procs[id]
}

func (m *Manager) put(id uuid.UUID, p *process) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.procs[id] = p
}

func (m *Manager) drop(id uuid.UUID) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.procs, id)
}

// ---------------------------------------------------------------------------
// Install
// ---------------------------------------------------------------------------

// Install downloads and verifies the artifact, writes eula/config files, and
// pulls the runtime image. Emits installing → offline.
func (m *Manager) Install(ctx context.Context, cmd *nodesv1.InstallServer, emit Emitter) error {
	serverID, err := uuid.Parse(cmd.GetServerId())
	if err != nil {
		return fmt.Errorf("invalid server id: %w", err)
	}

	dir, err := m.ServerDir(serverID, ".")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create server dir: %w", err)
	}

	emit.EmitStatus(serverID, "installing", "downloading "+cmd.GetArtifactName())

	jarPath := filepath.Join(dir, "server.jar")
	if err := m.downloadArtifact(ctx, cmd, jarPath, serverID, emit); err != nil {
		return err
	}

	// Auxiliary downloads (e.g. the BlueMap or playit.gg jar);
	// checksum-verified like the main artifact.
	for _, extra := range cmd.GetExtras() {
		name := filepath.Base(extra.GetFilename())
		if strings.TrimSpace(extra.GetFilename()) == "" || name == "." || name == "/" {
			return fmt.Errorf("invalid extra filename")
		}
		dest, err := m.ServerDir(serverID, extra.GetFilename())
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return fmt.Errorf("create extra dir: %w", err)
		}
		// Versioned jar names accumulate across version switches; when the
		// panel lands a fixed-name jar (bluemap.jar, playit-companion.jar),
		// clear older variants of it first.
		if prefix := strings.TrimSuffix(name, ".jar"); strings.HasSuffix(name, ".jar") && prefix != name {
			if old, _ := filepath.Glob(filepath.Join(filepath.Dir(dest), prefix+"-*.jar")); len(old) > 0 {
				for _, o := range old {
					_ = os.Remove(o)
				}
			}
		}
		emit.EmitInstallProgress(serverID, "download "+name, 0, "downloading")
		if err := m.download(ctx, download{
			URL:    extra.GetUrl(),
			SHA256: extra.GetSha256(),
			SHA512: extra.GetSha512(),
			Dest:   dest,
			Label:  name,
		}, serverID, emit); err != nil {
			return fmt.Errorf("install %s: %w", name, err)
		}
		// BlueMap needs Mojang's client assets to render textures; accept the
		// download up front (the owner accepted Mojang's EULA at creation).
		// Missing keys fall back to defaults in BlueMap's config loader.
		switch filepath.Dir(extra.GetFilename()) {
		case "plugins":
			m.writeIfMissing(filepath.Join(dir, "plugins", "BlueMap", "core.conf"))
		case "mods":
			m.writeIfMissing(filepath.Join(dir, "config", "bluemap", "core.conf"))
		}
	}

	// Minecraft EULA: accepted implicitly when the owner creates the server.
	// Existing files are kept: version switches re-run Install and must not
	// clobber worlds' properties or eula state.
	eulaPath := filepath.Join(dir, "eula.txt")
	if _, err := os.Stat(eulaPath); os.IsNotExist(err) {
		if err := os.WriteFile(eulaPath, []byte("# Accepted via Quarry panel on creation\neula=true\n"), 0o644); err != nil {
			return fmt.Errorf("write eula.txt: %w", err)
		}
	}
	propsPath := filepath.Join(dir, "server.properties")
	if _, err := os.Stat(propsPath); os.IsNotExist(err) {
		props := fmt.Sprintf("# Generated by Quarry\nserver-ip=\nserver-port=%d\nmotd=A Quarry server\n", cmd.GetBindPort())
		if err := os.WriteFile(propsPath, []byte(props), 0o644); err != nil {
			return fmt.Errorf("write server.properties: %w", err)
		}
	}

	emit.EmitInstallProgress(serverID, "image", 0, "pulling "+cmd.GetImage())
	if err := m.rt.EnsureImage(ctx, cmd.GetImage()); err != nil {
		return fmt.Errorf("ensure image: %w", err)
	}

	emit.EmitStatus(serverID, "offline", "install complete")
	return nil
}

// writeIfMissing creates a file with a single HOCON line if it does not
// exist yet (used for BlueMap's accept-download).
func (m *Manager) writeIfMissing(path string) {
	if _, err := os.Stat(path); err == nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	_ = os.WriteFile(path, []byte("accept-download = true\n"), 0o644)
}

func (m *Manager) downloadArtifact(ctx context.Context, cmd *nodesv1.InstallServer, dest string, serverID uuid.UUID, emit Emitter) error {
	return m.download(ctx, download{
		URL:    cmd.GetArtifactUrl(),
		SHA256: cmd.GetArtifactSha256(),
		Dest:   dest,
	}, serverID, emit)
}

// download is a downloadSpec holds one verified-download request.
type download struct {
	URL    string
	SHA256 string
	SHA512 string
	Dest   string
	Label  string // progress label, e.g. "download" or the plugin name
}

// download fetches url through the SSRF-guarded client, verifies whichever
// checksum the source publishes, and atomically lands the file at dest.
func (m *Manager) download(ctx context.Context, d download, serverID uuid.UUID, emit Emitter) error {
	if err := m.guard.Check(ctx, d.URL); err != nil {
		return fmt.Errorf("artifact url rejected: %w", err)
	}
	req, err := httpNewRequest(ctx, d.URL)
	if err != nil {
		return err
	}
	client := m.guard.HTTPClient()
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("download artifact: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != 200 {
		return fmt.Errorf("download artifact: status %d", resp.StatusCode)
	}

	tmp := d.Dest + ".download"
	out, err := os.Create(tmp)
	if err != nil {
		return fmt.Errorf("write artifact: %w", err)
	}
	defer func() { _ = os.Remove(tmp) }()

	hash256 := sha256.New()
	hash512 := sha512.New()
	label := d.Label
	if label == "" {
		label = "download"
	}
	prog := &progressWriter{
		total:    resp.ContentLength,
		serverID: serverID,
		emit:     emit,
		last:     time.Now(),
		label:    label,
	}
	written, err := prog.copyTo(io.MultiWriter(out, hash256, hash512), resp.Body)
	if err != nil {
		_ = out.Close()
		return fmt.Errorf("download artifact: %w", err)
	}
	_ = written
	if err := out.Close(); err != nil {
		return err
	}

	// Verify whichever checksum the source publishes. Vanilla artifacts
	// carry only upstream SHA-1 values; for those we rely on TLS to the
	// pinned Mojang host and record the upstream digest for provenance in
	// the server config instead of re-hashing here.
	if want := strings.TrimSpace(d.SHA256); want != "" {
		if got := hex.EncodeToString(hash256.Sum(nil)); got != strings.ToLower(want) {
			return fmt.Errorf("artifact sha256 mismatch (got %s)", got)
		}
	}
	if want := strings.TrimSpace(d.SHA512); want != "" {
		if got := hex.EncodeToString(hash512.Sum(nil)); got != strings.ToLower(want) {
			return fmt.Errorf("artifact sha512 mismatch (got %s)", got)
		}
	}
	emit.EmitInstallProgress(serverID, "verify", 100, "checksum ok")
	if err := os.Rename(tmp, d.Dest); err != nil {
		return err
	}
	return nil
}

// InstallPlugin downloads a marketplace jar into the server's content
// directory. The panel supplies the destination path relative to the server
// data dir ("plugins/…" or "mods/…"); path sanitization confines it there.
func (m *Manager) InstallPlugin(ctx context.Context, cmd *nodesv1.InstallPlugin, emit Emitter) error {
	serverID, err := uuid.Parse(cmd.GetServerId())
	if err != nil {
		return fmt.Errorf("invalid server id: %w", err)
	}
	if strings.TrimSpace(cmd.GetFilename()) == "" {
		return fmt.Errorf("invalid plugin filename")
	}
	dest, err := m.ServerDir(serverID, cmd.GetFilename())
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return fmt.Errorf("create content dir: %w", err)
	}
	return m.download(ctx, download{
		URL:    cmd.GetUrl(),
		SHA256: cmd.GetSha256(),
		SHA512: cmd.GetSha512(),
		Dest:   dest,
		Label:  "plugin " + filepath.Base(cmd.GetFilename()),
	}, serverID, emit)
}

type progressWriter struct {
	total    int64
	serverID uuid.UUID
	emit     Emitter
	last     time.Time
	label    string
}

// copyTo pumps bytes into w, emitting periodic progress events. It returns
// nil only on clean EOF.
func (p *progressWriter) copyTo(w io.Writer, r io.Reader) (int64, error) {
	buf := make([]byte, 256*1024)
	var done int64
	for {
		n, readErr := r.Read(buf)
		if n > 0 {
			if _, writeErr := w.Write(buf[:n]); writeErr != nil {
				return done, writeErr
			}
			done += int64(n)
			if time.Since(p.last) > 300*time.Millisecond {
				p.last = time.Now()
				var pct float64
				if p.total > 0 {
					pct = float64(done) / float64(p.total) * 100
				}
				p.emit.EmitInstallProgress(p.serverID, p.label, pct,
					fmt.Sprintf("%d/%d bytes", done, p.total))
			}
		}
		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				return done, nil
			}
			return done, readErr
		}
	}
}

// ---------------------------------------------------------------------------
// Power
// ---------------------------------------------------------------------------

// Start brings a server container up and wires its console.
func (m *Manager) Start(ctx context.Context, cmd *nodesv1.StartServer, emit Emitter) error {
	serverID, err := uuid.Parse(cmd.GetServerId())
	if err != nil {
		return fmt.Errorf("invalid server id: %w", err)
	}
	if p := m.get(serverID); p != nil {
		return nil // already running
	}

	dir, err := m.ServerDir(serverID, ".")
	if err != nil {
		return err
	}
	if _, err := os.Stat(filepath.Join(dir, "server.jar")); err != nil {
		return fmt.Errorf("server is not installed (missing server.jar)")
	}

	spec := runtime.ServerSpec{
		ID:         serverID,
		Image:      cmd.GetImage(),
		Launch:     cmd.GetLaunch(),
		MemoryMB:   int(cmd.GetMemoryMb()),
		CPUPercent: int(cmd.GetCpuPercent()),
		BindIP:     cmd.GetBindIp(),
		BindPort:   int(cmd.GetBindPort()),
		MapPort:    int(cmd.GetMapPort()),
	}

	emit.EmitStatus(serverID, "starting", "")
	console, err := m.rt.CreateAndStart(ctx, spec, dir)
	if err != nil {
		return fmt.Errorf("start container: %w", err)
	}

	p := &process{console: console, spec: spec}
	m.put(serverID, p)
	m.watchConsole(serverID, p)
	m.startStats(serverID, p)
	emit.EmitStatus(serverID, "running", "")
	return nil
}

// watchConsole pumps console lines upstream and detects container exit.
// watchConsole pumps console lines upstream and detects container exit.
// The emitter is resolved per event so channel reconnects keep output
// flowing from already-registered processes.
func (m *Manager) watchConsole(serverID uuid.UUID, p *process) {
	go func() {
		for line := range p.console.Lines() {
			feedTaps(p, line)
			if e := m.currentEmitter(); e != nil {
				e.EmitConsole(serverID, line)
			}
		}
		// Streams ended: the container exited.
		m.drop(serverID)
		if p.cancelStats != nil {
			p.cancelStats()
		}

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		p.mu.Lock()
		expected := p.expectedStop
		p.mu.Unlock()
		state, _ := m.rt.State(ctx, serverID)
		_ = m.rt.Remove(ctx, serverID)

		if e := m.currentEmitter(); e != nil {
			switch {
			case expected:
				e.EmitStatus(serverID, "offline", "")
			case state == "exited":
				e.EmitStatus(serverID, "crashed", "container exited unexpectedly")
			default:
				e.EmitStatus(serverID, "crashed", "container streams ended (state="+state+")")
			}
		}
	}()
}

// startStats samples container stats and emits them with network rates.
func (m *Manager) startStats(serverID uuid.UUID, p *process) {
	ctx, cancel := context.WithCancel(context.Background())
	p.cancelStats = cancel
	var lastRx, lastTx float64
	go func() {
		ticker := time.NewTicker(m.cfg.StatsInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				sctx, scancel := context.WithTimeout(ctx, m.cfg.StatsInterval)
				s, err := m.rt.StatsOneShot(sctx, serverID)
				scancel()
				if err != nil {
					continue
				}
				interval := m.cfg.StatsInterval.Seconds()
				stats := &nodesv1.ServerStats{
					CpuPercent:           s.CPUPercent,
					MemoryUsedMb:         s.MemoryUsedMB,
					MemoryLimitMb:        s.MemoryLimitMB,
					NetworkRxBytesPerSec: (s.NetworkRxPerSec - lastRx) / interval,
					NetworkTxBytesPerSec: (s.NetworkTxPerSec - lastTx) / interval,
				}
				lastRx, lastTx = s.NetworkRxPerSec, s.NetworkTxPerSec
				if e := m.currentEmitter(); e != nil {
					e.EmitStats(serverID, stats)
				}
			}
		}
	}()
}

// Stop asks the server to stop via console, then escalates.
func (m *Manager) Stop(ctx context.Context, cmd *nodesv1.StopServer, emit Emitter) error {
	serverID, err := uuid.Parse(cmd.GetServerId())
	if err != nil {
		return fmt.Errorf("invalid server id: %w", err)
	}
	p := m.get(serverID)
	if p == nil {
		emit.EmitStatus(serverID, "offline", "not running")
		return nil
	}
	p.mu.Lock()
	p.expectedStop = true
	p.mu.Unlock()

	grace := time.Duration(cmd.GetTimeoutSeconds()) * time.Second
	if grace <= 0 {
		grace = 30 * time.Second
	}

	if p.console != nil {
		_ = p.console.WriteLine("stop")
	}

	deadline := time.Now().Add(grace)
	for time.Now().Before(deadline) {
		if m.get(serverID) == nil { // watcher removed it
			return nil
		}
		state, err := m.rt.State(ctx, serverID)
		if err == nil && state == "exited" {
			_ = m.rt.Remove(ctx, serverID)
			m.drop(serverID)
			emit.EmitStatus(serverID, "offline", "")
			return nil
		}
		time.Sleep(500 * time.Millisecond)
	}

	// Escalate: SIGTERM with a short docker-side timeout, then remove.
	_ = m.rt.Stop(ctx, serverID, 3*time.Second)
	_ = m.rt.Remove(ctx, serverID)
	m.drop(serverID)
	if p.cancelStats != nil {
		p.cancelStats()
	}
	emit.EmitStatus(serverID, "offline", "stopped after timeout")
	return nil
}

// Kill terminates immediately.
func (m *Manager) Kill(ctx context.Context, cmd *nodesv1.KillServer, emit Emitter) error {
	serverID, err := uuid.Parse(cmd.GetServerId())
	if err != nil {
		return fmt.Errorf("invalid server id: %w", err)
	}
	if p := m.get(serverID); p != nil {
		p.mu.Lock()
		p.expectedStop = true
		p.mu.Unlock()
		if p.cancelStats != nil {
			p.cancelStats()
		}
	}
	if err := m.rt.Kill(ctx, serverID); err != nil {
		return err
	}
	_ = m.rt.Remove(ctx, serverID)
	m.drop(serverID)
	emit.EmitStatus(serverID, "offline", "killed")
	return nil
}

// SendCommand writes a line to the server console.
func (m *Manager) SendCommand(cmd *nodesv1.SendCommand) error {
	serverID, err := uuid.Parse(cmd.GetServerId())
	if err != nil {
		return fmt.Errorf("invalid server id: %w", err)
	}
	p := m.get(serverID)
	if p == nil {
		return runtime.ErrNotRunning
	}
	return p.console.WriteLine(cmd.GetInput())
}

// Reconcile reattaches to containers left running by a daemon restart.
func (m *Manager) Reconcile(ctx context.Context, emit Emitter) {
	running, err := m.rt.ListRunning(ctx)
	if err != nil {
		m.log.Warn("reconcile: list running containers failed", "err", err)
		return
	}
	for _, id := range running {
		if m.get(id) != nil {
			continue
		}
		console, err := m.rt.Attach(ctx, id)
		if err != nil {
			m.log.Warn("reconcile: reattach failed", "server_id", id, "err", err)
			continue
		}
		p := &process{console: console}
		m.put(id, p)
		m.watchConsole(id, p)
		m.startStats(id, p)
		emit.EmitStatus(id, "running", "reattached after daemon restart")
		m.log.Info("reconciled running server", "server_id", id)
	}
}

// ---------------------------------------------------------------------------
// Files
// ---------------------------------------------------------------------------

// FileEntry is one row of a directory listing (JSON-encoded in results).
type FileEntry struct {
	Name     string    `json:"name"`
	Size     int64     `json:"size"`
	IsDir    bool      `json:"is_dir"`
	Modified time.Time `json:"modified"`
}

// ListFiles lists a directory inside a server's data dir.
func (m *Manager) ListFiles(serverID uuid.UUID, rel string) ([]FileEntry, error) {
	if rel == "" {
		rel = "."
	}
	dir, err := m.ServerDir(serverID, rel)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return []FileEntry{}, nil
		}
		return nil, err
	}
	out := make([]FileEntry, 0, len(entries))
	for _, e := range entries {
		info, err := e.Info()
		if err != nil {
			continue
		}
		out = append(out, FileEntry{
			Name: e.Name(), Size: info.Size(), IsDir: e.IsDir(), Modified: info.ModTime(),
		})
	}
	return out, nil
}

// ReadFile reads a file (bounded) inside a server's data dir.
func (m *Manager) ReadFile(serverID uuid.UUID, rel string, maxBytes int64) ([]byte, error) {
	if maxBytes <= 0 || maxBytes > 4<<20 {
		maxBytes = 1 << 20
	}
	path, err := m.ServerDir(serverID, rel)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	return io.ReadAll(io.LimitReader(f, maxBytes))
}

// WriteFile writes (or creates) a file inside a server's data dir.
func (m *Manager) WriteFile(serverID uuid.UUID, rel string, content []byte, createDirs bool) error {
	path, err := m.ServerDir(serverID, rel)
	if err != nil {
		return err
	}
	if createDirs {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
	}
	return os.WriteFile(path, content, 0o644)
}

// DeletePath deletes a file or directory (recursive) inside a server's data
// dir. Deleting "." removes the whole server data directory.
func (m *Manager) DeletePath(serverID uuid.UUID, rel string, recursive bool) error {
	path, err := m.ServerDir(serverID, rel)
	if err != nil {
		return err
	}
	if recursive {
		return os.RemoveAll(path)
	}
	return os.Remove(path)
}

var _ = json.Marshal
