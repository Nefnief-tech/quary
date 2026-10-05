# Quarry

Fast, modular, open-source Minecraft server management panel. A modern alternative to Pterodactyl.

- **Panel** — Go control plane: auth, RBAC, nodes, jobs, WebSocket hub. All state in PostgreSQL.
- **Daemon** — Go agent on each game host: Docker containers, files, console, stats, backups. Dials out to the panel over gRPC with mTLS.
- **Web** — SvelteKit SPA with admin and user areas.

MIT licensed. See [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) for the design and [docs/adr/](docs/adr/) for decisions.

## Status

Phase 1 (MVP) — runners (Vanilla/Paper/Fabric), server lifecycle with live console over WebSocket, stats, file manager, and a SvelteKit UI. Create a Paper server in the browser, watch it boot in the console, run commands, and stop it cleanly.

## Development

Requirements: Go 1.26+, Docker, Node 22+ with pnpm, make.

```sh
make dev       # postgres + panel + daemon (API on :8080, gRPC on :9010)
make web-dev   # SvelteKit UI on :5173 (proxies /api to the panel)
```

Bootstrap on first boot:

1. Open http://localhost:5173 and register the first user (it becomes the admin), or:
   `curl -s localhost:8080/api/v1/auth/register -d '{"username":"admin","password":"…"}'`
2. In **Admin**, create a node, copy the enrollment token into `deploy/.env` as
   `QUARRY_ENROLLMENT_TOKEN=…`, and restart the daemon: `docker compose --env-file deploy/.env -f deploy/docker-compose.dev.yml up -d daemon`
3. Add an allocation range (e.g. ip `0.0.0.0`, ports 25565–25575) on the node.
4. Create a server — pick a runner and Minecraft version, then open the console.

Useful targets: `make build`, `make test` (unit + integration; needs Docker), `make test-unit`,
`make lint`, `make migrate`, `make proto`, `make web-build`.
