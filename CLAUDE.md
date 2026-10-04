# CLAUDE.md

Quarry — an open-source Minecraft server management panel (MIT). Single Go module `quarry.dev/quarry` with two binaries (panel, daemon), a PostgreSQL control plane, and a SvelteKit web SPA.

## Stack

| Layer | Choice |
|---|---|
| Backend + daemon | Go 1.26, stdlib `net/http` router (Go 1.22+ patterns), `slog` JSON logging |
| DB | PostgreSQL, `pgx/v5` pool, **sqlc** queries, **goose** migrations (library mode, embedded, reversible) |
| Job queue | River (Postgres-backed, Phase 1) — no Redis |
| Realtime | Postgres LISTEN/NOTIFY + in-process WebSocket hub (Phase 1) |
| Panel ↔ daemon | gRPC + mTLS, protobuf in `/proto` (buf), generated Go committed |
| Isolation | Docker via official SDK behind `ContainerRuntime` (Phase 1) |
| Frontend | SvelteKit + Tailwind + xterm.js (`/web`) |
| Auth | Argon2id (`alexedwards/argon2id`), opaque sessions, scoped API keys; TOTP/WebAuthn/OIDC later phases |

## Layout

```
/panel/cmd/panel        panel binary: serve | migrate | admin create
/panel/internal/        config, logging, db (migrations+queries+gen), auth, rbac, api, nodes, audit
/daemon/cmd/daemon      node agent binary
/daemon/internal/       config, state (identity persistence), client (gRPC)
/proto                  buf config + .proto sources; generated Go under /proto/gen (committed)
/internal               shared across binaries: pathsanitize, ssrf, seal, tokenx
/web                    SvelteKit SPA (admin + user areas)
/runners /modules       runner definitions / first-party modules
/docs  /docs/adr        architecture doc and ADRs
/deploy                 docker-compose.dev.yml, Dockerfiles, .env.example
```

## Commands

```sh
make build       # bin/panel + bin/daemon
make test        # unit + integration (integration tests use testcontainers; needs Docker)
make test-unit   # unit only
make lint        # golangci-lint
make migrate     # apply migrations (QUARRY_DATABASE_URL)
make proto       # regenerate protobuf (buf)
make dev         # docker compose dev stack (postgres + panel + daemon)
```

Tools used locally: `buf`, `sqlc`, `golangci-lint`, `protoc-gen-go`, `protoc-gen-go-grpc` (all installable via `go install`; see CI for exact usage).

## Conventions (enforced in review)

1. **SQL:** every query goes through sqlc (`panel/internal/db/queries/*.sql`). Never build SQL by string concatenation, fmt, or f-strings. Migrations are goose SQL with both `-- +goose Up` and `-- +goose Down`; every migration must be reversible. Real columns for anything filtered/queried; JSONB only for runner/module extensible config.
2. **Credentials:** read from environment only (`QUARRY_*`). No usable credentials, tokens, or passwords anywhere in source, examples, tests, or committed files — placeholders in `.env.example` only.
3. **Secrets at rest:** sensitive material persisted by the panel (e.g. the CA private key) is sealed with AES-GCM via `internal/seal`, keyed from `QUARRY_SECRETS_KEY` (random key generated into the data dir when unset, with a warning).
4. **Never log secrets or tokens.** Redact in request logging; never put tokens in URLs. Enrollment/API/session tokens appear only at creation time in API responses, never in logs or audit metadata.
5. **Outbound HTTP:** every server-side URL fetch must go through `internal/ssrf` (http/https only; resolves and rejects loopback/private/reserved hosts before dialing). No direct `http.Get` on user-influenced URLs.
6. **Filesystem access to server data** must go through `internal/pathsanitize` (traversal + symlink escape protection). Dedicated tests exist; extend them when touching it.
7. **Authz:** all permission checks flow through one middleware layer (`panel/internal/rbac` + api middleware). New endpoints declare required permissions; sensitive actions write to `audit_log`.
8. **Commits:** conventional commits, small and focused (`feat(panel):`, `fix(daemon):`, `docs:`, `chore:` …).
9. **Testing:** unit tests beside code; integration tests behind the `integration` build tag using testcontainers (Postgres) and a real Docker daemon where needed. Write tests as you go.
10. **Scope discipline:** don't build features beyond the current phase. Phase plan lives in `docs/ARCHITECTURE.md`; phase boundaries are hard stops for review.
11. Prefer boring, well-maintained libraries. No new dependency for something small.

## Security invariants (non-negotiable)

Containers: non-root, dropped capabilities, no privileged mode, read-only mounts where possible, per-server network isolation. mTLS + rotating node tokens for panel↔daemon. Rate limiting on auth. Strict path sanitization in file manager/SFTP with traversal/symlink tests. TLS everywhere between panel and daemon.

## Phase plan

Phase 0: foundations (auth, RBAC skeleton, schema, gRPC+mTLS handshake, node online) → Phase 1: MVP (runners vanilla/paper/fabric, server lifecycle, console WS, stats, files, minimal UI) → stop for review → Phases 2–5 after owner review (admin completeness, subusers, more runners, backups/SFTP, marketplace, module system, polish/OIDC/webhooks/multi-node).
