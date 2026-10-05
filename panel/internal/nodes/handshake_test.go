//go:build integration

package nodes_test

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	crand "crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/status"

	"quarry.dev/quarry/internal/seal"
	"quarry.dev/quarry/panel/internal/audit"
	"quarry.dev/quarry/panel/internal/config"
	"quarry.dev/quarry/panel/internal/db/gen"
	"quarry.dev/quarry/panel/internal/nodes"
	"quarry.dev/quarry/panel/internal/testutil"
	nodesv1 "quarry.dev/quarry/proto/gen/go/quarry/node/v1"
)

// TestHandshake covers the Phase 0 node contract end to end against a real
// mTLS gRPC server and Postgres:
//  1. RegisterNode with a single-use token issues a CA-chained client cert.
//  2. Heartbeat without a client certificate is rejected (mTLS enforced).
//  3. Heartbeat with the client cert is acked and updates last_seen.
//  4. The burned token cannot enroll again.
func TestHandshake(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	pool := testutil.StartPostgres(t)
	q := gen.New(pool)

	box, err := seal.NewBox(bytes.Repeat([]byte{0xAB}, seal.KeySize))
	if err != nil {
		t.Fatal(err)
	}
	ca, created, err := nodes.LoadOrCreateCA(t.TempDir(), box)
	if err != nil {
		t.Fatal(err)
	}
	if !created {
		t.Fatal("expected a fresh CA")
	}

	log := slog.Default()
	svc := nodes.NewService(pool, q, ca, audit.New(q, log), log)
	chanMgr := nodes.NewChannelManager(log)
	grpcSrv := nodes.NewGRPCServer(svc, log, chanMgr, func(*nodesv1.Event) {})

	nodeID := uuid.New()
	if _, err := q.CreateNode(ctx, gen.CreateNodeParams{
		ID: nodeID, Name: "handshake-node", Description: "integration",
	}); err != nil {
		t.Fatal(err)
	}
	token, err := svc.IssueToken(ctx, nodeID)
	if err != nil {
		t.Fatal(err)
	}

	lis, gs, err := nodes.NewListener(config.Config{
		GRPCAddr:    "127.0.0.1:0",
		TLSDNSNames: []string{"localhost"},
	}, ca)
	if err != nil {
		t.Fatal(err)
	}
	nodesv1.RegisterNodeAgentServiceServer(gs, grpcSrv)
	go func() { _ = gs.Serve(lis) }()
	defer gs.Stop()
	addr := lis.Addr().String()

	// --- 1. Enroll: no client cert yet, panel CA pinned, token in hand. ---
	caPool := ca.CertPool()
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(
		credentials.NewTLS(&tls.Config{RootCAs: caPool, MinVersion: tls.VersionTLS12}),
	))
	if err != nil {
		t.Fatal(err)
	}
	api := nodesv1.NewNodeAgentServiceClient(conn)

	key, err := ecdsa.GenerateKey(elliptic.P256(), crand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	csrDER, err := x509.CreateCertificateRequest(crand.Reader, &x509.CertificateRequest{
		Subject: pkix.Name{CommonName: "quarry-daemon-test"},
	}, key)
	if err != nil {
		t.Fatal(err)
	}

	resp, err := api.RegisterNode(ctx, &nodesv1.RegisterNodeRequest{
		EnrollmentToken: token,
		Name:            "handshake-node",
		CsrPem:          pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: csrDER}),
		Info:            &nodesv1.DaemonInfo{DaemonVersion: "test", Os: "linux", Arch: "amd64"},
	})
	if err != nil {
		t.Fatalf("RegisterNode: %v", err)
	}
	if got, err := uuid.Parse(resp.GetNodeId()); err != nil || got != nodeID {
		t.Fatalf("enrolled node id = %v (%v), want %s", resp.GetNodeId(), err, nodeID)
	}
	if !bytes.Equal(resp.GetCaPem(), ca.CACertPEM()) {
		t.Fatal("panel returned a CA that differs from its own")
	}
	verifyClientCert(t, ca, resp.GetCertPem(), nodeID)

	// --- 2. Heartbeat without a client cert must be rejected. ---
	noCertStream, err := api.Heartbeat(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_ = noCertStream.Send(&nodesv1.HeartbeatRequest{})
	if _, err := noCertStream.Recv(); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("heartbeat without client cert: err = %v, want Unauthenticated", err)
	}

	// --- 3. Heartbeat with the client cert is acked and recorded. ---
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	clientCert, err := tls.X509KeyPair(resp.GetCertPem(), pem.EncodeToMemory(&pem.Block{
		Type: "EC PRIVATE KEY", Bytes: keyDER,
	}))
	if err != nil {
		t.Fatalf("load issued client cert: %v", err)
	}

	conn2, err := grpc.NewClient(addr, grpc.WithTransportCredentials(
		credentials.NewTLS(&tls.Config{
			RootCAs:      caPool,
			MinVersion:   tls.VersionTLS12,
			Certificates: []tls.Certificate{clientCert},
		}),
	))
	if err != nil {
		t.Fatal(err)
	}
	api2 := nodesv1.NewNodeAgentServiceClient(conn2)
	stream, err := api2.Heartbeat(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := stream.Send(&nodesv1.HeartbeatRequest{
		Info: &nodesv1.DaemonInfo{DaemonVersion: "test", Os: "linux", Arch: "amd64"},
	}); err != nil {
		t.Fatalf("send heartbeat: %v", err)
	}
	ack, err := stream.Recv()
	if err != nil {
		t.Fatalf("recv ack: %v", err)
	}
	if ack.GetServerTimeUnixMilli() <= 0 {
		t.Fatal("ack carries no server time")
	}

	deadline := time.Now().Add(10 * time.Second)
	for {
		node, err := q.GetNode(ctx, nodeID)
		if err != nil {
			t.Fatal(err)
		}
		if node.LastSeenAt != nil && time.Since(*node.LastSeenAt) < 5*time.Second {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("heartbeat not recorded (last_seen=%v)", node.LastSeenAt)
		}
		time.Sleep(100 * time.Millisecond)
	}
	if node, _ := q.GetNode(ctx, nodeID); node.DaemonVersion != "test" {
		t.Fatalf("daemon_version = %q, want test", node.DaemonVersion)
	}

	// --- 4. The burned token cannot enroll again. ---
	csr2DER, err := x509.CreateCertificateRequest(crand.Reader, &x509.CertificateRequest{
		Subject: pkix.Name{CommonName: "second-attempt"},
	}, key)
	if err != nil {
		t.Fatal(err)
	}
	_, err = api.RegisterNode(ctx, &nodesv1.RegisterNodeRequest{
		EnrollmentToken: token,
		Name:            "handshake-node",
		CsrPem:          pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: csr2DER}),
	})
	if status.Code(err) != codes.Unauthenticated {
		t.Fatalf("reused token: err = %v, want Unauthenticated", err)
	}
}

func verifyClientCert(t *testing.T, ca *nodes.CA, certPEM []byte, wantID uuid.UUID) {
	t.Helper()
	block, _ := pem.Decode(certPEM)
	if block == nil {
		t.Fatal("client cert is not PEM")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cert.Verify(x509.VerifyOptions{
		Roots:     ca.CertPool(),
		KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}); err != nil {
		t.Fatalf("client cert does not chain to panel CA: %v", err)
	}
	gotID, ok := nodes.NodeIDFromCert(cert)
	if !ok || gotID != wantID {
		t.Fatalf("cert identity = %v (%v), want %s", gotID, ok, wantID)
	}
}
