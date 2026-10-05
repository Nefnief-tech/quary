package nodes

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"quarry.dev/quarry/internal/tokenx"
	"quarry.dev/quarry/panel/internal/audit"
	"quarry.dev/quarry/panel/internal/db/gen"
	nodesv1 "quarry.dev/quarry/proto/gen/go/quarry/node/v1"
)

var (
	ErrInvalidToken = errors.New("invalid, used or expired enrollment token")
	ErrNameMismatch = errors.New("daemon name does not match the node record")
	ErrInvalidCSR   = errors.New("invalid certificate signing request")
	ErrNotNode      = errors.New("request is not from an enrolled node")
)

// Service implements node enrollment and heartbeat bookkeeping.
type Service struct {
	pool     *pgxpool.Pool
	q        *gen.Queries
	ca       *CA
	auditLog *audit.Logger
	log      *slog.Logger
}

func NewService(pool *pgxpool.Pool, q *gen.Queries, ca *CA, auditLog *audit.Logger, log *slog.Logger) *Service {
	return &Service{pool: pool, q: q, ca: ca, auditLog: auditLog, log: log}
}

// Enroll validates a single-use token in a transaction, signs the daemon's
// CSR and burns the token. The token is only ever compared by hash.
func (s *Service) Enroll(ctx context.Context, req *nodesv1.RegisterNodeRequest) (uuid.UUID, []byte, []byte, error) {
	if req.GetEnrollmentToken() == "" {
		return uuid.Nil, nil, nil, ErrInvalidToken
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return uuid.Nil, nil, nil, fmt.Errorf("begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	qtx := s.q.WithTx(tx)
	tok, err := qtx.GetNodeTokenForUpdate(ctx, tokenx.Hash(req.GetEnrollmentToken()))
	if err != nil {
		return uuid.Nil, nil, nil, ErrInvalidToken
	}
	node, err := qtx.GetNode(ctx, tok.NodeID)
	if err != nil {
		return uuid.Nil, nil, nil, ErrInvalidToken
	}
	if req.GetName() != node.Name {
		return uuid.Nil, nil, nil, fmt.Errorf("%w: configured %q, node record %q", ErrNameMismatch, req.GetName(), node.Name)
	}

	certPEM, err := s.ca.IssueClientCert(req.GetCsrPem(), node.ID)
	if err != nil {
		return uuid.Nil, nil, nil, fmt.Errorf("%w: %v", ErrInvalidCSR, err)
	}

	if err := qtx.MarkNodeTokenUsed(ctx, tok.ID); err != nil {
		return uuid.Nil, nil, nil, fmt.Errorf("burn token: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return uuid.Nil, nil, nil, fmt.Errorf("commit enrollment: %w", err)
	}

	s.auditLog.Record(ctx, audit.Entry{
		Actor: audit.ActorNode, Action: "node.enrolled",
		ResourceType: "node", ResourceID: node.ID.String(),
		Metadata: map[string]any{
			"daemon_version": req.GetInfo().GetDaemonVersion(),
			"hostname":       req.GetInfo().GetHostname(),
		},
	})
	return node.ID, s.ca.CACertPEM(), certPEM, nil
}

// Beat records a heartbeat for an authenticated node.
func (s *Service) Beat(ctx context.Context, nodeID uuid.UUID, info *nodesv1.DaemonInfo) error {
	return s.q.TouchNodeHeartbeat(ctx, gen.TouchNodeHeartbeatParams{
		ID:            nodeID,
		DaemonVersion: info.GetDaemonVersion(),
		Hostname:      info.GetHostname(),
		DockerVersion: info.GetDockerVersion(),
		Os:            info.GetOs(),
		Arch:          info.GetArch(),
	})
}

func grpcError(err error) error {
	switch {
	case errors.Is(err, ErrInvalidToken), errors.Is(err, ErrNotNode):
		return status.Error(codes.Unauthenticated, err.Error())
	case errors.Is(err, ErrNameMismatch):
		return status.Error(codes.FailedPrecondition, err.Error())
	case errors.Is(err, ErrInvalidCSR):
		return status.Error(codes.InvalidArgument, err.Error())
	default:
		return status.Error(codes.Internal, "internal error")
	}
}
