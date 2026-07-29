# ADR-0001: Current-only Agent protocols

- Status: Accepted
- Date: 2026-07-29

## Context

Compatibility branches multiply discovery, authentication, schema, error, and
test paths. ChronoDesk is an Agent-native platform under active development, not
a legacy protocol gateway.

## Decision

ChronoDesk implements exactly:

- MCP `2026-07-28` over stateless Streamable HTTP;
- A2A official release `v1.0.1` with wire version `1.0`;
- OpenAPI `3.2.0`;
- CloudEvents `1.0`.

The server rejects older protocol versions and does not expose legacy MCP
session/SSE endpoints, method aliases, or downgraded schemas.

## Consequences

- Clients must upgrade before connecting.
- Protocol code and tests have one behavior path.
- A future version upgrade removes the previous implementation in the same
  release unless a new ADR explicitly changes this policy.

## Verification

Protocol contract tests assert accepted/rejected version headers, discovery
documents, method names, and absence of compatibility routes.
