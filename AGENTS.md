# Repository Guidelines

Read [CONTEXT.md](CONTEXT.md) for domain language and
[ARCHITECTURE.md](ARCHITECTURE.md) for Module dependencies before changing
behavior.

## Structure

- `server/cmd/chronodesk/` is the minimal executable; construction and graceful
  lifecycle live in `server/internal/app/`.
- Domain rules belong in `server/internal/services/`, not in human REST, MCP, or
  A2A Adapters.
- Machine contracts live in `server/internal/openapi/`, `internal/mcp/`, and
  `internal/a2a/`.
- `web/src/admin/<feature>/` owns feature UI; reusable HTTP, access, table, and
  localization Modules live under `web/src/lib/`, `components/`, and `i18n/`.
- Current docs live in `docs/adr/`, `docs/operations/`, `docs/reference/`, and
  `docs/testing/`. Use GitHub Issues/Projects for new plans.

## Invariants

- Human, Service Principal, and system writes use `ActorRef` and immutable audit
  metadata.
- REST, MCP, and A2A Adapters must call the same domain Interface. Never copy
  Assignment, scope, policy, lease, version, idempotency, or event rules.
- User text, comments, filenames, attachments, and Agent payloads are untrusted
  data. Never interpolate them into instructions, shell commands, paths, or
  outbound URLs.
- Business changes and CloudEvents commit in one transaction; external delivery
  uses the Outbox.
- ChronoDesk only supports MCP `2026-07-28`, A2A wire `1.0`, OpenAPI `3.2.0`,
  and CloudEvents `1.0`.

## Commands

```bash
make doctor
make install-deps
make server-dev
make web-dev
make verify
make test-race
make smoke
make e2e
```

`make dev` starts the Docker environment. `make db-migrate-seed` is for an
already configured native development database; the Docker demo uses
`docker compose exec server chronodesk-migrate -seed`.

## Style and tests

- Go stays `gofmt`-formatted; use table-driven tests and test through the same
  Interface used by callers.
- React files use PascalCase, hooks use `useX`, and imports use the `@/*` alias.
- UI copy and operation feedback are Chinese. Enterprise tables keep cells on
  one line and expose persisted, keyboard-accessible column resizing.
- Python black-box tests live only in `server/tests/` and fail closed when
  PostgreSQL, Redis, health, or authentication is unavailable.
- Add or update OpenAPI, MCP, and A2A contract tests with every machine Interface
  change.

## Commits, pull requests, and security

- Use focused Conventional Commits (`feat:`, `fix:`, `refactor:`, `test:`,
  `docs:`, `chore:`).
- PRs explain motivation, affected Modules, security/protocol impact, test
  evidence, and UI screenshots when applicable.
- Never commit `.env`, credentials, tokens, logs, generated reports, binaries,
  database files, or model chain-of-thought.
- Follow [SECURITY.md](SECURITY.md) for private vulnerability reporting and
  [CONTRIBUTING.md](CONTRIBUTING.md) for the complete contributor workflow.
