# ChronoDesk Domain Context

This glossary is the shared language for maintainers and coding agents. Code,
tests, OpenAPI, MCP tools, A2A skills, audit records, and documentation should
use these terms consistently.

## Product concepts

### Ticket

The durable business record for a support request. A Ticket owns its lifecycle,
priority, queue, assignment, SLA state, comments, attachments, history, and
optimistic `version`.

A Ticket is not an A2A Task. Protocol interaction state such as
`input-required` must never change a Ticket unless an explicit Ticket command is
accepted.

### Actor

The identity responsible for an operation. `ActorRef` has one of three types:

- `human`: a person authenticated through the browser/session flow;
- `service_principal`: an external AI Agent identity with credentials, scopes,
  policies, limits, and an emergency stop;
- `system`: a trusted ChronoDesk workflow such as SLA or automation execution.

Every durable write must record the Actor and its source protocol. A
compatibility user is a persistence bridge for legacy human-oriented columns;
it is not the authoritative Agent identity.

### Assignment

The Actor currently responsible for a Ticket. Assignment validation is a domain
invariant shared by REST, MCP, and A2A:

- a human assignee must reference an existing user;
- a service principal assignee must be active, not emergency-disabled, and have
  a compatibility user;
- a system assignee is explicit and clears human/service-principal compatibility
  columns.

Protocol adapters may translate errors but must not implement different
Assignment rules.

### Service Principal

An independent machine identity used by an external AI Agent. It never reuses a
human `agent` account. Its effective authority is the intersection of active
credentials, granted scopes, policy decisions, runtime limits, and global
controls.

### Policy Decision

The persisted result of checking whether a Service Principal may perform one
action on one resource. Denials use stable reason codes. Policy Decisions never
store model chain-of-thought.

### Ticket Lease

A time-limited exclusive claim used to coordinate concurrent Agents. A valid
Lease and matching Ticket version are both required for critical Agent writes.
Heartbeats extend a Lease; expiry or release makes it unusable.

### Agent Task

An A2A interaction lifecycle linked to, but separate from, a Ticket. Messages,
status history, and Artifacts belong to the Agent Task.

### Domain Event

An immutable CloudEvents 1.0 record created in the same transaction as the
business change. Its identity, Actor, correlation/causation chain, and resource
version are durable audit facts.

### Outbox Delivery

The retryable delivery state for one Domain Event and one destination. Business
transactions never depend on synchronous Webhook, MCP subscription, A2A Push,
notification, automation, or cleanup delivery.

### Operation Receipt

The stable result of a write: operation ID, resource ID/version, event ID,
changed fields, and policy decision ID. Replaying an idempotent request returns
the original Receipt.

## Trust model

- User text, comments, filenames, attachment bytes, and protocol payload data
  are untrusted content.
- Tool descriptions, schemas, policy configuration, server configuration, and
  trusted system workflows are control data.
- Untrusted content must never be interpolated into tool descriptions, system
  instructions, shell commands, file paths, or outbound URLs.
- ChronoDesk does not fetch arbitrary user-provided URLs.

## Architectural vocabulary

- A **Module** has one **Interface** and an internal **Implementation**.
- A **Seam** is where an Interface allows behavior to vary.
- An **Adapter** satisfies an Interface at a Seam.
- A deep Module creates **Leverage** for callers and **Locality** for
  maintainers.
- Apply the deletion test: deleting an Adapter must not delete domain rules.

The canonical module map and allowed dependencies are documented in
[ARCHITECTURE.md](ARCHITECTURE.md).
