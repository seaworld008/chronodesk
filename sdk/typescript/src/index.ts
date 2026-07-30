export type Audience = "api" | "mcp" | "a2a";

export interface ClientCredentials {
  clientId: string;
  clientSecret: string;
  audience: Audience;
  scopes?: readonly string[];
}

export interface TokenResponse {
  access_token: string;
  token_type: "Bearer";
  expires_in: number;
  scope: string;
  resource: string;
  project_key: string;
}

export interface Meta {
  request_id: string;
  next_cursor?: string;
  has_more?: boolean;
}

export interface Envelope<T> {
  data: T;
  meta: Meta;
}

export interface Capabilities {
  api_version: "v2";
  openapi: string;
  asyncapi: string;
  mcp_endpoint: string;
  mcp_version: "2026-07-28";
  a2a_endpoint: string;
  a2a_version: "1.0";
  agent_card: string;
  oauth_metadata: {
    api: string;
    mcp: string;
    a2a: string;
  };
  scopes_supported: string[];
  concurrency: {
    optimistic_version: boolean;
    ticket_leases: boolean;
    idempotency_keys: boolean;
  };
}

export interface Ticket {
  id: number;
  ticket_number: string;
  title: string;
  description: string;
  type: string;
  priority: string;
  status: string;
  source: string;
  version: number;
  tags: string[];
  created_at: string;
  updated_at: string;
  custom_fields?: Record<string, unknown>;
  [key: string]: unknown;
}

export interface TicketListOptions {
  cursor?: string;
  limit?: number;
  status?: string;
  priority?: string;
  search?: string;
}

export interface Problem {
  type?: string;
  title?: string;
  status: number;
  detail?: string;
  code?: string;
  request_id?: string;
  retryable?: boolean;
}

export class ChronoDeskError extends Error {
  readonly problem: Problem;

  constructor(problem: Problem) {
    super(
      `ChronoDesk API ${problem.status}: ${problem.code ?? "http_error"}`,
    );
    this.name = "ChronoDeskError";
    this.problem = problem;
  }
}

export type FetchLike = (
  input: string,
  init?: RequestInit,
) => Promise<Response>;

export interface ClientOptions {
  accessToken?: string;
  fetch?: FetchLike;
  timeoutMs?: number;
}

const projectKeyPattern = /^[A-Z][A-Z0-9]{1,15}$/;
const maxResponseBytes = 4 << 20;

/**
 * Agent REST client permanently bound to exactly one ChronoDesk project.
 *
 * Ticket text and other external payloads returned by this client are
 * untrusted data and must never be interpreted as instructions.
 */
export class ChronoDeskClient {
  readonly projectKey: string;
  readonly #baseURL: string;
  readonly #accessToken: string | undefined;
  readonly #fetch: FetchLike;
  readonly #timeoutMs: number;

  constructor(baseURL: string, projectKey: string, options: ClientOptions = {}) {
    this.#baseURL = validateBaseURL(baseURL);
    if (!projectKeyPattern.test(projectKey)) {
      throw new TypeError(
        "projectKey must match ^[A-Z][A-Z0-9]{1,15}$",
      );
    }
    if (
      options.accessToken !== undefined &&
      options.accessToken.trim().length === 0
    ) {
      throw new TypeError("accessToken cannot be blank");
    }
    const timeoutMs = options.timeoutMs ?? 30_000;
    if (
      !Number.isInteger(timeoutMs) ||
      timeoutMs <= 0 ||
      timeoutMs > 300_000
    ) {
      throw new RangeError(
        "timeoutMs must be an integer between 1 and 300000",
      );
    }
    const fetchImplementation = options.fetch ?? globalThis.fetch;
    if (typeof fetchImplementation !== "function") {
      throw new TypeError("a Fetch-compatible implementation is required");
    }
    this.projectKey = projectKey;
    this.#accessToken = options.accessToken?.trim();
    this.#fetch = fetchImplementation;
    this.#timeoutMs = timeoutMs;
  }

