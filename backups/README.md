# Backup Security Boundary

This directory intentionally contains no runtime backup artifacts.

Database dumps, uploaded assets, deployment configuration, `.env` files, API keys, channel credentials, and Codex session archives must be stored outside the Git repository in encrypted, access-controlled backup storage. A private Git repository is not a backup vault.

Before B13/B14 deployment or destructive schema maintenance:

1. Write the backup to an absolute path outside this repository.
2. Encrypt it and restrict filesystem and operator access.
3. Verify checksums and perform a restore rehearsal against an isolated database.
4. Record only the backup identifier, schema version, checksum, and restore result in the authoritative handoff document. Never record secrets or customer content.

Artifacts previously committed under `backups/migration-20260630-090044` and `backups/codex-session` were removed from the working tree during B13 security hardening. Because deletion does not erase Git history, all credentials present in historical deployment files must be rotated and repository-history cleanup must be handled as a separately coordinated operation before public release.
