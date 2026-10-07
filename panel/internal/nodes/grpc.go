package nodes

import (
	"context"
	"crypto/tls"
	"io"
	"log/slog"
	"net"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"

	"quarry.dev/quarry/panel/internal/config"
	nodesv1 "quarry.dev/quarry/proto/gen/go/quarry/node/v1"
)

// GRPCServer implements nodesv1.NodeAgentServiceServer.
type GRPCServer struct {
	nodesv1.UnimplementedNodeAgentServiceServer
	svc     *Service
	log     *slog.Logger
	chanMgr *ChannelManager
	// onEvent receives daemon events (console, stats, status).
	onEvent func(*nodesv1.Event)
	// onNodeOnline is called when a daemon channel opens (config push).
	onNodeOnline func(uuid.UUID)
}

func NewGRPCServer(svc *Service, log *slog.Logger, chanMgr *ChannelManager, onEvent func(*nodesv1.Event), onNodeOnline func(uuid.UUID)) *GRPCServer {
	return &GRPCServer{svc: svc, log: log, chanMgr: chanMgr, onEvent: onEvent, onNodeOnline: onNodeOnline}
}

func (g *GRPCServer) RegisterNode(ctx context.Context, req *nodesv1.RegisterNodeRequest) (*nodesv1.RegisterNodeResponse, error) {
	nodeID, caPEM, certPEM, err := g.svc.Enroll(ctx, req)
	if err != nil {
		g.log.Warn("node enrollment failed", "err", err)
		return nil, grpcError(err)
	}
	g.log.Info("node enrolled", "node_id", nodeID)
	return &nodesv1.RegisterNodeResponse{
		NodeId:  nodeID.String(),
		CaPem:   caPEM,
		CertPem: certPEM,
	}, nil
}

func (g *GRPCServer) Heartbeat(stream nodesv1.NodeAgentService_HeartbeatServer) error {
	nodeID, ok := NodeIDFromStreamContext(stream.Context())
	if !ok {
		return status.Error(codes.Unauthenticated, ErrNotNode.Error())
	}
	ctx := stream.Context()
	for {
		req, err := stream.Recv()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		if err := g.svc.Beat(ctx, nodeID, req.GetInfo()); err != nil {
			g.log.Error("heartbeat update failed", "node_id", nodeID, "err", err)
			return status.Error(codes.Internal, "heartbeat rejected")
		}
		if err := stream.Send(&nodesv1.HeartbeatResponse{
			ServerTimeUnixMilli: time.Now().UnixMilli(),
		}); err != nil {
			return err
		}
	}
}

// Channel serves one daemon's long-lived command stream.
func (g *GRPCServer) Channel(stream nodesv1.NodeAgentService_ChannelServer) error {
	nodeID, ok := NodeIDFromStreamContext(stream.Context())
	if !ok {
		return status.Error(codes.Unauthenticated, ErrNotNode.Error())
	}
	conn, done := g.chanMgr.Register(nodeID)
	defer done()
	g.log.Info("node channel open", "node_id", nodeID)
	if g.onNodeOnline != nil {
		g.onNodeOnline(nodeID)
	}

	// Forward panel commands down to the daemon.
	forwardDone := make(chan struct{})
	go func() {
		defer close(forwardDone)
		for msg := range conn.send {
			if err := stream.Send(msg); err != nil {
				return
			}
		}
	}()

	for {
		msg, err := stream.Recv()
		if err != nil {
			g.log.Info("node channel closed", "node_id", nodeID, "err", err)
			return err
		}
		switch payload := msg.GetPayload().(type) {
		case *nodesv1.ChannelRequest_Result:
			conn.deliver(payload.Result)
		case *nodesv1.ChannelRequest_Event:
			if g.onEvent != nil && payload.Event != nil {
				g.onEvent(payload.Event)
			}
		}
	}
}

// NodeIDFromStreamContext pulls the authenticated node identity out of the
// verified TLS peer certificate.
func NodeIDFromStreamContext(ctx context.Context) (uuid.UUID, bool) {
	p, ok := peer.FromContext(ctx)
	if !ok {
		return uuid.Nil, false
	}
	tlsInfo, ok := p.AuthInfo.(credentials.TLSInfo)
	if !ok || len(tlsInfo.State.VerifiedChains) == 0 {
		return uuid.Nil, false
	}
	return NodeIDFromCert(tlsInfo.State.VerifiedChains[0][0])
}

// NewListener builds the mTLS gRPC listener for node traffic.
func NewListener(cfg config.Config, ca *CA) (net.Listener, *grpc.Server, error) {
	serverCert, err := ca.IssueServerCert(cfg.TLSDNSNames)
	if err != nil {
		return nil, nil, err
	}
	tlsCfg := &tls.Config{
		MinVersion:   tls.VersionTLS12,
		Certificates: []tls.Certificate{serverCert},
		ClientAuth:   tls.VerifyClientCertIfGiven,
		ClientCAs:    ca.CertPool(),
	}
	lis, err := net.Listen("tcp", cfg.GRPCAddr)
	if err != nil {
		return nil, nil, err
	}
	gs := grpc.NewServer(grpc.Creds(credentials.NewTLS(tlsCfg)))
	return lis, gs, nil
}

var _ nodesv1.NodeAgentServiceServer = (*GRPCServer)(nil)
