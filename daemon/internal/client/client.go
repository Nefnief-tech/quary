// Package client implements the daemon side of the panel connection:
// enrollment over mTLS and the heartbeat stream, with reconnect backoff.
package client

import (
	"context"
	crand "crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"log/slog"
	"math/rand"
	"os"
	"runtime"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"

	"quarry.dev/quarry/daemon/internal/config"
	"quarry.dev/quarry/daemon/internal/servers"
	"quarry.dev/quarry/daemon/internal/state"
	nodesv1 "quarry.dev/quarry/proto/gen/go/quarry/node/v1"
)

// Version is the daemon version, set at build time.
var Version = "dev"

// errReenrolled signals the connection loop to redial immediately because a
// new identity was just written.
var errReenrolled = errors.New("enrolled; redialing with new identity")

type Client struct {
	cfg     config.Config
	store   *state.Store
	log     *slog.Logger
	manager *servers.Manager
}

func New(cfg config.Config, store *state.Store, log *slog.Logger, manager *servers.Manager) *Client {
	return &Client{cfg: cfg, store: store, log: log, manager: manager}
}

// Run keeps the daemon connected until ctx is cancelled, reconnecting with
// exponential backoff (plus jitter) on every failure. Backoff resets after
// any connection that lasted a while, so a panel restart costs about a
// second of disconnection rather than the accumulated maximum.
func (c *Client) Run(ctx context.Context) error {
	backoff := time.Second
	for {
		connectAt := time.Now()
		err := c.runOnce(ctx)
		if ctx.Err() != nil {
			return nil
		}
		if errors.Is(err, errReenrolled) {
			backoff = time.Second
			continue
		}
		if time.Since(connectAt) > time.Minute {
			backoff = time.Second
		}
		c.log.Warn("connection lost", "err", err, "retrying_in", backoff.String())
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(backoff):
		}
		backoff *= 2
		if backoff > 30*time.Second {
			backoff = 30 * time.Second
		}
		// jitter
		backoff += time.Duration(rand.Int63n(int64(backoff / 4)))
	}
}

func (c *Client) runOnce(ctx context.Context) error {
	id, err := c.store.Load()
	if err != nil {
		return err
	}

	conn, err := c.dial(id)
	if err != nil {
		return fmt.Errorf("dial panel: %w", err)
	}
	defer func() { _ = conn.Close() }()
	api := nodesv1.NewNodeAgentServiceClient(conn)

	if id == nil || len(id.CertPEM) == 0 {
		if _, enrollErr := c.enroll(ctx, api); enrollErr != nil {
			return enrollErr
		}
		// Redial so the new client certificate is presented.
		return errReenrolled
	}

	// Heartbeat and command channel run in parallel; either failing tears
	// the connection down and the outer loop reconnects.
	errCh := make(chan error, 2)
	go func() { errCh <- c.heartbeat(ctx, api) }()
	go func() { errCh <- c.runChannel(ctx, api) }()
	select {
	case <-ctx.Done():
		return nil
	case err := <-errCh:
		return err
	}
}

