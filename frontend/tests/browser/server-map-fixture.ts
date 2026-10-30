import type { Page, Route } from "@playwright/test"
import { host, snapshot, user } from "./host-fixture"

/**
 * One busy server as the map reads it, built to put every kind of system on
 * the canvas at once:
 *
 * - this dashboard: its Tailscale address, its three containers in their
 *   stack, its volume and its SQLite store, and the backup of that volume;
 * - a Next.js shop run as a stack of two containers, with a bound Postgres in
 *   its own container (a volume, a pooler, a nightly dump), a Redis cache, two
 *   domains and their certificate, and the checkout it is built from;
 * - an API and an n8n deployment sharing one Postgres a Compose stack runs,
 *   so the same database is drawn in four systems and marked shared;
 * - a blog mid-deploy, a stopped docs site and a billing service whose last
 *   deploy failed;
 * - a monitoring stack nobody deployed, reached on its own domain;
 * - a PM2 bot run from its checkout, and one that errored;
 * - a native Postgres under systemd, and a Gitea service a domain routes to;
 * - an Uptime Kuma and a Vaultwarden on their own, and a Metabase reading an
 *   external analytics database;
 * - and what nothing claims: an old domain with an expired certificate and a
 *   folder backup, beside an unused volume the map leaves out.
 *
 * With images, networks, accounts, keys, shells and drawings on the reading
 * too, as the server sends them, so what the map leaves out is tested.
 */

const at = new Date().toISOString()

type Tone = "ok" | "warning" | "danger" | "idle" | "unknown"

function item(
  id: string,
  name: string,
  tone: Tone,
  state: string,
  href: string,
  extra: Record<string, unknown> = {},
) {
  return { id, name, tone, state, href, ...extra }
}

const facts = (...pairs: string[]) => {
  const out: { label: string; value: string }[] = []
  for (let i = 0; i < pairs.length; i += 2)
    if (pairs[i + 1]) out.push({ label: pairs[i], value: pairs[i + 1] })
  return out
}

function kind(key: string, items: ReturnType<typeof item>[], extra: Record<string, unknown> = {}) {
  return { key, total: items.length, items, state: "read", ...extra }
}

const project = (
  id: number,
  name: string,
  product: string,
  tone: Tone,
  state: string,
  address: string,
  more: string[] = [],
) =>
  item(`project:${id}`, name, tone, state, `/deploy/${id}`, {
    product,
    projectId: id,
    detail: address,
    facts: facts("Address", address, "Environment", "production", ...more),
  })

const projects = [
  project(1, "shop", "nextjs", "ok", "ready", "shop.example.com", [
    "Framework",
    "Next.js",
    "Services",
    "2",
    "Last run",
    "succeeded",
  ]),
  project(2, "api", "fastapi", "ok", "ready", "api.example.com", [
    "Framework",
    "FastAPI",
    "Port",
    "8000",
  ]),
  project(3, "n8n", "n8n", "ok", "ready", "flows.example.com", ["Last run", "succeeded"]),
  project(4, "blog", "astro", "warning", "deploying", "blog.example.com", ["Framework", "Astro"]),
  project(5, "docs", "vitepress", "idle", "stopped", "docs.example.com", [
    "Framework",
    "VitePress",
  ]),
  project(6, "billing", "nestjs", "danger", "failed", "billing.example.com", [
    "Framework",
    "NestJS",
    "Last run",
    "failed",
  ]),
]

const container = (
  id: string,
  name: string,
  image: string,
  product: string,
  tone: Tone,
  state: string,
  more: string[] = [],
) =>
  item(`container:${id}`, name, tone, state, `/docker/containers/${id}`, {
    product,
    detail: image,
    facts: facts("Image", image, ...more),
  })

