import {
  ChronoDeskClient,
  type FetchLike,
} from "../src/index.js";

interface CapturedRequest {
  url: string;
  init?: RequestInit;
}

const calls: CapturedRequest[] = [];
const fetchMock: FetchLike = async (
  input: string,
  init?: RequestInit,
): Promise<Response> => {
  assert(init?.redirect === "error", "requests must reject redirects");
  assert(init.signal instanceof AbortSignal, "requests require a timeout signal");
  calls.push(init === undefined ? { url: input } : { url: input, init });
  const parsed = new URL(input);
  if (parsed.pathname === "/oauth/token") {
    const body = new URLSearchParams(String(init?.body));
    assert(body.get("project_key") === "OPS", "OAuth project_key mismatch");
    assert(
      body.get("resource") === "https://desk.example/api/v2",
      "OAuth resource mismatch",
    );
    return jsonResponse({
      access_token: "api-token",
      token_type: "Bearer",
      expires_in: 600,
      scope: "tickets:read",
      resource: "https://desk.example/api/v2",
      project_key: "OPS",
    });
  }
  assert(
    new Headers(init?.headers).get("Authorization") === "Bearer api-token",
    "Bearer token missing",
  );
  if (parsed.pathname === "/api/v2/projects/OPS/capabilities") {
    return jsonResponse({
      data: {
        api_version: "v2",
        openapi: "/openapi.yaml",
        asyncapi: "/asyncapi.yaml",
        mcp_endpoint: "/mcp",
        mcp_version: "2026-07-28",
        a2a_endpoint: "/a2a/v1",
        a2a_version: "1.0",
        agent_card: "/.well-known/agent-card.json",
        oauth_metadata: {
          api: "/.well-known/oauth-protected-resource/api/v2",
          mcp: "/.well-known/oauth-protected-resource/mcp",
          a2a: "/.well-known/oauth-protected-resource/a2a/v1",
        },
        scopes_supported: ["tickets:read"],
        concurrency: {
          optimistic_version: true,
          ticket_leases: true,
          idempotency_keys: true,
        },
      },
      meta: { request_id: "r1" },
    });
  }
  assert(
    parsed.pathname === "/api/v2/projects/OPS/tickets",
    `unexpected path ${parsed.pathname}`,
  );
  assert(parsed.searchParams.get("limit") === "10", "ticket limit mismatch");
  return jsonResponse({
    data: [
      {
        id: 42,
        ticket_number: "OPS-42",
        title: "untrusted",
        description: "untrusted",
        type: "incident",
        priority: "high",
        status: "open",
        source: "api",
        version: 1,
        tags: [],
        created_at: "2026-07-30T00:00:00Z",
        updated_at: "2026-07-30T00:00:00Z",
      },
    ],
    meta: { request_id: "r2" },
  });
};

const anonymous = new ChronoDeskClient("https://desk.example", "OPS", {
  fetch: fetchMock,
});
const token = await anonymous.exchangeClientCredentials({
  clientId: "client",
  clientSecret: "secret-value",
  audience: "api",
  scopes: ["tickets:read"],
});
assert(token.project_key === "OPS", "token project mismatch");
assert(
  !String(calls[0]?.init?.body).includes("secret-value"),
  "client secret must be removed from the retained form after exchange",
);

const authenticated = anonymous.withAccessToken(token.access_token);
const capabilities = await authenticated.capabilities();
assert(capabilities.data.api_version === "v2", "API version mismatch");
const tickets = await authenticated.listTickets({ limit: 10 });
assert(tickets.data[0]?.ticket_number === "OPS-42", "ticket mismatch");

let missingProjectRejected = false;
try {
  new ChronoDeskClient("https://desk.example", "", { fetch: fetchMock });
} catch {
  missingProjectRejected = true;
}
assert(missingProjectRejected, "empty project must be rejected");

let cleartextRemoteRejected = false;
try {
  new ChronoDeskClient("http://desk.example", "OPS", { fetch: fetchMock });
} catch {
  cleartextRemoteRejected = true;
}
assert(cleartextRemoteRejected, "cleartext remote base URL must be rejected");
let basePathRejected = false;
try {
  new ChronoDeskClient("https://desk.example/base", "OPS", {
    fetch: fetchMock,
  });
} catch {
  basePathRejected = true;
}
assert(basePathRejected, "base URL path must be rejected");
let invalidTimeoutRejected = false;
try {
  new ChronoDeskClient("https://desk.example", "OPS", {
    fetch: fetchMock,
    timeoutMs: 0,
  });
} catch {
  invalidTimeoutRejected = true;
}
assert(invalidTimeoutRejected, "unbounded timeout must be rejected");
new ChronoDeskClient("http://127.0.0.1:8081", "OPS", { fetch: fetchMock });
new ChronoDeskClient("http://[::1]:8081", "OPS", { fetch: fetchMock });

let missingAudienceRejected = false;
try {
  await anonymous.exchangeClientCredentials({
    clientId: "client",
    clientSecret: "secret",
    audience: "" as "api",
  });
} catch {
  missingAudienceRejected = true;
}
assert(missingAudienceRejected, "empty audience must be rejected");

function jsonResponse(payload: unknown): Response {
  return new Response(JSON.stringify(payload), {
    status: 200,
    headers: { "Content-Type": "application/json" },
  });
}

function assert(condition: unknown, message: string): asserts condition {
  if (!condition) {
    throw new Error(message);
  }
}
