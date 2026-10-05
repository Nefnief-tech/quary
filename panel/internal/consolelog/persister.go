// Console history persistence: console lines streaming through the hub are
// buffered and flushed to Postgres in small batches, so the panel can
// replay history after a restart. Writes are best-effort; losing a line is
// acceptable, blocking the event path is not.
package consolelog

import (
	"context"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"quarry.dev/quarry/panel/internal/db/gen"
)

const (
	queueCap    = 8192
	flushEvery  = 750 * time.Millisecond
	flushBatch  = 200
	keepPerSrv  = 500
	prunePeriod = 10 * time.Minute
)

type entry struct {
	serverID uuid.UUID
	at       time.Time
	line     string
}

// Persister batches console lines into the console_lines table.
type Persister struct {
	pool *pgxpool.Pool
	q    *gen.Queries
	log  *slog.Logger
	ch   chan entry
}

func NewPersister(pool *pgxpool.Pool, q *gen.Queries, log *slog.Logger) *Persister {
	return &Persister{pool: pool, q: q, log: log, ch: make(chan entry, queueCap)}
}

// Record enqueues one console line; never blocks the event path.
func (p *Persister) Record(serverID uuid.UUID, at time.Time, line string) {
	select {
	case p.ch <- entry{serverID, at, line}:
	default:
	}
}

// Run flushes batches until ctx is cancelled, and prunes old lines.
func (p *Persister) Run(ctx context.Context) {
	ticker := time.NewTicker(flushEvery)
	pruner := time.NewTicker(prunePeriod)
	defer ticker.Stop()
	defer pruner.Stop()
	for {
		select {
		case <-ctx.Done():
			p.flush(context.Background())
			return
		case <-ticker.C:
			p.flush(ctx)
		case <-pruner.C:
			if _, err := p.q.PruneConsoleLines(ctx); err != nil {
				p.log.Warn("console prune failed", "err", err)
			}
		}
	}
}

func (p *Persister) flush(ctx context.Context) {
	for {
		batch := make([]entry, 0, flushBatch)
	loop:
		for len(batch) < flushBatch {
			select {
			case e := <-p.ch:
				batch = append(batch, e)
			default:
				break loop
			}
		}
		if len(batch) == 0 {
			return
		}
		tx, err := p.pool.Begin(ctx)
		if err != nil {
			p.log.Warn("console flush begin failed", "err", err)
			return
		}
		qtx := p.q.WithTx(tx)
		for _, e := range batch {
			if err := qtx.InsertConsoleLine(ctx, gen.InsertConsoleLineParams{
				ServerID: e.serverID, At: e.at, Line: e.line,
			}); err != nil {
				p.log.Warn("console insert failed", "err", err)
			}
		}
		if err := tx.Commit(ctx); err != nil {
			p.log.Warn("console flush commit failed", "err", err)
			return
		}
	}
}
