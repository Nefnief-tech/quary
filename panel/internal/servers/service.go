// Package servers orchestrates the panel side of server management: the
// create flow (resolve artifact, pick allocation, dispatch install), power
// actions over the node channel, and the daemon event handling that keeps
// server statuses truthful.
package servers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"path"
	"regexp"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"quarry.dev/quarry/panel/internal/consolelog"
	"quarry.dev/quarry/panel/internal/db/gen"
	"quarry.dev/quarry/panel/internal/hub"
	"quarry.dev/quarry/panel/internal/lifecycle"
	"quarry.dev/quarry/panel/internal/marketplace"
	"quarry.dev/quarry/panel/internal/nodes"
	"quarry.dev/quarry/panel/internal/runners"
	nodesv1 "quarry.dev/quarry/proto/gen/go/quarry/node/v1"
)

const defaultStopGraceSeconds = 30

// mapProject is BlueMap's Modrinth slug; it is preinstalled on every
// Paper/Fabric server the loader and Java version can run it on.
const mapProject = "bluemap"

var (
	ErrNodeOffline   = nodes.ErrNodeOffline
	ErrNotPermitted  = errors.New("not permitted")
	ErrBadState      = errors.New("server is not in a state that allows this action")
	ErrNoAllocations = errors.New("node has no free allocation; add an allocation range first")
)

// Config is the runtime install snapshot persisted in servers.config.
type Config struct {
	Image    string   `json:"image"`
	Launch   []string `json:"launch"`
	BindIP   string   `json:"bind_ip"`
	BindPort int32    `json:"bind_port"`
	Artifact struct {
		URL    string `json:"url"`
		SHA256 string `json:"sha256,omitempty"`
		SHA1   string `json:"sha1,omitempty"`
	} `json:"artifact"`
	// Map describes the preinstalled BlueMap webserver binding; nil when
	// the runner or MC version cannot host one.
	Map *MapConfig `json:"map,omitempty"`
	// Expose tracks the playit.gg tunnel plugin state observed on the
	// console: the claim link and, once claimed, the public address.
	Expose *ExposeConfig `json:"expose,omitempty"`
}

// MapConfig is the second allocation the map webserver publishes on.
type MapConfig struct {
	IP      string `json:"ip"`
	Port    int32  `json:"port"`
	Version string `json:"version,omitempty"`
}

// ExposeConfig is the playit.gg companion plugin state.
type ExposeConfig struct {
	Installed bool   `json:"installed"`
	ClaimURL  string `json:"claim_url,omitempty"`
	Address   string `json:"address,omitempty"`
}

// Fixed extra filenames: the daemon removes older versioned variants of a
// fixed-name jar, so version switches replace instead of accumulate.
const bluemapFilename = "bluemap.jar"
const playitFilename = "playit-companion.jar"

// playitProject is the Playit Companion Modrinth slug (official playit.gg
// plugin; prints a claim URL on the console after install).
const playitProject = "playit-companion"

// PlayitProject and PlayitFilename are used by the expose install handler.
const (
	PlayitProject  = playitProject
	PlayitFilename = playitFilename
)

// MarkExposeInstalled records that the playit companion plugin landed.
func (s *Service) MarkExposeInstalled(ctx context.Context, serverID uuid.UUID) error {
	sv, err := s.q.GetServer(ctx, serverID)
	if err != nil {
		return err
	}
	var cfg Config
	if err := json.Unmarshal(sv.Config, &cfg); err != nil {
		cfg = Config{}
	}
	if cfg.Expose == nil {
		cfg.Expose = &ExposeConfig{}
	}
	cfg.Expose.Installed = true
	return s.q.UpdateServerConfig(ctx, gen.UpdateServerConfigParams{
		ID: serverID, Config: mustJSON(cfg),
	})
}