// dial builds the gRPC connection: TLS with the panel CA pinned, plus the
// client certificate once enrolled.
func (c *Client) dial(id *state.Identity) (*grpc.ClientConn, error) {
	caPEM, err := os.ReadFile(c.cfg.CAFile)
	if err != nil {
		return nil, fmt.Errorf("read CA file: %w", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caPEM) {
		return nil, errors.New("CA file contains no certificates")
	}

	tlsCfg := &tls.Config{
		MinVersion: tls.VersionTLS12,
		RootCAs:    pool,
		ServerName: c.cfg.ServerName,
	}
	if id != nil && len(id.CertPEM) > 0 {
		cert, err := tls.X509KeyPair(id.CertPEM, id.KeyPEM)
		if err != nil {
			return nil, fmt.Errorf("load daemon identity: %w", err)
		}
		tlsCfg.Certificates = []tls.Certificate{cert}
	}

	return grpc.NewClient(c.cfg.PanelAddr,
		grpc.WithTransportCredentials(credentials.NewTLS(tlsCfg)),
		grpc.WithUserAgent("quarry-daemon/"+Version),
	)
}

// enroll exchanges the enrollment token for a signed certificate. The token
// is only sent once; the private key never leaves the daemon.
func (c *Client) enroll(ctx context.Context, api nodesv1.NodeAgentServiceClient) (*state.Identity, error) {
	if c.cfg.EnrollmentToken == "" {
		return nil, errors.New("not enrolled yet and QUARRY_ENROLLMENT_TOKEN is empty")
	}
	key, created, err := c.store.LoadOrCreateKey()
	if err != nil {
		return nil, fmt.Errorf("load daemon key: %w", err)
	}
	if created {
		c.log.Info("generated new daemon key")
	}

	csrDER, err := x509.CreateCertificateRequest(crand.Reader, &x509.CertificateRequest{
		Subject: pkix.Name{CommonName: "quarry-daemon-" + c.cfg.NodeName},
	}, key)
	if err != nil {
		return nil, fmt.Errorf("create csr: %w", err)
	}

	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	resp, err := api.RegisterNode(ctx, &nodesv1.RegisterNodeRequest{
		EnrollmentToken: c.cfg.EnrollmentToken,
		Name:            c.cfg.NodeName,
		CsrPem:          pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: csrDER}),
		Info:            c.info(),
	})
	if err != nil {
		return nil, fmt.Errorf("enroll: %w", err)
	}

	nodeID, err := uuid.Parse(resp.GetNodeId())
	if err != nil {
		return nil, fmt.Errorf("panel returned invalid node id: %w", err)
	}
	id := &state.Identity{
		NodeID:  nodeID,
		CertPEM: resp.GetCertPem(),
	}
	// Keep the existing key PEM; the cert now matches it.
	if existing, err := c.store.Load(); err == nil && existing != nil {
		id.KeyPEM = existing.KeyPEM
	}
	if len(id.KeyPEM) == 0 {
		der, err := x509.MarshalECPrivateKey(key)
		if err != nil {
			return nil, err
		}
		id.KeyPEM = pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der})
	}
	if err := c.store.Save(id); err != nil {
		return nil, fmt.Errorf("persist identity: %w", err)
	}
	c.log.Info("enrolled with panel", "node_id", id.NodeID)
	return id, nil
}

// heartbeat opens the bidirectional stream: a beat every interval, acks
// consumed concurrently. Any stream error tears the connection down and the
// outer loop reconnects.
func (c *Client) heartbeat(ctx context.Context, api nodesv1.NodeAgentServiceClient) error {
	stream, err := api.Heartbeat(ctx)
	if err != nil {
		return fmt.Errorf("open heartbeat stream: %w", err)
	}

	errCh := make(chan error, 1)
	go func() {
		for {
			resp, err := stream.Recv()
			if err != nil {
				errCh <- err
				return
			}
			c.log.Debug("heartbeat ack", "server_time", resp.GetServerTimeUnixMilli())
		}
	}()

	c.log.Info("heartbeat stream established")
	ticker := time.NewTicker(c.cfg.HeartbeatInterval)
	defer ticker.Stop()
	// Send the first beat immediately.
	if err := stream.Send(&nodesv1.HeartbeatRequest{Info: c.info(), SentAtUnixMilli: time.Now().UnixMilli()}); err != nil {
		return fmt.Errorf("send heartbeat: %w", err)
	}
	for {
		select {
		case <-ctx.Done():
			_ = stream.CloseSend()
			return nil
		case err := <-errCh:
			if ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("heartbeat stream: %w", err)
		case <-ticker.C:
			if err := stream.Send(&nodesv1.HeartbeatRequest{
				UptimeSeconds:   uint64(time.Since(startedAt) / time.Second),
				Info:            c.info(),
				SentAtUnixMilli: time.Now().UnixMilli(),
			}); err != nil {
				return fmt.Errorf("send heartbeat: %w", err)
			}
		}
	}
}

func (c *Client) info() *nodesv1.DaemonInfo {
	host, _ := os.Hostname()
	return &nodesv1.DaemonInfo{
		DaemonVersion: Version,
		Hostname:      host,
		Os:            runtime.GOOS,
		Arch:          runtime.GOARCH,
		PublicHost:    c.cfg.PublicHost,
	}
}

var startedAt = time.Now()
