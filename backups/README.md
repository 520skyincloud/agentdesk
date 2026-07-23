# Backup Security Boundary

This directory intentionally contains no runtime backup artifacts.

Database dumps, uploaded assets, deployment configuration, `.env` files, API keys, channel credentials, and Codex session archives must be stored outside the Git repository in encrypted, access-controlled backup storage. A private Git repository is not a backup vault.

Before B13/B14 deployment or destructive schema maintenance:

1. Write the backup to an absolute path outside this repository.
2. Encrypt it and restrict filesystem and operator access.
3. Verify checksums and perform a restore rehearsal against an isolated database.
4. Record only the backup identifier, schema version, checksum, and restore result in the authoritative handoff document. Never record secrets or customer content.

Artifacts previously committed under `backups/migration-20260630-090044` and `backups/codex-session` were removed from the working tree during B13 security hardening. Because deletion does not erase Git history, all credentials present in historical deployment files must be rotated and repository-history cleanup must be handled as a separately coordinated operation before public release.

## Executable restore verification

`cmd/tenant_integrity_audit` is the only release-audit command. Its restore mode compares a stopped source database with an isolated database restored from the encrypted artifact:

```bash
unset AGENT_DESK_DB_DSN
export AGENT_DESK_BACKGROUND_WORKERS_ENABLED=false
export AGENT_DESK_RESTORE_AUDIT_SOURCE_DB_DSN='<source DSN>'
export AGENT_DESK_RESTORE_AUDIT_RESTORED_DB_DSN='<isolated restored DSN>'

go run ./cmd/tenant_integrity_audit \
  -config /secure/path/source-audit.yaml \
  -restore-config /secure/path/restored-audit.yaml \
  -backup-artifact /encrypted/storage/agentdesk_backup.age \
  -backup-sha256 '<checksum recorded before restore>' \
  -readiness-tenant-code '<tenant code>' \
  -readiness-level tag_gray \
  -readiness-evidence-start '<RFC3339 evidence window>'
```

Both config files must declare the correct database type and `backgroundWorkers.enabled: false`. The paired DSN environment variables are optional for protected SQLite config files but mandatory when DSNs must remain outside files. Never pass a DSN as a command-line flag or redirect the JSON report into this repository.

The gate requires:

- an absolute, non-empty, non-symlink backup artifact outside the detected repository root;
- no group/other filesystem permissions;
- a recognized age, ASCII-armored OpenPGP, or OpenSSL salted container header;
- an exact match with the checksum fixed before restoration;
- separate source and restored database endpoints using the same database driver;
- matching full application DDL, data, and Migration fingerprints;
- passing Tenant integrity and the requested release-readiness level on both databases.

The command opens both databases in read-only transactions and does not decrypt or restore the artifact. Stopping `8083` and all workers, creating the encrypted backup, restoring it into an isolated database, protecting decryption material, and deleting the rehearsal environment remain operator responsibilities.

A passing restore report is necessary but not sufficient for B14. The real Lisi Future NewAPI, FastGPT, reply, handoff, deterministic dispatch, tag-gray, and RMB billing evidence must also pass before any destructive Schema Cleanup.