// Console patterns the panel watches for (playit.gg integration).
var claimURLRe = regexp.MustCompile(`https://playit\.gg/claim/[A-Za-z0-9]+`)
var playitAddrRe = regexp.MustCompile(`\b([a-z0-9][a-z0-9-]*\.playit\.gg)\b`)

// mapRunner maps a runner to its Modrinth loader and content dir for the
// preinstalled map; vanilla has no plugin loader.
func mapRunner(runnerID string) (loader string, ok bool) {
	switch runnerID {
	case "paper":
		return "paper", true
	case "fabric":
		return "fabric", true
	default:
		return "", false
	}
}

type Service struct {
	pool      *pgxpool.Pool
	q         *gen.Queries
	hub       *hub.Hub
	channels  *nodes.ChannelManager
	catalog   *runners.Catalog
	modrinth  *marketplace.Modrinth
	persister *consolelog.Persister
	log       *slog.Logger

	mu              sync.Mutex
	pendingRestarts map[uuid.UUID]bool
}

func NewService(pool *pgxpool.Pool, q *gen.Queries, hb *hub.Hub, channels *nodes.ChannelManager, catalog *runners.Catalog, modrinth *marketplace.Modrinth, persister *consolelog.Persister, log *slog.Logger) *Service {
	return &Service{
		pool:            pool,
		q:               q,
		hub:             hb,
		channels:        channels,
		catalog:         catalog,
		modrinth:        modrinth,
		persister:       persister,
		log:             log,
		pendingRestarts: map[uuid.UUID]bool{},
	}
}

// CreateRequest is the create-server input.
type CreateRequest struct {
	Name       string    `json:"name"`
	NodeID     uuid.UUID `json:"node_id"`
	Runner     string    `json:"runner"`
	MCVersion  string    `json:"mc_version"`
	MemoryMB   int32     `json:"memory_mb"`
	CPUPercent int32     `json:"cpu_percent"`
	// WithMap preinstalls the BlueMap plugin where supported. Defaults to
	// true; explicit false skips it (and its second port allocation).
	WithMap *bool `json:"with_map"`
}

