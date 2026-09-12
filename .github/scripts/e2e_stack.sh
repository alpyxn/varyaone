#!/usr/bin/env bash
# Brings up the throwaway stack the browser tests drive, and proves each layer
# is ready before starting the next.
#
# The suite writes as well as reads, so it must never point at a shared
# installation. Everything here belongs to one CI run: its own database, its own
# API process, its own frontend process, seeded from scratch.
#
# Usage: e2e_stack.sh <up|down>
set -euo pipefail

: "${RUNNER_TEMP:=/tmp}"
BIN="$RUNNER_TEMP/varyaone"
LOGS="$RUNNER_TEMP/e2e-logs"
export VARYAONE_CONTROL_DIR="$RUNNER_TEMP/e2e-control"
export VARYAONE_STORAGE_ROOT="$RUNNER_TEMP/e2e-storage"

# Bounded readiness check. Never `sleep N && hope`: a fixed wait is either too
# short (a flaky failure nobody can reproduce) or too long (paid for on every
# run, forever).
wait_for() {
  local label="$1" url="$2" attempts="${3:-60}"
  for _ in $(seq 1 "$attempts"); do
    if curl --silent --fail --max-time 4 "$url" > /dev/null; then
      echo "$label ready"
      return 0
    fi
    sleep 1
  done
  echo "::error::$label did not become ready: $url" >&2
  return 1
}

up() {
  mkdir -p "$LOGS" "$VARYAONE_CONTROL_DIR" "$VARYAONE_STORAGE_ROOT"

  echo "::group::Build the server"
  go build -o "$BIN" ./cmd/varyaone
  echo "::endgroup::"

  echo "::group::Migrate"
  "$BIN" migrate up
  "$BIN" migrate status | tee "$LOGS/migrate-status.txt" | grep -q ' pending=0'
  echo "::endgroup::"

  echo "::group::Seed the fixtures"
  # Demo mode is on for the seeding command only. It is what provisions the
  # company, its user and the records the tests expect; the API below then runs
  # as a perfectly ordinary installation, so nothing under test takes a
  # demo-only code path (auto sign-in, the reset curtain, the reset endpoints).
  VARYAONE_DEMO_MODE=true VARYAONE_DEMO_RESET_INTERVAL=0 "$BIN" demo seed
  echo "::endgroup::"

  echo "::group::Start the API"
  "$BIN" server > "$LOGS/api.log" 2>&1 &
  echo $! > "$RUNNER_TEMP/e2e-api.pid"
  wait_for "API" "http://127.0.0.1:18099/health/ready"
  echo "::endgroup::"

  echo "::group::Build and start the frontend"
  # The production build, not the dev server: a test that passes against Vite's
  # dev output has not exercised what ships.
  (cd web && npm run build)
  (cd web && PORT=5199 HOST=127.0.0.1 NODE_ENV=production node build > "$LOGS/web.log" 2>&1) &
  echo $! > "$RUNNER_TEMP/e2e-web.pid"
  # Through the frontend's own proxy, so this also proves the path the browser
  # actually uses reaches the API.
  wait_for "frontend" "http://127.0.0.1:5199/api/health/ready"
  echo "::endgroup::"
}

down() {
  for name in web api; do
    pid_file="$RUNNER_TEMP/e2e-$name.pid"
    [ -f "$pid_file" ] || continue
    kill "$(cat "$pid_file")" 2> /dev/null || true
    rm -f "$pid_file"
  done
}

case "${1:-}" in
  up) up ;;
  down) down ;;
  *)
    echo "usage: $0 <up|down>" >&2
    exit 2
    ;;
esac
