# Repository Guidelines

## Project Structure & Module Organization
- `server/` runs the Go API; keep domain logic in `internal/` and migrations in `cmd/migrate/`.
- `web/` is the Vite + React admin; feature views live in `src/admin/`, shared helpers in `src/lib/` and `src/utils/`.
- Shadcn primitives live in `src/components/ui/`; import them into React by mirroring files under the `@/*` alias.
- Root tooling (`Makefile`, `docker-compose.yml`, `dev.sh`, smoke-test scripts) wires together local workflows.

## Build, Test, and Development Commands
- `make dev` launches Postgres, Redis, API, and web via Docker for an end-to-end sandbox.
- `make server-dev` / `make web-dev` run backend and frontend separately during active development.
- `make build` outputs `server/bin/server` and `web/dist/`; run before packaging images or deploying.
- `make test-server` executes `go test ./...`; front-end checks rely on `cd web && npm run lint` until Jest/Vitest lands.
- `cd server && make migrate` (or `make migrate-seed`) applies schema changes; reserve `migrate-drop` for disposable environments.

## Coding Style & Naming Conventions
- Run `cd server && make fmt && make vet`; Go stays `gofmt`-formatted with lowercase packages and CamelCase exports.
- React components use PascalCase filenames (`TicketBoard.tsx`), hooks follow the `useX` pattern, and stateful helpers belong in `src/lib/`.
- Tailwind classes read layout → spacing → color; prefer utilities over bespoke CSS modules.
- Honor the `@/*` alias from `web/tsconfig.json` to avoid deep relative imports.

## Testing Guidelines
- Backend suites use `go test`; extend coverage with table-driven cases for new handlers and services.
- Python regression checks sit in `server/tests/` and top-level `test_*.py`; install deps via `pip install -r server/requirements-test.txt` and run `pytest --cov` for HTML + terminal reports.
- Smoke scripts (`./test_integration.sh`, `server/test_notification_system.sh`) validate API health and cross-service wiring.
- Review coverage artifacts in `server/htmlcov/` before merging backend-heavy changes.

## Commit & Pull Request Guidelines
- Use short imperative Conventional Commit subjects (`feat: add ticket SLA breaches`, `fix: prevent nil redis client`).
- Keep one logical change per PR and squash noisy WIP commits locally.
- PR descriptions should state motivation, affected surfaces (`server`, `web`), and the checks you ran.
- Attach screenshots or sample payloads for UI/API changes and link the relevant ticket from `docs/planning/TASK_TRACKER.json`.

## Security & Configuration Tips
- Default ports: API `:8081`, web dev `:3000`; adjust `web/vite.config.ts` and `.env` together if you relocate services.
- Copy `server/.env.example` to `.env`, inject secrets via env vars, and avoid committing credentials.
