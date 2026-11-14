import type { Page } from "@playwright/test"
import { json, mockProject, now, user } from "./deploy-fixture"

/**
 * Data for the four section pages beside Notifications (Log drains, Secret
 * stores, Templates, Shared variables), with something in every state a row
 * can be in: the shared fixture lists none of them, so every capture of these
 * pages used to be an empty one. The projects named here are the showcase
 * fleet's (`mockProject(page, { showcase: true })`).
 */

// The showcase runs on its own clock (`mockProject`), two and a half minutes
// after `now`, and every age here is measured back from it.
const ago = (seconds: number) => new Date(Date.parse(now) + 150_000 - seconds * 1000).toISOString()

const drain = {
  id: 1,
  name: "Grafana Cloud",
  kind: "otlp",
  endpoint: "https://otlp-gateway.example.test/otlp",
  enabled: true,
  streams: ["output", "requests", "traces"],
  projects: [] as number[],
  traceUrl: "https://grafana.example.test/explore?traceId={traceId}",
  headerNames: ["Authorization"],
  createdAt: now,
  updatedAt: now,
  status: {
    shipped: 184_220,
    dropped: 0,
    batches: 912,
    lastSuccessAt: ago(4),
    queued: 0,
  } as Record<string, unknown>,
}

/** Shipping, failing, paused and one that has sent nothing yet. */
export const sectionDrains = [
  drain,
  {
    ...drain,
    id: 2,
    name: "Local Loki",
    kind: "loki",
    endpoint: "http://127.0.0.1:3100",
    streams: ["requests"],
    projects: [7],
    traceUrl: undefined,
    headerNames: [],
    status: {
      shipped: 5_210,
      dropped: 0,
      batches: 77,
      lastSuccessAt: ago(900),
      lastErrorAt: ago(190),
      lastError: "the destination did not answer: connection refused",
      queued: 86,
    },
  },
  {
    ...drain,
    id: 3,
    name: "Vector",
    kind: "http",
    endpoint: "https://vector.example.test/ingest",
    enabled: false,
    streams: ["output"],
    traceUrl: undefined,
    status: { shipped: 1_204, dropped: 12, batches: 40, lastSuccessAt: ago(86_400 * 3), queued: 0 },
  },
  {
    ...drain,
    id: 4,
    name: "Tempo",
    endpoint: "http://127.0.0.1:4318",
    streams: ["traces"],
    projects: [7, 8],
    headerNames: [],
    status: { shipped: 0, dropped: 0, batches: 0, queued: 0 },
  },
]

const storeUse = (projectId: number, projectName: string, variable: string, store: string) => ({
  projectId,
  projectName,
  environmentId: projectId * 10,
  environmentName: "production",
  environmentKind: "production",
  variable,
  reference: `${store}/apps/${projectName}#${variable}`,
})

const store = {
  id: 1,
  name: "vault-prod",
  kind: "vault",
  address: "https://vault.example.test:8200",
  endpoint: "https://vault.example.test:8200",
  settings: { mount: "secret", auth: "token" } as Record<string, unknown>,
  credentialNames: ["token"],
  allowPreviews: false,
  createdBy: "operator",
  updatedBy: "operator",
  createdAt: now,
  updatedAt: now,
  test: { at: ago(3600), ok: true, message: "Signed in as token with default, web" } as
    { at: string; ok: boolean; message: string } | undefined,
  lastRead: { at: ago(240), ok: true, message: "Read apps/api-production#DATABASE_URL" } as
    { at: string; ok: boolean; message: string } | undefined,
  usedBy: [
    storeUse(7, "api-production", "DATABASE_URL", "vault-prod"),
    storeUse(7, "api-production", "REDIS_URL", "vault-prod"),
    storeUse(14, "billing-worker", "STRIPE_SECRET_KEY", "vault-prod"),
  ],
}

