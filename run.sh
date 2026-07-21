#!/usr/bin/env bash
# Start the Meizon Framework Registry locally: Postgres, the Go daemon and the
# React console. Safe to re-run — it reuses whatever is already up.
#
#   ./run.sh          start everything, then follow the console log
#   ./run.sh status   show what is running
#   ./run.sh stop     stop the daemon and console (leaves Postgres up)
#
# Run this from a normal terminal so the servers outlive any editor/agent
# session that may reap child processes.

set -euo pipefail
cd "$(dirname "$0")"

API_PORT=8088
WEB_PORT=5173
PG_CONTAINER=meizon-registry-pg

up()   { curl -fsS -o /dev/null --max-time 3 "$1" 2>/dev/null; }
note() { printf '  %-28s %s\n' "$1" "$2"; }

status() {
  echo "services:"
  docker ps --format '{{.Names}}' | grep -qx "$PG_CONTAINER" \
    && note "postgres ($PG_CONTAINER)" "up" || note "postgres ($PG_CONTAINER)" "DOWN"
  up "http://localhost:$API_PORT/"  && note "registryd :$API_PORT" "up" || note "registryd :$API_PORT" "DOWN"
  up "http://localhost:$WEB_PORT/"  && note "console  :$WEB_PORT" "up" || note "console  :$WEB_PORT" "DOWN"
}

case "${1:-start}" in
status) status; exit 0 ;;
stop)
  pkill -f 'registryd -cfg-file' 2>/dev/null || true
  pkill -f 'vite' 2>/dev/null || true
  echo "stopped registryd + console (Postgres left running)"
  exit 0
  ;;
esac

# --- secrets / config ---------------------------------------------------------
if [[ ! -f .run/env ]]; then
  echo "missing .run/env (REGISTRYD_PG_ADDR, REGISTRYD_ENCRYPTION_KEY, …)" >&2
  exit 1
fi
# shellcheck disable=SC1091
source .run/env

# --- postgres -----------------------------------------------------------------
if ! docker ps --format '{{.Names}}' | grep -qx "$PG_CONTAINER"; then
  echo "starting postgres…"
  docker start "$PG_CONTAINER" >/dev/null 2>&1 || {
    echo "container $PG_CONTAINER not found — create it first (see docs/INSTALL-PRODUCTION.md)" >&2
    exit 1
  }
  sleep 2
fi

# --- api ----------------------------------------------------------------------
if ! up "http://localhost:$API_PORT/"; then
  echo "building + starting registryd…"
  go build -o bin/registryd ./cmd/registryd
  # Migrations run automatically on boot.
  nohup ./bin/registryd -cfg-file .run/registryd.yml > .run/registryd.log 2>&1 &
  for _ in $(seq 1 30); do up "http://localhost:$API_PORT/" && break; sleep 1; done
fi

# --- console ------------------------------------------------------------------
if ! up "http://localhost:$WEB_PORT/"; then
  echo "starting console…"
  nohup npm --prefix apps/registry run dev > .run/console.log 2>&1 &
  for _ in $(seq 1 40); do up "http://localhost:$WEB_PORT/" && break; sleep 1; done
fi

echo
status
echo
echo "console:  http://localhost:$WEB_PORT"
echo "sign in:  root@meizon.test / mod@meizon.test / auditor@meizon.test  (password12345)"
echo "logs:     .run/registryd.log  .run/console.log"
