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
candidates from only one layer. The current protocol is
`knowledge_evidence_judge.v2`; it returns one decision per task and knowledge
layer:

```text
direct_single   -> one selected FAQ fully answers the atomic question
direct_combined -> multiple selected FAQs in the same layer fully answer it
partial         -> selected facts are usable, but named aspects are still missing
insufficient    -> the layer has no authorized answer
protocol_invalid / timeout / malformed -> Judge execution or protocol failure
```

The Judge returns the selected Candidate IDs, supported facts, critical values,
and missing aspects. It does not answer the customer. Deterministic runtime code
then applies the following order independently for each atomic task:

```text
uncontested exact store handoff directive
-> complete store answer
-> complete general answer
-> partial store answer
-> partial general answer
-> existing reception route
```

Scores are never compared across layers. A higher-scoring general answer cannot
override a valid store answer. A protocol failure in one layer is isolated from
the other layer: it is not treated as a semantic miss, but it also cannot hide
an independently valid answer from the other layer. Generate and handoff decisions read only
`EffectiveHits` rebuilt from the winning selected Candidate IDs. `RawHits` and
the `pipeline.evidenceJudge` trace retain diagnostic candidates and decisions,
but are never exposed to Generate.

The Judge uses the internal `knowledge_judge_llm` model-profile slot. A normal
reply execution makes one Judge call for all candidate-bearing atomic tasks. If
that call times out or returns a malformed/protocol-invalid result that affects
the final selection, runtime isolates the affected task-layer without rerunning
Judge, Intent, or retrieval. It does not preserve or expose a pre-Judge selection:

```text
valid selected store answer -> expose only that selected evidence
otherwise valid selected general answer -> expose only that selected evidence
otherwise valid selected partial answer -> expose confirmed facts and defer missing aspects
no valid layer + protocol failure -> clear effective context and use a safe local reply
```

Every reply run calls Judge at most once. A protocol failure never becomes a
human handoff and never reaches free Generate; `RawHits` remains diagnostic
only. Already valid task-layer selections and facts remain frozen while only
the failed task-layer is isolated. A valid general answer may therefore win
when the store layer has only a protocol failure, and a valid store partial may
still answer confirmed facts when the general layer fails.

The Judge keeps one immutable usage event per reply run. Pricing and token field
semantics are unchanged, and protocol failure does not manufacture an extra
provider call or usage record. Exact handoff fallback is also withheld when the
same layer still contains a complete factual
FAQ in `RawCandidates`; under a one-candidate quota, that factual answer is
preserved instead of silently auto-dispatching the customer.

For a multi-question message, each task closes independently. Valid selected
evidence remains available to the single Generate call. A task with a Judge
protocol failure receives only the fixed local safe
reply; it does not clear a successful sibling task and does not trigger human
handoff. A selected exact handoff directive or a genuine no-evidence service
task continues through the existing reception flow. When answers and reception
tasks coexist, the grounded answers are preserved before the deferred direct
handoff is committed.

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
- representative general questions return the intended selected FAQ and facts;
- store-specific gold answers still produce a complete store decision;
- weak or unrelated candidates produce `insufficient` without becoming a
  protocol failure;
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