const containers = [
  container("c-jd-caddy", "just-dashboard-caddy-1", "caddy:2.10", "caddy", "ok", "running", [
    "Published",
    "8443→8443",
    "Up for",
    "6d",
  ]),
  container(
    "c-jd-frontend",
    "just-dashboard-frontend-1",
    "just-dashboard-frontend:0.8.2",
    "docker",
    "ok",
    "healthy",
    ["Up for", "6d"],
  ),
  container(
    "c-jd-backend",
    "just-dashboard-backend-1",
    "just-dashboard-backend:0.8.2",
    "docker",
    "ok",
    "healthy",
    ["Up for", "6d"],
  ),
  container("c-shop-web", "shop-web-1", "jd/shop:rel-41", "nodejs", "ok", "healthy", [
    "Stack",
    "jd-shop",
    "Published",
    "3001→3000",
    "Up for",
    "2d",
  ]),
  container("c-shop-worker", "shop-worker-1", "jd/shop:rel-41", "nodejs", "ok", "running", [
    "Stack",
    "jd-shop",
  ]),
  container("c-shop-pg", "jd-db-shop-postgres", "postgres:17", "postgres", "ok", "healthy", [
    "Up for",
    "12d",
  ]),
  container(
    "c-shop-bouncer",
    "jd-db-shop-pgbouncer",
    "edoburu/pgbouncer:1.23",
    "docker",
    "ok",
    "running",
  ),
  container("c-shop-redis", "shop-redis", "redis:7.4", "redis", "ok", "running"),
  container("c-api", "api-1", "jd/api:rel-12", "python", "ok", "healthy", [
    "Published",
    "8000→8000",
  ]),
  container("c-n8n", "n8n-1", "n8nio/n8n:1.92", "n8n", "ok", "running"),
  container("c-blog", "blog-1", "nginx:1.27-alpine", "nginx", "warning", "starting"),
  container("c-docs", "docs-1", "nginx:1.27-alpine", "nginx", "idle", "exited (0)"),
  container("c-pg-main", "postgres-main", "postgres:16", "postgres", "ok", "healthy", [
    "Stack",
    "data",
  ]),
  container("c-mongo", "mongo-events", "mongo:8.0", "mongodb", "danger", "exited (1)", [
    "Stack",
    "data",
  ]),
  container("c-grafana", "grafana", "grafana/grafana:11.3", "grafana", "ok", "running", [
    "Stack",
    "monitoring",
  ]),
  container("c-prom", "prometheus", "prom/prometheus:v3.0", "prometheus", "ok", "running", [
    "Stack",
    "monitoring",
  ]),
  container("c-kuma", "uptime-kuma", "louislam/uptime-kuma:1", "uptime-kuma", "ok", "running", [
    "Published",
    "3002→3001",
  ]),
  container("c-vault", "vaultwarden", "vaultwarden/server:1.32", "vaultwarden", "ok", "healthy"),
  container("c-metabase", "metabase", "metabase/metabase:v0.51", "metabase", "ok", "running"),
]

const stacks = [
  item(
    "stack:just-dashboard",
    "just-dashboard",
    "ok",
    "3 of 3 running",
    "/docker/stacks/just-dashboard",
    {
      facts: facts("Containers", "3", "Running", "3"),
    },
  ),
  item("stack:jd-shop", "jd-shop", "ok", "2 of 2 running", "/docker/stacks/jd-shop", {
    facts: facts("Containers", "2", "Running", "2"),
  }),
  item("stack:data", "data", "warning", "1 of 2 running", "/docker/stacks/data", {
    product: "postgres",
    facts: facts("Containers", "2", "Running", "1"),
  }),
  item("stack:monitoring", "monitoring", "ok", "2 of 2 running", "/docker/stacks/monitoring", {
    product: "grafana",
    facts: facts("Containers", "2", "Running", "2"),
  }),
]

const volume = (name: string, tone: Tone, state: string, size: string) =>
  item(`volume:${name}`, name, tone, state, `/docker/volumes?volume=${name}`, {
    detail: size,
    facts: facts("Size", size, "Driver", "local"),
  })

const volumes = [
  volume("just-dashboard_data", "ok", "in use", "84 MB"),
  volume("shop-pg-data", "ok", "in use", "4.2 GB"),
  volume("shop-redis-data", "ok", "in use", "180 MB"),
  volume("pg-main-data", "ok", "in use", "11.6 GB"),
  volume("mongo-events", "ok", "in use", "1.1 GB"),
  volume("n8n-data", "ok", "in use", "64 MB"),
  volume("grafana-storage", "ok", "in use", "22 MB"),
  volume("prom-data", "ok", "in use", "3.4 GB"),
  volume("kuma-data", "ok", "in use", "96 MB"),
  volume("vw-data", "ok", "in use", "18 MB"),
  volume("old-uploads", "idle", "unused", "2.4 GB"),
]

