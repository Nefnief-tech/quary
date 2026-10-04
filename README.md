# Quarry

Fast, modular, open-source Minecraft server management panel. A modern alternative to Pterodactyl.

- **Panel** — Go control plane: auth, RBAC, nodes, jobs, WebSocket hub. All state in PostgreSQL.
- **Daemon** — Go agent on each game host: Docker containers, files, console, stats, backups. Dials out to the panel over gRPC with mTLS.
- **Web** — SvelteKit SPA with admin and user areas.

MIT licensed. See [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) for the design and [docs/adr/](docs/adr/) for decisions.

## Status

Phase 0 (foundations) — panel boots, auth works, a daemon can enroll and show as online. UI, runners, and server lifecycle land in Phase 1.

## Development

Requirements: Go 1.26+, Docker, Node 22+ (web UI lands in Phase 1), make.

```sh
cp deploy/.env.example deploy/.env   # placeholders are fine for local dev
make dev                             # postgres + panel + daemon
```

Panel API on http://localhost:8080, gRPC on :9010. Bootstrap the first admin:

```sh
curl -s localhost:8080/api/v1/auth/register -d '{"username":"admin","password":"…"}'
```

Useful targets: `make build`, `make test` (unit + integration; needs Docker), `make test-unit`, `make lint`, `make migrate`, `make proto`.
