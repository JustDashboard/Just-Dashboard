import type { Page, Route } from "@playwright/test"

/**
 * A self-hosting server's volumes, as /docker/volumes draws them: a shop
 * stack's database, cache and uploads (the uploads shared by two containers,
 * one of them read-only), a monitoring stack, standalone services, a workflow
 * engine whose container is stopped, a Nextcloud stack taken down with its
 * data left behind, an anonymous volume a removed container left, an empty
 * one nobody filled, and a plugin volume Docker cannot measure. Five of them
 * are covered by a backup job, one of those by a paused one.
 */

const now = Date.now()
const ago = (hours: number) => new Date(now - hours * 3600_000).toISOString()
const KB = 1024
const MB = 1024 * KB
const GB = 1024 * MB

export const user = {
  authenticated: true,
  needsTotp: false,
  needsEnrollment: false,
  require2fa: false,
  capabilities: [
    "read",
    "service.control",
    "file.write",
    "terminal",
    "destructive",
    "system.admin",
  ],
  user: {
    id: 1,
    username: "operator",
    role: "admin",
    totpEnabled: true,
    disabled: false,
    mustChangePassword: false,
    lastLoginAt: ago(1),
    createdAt: ago(900),
  },
}

type State = "running" | "exited" | "restarting" | "paused"

function container(id: string, name: string, image: string, state: State, composeStack?: string) {
  return {
    id: id.repeat(16).slice(0, 16),
    names: [`/${name}`],
    name,
    image,
    imageId: `sha256:${id.repeat(64).slice(0, 64)}`,
    command: "",
    state,
    status: state === "running" ? "Up 3 days" : "Exited (0) 2 days ago",
    createdAt: ago(24 * 9),
    startedAt: ago(72),
    uptimeSeconds: state === "running" ? 72 * 3600 : 0,
    ports: [],
    labels: composeStack ? { "com.docker.compose.project": composeStack } : {},
    networks: ["bridge"],
    exposure: [],
    hasHealthcheck: false,
    inspected: state === "running",
    composeStack,
  }
}

export const CONTAINERS = [
  container("1", "shop-web-1", "ghcr.io/acme/shop:2.4.1", "running", "shop"),
  container("2", "shop-worker-1", "ghcr.io/acme/shop:2.4.1", "running", "shop"),
  container("3", "shop-db-1", "postgres:16", "running", "shop"),
  container("4", "shop-cache-1", "redis:7.2-alpine", "running", "shop"),
  container("5", "monitoring-prometheus-1", "prom/prometheus:v2.54.1", "running", "monitoring"),
  container("6", "monitoring-grafana-1", "grafana/grafana:11.2.0", "running", "monitoring"),
  container("7", "minio", "minio/minio:latest", "running"),
  container("8", "vaultwarden", "vaultwarden/server:1.32.0", "running"),
  container("9", "n8n", "n8nio/n8n:1.64.3", "exited"),
  container("a", "uptime-kuma", "louislam/uptime-kuma:1", "running"),
  container("b", "jellyfin", "jellyfin/jellyfin:10.9.11", "running"),
]

const byName = new Map(CONTAINERS.map((c) => [c.name, c]))

function mount(name: string, destination: string, readOnly = false) {
  const c = byName.get(name)!
  return {
    id: c.id,
    name: c.name,
    state: c.state,
    destination,
    readOnly,
    stack: c.composeStack,
  }
}

const compose = (project: string, volume: string) => ({
  "com.docker.compose.project": project,
  "com.docker.compose.volume": volume,
  "com.docker.compose.version": "2.29.7",
})

function volume(
  name: string,
  options: {
    size: number
    /** Docker's count of what references it; -1 when it has not measured it. */
    refCount?: number
    created: number
    labels?: Record<string, string>
    driver?: string
    /** Driver options, which only the inspect route returns; the list carries their kind. */
    options?: Record<string, string>
    mountType?: string
    usedBy?: ReturnType<typeof mount>[]
  },
) {
  const usedBy = options.usedBy ?? []
  return {
    name,
    driver: options.driver ?? "local",
    mountpoint: `/var/lib/docker/volumes/${name}/_data`,
    // A few hours past the day, as a real creation time is.
    createdAt: ago(options.created + 3.4),
    scope: "local",
    labels: options.labels ?? {},
    size: options.size,
    refCount: options.refCount ?? usedBy.length,
    inUse: usedBy.length > 0,
    usedBy,
    mountType: options.mountType,
    options: options.options,
  }
}

const ANONYMOUS = "3f9ad2c81e7b4f60a5d29c3e8b7f1a04d6e2c95b8a7f3d1e0c4b6a92f8e5d7c1"