const database = (
  id: number,
  name: string,
  engine: string,
  version: string,
  tone: Tone,
  state: string,
  where: string,
  answered?: string,
) =>
  item(`db:${id}`, name, tone, state, `/databases/${id}`, {
    product: engine,
    detail: `${engine} ${version}`,
    facts: facts("Engine", `${engine} ${version}`, "Runs in", where, "Answered in", answered ?? ""),
  })

const databases = [
  database(1, "shop-db", "postgres", "17.2", "ok", "answers", "jd-db-shop-postgres", "2 ms"),
  database(2, "shop-cache", "redis", "7.4", "ok", "answers", "shop-redis", "1 ms"),
  database(3, "main-pg", "postgres", "16.4", "ok", "answers", "postgres-main", "3 ms"),
  database(4, "events", "mongodb", "8.0", "danger", "not answering", "mongo-events"),
  database(5, "legacy", "postgres", "14.13", "ok", "answers", "postgresql@14-main.service", "4 ms"),
  database(
    6,
    "analytics-prod",
    "postgres",
    "16.1",
    "ok",
    "answers",
    "db.analytics.internal:5432",
    "21 ms",
  ),
]

const domain = (name: string, tone: Tone, state: string, serves: string) =>
  item(`domain:${name}`, name, tone, state, `/proxy/sites/${name}`, {
    detail: serves,
    facts: facts("Serves", serves, "TLS", state === "https" ? "yes" : "no"),
  })

const domains = [
  domain("shop.example.com", "ok", "https", "shop"),
  domain("www.shop.example.com", "ok", "https", "shop"),
  domain("api.example.com", "ok", "https", "api"),
  domain("flows.example.com", "ok", "https", "n8n"),
  domain("blog.example.com", "warning", "maintenance", "blog"),
  domain("docs.example.com", "idle", "disabled", "docs"),
  domain("billing.example.com", "ok", "https", "billing"),
  domain("metrics.example.com", "ok", "https", "127.0.0.1:3000"),
  domain("bot.example.com", "ok", "https", "127.0.0.1:8090"),
  domain("git.example.com", "ok", "https", "127.0.0.1:3010"),
  domain("status.example.com", "ok", "https", "127.0.0.1:3002"),
  domain("vault.example.com", "ok", "https", "127.0.0.1:8081"),
  domain("bi.example.com", "ok", "https", "127.0.0.1:3005"),
  domain("old.example.com", "danger", "broken", "10.0.0.9:80"),
]

const cert = (id: string, name: string, tone: Tone, state: string) =>
  item(`cert:/etc/letsencrypt/live/${id}`, name, tone, state, `/proxy/certificates?cert=${id}`, {
    product: "lets-encrypt",
    detail: "Let's Encrypt R11",
    facts: facts("Covers", name, "Issuer", "Let's Encrypt R11"),
  })

const certificates = [
  cert("shop", "shop.example.com", "ok", "54 days left"),
  cert("api", "api.example.com", "warning", "9 days left"),
  cert("flows", "flows.example.com", "ok", "71 days left"),
  cert("wildcard", "*.example.com", "ok", "63 days left"),
  cert("old", "old.example.com", "danger", "expired"),
]

const repo = (path: string, name: string, tone: Tone, state: string, branch: string) =>
  item(`repo:${path}`, name, tone, state, `/git?repo=${encodeURIComponent(path)}`, {
    product: "git",
    detail: branch,
    facts: facts("Branch", branch, "Folder", path, "Remote", `git@github.com:acme/${name}.git`),
  })

const repositories = [
  repo("/srv/shop", "shop", "ok", "clean", "main"),
  repo("/srv/api", "api", "warning", "3 changes", "main"),
  repo("/srv/billing", "billing", "danger", "2 conflicts", "release/2.4"),
  repo("/srv/bots/telegram", "telegram-bot", "ok", "2 ahead", "main"),
  repo("/home/deploy/dotfiles", "dotfiles", "ok", "clean", "master"),
]

const backup = (
  id: number,
  name: string,
  tone: Tone,
  state: string,
  next: string,
  destination: string,
  product?: string,
) =>
  item(`backup:${id}`, name, tone, state, `/backups/${id}`, {
    product,
    detail: next,
    facts: facts(
      "Destination",
      destination,
      "Schedule",
      "0 3 * * *",
      "Next run",
      next.replace("next ", ""),
    ),
  })

