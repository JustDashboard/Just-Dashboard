import type { Page, Route } from "@playwright/test"

/**
 * One compose stack as its own page reads it: the stack (`GET
 * /docker/stacks/shop`), every container with its live readings over the
 * containers socket, each container's last hour, the stack's events and the
 * machine's readings that size the band.
 *
 * `shop` is six services that each say something different: a web server
 * taking traffic on every interface, an API under a memory limit, a worker
 * in a restart loop that exits 1, a database near its limit, a cache with no
 * health check, and a search service the compose file declares and compose
 * never created. Its directory is a checkout with uncommitted changes, behind
 * its remote.
 */

const NOW = Date.now()
const SEC = 1000
const MIN = 60 * SEC
const HOUR = 60 * MIN
const DAY = 24 * HOUR
const MB = 1024 * 1024
const GB = 1024 * MB
const iso = (msAgo: number) => new Date(NOW - msAgo).toISOString()

export const STACK = "shop"

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
    lastLoginAt: iso(0),
    createdAt: iso(0),
  },
}

type Port = { ip?: string; privatePort: number; publicPort?: number; type: string }

type Spec = {
  id: string
  service: string
  image: string
  state?: string
  status?: string
  health?: string
  /** How long ago it started. */
  since: number
  ports?: Port[]
  networks: string[]
  memoryLimit?: number
  cpu?: number
  memory?: number
  /** Bytes a second in and out, which the live frames step the counters by. */
  rx?: number
  tx?: number
}

export const SPECS: Spec[] = [
  {
    id: "a1f0c3d2e4b5",
    service: "web",
    image: "nginx:1.27-alpine",
    health: "healthy",
    since: 3 * DAY + 4 * HOUR,
    ports: [
      { ip: "0.0.0.0", privatePort: 80, publicPort: 80, type: "tcp" },
      { ip: "0.0.0.0", privatePort: 443, publicPort: 443, type: "tcp" },
    ],
    networks: ["shop_frontend"],
    cpu: 2.4,
    memory: 18 * MB,
    rx: 420 * 1024,
    tx: 2.6 * MB,
  },
  {
    id: "a2b7d9e1f3c4",
    service: "api",
    image: "node:22-alpine",
    health: "healthy",
    since: 26 * MIN,
    ports: [{ ip: "127.0.0.1", privatePort: 3000, publicPort: 3000, type: "tcp" }],
    networks: ["shop_frontend", "shop_backend"],
    memoryLimit: 1 * GB,
    cpu: 38.6,
    memory: 286 * MB,
    rx: 180 * 1024,
    tx: 96 * 1024,
  },
  {
    id: "a5e1f4b8c2d6",
    service: "worker",
    image: "python:3.12-slim",
    state: "restarting",
    status: "Restarting (1) 8 seconds ago",
    since: 0,
    networks: ["shop_backend"],
  },
  {
    id: "a3c8e2f4a6b8",
    service: "db",
    image: "postgres:16-alpine",
    health: "healthy",
    since: 3 * DAY + 4 * HOUR,
    networks: ["shop_backend"],
    memoryLimit: 512 * MB,
    cpu: 11.2,
    memory: 461 * MB,
    rx: 64 * 1024,
    tx: 140 * 1024,
  },
  {
    id: "a4d9f3a5b7c9",
    service: "cache",
    image: "redis:7-alpine",
    since: 3 * DAY + 4 * HOUR,
    networks: ["shop_backend"],
    cpu: 1.3,
    memory: 46 * MB,
    rx: 12 * 1024,
    tx: 30 * 1024,
  },
]

const running = (spec: Spec) => (spec.state ?? "running") === "running"
const nameOf = (spec: Spec) => `${STACK}-${spec.service}-1`
export const idOf = (spec: Spec) => spec.id.padEnd(64, "0")

