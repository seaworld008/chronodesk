# ADR-0009: Finite Webhook delivery credentials and explicit emergency revoke

- Status: Accepted
- Date: 2026-08-11

## Context

Webhook delivery must survive an ordinary subscription edit, disable, or
deletion without silently changing the already committed destination or
credential. Conversely, an incident responder needs one explicit operation
that prevents every not-yet-started delivery from using a compromised
credential. Treating ordinary deletion as revocation would make committed work
non-deterministic; treating revocation as another configuration edit would
leave frozen credentials usable.

Delivery snapshots therefore need a finite lifetime, a monotonic terminal
state, and a serialization point shared by claim, replay, HTTP gates, cleanup,
finalize, and emergency revoke.

## Decision

Each Webhook Outbox delivery uses an immutable
`WebhookDeliverySnapshot`. Its credential deadline is the Domain Event time
plus seven days and can never be extended. Ordinary edit, disable, and
soft-delete affect only future fan-out. Existing snapshots remain deliverable
until their original deadline. A soft-deleted `WebhookConfig` remains an
unscoped, project-predicated lifecycle lock anchor.

Emergency revoke is a separate Human command:

`POST /api/projects/{projectKey}/admin/agents/webhooks/{webhookID}/emergency-revoke`

It requires an active, exact `project_admin` Membership, `If-Match` against the
preflight `resource_version`, and `Idempotency-Key`. Platform role never grants
project authority. The durable version anchor advances in the same transaction
as every ordinary configuration update or soft-delete, so an ETag read before
either mutation cannot authorize a later emergency revoke.

The command runs in one project-scoped transaction with this stable lock order:

1. Project, Human identity, and Membership;
2. `WebhookConfig`;
3. `OutboxDelivery`, ordered by stable ID;
4. `WebhookDeliverySnapshot`, ordered by stable ID.

The transaction disables the mutable config, changes `pending`, `failed`, and
`dead` deliveries to `expired`, leaves `succeeded` and already `expired` rows
unchanged, and reports `processing` rows as in flight because an HTTP request
that has left the process cannot be recalled. Every snapshot credential that
has not already been shredded is cleared with reason `revoked`.

An expiration transition never publishes its parent Domain Event. It also
never clears an existing `PublishedAt`: another destination may already have
published the same event, and that immutable history is preserved.

Claim, replay, both HTTP gates, finalize, and cleanup acquire the same
project/config barrier before delivery and snapshot locks. A revoked snapshot
therefore cannot reach HTTP after the revoke transaction commits. Events,
audit records, receipts, responses, and logs contain only identifiers, counts,
statuses, versions, and the closed shred reason; they never contain a Webhook
URL, credential, envelope, request body, or response body.

## Consequences

- Ordinary configuration lifecycle remains deterministic for committed work.
- Emergency revoke is visible, irreversible, and separately authorized.
- Replay is finite; terminal rows cannot be resurrected and credential
  deadlines cannot move forward.
- An incident may still have an in-flight request. Operators must use the
  reported count and the third-party provider's own controls when available.
- Logical crypto-shredding prevents future application use but does not
  instantly erase old ciphertext from WAL, replicas, snapshots, backups, or
  storage media. Backup retention and key lifecycle remain separate controls.
- A rolling deployment must not expose emergency revoke while any old worker
  lacks the shared config barrier and both HTTP gates.

## Verification

Service and PostgreSQL tests cover the status matrix, exact-project
authorization, cross-project non-disclosure, stable lock ordering, rollback,
idempotent repeat, claim/replay races, in-flight reporting, and preservation of
both nil and pre-existing `PublishedAt`. Handler and Human OpenAPI tests cover
preflight version, `If-Match`, idempotency, closed responses, and secret-free
events. Adapter tests use injectable or loopback clients and prove zero HTTP
after revoke. Web tests cover the exact-admin UI, irreversible confirmation,
CAS headers, result counts, terminal Chinese projection, and hidden replay for
expired rows.
