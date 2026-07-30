import { ChronoDeskClient } from "../src/index.js";

const environment = globalThis as typeof globalThis & {
  process?: { env?: Record<string, string | undefined> };
};

const projectKey = requiredEnvironment("CHRONODESK_PROJECT_KEY");
const anonymous = new ChronoDeskClient(
  requiredEnvironment("CHRONODESK_URL"),
  projectKey,
);
const token = await anonymous.exchangeClientCredentials({
  clientId: requiredEnvironment("CHRONODESK_CLIENT_ID"),
  clientSecret: requiredEnvironment("CHRONODESK_CLIENT_SECRET"),
  audience: "api",
  scopes: ["tickets:read"],
});
const result = await anonymous
  .withAccessToken(token.access_token)
  .listTickets({ limit: 20 });
console.log(
  `project=${projectKey} tickets=${result.data.length} ` +
    `request_id=${result.meta.request_id}`,
);

function requiredEnvironment(name: string): string {
  const value = environment.process?.env?.[name];
  if (!value) {
    throw new Error(`required environment variable ${name} is not set`);
  }
  return value;
}
