// Playit tunnel agent container management. The agent is the official
// playit-cloud image pinned by digest, run on the host network so it can
// reach the node's published server ports.
package runtime

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"
)

// playitImageRef is ghcr.io/playit-cloud/playit-agent tag 1.0.8, pinned by
// manifest digest.
const playitImageRef = "ghcr.io/playit-cloud/playit-agent@sha256:2c69d171b12ea907a1bf1f3fcf06d75de4458c3a3792022fe95375ed922e0bf1"

func playitContainerName(id uuid.UUID) string {
	return "quarry-playit-" + id.String()
}

// EnsurePlayitImage pulls the pinned agent image if not present locally.
func (d *Docker) EnsurePlayitImage(ctx context.Context) error {
	if _, err := d.cli.ImageInspect(ctx, playitImageRef); err == nil {
		return nil
	} else if !isNotFound(err) {
		return err
	}
	rc, err := d.cli.ImagePull(ctx, playitImageRef, client.ImagePullOptions{})
	if err != nil {
		return fmt.Errorf("pull playit image: %w", err)
	}
	defer func() { _ = rc.Close() }()
	_, _ = io.Copy(io.Discard, rc)
	return nil
}

// StartPlayitAgent runs the agent container for a server with the secret
// injected as the entrypoint's SECRET_KEY env, on the host network so the
// tunnel can reach the node's published server ports.
func (d *Docker) StartPlayitAgent(ctx context.Context, id uuid.UUID, secret string) error {
	_, _ = d.cli.ContainerRemove(ctx, playitContainerName(id), client.ContainerRemoveOptions{Force: true})

	cfg := &container.Config{
		Image: playitImageRef,
		Env:   []string{"SECRET_KEY=" + secret},
		Labels: map[string]string{
			"quarry.playit": id.String(),
		},
	}
	hostCfg := &container.HostConfig{
		NetworkMode: container.NetworkMode("host"),
		// The agent has no data plane beyond the tunnel itself; keep it
		// unprivileged with a modest memory ceiling.
		Resources: container.Resources{
			Memory:     128 << 20,
			MemorySwap: 128 << 20,
		},
		SecurityOpt: []string{"no-new-privileges"},
	}
	created, err := d.cli.ContainerCreate(ctx, client.ContainerCreateOptions{
		Config:     cfg,
		HostConfig: hostCfg,
		Name:       playitContainerName(id),
	})
	if err != nil {
		return fmt.Errorf("create playit container: %w", err)
	}
	if _, err := d.cli.ContainerStart(ctx, created.ID, client.ContainerStartOptions{}); err != nil {
		_, _ = d.cli.ContainerRemove(context.Background(), created.ID, client.ContainerRemoveOptions{Force: true})
		return fmt.Errorf("start playit container: %w", err)
	}
	return nil
}

// StopPlayitAgent removes the agent container for a server.
func (d *Docker) StopPlayitAgent(ctx context.Context, id uuid.UUID) error {
	_, err := d.cli.ContainerRemove(ctx, playitContainerName(id), client.ContainerRemoveOptions{Force: true})
	return err
}

// PlayitAgentRunning reports whether the agent container is running.
func (d *Docker) PlayitAgentRunning(ctx context.Context, id uuid.UUID) (bool, error) {
	inspect, err := d.cli.ContainerInspect(ctx, playitContainerName(id), client.ContainerInspectOptions{})
	if err != nil {
		if isNotFound(err) {
			return false, nil
		}
		return false, err
	}
	return inspect.Container.State != nil && inspect.Container.State.Running, nil
}

// playitLogRe selects agent log lines worth mirroring into the console.
var playitLogRe = regexp.MustCompile(`(?i)claim|tunnel|address|error|fatal`)

// StreamPlayitLogs feeds filtered agent log lines to onLine until the logs
// end or ctx is cancelled.
func (d *Docker) StreamPlayitLogs(ctx context.Context, id uuid.UUID, onLine func(line string)) {
	logCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	logs, err := d.cli.ContainerLogs(logCtx, playitContainerName(id), client.ContainerLogsOptions{
		ShowStdout: true, ShowStderr: true, Follow: true, Tail: "all",
	})
	if err != nil {
		return
	}
	defer func() { _ = logs.Close() }()

	// Agent container output is stdcopy-framed (no TTY); demux like the
	// console pump does.
	buf := make([]byte, 4096)
	var line strings.Builder
	deadline := time.Now().Add(time.Minute) // initial startup window
	for {
		if time.Now().After(deadline) {
			return
		}
		n, err := logs.Read(buf)
		if n > 0 {
			// Strip the 8-byte stdcopy header when present.
			chunk := buf[:n]
			if n > 8 && buf[0] < 0x03 {
				chunk = buf[8:]
			}
			line.Write(chunk)
			for {
				s := line.String()
				idx := strings.IndexByte(s, '\n')
				if idx < 0 {
					break
				}
				one := strings.TrimSpace(s[:idx])
				line.Reset()
				line.WriteString(s[idx+1:])
				if one != "" && playitLogRe.MatchString(one) && onLine != nil {
					onLine(one)
				}
			}
		}
		if err != nil {
			return
		}
	}
}

var _ AgentLogStreamer = (*Docker)(nil)

// AgentLogStreamer is satisfied by runtimes that can mirror agent logs.
type AgentLogStreamer interface {
	StreamPlayitLogs(ctx context.Context, id uuid.UUID, onLine func(line string))
}

var _ = slog.Default
