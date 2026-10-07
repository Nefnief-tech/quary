#!/usr/bin/env bash
# ---------------------------------------------------------------------------
# Quarry installer — a guided setup for the Quarry Minecraft server panel.
#
#   curl -fsSL https://raw.githubusercontent.com/Nefnief-tech/quary/main/install.sh -o install.sh
#   bash install.sh
#
# Requires: Docker (with the compose plugin) and git. Everything else is
# built inside containers.
# ---------------------------------------------------------------------------
set -euo pipefail

REPO_URL="https://github.com/Nefnief-tech/quary.git"
DEFAULT_PORT=8080
DEFAULT_ROUTER_PORT=25565
COMPOSE=(docker compose)

# ANSI colors as plain escape sequences; empty when stdout is not a
# terminal. Helpers print with %b so they render.
if [ -t 1 ]; then
	BOLD="\033[1m"
	DIM="\033[2m"
	GREEN="\033[32m"
	YELLOW="\033[33m"
	RED="\033[31m"
	CYAN="\033[36m"
	RESET="\033[0m"
else
	BOLD=""
	DIM=""
	GREEN=""
	YELLOW=""
	RED=""
	CYAN=""
	RESET=""
fi

say()  { printf '%b\n' "  $*"; }
ok()   { printf '%b\n' "  ${GREEN}OK${RESET} $*"; }
warn() { printf '%b\n' "  ${YELLOW}!${RESET} $*"; }
die()  { printf '%b\n' "  ${RED}x $*${RESET}" >&2; exit 1; }
hdr()  { printf '%b\n' "\n  ${BOLD}${CYAN}$*${RESET}"; }

# Prompts read from the terminal so piping this script stays interactive;
# the answer is printed to stdout for VAR= capture by the caller.
ask() { # ask <question> <default> -> answer
	local reply=""
	printf '%b\n' "  ${BOLD}$1${RESET}${2:+ ${DIM}[$2]${RESET}}"
	if [ -t 0 ]; then read -r reply || reply=""
	else read -r reply < /dev/tty || reply=""; fi
	printf '%s' "${reply:-$2}"
}
ask_secret() { # ask_secret <question> -> answer (silent)
	local reply=""
	printf '%b\n' "  ${BOLD}$1${RESET} ${DIM}(enter = generate a strong one)${RESET}"
	if [ -t 0 ]; then read -rs reply || reply=""
	else read -rs reply < /dev/tty || reply=""; fi
	printf '%s' "$reply"
	printf '%b\n' ""
}

random_hex() {
	openssl rand -hex "$1"
}

random_password() {
	openssl rand -base64 24 | tr -cd 'A-Za-z0-9' | cut -c1-20
}

# Single safe charset for anything that lands in an env file or command.
is_safe_username() {
	printf '%s' "$1" | grep -qE '^[A-Za-z0-9][A-Za-z0-9_.-]{0,31}$'
}

is_safe_password() {
	# Printable, no control characters, no leading dash, bounded length.
	[ -n "$1" ] && [ "${#1}" -le 128 ] || return 1
	case "$1" in -*) return 1 ;; esac
	printf '%s' "$1" | grep -q '^[[:print:]]*$'
}

banner() {
	printf '%b\n' "${CYAN}"
	printf '%b\n' "  ██████╗ ██╗   ██╗ █████╗ ██████╗ ██████╗ ██╗   ██╗"
	printf '%b\n' "  ██╔══██╗██║   ██║██╔══██╗██╔══██╗██╔══██╗╚██╗ ██╔╝"
	printf '%b\n' "  ██████╔╝██║   ██║███████║██████╔╝██║  ██║ ╚████╔╝ "
	printf '%b\n' "  ██╔══██╗██║   ██║██╔══██║██╔══██╗██║  ██║  ╚██╔╝  "
	printf '%b\n' "  ██║  ██║╚██████╔╝██║  ██║██║  ██║██████╔╝   ██║   "
	printf '%b\n' "  ╚═╝  ╚═╝ ╚═════╝ ╚═╝  ╚═╝╚═╝  ╚═╝╚═════╝    ╚═╝   "
	printf '%b\n' "${RESET}"
	printf '%b\n' "  ${BOLD}Minecraft server management, modernized.${RESET}"
	printf '%b\n' "  ${DIM}panel · console · files · maps · tunnels · domain routing${RESET}"
	printf '%b\n' ""
}

# ---------------------------------------------------------------------------
# prerequisite checks
# ---------------------------------------------------------------------------
check_prereqs() {
	hdr "Checking prerequisites"
	command -v docker >/dev/null 2>&1 || die "Docker is required. Install it from https://docs.docker.com/get-docker/ and re-run."
	if docker compose version >/dev/null 2>&1; then
		COMPOSE=(docker compose)
	elif command -v docker-compose >/dev/null 2>&1; then
		warn "legacy docker-compose found; using it"
		COMPOSE=(docker-compose)
	else
		die "Docker Compose v2 is required (docker compose). Update Docker or install the compose plugin."
	fi
	command -v git >/dev/null 2>&1 || die "git is required to fetch Quarry."
	command -v openssl >/dev/null 2>&1 || die "openssl is required for secret generation."
	local dv cv
	dv="$(docker --version | sed 's/Docker version //;s/,.*//')"
	cv="$("${COMPOSE[@]}" version --short 2>/dev/null || printf 'legacy')"
	ok "docker ${dv} / compose ${cv}"
}

