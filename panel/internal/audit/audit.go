// Package audit writes sensitive actions to audit_log. Callers must never
// include secrets or tokens in entries.
package audit

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"quarry.dev/quarry/panel/internal/db/gen"
)

const (
	ActorUser   = "user"
	ActorSystem = "system"
	ActorNode   = "node"
)

type Entry struct {
	UserID       *uuid.UUID
	Actor        string
	Action       string
	ResourceType string
	ResourceID   string
	IP           string
	Metadata     map[string]any
}

type Logger struct {
	q   *gen.Queries
	log *slog.Logger
}

func New(q *gen.Queries, log *slog.Logger) *Logger {
	return &Logger{q: q, log: log}
}

// Record writes an entry. Failures are logged, never fatal: an audit write
// must not take down the request path.
func (l *Logger) Record(ctx context.Context, e Entry) {
	meta := []byte("{}")
	if e.Metadata != nil {
		b, err := json.Marshal(e.Metadata)
		if err == nil {
			meta = b
		}
	}
	params := gen.InsertAuditEntryParams{
		Actor:        e.Actor,
		Action:       e.Action,
		ResourceType: e.ResourceType,
		ResourceID:   e.ResourceID,
		Ip:           e.IP,
		Metadata:     meta,
	}
	if e.UserID != nil {
		params.UserID = e.UserID
	}
	if err := l.q.InsertAuditEntry(ctx, params); err != nil {
		l.log.Error("audit write failed", "action", e.Action, "err", err)
	}
}

// RecordAsync writes from a goroutine with a detached context, for
// fire-and-forget call sites.
func (l *Logger) RecordAsync(e Entry) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		l.Record(ctx, e)
	}()
}