const backups = [
  backup(1, "shop nightly", "ok", "ran 9 h ago", "next in 15 h", "s3 · acme-backups", "aws"),
  backup(
    2,
    "main-pg dump",
    "danger",
    "last run failed",
    "next in 3 h",
    "b2 · pg-dumps",
    "backblaze",
  ),
  backup(3, "dashboard data", "ok", "ran 2 h ago", "next in 22 h", "sftp · backup@nas:/jd"),
  backup(4, "legacy weekly", "ok", "ran 3 days ago", "next in 4 days", "local · /var/backups"),
  backup(5, "vault", "ok", "ran 40 min ago", "next in 5 h", "s3 · vault-bk", "aws"),
  backup(6, "etc configs", "idle", "paused", "", "local · /mnt/usb"),
]

const services = [
  item("unit:gitea.service", "gitea", "ok", "running", "/processes/services?unit=gitea.service", {
    product: "gitea",
    detail: "Gitea (Git with a cup of tea)",
  }),
  item(
    "unit:postgresql@14-main.service",
    "postgresql@14-main",
    "ok",
    "running",
    "/processes/services?unit=postgresql@14-main.service",
    {
      product: "postgresql",
      detail: "PostgreSQL Cluster 14-main",
    },
  ),
  item("unit:cron.service", "cron", "ok", "running", "/processes/services?unit=cron.service", {
    detail: "Regular background program processing daemon",
  }),
  item("unit:ssh.service", "ssh", "ok", "running", "/processes/services?unit=ssh.service", {
    detail: "OpenBSD Secure Shell server",
  }),
]

const pm2 = [
  item("pm2:d1:0", "telegram-bot", "ok", "online", "/processes/pm2?app=d1:0", {
    product: "pm2",
    facts: facts("Status", "online", "Memory", "92 MB"),
  }),
  item("pm2:d1:1", "scraper", "danger", "errored", "/processes/pm2?app=d1:1", {
    product: "pm2",
    detail: "14 restarts",
    facts: facts("Status", "errored", "Restarts", "14"),
  }),
]

const dashboard = [
  item("dashboard:self", "Just Dashboard", "ok", "up to date", "/dashboard", {
    product: "just-dashboard",
    detail: "0.8.2",
    facts: facts("Version", "0.8.2", "Newest", "0.8.2"),
  }),
  item(
    "dashboard:site",
    "vps-07749119.tailed39ba.ts.net",
    "ok",
    "https",
    "/dashboard/configuration",
    {
      detail: "Tailscale's, for the machine's name",
      facts: facts(
        "Address",
        "vps-07749119.tailed39ba.ts.net",
        "Certificate",
        "Tailscale's, for the machine's name",
      ),
    },
  ),
  item("dashboard:store", "vpsd.db", "ok", "in use", "/dashboard", {
    product: "sqlite",
    detail: "38.2 MB",
    facts: facts("Engine", "SQLite", "Size", "38.2 MB"),
  }),
]

const tunnels = [
  item("vpn:tailscale", "Tailscale", "ok", "connected", "/network/vpn", {
    product: "tailscale",
    detail: "4 of 6 peers online",
  }),
  item("vpn:wireguard", "WireGuard", "idle", "no tunnels", "/network/vpn", {
    product: "wireguard",
  }),
]

const streams = [
  item("stream:pg-main", "pg-main", "ok", "listening", "/proxy/streams?stream=pg-main", {
    product: "nginx",
    detail: ":5433 → 127.0.0.1:5432",
  }),
]

const link = (from: string, to: string, via: string, also?: string[]) => ({
  from,
  to,
  via,
  ...(also ? { also } : {}),
})