function container(spec: Spec) {
  const state = spec.state ?? "running"
  const up = state === "running"
  return {
    id: idOf(spec),
    names: [nameOf(spec)],
    name: nameOf(spec),
    image: spec.image,
    imageId: `sha256:${spec.id}`,
    command: "",
    state,
    status: spec.status ?? `Up ${Math.round(spec.since / HOUR)} hours`,
    health: up ? spec.health : undefined,
    createdAt: iso(spec.since + MIN),
    startedAt: up ? iso(spec.since) : undefined,
    uptimeSeconds: up ? Math.floor(spec.since / 1000) : 0,
    ports: spec.ports ?? [],
    labels: {
      "com.docker.compose.project": STACK,
      "com.docker.compose.service": spec.service,
      "com.docker.compose.project.working_dir": `/srv/${STACK}`,
    },
    networks: spec.networks,
    composeStack: STACK,
    composeService: spec.service,
    exposure: (spec.ports ?? []).map((p) => ({
      hostIp: p.ip,
      hostPort: p.publicPort,
      containerPort: p.privatePort,
      protocol: p.type,
      scope: p.ip === "127.0.0.1" ? "loopback" : "all",
      label: p.ip === "127.0.0.1" ? "This server only" : "Every interface",
      summary: "",
    })),
    memoryLimit: spec.memoryLimit ?? 0,
    hasHealthcheck: spec.health !== undefined,
    restartPolicy: "unless-stopped",
    inspected: up,
  }
}

export const CONTAINERS = SPECS.map(container)

/**
 * One frame of `docker stats` for every running container, `seconds` after
 * the first: the counters step by each container's rate, and the readings
 * wander a little, so a page held open sees them move.
 */
export function statsFrame(seconds: number) {
  return SPECS.filter(running).map((spec, index) => {
    const limit = spec.memoryLimit ?? 0
    const wave = 1 + Math.sin(seconds / 3 + index) * 0.12
    const memory = Math.round((spec.memory ?? 0) * (1 + Math.sin(seconds / 7 + index) * 0.01))
    return {
      id: idOf(spec),
      name: nameOf(spec),
      ts: new Date(NOW + seconds * SEC).toISOString(),
      cpuPercent: Math.round((spec.cpu ?? 0) * wave * 100) / 100,
      cpuReady: true,
      memUsage: memory,
      memLimit: limit || 8 * GB,
      memLimited: limit > 0,
      memPercent: limit > 0 ? (memory / limit) * 100 : (memory / (8 * GB)) * 100,
      memHostPercent: (memory / (8 * GB)) * 100,
      hostCpus: 4,
      netRx: Math.round((spec.rx ?? 0) * (seconds + 600)),
      netTx: Math.round((spec.tx ?? 0) * (seconds + 600)),
      networkAvailable: true,
      blockRead: 0,
      blockWrite: 0,
      pids: 12,
      onlineCpus: 4,
      cpuTotal: 0,
      systemCpu: 0,
    }
  })
}

/** Forty buckets of each container's hour: a steady line with some weather in it. */
export const SPARKLINES = SPECS.filter(running).map((spec, index) => {
  const cpu = Array.from({ length: 40 }, (_, i) => {
    const wave = Math.sin((i + index * 3) / 4) * 0.35 + 1
    const burst = spec.service === "api" && i > 30 && i < 36 ? 1.8 : 1
    return Math.round((spec.cpu ?? 0) * wave * burst * 100) / 100
  })
  return {
    name: nameOf(spec),
    cpu,
    mem: cpu.map(() => spec.memory ?? 0),
    cpuPeak: Math.max(...cpu),
    memPeak: spec.memory ?? 0,
  }
})

export const DETAIL = {
  name: STACK,
  workingDir: `/srv/${STACK}`,
  configFiles: [`/srv/${STACK}/compose.yaml`],
  configPath: `/srv/${STACK}/compose.yaml`,
  services: [
    ...SPECS.map((spec) => {
      const c = container(spec)
      return {
        name: spec.service,
        container: c.id,
        state: c.state,
        status: c.status,
        image: spec.image,
        health: c.health,
        ports: c.ports,
      }
    }),
    {
      name: "search",
      container: "",
      state: "missing",
      status: "",
      image: "getmeili/meilisearch:v1.9",
      ports: [],
      missing: true,
    },
  ],
  running: 4,
  total: 6,
  managed: true,
  declared: ["api", "cache", "db", "search", "web", "worker"],
  declaredSource: "compose",
  containers: 5,
  deployed: true,
  orphans: [],
  state: "degraded",
  summary: "Degraded · 4/6 services",
  git: {
    path: `/srv/${STACK}`,
    branch: "main",
    dirty: true,
    changes: 2,
    ahead: 0,
    behind: 1,
    commit: "9f3c2a1",
    subject: "Raise the API's memory limit",
  },
}

