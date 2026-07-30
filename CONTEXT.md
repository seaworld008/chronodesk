# ChronoDesk Domain Context

This glossary is the shared language for maintainers and coding agents. Code,
tests, OpenAPI, MCP tools, A2A skills, audit records, and documentation should
use these terms consistently.

## Product concepts

### Organization, Business Unit, and Project

An Organization is the top-level owner in the first private-cloud release. A
Business Unit groups Projects for governance and reporting, but is not a data
security boundary.

A Project is the only runtime boundary for Ticket data, configuration,
knowledge, Agent authority, and external Connections. Every project-owned
command and query carries a server-resolved `ProjectScope`; request bodies,
custom fields, and A2A metadata can name a public project key but can never
construct the trusted numeric scope.

`ProjectMembership` grants a human one project role.
`ProjectPrincipalGrant` grants a Service Principal a bounded role and scope
set. Platform administrators may enter an explicit management overview, while
ordinary cross-project work uses only active memberships.

### Team and Queue

A Team groups human workers. A Queue is the project-local routing and
responsibility destination for Tickets. Neither is an Actor or an assignee:
the final writer and assignee remain a human or Service Principal.

Cross-project collaboration creates a linked Ticket in the destination
Project. The source Ticket retains primary responsibility; Tickets are not
moved across security boundaries.

### Ticket

The durable business record for a support request. A Ticket owns its lifecycle,
priority, queue, assignment, SLA state, comments, attachments, history, and
optimistic `version`. Its public number is allocated from the owning Project as
`{PROJECT_KEY}-{SEQUENCE}`.

A Ticket is not an A2A Task. Protocol interaction state such as
`input-required` must never change a Ticket unless an explicit Ticket command is
accepted.

Attachments are durable `TicketAttachment` records linked to a Ticket and,
optionally, a Comment. Ticket and Comment payloads never embed parallel
attachment-reference arrays.

### Request Type and Configuration Release

A `RequestTypeVersion` combines a JSON Schema 2020-12 form contract, a separate
UI schema, and a normalized `work_class`. A `WorkflowVersion` is an immutable
state graph whose project-specific states map to the common lifecycle
categories `new`, `active`, `waiting`, `resolved`, `closed`, and `cancelled`.

Project configuration follows draft, simulation, approval, and immutable
release semantics. Existing Tickets retain the Request Type, Workflow, SLA,
routing, automation, approval, and policy versions under which they were
created.

A signed Solution Package is a versioned bundle of those assets. Installation
creates a project snapshot; upgrades require a diff and migration preview and
can roll back to an earlier immutable release.

### Actor

The identity responsible for an operation. `ActorRef` has one of three types:

- `human`: a person authenticated through the browser/session flow;
- `service_principal`: an external AI Agent identity with credentials, scopes,
  policies, limits, and an emergency stop;
- `system`: a trusted ChronoDesk workflow such as SLA or automation execution.

Every durable write must record the Actor and its source protocol. Human user
foreign keys are optional projections used only when the Actor is actually a
human; service principals and system workflows never borrow a human identity.

### Assignment

The Actor currently responsible for a Ticket. Assignment validation is a domain
invariant shared by REST, MCP, and A2A:

- a human assignee must reference an existing user;
- a service principal assignee must be active and not emergency-disabled;
- `system` is a valid write Actor but not an assignable worker; system
  workflows must assign an explicit human or service principal, or release the
  Ticket to the queue.

Protocol adapters may translate errors but must not implement different
Assignment rules.

### Operation Context

`OperationContext` is trusted control data created only after authentication
and project authorization. It carries `ProjectScope`, `ActorRef`, source
protocol, credential identity, trace ID, and correlation ID. Human REST, Agent
REST, MCP, A2A, Connector, and Worker adapters all call the same scoped domain
interfaces with this context.

PostgreSQL Row-Level Security is an independent enforcement layer. Project
repositories and workers must still include explicit scope predicates and use
transaction-local scope settings; RLS is not a substitute for clear domain
interfaces.

### Service Principal

An independent machine identity used by an external AI Agent. It never reuses a
human `agent` account. Its effective authority is the intersection of active
credentials, one explicit project grant, granted scopes, policy decisions,
runtime limits, and global controls.

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

### Agent Run, Proposal, Approval, and Handoff

An `AgentRun` records execution status, model/tool/Prompt versions, usage,
cost, policy snapshot, and result. It never stores chain-of-thought.

An `ActionProposal` is an immutable digest of a structured intended action,
target Ticket version, evidence, preview, and risk. An `ApprovalTask` binds
approval to that digest, policy version, and expiry; changed proposal content
or Ticket version invalidates it. A `Handoff` records responsibility transfer,
completed work, missing information, reason, and evidence.

Human takeover atomically revokes the Agent execution claim and Ticket Lease
before responsibility changes. A stopped Agent cannot continue writing with an
old claim, approval, or version.

### Evidence and Knowledge

`EvidenceReference` identifies an immutable knowledge version, external object,
or Artifact together with a content hash. Knowledge metadata is authoritative
in PostgreSQL; source files live in object storage and a rebuildable
OpenSearch index provides project- and ACL-filtered hybrid retrieval.

Filtering occurs before ranking. A citation identifies the document version,
page or chunk, and content hash. Prompts, tools, and retrieval policies are
versioned control data; Ticket text, email, attachments, and retrieved content
remain untrusted data.

### Connection and Inbox

A `ConnectorDefinition` describes one versioned connector capability and
configuration schema. A project-bound `Connection` owns authentication,
health, limits, and credential rotation. `MappingVersion` is the immutable
field/status/identity mapping used for one inbound or outbound operation.

Inbound data first becomes an `InboxMessage`. Signature verification, replay
window enforcement, and `InboxReceipt` idempotency happen before the shared
domain command. `ExternalLink` supplies stable identity correlation, while
`SyncCursor`, `SyncRun`, conflict, dead-letter, and replay records make
integration state observable. Conflicts never use last-write-wins.

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
- Project keys supplied by REST, MCP, A2A, or Connectors are authorization
  inputs, not trusted scope. The server resolves and persists the authorized
  project ID.
- Platform risk obligations cannot be weakened by a Solution Package or
  Project. Deletion, bulk mutation, external communication, permission or
  credential changes, financial actions, and other irreversible actions
  require the configured approval threshold.

## Architectural vocabulary

- A **Module** has one **Interface** and an internal **Implementation**.
- A **Seam** is where an Interface allows behavior to vary.
- An **Adapter** satisfies an Interface at a Seam.
- A deep Module creates **Leverage** for callers and **Locality** for
  maintainers.
- Apply the deletion test: deleting an Adapter must not delete domain rules.

The canonical module map and allowed dependencies are documented in
[ARCHITECTURE.md](ARCHITECTURE.md).
