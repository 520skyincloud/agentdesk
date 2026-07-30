# AgentDesk

[简体中文](README.md) | English

An open-source, multi-tenant AI Agent customer support system with knowledge-grounded replies, rule-based dispatch, human handoff, WeCom customer-acquisition arrival flows, billing attribution, and self-hosted deployment.

> Built for teams that need online support, knowledge-base Q&A, human collaboration, and service tracking in one system. It is not just an LLM inside a chat box; it is an AI Helpdesk foundation designed around real support operations.

## Product Preview

Tenant onboarding, store operations, customer conversations, rule-based dispatch, managed knowledge, and AI replies are managed in one system.

### Support Operations Overview

![Weibao support operations overview](screenshots/dashboard-overview.jpg)

The overview separates live queues, SLA alerts, human handling, AI handling, agent presence,
and daily totals, with drill-down links constrained by the current account's data scope.

### Service Analytics

![Weibao service analytics](screenshots/service-analytics.jpg)

The report combines service overview, response efficiency, agent performance, quality and
satisfaction, dispatch quality, and source analysis with date, organization, store, WeCom
employee account, and data-quality filters.

### Managed Knowledge and Model Profiles

Each store uses one managed FastGPT dataset. The platform publishes complete nine-slot model profiles through a unified NewAPI gateway and assigns one active profile revision to each store. Store credentials remain masked, revisioned, and independently auditable.

## Why Use It

- **AI-first support**: Let AI Agents handle common questions, standard procedures, and knowledge-base answers first.
- **Knowledge-constrained replies**: Use RAG and the Answerability Gate to decide whether retrieved knowledge is strong enough to answer, reducing unsupported responses.
- **Natural human handoff**: Move to human agents when knowledge is insufficient, the user asks for help, or a workflow requires human confirmation.
- **Conversation-to-ticket loop**: Online chat, support handling, ticket creation, status flow, and progress records stay in one system.
- **Built for extension**: The backend uses Go, the frontend uses Next.js, and the runtime supports Skills, MCP, and OpenAI-compatible model access.
- **Self-host friendly**: Supports SQLite / MySQL, a managed FastGPT knowledge service, and a unified NewAPI gateway for enterprise deployment.

## Core Capabilities

- **AI reply runtime**: Industry-bound intent detection, planning, knowledge retrieval, validation, confirmation, tool calling, and human collaboration run through one reply engine.
- **Online conversation system**: Visitor sessions, message send/receive, unread status, assignment, transfer, and close flows.
- **Agent workspace**: Agents can take over conversations, reply to users, transfer teammates, link customers, and create tickets.
- **Managed knowledge RAG**: Tenant- and store-scoped FastGPT datasets, file synchronization, retrieval logs, and answerability analysis.
- **Answerability Gate**: Checks whether retrieved content can support an answer; otherwise returns a fallback and recommends human support.
- **Ticket system**: Create tickets from conversations, categorize, assign, move through status flows, record progress, and close the loop.
- **Support organization management**: Agent profiles, teams, schedules, and automatic assignment.
- **WeCom arrival flow**: Reuses one real customer-acquisition link per store and member, adds a
  separate opaque channel state to each scan, reconciles official customer events, and routes
  already-bound visits through the existing employee-account delivery path.
- **AI extensibility**: Skills, MCP debugging, and external tool integration.
- **Multiple entry points**: Admin dashboard, agent workspace, customer-facing web pages, and embeddable SDK.

## Use Cases

- Website live support
- SaaS product support
- AI + human hybrid support
- Internal enterprise service desk
- After-sales service, incident reporting, complaints, and operations support
- Support teams that need knowledge-base Q&A with human collaboration

## Quick Start

The fastest way to try the full stack is Docker Compose:

```bash
cp .env.example .env
# Fill every required blank value in .env, then restrict access to the file.
chmod 600 .env
docker compose config --quiet
docker compose up -d --build
```

Compose intentionally refuses to start without independent database, invitation, customer-session, asset-signing, and Store credential encryption secrets. Runtime backups and `.env` files must remain outside Git. See the [production secret and external credential runbook](docs/deployment/production-secrets.md) for exact formats, ownership, rotation limits, and the boundary between the FastGPT integration token and each Store's NewAPI key.