export const VOLUMES = [
  volume("minio-data", {
    size: 14.2 * GB,
    created: 24 * 210,
    usedBy: [mount("minio", "/data")],
  }),
  volume("shop_uploads", {
    size: 6.1 * GB,
    created: 24 * 96,
    labels: compose("shop", "uploads"),
    usedBy: [mount("shop-web-1", "/app/uploads"), mount("shop-worker-1", "/app/uploads", true)],
  }),
  volume("monitoring_prometheus", {
    size: 3.8 * GB,
    created: 24 * 120,
    labels: compose("monitoring", "prometheus"),
    usedBy: [mount("monitoring-prometheus-1", "/prometheus")],
  }),
  volume("shop_pgdata", {
    size: 2.4 * GB,
    created: 24 * 96,
    labels: compose("shop", "pgdata"),
    usedBy: [mount("shop-db-1", "/var/lib/postgresql/data")],
  }),
  volume("nextcloud_db", {
    size: 1.9 * GB,
    refCount: 0,
    created: 24 * 340,
    labels: compose("nextcloud", "db"),
  }),
  volume("jellyfin-config", {
    size: 1.3 * GB,
    created: 24 * 160,
    usedBy: [mount("jellyfin", "/config")],
  }),
  volume("nextcloud_html", {
    size: 840 * MB,
    refCount: 0,
    created: 24 * 340,
    labels: compose("nextcloud", "html"),
  }),
  volume("n8n_data", {
    size: 412 * MB,
    created: 24 * 58,
    usedBy: [mount("n8n", "/home/node/.n8n")],
  }),
  volume(ANONYMOUS, {
    size: 210 * MB,
    refCount: 0,
    created: 24 * 31,
    labels: { "com.docker.volume.anonymous": "" },
  }),
  volume("monitoring_grafana", {
    size: 126 * MB,
    created: 24 * 120,
    labels: compose("monitoring", "grafana"),
    usedBy: [mount("monitoring-grafana-1", "/var/lib/grafana")],
  }),
  volume("uptime-kuma", {
    size: 95 * MB,
    created: 24 * 75,
    usedBy: [mount("uptime-kuma", "/app/data")],
  }),
  volume("shop_redis", {
    size: 48 * MB,
    created: 24 * 96,
    labels: compose("shop", "redis"),
    usedBy: [mount("shop-cache-1", "/data")],
  }),
  volume("vaultwarden-data", {
    size: 38 * MB,
    created: 24 * 400,
    usedBy: [mount("vaultwarden", "/data")],
  }),
  volume("scratch", { size: 0, refCount: 0, created: 30 }),
  volume("nas-backups", {
    size: 0,
    refCount: -1,
    created: 24 * 44,
    mountType: "cifs",
    options: {
      type: "cifs",
      device: "//nas.lan/backups",
      o: "addr=10.0.0.5,username=backup,password=hunter2,vers=3.0",
    },
  }),
  volume("jellyfin-media", {
    size: -1,
    refCount: -1,
    created: 24 * 160,
    driver: "rclone",
    usedBy: [mount("jellyfin", "/media", true)],
  }),
]

export const STACKS = [
  { name: "shop", deployed: true, running: 4, total: 4, state: "running" },
  { name: "monitoring", deployed: true, running: 2, total: 2, state: "running" },
  { name: "nextcloud", deployed: false, running: 0, total: 2, state: "not-deployed" },
].map((s) => ({
  ...s,
  workingDir: `/opt/stacks/${s.name}`,
  configFiles: [`/opt/stacks/${s.name}/compose.yaml`],
  services: [],
  managed: true,
  declared: [],
  containers: s.running,
  orphans: [],
  summary: s.deployed ? `Running · ${s.running}/${s.total} services` : "Not deployed",
}))

function covered(
  name: string,
  job: { id: number; name: string; enabled?: boolean },
  lastBackupHours?: number,
) {
  return {
    kind: "volume",
    id: name,
    name,
    paths: [`/var/lib/docker/volumes/${name}/_data`],
    suggest: { name: `Volume ${name}`, sources: [`/var/lib/docker/volumes/${name}/_data`] },
    coveredBy: [{ jobId: job.id, jobName: job.name, enabled: job.enabled ?? true }],
    protected: job.enabled ?? true,
    lastBackupAt: lastBackupHours === undefined ? undefined : ago(lastBackupHours),
  }
}

function uncovered(name: string) {
  return {
    kind: "volume",
    id: name,
    name,
    paths: [`/var/lib/docker/volumes/${name}/_data`],
    suggest: { name: `Volume ${name}`, sources: [`/var/lib/docker/volumes/${name}/_data`] },
    coveredBy: [],
    protected: false,
  }
}

const SHOP = { id: 3, name: "Shop data" }
const COVERED = new Map([
  ["shop_pgdata", covered("shop_pgdata", SHOP, 3.2)],
  ["shop_uploads", covered("shop_uploads", SHOP, 3.2)],
  ["vaultwarden-data", covered("vaultwarden-data", { id: 4, name: "Passwords" }, 0.8)],
  ["monitoring_grafana", covered("monitoring_grafana", { id: 5, name: "Dashboards" }, 26)],
  ["minio-data", covered("minio-data", { id: 6, name: "Object storage", enabled: false }, 24 * 9)],
])

export const BACKUP_RESOURCES = {
  resources: VOLUMES.map((v) => COVERED.get(v.name) ?? uncovered(v.name)),
  unavailable: {},
}

