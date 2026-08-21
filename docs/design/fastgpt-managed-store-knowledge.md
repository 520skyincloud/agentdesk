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
parallel with their existing thresholds. `RawHits` retains the candidates from
both layers. All atomic tasks that returned candidates are then sent through
one shared batch evidence Judge call, including tasks that currently have
candidates from only one layer. The Judge classifies each candidate as:

```text
direct      -> directly and sufficiently answers the atomic question
supporting  -> relevant, but insufficient without a direct answer
unrelated   -> answers a different question or does not support the fact asked
```

The Judge does not answer the customer and does not choose the final knowledge
layer. After classification, deterministic runtime code applies the following
order independently for each atomic task:

```text
store has direct evidence
-> expose store direct + store supporting evidence only

otherwise general has direct evidence
-> expose general direct + general supporting evidence only

neither layer has direct evidence
-> expose no knowledge answer for that task
```

This preserves `store direct > general direct`: scores are never compared
across layers, and a higher-scoring general answer cannot override a direct
store answer. Conversely, an unrelated store candidate cannot hide a direct
general answer. Generate and handoff decisions read only the rebuilt selected
evidence; `RawHits` and the `pipeline.evidenceJudge` trace retain the diagnostic
candidate and decision data.

The Judge uses the internal `knowledge_judge_llm` model-profile slot. One reply
execution makes at most one Judge call for all candidate-bearing atomic tasks;
the normalized limit is 4 seconds, 2,048 output tokens, and zero retries. A
missing model configuration, model error, timeout, or invalid protocol response
does not fail or restart the reply pipeline. Runtime records a `fallback` trace
and preserves the pre-Judge deterministic selection:

```text
store has an effective retrieval hit -> keep store hits
otherwise -> keep general hits
```

The fallback does not add another Intent, retrieval, Judge, or Generate call.
If the store-layer lookup itself fails, runtime still must not treat that
failure as a clean miss and fall back to general knowledge.

For a multi-question message, questions with direct evidence remain available
to the single Generate call. Questions with no direct evidence or whose
selected answer is a handoff directive are removed from Generate. When both
kinds occur together, the knowledge answer is committed first and the existing
handoff-confirmation service is invoked afterward for only the deferred tasks;
if every task needs handoff, the existing pre-Generate handoff path is used.

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

## General-dataset blue-green rollout

Do not replace the contents of a production-referenced general dataset in
place. Create a staging dataset inside the same Store-owned FastGPT Team,
import and train the cleaned general FAQ data there, and verify collection
readiness and real searches before switching Agent Desk.

Cutover changes only the existing Agent Desk general knowledge-base record's
`dataset_id`, using a compare-and-swap condition against the previously
recorded dataset ID. The `reply_runtime.general_knowledge_base_by_store`
mapping continues to point to the same Agent Desk knowledge-base ID. Keep the
old FastGPT dataset enabled and unchanged during observation so rollback is an
atomic `dataset_id` restore rather than a reply-runtime rollback.

Before cutover, verify at least:

- staging training has completed with no failed items;
- representative general questions return the intended direct FAQ;
- store-specific gold answers still produce store direct evidence;
- weak or unrelated candidates are classified as `unrelated` by the Judge;
- the staging dataset belongs to the same Store Team as the store dataset.

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
