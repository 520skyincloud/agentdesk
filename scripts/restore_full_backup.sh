#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BACKUP_DIR="${1:-}"

if [[ -z "${BACKUP_DIR}" ]]; then
  BACKUP_DIR="$(find "${ROOT_DIR}/backups" -maxdepth 1 -type d -name 'migration-*' | sort | tail -1)"
fi

if [[ ! -d "${BACKUP_DIR}" ]]; then
  echo "Backup directory not found: ${BACKUP_DIR}" >&2
  exit 1
fi

cd "${ROOT_DIR}"

if [[ -f "${BACKUP_DIR}/SHA256SUMS" ]]; then
  (cd "${BACKUP_DIR}" && sha256sum -c SHA256SUMS)
fi

if [[ -f "${BACKUP_DIR}/config/agent-desk.yaml" ]]; then
  mkdir -p docker
  cp "${BACKUP_DIR}/config/agent-desk.yaml" docker/agent-desk.yaml
fi

if [[ -f "${BACKUP_DIR}/config/config.yaml" ]]; then
  mkdir -p config
  cp "${BACKUP_DIR}/config/config.yaml" config/config.yaml
fi

docker compose up -d mysql qdrant

echo "Waiting for MySQL..."
for _ in {1..60}; do
  if docker compose exec -T mysql sh -lc 'mysqladmin ping -h 127.0.0.1 -ucs_ai_agent -pcs_ai_agent_password --silent' >/dev/null 2>&1; then
    break
  fi
  sleep 2
done

if [[ -f "${BACKUP_DIR}/volumes/mysql-cs_ai_agent.sql.gz" ]]; then
  echo "Restoring MySQL dump..."
  gunzip -c "${BACKUP_DIR}/volumes/mysql-cs_ai_agent.sql.gz" | docker compose exec -T mysql sh -lc 'mysql -uroot -pcs_ai_agent_root_password'
fi

docker compose stop agent-desk qdrant >/dev/null 2>&1 || true

restore_volume_tar() {
  local service="$1"
  local destination="$2"
  local archive="$3"
  local container_id volume_name

  [[ -f "${archive}" ]] || return 0
  docker compose up -d "${service}" >/dev/null
  container_id="$(docker compose ps -q "${service}")"
  volume_name="$(docker inspect "${container_id}" --format "{{range .Mounts}}{{if eq .Destination \"${destination}\"}}{{.Name}}{{end}}{{end}}")"
  if [[ -z "${volume_name}" ]]; then
    echo "Could not resolve volume for ${service}:${destination}" >&2
    exit 1
  fi
  docker compose stop "${service}" >/dev/null
  docker run --rm -v "${volume_name}":/target -v "$(dirname "${archive}")":/backup alpine:3.22 sh -lc "rm -rf /target/* /target/.[!.]* /target/..?* 2>/dev/null || true; cd /target && tar -xzf /backup/$(basename "${archive}")"
}

restore_volume_tar qdrant /qdrant/storage "${BACKUP_DIR}/volumes/qdrant-storage.tgz"
restore_volume_tar agent-desk /app/data "${BACKUP_DIR}/volumes/agent-desk-data.tgz"

if [[ -f "${BACKUP_DIR}/local/repo-data.tgz" ]]; then
  tar -xzf "${BACKUP_DIR}/local/repo-data.tgz" -C "${ROOT_DIR}"
fi

docker compose up -d --build

echo "Restore complete. Open http://localhost:8083/dashboard/"
