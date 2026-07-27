# Fresh Database Initializers

`AutoMigrate(models.Models...)` owns schema creation for both SQLite and MySQL.
This directory contains only idempotent DML required to initialize a new
database:

- current permissions, roles, and bootstrap administrator
- the built-in weather skill
- the temporary OIDC fallback tenant required by the current authentication flow
- the authoritative industry intent/tag catalog
- the unconfigured nine-slot model profile

Existing-database backfills and compatibility migrations are intentionally not
supported by the unified integration baseline. Restore an archived pre-cleanup
deployment with its matching source revision instead of running this source
against that database.