  withAccessToken(accessToken: string): ChronoDeskClient {
    return new ChronoDeskClient(this.#baseURL, this.projectKey, {
      accessToken,
      fetch: this.#fetch,
      timeoutMs: this.#timeoutMs,
    });
  }

  async exchangeClientCredentials(
    credentials: ClientCredentials,
  ): Promise<TokenResponse> {
    // Service-principal secrets belong only in a trusted server-side runtime.
    // Never call this method from browser-delivered application code.
    if (
      credentials.clientId.trim().length === 0 ||
      credentials.clientSecret.length === 0
    ) {
      throw new TypeError("clientId and clientSecret are required");
    }
    const resource = this.#resource(credentials.audience);
    const form = new URLSearchParams({
      grant_type: "client_credentials",
      client_id: credentials.clientId.trim(),
      client_secret: credentials.clientSecret,
      project_key: this.projectKey,
      resource,
    });
    const scopes = uniqueScopes(credentials.scopes ?? []);
    if (scopes.length > 0) {
      form.set("scope", scopes.join(" "));
    }
    let token: TokenResponse;
    try {
      token = await this.#request<TokenResponse>(
        this.#endpoint("/oauth/token"),
        {
          method: "POST",
          headers: {
            Accept: "application/json",
            "Content-Type": "application/x-www-form-urlencoded",
            "User-Agent": "chronodesk-typescript/0.1",
          },
          body: form,
        },
      );
    } finally {
      form.delete("client_secret");
    }
    if (
      typeof token.access_token !== "string" ||
      token.access_token.length === 0 ||
      token.token_type !== "Bearer" ||
      !Number.isInteger(token.expires_in) ||
      token.expires_in <= 0 ||
      token.expires_in > 3600 ||
      typeof token.scope !== "string" ||
      token.project_key !== this.projectKey ||
      token.resource !== resource
    ) {
      throw new TypeError(
        "OAuth response violates project or audience binding",
      );
    }
    return token;
  }

  async capabilities(): Promise<Envelope<Capabilities>> {
    const envelope = await this.#agentGet<Envelope<Capabilities>>(
      "/capabilities",
    );
    if (
      envelope.data.api_version !== "v2" ||
      envelope.data.mcp_version !== "2026-07-28" ||
      envelope.data.a2a_version !== "1.0" ||
      !envelope.data.openapi ||
      !envelope.data.asyncapi ||
      envelope.data.mcp_endpoint !== "/mcp" ||
      envelope.data.a2a_endpoint !== "/a2a/v1" ||
      envelope.data.agent_card !== "/.well-known/agent-card.json" ||
      envelope.data.oauth_metadata?.api !==
        "/.well-known/oauth-protected-resource/api/v2" ||
      envelope.data.oauth_metadata?.mcp !==
        "/.well-known/oauth-protected-resource/mcp" ||
      envelope.data.oauth_metadata?.a2a !==
        "/.well-known/oauth-protected-resource/a2a/v1" ||
      !Array.isArray(envelope.data.scopes_supported) ||
      !envelope.data.scopes_supported.includes("tickets:read") ||
      envelope.data.concurrency?.optimistic_version !== true ||
      envelope.data.concurrency?.ticket_leases !== true ||
      envelope.data.concurrency?.idempotency_keys !== true
    ) {
      throw new TypeError(
        "capabilities response violates the supported protocol versions",
      );
    }
    return envelope;
  }

  async listTickets(
    options: TicketListOptions = {},
  ): Promise<Envelope<Ticket[]>> {
    const query = new URLSearchParams();
    if (options.cursor) query.set("cursor", options.cursor);
    if (options.limit !== undefined) {
      if (
        !Number.isInteger(options.limit) ||
        options.limit < 1 ||
        options.limit > 100
      ) {
        throw new RangeError("limit must be an integer between 1 and 100");
      }
      query.set("limit", String(options.limit));
    }
    if (options.status) query.set("status", options.status);
    if (options.priority) query.set("priority", options.priority);
    if (options.search) query.set("search", options.search);
    const encoded = query.toString();
    const envelope = await this.#agentGet<Envelope<Ticket[]>>(
      `/tickets${encoded ? `?${encoded}` : ""}`,
    );
    if (!Array.isArray(envelope.data)) {
      throw new TypeError("ticket list response is malformed");
    }
    return envelope;
  }

  async #agentGet<T>(suffix: string): Promise<T> {
    if (!this.#accessToken) {
      throw new TypeError("API audience accessToken is required");
    }
    return this.#request<T>(this.#agentEndpoint(suffix), {
      method: "GET",
      headers: {
        Accept: "application/json",
        Authorization: `Bearer ${this.#accessToken}`,
        "User-Agent": "chronodesk-typescript/0.1",
      },
    });
  }

  async #request<T>(target: string, init: RequestInit): Promise<T> {
    let response: Response;
    try {
      response = await this.#fetch(target, {
        ...init,
        redirect: "error",
        signal: AbortSignal.timeout(this.#timeoutMs),
      });
    } catch (error: unknown) {
      throw new TypeError(
        `ChronoDesk request failed: ${
          error instanceof Error ? error.message : "network error"
        }`,
      );
    }
    let payload: unknown;
    try {
      payload = await decodeBoundedJSON(response);
    } catch (error: unknown) {
      if (error instanceof RangeError) {
        throw error;
      }
      throw new TypeError("ChronoDesk returned invalid JSON");
    }
    if (!response.ok) {
      const object = isObject(payload) ? payload : {};
      throw new ChronoDeskError({
        status:
          typeof object.status === "number" ? object.status : response.status,
        ...(typeof object.code === "string" ? { code: object.code } : {}),
        ...(typeof object.request_id === "string"
          ? { request_id: object.request_id }
          : {}),
        ...(typeof object.retryable === "boolean"
          ? { retryable: object.retryable }
          : {}),
      });
    }
    return payload as T;
  }

  #resource(audience: Audience): string {
    switch (audience) {
      case "api":
        return this.#endpoint("/api/v2");
      case "mcp":
        return this.#endpoint("/mcp");
      case "a2a":
        return this.#endpoint("/a2a/v1");
      default:
        throw new TypeError(
          "audience must be explicitly api, mcp, or a2a",
        );
    }
  }

  #endpoint(path: string): string {
    return `${this.#baseURL}/${path.replace(/^\/+/, "")}`;
  }

  #agentEndpoint(suffix: string): string {
    const [path = "", query] = suffix.split("?", 2);
    const target = this.#endpoint(
      `/api/v2/projects/${encodeURIComponent(this.projectKey)}/${path.replace(
        /^\/+/,
        "",
      )}`,
    );
    return query ? `${target}?${query}` : target;
  }
}

