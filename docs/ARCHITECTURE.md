# Quarry Architecture

Status: living document. Updated when components change. Decisions with tradeoffs live in `docs/adr/`.

## Overview

Quarry is a Minecraft server management panel: a control plane (panel), per-host agents (daemons), and a web SPA.

```
                ┌────────────────────────────┐
                │        Web SPA (SvelteKit) │
                │   admin area │ user area   │
                └──────┬─────────────┬───────┘
                       │ HTTPS (REST + WebSocket)
┌──────────┐  gRPC+mTLS│             ▼
│ Postgres │◄──────────┼─────┌──────────────┐
└────▲─────┘           │     │  Panel (Go)  │
     │                 │     │  control plane: auth, RBAC, modules, jobs, WS hub
     └─────────────────┼─────┴──────────────┘
                       │ outbound gRPC streams (mTLS)
              ┌────────┴─────────┐
              │  Daemon (Go)     │  on each game host
              │  Docker containers, files, SFTP, console, stats, backups
              └──────────────────┘
```

- **Panel** (`/panel`): REST API, auth (Argon2id sessions + scoped API keys), RBAC, node registry, River job queue (Phase 1), WebSocket hub (Phase 1), module registry. All durable state in PostgreSQL. The panel never touches Minecraft files or containers.
- **Daemon** (`/daemon`): runs on each game host. Manages Docker containers via a `ContainerRuntime` interface, server files, console streams, stats, backups. Dials **out** to the panel over gRPC with mTLS — no inbound ports on the game host.
- **Web** (`/web`): one SvelteKit SPA split by route into admin and user areas; permissions gate both UI and API.

## Data flow (control plane)

1. Browser authenticates (session cookie) or uses a scoped API key; every request passes authn → authz middleware → handler; sensitive actions append to `audit_log`.
2. Mutations that involve work on a node (install, power actions, backups) enqueue a River job (Phase 1); workers call the daemon over gRPC.
3. Realtime (console, stats) flows daemon → panel over gRPC streams into an in-process hub keyed by server; browsers subscribe over WebSocket with short-lived signed tokens.

## Panel ↔ daemon connection

The daemon dials the panel; the panel never needs to reach the host.

```
Daemon                                        Panel (gRPC :9010, TLS)
  │ first boot: load or generate ECDSA key        │ CA generated on first panel boot
  │                                               │ (key sealed at rest via internal/seal)
  │ RegisterNode(enrollment_token, CSR) ─────────►│ validate single-use token,
  │                                               │ sign CSR → node client cert (URI SAN
  │ ◄───────────── node_id, cert, ca ─────────────│ quarry://node/<uuid>), audit "node.enrolled"
  │ persist identity (0600), redial with mTLS     │
  │ Heartbeat stream every 10s ──────────────────►│ update nodes.last_seen_at, daemon_version
  │ ◄──────────────────────────── ack / config ───│ online = last_seen < 35s ago
```

- Enrollment tokens are per-node, single-use, revocable/rotatable from the admin API; stored as SHA-256 hashes.
- All post-registration RPCs require a client certificate issued by the panel CA; the node identity comes from the certificate's URI SAN, never from request payloads.
- Lost daemon identity → admin rotates the token and re-enrolls.

## Server lifecycle (Phase 1)

State machine persisted in `servers.status`:

```
installing → offline → starting → running → stopping → offline
                  ▲                                 │
                  └────────── crashed ◄─────────────┘ (failed exit)
suspended (admin action, blocks all power ops)
```

Transitions are explicit, validated, and persisted; invalid transitions are rejected. Graceful stop sends `stop` to the console, escalates to hard kill after a timeout. Crash detection triggers configurable auto-restart with exponential backoff.

## Domain model (PostgreSQL)

`users, sessions, api_keys, roles, permissions, role_permissions, nodes, node_tokens, allocations, servers, server_members, runners, runner_versions, server_plugins, marketplace_sources, marketplace_cache, backups, schedules, audit_log, module_state`.

Rules: real columns for anything filtered or queried; JSONB only for runner- and module-specific config; every migration reversible (goose up/down).

## Module API sketch

Two tiers (Phase 4):

1. **First-party modules** — compiled in, registered via typed interfaces at startup, enable/disable in the admin panel. Manifest: name, version, required permissions, config schema, migrations namespace.

```go
type Module interface {
    Manifest() Manifest
    Register(ctx context.Context, host Host) error // host exposes registry points
}

type Host interface {
    Runners() RunnerRegistry
    MarketplaceProviders() ProviderRegistry
    BackupTargets() TargetRegistry
    AuthProviders() AuthRegistry
    Webhooks() Emitter          // typed events
    UI() SlotRegistry           // named extension slots (e.g. server-page tabs)
}
```

2. **Third-party extensions** — out of process (gRPC or WASM; ADR due before Phase 4), sandboxed so a bad extension can't crash the panel; same extension points, exposed over a narrow protocol.

Core extension interfaces (Phase 1+):

```go
type Runner interface {
    ID() string
    ListVersions(ctx) ([]Version, error)           // upstream APIs, cached
    Resolve(ctx, version string) (Artifact, error) // download URL + checksum
    JavaVersion(mcVersion string) int              // 8/17/21/25
    LaunchCommand(s Server) []string               // memory flags, optional Aikar flags
    PluginKind() PluginKind                        // plugins | mods | none
    ConfigFiles() []ConfigSchema                   // server.properties, paper-global.yml, …
}

type MarketplaceProvider interface {
    ID() string
    Search(ctx, Query) ([]Result, error)           // loader, mc version, category, sort
    Resolve(ctx, projectID, versionID string) (Download, error)
}
```

## Security model

- Containers: non-root, dropped capabilities, no privileged mode, read-only mounts where possible, per-server network isolation, cgroup CPU/memory/IO limits + disk quota.
- Panel↔daemon: mTLS with panel-issued certs + rotating single-use enrollment tokens.
- Web: Argon2id hashes, HttpOnly SameSite session cookies, scoped API keys (hash at rest), login rate limiting, CSRF protection (Phase 1 UI), short-lived signed WebSocket tokens.
- Path sanitization (traversal + symlink escape) with dedicated tests for every file path; SFTP (later phase) enforces the same permission model.
- Secrets sealed at rest; never logged.

## ADR index

- ADR-0001: MIT license
- ADR-0002: SvelteKit frontend
- ADR-0003: single Go module
- (pending, before Phase 4): third-party extension sandboxing — WASM vs out-of-process gRPC
