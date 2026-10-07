// Bore tunnel client containers: the ekzhang/bore client (pinned by
// digest) forwarding the server's game port to the public bore.pub relay.
package runtime

import (
	"context"
	"fmt"
	"io"
	"regexp"
	"strconv"

	"github.com/google/uuid"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"
)

// boreImageRef is ekzhang/bore 0.5.2 pinned by digest.
const boreImageRef = "ekzhang/bore@sha256:26fdf8b729da8953e7f5984f7e2143b2d9648d9079b7557b0a560cccb78b5b64"

const boreRelayHost = "bore.pub"

func boreContainerName(id uuid.UUID) string {
	return "quarry-bore-" + id.String()
}

// EnsureBoreImage pulls the pinned bore image if not present locally.
func (d *Docker) EnsureBoreImage(ctx context.Context) error {
	if _, err := d.cli.ImageInspect(ctx, boreImageRef); err == nil {
		return nil
	} else if !isNotFound(err) {
		return err
	}
	rc, err := d.cli.ImagePull(ctx, boreImageRef, client.ImagePullOptions{})
	if err != nil {
		return fmt.Errorf("pull bore image: %w", err)
	}
	defer func() { _ = rc.Close() }()
	_, _ = io.Copy(io.Discard, rc)
	return nil
}

// StartBoreContainer runs the bore client for a server, host-networked so
// the relay's data connections reach the server's published port.
func (d *Docker) StartBoreContainer(ctx context.Context, id uuid.UUID, localPort int32) error {
	_, _ = d.cli.ContainerRemove(ctx, boreContainerName(id), client.ContainerRemoveOptions{Force: true})

	cfg := &container.Config{
		Image: boreImageRef,
		Cmd:   []string{"local", strconv.FormatInt(int64(localPort), 10), "--to", boreRelayHost},
		Labels: map[string]string{
			"quarry.bore": id.String(),
		},
	}
	hostCfg := &container.HostConfig{
		NetworkMode: container.NetworkMode("host"),
		Resources: container.Resources{
			Memory:     32 << 20,
			MemorySwap: 32 << 20,
		},
		SecurityOpt: []string{"no-new-privileges"},
	}
	created, err := d.cli.ContainerCreate(ctx, client.ContainerCreateOptions{
		Config:     cfg,
		HostConfig: hostCfg,
		Name:       boreContainerName(id),
	})
	if err != nil {
		return fmt.Errorf("create bore container: %w", err)
	}
	if _, err := d.cli.ContainerStart(ctx, created.ID, client.ContainerStartOptions{}); err != nil {
		_, _ = d.cli.ContainerRemove(context.Background(), created.ID, client.ContainerRemoveOptions{Force: true})
		return fmt.Errorf("start bore container: %w", err)
	}
	return nil
}

// StopBore removes the bore container for a server.
func (d *Docker) StopBore(ctx context.Context, id uuid.UUID) error {
	_, err := d.cli.ContainerRemove(ctx, boreContainerName(id), client.ContainerRemoveOptions{Force: true})
	return err
}

// BoreRunning reports whether the bore container is running.
func (d *Docker) BoreRunning(ctx context.Context, id uuid.UUID) (bool, error) {
	inspect, err := d.cli.ContainerInspect(ctx, boreContainerName(id), client.ContainerInspectOptions{})
	if err != nil {
		if isNotFound(err) {
			return false, nil
		}
		return false, err
	}
	return inspect.Container.State != nil && inspect.Container.State.Running, nil
}

var boreAddrRe = regexp.MustCompile(`listening at ([A-Za-z0-9.-]+:\d+)`)

// BoreAssignedAddress reads the client's logs for the relay-assigned
// public address.
func (d *Docker) BoreAssignedAddress(ctx context.Context, id uuid.UUID) (string, error) {
	logs, err := d.cli.ContainerLogs(ctx, boreContainerName(id), client.ContainerLogsOptions{
		ShowStdout: true, ShowStderr: true, Tail: "all",
	})
	if err != nil {
		return "", err
	}
	defer func() { _ = logs.Close() }()
	raw, err := io.ReadAll(io.LimitReader(logs, 1<<20))
	if err != nil {
		return "", err
	}
	if m := boreAddrRe.FindSubmatch(raw); m != nil {
		return string(m[1]), nil
	}
	return "", nil
}