compose() { "${COMPOSE[@]}" --env-file deploy/.env -f deploy/docker-compose.yml "$@"; }

# ---------------------------------------------------------------------------
# actions
# ---------------------------------------------------------------------------
action_install() {
	hdr "Fresh install"

	# Where to put Quarry. When run from inside a checkout, offer it first.
	local src_dir install_dir
	src_dir="$(git rev-parse --show-toplevel 2>/dev/null || printf '')"
	if [ -n "$src_dir" ] && [ -f "${src_dir}/deploy/docker-compose.yml" ]; then
		install_dir="$(ask "Where should Quarry live?" "${src_dir} (use this checkout)")"
		case "$install_dir" in *"use this checkout"*) install_dir="$src_dir" ;; esac
	else
		if [ "$(id -u)" = "0" ]; then
			install_dir="$(ask "Where should Quarry live?" "/opt/quarry")"
		else
			install_dir="$(ask "Where should Quarry live?" "${PWD}/quarry")"
		fi
		if [ -d "${install_dir}/.git" ] || [ -f "${install_dir}/deploy/docker-compose.yml" ]; then
			say "  ${DIM}found an existing Quarry checkout there — updating it instead.${RESET}"
		else
			say "  ${DIM}cloning ${REPO_URL}${RESET}"
			git clone --depth 1 "$REPO_URL" "$install_dir"
		fi
	fi
	cd "$install_dir"
	ok "working in ${install_dir}"

	# Configuration questions.
	local panel_port router_port public_host admin_user admin_pass node_name
	panel_port="$(ask "Port for the web panel?" "$DEFAULT_PORT")"
	router_port="$(ask "Port for the connect-router (players join domains here)?" "$DEFAULT_ROUTER_PORT")"
	public_host="$(ask "Public hostname or IP players and browsers will use?" "$(hostname -f 2>/dev/null || hostname)")"
	node_name="$(ask "Name for this first node?" "main")"
	admin_user="$(ask "Admin username?" "admin")"
	admin_pass="$(ask_secret "Admin password?")"
	[ -n "$admin_pass" ] || admin_pass="$(random_password)"

	# Validate everything before it lands in an env file or command.
	is_safe_username "$admin_user" || die "admin username: use 1-32 letters, digits, dot, dash or underscore"
	is_safe_password "$admin_pass" || die "admin password: printable characters only, no leading dash, at most 128"
	case "$panel_port" in ''|*[!0-9]*) die "panel port must be a number" ;; esac
	case "$router_port" in ''|*[!0-9]*) die "router port must be a number" ;; esac
	[ "$panel_port" -ge 1 ] && [ "$panel_port" -le 65535 ] || die "panel port must be 1-65535"
	[ "$router_port" -ge 1 ] && [ "$router_port" -le 65535 ] || die "router port must be 1-65535"
	is_safe_username "$node_name" || die "node name: use letters, digits, dot, dash or underscore"

	# Host paths and IDs.
	local servers_dir daemon_dir sock_gid docker_uid docker_gid pg_pass
	servers_dir="${install_dir}/data/servers"
	daemon_dir="${install_dir}/data/daemon"
	docker_uid="${QUARRY_DOCKER_UID:-$(id -u)}"
	docker_gid="${QUARRY_DOCKER_GID:-$(id -g)}"
	sock_gid="$(stat -c %g /var/run/docker.sock 2>/dev/null || printf '999')"
	pg_pass="$(random_hex 24)"
	mkdir -p "$servers_dir" "$daemon_dir"

	hdr "Writing configuration"
	{
		printf '# Generated by install.sh on %s\n' "$(date -u '+%Y-%m-%d %H:%M UTC')"
		printf 'POSTGRES_DB=quarry\n'
		printf 'POSTGRES_USER=quarry\n'
		printf 'POSTGRES_PASSWORD=%s\n' "$pg_pass"
		printf 'QUARRY_PANEL_PORT=%s\n' "$panel_port"
		printf 'QUARRY_ROUTER_PORT=%s\n' "$router_port"
		printf 'QUARRY_PUBLIC_HOST=%s\n' "$public_host"
		printf 'QUARRY_NODE_NAME=%s\n' "$node_name"
		printf 'QUARRY_DOCKER_UID=%s\n' "$docker_uid"
		printf 'QUARRY_DOCKER_GID=%s\n' "$docker_gid"
		printf 'QUARRY_DOCKER_SOCK_GID=%s\n' "$sock_gid"
		printf 'QUARRY_HOST_SERVERS_DIR=%s\n' "$servers_dir"
		printf 'QUARRY_HOST_DAEMON_DIR=%s\n' "$daemon_dir"
		printf 'QUARRY_ENROLLMENT_TOKEN=\n'
	} > deploy/.env
	chmod 600 deploy/.env
	ok "deploy/.env written (secrets inside, keep it private)"

	hdr "Building and starting the stack"
	say "  ${DIM}first build pulls Go, Node and runtime images — grab a coffee.${RESET}"
	compose up -d --build

	hdr "Waiting for the panel"
	local tries=0
	until curl -fsS "http://localhost:${panel_port}/healthz" >/dev/null 2>&1; do
		tries=$((tries + 1))
		if [ "$tries" -gt 90 ]; then
			die "panel did not become healthy — check the logs: docker compose --env-file deploy/.env -f deploy/docker-compose.yml logs panel"
		fi
		sleep 2
	done
	ok "panel is healthy"

	hdr "Creating the admin account"
	# Credentials reach the panel through a fixed-name env file, never argv
	# (nothing user-supplied is expanded into a command line).
	cat > deploy/.admin.env <<ADMIN_ENV
