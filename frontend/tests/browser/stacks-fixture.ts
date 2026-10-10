import type { Page, Route } from "@playwright/test"

/**
 * A server's compose stacks, as the Stacks page reads them: the stack list
 * (`GET /docker/stacks/`), every container and its live readings over the
 * containers socket, each container's last hour, the daemon's recent events
 * and the machine's own readings that size the band.
 *
 * Six stacks that each differ in what an operator should do about them: a
 * shop that is fine and busy, monitoring with Alertmanager killed for memory,
 * n8n up but failing its health check, analytics running a worker its compose
 * file no longer declares, a mail server stopped two days ago, and a wiki that
 * was written and never deployed.
 */

const NOW = Date.now()
const SEC = 1000
const MIN = 60 * SEC
const HOUR = 60 * MIN
const DAY = 24 * HOUR
const MB = 1024 * 1024
const GB = 1024 * MB
const iso = (msAgo: number) => new Date(NOW - msAgo).toISOString()

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
  stack: string
  service: string
  image: string
  state?: string
  status?: string
  health?: string
  /** How long ago it started, or stopped when it is not running. */
  since: number
  ports?: Port[]
  memoryLimit?: number
  cpu?: number
  memory?: number
  restartPolicy?: string
}

const SPECS: Spec[] = [
  // shop: four services, all up, the API carrying the traffic.
  {
    id: "a1f0c3d2e4b5",
    stack: "shop",
    service: "web",
    image: "nginx:1.27-alpine",
    health: "healthy",
    since: 3 * DAY + 4 * HOUR,
    ports: [
      { ip: "0.0.0.0", privatePort: 80, publicPort: 80, type: "tcp" },
      { ip: "0.0.0.0", privatePort: 443, publicPort: 443, type: "tcp" },
    ],
    cpu: 2.4,
    memory: 18 * MB,
  },
  {
    id: "a2b7d9e1f3c4",
    stack: "shop",
    service: "api",
    image: "ghcr.io/acme/shop-api:2.4.1",
    health: "healthy",
    since: 26 * MIN,
    ports: [{ ip: "127.0.0.1", privatePort: 3000, publicPort: 3000, type: "tcp" }],
    memoryLimit: 1 * GB,
    cpu: 38.6,
    memory: 286 * MB,
  },
  {
    id: "a3c8e2f4a6b8",
    stack: "shop",
    service: "db",
    image: "postgres:16-alpine",
    health: "healthy",
    since: 3 * DAY + 4 * HOUR,
    ports: [{ ip: "127.0.0.1", privatePort: 5432, publicPort: 5432, type: "tcp" }],
    memoryLimit: 1 * GB,
    cpu: 11.2,
    memory: 412 * MB,
  },
  {
    id: "a4d9f3a5b7c9",
    stack: "shop",
    service: "cache",
    image: "redis:7-alpine",
    since: 3 * DAY + 4 * HOUR,
    cpu: 1.3,
    memory: 46 * MB,
  },
  // monitoring: three of four up; Alertmanager was killed for memory.
  {
    id: "b1e4a7c2d5f8",
    stack: "monitoring",
    service: "prometheus",
    image: "prom/prometheus:v2.53.0",
    since: 9 * DAY,
    ports: [{ ip: "127.0.0.1", privatePort: 9090, publicPort: 9090, type: "tcp" }],
    cpu: 6.8,
    memory: 524 * MB,
  },
  {
    id: "b2f5b8d3e6a9",
    stack: "monitoring",
    service: "grafana",
    image: "grafana/grafana:11.1.0",
    health: "healthy",
    since: 9 * DAY,
    ports: [{ ip: "0.0.0.0", privatePort: 3000, publicPort: 3001, type: "tcp" }],
    cpu: 1.9,
    memory: 163 * MB,
  },
  {
    id: "b3a6c9e4f7b1",
    stack: "monitoring",
    service: "node-exporter",
    image: "prom/node-exporter:v1.8.1",
    since: 9 * DAY,
    cpu: 0.6,
    memory: 21 * MB,
  },
  {
    id: "b4b7d1f5a8c2",
    stack: "monitoring",
    service: "alertmanager",
    image: "prom/alertmanager:v0.27.0",
    state: "exited",
    status: "Exited (137) 12 minutes ago",
    since: 12 * MIN,
  },
  // n8n: both up, the app failing its own health check.
  {
    id: "c1c9e2a6b3d4",
    stack: "n8n",
    service: "n8n",
    image: "n8nio/n8n:1.62.1",
    health: "unhealthy",
    since: 2 * HOUR + 10 * MIN,
    ports: [{ ip: "127.0.0.1", privatePort: 5678, publicPort: 5678, type: "tcp" }],
    memoryLimit: 768 * MB,
    cpu: 17.4,
    memory: 655 * MB,
  },
  {
    id: "c2d1f3b7c4e5",
    stack: "n8n",
    service: "postgres",
    image: "postgres:16",
    health: "healthy",
    since: 6 * DAY,
    cpu: 2.1,
    memory: 138 * MB,
  },
  // analytics: up, with a worker the compose file stopped declaring.
  {
    id: "d1e2a4c8d5f6",
    stack: "analytics",
    service: "plausible",
    image: "ghcr.io/plausible/community-edition:v2.1.4",
    since: 1 * HOUR + 4 * MIN,
    ports: [{ ip: "127.0.0.1", privatePort: 8000, publicPort: 8000, type: "tcp" }],
    cpu: 4.2,
    memory: 231 * MB,
  },
  {
    id: "d2f3b5d9e6a7",
    stack: "analytics",
    service: "clickhouse",
    image: "clickhouse/clickhouse-server:24.3-alpine",
    since: 1 * HOUR + 4 * MIN,
    memoryLimit: 2 * GB,
    cpu: 9.7,
    memory: 1.31 * GB,
  },
  {
    id: "d3a4c6e1f7b8",
    stack: "analytics",
    service: "old-worker",
    image: "ghcr.io/acme/analytics-worker:0.9.0",
    since: 41 * DAY,
    cpu: 0.4,
    memory: 64 * MB,
  },
  // mailserver: stopped by hand two days ago.
  {
    id: "e1b5d7f2a8c9",
    stack: "mailserver",
    service: "mailserver",
    image: "ghcr.io/docker-mailserver/docker-mailserver:14.0",
    state: "exited",
    status: "Exited (0) 2 days ago",
    since: 2 * DAY,
  },
]

