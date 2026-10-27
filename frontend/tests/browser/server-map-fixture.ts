import type { Page, Route } from "@playwright/test"
import { host, snapshot, user } from "./host-fixture"

/**
 * One busy server as the map reads it: six projects and the containers they
 * run, their databases and volumes, the domains in front of them and the
 * certificates that cover those, backups, schedules, shells, drawings and the
 * people who can reach it — with a database that stopped answering, a
 * container that exited on its own, a failed backup and a firing alert, so the
 * states the map colours are all on it.
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

/** The server's own order: what is down first, then what needs a look, then by name. */
const RANK: Record<Tone, number> = { danger: 0, warning: 1, ok: 2, idle: 2, unknown: 2 }
const KEPT_ORDER = new Set(["drawings", "computers", "scheduled"])

function kind(key: string, items: ReturnType<typeof item>[], extra: Record<string, unknown> = {}) {
  const sorted = [...items].sort(
    (a, b) =>
      RANK[a.tone] - RANK[b.tone] || (KEPT_ORDER.has(key) ? 0 : a.name.localeCompare(b.name)),
  )
  return { key, total: items.length, items: sorted, state: "read", ...extra }
}

const projects = [
  item("project:1", "shop", "ok", "ready", "/deploy/1", {
    product: "nextjs",
    detail: "shop.example.com",
    projectId: 1,
  }),
  item("project:2", "api", "ok", "ready", "/deploy/2", {
    product: "fastapi",
    detail: "api.example.com",
    projectId: 2,
  }),
  item("project:3", "n8n", "ok", "ready", "/deploy/3", {
    product: "n8n",
    detail: "flows.example.com",
    projectId: 3,
  }),
  item("project:4", "blog", "warning", "deploying", "/deploy/4", {
    product: "astro",
    detail: "blog.example.com",
    projectId: 4,
  }),
  item("project:5", "grafana", "ok", "ready", "/deploy/5", {
    product: "grafana",
    detail: "metrics.example.com",
    projectId: 5,
  }),
  item("project:6", "docs", "idle", "stopped", "/deploy/6", {
    product: "vitepress",
    detail: "docs.example.com",
    projectId: 6,
  }),
]

const container = (id: string, name: string, image: string, tone: Tone, state: string) =>
  item(`container:${id}`, name, tone, state, `/docker/containers/${id}`, {
    product: image,
    detail: image,
  })

const containers = [
  container("c-shop-web", "shop-web-1", "node:22-alpine", "ok", "healthy"),
  container("c-shop-worker", "shop-worker-1", "node:22-alpine", "ok", "running"),
  container("c-api", "api-1", "python:3.13-slim", "ok", "healthy"),
  container("c-n8n", "n8n-1", "n8nio/n8n:1.92", "ok", "running"),
  container("c-blog", "blog-1", "nginx:1.27-alpine", "warning", "starting"),
  container("c-grafana", "grafana-1", "grafana/grafana:11.3", "ok", "running"),
  container("c-prom", "prometheus", "prom/prometheus:v3.0", "ok", "running"),
  container("c-pg", "postgres-main", "postgres:17", "ok", "healthy"),
  container("c-redis", "redis-cache", "redis:7.4", "ok", "running"),
  container("c-mongo", "mongo-events", "mongo:8.0", "danger", "exited (1)"),
  container("c-minio", "minio", "minio/minio:latest", "ok", "running"),
  container("c-kuma", "uptime-kuma", "louislam/uptime-kuma:1", "ok", "running"),
  container("c-vault", "vaultwarden", "vaultwarden/server:1.32", "ok", "running"),
  container("c-docs", "docs-1", "nginx:1.27-alpine", "idle", "exited (0)"),
]

const stacks = [
  item("stack:monitoring", "monitoring", "ok", "2 of 2 running", "/docker/stacks/monitoring", {
    product: "grafana",
  }),
  item("stack:data", "data", "warning", "2 of 3 running", "/docker/stacks/data", {
    product: "postgres",
  }),
  item("stack:tools", "tools", "ok", "3 of 3 running", "/docker/stacks/tools", {
    product: "minio",
  }),
]

