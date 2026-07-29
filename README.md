# ChronoDesk

[简体中文](README.zh-CN.md) · English

[![Smoke Tests](https://github.com/seaworld008/chronodesk/actions/workflows/smoke.yml/badge.svg)](https://github.com/seaworld008/chronodesk/actions/workflows/smoke.yml)
[![Dependency Security](https://github.com/seaworld008/chronodesk/actions/workflows/security.yml/badge.svg)](https://github.com/seaworld008/chronodesk/actions/workflows/security.yml)
[![CodeQL](https://github.com/seaworld008/chronodesk/actions/workflows/codeql.yml/badge.svg)](https://github.com/seaworld008/chronodesk/actions/workflows/codeql.yml)
[![License](https://img.shields.io/github/license/seaworld008/chronodesk)](LICENSE)

ChronoDesk is an AI-agent-native ticket automation platform for support and
operations teams. Humans work in a Chinese enterprise console; external Agents
use stable REST, MCP, or A2A Interfaces backed by the same authorization,
concurrency, event, and audit semantics.

The project is a single-organization, self-hosted modular monolith. It does not
embed an LLM, RAG system, prompt platform, or autonomous planner.

## Why ChronoDesk

- **One Ticket domain, four Interfaces**: human REST/WebSocket, Agent REST,
  MCP, and A2A share the same domain Implementation.
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
    Agents["External AI Agents"] --> REST["Agent REST /api/v1"]
    Agents --> MCP["MCP 2026-07-28"]
    Agents --> A2A["A2A 1.0"]
    Human --> Adapters["Protocol Adapters"]
    REST --> Adapters
    MCP --> Adapters
    A2A --> Adapters
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
- Agent REST: <http://localhost:8081/api/v1>
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
  internal/app/         composition root and graceful lifecycle
  internal/services/    shared domain/application rules
  internal/agentplatform/
                        Agent REST and MCP/A2A domain Adapters
  internal/mcp/         MCP protocol Module
  internal/a2a/         A2A protocol Module
  internal/openapi/     embedded OpenAPI 3.2 contract
web/
  src/admin/            React Admin feature slices
  src/components/       shared enterprise UI Modules
docs/
  adr/                  accepted architecture decisions
  operations/           deployment and migration guides
  reference/            protocol and machine-contract references
  testing/              durable verification reports
```

## Documentation

- [Project manual](docs/PROJECT_MANUAL.md)
- [Architecture decisions](docs/adr/README.md)
- [Agent REST and machine contract](docs/reference/API_DOCUMENTATION.md)
- [MCP integration](docs/reference/MCP_2026_07_28.md)
- [A2A integration](docs/reference/A2A_1_0.md)
- [Database migrations](docs/operations/database-migrations.md)
- [Testing guide](docs/testing_guide.md)
- [Full Agent-native verification report](docs/testing/CHRONODESK_AGENT_NATIVE_FULL_TEST_REPORT_2026-07-30.md)

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
