#!/usr/bin/env bash
# End-to-end smoke test for nlink using docker-compose (server + echo + client).
set -euo pipefail

HERE="$(cd "$(dirname "$0")" && pwd)"
cd "$HERE"

COMPOSE="docker compose"
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
ok=0
for i in $(seq 1 30); do
  if curl -fsS http://127.0.0.1:18080/health >/dev/null 2>&1; then
    ok=1; echo "[e2e] server healthy"; break
  fi
  sleep 1
done
if [ "$ok" -ne 1 ]; then
  echo "[e2e] ERROR: server did not become healthy" >&2
  $COMPOSE logs --tail=100
  exit 1
fi

echo "[e2e] waiting for proxy remote_port 19999 ..."
ok=0
for i in $(seq 1 30); do
  if nc -z 127.0.0.1 19999 2>/dev/null; then
    ok=1; echo "[e2e] remote_port is listening"; break
  fi
  sleep 1
done
if [ "$ok" -ne 1 ]; then
  echo "[e2e] ERROR: proxy did not register in time" >&2
  $COMPOSE logs --tail=150
  exit 1
fi

PAYLOAD="hello-nlink-$(date +%s)"
echo "[e2e] sending '$PAYLOAD' through the tunnel ..."
RESP="$(printf '%s\n' "$PAYLOAD" | nc -w 3 127.0.0.1 19999 || true)"
echo "[e2e] received: $RESP"

if echo "$RESP" | grep -q "$PAYLOAD"; then
  echo "[e2e] ✅ PASS: round-trip through proxy succeeded"
  exit 0
fi

echo "[e2e] ❌ FAIL: payload did not round-trip" >&2
$COMPOSE logs --tail=200
exit 1
