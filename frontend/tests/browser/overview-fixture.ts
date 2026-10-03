import type { Page } from "@playwright/test"
import { iso, json, mockHost, now, processes as hostProcesses } from "./host-fixture"
import { now as fleetNow, showcaseFleet } from "./deploy-fixture"

/**
 * The Overview of a server with something in every place: the showcase fleet
 * with a failing project in it, a dozen containers, saved databases,
 * certificates (one past its renewal), backups, Git checkouts, pending
 * security updates and a day of activity — on top of the Metrics page's
 * mocked host.
 */

// The fleet fixture is pinned to its own day; move it to this one so every
// "ago" on the page reads as minutes and hours rather than a month.
const shift = Date.now() - Date.parse(fleetNow)
export const fleet = JSON.parse(
  JSON.stringify(showcaseFleet).replace(/"(\d{4}-\d\d-\d\dT[\d:.]+Z)"/g, (_, at: string) =>
    JSON.stringify(new Date(Date.parse(at) + shift).toISOString()),
  ),
)

const containers = [
  ["shop-web", "registry.gitlab.com/acme/shop:2.4.0", "running"],
  ["shop-db", "postgres:16-alpine", "running"],
  ["shop-cache", "redis:7-alpine", "running"],
  ["automations", "docker.n8n.io/n8nio/n8n:1.2.3", "running"],
  ["status-page", "louislam/uptime-kuma:1.23.16", "running"],
  ["grafana", "grafana/grafana:11.2.0", "running"],
  ["vaultwarden", "vaultwarden/server:1.32.0", "running"],
  ["minio", "minio/minio:RELEASE.2026-08-01", "running"],
  ["billing-worker", "ghcr.io/acme/billing:3.1", "running"],
  ["survival", "itzg/minecraft-server:2026.9.1-java21", "exited"],
  ["old-meili", "getmeili/meilisearch:v1.9", "exited"],
].map(([name, image, state], i) => ({
  id: `c${i}`.padEnd(64, "0"),
  names: [name],
  name,
  image,
  imageId: "",
  command: "",
  state,
  status: state === "running" ? "Up 3 days" : "Exited (0) 2 hours ago",
  createdAt: iso(now - 86_400_000 * 3),
  uptimeSeconds: state === "running" ? 86_400 * 3 : 0,
  ports: [],
  labels: {},
  networks: ["bridge"],
  exposure: [],
  hasHealthcheck: false,
}))

const databases = [
  [1, "shop", "postgres", "5432"],
  [2, "blog", "mysql", "3306"],
  [3, "cache", "redis", "6379"],
  [4, "events", "mongodb", "27017"],
].map(([id, name, driver, port]) => ({
  id,
  name,
  driver,
  host: "127.0.0.1",
  port,
  user: "app",
  database: name,
  createdAt: iso(now - 86_400_000 * 30),
  environment: "production",
  readOnly: false,
  notes: "",
  origin: "",
}))

const certificates = [
  ["shop.example.test", 61],
  ["status.example.test", 12],
  ["docs.example.test", 74],
].map(([domain, daysLeft]) => ({
  name: domain,
  path: `/etc/letsencrypt/live/${domain}/fullchain.pem`,
  domains: [domain],
  issuer: "Let's Encrypt R11",
  notBefore: iso(now - 86_400_000 * 20),
  notAfter: iso(now + 86_400_000 * Number(daysLeft)),
  daysLeft,
  expired: false,
  expiring: Number(daysLeft) < 14,
  selfSigned: false,
  source: "letsencrypt",
  usedBy: [domain],
}))

const backupRun = (jobId: number, hoursAgo: number, status = "success") => ({
  id: jobId * 10,
  jobId,
  startedAt: iso(now - hoursAgo * 3_600_000),
  endedAt: iso(now - hoursAgo * 3_600_000 + 90_000),
  status,
  artifact: "",
  sizeBytes: 1024 ** 3,
  log: "",
  trigger: "schedule",
})

const backups = [
  [1, "Databases nightly", 7],
  [2, "App data", 3],
].map(([id, name, hoursAgo]) => ({
  id,
  name,
  sources: ["/srv"],
  excludes: [],
  targetKind: "s3",
  target: { bucket: "atlas-backups" },
  schedule: "0 3 * * *",
  retention: 7,
  retentionDays: 0,
  enabled: true,
  createdAt: iso(now - 86_400_000 * 40),
  hasCredentials: true,
  lastRun: backupRun(Number(id), Number(hoursAgo)),
  nextRun: iso(now + 86_400_000 - Number(hoursAgo) * 3_600_000),
  overdue: false,
  stored: { runs: 7, bytes: 7 * 1024 ** 3 },
}))

const traffic = Object.fromEntries(
  [
    ["8", 3.2, 0],
    ["10", 11.8, 0.002],
    ["11", 42.5, 0.004],
    ["12", 0.4, 0.21],
  ].map(([id, perMinute, errorRate]) => [
    id,
    {
      status: "available",
      perMinute,
      errorRate,
      pages: Math.round(Number(perMinute) * 18),
      points: Array.from(
        { length: 60 },
        (_, i) => Number(perMinute) * (0.7 + 0.3 * Math.sin((i + Number(id)) / 6)),
      ),
    },
  ]),
)

