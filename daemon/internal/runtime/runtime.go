// Package runtime abstracts the container engine. The daemon only ever uses
// this interface — Podman support later means implementing it again, not
// rewiring the daemon.
package runtime

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
)

var (
	ErrNotRunning = errors.New("server container is not running")
	ErrNotFound   = errors.New("server container does not exist")
)

// ServerSpec describes one server container.
type ServerSpec struct {
	ID         uuid.UUID
	Image      string
	Launch     []string
	MemoryMB   int
	CPUPercent int // 100 = one full core
	BindIP     string
	BindPort   int
	// MapPort publishes the container's map webserver port (8100) on this
	// host port; 0 disables it.
	MapPort int
}

// Stats is a one-shot resource snapshot.
type Stats struct {
	CPUPercent      float64
	MemoryUsedMB    float64
	MemoryLimitMB   float64
	NetworkRxPerSec float64
	NetworkTxPerSec float64
}

// Console is a bidirectional attach to the server process. Lines from
// stdout/stderr arrive on Lines; WriteLine feeds stdin. Write is safe for
// concurrent use.
type Console interface {
	WriteLine(line string) error
	Lines() <-chan string
	Done() <-chan struct{} // closed when the container's streams end
	Close()
}

// ContainerRuntime manages server containers.
type ContainerRuntime interface {
	// EnsureImage pulls the image if it is not present locally.
	EnsureImage(ctx context.Context, image string) error
	// CreateAndStart creates a hardened container with the server directory
	// mounted at /data, attaches the console, then starts it.
	CreateAndStart(ctx context.Context, spec ServerSpec, dataDir string) (Console, error)
	// State returns "running", "exited" or "" (unknown/not found).
	State(ctx context.Context, id uuid.UUID) (string, error)
	// Stop terminates gracefully: console stop should already have been
	// attempted; this stops the container after timeout.
	Stop(ctx context.Context, id uuid.UUID, timeout time.Duration) error
	// Kill terminates immediately.
	Kill(ctx context.Context, id uuid.UUID) error
	// Remove deletes a stopped container.
	Remove(ctx context.Context, id uuid.UUID) error
	// StatsOneShot returns a single resource snapshot.
	StatsOneShot(ctx context.Context, id uuid.UUID) (*Stats, error)
	// ListRunning returns IDs of containers labeled as quarry servers that
	// are currently running (used to reconcile after daemon restarts).
	ListRunning(ctx context.Context) ([]uuid.UUID, error)
	// ListExited returns IDs of labeled quarry containers that exist but
	// are not running (stale statuses after a daemon downtime).
	ListExited(ctx context.Context) ([]uuid.UUID, error)
	// Attach reattaches a console to an already-running container.
	Attach(ctx context.Context, id uuid.UUID) (Console, error)
	// DiskUsage returns the size (bytes) of a server data directory.
	DiskUsage(ctx context.Context, dataDir string) (int64, error)
}