/** Answering, refused by a run's read, answering and unread, and never tested. */
export const sectionStores = [
  store,
  {
    ...store,
    id: 2,
    name: "bao-staging",
    kind: "openbao",
    address: "https://bao.example.test:8200",
    endpoint: "https://bao.example.test:8200",
    settings: { mount: "secret", auth: "approle", authMount: "approle" },
    credentialNames: ["roleId", "secretId"],
    test: { at: ago(1800), ok: true, message: "Signed in as approle with default, web" },
    lastRead: {
      at: ago(130),
      ok: false,
      message: "AppRole sign-in refused: invalid role or secret ID (400)",
    },
    usedBy: [storeUse(8, "automations", "N8N_ENCRYPTION_KEY", "bao-staging")],
  },
  {
    ...store,
    id: 3,
    name: "infisical",
    kind: "infisical",
    address: "https://app.infisical.com",
    endpoint: "https://app.infisical.com",
    settings: { project: "4f1c2b7e-0d1a-4c55-9b0e-2f7d4f1a9c11", environment: "prod" },
    credentialNames: ["clientId", "clientSecret"],
    allowPreviews: true,
    test: { at: ago(60), ok: true, message: "Signed in; 12 secrets at / in prod" },
    lastRead: undefined,
    usedBy: [],
  },
  {
    ...store,
    id: 4,
    name: "aws",
    kind: "aws",
    address: "",
    endpoint: "https://secretsmanager.eu-west-1.amazonaws.com",
    settings: { region: "eu-west-1" },
    credentialNames: ["accessKeyId", "secretAccessKey"],
    test: undefined,
    lastRead: undefined,
    usedBy: [],
  },
]

const summary = {
  id: "user-shop-front",
  version: "1.0.0",
  name: "Shop front",
  category: "http",
  profile: "web",
  description: "Shop front, the image nginx:1.27-alpine, as a template added on this dashboard.",
  iconId: "globe",
  docsUrl: "https://hub.docker.com/_/nginx",
  license: "Not stated",
  maintainer: "operator",
  reviewedAt: "2026-10-29",
  access: { kind: "open", note: "This template was added on this dashboard." },
  image: "nginx:1.27-alpine",
  memoryMb: 256,
  sourceKind: "blueprint",
  origin: "user",
  deploymentSupported: true as boolean,
  unavailableReason: undefined as string | undefined,
  services: undefined as unknown,
}

const template = {
  id: "user-shop-front",
  name: "Shop front",
  version: "1.0.0",
  source: "deployment",
  fromDeploymentId: 7 as number | undefined,
  summary,
  deployments: 2,
  createdBy: "operator",
  createdAt: ago(86_400 * 2),
  updatedBy: "operator",
  updatedAt: ago(86_400 * 2),
}

/** One image in use, a Compose stack of three nobody uses yet, and one this dashboard cannot deploy. */
export const sectionTemplates = [
  template,
  {
    ...template,
    id: "user-plausible",
    name: "Plausible",
    source: "compose",
    fromDeploymentId: undefined,
    deployments: 0,
    createdAt: ago(3600 * 5),
    summary: {
      ...summary,
      id: "user-plausible",
      name: "Plausible",
      image: "ghcr.io/plausible/community-edition:v2.1",
      memoryMb: 2048,
      sourceKind: "compose",
      services: [
        {
          name: "plausible",
          role: "app",
          purpose: "Analytics",
          image: "ghcr.io/plausible/community-edition:v2.1",
          memoryMb: 1024,
          public: true,
        },
        {
          name: "db",
          role: "database",
          purpose: "Accounts",
          image: "postgres:16-alpine",
          memoryMb: 512,
        },
        {
          name: "events",
          role: "database",
          purpose: "Events",
          image: "clickhouse/clickhouse-server:24.3-alpine",
          memoryMb: 512,
        },
      ],
    },
  },
  {
    ...template,
    id: "user-valkey",
    name: "Valkey",
    source: "import",
    fromDeploymentId: undefined,
    deployments: 0,
    createdBy: "alex",
    createdAt: ago(86_400 * 9),
    summary: {
      ...summary,
      id: "user-valkey",
      name: "Valkey",
      image: "valkey/valkey:8-alpine",
      deploymentSupported: false,
      unavailableReason: "This template needs dashboard 0.9.0 or newer.",
    },
  },
]