function event(
  msAgo: number,
  service: string,
  action: string,
  level: "info" | "notice" | "error",
  message: string,
  exitCode?: string,
) {
  const spec = SPECS.find((s) => s.service === service)!
  return {
    time: iso(msAgo),
    type: "container",
    action,
    name: nameOf(spec),
    id: idOf(spec),
    image: spec.image,
    stack: STACK,
    service,
    exitCode,
    message,
    level,
    source: "compose",
  }
}

/**
 * The worker exits 1 and comes back, again and again for nine minutes, and
 * is down between two tries now: its newest exit has no start after it.
 */
const LOOP = [
  event(9 * MIN + 30 * SEC, "worker", "start", "info", "worker started"),
  ...Array.from({ length: 6 }, (_, i) => {
    const at = 8 * SEC + i * 95 * SEC
    const exit = event(at, "worker", "die", "error", "worker exited with code 1", "1")
    return i === 0
      ? [exit]
      : [exit, event(at - 4 * SEC, "worker", "start", "info", "worker started")]
  }).flat(),
]

/** Newest first, as the feed answers. */
export const EVENTS = [
  ...LOOP,
  event(14 * MIN, "db", "health_status: unhealthy", "error", "db is failing its health check"),
  event(13 * MIN, "db", "health_status: healthy", "info", "db passed its health check"),
  event(26 * MIN, "api", "start", "info", "api started"),
  event(26 * MIN + 2 * SEC, "api", "die", "info", "api exited with code 0", "0"),
  event(3 * DAY + 4 * HOUR, "web", "start", "info", "web started"),
].sort((a, b) => Date.parse(b.time) - Date.parse(a.time))

export const HOST = {
  hostname: "atlas",
  os: "linux",
  platform: "ubuntu",
  platformVersion: "24.04",
  kernelVersion: "6.8.0-45-generic",
  kernelArch: "x86_64",
  cpuModel: "AMD EPYC 7B13",
  cpuCores: 4,
  processes: 284,
}

export const SNAPSHOT = {
  ts: iso(0),
  cpu: {
    totalPercent: 31.2,
    perCore: [36, 28, 33, 28],
    loadAvg1: 1.24,
    loadAvg5: 1.02,
    loadAvg15: 0.94,
    cores: 4,
    modes: { user: 24, system: 6, iowait: 1, steal: 0, idle: 69 },
  },
  memory: {
    total: 8 * GB,
    used: 5.2 * GB,
    free: 0.9 * GB,
    available: 2.8 * GB,
    cached: 1.9 * GB,
    buffers: 0,
    usedPercent: 65,
  },
  swap: { total: 0, used: 0, free: 0, usedPercent: 0 },
  mounts: [],
  net: [],
  uptimeSeconds: 41 * 86_400,
}

const COMPOSE = `name: shop

services:
  web:
    image: nginx:1.27-alpine
    ports: ["80:80", "443:443"]
    volumes:
      - ./nginx:/etc/nginx/conf.d:ro
    depends_on: [api]
    networks: [frontend]
  api:
    image: node:22-alpine
    build: ./app
    ports: ["127.0.0.1:3000:3000"]
    env_file: .env
    volumes:
      - ./uploads:/app/uploads
    mem_limit: 1g
    healthcheck:
      test: ["CMD", "wget", "-qO-", "http://localhost:3000/health"]
    depends_on: [db, cache]
    networks: [frontend, backend]
  worker:
    image: python:3.12-slim
    command: python -m worker
    environment:
      QUEUE_CONCURRENCY: 4
    depends_on: [cache]
    networks: [backend]
  db:
    image: postgres:16-alpine
    mem_limit: 512m
    volumes:
      - pgdata:/var/lib/postgresql/data
    networks: [backend]
  cache:
    image: redis:7-alpine
    networks: [backend]
  search:
    image: getmeili/meilisearch:v1.9
    volumes:
      - meili:/meili_data
    networks: [backend]

networks:
  frontend:
  backend:

volumes:
  pgdata:
  meili:
`