const volumes = [
  item("volume:pg-data", "pg-data", "ok", "in use", "/docker/volumes?volume=pg-data", {
    product: "docker",
    detail: "4.2 GB",
  }),
  item("volume:redis-data", "redis-data", "ok", "in use", "/docker/volumes?volume=redis-data", {
    product: "docker",
    detail: "180 MB",
  }),
  item("volume:mongo-data", "mongo-data", "ok", "in use", "/docker/volumes?volume=mongo-data", {
    product: "docker",
    detail: "1.1 GB",
  }),
  item("volume:n8n-data", "n8n-data", "ok", "in use", "/docker/volumes?volume=n8n-data", {
    product: "docker",
    detail: "64 MB",
  }),
  item(
    "volume:grafana",
    "grafana-storage",
    "ok",
    "in use",
    "/docker/volumes?volume=grafana-storage",
    { product: "docker", detail: "22 MB" },
  ),
  item("volume:minio", "minio-data", "ok", "in use", "/docker/volumes?volume=minio-data", {
    product: "docker",
    detail: "38 GB",
  }),
  item("volume:old", "old-uploads", "idle", "unused", "/docker/volumes?volume=old-uploads", {
    product: "docker",
    detail: "2.4 GB",
  }),
]

const networks = [
  item("network:n-shop", "shop_default", "ok", "2 containers", "/docker/networks?network=n-shop", {
    product: "docker",
    detail: "172.20.0.0/16",
  }),
  item("network:n-data", "data_backend", "ok", "4 containers", "/docker/networks?network=n-data", {
    product: "docker",
    detail: "172.21.0.0/16",
  }),
  item("network:n-mon", "monitoring", "ok", "2 containers", "/docker/networks?network=n-mon", {
    product: "docker",
    detail: "172.22.0.0/16",
  }),
  item("network:n-bridge", "bridge", "ok", "3 containers", "/docker/networks?network=n-bridge", {
    product: "docker",
    detail: "172.17.0.0/16",
  }),
]

const images = [
  item("image:node", "node:22-alpine", "ok", "in use", "/docker/images?image=node", {
    product: "node",
    detail: "160 MB",
  }),
  item("image:pg", "postgres:17", "ok", "in use", "/docker/images?image=pg", {
    product: "postgres",
    detail: "435 MB",
  }),
  item("image:nginx", "nginx:1.27-alpine", "ok", "in use", "/docker/images?image=nginx", {
    product: "nginx",
    detail: "48 MB",
  }),
  item("image:n8n", "n8nio/n8n:1.92", "ok", "in use", "/docker/images?image=n8n", {
    product: "n8n",
    detail: "1.2 GB",
  }),
  item("image:old", "node:18", "idle", "unused", "/docker/images?image=old", {
    product: "node",
    detail: "1.1 GB",
  }),
]

const databases = [
  item("db:1", "shop", "ok", "answers", "/databases/1", {
    product: "postgres",
    detail: "postgres 17.2",
  }),
  item("db:2", "cache", "ok", "answers", "/databases/2", {
    product: "redis",
    detail: "redis 7.4.1",
  }),
  item("db:3", "events", "danger", "not answering", "/databases/3", {
    product: "mongodb",
    detail: "mongodb 8.0",
  }),
  item("db:4", "analytics", "ok", "answers", "/databases/4", {
    product: "postgres",
    detail: "postgres 17.2",
  }),
]

const domain = (name: string, project: string, tone: Tone = "ok", state = "https") =>
  item(`domain:${name}`, name, tone, state, `/proxy/sites/${name}`, {
    product: "nginx",
    detail: project,
  })