For the WeCom arrival flow, production must explicitly set
`AGENT_DESK_ARRIVAL_CONTACT_PROVIDER=customer_acquisition`. The legacy `contact_way` provider
is retained only for compatibility and operator-controlled rollback; permission, quota, or link
errors never trigger an automatic downgrade. See the
[arrival link engine design](docs/design/arrival-link-engine.md) for authorization, callback,
quota, QR, reconciliation, and acceptance gates.

The active service keeps `AGENT_DESK_BACKGROUND_WORKERS_ENABLED=true`. The current release must start from an empty SQLite/MySQL database. Historical databases may only be restored in an isolated, read-only environment with this application stopped; they are not supported migration inputs.

For production deployment, see the [Chinese deployment guide](docs/deployment/deployment-guide.md).

To embed customer support on your website, see [Web Widget Integration](https://agent-desk.huabei.pro/docs/integration/web-widget.html).

Compose starts:

- `agent-desk`: application service on port `8083`
- `mysql`: MySQL 8.4 with the `mysql-data` volume

After startup, open:

- Admin dashboard: `http://localhost:8083/dashboard`
- Agent workspace: `http://localhost:8083/dashboard/conversations`
- Customer web integration demo: `http://localhost:8083/support/demo`
- Customer chat page: `http://localhost:8083/support/chat`

The initial administrator is created only on a fresh database from
`AGENT_DESK_BOOTSTRAP_ADMIN_USERNAME` and `AGENT_DESK_BOOTSTRAP_ADMIN_PASSWORD`.
Set both values through the deployment secret file before first startup. Do not rely
on fallback credentials in any shared or internet-facing environment.

## Local Development

### Requirements

- Go `1.26+`
- Node.js `20+`
- `pnpm`

### Prepare Configuration

```bash
cp config/config.example.yaml config/config.yaml
```

The default configuration uses:

- SQLite: `data/app.db`
- Backend: `http://127.0.0.1:8083`
- Managed FastGPT uses deployment settings and an environment-only integration token.
- NewAPI model profiles are managed by platform administrators; each Store supplies its own encrypted API key.

Install frontend dependencies:

```bash
cd web
pnpm install
cd ..
```

Start backend and frontend development servers together:

```bash
make dev
```

Or start them separately:

```bash
make run-go
make web-dev
```

Default development URLs:

- Admin dashboard: `http://localhost:3000/dashboard`
- Agent workspace: `http://localhost:3000/dashboard/conversations`
- Customer web integration demo: `http://localhost:3000/support/demo`
- Customer chat page: `http://localhost:3000/support/chat`

## Tech Stack

- Backend: Golang + Gin + GORM + `github.com/mlogclub/simple`
- Frontend: Next.js 16 + React 19 + shadcn/ui + Tailwind CSS
- Database: SQLite / MySQL
- Knowledge service: Managed FastGPT
- AI: Unified NewAPI gateway + OpenAI-compatible models + RAG + Skills + MCP

## Project Structure

```text
.
├── cmd/                    # server / migration / generator / testdata
├── internal/
│   ├── bootstrap/          # startup, routes, database, and migration initialization
│   ├── builders/           # model / aggregate result to response DTO mapping
│   ├── handlers/           # dashboard / api / third HTTP handlers
│   ├── middleware/         # Gin middleware
│   ├── migration/          # idempotent data migrations
│   ├── models/             # GORM models
│   ├── repositories/       # data access layer
│   ├── services/           # business orchestration and transaction boundaries
│   ├── ai/                 # LLM / RAG / Runtime / Skills / MCP
│   └── pkg/                # config / dto / enums / httpx / utils and shared packages
├── web/                    # Next.js frontend project
│   ├── app/dashboard/      # admin dashboard and agent workspace
│   ├── app/support/        # customer integration and chat pages
│   ├── components/         # React components
│   ├── lib/                # API client, SDK source, and utilities
│   └── public/sdk/         # built embeddable SDK
├── config/                 # configuration files
├── docker/                 # Docker configuration
└── docs/                   # documentation site
```

## Common Commands

```bash
make dev            # start backend and frontend development servers
make run            # build the frontend SPA, then start the backend
make run-go         # start the backend and ensure the SPA has been built
make web-dev        # start the frontend development server
make build          # build the frontend SPA and current-platform Go binary
make build-linux    # build the linux/amd64 binary
make release        # build common release binaries
make web-build-spa  # build the web static SPA and embeddable SDK
make test           # run Go tests after ensuring the SPA is built
make check          # run Go tests, frontend typecheck, and lint
make generator      # run code generation
make enums          # generate frontend enums
make migration      # run migrations
make testdata       # initialize demo/test data
```

## AI Agent Workflow

```mermaid
flowchart TD
    A[User starts a support request<br/>Web support entry / Open API] --> B[Create or match a conversation]
    B --> C[Customer sends a message]
    C --> D[Trigger AI Reply Runtime]
    D --> E[Load conversation history / tenant industry / store model profile]
    E --> F[Retrieve from the store FastGPT dataset]
    F --> G{Are retrieved chunks enough to answer?}
    G -- No --> Z[Return knowledge fallback<br/>and recommend human support]
    G -- Yes --> H[Prepare Skills / MCP Tools]
    H --> I[Pass trusted knowledge context to the Agent]
    I --> J{Direct reply?}
    J -- Yes --> K[LLM generates a knowledge-grounded reply]
    J -- No --> N{Call Graph / MCP Tool?}
    N -- Yes --> O[Run Skill / Graph / MCP Tool]
    O --> P{Need user confirmation?}
    P -- No --> I
    P -- Yes --> Q[Ask the user to confirm]
    Q --> R{Confirmation result}
    R -- Confirm handoff --> S[Move conversation to human handoff pool]
    S --> T[Automatic or manual assignment]
    T --> U[Agent workspace takeover]
    U --> V{Need ticket tracking?}
    V -- Yes --> W[Create or link a ticket]
    V -- No --> X[Human agent continues handling]
    W --> X
    X --> Y[Resolve and close]
    R -- Confirm ticket --> AA[Create a ticket from the current conversation]
    AA --> I
    R -- Cancel --> K
    N -- No --> K
```

## Support Loop

```mermaid
flowchart LR
    A[Customer request] --> B[AI Agent handles first]
    B --> C{Can the knowledge base answer?}
    C -- Yes --> D[AI replies with trusted knowledge]
    C -- No --> E[Fallback / recommend human support]
    D --> F{Need a human?}
    E --> G[Human takeover]
    F -- No --> H[Conversation ends or data is retained]
    F -- Yes --> G
    G --> I[Agent workspace handles the case]
    I --> J{Need follow-up tracking?}
    J -- Yes --> K[Create / link a ticket]
    J -- No --> L[Resolve directly]
    K --> M[Ticket status flow and progress records]
    M --> N[Complete]
    L --> N
```

## Docker Image

If you only need to build the application image, prepare MySQL and the configured external AI services, then mount a configuration file:

```bash
docker build -t mlogclub/agent-desk .
docker run --rm -p 8083:8083 --env-file .env \
  -e APP_ENV=production -e AGENT_DESK_ENV=production \
  -v $(pwd)/docker/agent-desk.yaml:/app/config/config.yaml:ro \
  -v agent-desk-data:/app/data \
  mlogclub/agent-desk
```

Compose uses [docker/agent-desk.yaml](docker/agent-desk.yaml) only for non-secret settings. All deployment secrets come from the ignored `.env` file or a production secret manager. NewAPI calls and billing queries use credentials submitted through each Store's credential workflow; Store keys never belong in `.env`, and there is no platform-wide NewAPI usage token.

Do not connect this image to a historical business database. Restore old backups only with the matching archived code in an isolated, outbound-blocked environment.

## Open-source Positioning

`AgentDesk` is useful as an open-source foundation for:

- AI customer support systems
- AI Helpdesk / AI Support Platform projects
- RAG answerability + human handoff implementation references
- Enterprise AI Agent application frameworks

If you are looking for a customer support system centered on AI Agents rather than a simple LLM chat box, this project is designed for that purpose.