/**
 * What Deploy would do now: the API's limit and the worker's image changed
 * since the last recorded deployment, search was never created, and the rest
 * is as it was. The diff is the server's — grouped by the service each line
 * sits under, three lines of context around a change.
 */
export const PREVIEW = {
  project: STACK,
  action: "up",
  services: [
    {
      name: "api",
      change: "recreate",
      reason:
        "Its resource limits changed since the last deployment, so the container is replaced. Anything written inside it rather than into a volume is lost.",
      fields: ["resource limits"],
      inferred: true,
    },
    {
      name: "worker",
      change: "recreate",
      reason:
        "Its image, environment changed since the last deployment, so the container is replaced. Anything written inside it rather than into a volume is lost.",
      fields: ["image", "environment"],
      imageBefore: "python:3.11-slim",
      imageAfter: "python:3.12-slim",
      inferred: true,
    },
    {
      name: "search",
      change: "create",
      reason: "No container exists for this service, so one will be created and started.",
      fields: [],
      inferred: true,
    },
    ...["web", "db", "cache"].map((name) => ({
      name,
      change: "unchanged",
      reason: "Its configuration is identical to the last deployment.",
      fields: [],
      inferred: true,
    })),
  ],
  recreate: 2,
  start: 0,
  unchanged: 3,
  create: 1,
  remove: 0,
  volumesRemoved: [],
  volumesKept: ["shop_pgdata", "shop_meili"],
  diff: [
    { kind: "same", text: "  api:", section: "api" },
    { kind: "same", text: "    image: node:22-alpine", section: "api" },
    { kind: "same", text: '    ports: ["127.0.0.1:3000:3000"]', section: "api" },
    { kind: "removed", text: "    mem_limit: 768m", section: "api" },
    { kind: "added", text: "    mem_limit: 1g", section: "api" },
    { kind: "same", text: "    networks: [frontend, backend]", section: "api" },
    { kind: "gap", text: "…" },
    { kind: "same", text: "  worker:", section: "worker" },
    { kind: "removed", text: "    image: python:3.11-slim", section: "worker" },
    { kind: "added", text: "    image: python:3.12-slim", section: "worker" },
    { kind: "added", text: "    environment:", section: "worker" },
    { kind: "added", text: "      QUEUE_CONCURRENCY: 4", section: "worker" },
    { kind: "same", text: "    networks: [backend]", section: "worker" },
  ],
  diffAgainst: "the last deployment recorded by this dashboard, 2 days ago",
  summary:
    "2 services will be recreated, 1 service will be created, 3 services will remain unchanged. No volumes will be removed.",
  caveats: [
    "Compose makes the final decision and can recreate a service for a reason outside the file — a changed base image, or a container removed by hand. This is what is expected, not a guarantee.",
  ],
}

const DIGESTS = {
  web: "nginx@sha256:5f0574409b3add89581b96c68afe9e9c7b284651c3a974b6e8bac46bf95e6b7f",
  api: "node@sha256:1c4c7a3f4d9e0b6a2f8e5d3c1b0a9f8e7d6c5b4a3f2e1d0c9b8a7f6e5d4c3b2a",
  worker: "python@sha256:9e8d7c6b5a4f3e2d1c0b9a8f7e6d5c4b3a2f1e0d9c8b7a6f5e4d3c2b1a0f9e8d",
  db: "postgres@sha256:3a2b1c0d9e8f7a6b5c4d3e2f1a0b9c8d7e6f5a4b3c2d1e0f9a8b7c6d5e4f3a2b",
  cache: "redis@sha256:7b6a5f4e3d2c1b0a9f8e7d6c5b4a3f2e1d0c9b8a7f6e5d4c3b2a1f0e9d8c7b6a",
}

