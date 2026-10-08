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

const COMPOSE = `services:
  web:
    image: nginx:1.27-alpine
    ports: ["80:80", "443:443"]
    networks: [frontend]
  api:
    image: node:22-alpine
    ports: ["127.0.0.1:3000:3000"]
    networks: [frontend, backend]
  worker:
    image: python:3.12-slim
    networks: [backend]
  db:
    image: postgres:16-alpine
    networks: [backend]
  cache:
    image: redis:7-alpine
    networks: [backend]
  search:
    image: getmeili/meilisearch:v1.9
networks:
  frontend:
  backend:
`

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
  options: { detail?: object; containers?: object[]; live?: boolean } = {},
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
    return route.fulfill({
      status: 503,
      contentType: "application/json",
      body: JSON.stringify({ error: { code: "not_available", message: "Not mocked" } }),
    })
  })
  return mocks
}