const domains = [
  domain("shop.example.com", "shop"),
  domain("www.shop.example.com", "shop"),
  domain("api.example.com", "api"),
  domain("flows.example.com", "n8n"),
  domain("blog.example.com", "blog"),
  domain("metrics.example.com", "grafana"),
  domain("docs.example.com", "docs", "idle", "disabled"),
  domain("status.example.com", "http://127.0.0.1:3001"),
  domain("vault.example.com", "http://127.0.0.1:8081"),
]

const cert = (name: string, days: number) =>
  item(
    `cert:/etc/letsencrypt/live/${name}`,
    name,
    days < 14 ? "warning" : "ok",
    `${days} days left`,
    `/proxy/certificates?cert=${name}`,
    { product: "lets-encrypt", detail: "Let's Encrypt" },
  )

const certificates = [
  cert("shop.example.com", 71),
  cert("api.example.com", 64),
  cert("flows.example.com", 9),
  cert("metrics.example.com", 52),
  cert("status.example.com", 33),
]

const streams = [
  item("stream:postgres", "postgres", "ok", "listening", "/proxy/streams?stream=postgres", {
    product: "nginx",
    detail: ":5433 → 127.0.0.1:5432",
  }),
]

const unit = (name: string, tone: Tone, state: string, detail: string) =>
  item(`unit:${name}.service`, name, tone, state, `/processes/services?unit=${name}.service`, {
    product: name,
    detail,
  })

const services = [
  unit("fail2ban", "danger", "failed", "Fail2Ban Service"),
  unit("nginx", "ok", "running", "A high performance web server"),
  unit("docker", "ok", "running", "Docker Application Container Engine"),
  unit("ssh", "ok", "running", "OpenBSD Secure Shell server"),
  unit("tailscaled", "ok", "running", "Tailscale node agent"),
  unit("cron", "ok", "running", "Regular background program processing daemon"),
  unit("systemd-journald", "ok", "running", "Journal Service"),
  unit("unattended-upgrades", "ok", "running", "Unattended Upgrades Shutdown"),
  unit("containerd", "ok", "running", "containerd container runtime"),
  unit("certbot", "idle", "inactive", "Certbot"),
]

const pm2 = [
  item("pm2:d1:0", "webhook-relay", "ok", "online", "/processes/pm2?app=d1:0", { product: "pm2" }),
  item("pm2:d1:1", "queue-worker", "ok", "online", "/processes/pm2?app=d1:1", {
    product: "pm2",
    detail: "2 restarts",
  }),
]

const dashboard = [
  item("dashboard:self", "Just Dashboard", "ok", "up to date", "/dashboard", { detail: "0.8.2" }),
]

const terminals = [
  item("terminal:w1", "deploy shop", "ok", "working", "/terminal?session=w1", {
    product: "terminal",
    detail: "root",
  }),
  item("terminal:w2", "claude", "ok", "claude", "/terminal?session=w2", {
    product: "claude",
    detail: "root · 2 windows",
  }),
  item("terminal:w3", "Shell", "idle", "idle", "/terminal?session=w3", {
    product: "terminal",
    detail: "root",
  }),
]

const drawings = [
  item("drawing:1", "Network plan", "idle", "edited 2h ago", "/excalidraw/1", {
    product: "excalidraw",
  }),
  item("drawing:2", "Shop architecture", "idle", "edited 1d ago", "/excalidraw/2", {
    product: "excalidraw",
  }),
  item("drawing:3", "Backup runbook", "idle", "edited 5d ago", "/excalidraw/3", {
    product: "excalidraw",
  }),
]

const repositories = [
  item("repo:/srv/shop", "shop", "warning", "3 changes", "/git?repo=/srv/shop", {
    product: "git",
    detail: "main",
  }),
  item("repo:/srv/api", "api", "ok", "clean", "/git?repo=/srv/api", {
    product: "git",
    detail: "main",
  }),
  item("repo:/srv/infra", "infra", "ok", "2 ahead", "/git?repo=/srv/infra", {
    product: "git",
    detail: "feature/backups",
  }),
]

