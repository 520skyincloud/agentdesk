#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SESSION_DIR="${1:-${ROOT_DIR}/backups/codex-session}"
THREAD_ID="019e81e2-a5e3-7c70-9c68-0dbfb36dd257"
OUT_DIR="${ROOT_DIR}/.codex-session-restore"
GZ_FILE="${OUT_DIR}/rollout-agent-desk-main-${THREAD_ID}.jsonl.gz"
JSONL_FILE="${OUT_DIR}/rollout-agent-desk-main-${THREAD_ID}.jsonl"

if [[ ! -d "${SESSION_DIR}" ]]; then
  echo "Session backup directory not found: ${SESSION_DIR}" >&2
  exit 1
fi

mkdir -p "${OUT_DIR}"

if [[ -f "${SESSION_DIR}/SHA256SUMS" ]]; then
  (cd "${SESSION_DIR}" && sha256sum -c SHA256SUMS)
fi

cat "${SESSION_DIR}/rollout-agent-desk-main-${THREAD_ID}.jsonl.gz.part-"* > "${GZ_FILE}"
gunzip -c "${GZ_FILE}" > "${JSONL_FILE}"
cp "${SESSION_DIR}/thread-${THREAD_ID}.json" "${OUT_DIR}/thread-${THREAD_ID}.json"

cat <<EOF
Codex session backup rebuilt:
  ${JSONL_FILE}
  ${OUT_DIR}/thread-${THREAD_ID}.json

This repository includes the raw Codex rollout and thread metadata for continuation.
If the Codex desktop app supports importing local rollout files on the new machine,
import the JSONL above. Otherwise, open docs/development-handoff.md and this JSONL
as context in a new Codex thread.
EOF