const events = [
  [12, "deploy", "shop-stack deployed", "release 349 · main@9f8e7d6", "info"],
  [41, "backup", "App data backed up", "1.0 GB to atlas-backups", "info"],
  [95, "deploy", "docs-site deploy failed", "build_failed · exit 1", "error"],
  [180, "action", "Restarted grafana", "by operator", "info"],
  [420, "backup", "Databases nightly backed up", "1.0 GB to atlas-backups", "info"],
  [610, "action", "Installed 4 package updates", "apt", "info"],
].map(([minutesAgo, kind, title, detail, severity]) => ({
  ts: iso(now - Number(minutesAgo) * 60_000),
  kind,
  title,
  detail,
  severity,
}))

const processes = [
  ...hostProcesses,
  ...[
    ["node", "node /app/dist/server.js", "app", 6.2, 420, "container", "shop-web"],
    ["redis-server", "redis-server *:6379", "redis", 2.4, 96, "container", "shop-cache"],
    ["grafana", "grafana server", "grafana", 1.1, 180, "container", "grafana"],
    ["java", "java -jar server.jar", "minecraft", 0.4, 2_100, "container", "survival"],
    ["dockerd", "dockerd -H fd://", "root", 0.9, 140, "systemd", "docker.service"],
  ].map(([name, cmdline, username, cpuPercent, rssMb, manager, managerName], i) => ({
    pid: 4000 + i,
    name,
    cmdline,
    username,
    cpuPercent,
    rss: Number(rssMb) * 1024 ** 2,
    manager,
    managerName,
    ppid: 1,
    status: "S",
    memPercent: ((Number(rssMb) * 1024 ** 2) / (16 * 1024 ** 3)) * 100,
    vms: Number(rssMb) * 2 * 1024 ** 2,
    threads: 8,
    nice: 0,
    createTime: iso(now - 3_600_000),
    state: "sleeping",
    ioReadRate: 0,
    ioWriteRate: 0,
  })),
]

const repos = [
  ["shop", "https://gitlab.com/acme/shop", true, 0],
  ["docs", "git@codeberg.org:acme/docs.git", false, 0],
  ["billing", "https://github.com/acme/billing", false, 2],
  ["dotfiles", "https://github.com/operator/dotfiles", false, 0],
].map(([name, remote, dirty, behind]) => ({
  path: `/srv/${name}`,
  name,
  branch: "main",
  remote,
  dirty,
  changes: dirty ? 3 : 0,
  staged: 0,
  untracked: 0,
  conflicts: 0,
  ahead: 0,
  behind,
  detached: false,
}))

/**
 * Mocks every read the Overview makes. `deployments` replaces the fleet, so a
 * spec can draw the empty fleet or one longer than the section shows.
 */
export async function mockOverview(
  page: Page,
  { deployments = fleet }: { deployments?: unknown[] } = {},
) {
  await mockHost(page)
  // Oldest first and only inside the asked window, as the recorder answers.
  await page.route("**/api/v1/system/metrics/events**", (route) => {
    const range = new URL(route.request().url()).searchParams.get("range") ?? "1h"
    const hours = range.endsWith("d") ? Number(range.slice(0, -1)) * 24 : Number(range.slice(0, -1))
    const since = now - hours * 3_600_000
    return json(route, events.filter((event) => Date.parse(event.ts) >= since).reverse())
  })
  // Ordered by what was asked for, as the process walk answers.
  await page.route("**/api/v1/processes/**", (route) => {
    const sort = new URL(route.request().url()).searchParams.get("sort")
    const by = sort === "memory" ? "rss" : "cpuPercent"
    return json(
      route,
      [...processes].sort((a, b) => Number(b[by]) - Number(a[by])),
    )
  })
  await page.route("**/api/v1/docker/containers/**", (route) => json(route, containers))
  await page.route("**/api/v1/databases/", (route) => json(route, databases))
  await page.route("**/api/v1/certificates/", (route) => json(route, certificates))
  await page.route("**/api/v1/backups/", (route) => json(route, backups))
  await page.route("**/api/v1/git/", (route) => json(route, { available: true, repos }))
  await page.route("**/api/v1/packages/updates", (route) =>
    json(route, {
      available: true,
      manager: "apt",
      packages: Array.from({ length: 7 }, (_, i) => ({
        name: `pkg-${i}`,
        current: "1.0",
        candidate: "1.1",
        security: i < 2,
      })),
      securityCount: 2,
      rebootRequired: false,
      securityFiltering: true,
      lastChecked: iso(now - 3_600_000),
    }),
  )
  await page.route("**/api/v1/exposure", (route) =>
    json(route, {
      grade: "tailscale",
      summary: "",
      allowlist: ["100.64.0.0/10"],
      interfaces: ["tailscale0"],
    }),
  )
  await page.route("**/api/v1/deploy/**", (route) => {
    const url = new URL(route.request().url())
    if (url.pathname.endsWith("/deploy/traffic")) return json(route, traffic)
    if (url.pathname.endsWith("/deploy/pull-requests")) return json(route, { projects: {} })
    if (url.searchParams.get("view") === "fleet")
      return json(route, {
        deployments,
        activeWork: [],
        slots: { heavyUsed: 0, heavyCapacity: 2, lightUsed: 0, lightCapacity: 4 },
      })
    return json(route, [])
  })
  await page.route("**/api/v1/dashboard/update**", (route) =>
    json(route, {
      version: "0.7.1",
      available: false,
      releases: [],
      history: [],
      breaking: false,
      check: { enabled: true, repo: "a/b", ref: "main" },
      install: { supported: true },
    }),
  )
}
