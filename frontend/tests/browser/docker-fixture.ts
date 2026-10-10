import type { Page, Route } from "@playwright/test"

/**
 * A working Docker host, as the overview reads it: three compose projects and
 * two containers started by hand, live readings for everything running, an
 * hour of each one's shape, the daemon's recent events and its facts.
 *
 * Two things are wrong on it, on purpose, because they are what the page
 * exists to say first: the shop's worker was killed for memory twelve minutes
 * ago, and Uptime Kuma is failing its health check. Mailhog was stopped by
 * hand two days ago, which is not a problem and must not read as one.
 */

const NOW = Date.now()
const SEC = 1000
const MIN = 60 * SEC
const HOUR = 60 * MIN
const DAY = 24 * HOUR
const MB = 1024 * 1024
const GB = 1024 * MB
const iso = (msAgo: number) => new Date(NOW - msAgo).toISOString()

type Over = Record<string, unknown>

function container(
  id: string,
  name: string,
  image: string,
  stack: string | undefined,
  over: Over = {},
) {
  const uptime = (over.uptimeSeconds as number | undefined) ?? (3 * DAY) / 1000
  return {
    id: id.padEnd(64, "0"),
    names: [name],
    name,
    image,
    imageId: `sha256:${id}`,
    command: "",
    state: "running",
    status: "Up 3 days",
    createdAt: iso(4 * DAY),
    startedAt: iso(uptime * 1000),
    uptimeSeconds: uptime,
    ports: [],
    labels: stack
      ? {
          "com.docker.compose.project": stack,
          "com.docker.compose.service": name.replace(`${stack}-`, ""),
        }
      : {},
    networks: [stack ? `${stack}_default` : "bridge"],
    composeStack: stack,
    composeService: stack ? name.replace(`${stack}-`, "") : undefined,
    exposure: [],
    hasHealthcheck: false,
    inspected: true,
    memoryLimit: 0,
    restartPolicy: "unless-stopped",
    ...over,
  }
}

function port(hostPort: number, containerPort: number, scope: "all" | "loopback") {
  return {
    hostIp: scope === "all" ? "0.0.0.0" : "127.0.0.1",
    hostPort,
    containerPort,
    protocol: "tcp",
    scope,
    label: scope === "all" ? "Every interface" : "This server only",
    summary:
      scope === "all"
        ? `Bound to every interface on port ${hostPort}.`
        : `Bound to 127.0.0.1:${hostPort}. Only processes on this server can reach it.`,
  }
}

export const DOCKER_CONTAINERS = [
  container("a1b2c3d4e5f6", "shop-web", "nginx:1.27-alpine", "shop", {
    exposure: [port(443, 443, "all"), port(80, 80, "all")],
    health: "healthy",
    hasHealthcheck: true,
  }),
  container("b2c3d4e5f6a1", "shop-api", "node:22-alpine", "shop", {
    status: "Up 41 minutes",
    uptimeSeconds: 41 * 60,
    memoryLimit: 512 * MB,
    cpuLimit: 2,
    health: "healthy",
    hasHealthcheck: true,
    exposure: [port(3000, 3000, "loopback")],
  }),
  container("c3d4e5f6a1b2", "shop-db", "postgres:16", "shop", {
    exposure: [port(5432, 5432, "all")],
    health: "healthy",
    hasHealthcheck: true,
  }),
  container("d4e5f6a1b2c3", "shop-cache", "redis:7-alpine", "shop", {
    exposure: [port(6379, 6379, "loopback")],
  }),
  container("e5f6a1b2c3d4", "shop-worker", "node:22-alpine", "shop", {
    state: "exited",
    status: "Exited (137) 12 minutes ago",
    uptimeSeconds: 0,
    memoryLimit: 256 * MB,
    inspected: false,
  }),
  container("f6a1b2c3d4e5", "monitoring-prometheus", "prom/prometheus:v3.1.0", "monitoring", {
    memoryLimit: 2 * GB,
    exposure: [port(9090, 9090, "loopback")],
  }),
  container("a7b8c9d0e1f2", "monitoring-grafana", "grafana/grafana:11.4.0", "monitoring", {
    exposure: [port(3001, 3000, "loopback")],
    health: "healthy",
    hasHealthcheck: true,
  }),
  container("b8c9d0e1f2a7", "monitoring-node-exporter", "prom/node-exporter:v1.8.2", "monitoring"),
  container("c9d0e1f2a7b8", "monitoring-uptime-kuma", "louislam/uptime-kuma:1", "monitoring", {
    status: "Up 6 hours (unhealthy)",
    uptimeSeconds: 6 * 3600,
    health: "unhealthy",
    hasHealthcheck: true,
    exposure: [port(3002, 3001, "loopback")],
  }),
  container("d0e1f2a7b8c9", "automation-n8n", "n8nio/n8n:1.80.0", "automation", {
    status: "Up 2 hours",
    uptimeSeconds: 2 * 3600,
    health: "healthy",
    hasHealthcheck: true,
    exposure: [port(5678, 5678, "loopback")],
  }),
  container("e1f2a7b8c9d0", "portainer", "portainer/portainer-ce:2.24.1", undefined, {
    exposure: [port(9443, 9443, "all")],
  }),
  container("f2a7b8c9d0e1", "mailhog", "mailhog/mailhog:latest", undefined, {
    state: "exited",
    status: "Exited (0) 2 days ago",
    uptimeSeconds: 0,
    inspected: false,
  }),
]

