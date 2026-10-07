<div align="center">

# ⛏️ Quarry

### Minecraft server management, modernized.

**An open-source server panel — live console, files, players, maps, tunnels
and domain routing, in one clean web interface.**

[![License: MIT](https://img.shields.io/badge/License-MIT-10b981?style=flat-square)](LICENSE)
[![Go](https://img.shields.io/badge/Go-1.26-00ADD8?style=flat-square&logo=go&logoColor=white)](https://go.dev)
[![SvelteKit](https://img.shields.io/badge/SvelteKit-2-FF3E00?style=flat-square&logo=svelte&logoColor=white)](https://kit.svelte.dev)
[![Docker](https://img.shields.io/badge/Docker-required-2496ED?style=flat-square&logo=docker&logoColor=white)](https://docs.docker.com/get-docker/)

**[Quick install](#-quick-install) · [Features](#-features) · [How it works](#-how-it-works) · [Tunnels](#-make-it-public) · [Development](#-development)**

</div>

---

## Why Quarry

Pterodactyl is showing its age. Quarry is what a Minecraft panel looks like
when it's designed today: one control plane plus a node agent, live
everything, and public hosting that doesn't require a networking degree.

| | |
|---|---|
| **🖥 Live console** | Streaming output over WebSocket with persistent history — restart the panel, your log is still there. |
| **👥 Players & moderation** | Online list with skin heads, kick / ban / ban-IP / op, whitelist — all without leaving the panel. |
| **🗺 BlueMap preinstalled** | Every Paper/Fabric server gets a live 3D web map automatically, with its own port and a Map tab. |
| **🌍 One port, many servers** | The handshake router reads the hostname in each client's handshake and routes `survival.example.com` and `creative.example.com` to different servers on the same port. |
| **🔌 Make it public** | Three tunnel providers built in — playit.gg, bore, frp — for hosting without port forwarding. |
| **🧩 Plugin explorer** | Search Modrinth in the panel, see descriptions and thumbnails, install with checksum verification. |
| **📦 Version switcher** | Move a server to a newer Minecraft version or another runner — worlds, configs and ports stay. |
| **📈 Activity graphs** | CPU, RAM and player counts over time, live on every server page. |
| **🔐 Real security** | mTLS between panel and nodes, Argon2id passwords, sealed secrets, SSRF-guarded fetches, hardened containers (no caps, read-only rootfs, non-root). |

## 🚀 Quick install

One command — the interactive guide asks where to install, which ports to
use, and creates your admin account:

```bash
curl -fsSL https://raw.githubusercontent.com/Nefnief-tech/quary/main/install.sh -o install.sh
bash install.sh
```

> **You need:** Docker (with the compose plugin) and git. Everything else —
> Go, Node, the database — is built and run inside containers.

<details>
<summary><b>What the installer does</b></summary>

1. Checks Docker and Compose
2. Asks where to install, which ports to use, and your admin credentials
   (the password can be generated for you)
3. Clones this repository and writes a private <code>deploy/.env</code> with
   random database secrets
4. Builds and starts the stack: PostgreSQL, panel, web UI, node daemon
5. Waits for health checks and creates your admin account
6. Prints your panel URL and login

<code>bash install.sh update</code> — pull and rebuild in place.<br>
<code>bash install.sh uninstall</code> — stop, remove volumes, optionally wipe data.

</details>

<details>
<summary><b>Manual install</b></summary>

```bash
git clone https://github.com/Nefnief-tech/quary.git && cd quary
# create deploy/.env (see the variable names in deploy/docker-compose.yml)
docker compose --env-file deploy/.env -f deploy/docker-compose.yml up -d --build
# create your admin (credentials via env, never argv):
docker exec -e QUARRY_ADMIN_USERNAME=admin -e QUARRY_ADMIN_PASSWORD=... quarry-panel-1 panel admin create
```

</details>

## 🖼 The panel

Open the web UI and you get your server grid with live activity sparklines.
Each server page has:

- **Console** — streaming, persistent, with a command input
- **Players** — skin heads, moderation actions, whitelist, bans
- **Files** — browser file manager with editor
- **Plugins** — Modrinth explorer with thumbnails and one-click install
- **Map** — the preinstalled BlueMap web view, plus the public link
- **Settings** — rename, memory/CPU, port moves, version switching, domains

The sidebar shows live CPU / RAM / players charts, the server's public
address once a tunnel is active, and its routed domains.

## 🔌 Make it public

Three ways to let players in without touching your router, all managed from
the server's **Public access** card:

| Provider | Account | Limits | Best for |
|---|---|---|---|
| **[playit.gg](https://playit.gg)** | free | 1 tunnel | The main server — stable address, claim-link setup |
| **[bore](https://github.com/ekzhang/bore)** | none | port rotates | Instant extra tunnels, zero setup |
| **[frp](https://github.com/fatedier/frp)** | your own relay | unlimited | You run (or rent) an frps — full control |

Add a **domain** to any server (server page → *Connect domains*) and the
handshake router sends players who type that domain to the right server —
all through one port.

## 🏗 How it works

```
 ┌──────────────────────────────  your machine  ──────────────────────────────┐
 │                                                                            │
 │   ┌─────────┐   /api + ws   ┌─────────┐   mTLS gRPC   ┌──────────────┐    │
 │   │  web UI  ├──────────────►│  panel  │◄────────────►│ node daemon  │    │
 │   └─────────┘    Caddy      └────┬────┘   enroll +    └──────┬───────┘    │
 │                            ┌─────┴─────┐   command channel  │ supervises │
 │                            │ postgres  │                    ▼            │
 │                            └───────────┘             ┌──────────────┐     │
 │      playit / bore / frp tunnels ───────────────────►│  Minecraft   │     │
 │      handshake router :25565 (domain → server) ─────►│  containers  │     │
 │                                                      └──────────────┘     │
 └────────────────────────────────────────────────────────────────────────────┘
```

- **Panel** (Go) — API, auth, orchestration, PostgreSQL via sqlc
- **Node daemon** (Go) — enrolls over mTLS, runs each server in a hardened
  container, streams console/stats/events back over one gRPC channel
- **Web** (SvelteKit + Tailwind) — served by Caddy, proxies API and WebSockets
- **Minecraft servers** — Vanilla, Paper or Fabric, checksum-verified
  downloads, isolated containers

## 🛡 Security model

- Panel↔daemon traffic is **mutual TLS** with single-use enrollment tokens
- Passwords hashed with **Argon2id**; sessions are opaque tokens (SHA-256 at rest)
- Provider tokens and secrets are **AES-GCM sealed** at rest
- Every outbound fetch passes an **SSRF guard**; marketplace installs are
  checksum-verified
- Server containers run **non-root, no capabilities, read-only rootfs**,
  with memory/CPU limits
- Panel-issued bans and whitelist entries are validated to prevent
  console injection

## 🧪 Development

```bash
make dev                 # dev stack: postgres + panel + daemon
pnpm --dir web run dev   # web UI with hot reload on :5173
make test                # unit + integration tests (testcontainers)
make lint                # golangci-lint
```

First boot: register the first user in the UI (it becomes admin), or use
`panel admin create`. See [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) and
the [ADR index](docs/adr/) for design decisions.

## 🗺 Roadmap

- [ ] Backups — scheduled + manual, retention, restore, download
- [ ] Subusers — share a single server with scoped permissions
- [ ] Schedules — cron restarts, timed messages, backup plans
- [ ] Modpacks — Modrinth `.mrpack` installs
- [ ] SFTP access
- [ ] Webhook / Discord notifications

## 📄 License

[MIT](LICENSE) — do whatever you like, attribution appreciated.

---

<div align="center">
<sub>Built with Go, SvelteKit and an unhealthy amount of Minecraft.</sub>
</div>
