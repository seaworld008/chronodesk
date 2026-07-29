# ADR-0002: One Actor and Assignment model

- Status: Accepted
- Date: 2026-07-29

## Context

Human REST, Agent REST, MCP, A2A, automation, and SLA workflows can all mutate a
Ticket. Duplicating identity or Assignment validation in protocol Adapters
causes different behavior and incomplete audit trails.

## Decision

All writes use `ActorRef` with `human`, `service_principal`, or `system`.
Assignment is validated by one domain Module before a versioned Ticket update.
Protocol Adapters only decode transport values and translate stable errors.

Compatibility user columns remain a persistence bridge. The Actor fields are
authoritative for audit and Agent identity.

## Consequences

- MCP and A2A accept and reject the same assignees.
- Service Principal status, emergency stop, and compatibility mapping are
  checked once.
- Deleting a protocol Adapter cannot delete Assignment rules.

## Verification

Domain and cross-protocol tests cover missing humans, disabled or
emergency-disabled Service Principals, system Assignment, and successful change
sets.