const byName = (name: string) => DOCKER_CONTAINERS.find((c) => c.name === name)!

/** What each running container is using this second: [CPU % of one core, memory]. */
const READINGS: Record<string, [number, number]> = {
  "shop-web": [0.8, 14 * MB],
  "shop-api": [38.4, 341 * MB],
  "shop-db": [9.2, 612 * MB],
  "shop-cache": [1.4, 48 * MB],
  "monitoring-prometheus": [22.6, 890 * MB],
  "monitoring-grafana": [3.9, 184 * MB],
  "monitoring-node-exporter": [1.9, 22 * MB],
  "monitoring-uptime-kuma": [2.7, 131 * MB],
  "automation-n8n": [12.1, 418 * MB],
  portainer: [0.3, 36 * MB],
}

const HOST_MEMORY = 16 * GB

export function dockerStats(jitter = 0) {
  return Object.entries(READINGS).map(([name, [cpu, memory]], index) => {
    const c = byName(name)
    const limit = c.memoryLimit || HOST_MEMORY
    const wobble = jitter ? 1 + Math.sin(jitter + index) * 0.12 : 1
    const usage = Math.round(memory * (jitter ? 1 + Math.sin(jitter * 0.5 + index) * 0.02 : 1))
    return {
      id: c.id,
      name,
      ts: new Date().toISOString(),
      cpuPercent: cpu * wobble,
      cpuReady: true,
      memUsage: usage,
      memLimit: limit,
      memLimited: c.memoryLimit > 0,
      memPercent: (usage / limit) * 100,
      memHostPercent: (usage / HOST_MEMORY) * 100,
      hostCpus: 8,
      cpuLimit: (c as { cpuLimit?: number }).cpuLimit,
      netRx: 0,
      netTx: 0,
      blockRead: 0,
      blockWrite: 0,
      pids: 4 + index,
      onlineCpus: 8,
      cpuTotal: 0,
      systemCpu: 0,
    }
  })
}

/** An hour of shape: a slow wave around each container's reading, with a spike in the API's. */
const SPARKLINES = Object.entries(READINGS).map(([name, [cpu, memory]], index) => {
  const cpuLine = Array.from({ length: 40 }, (_, i) => {
    const wave = cpu * (1 + Math.sin(i / 4 + index) * 0.35)
    return name === "shop-api" && i > 26 && i < 31 ? cpu * 2.4 : Math.max(wave, 0.1)
  })
  return {
    name,
    cpu: cpuLine,
    mem: Array.from({ length: 40 }, (_, i) => memory * (0.92 + (i / 40) * 0.08)),
    cpuPeak: Math.max(...cpuLine),
    memPeak: memory,
  }
})

function service(name: string, stack: string, image: string) {
  const c = byName(`${stack}-${name}`)
  return {
    name,
    container: c.id,
    state: c.state,
    status: c.status,
    image,
    health: (c as { health?: string }).health,
    ports: [],
  }
}

