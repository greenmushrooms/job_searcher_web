#!/usr/bin/env bash
# test-vpn.sh — verify the WireGuard tunnel to the Hetzner box works before we
# wire up Postgres replication (pglogical) or deploy the app behind it.
#
# RUN THIS ON YOUR MACHINE (the homelab/laptop side that has .env and the VPN
# route). It does NOT run in the Claude remote container — that sandbox has
# neither the keys nor a network path to the endpoint.
#
# It uses the same gluetun custom-WireGuard pattern as the vpn-test experiment:
# spin up a throwaway gluetun container from the WIREGUARD_* vars in .env, wait
# for the handshake, then run reachability checks from *inside* the tunnel's
# network namespace. Tears everything down on exit.
#
#   ./deploy/test-vpn.sh                 # uses ./.env
#   ENV_FILE=/path/.env ./deploy/test-vpn.sh
#
# Secrets stay in .env. Nothing is printed except pass/fail + non-secret facts.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"
ENV_FILE="${ENV_FILE:-${REPO_ROOT}/.env}"
GLUETUN_NAME="vpn-test-$$"
GLUETUN_IMAGE="qmcgaw/gluetun:latest"

red()   { printf '\033[31m%s\033[0m\n' "$*"; }
green() { printf '\033[32m%s\033[0m\n' "$*"; }
info()  { printf '\033[36m%s\033[0m\n' "$*"; }

cleanup() {
  docker rm -f "${GLUETUN_NAME}" >/dev/null 2>&1 || true
}
trap cleanup EXIT

[[ -f "${ENV_FILE}" ]] || { red "no .env at ${ENV_FILE} (set ENV_FILE=...)"; exit 1; }
command -v docker >/dev/null || { red "docker not found"; exit 1; }

# Pull required names from .env without leaking values into the environment of
# child processes we don't control. set -a so the sourced vars are exported for
# the docker run below.
set -a
# shellcheck disable=SC1090
source "${ENV_FILE}"
set +a

require() {
  local missing=0
  for v in "$@"; do
    if [[ -z "${!v:-}" ]]; then red "missing required var: ${v}"; missing=1; fi
  done
  [[ "${missing}" -eq 0 ]] || exit 1
}

require WIREGUARD_PRIVATE_KEY WIREGUARD_PUBLIC_KEY WIREGUARD_ENDPOINT_IP \
        WIREGUARD_ENDPOINT_PORT WIREGUARD_ADDRESSES

info "Endpoint : ${WIREGUARD_ENDPOINT_IP}:${WIREGUARD_ENDPOINT_PORT}"
info "Our addr : ${WIREGUARD_ADDRESSES}"
info "DB target: ${DB_HOST:-<unset>}:${DB_PORT:-5432}"
echo

# ---- 1. bring up the tunnel -------------------------------------------------
info "[1/4] starting throwaway gluetun (${GLUETUN_NAME})…"
docker run -d --name "${GLUETUN_NAME}" \
  --cap-add=NET_ADMIN \
  --device /dev/net/tun:/dev/net/tun \
  -e VPN_SERVICE_PROVIDER=custom \
  -e VPN_TYPE=wireguard \
  -e WIREGUARD_PRIVATE_KEY="${WIREGUARD_PRIVATE_KEY}" \
  -e WIREGUARD_PUBLIC_KEY="${WIREGUARD_PUBLIC_KEY}" \
  -e WIREGUARD_ENDPOINT_IP="${WIREGUARD_ENDPOINT_IP}" \
  -e WIREGUARD_ENDPOINT_PORT="${WIREGUARD_ENDPOINT_PORT}" \
  -e WIREGUARD_ADDRESSES="${WIREGUARD_ADDRESSES}" \
  -e FIREWALL_OUTBOUND_SUBNETS="${FIREWALL_OUTBOUND_SUBNETS:-}" \
  "${GLUETUN_IMAGE}" >/dev/null

# ---- 2. wait for handshake --------------------------------------------------
info "[2/4] waiting for WireGuard handshake (up to 30s)…"
handshake_ok=0
for _ in $(seq 1 30); do
  if docker exec "${GLUETUN_NAME}" wg show 2>/dev/null | grep -q "latest handshake"; then
    handshake_ok=1; break
  fi
  sleep 1
done
if [[ "${handshake_ok}" -eq 1 ]]; then
  green "  ✓ handshake established"
  docker exec "${GLUETUN_NAME}" wg show 2>/dev/null \
    | grep -E "latest handshake|transfer" | sed 's/^/    /'
else
  red "  ✗ no handshake — check keys/endpoint/firewall (UDP ${WIREGUARD_ENDPOINT_PORT})"
  red "  gluetun logs:"; docker logs "${GLUETUN_NAME}" 2>&1 | tail -20 | sed 's/^/    /'
  exit 1
fi

# helper: run a command inside the tunnel namespace
in_tunnel() { docker run --rm --network "container:${GLUETUN_NAME}" alpine:3.20 sh -c "$*"; }

# ---- 3. reach the Hetzner box over the tunnel -------------------------------
info "[3/4] pinging Hetzner endpoint through the tunnel…"
if in_tunnel "apk add -q iputils >/dev/null 2>&1; ping -c2 -W2 ${WIREGUARD_ENDPOINT_IP} >/dev/null 2>&1"; then
  green "  ✓ ${WIREGUARD_ENDPOINT_IP} reachable"
else
  red "  ! ICMP blocked or unreachable (not fatal — many hosts drop ping)"
fi

# ---- 4. check Postgres port on the DB target --------------------------------
DB_PROBE_HOST="${DB_HOST:-}"
DB_PROBE_PORT="${DB_PORT:-5432}"
if [[ -n "${DB_PROBE_HOST}" && "${DB_PROBE_HOST}" != "localhost" ]]; then
  info "[4/4] probing Postgres ${DB_PROBE_HOST}:${DB_PROBE_PORT} through the tunnel…"
  if in_tunnel "nc -z -w5 ${DB_PROBE_HOST} ${DB_PROBE_PORT}"; then
    green "  ✓ Postgres port open over the VPN"
  else
    red "  ✗ Postgres port ${DB_PROBE_PORT} not reachable — check the remote"
    red "    DB host binding and FIREWALL_OUTBOUND_SUBNETS in .env"
    exit 1
  fi
else
  info "[4/4] DB_HOST is unset/localhost — skipping Postgres probe."
  info "      Set DB_HOST to the remote Postgres (e.g. 10.10.0.1) to test it."
fi

echo
green "VPN tunnel check complete."
