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
finalize, and emergency revoke. A durable dispatch marker must also distinguish
a claim that is still safe to revoke from one whose external side effect is
already uncertain.

## Decision

Each Webhook Outbox delivery uses an immutable
`WebhookDeliverySnapshot`. Its credential deadline is the Domain Event time
plus seven days and can never be extended. Ordinary edit, disable, and
soft-delete affect only future fan-out. Existing snapshots remain deliverable
until their original deadline. A soft-deleted `WebhookConfig` remains an
unscoped, project-predicated lifecycle lock anchor. Human `PUT` and `DELETE`
both require a strong `If-Match` value and compare-and-swap the durable admin
resource version in the same transaction as the mutation.

Emergency revoke is a separate Human command:

`POST /api/projects/{projectKey}/admin/agents/webhooks/{webhookID}/emergency-revoke`

It requires an active, exact `project_admin` Membership, `If-Match` against the
preflight `resource_version`, and `Idempotency-Key`. Platform role never grants
project authority. The durable version anchor advances in the same transaction
as every ordinary configuration update or soft-delete, so an ETag read before
either mutation cannot authorize a later emergency revoke. Exact-admin
preflight remains available for a live or soft-deleted tombstone and returns
only `config_id`, status, deletion state, emergency-revoked state, and
`resource_version`. This projection contains no URL or credential and does not
execute the command; the operator must make a separate irreversible
confirmation before the POST. A durable emergency-revoked marker is terminal,
so no later ordinary edit can resurrect the configuration.

The command runs in one project-scoped transaction with this stable lock order:

1. Project, Human identity, and Membership;
2. `WebhookConfig`;
3. `OutboxDelivery`, ordered by stable ID;
4. `WebhookDeliverySnapshot`, ordered by stable ID.

The transaction disables the mutable config and clears all four mutable
credential fields: `secret`, `previous_secret`,
`previous_secret_expires_at`, and `access_token`. It changes `pending`,
`failed`, and `dead` deliveries to `expired`, leaves `succeeded` and already
`expired` rows unchanged, and shreds every remaining snapshot credential with
reason `revoked`.

Webhook `processing` rows use `dispatch_started_at` as a three-state protocol:

- `NULL` means legacy or unknown. It is conservatively in flight and cannot be
  reclaimed automatically. An older worker may leave `NULL` on its first claim
  and may complete that claim, but a crash leaves the row for deadline cleanup.
- `dispatch_started_at == locked_at` means prepared for that exact claim
  generation. A new worker writes both timestamps together; the row can still
  be revoked or, when stale, reclaimed into a new prepared generation.
- `dispatch_started_at > locked_at` means dispatch authorization committed.
  The request may be external or in flight, but the marker does not prove that
  it left the process.

Immediately before calling the HTTP client's `Do`, a short transaction takes
the config lifecycle lock and compare-and-swaps the equality-bound prepared
marker to a timestamp strictly later than `locked_at`. That config lock is the
linearization barrier between dispatch start and emergency revoke. If revoke
commits first, the prepared delivery is expired and no HTTP call starts. If
dispatch start commits first, revoke reports the row as in flight and makes no
zero-HTTP claim. A worker crash that leaves a later marker without a terminal
transport result is not automatically reclaimed or resent; only deadline
cleanup closes it.

PostgreSQL and SQLite enforce the claim generation tuple at the database
boundary. A `processing → processing` ownership change is allowed only from
one prepared generation to another: `attempts` advances by exactly one,
`locked_at` moves forward, `locked_by` is non-empty, `lock_token` is a new
UUIDv7, and `dispatch_started_at` is rebound to the new `locked_at`. This
blocks an older binary from stale-reclaiming a `NULL` or dispatch-authorized
row, or from retaining or downgrading a marker across generations. An older
worker may still make a first claim from `pending` or `failed`, leave `NULL`,
and finish it; after a crash no worker may reclaim that unknown attempt.

An expiration transition never publishes its parent Domain Event. It also
never sets, clears, or otherwise changes the parent Domain Event's
`PublishedAt`: another destination may already have published the same event,
and that immutable history is preserved.

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
- An incident may still have a real or legacy/unknown in-flight request.
  Operators must use the reported count and the third-party provider's own
  controls when available; the count is not proof that the request left the
  process.
- Logical crypto-shredding prevents future application use but does not
  instantly erase old ciphertext from WAL, replicas, snapshots, backups, or
  storage media. Backup retention and key lifecycle remain separate controls.
- The nullable dispatch marker and database generation fence support a
  compatible rolling deployment. Old-worker `NULL` rows remain conservatively
  in flight and must finish or reach deadline; they must never be backfilled
  or rebound as prepared. The database rejects stale reclaim of `NULL` and
  dispatch-authorized rows even when an older application query would select
  them. A worker that lacks the shared config barrier and HTTP gates is not
  compatible with an exposed emergency-revoke endpoint.

## Verification

Service and PostgreSQL tests cover the status matrix, exact-project
authorization, cross-project non-disclosure, stable lock ordering, rollback,
idempotent repeat, the three dispatch-marker states, claim/replay races,
PostgreSQL/SQLite generation-fence drift and mutation cases, revoke-first zero
HTTP, start-first in-flight reporting, and preservation of both nil and
pre-existing `PublishedAt`. Handler and Human OpenAPI tests cover strong
`If-Match`, same-transaction CAS, tombstone preflight, idempotency, closed
responses, and secret-free events. Adapter tests use injectable or loopback
clients. Web tests cover the exact-admin UI, independent irreversible
confirmation, CAS headers, result counts, terminal Chinese projection, and
hidden replay for expired rows.