QUARRY_ADMIN_USERNAME=${admin_user}
QUARRY_ADMIN_PASSWORD=${admin_pass}
ADMIN_ENV
	chmod 600 deploy/.admin.env
	if docker exec --env-file deploy/.admin.env quarry-panel-1 panel admin create >/dev/null 2>&1; then
		ok "admin '${admin_user}' created"
		rm -f deploy/.admin.env
	else
		rm -f deploy/.admin.env
		die "admin creation failed — check: docker logs quarry-panel-1"
	fi

	summary "$panel_port" "$admin_user" "$admin_pass" "$install_dir" "$router_port"
}

action_update() {
	hdr "Update Quarry"
	[ -f deploy/.env ] || die "no deploy/.env here — run this inside your Quarry checkout (or reinstall)."
	say "  ${DIM}pulling the latest code${RESET}"
	git pull --ff-only || warn "git pull failed (local changes?) — continuing with the current checkout"
	say "  ${DIM}rebuilding and restarting${RESET}"
	compose up -d --build
	ok "updated. The panel applies database migrations automatically on boot."
	compose ps
}

action_uninstall() {
	hdr "Uninstall Quarry"
	[ -f deploy/.env ] || die "no deploy/.env here — run this inside your Quarry checkout."
	warn "This stops Quarry and removes its databases (volumes)."
	local reply
	reply="$(ask "Also delete server data directories (worlds are LOST)? [y/N]" "no")"
	compose down -v
	case "$reply" in
		y|Y|yes|Yes)
			# shellcheck disable=SC1091
			source deploy/.env
			rm -rf "$QUARRY_HOST_SERVERS_DIR" "$QUARRY_HOST_DAEMON_DIR"
			ok "server data removed"
			;;
	esac
	ok "Quarry stopped. The checkout itself is left in place."
}

summary() {
	local panel_port="$1" admin_user="$2" admin_pass="$3" install_dir="$4" router_port="$5"
	local ip
	ip="$(hostname -I 2>/dev/null | cut -d' ' -f1)"
	local host_display="${ip:-localhost}"
	printf '%b\n' "\n"
	printf '%b\n' "  ${GREEN}${BOLD}  ┌─────────────────────────────────────────────────────┐${RESET}"
	printf '%b\n' "  ${GREEN}${BOLD}  │            Quarry is up and running                 │${RESET}"
	printf '%b\n' "  ${GREEN}${BOLD}  └─────────────────────────────────────────────────────┘${RESET}"
	printf '%b\n' ""
	printf '%b\n' "    ${BOLD}Panel:${RESET}      http://${host_display}:${panel_port}"
	printf '%b\n' "    ${BOLD}Login:${RESET}      ${admin_user} / ${admin_pass}"
	printf '%b\n' "    ${BOLD}Router:${RESET}     port ${router_port} (map domains per server in the panel)"
	printf '%b\n' "    ${BOLD}Install:${RESET}    ${install_dir}"
	printf '%b\n' ""
	printf '%b\n' "    ${DIM}Next steps:${RESET}"
	printf '%b\n' "    ${DIM}· open the panel, create a Paper/Fabric server${RESET}"
	printf '%b\n' "    ${DIM}· BlueMap is preinstalled — see the Map tab once it boots${RESET}"
	printf '%b\n' "    ${DIM}· expose servers publicly via playit.gg, bore or frp${RESET}"
	printf '%b\n' "    ${DIM}· stop / start later: cd ${install_dir} && docker compose${RESET}"
	printf '%b\n' "    ${DIM}  --env-file deploy/.env -f deploy/docker-compose.yml down | up -d${RESET}"
	printf '%b\n' "\n"
}

usage() {
	printf '%b\n' "  Usage: install.sh [install|update|uninstall]"
	exit 0
}

# ---------------------------------------------------------------------------
# main
# ---------------------------------------------------------------------------
banner
check_prereqs

case "${1:-}" in
	install|"") action_install ;;
	update) action_update ;;
	uninstall) action_uninstall ;;
	-h|--help|help) usage ;;
	*) die "unknown command '${1}' — try install, update or uninstall" ;;
esac