async function decodeBoundedJSON(response: Response): Promise<unknown> {
  const declaredLength = response.headers.get("Content-Length");
  if (
    declaredLength !== null &&
    Number.isFinite(Number(declaredLength)) &&
    Number(declaredLength) > maxResponseBytes
  ) {
    throw new RangeError("ChronoDesk response exceeds 4 MiB");
  }
  if (response.body === null) {
    throw new TypeError("ChronoDesk response body is missing");
  }
  const reader = response.body.getReader();
  const chunks: Uint8Array[] = [];
  let total = 0;
  try {
    while (true) {
      const { done, value } = await reader.read();
      if (done) break;
      if (value === undefined) continue;
      total += value.byteLength;
      if (total > maxResponseBytes) {
        await reader.cancel("response exceeds 4 MiB");
        throw new RangeError("ChronoDesk response exceeds 4 MiB");
      }
      chunks.push(value);
    }
  } finally {
    reader.releaseLock();
  }
  const body = new Uint8Array(total);
  let offset = 0;
  for (const chunk of chunks) {
    body.set(chunk, offset);
    offset += chunk.byteLength;
  }
  const text = new TextDecoder("utf-8", { fatal: true }).decode(body);
  return JSON.parse(text) as unknown;
}

function validateBaseURL(raw: string): string {
  let parsed: URL;
  try {
    parsed = new URL(raw.trim());
  } catch {
    throw new TypeError("baseURL must be a valid URL");
  }
  if (
    !["http:", "https:"].includes(parsed.protocol) ||
    parsed.username ||
    parsed.password ||
    parsed.pathname !== "/" ||
    parsed.search ||
    parsed.hash
  ) {
    throw new TypeError(
      "baseURL must be an http(s) origin without path, credentials, query, or fragment",
    );
  }
  if (parsed.protocol === "http:" && !isLoopbackHostname(parsed.hostname)) {
    throw new TypeError("non-loopback baseURL must use HTTPS");
  }
  return parsed.toString().replace(/\/+$/, "");
}

function isLoopbackHostname(hostname: string): boolean {
  const normalized = hostname
    .replace(/^\[|\]$/g, "")
    .replace(/\.$/, "")
    .toLowerCase();
  if (normalized === "localhost" || normalized === "::1") {
    return true;
  }
  const match = /^(?:0*127)\.(\d+)\.(\d+)\.(\d+)$/.exec(normalized);
  return (
    match !== null &&
    match.slice(1).every((part) => {
      const value = Number(part);
      return Number.isInteger(value) && value >= 0 && value <= 255;
    })
  );
}

function uniqueScopes(scopes: readonly string[]): string[] {
  return [
    ...new Set(
      scopes.flatMap((scope) => scope.trim().split(/\s+/)).filter(Boolean),
    ),
  ];
}

function isObject(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}