function entry(root: string, name: string, isDir: boolean, size = 0) {
  return {
    name,
    path: `${root}/${name}`,
    size,
    mode: isDir ? "drwxr-xr-x" : "-rw-r--r--",
    modeOctal: isDir ? "0755" : "0644",
    isDir,
    isSymlink: false,
    modified: ago(2),
    owner: "999",
    group: "999",
    uid: 999,
    gid: 999,
  }
}

function listing(path: string) {
  const root = path.replace(/\/$/, "")
  return {
    path: root,
    parent: root.replace(/\/[^/]+$/, ""),
    entries: root.endsWith("shop_pgdata/_data")
      ? [
          entry(root, "base", true),
          entry(root, "global", true),
          entry(root, "pg_wal", true),
          entry(root, "PG_VERSION", false, 3),
          entry(root, "postgresql.conf", false, 29 * KB),
          entry(root, "pg_hba.conf", false, 5 * KB),
        ]
      : [entry(root, "data", true), entry(root, "config.json", false, 2 * KB)],
    roots: ["/"],
  }
}

export type VolumeMocks = {
  /** Every mutation the page sent, as `METHOD path?query`. */
  calls: string[]
  /** Sends a containers frame down the socket, as the daemon does on a state change. */
  frame: (containers: typeof CONTAINERS) => void
}

async function json(route: Route, body: unknown, status = 200) {
  await route.fulfill({ status, contentType: "application/json", body: JSON.stringify(body) })
}

export async function mockVolumes(
  page: Page,
  options: {
    volumes?: typeof VOLUMES
    capabilities?: string[]
    /** Leave the backup coverage unanswered, as it is for a reader the route refuses. */
    noBackups?: boolean
    /** Volumes the daemon refuses to delete, as it does one a container has mounted since. */
    refuse?: string[]
    /** What the daemon's prune reports instead of what this fixture would delete. */
    pruned?: string[]
  } = {},
): Promise<VolumeMocks> {
  const sockets: { send: (message: string) => void }[] = []
  const mocks: VolumeMocks = {
    calls: [],
    frame: (containers) => {
      for (const ws of sockets) ws.send(JSON.stringify({ type: "containers", data: containers }))
    },
  }
  let volumes = options.volumes ?? VOLUMES
  const session = options.capabilities ? { ...user, capabilities: options.capabilities } : user

  await page.routeWebSocket(/\/api\/v1\/docker\/containers\/stream/, (ws) => {
    sockets.push(ws)
    ws.send(JSON.stringify({ type: "containers", data: CONTAINERS }))
  })

  await page.route("**/api/v1/**", async (route) => {
    const request = route.request()
    const url = new URL(request.url())
    const path = url.pathname.replace(/^\/api\/v1/, "")
    const method = request.method()
    if (method !== "GET") mocks.calls.push(`${method} ${path}${url.search}`)
    switch (path) {
      case "/auth/session":
        return json(route, session)
      case "/dashboard/update":
        return json(route, { current: "0.7.1", latest: "0.7.1" })
      case "/docker/ping":
        return json(route, { available: true, serverVersion: "29.8.0" })
      case "/docker/containers/":
        return json(route, CONTAINERS)
      case "/docker/stacks/":
        return json(route, STACKS)
      case "/docker/volumes/":
        if (method === "POST") {
          const body = JSON.parse(request.postData() ?? "{}") as { name: string }
          const made = volume(body.name, { size: 0, refCount: 0, created: 0 })
          volumes = [...volumes, made]
          return json(route, made)
        }
        // The list route carries what options mount, never the options.
        return json(
          route,
          volumes.map((v) => ({ ...v, options: undefined })),
        )
      case "/docker/volumes/prune": {
        // The daemon's own filter: local, no driver options, nothing mounting it.
        const gone = options.pruned
          ? volumes.filter((v) => options.pruned!.includes(v.name))
          : volumes.filter((v) => v.usedBy.length === 0 && v.driver === "local" && !v.mountType)
        volumes = volumes.filter((v) => !gone.includes(v))
        return json(route, {
          kind: "volumes",
          spaceReclaimed: gone.reduce((sum, v) => sum + Math.max(v.size, 0), 0),
          items: gone.map((v) => v.name),
        })
      }
      case "/backups/resources":
        if (options.noBackups) {
          return json(route, { error: { code: "forbidden", message: "Not allowed" } }, 403)
        }
        return json(route, BACKUP_RESOURCES)
      case "/files/list":
        return json(route, listing(url.searchParams.get("path") ?? "/"))
    }
    const one = /^\/docker\/volumes\/([^/]+)$/.exec(path)
    if (one) {
      const name = decodeURIComponent(one[1])
      const found = volumes.find((v) => v.name === name)
      if (method === "DELETE") {
        if (options.refuse?.includes(name)) {
          return json(
            route,
            { error: { code: "conflict", message: `remove ${name}: volume is in use` } },
            409,
          )
        }
        volumes = volumes.filter((v) => v.name !== name)
        return route.fulfill({ status: 204 })
      }
      if (!found)
        return json(route, { error: { code: "not_found", message: "No such volume" } }, 404)
      return json(route, found)
    }
    return json(route, { error: { code: "not_available", message: "Not mocked" } }, 503)
  })
  return mocks
}