const computers = [
  item("computer:a", "studio-mac", "idle", "opened 3h ago", "/remote-desktop/a", {
    product: "macos",
    detail: "100.88.1.4",
  }),
  item("computer:b", "office-pc", "idle", "never opened", "/remote-desktop/b", {
    product: "windows",
    detail: "100.88.1.9",
  }),
]

const backups = [
  item("backup:1", "nightly-databases", "ok", "ran 6h ago", "/backups/1", {
    product: "aws",
    detail: "next in 18h",
  }),
  item("backup:2", "volumes", "danger", "last run failed", "/backups/2", {
    product: "backblaze",
    detail: "next in 2h",
  }),
  item("backup:3", "configs", "ok", "ran 1d ago", "/backups/3", { detail: "next in 6d" }),
]

const scheduled = [
  item(
    "timer:certbot.timer",
    "certbot",
    "ok",
    "next in 9h",
    "/processes/scheduled?timer=certbot.timer",
    { product: "certbot", detail: "certbot.service" },
  ),
  item(
    "timer:apt-daily.timer",
    "apt-daily",
    "ok",
    "next in 3h",
    "/processes/scheduled?timer=apt-daily.timer",
    { detail: "apt-daily.service" },
  ),
  item(
    "timer:logrotate.timer",
    "logrotate",
    "ok",
    "next in 14h",
    "/processes/scheduled?timer=logrotate.timer",
    { detail: "logrotate.service" },
  ),
  item(
    "cron:/etc/cron.d/reports:3",
    "/usr/local/bin/send-reports",
    "ok",
    "0 7 * * 1",
    "/processes/scheduled",
    { product: "cron", detail: "/etc/cron.d/reports" },
  ),
]

const alerts = [
  item("alert:host:1", "Disk filling", "danger", "firing · 1", "/metrics/alerts", {
    detail: "host",
  }),
  item("alert:host:2", "Container down", "ok", "quiet", "/metrics/alerts", { detail: "host" }),
  item("alert:host:3", "CPU high", "ok", "quiet", "/metrics/alerts", { detail: "host" }),
  item("alert:proxy:1", "Certificate expiring", "ok", "quiet", "/proxy/alerts", {
    detail: "proxy",
  }),
]

const channels = [
  item("channel:1", "ops", "ok", "delivered", "/deploy/notifications", {
    product: "slack",
    detail: "slack",
  }),
  item("channel:2", "phone", "ok", "delivered", "/deploy/notifications", {
    product: "ntfy",
    detail: "ntfy",
  }),
]

const accounts = [
  item("account:1", "operator", "ok", "admin", "/account/users", { detail: "two-factor on" }),
  item("account:2", "maria", "ok", "limited", "/account/users", { detail: "two-factor on" }),
  item("account:3", "ci-viewer", "ok", "readonly", "/account/users", { detail: "no two-factor" }),
]

const keys = [
  item("key:1", "github-actions", "ok", "admin", "/account/keys", { detail: "used 4h ago" }),
  item("key:2", "claude-code", "ok", "mcp", "/account/keys", { detail: "used 12m ago" }),
  item("key:3", "prometheus", "ok", "metrics.read", "/account/keys", { detail: "used 15s ago" }),
]

const systemUsers = [
  item("sysuser:deploy", "deploy", "ok", "can sign in", "/system-users", { detail: "2 SSH keys" }),
  item("sysuser:maria", "maria", "ok", "can sign in", "/system-users", { detail: "1 SSH key" }),
]

const security = [
  item("firewall", "Firewall", "ok", "on", "/network/firewall", {
    product: "ufw",
    detail: "incoming deny",
  }),
]

const tunnels = [
  item("vpn:wireguard", "WireGuard", "ok", "2 of 3 peers online", "/network/vpn", {
    product: "wireguard",
    detail: "1 tunnel",
  }),
  item("vpn:tailscale", "Tailscale", "ok", "connected", "/network/vpn", {
    product: "tailscale",
    detail: "5 of 7 peers online",
  }),
]