export const busyMap = {
  host: { hostname: host.hostname },
  checkedAt: at,
  kinds: [
    kind("projects", projects),
    kind("containers", containers),
    kind("stacks", stacks),
    kind("volumes", volumes),
    kind("networks", [
      item("network:n1", "jd-shop_default", "ok", "2 containers", "/docker/networks?network=n1"),
    ]),
    kind("images", [
      item("image:node", "node:22-alpine", "ok", "in use", "/docker/images?image=node"),
    ]),
    kind("databases", databases),
    kind("domains", domains),
    kind("certificates", certificates),
    kind("streams", streams),
    kind("services", services),
    kind("pm2", pm2),
    kind("dashboard", dashboard),
    kind("terminals", [item("terminal:t1", "deploy", "ok", "2 windows", "/terminal")]),
    kind("drawings", [
      item("drawing:1", "Network plan", "idle", "edited 2 days ago", "/excalidraw/1"),
    ]),
    kind("repositories", repositories),
    kind("computers", []),
    kind("backups", backups),
    kind("scheduled", []),
    kind("alerts", []),
    kind("channels", []),
    kind("accounts", [item("account:1", "admin", "ok", "admin", "/account/users")]),
    kind("keys", [item("key:1", "ci", "ok", "used today", "/account/keys")]),
    kind("systemUsers", []),
    kind("security", []),
    kind("tunnels", tunnels),
  ],
  links: [
    // This dashboard.
    link("dashboard:site", "dashboard:self", "route"),
    link("dashboard:site", "vpn:tailscale", "tailnet"),
    link("dashboard:self", "stack:just-dashboard", "self"),
    link("stack:just-dashboard", "container:c-jd-caddy", "member"),
    link("stack:just-dashboard", "container:c-jd-frontend", "member"),
    link("stack:just-dashboard", "container:c-jd-backend", "member"),
    link("stack:just-dashboard", "volume:just-dashboard_data", "member"),
    link("container:c-jd-backend", "volume:just-dashboard_data", "mount"),
    link("container:c-jd-backend", "dashboard:store", "mount"),
    link("volume:just-dashboard_data", "backup:3", "backup"),
    // The shop: a stack, a bound Postgres with a pooler and a dump, a cache.
    link("domain:shop.example.com", "project:1", "route"),
    link("domain:www.shop.example.com", "project:1", "route"),
    link("domain:shop.example.com", "cert:/etc/letsencrypt/live/shop", "certificate"),
    link("domain:www.shop.example.com", "cert:/etc/letsencrypt/live/shop", "certificate"),
    link("project:1", "stack:jd-shop", "release"),
    link("project:1", "container:c-shop-web", "release"),
    link("project:1", "container:c-shop-worker", "release"),
    link("stack:jd-shop", "container:c-shop-web", "member"),
    link("stack:jd-shop", "container:c-shop-worker", "member"),
    link("project:1", "db:1", "binding", ["reference", "env"]),
    link("project:1", "db:2", "binding"),
    link("container:c-shop-pg", "db:1", "serves"),
    link("container:c-shop-bouncer", "db:1", "pooler"),
    link("container:c-shop-redis", "db:2", "serves"),
    link("container:c-shop-pg", "volume:shop-pg-data", "mount"),
    link("container:c-shop-redis", "volume:shop-redis-data", "mount"),
    link("db:1", "backup:1", "backup"),
    link("project:1", "repo:/srv/shop", "source"),
    link("container:c-shop-web", "image:node", "image"),
    // The API and n8n share the data stack's Postgres.
    link("domain:api.example.com", "project:2", "route"),
    link("domain:api.example.com", "cert:/etc/letsencrypt/live/api", "certificate"),
    link("project:2", "container:c-api", "release"),
    link("project:2", "db:3", "binding"),
    link("project:2", "db:4", "env", ["session"]),
    link("project:2", "repo:/srv/api", "source"),
    link("domain:flows.example.com", "project:3", "route"),
    link("domain:flows.example.com", "cert:/etc/letsencrypt/live/flows", "certificate"),
    link("project:3", "container:c-n8n", "release"),
    link("project:3", "db:3", "reference"),
    link("container:c-n8n", "volume:n8n-data", "mount"),
    link("stack:data", "container:c-pg-main", "member"),
    link("stack:data", "container:c-mongo", "member"),
    link("container:c-pg-main", "db:3", "serves"),
    link("container:c-mongo", "db:4", "serves"),
    link("container:c-pg-main", "volume:pg-main-data", "mount"),
    link("container:c-mongo", "volume:mongo-events", "mount"),
    link("db:3", "backup:2", "backup"),
    link("stream:pg-main", "container:c-pg-main", "route"),
    // The blog, the docs and billing.
    link("domain:blog.example.com", "project:4", "route"),
    link("domain:blog.example.com", "cert:/etc/letsencrypt/live/wildcard", "certificate"),
    link("project:4", "container:c-blog", "release"),
    link("domain:docs.example.com", "project:5", "route"),
    link("project:5", "container:c-docs", "release"),
    link("domain:billing.example.com", "project:6", "route"),
    link("domain:billing.example.com", "cert:/etc/letsencrypt/live/wildcard", "certificate"),
    link("project:6", "repo:/srv/billing", "source"),
    link("project:6", "db:3", "reference"),
    // A monitoring stack nobody deployed.
    link("stack:monitoring", "container:c-grafana", "member"),
    link("stack:monitoring", "container:c-prom", "member"),
    link("stack:monitoring", "volume:grafana-storage", "member"),
    link("stack:monitoring", "volume:prom-data", "member"),
    link("container:c-grafana", "volume:grafana-storage", "mount"),
    link("container:c-prom", "volume:prom-data", "mount"),
    link("domain:metrics.example.com", "container:c-grafana", "route"),
    link("domain:metrics.example.com", "cert:/etc/letsencrypt/live/wildcard", "certificate"),
    // PM2.
    link("domain:bot.example.com", "pm2:d1:0", "route"),
    link("domain:bot.example.com", "cert:/etc/letsencrypt/live/wildcard", "certificate"),
    link("pm2:d1:0", "repo:/srv/bots/telegram", "workdir"),
    // Native services.
    link("unit:postgresql@14-main.service", "db:5", "serves"),
    link("db:5", "backup:4", "backup"),
    link("domain:git.example.com", "unit:gitea.service", "route"),
    link("domain:git.example.com", "cert:/etc/letsencrypt/live/wildcard", "certificate"),
    // Containers on their own.
    link("domain:status.example.com", "container:c-kuma", "route"),
    link("domain:status.example.com", "cert:/etc/letsencrypt/live/wildcard", "certificate"),
    link("container:c-kuma", "volume:kuma-data", "mount"),
    link("domain:vault.example.com", "container:c-vault", "route"),
    link("domain:vault.example.com", "cert:/etc/letsencrypt/live/wildcard", "certificate"),
    link("container:c-vault", "volume:vw-data", "mount"),
    link("container:c-vault", "backup:5", "backup"),
    link("volume:vw-data", "backup:5", "backup"),
    link("domain:bi.example.com", "container:c-metabase", "route"),
    link("domain:bi.example.com", "cert:/etc/letsencrypt/live/wildcard", "certificate"),
    link("container:c-metabase", "db:6", "env", ["session"]),
    // What nothing claims.
    link("domain:old.example.com", "cert:/etc/letsencrypt/live/old", "certificate"),
    // What the map leaves to other pages.
    link("key:1", "account:1", "owner"),
    link("terminal:t1", "account:1", "user"),
  ],
}

