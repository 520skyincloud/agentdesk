# FastGPT Managed Store Knowledge Integration

## Boundary

The managed integration itself does not change variables, media processing,
the public API, conversation state, or the human-handoff state machine.
Reply-runtime policy may select more than one Store-owned dataset as described
below, while keeping one Intent call, parallel retrieval, and one Generate
call.

The managed topology is:

```text
Agent Desk Store -> FastGPT managed Team -> FastGPT Datasets
```

One Store maps to one FastGPT Team. A Store can own several datasets. Each
WeCom employee account selects its store-specific knowledge base. An optional
Store-owned general knowledge base may be appended by runtime configuration;
it never replaces or crosses the Store boundary.

## Reply-runtime knowledge layers

The optional mapping is stored in `SystemConfig` under
`reply_runtime.general_knowledge_base_by_store` as Store ID to Agent Desk
knowledge-base ID, for example `{"1":"4"}`. The mapped knowledge base must be
enabled, belong to the same Store, use FastGPT, and have a dataset ID. Invalid
or missing mappings preserve the existing store-only behavior.

For every atomic task, the store-specific and general datasets are searched in
parallel with their existing thresholds. The result is selected by layer:

```text
store layer has an effective hit -> expose only store hits
store layer has no effective hit -> expose general hits
```

Scores are not compared across layers. Raw results from both layers remain in
the retrieval trace for diagnosis, while Generate and handoff directives see
only the selected layer. If a store-layer lookup fails, runtime must not treat
that failure as a clean miss and fall back to general knowledge.

The same release also adds two scoped prompt policies without adding model
calls: `answer_rejected` is disclosed to Intent only when the physically
adjacent previous message is an ordinary AI reply, and spatial fact rules are
disclosed to Generate only for `surrounding_facilities` or `location_info`
tasks. Neither policy changes the existing confirmation/cancel handoff state
machine.

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
