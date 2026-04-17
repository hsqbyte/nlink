#!/usr/bin/env bash
# End-to-end smoke test for nlink using two containers (server + client)
# connected via docker-compose. Verifies:
#   1. Server dashboard /health is up
#   2. Client connects and registers a TCP proxy
#   3. TCP traffic flows through the proxy to a local echo backend
#
# Requires: docker, docker compose plugin.

set -euo pipefail

HERE="$(cd "$(dirname "$0")" && pwd)"
cd "$HERE"

COMPOSE="docker compose"
# Fallback to the legacy binary if the plugin is unavailable.
if ! docker compose version >/dev/null 2>&1; then
  COMPOSE="docker-compose"
fi

cleanup() {
  echo "[e2e] tearing down..."
  $COMPOSE down -v --remove-orphans >/dev/null 2>&1 || true
}
trap cleanup EXIT

echo "[e2e] building & starting stack..."
$COMPOSE up -d --build

echo "[e2e] waiting for server /health ..."
for i in $(seq 1 30); do
  if curl -fsS http://127.0.0.1:18080/health >/dev/null 2>&1; then
    echo "[e2e] server healthy"
    break
  fi
  sleep 1
  if [ "$i" -eq 30 ]; then
    echo "[e2e] ERROR: server did not become healthy in time" >&2
    $COMPOSE logs --tail=100
    exit 1
  fi
done

echo "[e2e] waiting for client to register proxy ..."
REGISTERED=0
for i in $(seq 1 30); do
  # TCP check on the proxy remote port
  if (exec 3<>/dev/tcp/127.0.0.1/19999) 2>/dev/null; then
    exec 3<&- 3>&- || true
    REGISTERED=1
    echo "[e2e] remote_port 19999 is listening"
    break
  fi
  sleep 1
done

if [ "$REGISTERED" -ne 1 ]; then
  echo "[e2e] ERROR: proxy remote port 19999 never came up" >&2
  $COMPOSE logs --tail=150
  exit 1
fi

echo "[e2e] sending payload through the tunnel ..."
PAYLOAD="hello-nlink-$(date +%s)"
# echo backend is `socat ... EXEC:/bin/cat` which echoes whatever we send.
RESP="$(printf '%s\n' "$PAYLOAD" | { exec 3<>/dev/tcp/127.0.0.1/19999; cat <&3 & read -r line <&3 || true; echo "$line"; } 2>/dev/null || true)"

# Simpler fallback using nc/ncat if available
if ! echo "$RESP" | grep -q "$PAYLOAD"; then
  if command -v nc >/dev/null 2>&1; then
    RESP="$(printf '%s\n' "$PAYLOAD" | nc -w 3 127.0.0.1 19999 || true)"
  fi
fi

echo "[e2e] received: $RESP"
if echo "$RESP" | grep -q "$PAYLOAD"; then
  echo "[e2e] ✅ PASS: round-trip through proxy succeeded"
  exit 0
else
  echo "[e2e] ❌ FAIL: payload did not round-trip through proxy" >&2
  $COMPOSE logs --tail=200
  exit 1
fi