export const DOCKER_STACKS = [
  {
    name: "shop",
    workingDir: "/srv/shop",
    configFiles: ["/srv/shop/compose.yaml"],
    services: [
      service("web", "shop", "nginx:1.27-alpine"),
      service("api", "shop", "node:22-alpine"),
      service("db", "shop", "postgres:16"),
      service("cache", "shop", "redis:7-alpine"),
      service("worker", "shop", "node:22-alpine"),
    ],
    running: 4,
    total: 5,
    managed: true,
    declared: ["web", "api", "db", "cache", "worker"],
    declaredSource: "compose",
    containers: 5,
    deployed: true,
    orphans: [],
    state: "partial",
    summary: "Partly running · 4/5 services",
  },
  {
    name: "monitoring",
    workingDir: "/srv/monitoring",
    configFiles: ["/srv/monitoring/docker-compose.yml"],
    services: [
      service("prometheus", "monitoring", "prom/prometheus:v3.1.0"),
      service("grafana", "monitoring", "grafana/grafana:11.4.0"),
      service("node-exporter", "monitoring", "prom/node-exporter:v1.8.2"),
      service("uptime-kuma", "monitoring", "louislam/uptime-kuma:1"),
    ],
    running: 4,
    total: 4,
    managed: true,
    declared: ["prometheus", "grafana", "node-exporter", "uptime-kuma"],
    declaredSource: "compose",
    containers: 4,
    deployed: true,
    orphans: [],
    state: "degraded",
    summary: "Degraded · 1 unhealthy",
  },
  {
    name: "automation",
    workingDir: "/srv/automation",
    configFiles: ["/srv/automation/compose.yaml"],
    services: [service("n8n", "automation", "n8nio/n8n:1.80.0")],
    running: 1,
    total: 1,
    managed: true,
    declared: ["n8n"],
    declaredSource: "compose",
    containers: 1,
    deployed: true,
    orphans: [],
    state: "running",
    summary: "Running · 1/1 services",
  },
  {
    name: "blog",
    workingDir: "/srv/blog",
    configFiles: ["/srv/blog/compose.yaml"],
    services: [],
    running: 0,
    total: 2,
    managed: true,
    declared: ["ghost", "mysql"],
    declaredSource: "file",
    containers: 0,
    deployed: false,
    orphans: [],
    state: "not-deployed",
    summary: "Not deployed · 2 services defined",
  },
]

export const DOCKER_DIAGNOSIS = {
  status: "critical",
  checkedAt: iso(20 * SEC),
  checked: 12,
  runtime: {
    total: 12,
    running: 10,
    exited: 2,
    created: 0,
    restarting: 0,
    paused: 0,
    dead: 0,
    removing: 0,
    healthy: 5,
    unhealthy: 1,
    starting: 0,
    noHealthcheck: 4,
    status: "critical",
    summary: "1 unhealthy, 1 exited with an error",
  },
  attention: { critical: 1, warning: 1, recommendations: 2, info: 0, issues: 2, total: 4 },
  findings: [
    // A runtime finding: Attention leaves it out, and the table reads it to
    // say why the worker stopped.
    {
      id: `container.oom.${byName("shop-worker").id}`,
      level: "critical",
      severity: "critical",
      class: "runtime",
      title: "shop-worker was killed for using too much memory",
      detail:
        "The kernel stopped this container because it asked for more memory than it was allowed — its limit was 256 MB.",
      scope: "container",
      target: "shop-worker",
      targetId: byName("shop-worker").id,
      action: "usage",
      actionLabel: "Show its memory history",
    },
    {
      id: `container.dockersock.${byName("portainer").id}`,
      level: "critical",
      severity: "critical",
      class: "security",
      title: "portainer can control Docker itself",
      detail: "The Docker socket is mounted into this container.",
      scope: "container",
      target: "portainer",
      targetId: byName("portainer").id,
    },
    {
      id: `container.exposed.${byName("shop-db").id}`,
      level: "warning",
      severity: "warning",
      class: "exposure",
      title: "shop-db publishes PostgreSQL on every interface",
      detail: "Ports of this kind are meant to be reached by the application in front of them.",
      scope: "container",
      target: "shop-db",
      targetId: byName("shop-db").id,
    },
    {
      id: `container.nomemorylimit.${byName("shop-db").id}`,
      level: "notice",
      severity: "recommendation",
      class: "configuration",
      title: "shop-db has no memory limit",
      detail: "It can use as much of this server's memory as it asks for.",
      scope: "container",
      target: "shop-db",
      targetId: byName("shop-db").id,
    },
    {
      id: `container.nohealthcheck.${byName("shop-cache").id}`,
      level: "notice",
      severity: "recommendation",
      class: "configuration",
      title: "shop-cache has no health check",
      detail: "Docker reports this container as up whenever its main process is alive.",
      scope: "container",
      target: "shop-cache",
      targetId: byName("shop-cache").id,
    },
  ],
}

