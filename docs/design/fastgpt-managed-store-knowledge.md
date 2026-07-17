# FastGPT Managed Store Knowledge Integration

## Boundary

This integration keeps the existing reply runtime unchanged. It does not
change intent detection, prompt packs, JSON schema, retrieval parameters,
variables, media processing, conversation routing, or human handoff.

The managed topology is:

```text
Agent Desk Store -> FastGPT managed Team -> FastGPT Datasets
```

One Store maps to one FastGPT Team. A Store can own several datasets. Each
WeCom employee account selects one current knowledge base, and only that
dataset is used for subsequent reply retrieval.

## Credentials and isolation

- Configure the FastGPT root address with `AGENT_DESK_FASTGPT_BASE_URL`.
- Configure the dedicated service credential with
  `AGENT_DESK_FASTGPT_INTEGRATION_TOKEN`.
- Do not put either value in a knowledge-base form, a database remark, a
  frontend response, a browser log, or source control.
- The managed gateway sends the token only in `X-Agent-Desk-Token` and scopes
  every managed request with the Store identity.
- FastGPT verifies `storeId -> teamId -> datasetId` before every dataset read
  or write. A mismatch is an error; it must never fall back to another Store
  or dataset.
- Model provider keys, provider URLs, encrypted values, and model routing
  remain inside FastGPT. Agent Desk only receives profile name, revision,
  status, a non-sensitive fingerprint, and immutable usage events.

Existing manually connected datasets remain on the legacy compatibility
transport until they are explicitly migrated. New managed datasets use
`connectionId=agentdesk_integration`.

## Operational behavior

- Creating a Store Team is idempotent on the stable `externalStoreId`.
- The FastGPT Team and Store mapping are written in one FastGPT transaction;
  a short-lived provisioning lease prevents concurrent first requests from
  creating duplicate Teams.
- File deletion first physically deletes the FastGPT Collection. Agent Desk
  updates the local task state only after that operation succeeds.
- Full dataset deletion requires the exact knowledge-base name. Agent Desk
  clears employee-account bindings only after FastGPT reports the dataset
  deleted or already deleted.
- FastGPT usage is imported asynchronously into `AIUsageEvent` using
  `fastgpt:<teamId>:<externalEventId>` as the idempotency key. Synchronization
  failures do not block a customer reply.

## Candidate verification

The real candidate lifecycle test is opt-in and does not run in ordinary Go
tests. It requires a dedicated integration token and an isolated test Store:

```text
FASTGPT_MANAGED_INTEGRATION_LIFECYCLE=1
FASTGPT_MANAGED_INTEGRATION_BASE_URL=<candidate-base-url>
FASTGPT_MANAGED_INTEGRATION_TOKEN=<candidate-service-token>
FASTGPT_MANAGED_INTEGRATION_STORE_ID=<temporary-store-id>
go test ./internal/pkg/fastgpt -run TestGatewayManagedIntegrationLifecycle -count=1
```

The test creates a temporary dataset, uploads a temporary file, waits for
collection readiness, searches it, physically deletes the collection and
dataset, and verifies the result. It is the only acceptable evidence for a
real managed integration lifecycle.

## Production gate

Production cutover requires written authorization from the FastGPT multi-
tenant SaaS owner, a successful candidate lifecycle test, and a controlled
single-Store rollout. Keep the existing FastGPT address, port, volumes,
backups, and third-party integrations unchanged until those gates pass.
