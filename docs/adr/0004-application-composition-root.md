# ADR-0004: Minimal executable and application composition root

- Status: Accepted
- Date: 2026-07-29

## Context

A large repository-root `main.go` exposes construction details as the executable
Interface, makes lifecycle behavior hard to test, and obscures the canonical
entry point for contributors and coding agents.

## Decision

`server/cmd/chronodesk` is the minimal executable. `server/internal/app` owns
configuration, Adapter construction, route registration, background workers, and
graceful shutdown.

The Go module uses the canonical repository import path. Build, Docker, and
development commands invoke `./cmd/chronodesk`.

## Consequences

- The executable Interface is a single `app.Run` call.
- Construction and shutdown behavior have one location.
- Further composition refactoring can occur behind the `app` Module without
  changing build commands.

## Verification

Go tests compile both Modules, Docker builds `./cmd/chronodesk`, and SIGTERM
causes HTTP shutdown followed by deferred scheduler, MCP, database, and worker
cleanup.
