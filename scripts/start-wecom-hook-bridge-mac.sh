#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
ENV_FILE="${ROOT_DIR}/.wecom-hook-bridge.env"
HOOK_HOST="${WECOM_HOOK_HOST:-}"
AGENT_DESK_URL="${AGENT_DESK_BASE_URL:-http://127.0.0.1:8083}"

if [[ -f "${ENV_FILE}" ]]; then
  set -a
  # shellcheck disable=SC1090
  source "${ENV_FILE}"
  set +a
  HOOK_HOST="${WECOM_HOOK_HOST:-${HOOK_HOST}}"
  AGENT_DESK_URL="${AGENT_DESK_BASE_URL:-${AGENT_DESK_URL}}"
fi

while [[ $# -gt 0 ]]; do
  case "$1" in
    --hook-host)
      HOOK_HOST="${2:-}"
      shift 2
      ;;
    --agent-desk)
      AGENT_DESK_URL="${2:-}"
      shift 2
      ;;
    *)
      echo "Unknown argument: $1" >&2
      echo "Usage: $0 --hook-host <windows-ip> [--agent-desk <url>]" >&2
      exit 1
      ;;
  esac
done

if [[ -z "${HOOK_HOST}" ]]; then
  echo "Missing --hook-host. Example: $0 --hook-host 192.168.2.88" >&2
  exit 1
fi

export AGENT_DESK_BASE_URL="${AGENT_DESK_URL}"
export WECOM_HOOK_API_URL="http://${HOOK_HOST}:8060/"
export WECOM_HOOK_WS_URL="ws://${HOOK_HOST}:8061/message/"

echo "AgentDesk: ${AGENT_DESK_BASE_URL}"
echo "Hook API : ${WECOM_HOOK_API_URL}"
echo "Hook WS  : ${WECOM_HOOK_WS_URL}"
echo
echo "Running doctor..."
node "${ROOT_DIR}/scripts/wecom-hook-doctor.mjs"

echo
echo "Starting bridge..."
if [[ -z "${AGENT_DESK_CHANNEL_ID:-}" || -z "${AGENT_DESK_BRIDGE_TOKEN:-}" ]]; then
  echo "Missing AGENT_DESK_CHANNEL_ID or AGENT_DESK_BRIDGE_TOKEN." >&2
  echo "Create ${ENV_FILE} from .wecom-hook-bridge.env.example first." >&2
  exit 1
fi

cd "${ROOT_DIR}"
exec node scripts/wecom-hook-bridge.mjs