function event(
  msAgo: number,
  name: string,
  action: string,
  message: string,
  level: "info" | "notice" | "error",
  over: Over = {},
) {
  const c = byName(name)
  return {
    time: iso(msAgo),
    type: "container",
    action,
    name,
    id: c.id,
    image: c.image,
    stack: c.composeStack,
    service: c.composeService,
    message,
    level,
    source:
      action === "die" || action === "oom" || action.startsWith("health") ? "daemon" : "compose",
    ...over,
  }
}

/** Newest first, as the feed answers. */
export const DOCKER_EVENTS = [
  event(12 * MIN, "shop-worker", "die", "shop-worker exited with status 137", "error", {
    exitCode: "137",
  }),
  event(12 * MIN + 2 * SEC, "shop-worker", "oom", "shop-worker ran out of memory", "error"),
  event(19 * MIN, "shop-worker", "start", "shop-worker started", "notice"),
  event(19 * MIN + 30 * SEC, "shop-worker", "die", "shop-worker exited with status 137", "error", {
    exitCode: "137",
  }),
  event(
    25 * MIN,
    "monitoring-uptime-kuma",
    "health_status: unhealthy",
    "monitoring-uptime-kuma is failing its health check",
    "error",
  ),
  event(41 * MIN, "shop-api", "start", "shop-api started", "notice"),
  event(41 * MIN + 3 * SEC, "shop-api", "die", "shop-api exited cleanly", "notice", {
    exitCode: "0",
  }),
  event(2 * HOUR, "automation-n8n", "start", "automation-n8n started", "notice"),
  event(2 * HOUR + 4 * SEC, "automation-n8n", "create", "automation-n8n was created", "info"),
  event(2 * DAY, "mailhog", "die", "mailhog exited cleanly", "notice", { exitCode: "0" }),
  event(2 * DAY + 1 * SEC, "mailhog", "stop", "mailhog was asked to stop", "notice"),
]

export const DOCKER_INFO = {
  Name: "srv-1",
  ServerVersion: "29.2.0",
  OperatingSystem: "Ubuntu 24.04.3 LTS",
  KernelVersion: "6.14.0-37-generic",
  Architecture: "x86_64",
  Driver: "overlay2",
  CgroupVersion: "2",
  NCPU: 8,
  MemTotal: HOST_MEMORY,
  Images: 23,
  Containers: 12,
  ContainersRunning: 10,
  ContainersPaused: 0,
  ContainersStopped: 2,
}

export const DOCKER_DISK = {
  layersSize: 6.4 * GB,
  imagesSize: 8.1 * GB,
  containersSize: 220 * MB,
  volumesSize: 3.2 * GB,
  buildCacheSize: 410 * MB,
  sharedLayers: 1.7 * GB,
  writable: [],
  definitions: [],
  images: { total: 23, active: 12, size: 6.4 * GB, reclaimable: 420 * MB },
  containers: { total: 12, active: 10, size: 220 * MB, reclaimable: 0 },
  volumes: { total: 9, active: 7, size: 3.2 * GB, reclaimable: 0 },
  buildCache: { total: 14, active: 0, size: 410 * MB, reclaimable: 180 * MB },
}

const user = {
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
    lastLoginAt: new Date(NOW).toISOString(),
    createdAt: new Date(NOW).toISOString(),
  },
}

const json = (route: Route, body: unknown) =>
  route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify(body) })

export type DockerHost = {
  containers?: object[]
  stacks?: object[]
  diagnosis?: object
  events?: object[]
  /** Push a fresh stats frame this often, so figures can be seen to move. */
  tick?: number
}

export type DockerMocks = {
  /** Every container action posted, as `name/action`. */
  actions: string[]
  /** Sends a new event down the open events socket. */
  emit: (event: object) => void
}

/**
 * The host behind the Docker overview: the session, the machine and its
 * metrics, the daemon's facts, the containers socket with its readings, their
 * hour, the diagnosis, the compose projects, the disk and the events feed.
 */