/**
 * The states this dashboard wrote down before changing the stack, newest
 * first. Two configurations repeat — a restart records the file it found —
 * and one deploy happened with uncommitted changes in the checkout.
 */
export const DEPLOYMENTS = [
  { id: 41, action: "restart", ago: 2 * DAY, hash: "c7e1a9f04b2d6e8a1c3f5b7d", actor: "operator" },
  {
    id: 40,
    action: "up",
    ago: 2 * DAY + 3 * HOUR,
    hash: "c7e1a9f04b2d6e8a1c3f5b7d",
    actor: "operator",
    dirty: true,
  },
  { id: 37, action: "update", ago: 5 * DAY, hash: "8b3f2d6c1a9e7b5d3f1a8c6e", actor: "maria" },
  { id: 33, action: "recreate", ago: 9 * DAY, hash: "8b3f2d6c1a9e7b5d3f1a8c6e", actor: "operator" },
  { id: 30, action: "stop", ago: 12 * DAY, hash: "2a6c8e0f4b1d3a5c7e9f0b2d", actor: "maria" },
  { id: 28, action: "up", ago: 16 * DAY, hash: "2a6c8e0f4b1d3a5c7e9f0b2d", actor: "operator" },
].map((d) => ({
  id: d.id,
  project: STACK,
  workingDir: `/srv/${STACK}`,
  createdAt: iso(d.ago),
  configHash: d.hash,
  services: ["api", "cache", "db", "search", "web", "worker"],
  imageDigests: DIGESTS,
  envHash: "e3b0c44298fc1c149afbf4c8",
  gitCommit: d.id >= 40 ? "9f3c2a1e8b7d6c5a" : "41be07c2d9a8f6e3",
  gitBranch: "main",
  gitDirty: d.dirty ?? false,
  actor: d.actor,
  source: "dashboard",
  action: d.action,
  result: "replaced",
}))

/** The single-record route: the file as it was, and whether its images are still here. */
function deployment(id: number) {
  const record = DEPLOYMENTS.find((d) => d.id === id)
  if (!record) return undefined
  const older = id < 37
  return {
    ...record,
    config: older
      ? COMPOSE.replace("node:22-alpine", "node:20-alpine").replace(
          "python:3.12-slim",
          "python:3.11-slim",
        )
      : COMPOSE.replace("python:3.12-slim", "python:3.11-slim"),
    restorable: !older,
    missing: older ? ["api"] : [],
  }
}

/** The stack's directory: its compose file beside what compose reads and builds from. */
export const FILES = [
  { name: "app", isDir: true, size: 4096, ago: 2 * DAY },
  { name: "nginx", isDir: true, size: 4096, ago: 9 * DAY },
  { name: "backups", isDir: true, size: 4096, ago: 6 * HOUR },
  { name: "compose.yaml", size: 486, ago: 2 * DAY },
  { name: ".env", size: 312, ago: 5 * DAY },
  { name: "Dockerfile", size: 642, ago: 9 * DAY },
  { name: "README.md", size: 2_380, ago: 16 * DAY },
  { name: "backup.sh", size: 914, ago: 16 * DAY, mode: "-rwxr-xr-x" },
].map((f) => ({
  name: f.name,
  path: `/srv/${STACK}/${f.name}`,
  size: f.size,
  mode: f.mode ?? (f.isDir ? "drwxr-xr-x" : "-rw-r--r--"),
  modeOctal: f.isDir || f.mode ? "0755" : "0644",
  isDir: f.isDir ?? false,
  isSymlink: false,
  modified: iso(f.ago),
  owner: "deploy",
  group: "deploy",
  uid: 1000,
  gid: 1000,
}))

/** What the checkout says has changed in the stack's directory. */
export const GIT_STATUS = {
  repo: { path: `/srv/${STACK}`, name: STACK, branch: "main" },
  files: [
    {
      path: "compose.yaml",
      index: " ",
      worktree: "M",
      label: "modified",
      staged: false,
      unstaged: true,
    },
    {
      path: "backups/",
      index: "?",
      worktree: "?",
      label: "untracked",
      staged: false,
      unstaged: true,
    },
  ],
  clean: false,
  stashes: 0,
  identity: {},
}

