# ADR-0005: Project is the runtime security and configuration boundary

- Status: Accepted
- Date: 2026-07-30

## Context

The original global Ticket pool mixed unrelated business lines. Adding only a
project filter to the UI would leave repositories, Workers, Agents, knowledge,
events, and integrations able to read or write an unintended Project.
Business Units are useful for governance but are too broad to own operational
authority.

## Decision

ChronoDesk uses this hierarchy:

`Organization → Business Unit → Project → Team/Queue → Ticket`

The Project is the only runtime boundary. Every project-owned record stores
`organization_id` and `project_id`; every command and query receives a trusted
`OperationContext` containing a server-authorized `ProjectScope` and
`ActorRef`.

Humans receive explicit `ProjectMembership` records. Service Principals receive
explicit `ProjectPrincipalGrant` records and one OAuth access token binds one
Project and one protocol audience. Team and Queue are routing constructs, never
Actors.

PostgreSQL `ENABLE` and `FORCE ROW LEVEL SECURITY` provide an independent
enforcement layer. The application uses a non-owner, `NOSUPERUSER`,
`NOBYPASSRLS` role and sets Project scope transaction-locally. Missing scope,
unknown role, unavailable policy state, and ambiguous project keys fail closed.

Cross-project collaboration creates a related Ticket in the destination
Project. It does not move the source Ticket or create a transaction spanning
two Project security boundaries.

## Consequences

- Project keys are authorization inputs, not trusted tenant identifiers.
- Business Unit reports explicitly aggregate an authorized Project set.
- Background work iterates Projects and opens one scoped transaction per
  Project; there is no unscoped project-data Worker.
- Every new project-owned table must join the RLS inventory and include
  negative cross-project tests.
- Platform administration and ordinary cross-project work remain separate,
  visible product surfaces.

## Verification

Service tests deny missing, inactive, ambiguous, and unauthorized Project
resolution. PostgreSQL integration tests prove that a least-privilege runtime
role sees no rows without transaction-local scope, sees only the selected
Project, rejects cross-project writes, and does not leak scope after commit.
Human, Agent REST, MCP, A2A, Connector, and Worker contract suites exercise the
same domain interfaces.
