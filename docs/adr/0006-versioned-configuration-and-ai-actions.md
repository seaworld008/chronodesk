# ADR-0006: Immutable project configuration and proposal-based AI actions

- Status: Accepted
- Date: 2026-07-30

## Context

Hard-coded Ticket types and workflows cannot serve multiple industries.
Allowing arbitrary scripts or direct Agent mutation would make configuration
and AI behavior impossible to simulate, audit, approve, or safely roll back.

## Decision

Request Types, forms, workflows, SLA calendars, routing, automation, approvals,
risk policies, Prompts, tools, and retrieval policies use draft, simulation,
approval, and immutable release semantics. A Ticket retains the exact versions
under which it was created.

Industry support is delivered as an Ed25519-signed Solution Package. Installing
or upgrading a package produces a Project configuration snapshot, diff,
compatibility result, and migration preview. Industry terminology and behavior
do not fork core code.

Agent execution is represented by `AgentRun`. A state-changing Agent produces
an immutable `ActionProposal` with a canonical digest, evidence, risk,
preview, and target Ticket version. `ApprovalTask` binds approval to that
digest, policy version, expiry, and Ticket version. A changed or expired
Proposal cannot execute.

Platform risk obligations are a non-reducible floor; a Project may only add
restrictions. Human takeover atomically invalidates the Agent claim and Ticket
Lease before responsibility changes.

## Consequences

- Published configuration is never edited in place.
- Existing Tickets remain reproducible after configuration upgrades.
- Arbitrary user scripts are not an automation extension mechanism.
- Agent and human actions use the same Assignment, version, lease, event,
  Outbox, and audit rules.
- Chain-of-thought is neither required nor persisted.

## Verification

Configuration tests cover draft validation, deterministic simulation,
publication immutability, signed package installation, upgrade diff, and
rollback. Collaboration tests reject expired Approvals, stale Ticket versions,
changed Proposal digests, and writes after human takeover.