/**
 * The stack's log as the server merges it: each line carries its service as
 * its source and as an attr, and its container's lens has read it.
 */
export const LOG_LINES = [
  ["web", "info", '172.18.0.1 - - "GET /api/products HTTP/1.1" 200 1843'],
  ["api", "info", "GET /api/products 200 in 38ms"],
  ["db", "info", "LOG:  checkpoint complete: wrote 1840 buffers (11.2%)"],
  ["worker", "error", "ConnectionError: could not reach the queue at cache:6379"],
  ["worker", "error", "Traceback (most recent call last): worker exited with code 1"],
  ["cache", "info", "* Background saving terminated with success"],
  ["api", "warn", "Slow query on /api/orders took 1843ms"],
  ["web", "info", '172.18.0.1 - - "GET / HTTP/1.1" 200 6120'],
  ["db", "warn", "WARNING:  memory usage is at 90% of its limit"],
  ["api", "info", "POST /api/cart 201 in 54ms"],
].map(([service, level, text], i) => ({
  text,
  level,
  source: service,
  timestamp: new Date(NOW - (10 - i) * 6 * SEC).toISOString(),
  attrs: { service, container: SPECS.find((s) => s.service === service)!.id.slice(0, 12) },
}))

/** Lines each service wrote in the last hour, and the errors among them. */
const LINES_AN_HOUR: Record<string, number> = {
  web: 1820,
  api: 964,
  db: 212,
  worker: 386,
  cache: 64,
}
const ERRORS_AN_HOUR: Record<string, number> = { worker: 46, api: 3 }

/** The log search the readings and the services strip ask: counts, faceted and bucketed by service. */
function logSearch(params: URLSearchParams) {
  const levels = params.get("levels") ?? ""
  const counts = levels.includes("error")
    ? ERRORS_AN_HOUR
    : levels === "warn"
      ? { api: 12, db: 4 }
      : params.getAll("f").some((f) => f.startsWith("event:"))
        ? { worker: 6, api: 1 }
        : LINES_AN_HOUR
  const values = Object.entries(counts).map(([value, count]) => ({ value, count }))
  const total = values.reduce((sum, v) => sum + v.count, 0)
  const histogram = Array.from({ length: 12 }, (_, i) => ({
    start: iso((12 - i) * 5 * MIN),
    total: 0,
    counts: Object.fromEntries(
      Object.entries(counts).map(([name, count], k) => [
        name,
        Math.round((count / 12) * (1 + Math.sin(i / 2 + k) * 0.6)),
      ]),
    ),
  }))
  return {
    lines: [],
    scanned: total,
    matched: total,
    truncated: false,
    complete: true,
    files: [],
    histogram,
    histogramBy: params.get("histogramBy") ?? undefined,
    facets: params.get("facets")
      ? { service: { values, distinct: values.length, other: 0, missing: 0 } }
      : undefined,
    tookMillis: 4,
  }
}

const json = (route: Route, body: unknown) =>
  route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify(body) })

export type StackDetailMocks = {
  /** Every compose command the page ran, as `action` or `action:service`. */
  runs: string[]
}

/**
 * The server behind `/docker/stacks/shop`. The containers socket sends a
 * frame every two seconds while the page holds it open, as the server does.
 */
