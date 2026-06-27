#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
ENV_FILE="${ROOT_DIR}/.wecom-hook-bridge.env"

if [[ -f "${ENV_FILE}" ]]; then
  set -a
  # shellcheck disable=SC1090
  source "${ENV_FILE}"
  set +a
fi

export AGENT_DESK_BASE_URL="${AGENT_DESK_BASE_URL:-http://127.0.0.1:8083}"
export WECOM_HOOK_API_URL="${WECOM_HOOK_API_URL:-http://127.0.0.1:8060/}"
export WECOM_HOOK_WS_URL="${WECOM_HOOK_WS_URL:-ws://127.0.0.1:8061/message/}"
export POLL_INTERVAL_MS="${POLL_INTERVAL_MS:-3000}"

if [[ -z "${AGENT_DESK_CHANNEL_ID:-}" || -z "${AGENT_DESK_BRIDGE_TOKEN:-}" ]]; then
  echo "Missing AGENT_DESK_CHANNEL_ID or AGENT_DESK_BRIDGE_TOKEN." >&2
  echo "Create ${ENV_FILE} from .wecom-hook-bridge.env.example first." >&2
  exit 1
fi

cd "${ROOT_DIR}"
exec node scripts/wecom-hook-bridge.mjs