function container(spec: Spec) {
  const state = spec.state ?? "running"
  const running = state === "running"
  const name = `${spec.stack}-${spec.service}-1`
  const id = spec.id.padEnd(64, "0")
  return {
    id,
    names: [name],
    name,
    image: spec.image,
    imageId: `sha256:${spec.id}`,
    command: "",
    state,
    status: spec.status ?? `Up ${Math.round(spec.since / HOUR)} hours`,
    health: running ? spec.health : undefined,
    createdAt: iso(spec.since + MIN),
    startedAt: running ? iso(spec.since) : undefined,
    uptimeSeconds: running ? Math.floor(spec.since / 1000) : 0,
    ports: spec.ports ?? [],
    labels: {
      "com.docker.compose.project": spec.stack,
      "com.docker.compose.service": spec.service,
      "com.docker.compose.project.working_dir": `/srv/${spec.stack}`,
    },
    networks: [`${spec.stack}_default`],
    composeStack: spec.stack,
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
    restartPolicy: spec.restartPolicy ?? "unless-stopped",
    inspected: running,
  }
}

export const CONTAINERS = SPECS.map(container)

/** One frame of `docker stats` for every running container. */
export const STATS = SPECS.filter((s) => (s.state ?? "running") === "running").map((spec) => {
  const limit = spec.memoryLimit ?? 0
  const memory = spec.memory ?? 0
  return {
    id: spec.id.padEnd(64, "0"),
    name: `${spec.stack}-${spec.service}-1`,
    ts: iso(0),
    cpuPercent: spec.cpu ?? 0,
    cpuReady: true,
    memUsage: memory,
    memLimit: limit || 8 * GB,
    memLimited: limit > 0,
    memPercent: limit > 0 ? (memory / limit) * 100 : (memory / (8 * GB)) * 100,
    memHostPercent: (memory / (8 * GB)) * 100,
    hostCpus: 4,
    netRx: 0,
    netTx: 0,
    blockRead: 0,
    blockWrite: 0,
    pids: 12,
    onlineCpus: 4,
    cpuTotal: 0,
    systemCpu: 0,
  }
})

