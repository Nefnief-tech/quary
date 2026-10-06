package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/netip"
	"strconv"
	"sync"
	"time"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/google/uuid"
	"github.com/moby/moby/api/pkg/stdcopy"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/network"
	"github.com/moby/moby/client"
)

// labelServer marks containers as quarry-managed.
const labelServer = "quarry.server-id"

// Docker implements ContainerRuntime against the Docker Engine API.
type Docker struct {
	cli *client.Client
	// user is "uid:uid" — server containers run as the daemon's own UID so
	// the bind-mounted data directory is writable inside and outside.
	user string
}

// NewDocker builds the runtime; containerUID must match the daemon's
// effective UID.
func NewDocker(containerUID int) (*Docker, error) {
	// API-version negotiation is on by default in the current client.
	cli, err := client.New(client.FromEnv)
	if err != nil {
		return nil, fmt.Errorf("docker client: %w", err)
	}
	if containerUID <= 0 {
		containerUID = 1500
	}
	return &Docker{cli: cli, user: fmt.Sprintf("%d:%d", containerUID, containerUID)}, nil
}

func containerName(id uuid.UUID) string {
	return "quarry-server-" + id.String()
}

// isNotFound reports whether err is a daemon "object not found" error.
func isNotFound(err error) bool {
	return cerrdefs.IsNotFound(err)
}

func (d *Docker) EnsureImage(ctx context.Context, ref string) error {
	if _, err := d.cli.ImageInspect(ctx, ref); err == nil {
		return nil
	} else if !isNotFound(err) {
		return err
	}
	rc, err := d.cli.ImagePull(ctx, ref, client.ImagePullOptions{})
	if err != nil {
		return fmt.Errorf("pull image %s: %w", ref, err)
	}
	defer func() { _ = rc.Close() }()
	_, _ = io.Copy(io.Discard, rc) // consume the progress stream
	return nil
}

func (d *Docker) CreateAndStart(ctx context.Context, spec ServerSpec, dataDir string) (Console, error) {
	name := containerName(spec.ID)
	// Leftovers from a crashed daemon would collide.
	_ = d.remove(ctx, name)

	portKey, err := network.ParsePort(fmt.Sprintf("%d/tcp", spec.BindPort))
	if err != nil {
		return nil, fmt.Errorf("parse port: %w", err)
	}
	hostIP, err := netip.ParseAddr(normalizeBindIP(spec.BindIP))
	if err != nil {
		return nil, fmt.Errorf("parse bind ip: %w", err)
	}

	exposed := network.PortSet{portKey: {}}
	bindings := network.PortMap{portKey: []network.PortBinding{
		{HostIP: hostIP, HostPort: strconv.Itoa(spec.BindPort)},
	}}
	if spec.MapPort > 0 {
		mapKey, err := network.ParsePort(fmt.Sprintf("%d/tcp", mapContainerPort))
		if err != nil {
			return nil, fmt.Errorf("parse map port: %w", err)
		}
		exposed[mapKey] = struct{}{}
		bindings[mapKey] = []network.PortBinding{
			{HostIP: hostIP, HostPort: strconv.Itoa(spec.MapPort)},
		}
	}

	cfg := &container.Config{
		Image: spec.Image,
		Cmd:   spec.Launch,
		Labels: map[string]string{
			labelServer: spec.ID.String(),
		},
		User:         d.user,
		WorkingDir:   "/data",
		OpenStdin:    true,
		StdinOnce:    false,
		AttachStdin:  true,
		AttachStdout: true,
		AttachStderr: true,
		ExposedPorts: exposed,
	}
	hostCfg := &container.HostConfig{
		Binds: []string{dataDir + ":/data"},
		// Hardening: no capabilities, no privilege escalation, read-only root
		// filesystem — everything Minecraft writes lives in /data.
		CapDrop:        []string{"ALL"},
		SecurityOpt:    []string{"no-new-privileges"},
		ReadonlyRootfs: true,
		Tmpfs:          map[string]string{"/tmp": "rw,noexec,nosuid,size=64m"},
		Resources: container.Resources{
			Memory:     int64(spec.MemoryMB) << 20,
			MemorySwap: int64(spec.MemoryMB) << 20, // no swap
			NanoCPUs:   int64(spec.CPUPercent) * 1_000_000,
		},
		PortBindings: bindings,
	}

	created, err := d.cli.ContainerCreate(ctx, client.ContainerCreateOptions{
		Config:     cfg,
		HostConfig: hostCfg,
		Name:       name,
	})
	if err != nil {
		return nil, fmt.Errorf("create container: %w", err)
	}

	// Attach before start so no early output is lost. Outbound is
	// stdcopy-framed (no TTY); inbound stdin is raw bytes.
	attach, err := d.cli.ContainerAttach(ctx, created.ID, client.ContainerAttachOptions{
		Stream: true, Stdin: true, Stdout: true, Stderr: true,
	})
	if err != nil {
		_ = d.remove(ctx, created.ID)
		return nil, fmt.Errorf("attach container: %w", err)
	}

	if _, err := d.cli.ContainerStart(ctx, created.ID, client.ContainerStartOptions{}); err != nil {
		attach.Close()
		_ = d.remove(ctx, created.ID)
		return nil, fmt.Errorf("start container: %w", err)
	}
	return newAttachConsole(attach.Conn), nil
}