const reader = (projectId: number, projectName: string, variable: string, waiting = false) => ({
  projectId,
  projectName,
  environmentId: projectId * 10,
  environmentName: "production",
  environmentKind: "production",
  variable,
  waiting,
})

const variable = (
  name: string,
  sensitivity: "plain" | "secret",
  usedBy: ReturnType<typeof reader>[] = [],
) => ({
  scope: "server",
  name,
  sensitivity,
  masked: sensitivity === "secret" ? "••••••••" : "configured",
  valueDigest: `sha256:${"5".repeat(64)}`,
  reference: `\${{server.${name}}}`,
  updatedBy: "operator",
  createdAt: now,
  updatedAt: now,
  usedBy,
})

/** Read by two projects, read with a reader not live yet (twice), and unread. */
export const sectionVariables = [
  variable("SENTRY_DSN", "secret", [
    reader(7, "api-production", "SENTRY_DSN"),
    reader(8, "automations", "SENTRY_DSN"),
  ]),
  variable("STRIPE_SECRET_KEY", "secret", [reader(7, "api-production", "STRIPE_SECRET_KEY", true)]),
  variable("MAILGUN_API_KEY", "secret", [reader(14, "billing-worker", "MAILGUN_KEY", true)]),
  variable("AWS_REGION", "plain", [reader(10, "status-page", "REGION")]),
  variable("PUBLIC_API_URL", "plain"),
]

export type SectionData = {
  drains?: unknown[]
  stores?: unknown[]
  templates?: unknown[]
  variables?: unknown[]
  /** Sign in as an account that can read and nothing else. */
  readOnly?: boolean
  /** Holds the four lists back until the returned `release` is called. */
  hold?: boolean
}

/**
 * The showcase fleet with the four lists over it. Writes are recorded and
 * answered with the list's first item; a store's Test answers that it signed
 * in.
 */
export async function mockSections(page: Page, data: SectionData = {}) {
  await mockProject(page, { showcase: true })
  if (data.readOnly) {
    await page.route("**/api/v1/auth/session", (route) =>
      json(route, {
        ...user,
        capabilities: ["read"],
        user: { ...user.user, role: "viewer" },
      }),
    )
  }
  let open: () => void = () => {}
  const gate = data.hold ? new Promise<void>((resolve) => (open = resolve)) : Promise.resolve()
  const writes: { method: string; path: string; body: unknown }[] = []
  const lists: [string, unknown[]][] = [
    ["**/api/v1/deploy/log-drains**", data.drains ?? sectionDrains],
    ["**/api/v1/deploy/secret-stores**", data.stores ?? sectionStores],
    ["**/api/v1/deploy/templates**", data.templates ?? sectionTemplates],
    ["**/api/v1/deploy/shared-variables**", data.variables ?? sectionVariables],
  ]
  for (const [pattern, list] of lists) {
    await page.route(pattern, async (route) => {
      const request = route.request()
      const path = new URL(request.url()).pathname
      if (request.method() === "GET") {
        if (path.endsWith("/export")) return json(route, { id: "user-shop-front" })
        if (path.endsWith("/reveal")) return json(route, { name: "X", value: "revealed-value" })
        await gate
        return json(route, list)
      }
      writes.push({ method: request.method(), path, body: request.postDataJSON() })
      if (path.endsWith("/test")) return json(route, { ok: true, message: "Signed in" })
      if (request.method() === "DELETE") return route.fulfill({ status: 204 })
      return json(route, list[0] ?? {})
    })
  }
  return { writes, release: () => open() }
}