/** Forty buckets of each container's hour: a steady line with some weather in it. */
export const SPARKLINES = SPECS.filter((s) => (s.state ?? "running") === "running").map(
  (spec, index) => {
    const cpu = Array.from({ length: 40 }, (_, i) => {
      const wave = Math.sin((i + index * 3) / 4) * 0.35 + 1
      const burst = spec.service === "api" && i > 30 && i < 36 ? 1.8 : 1
      return Math.round((spec.cpu ?? 0) * wave * burst * 100) / 100
    })
    return {
      name: `${spec.stack}-${spec.service}-1`,
      cpu,
      mem: cpu.map(() => spec.memory ?? 0),
      cpuPeak: Math.max(...cpu),
      memPeak: spec.memory ?? 0,
    }
  },
)

type Service = {
  name: string
  container: string
  state: string
  status: string
  image: string
  health?: string
  ports: Port[]
  missing?: boolean
}

function services(stack: string): Service[] {
  return SPECS.filter((s) => s.stack === stack).map((spec) => {
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
  })
}

const missing = (name: string): Service => ({
  name,
  container: "",
  state: "",
  status: "",
  image: "",
  ports: [],
  missing: true,
})

function stack(
  name: string,
  over: { state: string; summary: string; declared: string[]; orphans?: string[] },
  extra: Service[] = [],
) {
  const list = [...services(name), ...extra].sort((a, b) => a.name.localeCompare(b.name))
  const present = list.filter((s) => !s.missing)
  return {
    name,
    workingDir: `/srv/${name}`,
    configFiles: [`/srv/${name}/compose.yaml`],
    services: list,
    running: present.filter((s) => s.state === "running").length,
    total: over.declared.length,
    managed: true,
    declared: over.declared,
    declaredSource: "file",
    containers: present.length,
    deployed: present.length > 0,
    orphans: over.orphans ?? [],
    state: over.state,
    summary: over.summary,
  }
}

export const STACKS = [
  stack("shop", {
    state: "running",
    summary: "Running · 4/4 services",
    declared: ["api", "cache", "db", "web"],
  }),
  stack("monitoring", {
    state: "partial",
    summary: "Partially running · 3/4 services",
    declared: ["alertmanager", "grafana", "node-exporter", "prometheus"],
  }),
  stack("n8n", {
    state: "degraded",
    summary: "Degraded · 1 unhealthy",
    declared: ["n8n", "postgres"],
  }),
  stack("analytics", {
    state: "running",
    summary: "Running · 2/2 services · 1 orphan",
    declared: ["clickhouse", "plausible"],
    orphans: ["old-worker"],
  }),
  stack("mailserver", {
    state: "stopped",
    summary: "Stopped · 0/1 services",
    declared: ["mailserver"],
  }),
  stack(
    "wiki",
    {
      state: "not-deployed",
      summary: "Not deployed · 2 services defined",
      declared: ["db", "wiki"],
    },
    [missing("db"), missing("wiki")],
  ),
]