func (d *Docker) Attach(ctx context.Context, id uuid.UUID) (Console, error) {
	state, err := d.State(ctx, id)
	if err != nil {
		return nil, err
	}
	if state != "running" {
		return nil, ErrNotRunning
	}
	attach, err := d.cli.ContainerAttach(ctx, containerName(id), client.ContainerAttachOptions{
		Stream: true, Stdin: true, Stdout: true, Stderr: true,
	})
	if err != nil {
		return nil, fmt.Errorf("attach container: %w", err)
	}
	return newAttachConsole(attach.Conn), nil
}

func (d *Docker) State(ctx context.Context, id uuid.UUID) (string, error) {
	inspect, err := d.cli.ContainerInspect(ctx, containerName(id), client.ContainerInspectOptions{})
	if err != nil {
		if isNotFound(err) {
			return "", nil
		}
		return "", err
	}
	if inspect.Container.State == nil {
		return "", nil
	}
	if inspect.Container.State.Running {
		return "running", nil
	}
	return "exited", nil
}

func (d *Docker) Stop(ctx context.Context, id uuid.UUID, timeout time.Duration) error {
	timeoutSecs := int(timeout.Seconds())
	_, err := d.cli.ContainerStop(ctx, containerName(id), client.ContainerStopOptions{
		Timeout: &timeoutSecs,
	})
	if isNotFound(err) {
		return nil
	}
	return err
}

func (d *Docker) Kill(ctx context.Context, id uuid.UUID) error {
	_, err := d.cli.ContainerKill(ctx, containerName(id), client.ContainerKillOptions{})
	if isNotFound(err) {
		return nil
	}
	return err
}

func (d *Docker) Remove(ctx context.Context, id uuid.UUID) error {
	return d.remove(ctx, containerName(id))
}

func (d *Docker) remove(ctx context.Context, name string) error {
	_, err := d.cli.ContainerRemove(ctx, name, client.ContainerRemoveOptions{})
	if isNotFound(err) {
		return nil
	}
	return err
}

func (d *Docker) ListRunning(ctx context.Context) ([]uuid.UUID, error) {
	list, err := d.cli.ContainerList(ctx, client.ContainerListOptions{
		All:     true,
		Filters: client.Filters{}.Add("label", labelServer),
	})
	if err != nil {
		return nil, err
	}
	var out []uuid.UUID
	for _, c := range list.Items {
		if c.State != "running" {
			continue
		}
		id, err := uuid.Parse(c.Labels[labelServer])
		if err != nil {
			continue
		}
		out = append(out, id)
	}
	return out, nil
}

// ListExited returns labeled quarry containers that exist but are not
// running.
func (d *Docker) ListExited(ctx context.Context) ([]uuid.UUID, error) {
	list, err := d.cli.ContainerList(ctx, client.ContainerListOptions{
		All:     true,
		Filters: client.Filters{}.Add("label", labelServer),
	})
	if err != nil {
		return nil, err
	}
	var out []uuid.UUID
	for _, c := range list.Items {
		if c.State == "running" {
			continue
		}
		id, err := uuid.Parse(c.Labels[labelServer])
		if err != nil {
			continue
		}
		out = append(out, id)
	}
	return out, nil
}

type dockerStatsJSON struct {
	CPUStats struct {
		CPUUsage struct {
			TotalUsage uint64 `json:"total_usage"`
		} `json:"cpu_usage"`
		SystemUsage uint64 `json:"system_cpu_usage"`
		OnlineCPUs  int    `json:"online_cpus"`
	} `json:"cpu_stats"`
	PreCPUStats struct {
		CPUUsage struct {
			TotalUsage uint64 `json:"total_usage"`
		} `json:"cpu_usage"`
		SystemUsage uint64 `json:"system_cpu_usage"`
	} `json:"precpu_stats"`
	MemoryStats struct {
		Usage uint64 `json:"usage"`
		Limit uint64 `json:"limit"`
		Stats struct {
			InactiveFile uint64 `json:"inactive_file"`
			Cache        uint64 `json:"cache"`
		} `json:"stats"`
	} `json:"memory_stats"`
	Networks map[string]struct {
		RxBytes uint64 `json:"rx_bytes"`
		TxBytes uint64 `json:"tx_bytes"`
	} `json:"networks"`
}