/** A server the dashboard was just installed on: nothing deployed, nothing added. */
export const freshMap = {
  host: { hostname: host.hostname },
  checkedAt: at,
  kinds: [
    kind("projects", []),
    kind("containers", []),
    kind("stacks", []),
    kind("volumes", []),
    kind("databases", []),
    kind("domains", []),
    kind("certificates", [], { state: "unread", reason: "Still being read" }),
    kind("pm2", [], { state: "absent" }),
    kind("tunnels", [], { state: "absent" }),
  ],
  links: [],
}

async function json(route: Route, body: unknown, status = 200) {
  await route.fulfill({ status, contentType: "application/json", body: JSON.stringify(body) })
}

/**
 * The map's API, answered for one page: the reading and the account's saved
 * arrangement (kept in memory so a reload brings it back). Returns what the
 * page saved.
 */
export async function mockServerMap(
  page: Page,
  { map = busyMap, layout = null as unknown }: { map?: unknown; layout?: unknown } = {},
) {
  const saved = { layout, puts: 0, deletes: 0 }
  await page.routeWebSocket("**/api/v1/**", () => {})
  await page.route("**/api/v1/**", async (route) => {
    const request = route.request()
    const path = new URL(request.url()).pathname.replace(/^\/api\/v1/, "")
    if (path === "/auth/session") return json(route, user)
    if (path === "/system/host") return json(route, host)
    if (path === "/system/metrics") return json(route, snapshot)
    if (path === "/map/") return json(route, map)
    if (path === "/map/layout") {
      if (request.method() === "PUT") {
        saved.puts++
        saved.layout = (request.postDataJSON() as { layout: unknown }).layout
        return json(route, { layout: saved.layout, updatedAt: new Date().toISOString() })
      }
      if (request.method() === "DELETE") {
        saved.deletes++
        saved.layout = null
        return route.fulfill({ status: 204 })
      }
      return json(route, { layout: saved.layout, updatedAt: saved.layout ? at : undefined })
    }
    if (path === "/dashboard/update") return json(route, { current: "0.8.2", latest: "0.8.2" })
    return json(route, [])
  })
  return { saved }
}
