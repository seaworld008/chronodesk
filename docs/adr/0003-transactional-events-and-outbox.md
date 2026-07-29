# ADR-0003: Transactional Domain Events and Outbox

- Status: Accepted
- Date: 2026-07-29

## Context

Synchronous notification and webhook calls can fail after a Ticket transaction
commits, while calling them before commit can publish state that later rolls
back.

## Decision

Every publishable business change writes a CloudEvents 1.0 Domain Event and its
Outbox Deliveries in the same PostgreSQL transaction. Workers claim, retry,
dead-letter, replay, and observe deliveries independently.

Consumers deduplicate by event identity and persist cursors where the protocol
supports resumption.

## Consequences

- Business writes do not depend on external availability.
- Delivery is at-least-once; consumers must be idempotent.
- Event and delivery retention require operational monitoring and cleanup.

## Verification

Tests terminate processing after commit, reclaim expired deliveries, replay
failures, deduplicate events, and resume MCP/A2A/Webhook consumers from cursors.
