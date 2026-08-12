# ChronoDesk

[简体中文](README.zh-CN.md) · English

[![Smoke Tests](https://github.com/seaworld008/chronodesk/actions/workflows/smoke.yml/badge.svg)](https://github.com/seaworld008/chronodesk/actions/workflows/smoke.yml)
[![Dependency Security](https://github.com/seaworld008/chronodesk/actions/workflows/security.yml/badge.svg)](https://github.com/seaworld008/chronodesk/actions/workflows/security.yml)
[![CodeQL](https://github.com/seaworld008/chronodesk/actions/workflows/codeql.yml/badge.svg)](https://github.com/seaworld008/chronodesk/actions/workflows/codeql.yml)
[![License](https://img.shields.io/github/license/seaworld008/chronodesk)](LICENSE)

ChronoDesk is an AI-agent-native ticket and task execution platform for teams
that need trustworthy human-agent collaboration across support, IT service,
SRE, security operations, internal services, and field operations. Humans work
in a Chinese enterprise console; external Agents use stable REST, MCP, or A2A
Interfaces backed by the same authorization, concurrency, event, and audit
semantics. One private Organization can operate multiple isolated Projects;
each Project is the runtime boundary for data, configuration, Agent authority,
and external Connections.

The project is a single-organization, self-hosted modular monolith. It does not
ship an in-process model runtime, prompt-authoring platform, or autonomous
planner. It does include project-scoped knowledge lifecycle, ACL-filtered
hybrid retrieval, ticket/attachment provenance, Human-gated publication,
citations, feedback, and model-policy foundations that connect to
deployment-owned search and model gateways. Private objects use the local
filesystem by default and can be routed to an S3-compatible store.

## Product preview

Public identity flows keep sign-in, registration, and recovery in a consistent
right-side workspace. The enterprise console keeps project scope and the active
navigation path explicit.

<p align="center">
  <a href="docs/assets/screenshots/chronodesk-login-desktop.webp">
    <img src="docs/assets/screenshots/chronodesk-login-desktop.webp"
         alt="ChronoDesk enterprise sign-in page with AI ticket orchestration artwork"
         width="100%">
  </a><br>
  <sub>Enterprise sign-in for trustworthy human-agent operations</sub>
</p>

<table>
  <tr>
    <td width="75%">
      <a href="docs/assets/screenshots/chronodesk-console-navigation.webp">
        <img src="docs/assets/screenshots/chronodesk-console-navigation.webp"
             alt="ChronoDesk enterprise console with a clear active navigation hierarchy"
             width="100%">
      </a>
    </td>
    <td width="25%">
      <a href="docs/assets/screenshots/chronodesk-login-mobile.webp">
        <img src="docs/assets/screenshots/chronodesk-login-mobile.webp"
             alt="ChronoDesk responsive mobile sign-in page"
             width="100%">
      </a>
    </td>
  </tr>
  <tr>
    <td align="center"><sub>Clear active hierarchy and adjustable console navigation</sub></td>
    <td align="center"><sub>Responsive mobile sign-in</sub></td>
  </tr>
</table>

## Why ChronoDesk

- **One Ticket domain, five Adapters**: Human REST/WebSocket, Agent REST,
  MCP, A2A, and Connector/Inbox adapters share the same domain
  Implementation.
- **Machine identity is first-class**: Service Principals, short-lived tokens,
  minimum scopes, policy decisions, rate/concurrency limits, emergency stops,
  and delegated audit trails.
- **Safe concurrent automation**: `Idempotency-Key`, optimistic versions,
  `ETag` / `If-Match`, and expiring Ticket Leases.
- **Recoverable event delivery**: CloudEvents 1.0 Domain Events and a
  transactional Outbox with retry, replay, deduplication, and observability.
- **Untrusted-content discipline**: comments, attachments, filenames, and Agent
  payloads are always data, never control instructions.
- **Enterprise console**: Chinese interaction copy, object-level permissions,
  no-wrap/resizable tables, persisted widths, responsive navigation, SLA,
  automation, notifications, Webhooks, and Agent operations.

## Use cases and industries

ChronoDesk is broader than a traditional support desk. If work can be expressed
as “intake → triage and assignment → execution → information gathering →
review or escalation → completion,” a Ticket can coordinate humans, AI Agents,
and enterprise systems in one auditable workflow.

| Industry or team | Typical use cases |
| --- | --- |
| IT and software | Service desk, access requests, bugs, cloud resources, alerts, incidents, and changes |
| Support, retail, and commerce | After-sales support, complaints, refunds, logistics exceptions, suppliers, and VIP escalation |
| Manufacturing, energy, and facilities | Equipment faults, quality exceptions, inspections, repairs, spare parts, and maintenance records |
| Finance, insurance, and professional services | Complaints, document checks, claims assistance, risk investigation, legal, and finance requests |
| Healthcare, education, and public service | Equipment operations, administration, student or citizen requests, document routing, and supervision |
| Data and content operations | Content review, data-quality issues, labeling tasks, and exception remediation |
| Enterprise shared services | HR, administration, procurement, finance, compliance, and cross-team requests |

ChronoDesk is a particularly good fit when:

- status, ownership, and escalation paths are explicit, with SLA, notification,
  and audit requirements;
- AI must query and execute real operations instead of only drafting text;
- multiple humans or Agents may work concurrently and need idempotency,
  versions, and Ticket Leases;
- automation must coexist with least privilege, policy, circuit breakers, and
  human takeover.

## Human-agent collaboration

ChronoDesk supports several operating models, from assistance to policy-bound
autonomy:

1. **AI assists humans**: classify, summarize, complete fields, and recommend
   assignees or replies while a human decides what to execute.
2. **AI handles first, humans take over**: an Agent performs initial diagnosis
   and escalates when confidence, information, or permission is insufficient.
3. **AI acts autonomously within policy**: an Agent can claim a Ticket, add
   comments, transition status, and release its lease. Scopes, policies,
   limits, versions, leases, and circuit breakers constrain risk.
4. **Humans supervise multiple Agents**: administrators inspect identities,
   credentials, policy decisions, live leases, event delivery, and audit trails,
   and can disable an Agent, enter read-only mode, or take over.
5. **Agent-to-Agent collaboration**: MCP exposes Ticket tools while A2A carries
   tasks, messages, status, and Artifacts. Every participant remains anchored
   to the same business Ticket.

```text
Email / alert / human / external Agent creates a Ticket
→ AI classifies, prioritizes, and claims it
→ automation resolves it or requests more information
→ low-risk operations complete within policy
→ high-risk, exceptional, or low-confidence work escalates to a human
→ completion, notification, and a reconstructable audit trail
```

### Current boundaries

- The current deployment model is single-organization and self-hosted. Model
  inference and autonomous planning live in an external Agent platform or
  deployment-owned gateway. ChronoDesk owns the project-scoped knowledge,
  retrieval-policy, citation, and audit boundary.
- ChronoDesk can coordinate exceptions produced by real-time control, clinical,
  trading, or other specialist systems, but it does not replace those systems
  or their professional decisions.
- Multi-tenancy, billing, embedded model execution, production
  ingestion/scanning and document-conversion workers, and outbound delegation
  are later phases.

### Organization, Project, and access boundaries

ChronoDesk is AI-native in the operational sense: humans and Agents can use the
same project-scoped domain operations, while identity and authorization remain
server-owned. A single Organization can contain multiple Projects, but a
Project never inherits another Project's data or authority.

- Platform roles are exactly `platform_admin`, `security_auditor`,
  `emergency_operator`, and `member`. They govern platform operations only.
- Project roles are exactly `project_admin`, `manager`, `agent`, `requester`,
  and `observer`, and exist only on an active `ProjectMembership`.
- A Project administrator may independently grant an ordinary Membership the
  draft-only knowledge-contributor capability. It lets that Human create and
  revise personally managed drafts, but never publish, change ACLs, model
  policy, or indexes; administrators and managers retain those review gates.
- These two closed role sets are independent and unordered: a platform role
  never grants a Project role or project access.
- A Human JWT contains only its platform-role assertion. Every authenticated
  request revalidates the active user and platform role; a changed assertion is
  rejected as `stale_token`. Project access and project role are resolved from
  active Membership on each project request.

The Human REST surface reflects this split: `/api/platform/*` is narrow
platform governance, `/api/projects/*` is project-scoped work, and
`/api/workbench/*` aggregates only the caller's active Membership projects.
The Human Web contract is published at `/human-openapi.json`; Agent machine
contracts remain separate from it.

## Current protocol baseline

ChronoDesk intentionally supports only the current protocol line:

| Interface | Version |
| --- | --- |
| MCP | `2026-07-28`, stateless Streamable HTTP |
| A2A | official release `v1.0.1`, wire `A2A-Version: 1.0` |
| OpenAPI | `3.2.0` |
| CloudEvents | `1.0` |

There are no legacy MCP sessions, downgraded schemas, or old A2A method aliases.

## Architecture

```mermaid
flowchart LR
    Users["Support teams"] --> Human["Human REST + WebSocket"]
    Agents["External AI Agents"] --> REST["Agent REST /api/v2/projects/{projectKey}"]
    Agents --> MCP["MCP 2026-07-28"]
    Agents --> A2A["A2A 1.0"]
    Systems["External systems"] --> Connector["Connector / signed Inbox"]
    Human --> Adapters["Protocol Adapters"]
    REST --> Adapters
    MCP --> Adapters
    A2A --> Adapters
    Connector --> Adapters
    Adapters --> Domain["Ticket / policy / lease Domain"]
    Domain --> PG["PostgreSQL"]
    Domain --> Redis["Redis"]
    PG --> Outbox["CloudEvents + Outbox"]
    Outbox --> Integrations["Notifications / Webhooks / subscriptions / Push"]
```

Read [ARCHITECTURE.md](ARCHITECTURE.md) for the Module map and dependency rules,
and [CONTEXT.md](CONTEXT.md) for the domain language expected in code and docs.

## 60-second local demo

Requirements: Docker with Compose v2.

```bash
git clone https://github.com/seaworld008/chronodesk.git
cd chronodesk
make dev
docker compose exec server chronodesk-migrate -seed
```

Open:

- Console: <http://localhost:3000>
- Health: <http://localhost:8081/healthz>
- OpenAPI: <http://localhost:8081/openapi.yaml>
- Human Web OpenAPI: <http://localhost:8081/human-openapi.json>
- Platform governance: `http://localhost:8081/api/platform/*`
- Project work: `http://localhost:8081/api/projects/{projectKey}/*`
- Membership-scoped workbench: `http://localhost:8081/api/workbench/*`
- Agent REST: `http://localhost:8081/api/v2/projects/{projectKey}`
- MCP: <http://localhost:8081/mcp>
- A2A Agent Card: <http://localhost:8081/.well-known/agent-card.json>

The Compose credentials and secrets are development-only. Never reuse them in a
shared or production environment.

Stop and remove the local services with:

```bash
make docker-down
```

## Native development

Requirements:

- Go `1.26.5`
- Node.js `24`
- Python `3.12+`
- PostgreSQL `18`
- Redis `8`

```bash
make doctor
cp server/.env.example server/.env
make install-deps
make db-migrate-seed
```

Run the API and Web console in separate terminals:

```bash
make server-dev
make web-dev
```

Local secrets belong in ignored `.env` files or an external secret manager.

## Quality gates

```bash
make test          # Go tests/vet, Web type/lint/audit, OpenAPI lint
make security      # govulncheck and Web production dependency policy
make build         # API, migration binary, and production Web assets
make test-race     # Go race detector
make smoke         # all black-box API suites against a running environment
make e2e           # Playwright browser suite against a running environment
make verify        # complete local release gate
```

CI also runs CodeQL, secret scanning/push protection, dependency updates, and
the Docker-backed smoke path.

## Repository layout

```text
server/
  cmd/chronodesk/       minimal executable
  cmd/migrate/          explicit migration/seed command
  cmd/credential-maintain/ current-format validation, rotation, and quarantine
  cmd/chronodeskctl/     connection and machine-contract diagnostics
  internal/app/         composition root and graceful lifecycle
  internal/services/    shared domain/application rules
  internal/agentplatform/
                        Agent REST and MCP/A2A domain Adapters
  internal/eventcontract/ canonical CloudEvent type catalog
  internal/scopeddb/     project-scoped transaction routing
  internal/mcp/         MCP protocol Module
  internal/a2a/         A2A protocol Module
  internal/openapi/     embedded OpenAPI 3.2 contract
  internal/humanopenapi/ Human Web OpenAPI 3.2 contract
  internal/asyncapi/     embedded CloudEvents/stream contract
web/
  src/admin/            React Admin feature slices
  src/components/       shared enterprise UI Modules
sdk/
  go/                   generated Go Agent SDK
  typescript/           generated TypeScript Agent SDK
  python/               generated Python Agent SDK
docs/
  adr/                  accepted architecture decisions
  operations/           deployment and migration guides
  reference/            protocol and machine-contract references
  testing/              durable verification reports
```

## Documentation

- [Project manual](docs/PROJECT_MANUAL.md)
- [Architecture decisions](docs/adr/README.md)
- [AI-native multi-project status](docs/reference/AI_NATIVE_UPGRADE_PROGRESS.md)
- [Agent REST and machine contract](docs/reference/API_DOCUMENTATION.md)
- [MCP integration](docs/reference/MCP_2026_07_28.md)
- [A2A integration](docs/reference/A2A_1_0.md)
- [CloudEvents 1.0](docs/reference/CLOUDEVENTS_1_0.md)
- [Integration SDKs](docs/reference/INTEGRATION_SDKS.md)
- [Integration tooling](docs/reference/INTEGRATION_TOOLING.md)
- [Database migrations](docs/operations/database-migrations.md)
- [Testing guide](docs/testing_guide.md)
- [P1 platform/project role cutover evidence](docs/testing/P1_PLATFORM_ROLES_RELEASE_EVIDENCE_2026-07-31.md)
- [Historical Agent-native verification report](docs/testing/CHRONODESK_AGENT_NATIVE_FULL_TEST_REPORT_2026-07-30.md)

## Project status

ChronoDesk is in active pre-1.0 development. Protocol contracts and security
invariants are tested, but release compatibility is not promised until the
first stable release. See [ROADMAP.md](ROADMAP.md) and
[CHANGELOG.md](CHANGELOG.md).

## Contributing and security

Read [CONTRIBUTING.md](CONTRIBUTING.md) before opening a pull request. Use
[GitHub Discussions](https://github.com/seaworld008/chronodesk/discussions) for
design questions and [GitHub Issues](https://github.com/seaworld008/chronodesk/issues)
for reproducible defects.

Do not report vulnerabilities in a public issue. Follow
[SECURITY.md](SECURITY.md) and submit a
[private security advisory](https://github.com/seaworld008/chronodesk/security/advisories/new).

## License

Licensed under the [Apache License 2.0](LICENSE).
