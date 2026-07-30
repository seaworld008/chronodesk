# ADR-0007: Inbox, immutable mapping, and explicit external identity

- Status: Accepted
- Date: 2026-07-30

## Context

Legacy systems, SaaS applications, event brokers, email, and external Agents
deliver duplicate, delayed, and out-of-order messages. Directly translating
each protocol into database writes would duplicate domain rules and make
recovery or conflict review unreliable.

## Decision

Every project-bound integration uses a versioned `ConnectorDefinition` and
`Connection`. Inbound data first becomes an `InboxMessage` after connection
authentication, timestamped signature validation, replay-window validation,
and payload bounds checks.

An immutable `MappingVersion` converts the message into a shared domain
command. `InboxReceipt` makes the domain effect idempotent.
`ExternalLink` binds `(Project, Connection, object type, external ID)` to one
internal resource. `SyncCursor`, `SyncRun`, conflict, dead-letter, and replay
records make progress and operator decisions durable.

The business change, External Link, Inbox Receipt, Domain Event, and Outbox
Deliveries commit in one transaction. Each mapped field declares ChronoDesk,
the external system, or neither as its authority. Unresolved conflicts enter an
operator queue; last-write-wins is forbidden.

Legacy network access runs in an outbound Relay controlled by the customer
network. ChronoDesk core does not fetch arbitrary database, file, SFTP, or
private-network URLs supplied by connector data.

## Consequences

- Duplicate or crash-replayed input has one domain effect.
- Mapping upgrades do not reinterpret already processed messages.
- Operators can inspect, replay, or dead-letter integration failures without
  editing business records manually.
- REST, Webhook, CloudEvents, broker, email, Relay, MCP, and A2A Adapters call
  the same project-scoped domain interface.

## Verification

Integration tests cover valid and rotated signatures, stale timestamps,
duplicates, crash replay, mapping dry-run, authority conflicts, dead-letter
replay, and atomic rollback when event or receipt persistence fails.