// Create resolves the artifact, claims a free allocation, persists the
// server (status installing) and dispatches the install to the node. An
// unset NodeID means "any online node with a free allocation".
func (s *Service) Create(ctx context.Context, ownerID uuid.UUID, req CreateRequest) (gen.Server, error) {
	if req.Name == "" || len(req.Name) > 64 {
		return gen.Server{}, fmt.Errorf("name must be 1-64 characters")
	}
	if req.MemoryMB == 0 {
		req.MemoryMB = 2048
	}
	if req.CPUPercent == 0 {
		req.CPUPercent = 200
	}
	if req.CPUPercent < 50 || req.CPUPercent > 3200 {
		return gen.Server{}, fmt.Errorf("cpu percent must be between 50 and 3200")
	}
	if req.WithMap == nil {
		wantMap := true
		req.WithMap = &wantMap
	}
	if req.NodeID == uuid.Nil {
		node, err := s.q.PickNodeWithFreeAllocation(ctx)
		if err != nil {
			return gen.Server{}, errors.New("no online node with a free allocation; create a node and add allocations first")
		}
		req.NodeID = node.ID
	} else if _, err := s.q.GetNode(ctx, req.NodeID); err != nil {
		return gen.Server{}, errors.New("node not found")
	}

	runner, artifact, err := s.catalog.Resolve(ctx, req.Runner, req.MCVersion)
	if err != nil {
		return gen.Server{}, err
	}
	java := runner.JavaVersion(req.MCVersion)

	// BlueMap is preinstalled where it can run: plugin loaders on Java 21+.
	// Resolution is best-effort: a missing BlueMap build for this MC version
	// creates the server without a map instead of failing the create.
	mapLoader, mapOK := mapRunner(req.Runner)
	if mapOK && java < 21 {
		mapOK = false
	}
	if !*req.WithMap {
		mapOK = false
	}
	var mapDL *marketplace.ResolvedDownload
	if mapOK {
		dl, err := s.modrinth.Resolve(ctx, mapProject, req.MCVersion, mapLoader)
		if err != nil {
			s.log.Warn("map resolution failed; creating server without a map",
				"runner", req.Runner, "mc_version", req.MCVersion, "err", err)
		} else {
			mapDL = &dl
		}
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return gen.Server{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	qtx := s.q.WithTx(tx)

	alloc, err := qtx.PickFreeAllocation(ctx, req.NodeID)
	if err != nil {
		return gen.Server{}, ErrNoAllocations
	}

	serverID := uuid.New()

	// The map needs its own port on the node. Without one, create without a
	// map rather than failing the server. PickFreeAllocationExcluding is
	// required because SKIP LOCKED does not skip rows this transaction
	// itself already locked.
	var mapAlloc *gen.Allocation
	if mapDL != nil {
		a, err := qtx.PickFreeAllocationExcluding(ctx, gen.PickFreeAllocationExcludingParams{
			NodeID: req.NodeID, ID: alloc.ID,
		})
		if err != nil {
			s.log.Warn("no free allocation for the map port; creating server without a map",
				"server_id", serverID)
			mapDL = nil
		} else {
			mapAlloc = &a
		}
	}

	cfg := Config{
		Image:    runners.ImageForJava(java),
		Launch:   runner.Launch(int(req.MemoryMB)),
		BindIP:   alloc.Ip,
		BindPort: alloc.Port,
	}
	cfg.Artifact.URL = artifact.URL
	cfg.Artifact.SHA256 = artifact.SHA256
	cfg.Artifact.SHA1 = artifact.SHA1
	if mapDL != nil && mapAlloc != nil {
		cfg.Map = &MapConfig{IP: mapAlloc.Ip, Port: mapAlloc.Port, Version: mapDL.Version}
	}
	cfgRaw, _ := json.Marshal(cfg)

	var mapAllocID *uuid.UUID
	if mapAlloc != nil {
		id := mapAlloc.ID
		mapAllocID = &id
	}
	server, err := qtx.CreateServer(ctx, gen.CreateServerParams{
		ID:              serverID,
		Name:            req.Name,
		OwnerID:         ownerID,
		NodeID:          req.NodeID,
		AllocationID:    alloc.ID,
		MapAllocationID: mapAllocID,
		Runner:          req.Runner,
		McVersion:       req.MCVersion,
		Status:          string(lifecycle.Installing),
		MemoryMb:        req.MemoryMB,
		DiskMb:          10240,
		CpuPercent:      req.CPUPercent,
		Config:          cfgRaw,
	})
	if err != nil {
		return gen.Server{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return gen.Server{}, err
	}

	// Install runs asynchronously on the node; progress streams as events.
	var extras []extraDownload
	if mapDL != nil {
		extras = append(extras, extraDownload{
			loader:     mapLoader,
			dl:         *mapDL,
			filename:   bluemapFilename,
			acceptNote: true,
		})
	}
	go s.dispatchInstall(serverID, req.NodeID, req.Runner, req.MCVersion, cfg, req.MemoryMB, artifact, extras)

	return server, nil
}

// extraDownload is an auxiliary artifact (BlueMap, playit.gg companion)
// installed alongside the server jar.
type extraDownload struct {
	loader string
	dl     marketplace.ResolvedDownload
	// filename is fixed (not version-named) so reinstalls replace the jar.
	filename string
	// acceptNote writes BlueMap's accept-download=true config.
	acceptNote bool
}

// dispatchInstall sends the InstallServer command and routes failures into
// the status machine. It owns the goroutine lifecycle for create/switch.
func (s *Service) dispatchInstall(serverID, nodeID uuid.UUID, runnerID, mcVersion string, cfg Config, memoryMB int32, artifact runners.Artifact, extras []extraDownload) {
	go func() {
		bg, cancel := context.WithTimeout(context.Background(), 45*time.Minute)
		defer cancel()
		install := &nodesv1.InstallServer{
			ServerId:       serverID.String(),
			Runner:         runnerID,
			McVersion:      mcVersion,
			ArtifactUrl:    artifact.URL,
			ArtifactSha256: artifact.SHA256,
			ArtifactName:   artifact.Name,
			Image:          cfg.Image,
			Launch:         cfg.Launch,
			MemoryMb:       memoryMB,
			BindIp:         cfg.BindIP,
			BindPort:       cfg.BindPort,
		}
		for _, ex := range extras {
			install.Extras = append(install.Extras, &nodesv1.ExtraFile{
				Url:      ex.dl.URL,
				Sha512:   ex.dl.SHA512,
				Filename: path.Join(ex.loader, ex.filename),
			})
		}
		result, err := s.channels.Send(bg, nodeID, &nodesv1.Command{
			Action: &nodesv1.Command_InstallServer{InstallServer: install},
		})
		if err != nil || !result.GetOk() {
			var detail string
			switch {
			case err != nil:
				detail = err.Error()
			default:
				detail = result.GetError()
			}
			s.log.Error("server install failed", "server_id", serverID, "err", detail)
			// Route through handleStatus so the DB row transitions, not just
			// the realtime subscribers.
			s.handleStatus(serverID, &nodesv1.StatusChange{Status: "crashed", Detail: detail})
		}
	}()
}

// SwitchVersionRequest is the settings-tab version-switch input.
type SwitchVersionRequest struct {
	Runner    string `json:"runner"`
	MCVersion string `json:"mc_version"`
}

// SwitchVersion replaces the server jar (and its runner/launch config)
// while keeping worlds, configs and ports. The server must be stopped; it
// re-enters installing and can be started again once the download lands.
func (s *Service) SwitchVersion(ctx context.Context, sv gen.Server, req SwitchVersionRequest) (gen.Server, []string, error) {
	var notes []string
	status := lifecycle.Status(sv.Status)
	if status != lifecycle.Offline && status != lifecycle.Crashed {
		return sv, nil, fmt.Errorf("%w: stop the server before switching version", ErrBadState)
	}
	if req.Runner == "" {
		req.Runner = sv.Runner
	}
	if req.MCVersion == "" {
		req.MCVersion = sv.McVersion
	}
	if req.Runner == sv.Runner && req.MCVersion == sv.McVersion {
		return sv, nil, fmt.Errorf("server already runs %s %s", sv.Runner, sv.McVersion)
	}
	if !s.channels.Connected(sv.NodeID) {
		return sv, nil, ErrNodeOffline
	}
	runner, artifact, err := s.catalog.Resolve(ctx, req.Runner, req.MCVersion)
	if err != nil {
		return sv, nil, err
	}
	java := runner.JavaVersion(req.MCVersion)

	var cfg Config
	if err := json.Unmarshal(sv.Config, &cfg); err != nil {
		return sv, nil, fmt.Errorf("server config unreadable")
	}
	cfg.Image = runners.ImageForJava(java)
	cfg.Launch = runner.Launch(int(sv.MemoryMb))

	// Keep the map working on the new version if we can; otherwise drop it.
	var extras []extraDownload
	if sv.MapAllocationID != nil {
		mapLoader, mapOK := mapRunner(req.Runner)
		if mapOK && java < 21 {
			mapOK = false
		}
		if mapOK {
			if dl, err := s.modrinth.Resolve(ctx, mapProject, req.MCVersion, mapLoader); err == nil {
				cfg.Map.Version = dl.Version
				extras = append(extras, extraDownload{
					loader: mapLoader, dl: dl, filename: bluemapFilename, acceptNote: true,
				})
			} else {
				cfg.Map = nil
				_ = s.q.SetServerMapAllocation(ctx, gen.SetServerMapAllocationParams{ID: sv.ID, MapAllocationID: nil})
				notes = append(notes, "map removed: BlueMap has no build for "+req.MCVersion)
			}
		} else {
			cfg.Map = nil
			_ = s.q.SetServerMapAllocation(ctx, gen.SetServerMapAllocationParams{ID: sv.ID, MapAllocationID: nil})
			notes = append(notes, "map removed: the new runner cannot host BlueMap")
		}
	}
	go s.dispatchInstall(sv.ID, sv.NodeID, req.Runner, req.MCVersion, cfg, sv.MemoryMb, artifact, extras)

	if err := s.q.SetServerVersion(ctx, gen.SetServerVersionParams{
		ID: sv.ID, Runner: req.Runner, McVersion: req.MCVersion, Config: mustJSON(cfg),
	}); err != nil {
		return sv, nil, err
	}
	// installing is normally a create-time state; a switch re-enters it
	// directly from a stopped state by design.
	if err := s.q.SetServerStatus(ctx, gen.SetServerStatusParams{
		ID: sv.ID, Status: string(lifecycle.Installing),
	}); err != nil {
		return sv, nil, err
	}
	s.hub.Publish(hub.Event{Type: hub.EventStatus, ServerID: sv.ID, Status: string(lifecycle.Installing),
		Detail: "switching to " + req.Runner + " " + req.MCVersion})
	notes = append(notes, fmt.Sprintf("switching to %s %s (Java %d) — start once installed", req.Runner, req.MCVersion, java))

	updated, err := s.q.GetServer(ctx, sv.ID)
	if err != nil {
		return sv, notes, err
	}
	return updated, notes, nil
}

// watchExpose tracks the playit.gg companion plugin's console output: the
// claim link after install and the public tunnel address once claimed.
func (s *Service) watchExpose(serverID uuid.UUID, line string) {
	var claim, addr string
	if m := claimURLRe.FindString(line); m != "" {
		claim = m
	}
	if m := playitAddrRe.FindStringSubmatch(line); m != nil {
		addr = m[1]
	}
	if claim == "" && addr == "" {
		return
	}
	sv, err := s.q.GetServer(context.Background(), serverID)
	if err != nil {
		return
	}
	var cfg Config
	if err := json.Unmarshal(sv.Config, &cfg); err != nil || cfg.Expose == nil {
		// No expose record (plugin installed via files, or pre-existing):
		// start tracking from what the console shows.
		cfg.Expose = &ExposeConfig{Installed: true}
	}
	changed := false
	if claim != "" && cfg.Expose.ClaimURL != claim {
		cfg.Expose.ClaimURL = claim
		changed = true
	}
	if addr != "" && cfg.Expose.Address != addr {
		cfg.Expose.Address = addr
		changed = true
	}
	if !changed {
		return
	}
	if err := s.q.UpdateServerConfig(context.Background(), gen.UpdateServerConfigParams{
		ID: serverID, Config: mustJSON(cfg),
	}); err != nil {
		s.log.Warn("expose state persist failed", "server_id", serverID, "err", err)
	}
	s.hub.Publish(hub.Event{Type: hub.EventStatus, ServerID: serverID, Status: sv.Status,
		Detail: "public address: " + cfg.Expose.Address})
}

func mustJSON(v any) []byte {
	raw, _ := json.Marshal(v)
	return raw
}

// Power performs a validated power action. Restart is a composite: stop,
// then start once the daemon reports offline.
func (s *Service) Power(ctx context.Context, server gen.Server, action lifecycle.PowerAction) error {
	current := lifecycle.Status(server.Status)
	if !lifecycle.ActionAllowed(action, current) {
		return fmt.Errorf("%w: cannot %s while %s", ErrBadState, action, current)
	}

	nodeID := server.NodeID
	if !s.channels.Connected(nodeID) {
		return ErrNodeOffline
	}

	switch action {
	case lifecycle.ActionStart:
		if err := s.setStatus(server.ID, current, lifecycle.Starting); err != nil {
			return err
		}
		_, err := s.channels.Send(ctx, nodeID, s.startCommand(server))
		return err
	case lifecycle.ActionStop:
		if err := s.setStatus(server.ID, current, lifecycle.Stopping); err != nil {
			return err
		}
		_, err := s.channels.Send(ctx, nodeID, &nodesv1.Command{
			Action: &nodesv1.Command_StopServer{StopServer: &nodesv1.StopServer{
				ServerId: server.ID.String(), TimeoutSeconds: defaultStopGraceSeconds,
			}},
		})
		return err
	case lifecycle.ActionKill:
		if err := s.setStatus(server.ID, current, lifecycle.Offline); err != nil {
			return err
		}
		_, err := s.channels.Send(ctx, nodeID, &nodesv1.Command{
			Action: &nodesv1.Command_KillServer{KillServer: &nodesv1.KillServer{ServerId: server.ID.String()}},
		})
		return err
	case lifecycle.ActionRestart:
		s.mu.Lock()
		s.pendingRestarts[server.ID] = true
		s.mu.Unlock()
		if err := s.setStatus(server.ID, current, lifecycle.Stopping); err != nil {
			return err
		}
		_, err := s.channels.Send(ctx, nodeID, &nodesv1.Command{
			Action: &nodesv1.Command_StopServer{StopServer: &nodesv1.StopServer{
				ServerId: server.ID.String(), TimeoutSeconds: defaultStopGraceSeconds,
			}},
		})
		return err
	}
	return fmt.Errorf("unknown action")
}

func (s *Service) startCommand(server gen.Server) *nodesv1.Command {
	var cfg Config
	_ = json.Unmarshal(server.Config, &cfg)
	start := &nodesv1.StartServer{
		ServerId:   server.ID.String(),
		Image:      cfg.Image,
		Launch:     cfg.Launch,
		MemoryMb:   server.MemoryMb,
		CpuPercent: server.CpuPercent,
		BindIp:     cfg.BindIP,
		BindPort:   cfg.BindPort,
	}
	if cfg.Map != nil {
		start.MapBindIp = cfg.Map.IP
		start.MapPort = cfg.Map.Port
	}
	return &nodesv1.Command{
		Action: &nodesv1.Command_StartServer{StartServer: start},
	}
}

func (s *Service) setStatus(serverID uuid.UUID, from, to lifecycle.Status) error {
	if err := lifecycle.Transition(from, to); err != nil {
		return err
	}
	return s.q.SetServerStatus(context.Background(), gen.SetServerStatusParams{
		ID: serverID, Status: string(to),
	})
}

// HandleEvent consumes daemon events: status transitions are validated and
// persisted; console/stats/install events fan out to the hub.
func (s *Service) HandleEvent(ev *nodesv1.Event) {
	serverID, err := uuid.Parse(ev.GetServerId())
	if err != nil {
		return
	}
	switch payload := ev.GetPayload().(type) {
	case *nodesv1.Event_Console:
		line := payload.Console.GetLine()
		if s.persister != nil {
			s.persister.Record(serverID, time.UnixMilli(payload.Console.GetAtUnixMilli()), line)
		}
		s.watchExpose(serverID, line)
		s.hub.Publish(hub.Event{Type: hub.EventConsole, ServerID: serverID, Console: payload.Console})
	case *nodesv1.Event_Stats:
		s.hub.Publish(hub.Event{Type: hub.EventStats, ServerID: serverID, Stats: payload.Stats})
	case *nodesv1.Event_InstallProgress:
		s.hub.Publish(hub.Event{Type: hub.EventInstall, ServerID: serverID, Install: payload.InstallProgress})
	case *nodesv1.Event_Status:
		s.handleStatus(serverID, payload.Status)
	}
}

func (s *Service) handleStatus(serverID uuid.UUID, change *nodesv1.StatusChange) {
	to := lifecycle.Status(change.GetStatus())
	if !lifecycle.Valid(string(to)) {
		return
	}
	server, err := s.q.GetServer(context.Background(), serverID)
	if err != nil {
		return
	}
	from := lifecycle.Status(server.Status)
	if from == to {
		return
	}
	if err := lifecycle.Transition(from, to); err != nil {
		// Daemon observations must obey the same graph; log and accept the
		// daemon's truth when it reports a state the graph allows from
		// anywhere (crashed/offline are reachable recovery states).
		s.log.Warn("ignoring out-of-graph status", "server_id", serverID, "from", from, "to", to, "detail", change.GetDetail())
		if to != lifecycle.Crashed && to != lifecycle.Offline {
			return
		}
	}
	if err := s.q.SetServerStatus(context.Background(), gen.SetServerStatusParams{
		ID: serverID, Status: string(to),
	}); err != nil {
		s.log.Error("persist status failed", "server_id", serverID, "err", err)
	}
	s.hub.Publish(hub.Event{Type: hub.EventStatus, ServerID: serverID, Status: string(to), Detail: change.GetDetail()})

	// Composite restarts kick off once the stop settles.
	if to == lifecycle.Offline {
		s.mu.Lock()
		restart := s.pendingRestarts[serverID]
		delete(s.pendingRestarts, serverID)
		s.mu.Unlock()
		if restart {
			if err := s.q.SetServerStatus(context.Background(), gen.SetServerStatusParams{
				ID: serverID, Status: string(lifecycle.Starting),
			}); err == nil {
				if _, err := s.channels.Send(context.Background(), server.NodeID, s.startCommand(server)); err != nil {
					s.log.Error("restart dispatch failed", "server_id", serverID, "err", err)
					_ = s.q.SetServerStatus(context.Background(), gen.SetServerStatusParams{
						ID: serverID, Status: string(lifecycle.Crashed),
					})
				}
			}
		}
	}
}

// UpdateRequest is the settings-tab input. Zero values leave fields alone.
type UpdateRequest struct {
	Name       string     `json:"name"`
	MemoryMB   int32      `json:"memory_mb"`
	CPUPercent int32      `json:"cpu_percent"`
	Allocation *uuid.UUID `json:"allocation_id"` // pointer: explicit null-free change
}

// Update applies visual-settings changes. Resource changes take effect on
// the next start; a port move swaps the allocation atomically.
func (s *Service) Update(ctx context.Context, sv gen.Server, req UpdateRequest) (gen.Server, []string, error) {
	var notes []string

	if req.Name != "" && req.Name != sv.Name {
		if len(req.Name) > 64 {
			return sv, nil, fmt.Errorf("name must be 1-64 characters")
		}
		if err := s.q.RenameServer(ctx, gen.RenameServerParams{ID: sv.ID, Name: req.Name}); err != nil {
			return sv, nil, err
		}
		notes = append(notes, "renamed")
		sv.Name = req.Name
	}

	if req.MemoryMB != 0 || req.CPUPercent != 0 {
		mem := sv.MemoryMb
		cpu := sv.CpuPercent
		if req.MemoryMB != 0 {
			if req.MemoryMB < 512 {
				return sv, nil, fmt.Errorf("memory must be at least 512 MiB")
			}
			mem = req.MemoryMB
		}
		if req.CPUPercent != 0 {
			if req.CPUPercent < 50 || req.CPUPercent > 3200 {
				return sv, nil, fmt.Errorf("cpu percent must be between 50 and 3200")
			}
			cpu = req.CPUPercent
		}
		if err := s.q.SetServerResources(ctx, gen.SetServerResourcesParams{
			ID: sv.ID, MemoryMb: mem, CpuPercent: cpu,
		}); err != nil {
			return sv, nil, err
		}
		// The -Xmx launch flags are baked into the install config; regenerate
		// them so the new memory applies on the next start.
		if mem != sv.MemoryMb {
			runner, ok := s.catalog.Get(sv.Runner)
			if ok {
				var cfg Config
				if err := json.Unmarshal(sv.Config, &cfg); err == nil {
					cfg.Launch = runner.Launch(int(mem))
					cfgRaw, _ := json.Marshal(cfg)
					if err := s.q.UpdateServerConfig(ctx, gen.UpdateServerConfigParams{
						ID: sv.ID, Config: cfgRaw,
					}); err != nil {
						return sv, nil, err
					}
				}
			}
			notes = append(notes, "resources updated — applies after restart")
		} else {
			notes = append(notes, "resources updated")
		}
		sv.MemoryMb = mem
		sv.CpuPercent = cpu
	}

	if req.Allocation != nil && *req.Allocation != sv.AllocationID {
		if lifecycle.Status(sv.Status) == lifecycle.Running || lifecycle.Status(sv.Status) == lifecycle.Starting {
			return sv, nil, fmt.Errorf("%w: stop the server before moving its port", ErrBadState)
		}
		tx, err := s.pool.Begin(ctx)
		if err != nil {
			return sv, nil, err
		}
		defer func() { _ = tx.Rollback(ctx) }()
		qtx := s.q.WithTx(tx)
		// Claim the new allocation first (free + unclaimed under lock).
		newAlloc, err := qtx.GetAllocationForUpdate(ctx, *req.Allocation)
		if err != nil {
			return sv, nil, fmt.Errorf("allocation not found")
		}
		assigned, err := qtx.AllocationServerCount(ctx, gen.AllocationServerCountParams{
			AllocationID: *req.Allocation, ID: sv.ID,
		})
		if err != nil {
			return sv, nil, err
		}
		if assigned != 0 {
			return sv, nil, fmt.Errorf("%w: allocation already in use", ErrNoAllocations)
		}
		if err := qtx.SetServerAllocation(ctx, gen.SetServerAllocationParams{
			ID: sv.ID, AllocationID: *req.Allocation,
		}); err != nil {
			return sv, nil, err
		}
		if err := tx.Commit(ctx); err != nil {
			return sv, nil, err
		}
		// Keep the install-config bind port in sync for the next start.
		var cfg Config
		if err := json.Unmarshal(sv.Config, &cfg); err == nil {
			cfg.BindIP = newAlloc.Ip
			cfg.BindPort = newAlloc.Port
			cfgRaw, _ := json.Marshal(cfg)
			_ = s.q.UpdateServerConfig(ctx, gen.UpdateServerConfigParams{ID: sv.ID, Config: cfgRaw})
		}
		notes = append(notes, fmt.Sprintf("port moved to %s:%d — applies after restart", newAlloc.Ip, newAlloc.Port))
		sv.AllocationID = *req.Allocation
	}

	updated, err := s.q.GetServer(ctx, sv.ID)
	if err != nil {
		return sv, notes, err
	}
	return updated, notes, nil
}

// Delete removes a server and wipes its data directory on the node.
// Installing servers can be deleted too: nothing is running yet, and a late
// install result is ignored once the row is gone.
func (s *Service) Delete(ctx context.Context, server gen.Server) error {
	status := lifecycle.Status(server.Status)
	switch status {
	case lifecycle.Offline, lifecycle.Crashed, lifecycle.Installing:
	default:
		return fmt.Errorf("%w: stop the server before deleting", ErrBadState)
	}
	if status == lifecycle.Installing {
		_ = s.q.SetServerStatus(ctx, gen.SetServerStatusParams{
			ID: server.ID, Status: string(lifecycle.Offline),
		})
	}
	// Best-effort data wipe; the row goes regardless.
	if s.channels.Connected(server.NodeID) {
		sendCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
		_, _ = s.channels.Send(sendCtx, server.NodeID, &nodesv1.Command{
			Action: &nodesv1.Command_DeleteFile{DeleteFile: &nodesv1.DeleteFile{
				ServerId: server.ID.String(), Path: ".", Recursive: true,
			}},
		})
		cancel()
	}
	return s.q.DeleteServer(ctx, server.ID)
}