export async function mockDockerHost(page: Page, host: DockerHost = {}): Promise<DockerMocks> {
  const containers = host.containers ?? DOCKER_CONTAINERS
  const events = host.events ?? DOCKER_EVENTS
  const sockets: { send: (message: string) => void }[] = []
  const mocks: DockerMocks = {
    actions: [],
    emit: (event) => {
      for (const ws of sockets) ws.send(JSON.stringify({ type: "events", data: [event], ts: 0 }))
    },
  }

  await page.routeWebSocket(/\/api\/v1\/docker\/containers\/stream/, (ws) => {
    ws.send(JSON.stringify({ type: "containers", data: containers, ts: 0 }))
    ws.send(JSON.stringify({ type: "stats", data: dockerStats(), ts: 0 }))
    if (host.tick) {
      let frame = 0
      const timer = setInterval(() => {
        frame += 1
        try {
          ws.send(JSON.stringify({ type: "stats", data: dockerStats(frame), ts: 0 }))
        } catch {
          clearInterval(timer)
        }
      }, host.tick)
      ws.onClose(() => clearInterval(timer))
    }
  })
  await page.routeWebSocket(/\/api\/v1\/docker\/events\/stream/, (ws) => {
    sockets.push(ws)
    ws.send(JSON.stringify({ type: "events", data: events, ts: 0 }))
  })

  await page.route("**/api/v1/**", async (route) => {
    const url = new URL(route.request().url())
    const path = url.pathname.replace(/^\/api\/v1/, "")
    const method = route.request().method()
    switch (path) {
      case "/auth/session":
        return json(route, user)
      case "/updates/self":
        return json(route, { current: "0.7.1", latest: "0.7.1" })
      case "/system/host":
        return json(route, {
          hostname: "srv-1",
          os: "linux",
          platform: "ubuntu",
          platformVersion: "24.04",
          kernelVersion: "6.14.0",
          kernelArch: "x86_64",
          cpuModel: "AMD EPYC 7763 64-Core Processor",
          processes: 312,
        })
      case "/system/metrics":
        return json(route, {
          ts: new Date().toISOString(),
          cpu: {
            totalPercent: 14.2,
            perCore: [18, 12, 16, 11, 15, 13, 14, 15],
            loadAvg1: 1.12,
            loadAvg5: 0.94,
            loadAvg15: 0.88,
            cores: 8,
            modes: { user: 10, system: 3, iowait: 1, steal: 0, idle: 86 },
          },
          memory: {
            total: HOST_MEMORY,
            used: 5.9 * GB,
            free: 2 * GB,
            available: 9.4 * GB,
            cached: 6 * GB,
            buffers: 0,
            usedPercent: 37,
          },
          swap: { total: 0, used: 0, free: 0, usedPercent: 0 },
          mounts: [],
          net: [],
          uptimeSeconds: (9 * DAY) / 1000,
          pressure: { supported: true, cpuSome: 1, memSome: 0, ioSome: 0 },
          sockets: { tcpInUse: 40 },
          procs: { running: 4, blocked: 0, total: 312 },
        })
      case "/docker/ping":
        return json(route, { available: true, serverVersion: "29.2.0", apiVersion: "1.52" })
      case "/docker/info":
        return json(route, DOCKER_INFO)
      case "/docker/containers/":
        return json(route, containers)
      case "/docker/containers/stats/history":
        return json(route, SPARKLINES)
      case "/docker/health":
        return json(route, host.diagnosis ?? DOCKER_DIAGNOSIS)
      case "/docker/stacks/":
        return json(route, host.stacks ?? DOCKER_STACKS)
      case "/docker/disk-usage":
        return json(route, DOCKER_DISK)
      case "/docker/events":
        return json(route, {
          listening: true,
          since: iso(9 * DAY),
          buffered: events.length,
          events,
        })
    }
    const action = path.match(/^\/docker\/containers\/([^/]+)\/([a-z]+)$/)
    if (action && method === "POST") {
      const target = containers.find((c) => (c as { id: string }).id === action[1]) as
        { name: string } | undefined
      mocks.actions.push(`${target?.name ?? action[1]}/${action[2]}`)
      return json(route, {})
    }
    return route.fulfill({
      status: 503,
      contentType: "application/json",
      body: JSON.stringify({ error: { code: "not_available", message: "Not mocked" } }),
    })
  })
  return mocks
}