export async function mockStackDetail(
  page: Page,
  options: {
    detail?: object
    containers?: object[]
    live?: boolean
    preview?: object
    deployments?: object[]
  } = {},
): Promise<StackDetailMocks> {
  const mocks: StackDetailMocks = { runs: [] }
  await page.routeWebSocket("**/api/v1/system/stream**", (socket) => {
    socket.send(JSON.stringify({ type: "host", data: HOST }))
    socket.send(JSON.stringify({ type: "metrics", data: SNAPSHOT }))
  })
  await page.routeWebSocket(/\/api\/v1\/docker\/containers\/stream/, (socket) => {
    socket.send(JSON.stringify({ type: "containers", data: options.containers ?? CONTAINERS }))
    socket.send(JSON.stringify({ type: "stats", data: statsFrame(-2) }))
    socket.send(JSON.stringify({ type: "stats", data: statsFrame(0) }))
    if (options.live === false) return
    let second = 0
    const timer = setInterval(() => {
      second += 2
      try {
        socket.send(JSON.stringify({ type: "stats", data: statsFrame(second) }))
      } catch {
        clearInterval(timer)
      }
    }, 2000)
    socket.onClose(() => clearInterval(timer))
  })
  await page.routeWebSocket(/\/api\/v1\/docker\/events\/stream/, () => {})
  await page.routeWebSocket(/\/api\/v1\/logs\/stream/, (socket) => {
    socket.send(JSON.stringify({ type: "logs", data: LOG_LINES, ts: Date.now() }))
  })
  await page.routeWebSocket(/\/api\/v1\/docker\/stacks\/[^/]+\/run/, (socket) => {
    const url = new URL(socket.url())
    const action = url.searchParams.get("action") ?? ""
    const service = url.searchParams.get("service")
    mocks.runs.push(service ? `${action}:${service}` : action)
    socket.send(JSON.stringify({ type: "output", data: [{ stream: "stdout", text: "Done" }] }))
    socket.send(JSON.stringify({ type: "done", data: { exitCode: 0 } }))
  })
  await page.route("**/api/v1/**", async (route) => {
    const url = new URL(route.request().url())
    const path = url.pathname.replace(/^\/api\/v1/, "")
    if (path === "/auth/session") return json(route, user)
    if (path === "/dashboard/update") return json(route, { current: "0.7.1", latest: "0.7.1" })
    if (path === "/system/host") return json(route, HOST)
    if (path === "/system/metrics") return json(route, SNAPSHOT)
    if (path === "/docker/ping") return json(route, { available: true, serverVersion: "29.8.0" })
    if (path === `/docker/stacks/${STACK}`) return json(route, options.detail ?? DETAIL)
    if (path === `/docker/stacks/${STACK}/config`)
      return json(route, { path: DETAIL.configPath, content: COMPOSE })
    if (path === "/docker/containers/") return json(route, options.containers ?? CONTAINERS)
    if (path === "/docker/containers/stats/history") return json(route, SPARKLINES)
    if (path === "/docker/events") {
      return json(route, { listening: true, since: iso(41 * DAY), buffered: 1, events: EVENTS })
    }
    if (path === `/docker/stacks/${STACK}/preview`) return json(route, options.preview ?? PREVIEW)
    if (path === `/docker/stacks/${STACK}/deployments`)
      return json(route, options.deployments ?? DEPLOYMENTS)
    const record = path.match(new RegExp(`^/docker/stacks/${STACK}/deployments/(\\d+)$`))
    if (record) return json(route, deployment(Number(record[1])))
    if (path === `/docker/stacks/${STACK}/validate`) {
      return json(route, { valid: true, services: DETAIL.declared })
    }
    if (path === "/files/list") {
      const dir = url.searchParams.get("path") ?? ""
      return json(route, {
        path: dir,
        parent: `/srv`,
        entries: dir === `/srv/${STACK}` ? FILES : [],
        roots: ["/"],
      })
    }
    if (path === "/files/stat") {
      const entry = FILES.find((f) => f.path === url.searchParams.get("path"))
      if (entry) return json(route, entry)
      return route.fulfill({
        status: 404,
        contentType: "application/json",
        body: JSON.stringify({ error: { code: "not_found", message: "Not found" } }),
      })
    }
    if (path === "/git/status") return json(route, GIT_STATUS)
    if (path === "/logs/search") return json(route, logSearch(url.searchParams))
    if (path.startsWith("/logs/")) return json(route, {})
    return route.fulfill({
      status: 503,
      contentType: "application/json",
      body: JSON.stringify({ error: { code: "not_available", message: "Not mocked" } }),
    })
  })
  return mocks
}