/** What the inspector lists under a box, as the server sends it. */
const FACTS: Record<string, [string, string][]> = {
  "project:1": [
    ["Address", "shop.example.com"],
    ["Environment", "production"],
    ["Framework", "nextjs"],
    ["Services", "2"],
    ["Last run", "succeeded"],
  ],
  "project:2": [
    ["Address", "api.example.com"],
    ["Environment", "production"],
    ["Framework", "fastapi"],
    ["Last run", "succeeded"],
  ],
  "project:4": [
    ["Address", "blog.example.com"],
    ["Environment", "production"],
    ["Framework", "astro"],
    ["Last run", "running"],
  ],
  "container:c-mongo": [
    ["Image", "mongo:8.0"],
    ["Stack", "data"],
    ["Restarts", "3"],
  ],
  "container:c-pg": [
    ["Image", "postgres:17"],
    ["Stack", "data"],
    ["Published", "5432→5432"],
    ["Up for", "12d"],
  ],
  "container:c-api": [
    ["Image", "python:3.13-slim"],
    ["Published", "8000→8000"],
    ["Up for", "3d"],
  ],
  "container:c-shop-web": [
    ["Image", "node:22-alpine"],
    ["Published", "3000→3000"],
    ["Up for", "3d"],
  ],
  "db:1": [
    ["Engine", "postgres 17.2"],
    ["Database", "shop"],
    ["Runs in", "postgres-main"],
    ["Environment", "production"],
    ["Answered in", "3 ms"],
  ],
  "db:3": [
    ["Engine", "mongodb 8.0"],
    ["Database", "events"],
    ["Runs in", "mongo-events"],
    ["Environment", "production"],
  ],
  "db:4": [
    ["Engine", "postgres 17.2"],
    ["Database", "analytics"],
    ["Runs in", "postgres-main"],
    ["Answered in", "4 ms"],
  ],
  "volume:mongo-data": [
    ["Size", "1.1 GB"],
    ["Driver", "local"],
    ["Mount point", "/var/lib/docker/volumes/mongo-data/_data"],
  ],
  "volume:pg-data": [
    ["Size", "4.2 GB"],
    ["Driver", "local"],
  ],
  "stack:data": [
    ["Containers", "3"],
    ["Running", "2"],
  ],
  "domain:shop.example.com": [
    ["Site", "shop.conf"],
    ["Serves", "shop"],
    ["Proxy", "nginx"],
    ["HTTPS", "yes"],
  ],
  "domain:api.example.com": [
    ["Site", "api.conf"],
    ["Serves", "api"],
    ["Proxy", "nginx"],
    ["HTTPS", "yes"],
  ],
  "cert:/etc/letsencrypt/live/flows.example.com": [
    ["Covers", "flows.example.com"],
    ["Issuer", "Let's Encrypt"],
    ["Expires", "3 Nov 2026"],
  ],
  "backup:1": [
    ["Destination", "s3 · atlas-backups"],
    ["Schedule", "0 3 * * *"],
    ["Last run", "6h ago, success"],
    ["Next run", "in 18h"],
    ["Sources", "2"],
  ],
  "backup:2": [
    ["Destination", "b2 · atlas-volumes"],
    ["Schedule", "0 */6 * * *"],
    ["Last run", "4h ago, failed"],
    ["Next run", "in 2h"],
    ["Sources", "4"],
  ],
  "backup:3": [
    ["Destination", "local · /srv/backups"],
    ["Schedule", "weekly"],
    ["Last run", "1d ago, success"],
    ["Next run", "in 6d"],
  ],
}

function withFacts<T extends { id: string }>(list: T[]) {
  return list.map((one) =>
    FACTS[one.id]
      ? { ...one, facts: FACTS[one.id].map(([label, value]) => ({ label, value })) }
      : one,
  )
}

const link = (from: string, to: string, via: string, also?: string[]) =>
  also ? { from, to, via, also } : { from, to, via }

