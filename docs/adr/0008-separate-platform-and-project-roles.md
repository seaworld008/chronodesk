# ADR-0008: Separate platform and Project roles

- Status: Accepted
- Date: 2026-07-31

## Context

The historical Human `role` field mixed platform governance duties with Project
business responsibilities. Treating a global administrator as a project
administrator makes a multi-Project boundary illusory: an identity can gain
Ticket, configuration, integration, or Agent authority without an explicit
Project relationship. Storing a project role in a long-lived Human JWT creates
the same problem after Membership changes.

ChronoDesk needs narrow platform governance, explicit project work, and a
membership-scoped cross-project workbench without copying domain rules into
HTTP or protocol adapters.

## Decision

ChronoDesk keeps two independent, closed, unordered role types:

- `PlatformRole`: `platform_admin`, `security_auditor`,
  `emergency_operator`, `member`.
- `ProjectRole`: `project_admin`, `manager`, `agent`, `requester`, `observer`.

Each `/api/platform/*` governance operation uses an exact platform-role
allowlist; possessing any platform role does not authorize the whole surface.
Project roles come only from an active `ProjectMembership` and authorize
`/api/projects/{projectKey}/*`. `/api/workbench/*` derives its authorized set
from active Memberships, never from a platform role. Platform operations do not
create a fabricated `ProjectScope`, `ProjectAccess`, or `project_admin` role.

A Human JWT carries only a platform-role assertion. Authentication revalidates
the active user and current platform role on every request; a changed or
invalid assertion is rejected as `stale_token`. Project Membership and role are
resolved per project request. Service Principals remain governed by explicit
project grants and protocol-specific audiences.

The one-time database cutover follows the project-scope cutover and records
`20260730_platform_roles_v1_cutover` only after role columns, Membership
backfill, audit columns, and constraints pass in one transaction. Old role
sources, checkpoints, and values that cannot prove a safe mapping fail closed.

## Consequences

- A platform administrator may govern projects but does not automatically read
  or write their Tickets.
- Revoking a Membership takes effect on the next project request and removes
  that project from the workbench.
- API, UI, tests, audit records, and migration instructions must name the
  correct role type. New role values require a new ADR, migration, contract,
  and negative authorization coverage.
- Human Web OpenAPI documents the split at `/human-openapi.json`; it does not
  expand the Agent REST contract at `/openapi.yaml`.

## Verification

Contract and handler tests must prove that all platform roles lack implicit
project access, every project role requires an active Membership, a revoked
Membership is immediately rejected, and unknown/historical values fail closed.
Migration tests must verify the checkpoint, legacy mapping, retained existing
Memberships, and rollback on incomplete sources. Release evidence must include
the applicable Go, smoke, browser, and database/RLS checks rather than relying
on this ADR as a test result.