function event(
  msAgo: number,
  stackName: string,
  service: string,
  action: string,
  level: "info" | "notice" | "error",
  message: string,
  exitCode?: string,
) {
  const spec = SPECS.find((s) => s.stack === stackName && s.service === service)
  return {
    time: iso(msAgo),
    type: "container",
    action,
    name: `${stackName}-${service}-1`,
    id: spec?.id.padEnd(64, "0"),
    image: spec?.image,
    stack: stackName,
    service,
    exitCode,
    message,
    level,
    source: "compose",
  }
}

/** Newest first, as the feed answers. */
export const EVENTS = [
  event(
    4 * MIN,
    "n8n",
    "n8n",
    "health_status: unhealthy",
    "error",
    "n8n is failing its health check",
  ),
  event(12 * MIN, "monitoring", "alertmanager", "oom", "error", "alertmanager ran out of memory"),
  event(
    12 * MIN,
    "monitoring",
    "alertmanager",
    "die",
    "error",
    "alertmanager exited with code 137",
    "137",
  ),
  event(26 * MIN, "shop", "api", "start", "info", "api started"),
  event(27 * MIN, "shop", "api", "die", "info", "api exited with code 0", "0"),
  event(64 * MIN, "analytics", "clickhouse", "start", "info", "clickhouse started"),
  event(64 * MIN, "analytics", "plausible", "start", "info", "plausible started"),
  event(2 * DAY, "mailserver", "mailserver", "stop", "info", "mailserver stopped"),
]

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

const json = (route: Route, body: unknown) =>
  route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify(body) })

export type StackMocks = {
  /** Every stack action posted, as `name/action`. */
  actions: string[]
}

/**
 * The server behind the Stacks page. `stacks` replaces the list, for a spec
 * that needs a server with nothing on it.
 */
export async function mockStacks(
  page: Page,
  options: { stacks?: object[]; containers?: object[]; stats?: object[] } = {},
): Promise<StackMocks> {
  const mocks: StackMocks = { actions: [] }
  await page.routeWebSocket("**/api/v1/system/stream**", (socket) => {
    socket.send(JSON.stringify({ type: "host", data: HOST }))
    socket.send(JSON.stringify({ type: "metrics", data: SNAPSHOT }))
  })
  await page.routeWebSocket(/\/api\/v1\/docker\/containers\/stream/, (socket) => {
    socket.send(JSON.stringify({ type: "containers", data: options.containers ?? CONTAINERS }))
    socket.send(JSON.stringify({ type: "stats", data: options.stats ?? STATS }))
  })
  await page.route("**/api/v1/**", async (route) => {
    const url = new URL(route.request().url())
    const path = url.pathname.replace(/^\/api\/v1/, "")
    const method = route.request().method()
    if (path === "/auth/session") return json(route, user)
    if (path === "/dashboard/update") return json(route, { current: "0.7.1", latest: "0.7.1" })
    if (path === "/system/host") return json(route, HOST)
    if (path === "/system/metrics") return json(route, SNAPSHOT)
    if (path === "/docker/ping") return json(route, { available: true, serverVersion: "29.8.0" })
    if (path === "/docker/stacks/" && method === "GET") return json(route, options.stacks ?? STACKS)
    if (path === "/docker/containers/") return json(route, options.containers ?? CONTAINERS)
    if (path === "/docker/containers/stats/history") return json(route, SPARKLINES)
    if (path === "/docker/events") {
      return json(route, { listening: true, since: iso(41 * DAY), buffered: 1, events: EVENTS })
    }
    const action = path.match(/^\/docker\/stacks\/([^/]+)\/(up|run)$/)
    if (action && method === "POST") {
      mocks.actions.push(`${decodeURIComponent(action[1])}/${action[2]}`)
      return json(route, { ok: true })
    }
    return route.fulfill({
      status: 503,
      contentType: "application/json",
      body: JSON.stringify({ error: { code: "not_available", message: "Not mocked" } }),
    })
  })
  return mocks
}