export const busyMap = {
  host: { hostname: host.hostname },
  checkedAt: at,
  kinds: [
    kind("projects", withFacts(projects)),
    kind("containers", withFacts(containers)),
    kind("stacks", withFacts(stacks)),
    kind("volumes", withFacts(volumes)),
    kind("networks", networks),
    kind("images", images),
    kind("databases", withFacts(databases)),
    kind("domains", withFacts(domains)),
    kind("certificates", withFacts(certificates)),
    kind("streams", streams),
    kind("services", services, { total: 212 }),
    kind("pm2", pm2),
    kind("dashboard", dashboard),
    kind("terminals", terminals, { facts: [{ label: "home", value: "/root" }] }),
    kind("drawings", drawings),
    kind("repositories", repositories),
    kind("computers", computers),
    kind("backups", withFacts(backups)),
    kind("scheduled", scheduled),
    kind("alerts", alerts),
    kind("channels", channels),
    kind("accounts", accounts),
    kind("keys", keys),
    kind("systemUsers", systemUsers),
    kind("security", security),
    kind("tunnels", tunnels),
  ],
  links: [
    link("domain:shop.example.com", "project:1", "route"),
    link("domain:www.shop.example.com", "project:1", "route"),
    link("domain:api.example.com", "project:2", "route"),
    link("domain:flows.example.com", "project:3", "route"),
    link("domain:blog.example.com", "project:4", "route"),
    link("domain:metrics.example.com", "project:5", "route"),
    link("domain:docs.example.com", "project:6", "route"),
    link("domain:status.example.com", "container:c-kuma", "route"),
    link("domain:vault.example.com", "container:c-vault", "route"),
    link("domain:shop.example.com", "cert:/etc/letsencrypt/live/shop.example.com", "certificate"),
    link("domain:api.example.com", "cert:/etc/letsencrypt/live/api.example.com", "certificate"),
    link("domain:flows.example.com", "cert:/etc/letsencrypt/live/flows.example.com", "certificate"),
    link(
      "domain:metrics.example.com",
      "cert:/etc/letsencrypt/live/metrics.example.com",
      "certificate",
    ),
    link(
      "domain:status.example.com",
      "cert:/etc/letsencrypt/live/status.example.com",
      "certificate",
    ),
    link("project:1", "container:c-shop-web", "release"),
    link("project:1", "container:c-shop-worker", "release"),
    link("project:2", "container:c-api", "release"),
    link("project:3", "container:c-n8n", "release"),
    link("project:4", "container:c-blog", "release"),
    link("project:5", "container:c-grafana", "release"),
    link("project:6", "container:c-docs", "release"),
    link("project:1", "db:1", "binding", ["reference", "env"]),
    link("project:1", "db:2", "binding"),
    link("project:2", "db:1", "binding"),
    link("project:2", "db:3", "binding"),
    link("project:5", "db:4", "binding"),
    link("container:c-pg", "db:1", "serves"),
    link("container:c-pg", "db:4", "serves"),
    link("container:c-redis", "db:2", "serves"),
    link("container:c-mongo", "db:3", "serves"),
    link("stack:monitoring", "container:c-grafana", "member"),
    link("stack:monitoring", "container:c-prom", "member"),
    link("stack:data", "container:c-pg", "member"),
    link("stack:data", "container:c-redis", "member"),
    link("stack:data", "container:c-mongo", "member"),
    link("stack:tools", "container:c-minio", "member"),
    link("stack:tools", "container:c-kuma", "member"),
    link("stack:tools", "container:c-vault", "member"),
    link("container:c-pg", "volume:pg-data", "mount"),
    link("container:c-redis", "volume:redis-data", "mount"),
    link("container:c-mongo", "volume:mongo-data", "mount"),
    link("container:c-n8n", "volume:n8n-data", "mount"),
    link("container:c-grafana", "volume:grafana", "mount"),
    link("container:c-minio", "volume:minio", "mount"),
    link("container:c-shop-web", "network:n-shop", "network"),
    link("container:c-shop-worker", "network:n-shop", "network"),
    link("container:c-pg", "network:n-data", "network"),
    link("container:c-shop-web", "image:node", "image"),
    link("container:c-pg", "image:pg", "image"),
    link("db:1", "backup:1", "backup"),
    link("db:4", "backup:1", "backup"),
    link("container:c-minio", "backup:2", "backup"),
    // Known otherwise than by a binding: n8n's DATABASE_URL names the
    // analytics server, the blog's variables refer to the shop database.
    link("project:3", "db:4", "env", ["session"]),
    link("project:4", "db:1", "reference"),
    link("project:1", "repo:/srv/shop", "source"),
    link("project:2", "repo:/srv/api", "source"),
    link("stream:postgres", "container:c-pg", "route"),
    link("stack:data", "volume:pg-data", "member"),
    link("volume:pg-data", "backup:2", "backup"),
    link("repo:/srv/infra", "backup:3", "backup"),
    link("pm2:d1:0", "repo:/srv/infra", "workdir"),
    link("pm2:d1:0", "sysuser:deploy", "user"),
    link("pm2:d1:1", "sysuser:deploy", "user"),
    link("cron:/etc/cron.d/reports:3", "sysuser:deploy", "user"),
    link("timer:certbot.timer", "unit:certbot.service", "starts"),
    link("alert:host:1", "channel:1", "notifies"),
    link("alert:host:1", "channel:2", "notifies"),
    link("alert:proxy:1", "channel:1", "notifies"),
    link("key:1", "account:1", "owner"),
    link("key:2", "account:1", "owner"),
    link("key:3", "account:2", "owner"),
    link("key:2", "db:1", "access"),
    link("key:2", "db:4", "access"),
  ],
}