// StatsOneShot returns a snapshot. Network counters are cumulative; the
// caller derives per-second rates from consecutive samples.
func (d *Docker) StatsOneShot(ctx context.Context, id uuid.UUID) (*Stats, error) {
	raw, err := d.cli.ContainerStats(ctx, containerName(id), client.ContainerStatsOptions{
		IncludePreviousSample: true,
	})
	if err != nil {
		return nil, err
	}
	defer func() { _ = raw.Body.Close() }()
	var s dockerStatsJSON
	if err := json.NewDecoder(raw.Body).Decode(&s); err != nil {
		return nil, err
	}

	out := &Stats{}
	cpuDelta := float64(s.CPUStats.CPUUsage.TotalUsage - s.PreCPUStats.CPUUsage.TotalUsage)
	sysDelta := float64(s.CPUStats.SystemUsage - s.PreCPUStats.SystemUsage)
	cpus := s.CPUStats.OnlineCPUs
	if cpus == 0 {
		cpus = 1
	}
	if sysDelta > 0 && cpuDelta >= 0 {
		out.CPUPercent = (cpuDelta / sysDelta) * float64(cpus) * 100
	}
	mem := s.MemoryStats.Usage
	if s.MemoryStats.Stats.InactiveFile > 0 {
		mem -= s.MemoryStats.Stats.InactiveFile
	} else if s.MemoryStats.Stats.Cache > 0 {
		mem -= s.MemoryStats.Stats.Cache
	}
	out.MemoryUsedMB = float64(mem) / (1 << 20)
	out.MemoryLimitMB = float64(s.MemoryStats.Limit) / (1 << 20)
	for _, n := range s.Networks {
		out.NetworkRxPerSec += float64(n.RxBytes)
		out.NetworkTxPerSec += float64(n.TxBytes)
	}
	return out, nil
}

func (d *Docker) DiskUsage(_ context.Context, dataDir string) (int64, error) {
	return diskUsageLocal(dataDir)
}

// mapContainerPort is the port BlueMap's embedded webserver listens on
// inside every server container.
const mapContainerPort = 8100

func normalizeBindIP(ip string) string {
	if ip == "" {
		return "0.0.0.0"
	}
	return ip
}

// attachConsole adapts a Docker attach connection to the Console interface.
// Outbound is stdcopy-demuxed into lines; stdin is written raw. Half-closing
// the write side signals EOF to the server process.
type attachConsole struct {
	conn    netConn
	lines   chan string
	done    chan struct{}
	once    sync.Once
	writeMu sync.Mutex
}

type netConn interface {
	io.ReadWriteCloser
}

func newAttachConsole(conn netConn) *attachConsole {
	c := &attachConsole{
		conn:  conn,
		lines: make(chan string, 1024),
		done:  make(chan struct{}),
	}
	go c.pump()
	return c
}

func (c *attachConsole) pump() {
	defer close(c.done)
	defer close(c.lines)

	// StdCopy demuxes stdout/stderr frames into a single pipe.
	r, w := io.Pipe()
	go func() {
		_, _ = stdcopy.StdCopy(w, w, c.conn)
		_ = w.Close()
	}()

	buf := make([]byte, 0, 64*1024)
	chunk := make([]byte, 4096)
	for {
		n, err := r.Read(chunk)
		if n > 0 {
			buf = append(buf, chunk[:n]...)
			for {
				idx := indexByte(buf, '\n')
				if idx == -1 {
					break
				}
				line := string(buf[:idx])
				buf = buf[idx+1:]
				select {
				case c.lines <- line:
				default: // drop on overflow; the UI keeps its own scrollback
				}
			}
			if len(buf) > 1<<20 { // pathological long line: drop it
				buf = buf[:0]
			}
		}
		if err != nil {
			return
		}
	}
}

func indexByte(b []byte, c byte) int {
	for i := range b {
		if b[i] == c {
			return i
		}
	}
	return -1
}

func (c *attachConsole) WriteLine(line string) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	_, err := c.conn.Write([]byte(line + "\n"))
	return err
}

func (c *attachConsole) Lines() <-chan string  { return c.lines }
func (c *attachConsole) Done() <-chan struct{} { return c.done }

// Close half-closes stdin (signals EOF to the server process).
func (c *attachConsole) Close() {
	c.once.Do(func() {
		if cw, ok := c.conn.(interface{ CloseWrite() error }); ok {
			_ = cw.CloseWrite()
		}
	})
}
