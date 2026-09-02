#!/bin/sh
# varyaone-update-agent — host-side update worker.
#
# Polls the ERP API for a queued update and, when one appears, runs the staged
# `./deploy.sh update` pipeline (pre-flight -> backup -> build -> migrate ->
# restart -> health check, with automatic rollback). The pipeline reports its
# own phase progress and final result back to the API, so this loop stays tiny.
#
# It talks only to 127.0.0.1 and authenticates with VARYAONE_UPDATE_AGENT_TOKEN
# from the project's .env. Install it with: ./deploy.sh install-agent
set -eu

REPO_DIR=${VARYAONE_REPO_DIR:-$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)}
cd "$REPO_DIR"

POLL_SECONDS=${VARYAONE_UPDATE_POLL_SECONDS:-30}
# Aynı hedef için üst üste bu kadar başarısızlıktan sonra denemeyi bırak.
MAX_CONSECUTIVE_FAILURES=${VARYAONE_UPDATE_MAX_FAILURES:-3}

env_get() { grep "^$1=" .env 2>/dev/null | tail -n1 | cut -d= -f2-; }

json_field() {
  # crude flat-JSON string field reader: json_field '{"a":"b"}' a  ->  b
  printf '%s' "$1" | sed -n "s/.*\"$2\"[[:space:]]*:[[:space:]]*\"\([^\"]*\)\".*/\1/p"
}

# Bir git referansı olarak makul mü? (enjeksiyon / bozuk JSON koruması)
valid_target() {
  case "$1" in
    "" ) return 0 ;;
    *[!A-Za-z0-9._/-]* ) return 1 ;;
    * ) return 0 ;;
  esac
}

[ -f .env ] || { echo "update-agent: .env not found in $REPO_DIR" >&2; exit 1; }
TOKEN=$(env_get VARYAONE_UPDATE_AGENT_TOKEN)
[ -n "$TOKEN" ] || { echo "update-agent: VARYAONE_UPDATE_AGENT_TOKEN is empty; nothing to do" >&2; exit 0; }

API_PORT=$(env_get VARYAONE_API_PORT); API_PORT=${API_PORT:-8080}
API="http://127.0.0.1:${API_PORT}"

echo "update-agent: watching ${API}/internal/update (every ${POLL_SECONDS}s)"

fail_target=""
fail_count=0

while :; do
  resp=$(curl -fsS -m 10 -H "authorization: Bearer ${TOKEN}" "${API}/internal/update/next" 2>/dev/null || echo '{}')
  action=$(json_field "$resp" action)

  if [ "$action" = "apply" ]; then
    target=$(json_field "$resp" target_version)
    if ! valid_target "$target"; then
      echo "update-agent: refusing suspicious target '${target}'" >&2
      sleep "$POLL_SECONDS"; continue
    fi

    # Aynı hedef tekrar tekrar başarısızsa döngüye girip disk/backup tüketme.
    if [ "$target" = "$fail_target" ] && [ "$fail_count" -ge "$MAX_CONSECUTIVE_FAILURES" ]; then
      echo "update-agent: '${target:-latest}' ${fail_count} kez başarısız — bu hedef için duraklatıldı" >&2
      sleep "$POLL_SECONDS"; continue
    fi

    echo "update-agent: applying ${target:-latest}"
    # deploy.sh update posts progress + the final result to the API itself and
    # holds its own single-run lock, so a stray second agent tick just waits.
    if ./deploy.sh update --yes --report ${target:+--target "$target"}; then
      echo "update-agent: apply finished"
      fail_target=""; fail_count=0
    else
      if [ "$target" = "$fail_target" ]; then
        fail_count=$((fail_count + 1))
      else
        fail_target="$target"; fail_count=1
      fi
      echo "update-agent: apply failed (${fail_count}/${MAX_CONSECUTIVE_FAILURES}); see deploy/update-*.log" >&2
    fi
  fi

  sleep "$POLL_SECONDS"
done