/** A server the dashboard was just installed on: nothing deployed, nothing added. */
export const freshMap = {
  host: { hostname: host.hostname },
  checkedAt: at,
  kinds: [
    kind("projects", []),
    kind("containers", [
      container(
        "c-dash",
        "just-dashboard-backend",
        "ghcr.io/justdashboard/backend",
        "ok",
        "running",
      ),
    ]),
    kind("stacks", [
      item("stack:jd", "just-dashboard", "ok", "3 of 3 running", "/docker/stacks/just-dashboard"),
    ]),
    kind("volumes", []),
    kind("networks", []),
    kind("images", []),
    kind("databases", []),
    kind("domains", []),
    kind("certificates", [], { state: "unread", reason: "Still being read" }),
    kind("streams", []),
    kind("services", [unit("nginx", "ok", "running", "A high performance web server")]),
    kind("pm2", [], { state: "absent" }),
    kind("dashboard", dashboard),
    kind("terminals", []),
    kind("drawings", []),
    kind("repositories", []),
    kind("computers", []),
    kind("backups", []),
    kind("scheduled", []),
    kind("alerts", []),
    kind("channels", []),
    kind("accounts", [accounts[0]]),
    kind("keys", []),
    kind("systemUsers", []),
    kind("security", []),
    kind("tunnels", [], { state: "absent" }),
  ],
  links: [],
}

async function json(route: Route, body: unknown, status = 200) {
  await route.fulfill({ status, contentType: "application/json", body: JSON.stringify(body) })
}

/**
 * The map's API, answered for one page: the reading, the account's saved
 * arrangement (kept in memory so a reload brings it back), and the host and
 * metrics the server box reads. Returns what the page saved.
 */
export async function mockServerMap(
  page: Page,
  { map = busyMap, layout = null as unknown }: { map?: unknown; layout?: unknown } = {},
) {
  const saved = { layout, puts: 0, deletes: 0 }
  const created: string[] = []
  await page.routeWebSocket("**/api/v1/system/stream**", () => {})
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
    if (path === "/boards/" && request.method() === "POST") {
      created.push("drawing")
      return json(route, {
        id: 41,
        name: "Untitled drawing",
        revision: 1,
        createdAt: at,
        updatedAt: at,
      })
    }
    if (path === "/dashboard/update") return json(route, { current: "0.8.2", latest: "0.8.2" })
    return json(route, [])
  })
  return { saved, created }
}
