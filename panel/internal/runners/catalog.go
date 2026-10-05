// Catalog caches upstream version lists per runner and resolves artifacts.
package runners

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"time"

	"quarry.dev/quarry/panel/internal/db/gen"
)

// Catalog is the version-caching front end for all registered runners.
type Catalog struct {
	q       *gen.Queries
	log     *slog.Logger
	runners map[string]Runner
}

func NewCatalog(q *gen.Queries, log *slog.Logger) *Catalog {
	c := &Catalog{q: q, log: log, runners: map[string]Runner{}}
	c.Register(NewVanilla())
	c.Register(NewPaper())
	c.Register(NewFabric())
	return c
}

// Register adds a runner (first-party in the default constructor; custom and
// test runners go through here too).
func (c *Catalog) Register(r Runner) {
	c.runners[r.ID()] = r
}

func (c *Catalog) IDs() []string {
	out := make([]string, 0, len(c.runners))
	for id := range c.runners {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

func (c *Catalog) Get(id string) (Runner, bool) {
	r, ok := c.runners[id]
	return r, ok
}

// Versions returns the cached catalog for a runner, refreshing it from the
// upstream API when empty or stale (older than a day).
func (c *Catalog) Versions(ctx context.Context, runnerID string) ([]gen.RunnerVersion, error) {
	r, ok := c.runners[runnerID]
	if !ok {
		return nil, fmt.Errorf("unknown runner %q", runnerID)
	}
	fresh, err := c.q.FreshRunnerVersionCount(ctx, runnerID)
	if err != nil {
		return nil, err
	}
	if fresh == 0 {
		if err := c.Refresh(ctx, r); err != nil {
			return nil, err
		}
	}
	versions, err := c.q.ListRunnerVersions(ctx, runnerID)
	if err != nil {
		return nil, err
	}
	sort.Slice(versions, func(i, j int) bool {
		if versions[i].Stable != versions[j].Stable {
			return versions[i].Stable
		}
		return versionLess(versions[j].McVersion, versions[i].McVersion)
	})
	return versions, nil
}

// Refresh re-reads the upstream version list and rewrites the cache rows.
func (c *Catalog) Refresh(ctx context.Context, r Runner) error {
	versions, err := r.ListVersions(ctx)
	if err != nil {
		return fmt.Errorf("refresh runner %s: %w", r.ID(), err)
	}
	for _, v := range versions {
		var published *time.Time
		if !v.Published.IsZero() {
			published = &v.Published
		}
		if err := c.q.UpsertRunnerVersion(ctx, gen.UpsertRunnerVersionParams{
			Runner:      r.ID(),
			McVersion:   v.MCVersion,
			Stable:      v.Stable,
			JavaVersion: int32(r.JavaVersion(v.MCVersion)),
			PublishedAt: published,
		}); err != nil {
			return err
		}
	}
	c.log.Info("runner catalog refreshed", "runner", r.ID(), "versions", len(versions))
	return nil
}

// Resolve returns the runner and its checksummed artifact for a version.
func (c *Catalog) Resolve(ctx context.Context, runnerID, mcVersion string) (Runner, Artifact, error) {
	r, ok := c.runners[runnerID]
	if !ok {
		return nil, Artifact{}, fmt.Errorf("unknown runner %q", runnerID)
	}
	artifact, err := r.Resolve(ctx, mcVersion)
	if err != nil {
		return nil, Artifact{}, fmt.Errorf("resolve %s %s: %w", runnerID, mcVersion, err)
	}
	return r, artifact, nil
}
