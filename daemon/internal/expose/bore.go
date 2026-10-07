// bore tunnel provider: zero-account public relay at bore.pub. One client
// container per server forwards the server's game port to a relay-assigned
// public port. The assigned address is stable while the tunnel runs but
// rotates when the tunnel is recreated — reflected in the panel UI.
package expose

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
)

func filepathGlob(pattern string) ([]string, error) {
	return filepath.Glob(pattern)
}

func fileNameBase(p string) string { return filepath.Base(p) }

func trimSuffixAll(s, suffix string) string { return strings.TrimSuffix(s, suffix) }

func jsonUnmarshal(raw []byte, v any) error { return json.Unmarshal(raw, v) }

// StartBore opens a tunnel and returns the assigned public address.
func (m *Manager) StartBore(ctx context.Context, id uuid.UUID, localPort int32, emit Emitter) (*Status, error) {
	if localPort <= 0 {
		return nil, fmt.Errorf("invalid local port")
	}
	if err := m.rt.EnsureBoreImage(ctx); err != nil {
		return nil, err
	}
	if err := m.rt.StartBoreContainer(ctx, id, localPort); err != nil {
		return nil, err
	}
	addr, err := m.waitBoreAddress(ctx, id, 12*time.Second)
	if err != nil {
		return nil, err
	}
	_ = os.WriteFile(m.boreMappingPath(id), []byte(fmt.Sprintf(`{"local_port":%d}`, localPort)), 0o600)
	emitConsole(emit, id, "bore: tunnel live at "+addr)
	return &Status{Active: true, Address: addr}, nil
}

// waitBoreAddress polls the container logs for the relay-assigned address.
func (m *Manager) waitBoreAddress(ctx context.Context, id uuid.UUID, window time.Duration) (string, error) {
	deadline := time.Now().Add(window)
	for time.Now().Before(deadline) {
		addr, err := m.rt.BoreAssignedAddress(ctx, id)
		if err == nil && addr != "" {
			return addr, nil
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
	return "", errors.New("bore relay did not assign a port in time")
}

func (m *Manager) boreMappingPath(id uuid.UUID) string {
	return m.cfg.DataDir + "/bore-" + id.String() + ".json"
}

// StopBore removes the tunnel container and mapping.
func (m *Manager) StopBore(ctx context.Context, id uuid.UUID, emit Emitter) error {
	_ = m.rt.StopBore(ctx, id)
	_ = os.Remove(m.boreMappingPath(id))
	emitConsole(emit, id, "bore: tunnel stopped")
	return nil
}

// ReconcileBore restarts bore tunnels whose mapping survived a restart.
func (m *Manager) ReconcileBore(ctx context.Context) {
	files, err := filepathGlob(m.cfg.DataDir + "/bore-*.json")
	if err != nil {
		return
	}
	for _, f := range files {
		base := fileNameBase(f)
		id, err := uuid.Parse(trimSuffixAll(base, ".json"))
		if err != nil {
			continue
		}
		raw, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		localPort := parseLocalPort(raw)
		if localPort <= 0 {
			continue
		}
		running, err := m.rt.BoreRunning(ctx, id)
		if err == nil && running {
			continue
		}
		if err := m.rt.StartBoreContainer(ctx, id, localPort); err != nil {
			m.log.Warn("bore restart failed", "server_id", id, "err", err)
		}
	}
}

func parseLocalPort(raw []byte) int32 {
	var v struct {
		LocalPort int32 `json:"local_port"`
	}
	_ = jsonUnmarshal(raw, &v)
	return v.LocalPort
}
