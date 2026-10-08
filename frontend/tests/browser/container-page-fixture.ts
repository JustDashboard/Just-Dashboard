import type { Page, Route } from "@playwright/test"
import { mockHost } from "./host-fixture"

/**
 * One container's page on a host where it has company: n8n in a compose
 * project of five, beside its worker, its Postgres, its Redis and a task
 * runner that is crash-looping. The n8n container is the healthy case — up
 * four days, proxied, with one port published on every interface — and the
 * runner is the one an operator opens because it is broken.
 *
 * The container's own stats socket sends a frame a second, so the readings
 * move the way they do against a real daemon, and the inventory socket sends
 * the five with their stats, which is what the page's table of the stack reads.
 */

const NOW = Date.now()
const iso = (ms: number) => new Date(ms).toISOString()
const ago = (minutes: number) => iso(NOW - minutes * 60_000)
const MB = 1024 * 1024
const GB = 1024 * MB

export const N8N = "a1b2c3d4e5f60718293a4b5c6d7e8f90a1b2c3d4e5f60718293a4b5c6d7e8f90"
export const WORKER = "b2c3d4e5f60718293a4b5c6d7e8f90a1b2c3d4e5f60718293a4b5c6d7e8f90a1"
export const POSTGRES = "c3d4e5f60718293a4b5c6d7e8f90a1b2c3d4e5f60718293a4b5c6d7e8f90a1b2"
export const REDIS = "d4e5f60718293a4b5c6d7e8f90a1b2c3d4e5f60718293a4b5c6d7e8f90a1b2c3"
export const RUNNER = "e5f60718293a4b5c6d7e8f90a1b2c3d4e5f60718293a4b5c6d7e8f90a1b2c3d4"
export const STANDALONE = "f60718293a4b5c6d7e8f90a1b2c3d4e5f60718293a4b5c6d7e8f90a1b2c3d4e5"

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
    lastLoginAt: iso(NOW),
    createdAt: iso(NOW),
  },
}

const compose = (service: string) => ({
  "com.docker.compose.project": "automations",
  "com.docker.compose.service": service,
  "com.docker.compose.project.working_dir": "/srv/automations",
})

/** A container as the listing sends it; the fixture only names the fields it varies. */
type Listed = {
  id: string
  name: string
  image: string
  state: string
  composeStack?: string
  composeService?: string
  memoryLimit?: number
  cpuLimit?: number
  restartPolicy?: string
  [field: string]: unknown
}

const base = {
  imageId: "sha256:0f1e2d3c4b5a",
  ports: [],
  hasHealthcheck: false,
  inspected: true,
  composeStack: "automations",
  networks: ["automations_default"],
  exposure: [],
}

export const containers: Listed[] = [
  {
    ...base,
    id: N8N,
    names: ["automations-n8n-1"],
    name: "automations-n8n-1",
    image: "docker.n8n.io/n8nio/n8n:1.98.2",
    command: "tini -- /docker-entrypoint.sh",
    state: "running",
    status: "Up 4 days (healthy)",
    health: "healthy",
    hasHealthcheck: true,
    createdAt: ago(6 * 24 * 60),
    startedAt: ago(4 * 24 * 60 + 6 * 60 + 12),
    uptimeSeconds: (4 * 24 * 60 + 6 * 60 + 12) * 60,
    labels: compose("n8n"),
    composeService: "n8n",
    networks: ["automations_default", "proxy"],
    memoryLimit: 2 * GB,
    cpuLimit: 2,
    restartPolicy: "unless-stopped",
    exposure: [
      {
        hostIp: "127.0.0.1",
        hostPort: 5678,
        containerPort: 5678,
        protocol: "tcp",
        scope: "loopback",
        label: "This server only",
        summary: "Bound to 127.0.0.1:5678. Only processes on this server can reach it.",
      },
      {
        hostIp: "0.0.0.0",
        hostPort: 9464,
        containerPort: 9464,
        protocol: "tcp",
        scope: "all",
        label: "Every interface",
        summary: "Bound to every interface on port 9464.",
      },
    ],
  },
  {
    ...base,
    id: WORKER,
    names: ["automations-worker-1"],
    name: "automations-worker-1",
    image: "docker.n8n.io/n8nio/n8n:1.98.2",
    command: "tini -- /docker-entrypoint.sh worker",
    state: "running",
    status: "Up 4 days",
    createdAt: ago(6 * 24 * 60),
    startedAt: ago(4 * 24 * 60 + 6 * 60),
    uptimeSeconds: (4 * 24 * 60 + 6 * 60) * 60,
    labels: compose("worker"),
    composeService: "worker",
    memoryLimit: 2 * GB,
    restartPolicy: "unless-stopped",
  },
  {
    ...base,
    id: POSTGRES,
    names: ["automations-postgres-1"],
    name: "automations-postgres-1",
    image: "postgres:16-alpine",
    command: "docker-entrypoint.sh postgres",
    state: "running",
    status: "Up 6 days (healthy)",
    health: "healthy",
    hasHealthcheck: true,
    createdAt: ago(6 * 24 * 60),
    startedAt: ago(6 * 24 * 60),
    uptimeSeconds: 6 * 24 * 3600,
    labels: compose("postgres"),
    composeService: "postgres",
    memoryLimit: 0,
    restartPolicy: "unless-stopped",
  },
  {
    ...base,
    id: REDIS,
    names: ["automations-redis-1"],
    name: "automations-redis-1",
    image: "redis:7-alpine",
    command: "docker-entrypoint.sh redis-server",
    state: "running",
    status: "Up 6 days",
    createdAt: ago(6 * 24 * 60),
    startedAt: ago(6 * 24 * 60),
    uptimeSeconds: 6 * 24 * 3600,
    labels: compose("redis"),
    composeService: "redis",
    memoryLimit: 256 * MB,
    restartPolicy: "unless-stopped",
  },
  {
    ...base,
    id: RUNNER,
    names: ["automations-runner-1"],
    name: "automations-runner-1",
    image: "n8nio/runners:1.98.2",
    command: "/usr/local/bin/task-runner-launcher javascript",
    state: "restarting",
    status: "Restarting (1) 8 seconds ago",
    createdAt: ago(6 * 24 * 60),
    uptimeSeconds: 0,
    labels: compose("runner"),
    composeService: "runner",
    memoryLimit: 512 * MB,
    restartPolicy: "always",
  },
]

const standalone: Listed = {
  ...base,
  id: STANDALONE,
  names: ["uptime-kuma"],
  name: "uptime-kuma",
  image: "louislam/uptime-kuma:1.23.16",
  command: "/usr/bin/dumb-init -- extra/entrypoint.sh",
  state: "running",
  status: "Up 12 days (healthy)",
  health: "healthy",
  hasHealthcheck: true,
  createdAt: ago(30 * 24 * 60),
  startedAt: ago(12 * 24 * 60),
  uptimeSeconds: 12 * 24 * 3600,
  labels: {},
  composeStack: undefined,
  networks: ["bridge"],
  memoryLimit: 0,
  restartPolicy: "always",
}

const inventory = [...containers, standalone]

const ENV = [
  "PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
  "NODE_ENV=production",
  "N8N_HOST=automations.example.test",
  "N8N_PROTOCOL=https",
  "WEBHOOK_URL=https://automations.example.test/",
  "GENERIC_TIMEZONE=Europe/Chisinau",
  "DB_TYPE=postgresdb",
  "DB_POSTGRESDB_HOST=postgres",
  "DB_POSTGRESDB_DATABASE=n8n",
  "DB_POSTGRESDB_USER=n8n",
  "DB_POSTGRESDB_PASSWORD=correct-horse-battery",
  "N8N_ENCRYPTION_KEY=6f1d0c7a9b3e4f21",
  "QUEUE_BULL_REDIS_HOST=redis",
  "EXECUTIONS_MODE=queue",
  "N8N_METRICS=true",
  "N8N_RUNNERS_ENABLED=true",
  "N8N_RUNNERS_AUTH_TOKEN=runner-shared-secret",
]

function detailOf(container: Listed) {
  const n8n = container.id === N8N
  const runner = container.id === RUNNER
  return {
    ...container,
    env: n8n
      ? ENV
      : ["PATH=/usr/local/bin:/usr/bin:/bin", "N8N_RUNNERS_TASK_BROKER_URI=http://n8n:5679"],
    mounts: n8n
      ? [
          {
            type: "volume",
            name: "automations_n8n_data",
            source: "/var/lib/docker/volumes/automations_n8n_data/_data",
            destination: "/home/node/.n8n",
            mode: "z",
            rw: true,
          },
          {
            type: "bind",
            source: "/srv/automations/files",
            destination: "/files",
            mode: "",
            rw: true,
          },
          {
            type: "bind",
            source: "/etc/localtime",
            destination: "/etc/localtime",
            mode: "ro",
            rw: false,
          },
          { type: "tmpfs", source: "", destination: "/tmp", mode: "", rw: true },
        ]
      : [],
    networkMode: "automations_default",
    networkDetails: (n8n ? ["automations_default", "proxy"] : ["automations_default"]).map(
      (name, index) => ({
        name,
        ipAddress: name === "proxy" ? "172.18.0.7" : `172.20.0.${index + 4}`,
        gateway: name === "proxy" ? "172.18.0.1" : "172.20.0.1",
        macAddress: "02:42:ac:14:00:04",
        aliases: [container.composeService ?? container.name, container.id.slice(0, 12)],
        networkId: `net-${name}`,
      }),
    ),
    restartPolicy: container.restartPolicy ?? "no",
    privileged: false,
    capAdd: [],
    logPath: `/var/lib/docker/containers/${container.id}/${container.id}-json.log`,
    exitCode: runner ? 1 : 0,
    restartCount: runner ? 23 : n8n ? 1 : 0,
    // As inspect reports them: the entrypoint, and the command handed to it apart.
    command: n8n ? "" : runner ? "javascript" : container.command,
    entrypoint: n8n
      ? ["tini", "--", "/docker-entrypoint.sh"]
      : runner
        ? ["/usr/local/bin/task-runner-launcher"]
        : [],
    workingDir: n8n ? "/home/node" : "/",
    user: n8n ? "node" : "runner",
  }
}

const failures: Record<string, unknown> = {
  [N8N]: {
    containerId: N8N,
    name: "automations-n8n-1",
    checkedAt: ago(0),
    state: "running",
    headline: "Running for 4 days without a restart.",
    confidence: "observed",
    evidence: [],
    restarts: { count: 1, recent: 0, looping: false },
    suggestions: [],
  },
  [RUNNER]: {
    containerId: RUNNER,
    name: "automations-runner-1",
    checkedAt: ago(0),
    state: "looping",
    headline: "Restart loop: 23 starts in the last 19 minutes.",
    likely:
      "It exits with status 1 about two seconds after starting — it cannot reach the task broker it was told to use.",
    confidence: "inferred",
    evidence: [
      { label: "Exit code", value: "1 on every exit", source: "docker events", weight: "decisive" },
      {
        label: "Ran for",
        value: "about 2 s each time",
        source: "docker events",
        weight: "supporting",
      },
      {
        label: "Last line",
        value: "Error: connect ECONNREFUSED 172.20.0.4:5679",
        source: "container log",
        weight: "supporting",
      },
    ],
    restarts: { count: 23, recent: 23, looping: true, window: "19m", summary: "23 starts in 19m" },
    suggestions: [
      "Check that n8n is started with N8N_RUNNERS_ENABLED and listens on the broker port.",
    ],
    logWindow: {
      since: ago(2),
      until: ago(1),
      reason: "the minute around the most recent exit",
    },
  },
}

const routes: Record<string, unknown[]> = {
  [N8N]: [
    {
      hostIp: "127.0.0.1",
      hostPort: 5678,
      containerPort: 5678,
      protocol: "tcp",
      public: false,
      scope: "loopback",
      label: "This server only",
      binding: "127.0.0.1:5678",
      vhost: "automations.example.test",
      url: "https://automations.example.test",
      tls: true,
      firewall: { known: true, backend: "ufw", enabled: true, verdict: "default" },
      reach: "proxied",
      reasoning: "Bound to loopback; Caddy forwards automations.example.test to it over HTTPS.",
      inferred: false,
    },
    {
      hostIp: "0.0.0.0",
      hostPort: 9464,
      containerPort: 9464,
      protocol: "tcp",
      public: true,
      scope: "all",
      label: "Every interface",
      binding: "0.0.0.0:9464",
      firewall: {
        known: true,
        backend: "ufw",
        enabled: true,
        verdict: "denied",
        rule: "deny 9464/tcp",
        dockerBypass: true,
      },
      reach: "external",
      reasoning:
        "Bound to every interface, and Docker's NAT rule is consulted before ufw's deny, so it answers from anywhere.",
      inferred: true,
    },
  ],
}

const diagnosis = {
  status: "critical",
  checkedAt: ago(0),
  checked: 6,
  runtime: {
    total: 6,
    running: 5,
    exited: 0,
    created: 0,
    restarting: 1,
    paused: 0,
    dead: 0,
    removing: 0,
    healthy: 3,
    unhealthy: 0,
    starting: 0,
    noHealthcheck: 2,
    status: "critical",
    summary: "1 restarting",
  },
  attention: { critical: 1, warning: 1, recommendations: 1, info: 0, issues: 2, total: 3 },
  findings: [
    {
      id: `container.restarting.${RUNNER}`,
      level: "critical",
      severity: "critical",
      class: "runtime",
      title: "automations-runner-1 is restarting in a loop",
      detail: "It has exited and been restarted 23 times in 19 minutes.",
      scope: "container",
      target: "automations-runner-1",
      targetId: RUNNER,
      action: "logs",
      actionLabel: "Read its logs",
    },
    {
      id: `container.exposed.${N8N}`,
      level: "warning",
      severity: "warning",
      class: "exposure",
      title: "automations-n8n-1 publishes port 9464 on every interface",
      detail:
        "Docker's NAT rule answers before the firewall's deny, so the metrics port is reachable from anywhere.",
      scope: "container",
      target: "automations-n8n-1",
      targetId: N8N,
    },
    {
      id: `container.nohealthcheck.${RUNNER}`,
      level: "notice",
      severity: "recommendation",
      class: "configuration",
      title: "automations-runner-1 has no health check",
      detail: "Docker reports this container as up whenever its main process is alive.",
      scope: "container",
      target: "automations-runner-1",
      targetId: RUNNER,
    },
  ],
}

/** How the server words an event the fixture does not word itself. */
const SAID: Record<string, string> = {
  start: "started",
  create: "was created",
  die: "exited",
}

function eventsOf(id: string) {
  const container = inventory.find((one) => one.id === id)!
  const ev = (minutes: number, action: string, extra: Record<string, unknown> = {}) => ({
    time: ago(minutes),
    type: "container",
    action,
    name: container.name,
    id,
    image: container.image,
    stack: container.composeStack,
    service: container.composeService,
    message: `${container.name} ${SAID[action] ?? action}`,
    level: action === "die" ? "error" : action.startsWith("health_status") ? "notice" : "info",
    source: "daemon",
    ...extra,
  })
  if (id === RUNNER) {
    const loop = []
    for (let i = 0; i < 23; i++) {
      const at = 19 - i * 0.8
      loop.push(ev(at, "start"))
      loop.push(
        ev(at - 0.04, "die", {
          exitCode: "1",
          message: `${container.name} exited with status 1`,
        }),
      )
    }
    return [...loop.reverse(), ev(6 * 24 * 60, "create")]
  }
  if (id === N8N) {
    return [
      ev(3, "exec_start", { message: "wget --spider http://localhost:5678/healthz" }),
      ev(4 * 24 * 60 + 6 * 60 + 11.5, "health_status: healthy", {
        message: `${container.name} is healthy`,
      }),
      ev(4 * 24 * 60 + 6 * 60 + 12, "start", {
        source: "dashboard",
        trigger: {
          auditId: 412,
          action: "docker.container.restart",
          actor: "operator",
          confidence: "likely",
        },
      }),
      ev(4 * 24 * 60 + 6 * 60 + 12.1, "die", {
        exitCode: "0",
        message: `${container.name} exited with status 0`,
        source: "dashboard",
      }),
      ev(6 * 24 * 60 - 1, "start"),
      ev(6 * 24 * 60, "create"),
      ev(6 * 24 * 60 + 2, "pull", { message: "Pulled docker.n8n.io/n8nio/n8n:1.98.2" }),
    ]
  }
  return [ev(6 * 24 * 60 - 1, "start"), ev(6 * 24 * 60, "create")]
}

/** The shape of one container's last hours, for its readings and its charts. */
function historyOf(id: string, url: URL) {
  const container = inventory.find((one) => one.id === id)!
  const range = url.searchParams.get("range") ?? "1h"
  const hours = range.endsWith("d") ? Number(range.slice(0, -1)) * 24 : Number(range.slice(0, -1))
  const step = Math.max(15, Math.round((hours * 3600) / 120))
  const count = Math.round((hours * 3600) / step)
  const limit = container.memoryLimit || 0
  const points = Array.from({ length: count }, (_, i) => {
    const t = NOW - (count - i) * step * 1000
    const wave = Math.sin(i / 6) * 0.5 + Math.sin(i / 2.3) * 0.3
    const burst = i % 37 > 33 ? 1 : 0
    const cpu = id === RUNNER ? null : Math.max(1, 18 + wave * 9 + burst * 40)
    const memBytes = (id === N8N ? 640 : 120) * MB + Math.sin(i / 9) * 40 * MB + i * 0.2 * MB
    return {
      ts: iso(t),
      samples: 10,
      cpu,
      cpuPeak: cpu === null ? null : cpu + 12,
      mem: limit ? (memBytes / limit) * 100 : 0,
      memPeak: limit ? ((memBytes + 30 * MB) / limit) * 100 : 0,
      memBytes,
      memBytesPeak: memBytes + 30 * MB,
      memLimit: limit,
      pids: 23,
      pidsPeak: 25,
      netRx: 40_000 + wave * 25_000 + burst * 300_000,
      netTx: 22_000 + wave * 12_000 + burst * 90_000,
      blockRead: 2_000 + burst * 80_000,
      blockWrite: 12_000 + wave * 6_000,
    }
  })
  return {
    name: container.name,
    from: iso(NOW - hours * 3_600_000),
    to: iso(NOW),
    stepSeconds: step,
    sampleIntervalSeconds: 15,
    retentionSeconds: 7 * 86_400,
    earliest: ago(6 * 24 * 60),
    points,
  }
}

/** One frame of a container's live readings, `tick` seconds into the socket. */
function statOf(id: string, tick: number) {
  const container = inventory.find((one) => one.id === id)!
  const limit = container.memoryLimit || 0
  const profile: Record<string, { cpu: number; mem: number; net: number }> = {
    [N8N]: { cpu: 21, mem: 684, net: 52_000 },
    [WORKER]: { cpu: 46, mem: 912, net: 18_000 },
    [POSTGRES]: { cpu: 7, mem: 418, net: 64_000 },
    [REDIS]: { cpu: 1.4, mem: 22, net: 9_000 },
    [STANDALONE]: { cpu: 3, mem: 141, net: 4_000 },
  }
  const shape = profile[id] ?? { cpu: 0, mem: 0, net: 0 }
  const wave = Math.sin(tick / 1.7) * 0.35 + Math.sin(tick / 4.1) * 0.25
  const cpu = Math.max(0.2, shape.cpu * (1 + wave))
  const memUsage = shape.mem * MB * (1 + wave * 0.02)
  const elapsed = 4 * 86_400 + tick
  // Cumulative, so never falling: tick + sin(tick / 1.7) only ever rises.
  const received = shape.net * elapsed + shape.net * 0.6 * (tick + Math.sin(tick / 1.7))
  const sent = received * 0.42
  return {
    id,
    name: container.name,
    ts: iso(NOW + tick * 1000),
    cpuPercent: cpu,
    cpuReady: tick > 0,
    cpuTotal: 1e9 * elapsed,
    systemCpu: 8e9 * elapsed,
    hostCpus: 8,
    onlineCpus: 8,
    cpuLimit: container.cpuLimit,
    cpuPeriods: tick * 10,
    cpuThrottledPeriods: 0,
    memUsage,
    memRaw: memUsage + 96 * MB,
    memCache: 96 * MB,
    memRss: memUsage * 0.82,
    memSwap: null,
    memLimit: limit || 16 * GB,
    memLimited: limit > 0,
    memPercent: limit ? (memUsage / limit) * 100 : (memUsage / (16 * GB)) * 100,
    memHostPercent: (memUsage / (16 * GB)) * 100,
    pids: id === N8N ? 23 : 9,
    pidsLimit: id === N8N ? 512 : undefined,
    netRx: received,
    netTx: sent,
    networkAvailable: true,
    blockAvailable: true,
    blockRead: 380 * MB + tick * 4_000,
    blockWrite: 1.2 * GB + 14_000 * (tick + Math.sin(tick / 1.3)),
    networks: {
      eth0: {
        rxBytes: received * 0.7,
        txBytes: sent * 0.7,
        rxPackets: received / 900,
        txPackets: sent / 700,
        rxErrors: 0,
        txErrors: 0,
        rxDropped: 0,
        txDropped: 0,
      },
      eth1: {
        rxBytes: received * 0.3,
        txBytes: sent * 0.3,
        rxPackets: received / 2000,
        txPackets: sent / 1600,
        rxErrors: 0,
        txErrors: 0,
        rxDropped: 0,
        txDropped: 0,
      },
    },
  }
}

async function json(route: Route, body: unknown) {
  await route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify(body) })
}

const notMocked = (route: Route) =>
  route.fulfill({
    status: 503,
    contentType: "application/json",
    body: JSON.stringify({ error: { code: "not_available", message: "Not mocked" } }),
  })

/**
 * Everything one container's page reads, over the Metrics page's mocked host
 * for the shell. `frames` is how many live frames each socket sends, one a
 * second; a screenshot wants a few so the trends have a shape.
 */
export async function mockContainerPage(page: Page, options: { frames?: number } = {}) {
  const frames = options.frames ?? 600
  await mockHost(page)

  await page.routeWebSocket(/\/api\/v1\/docker\/containers\/stream/, (socket) => {
    socket.send(JSON.stringify({ type: "containers", data: inventory }))
    let tick = 1
    const send = () =>
      socket.send(
        JSON.stringify({
          type: "stats",
          data: inventory
            .filter((one) => one.state === "running")
            .map((one) => statOf(one.id, tick)),
        }),
      )
    send()
    const timer = setInterval(() => {
      tick++
      if (tick > frames) return clearInterval(timer)
      send()
    }, 1000)
    socket.onClose(() => clearInterval(timer))
  })
  await page.routeWebSocket(/\/api\/v1\/docker\/containers\/[0-9a-f]+\/stats\/stream/, (socket) => {
    const id = new URL(socket.url()).pathname.split("/").at(-3)!
    let tick = 0
    socket.send(JSON.stringify({ type: "stats", data: statOf(id, tick) }))
    const timer = setInterval(() => {
      tick++
      if (tick > frames) return clearInterval(timer)
      socket.send(JSON.stringify({ type: "stats", data: statOf(id, tick) }))
    }, 1000)
    socket.onClose(() => clearInterval(timer))
  })
  await page.routeWebSocket(/\/api\/v1\/docker\/events\/stream/, () => {})
  await page.routeWebSocket(/\/api\/v1\/logs\/stream/, () => {})

  await page.route("**/api/v1/**", async (route) => {
    const url = new URL(route.request().url())
    const path = url.pathname.replace(/^\/api\/v1/, "")
    if (path === "/auth/session") return json(route, user)
    if (path === "/dashboard/update") return json(route, { current: "0.7.1", latest: "0.7.1" })
    if (path === "/docker/ping") return json(route, { available: true, serverVersion: "28.4.0" })
    if (path === "/docker/health") return json(route, diagnosis)
    if (path === "/docker/containers/") return json(route, inventory)
    if (path === "/docker/containers/stats/history") {
      return json(
        route,
        inventory.map((one) => {
          const points = historyOf(one.id, url).points.slice(-40)
          return {
            name: one.name,
            cpu: points.map((p) => p.cpu ?? 0),
            mem: points.map((p) => p.mem),
            cpuPeak: Math.max(...points.map((p) => p.cpuPeak ?? 0)),
            memPeak: Math.max(...points.map((p) => p.memPeak)),
          }
        }),
      )
    }
    if (path === "/docker/events") {
      const id = url.searchParams.get("container") ?? ""
      return json(route, {
        listening: true,
        since: ago(7 * 24 * 60),
        buffered: 40,
        events: inventory.some((one) => one.id === id) ? eventsOf(id) : [],
      })
    }
    if (path === "/files/list") {
      // What n8n keeps in its volume, wherever in the mounts the browser asks.
      const at = url.searchParams.get("path") ?? "/"
      const file = (name: string, isDir: boolean, size = 0) => ({
        name,
        path: `${at}/${name}`,
        size,
        mode: isDir ? "drwxr-xr-x" : "-rw-r--r--",
        modeOctal: isDir ? "0755" : "0644",
        isDir,
        isSymlink: false,
        modified: ago(42),
        owner: "node",
        group: "node",
        uid: 1000,
        gid: 1000,
      })
      return json(route, {
        path: at,
        parent: at.slice(0, at.lastIndexOf("/")) || "/",
        roots: ["/"],
        entries: [
          file("binaryData", true),
          file("nodes", true),
          file("config", false, 214),
          file("database.sqlite", false, 41 * MB),
          file("n8nEventLog.log", false, 3 * MB),
        ],
      })
    }
    if (path === "/system/metrics/events") return json(route, [])
    if (path.startsWith("/logs/")) {
      if (path === "/logs/source") {
        return json(route, {
          id: url.searchParams.get("source"),
          kind: "docker",
          status: "running",
        })
      }
      if (path === "/logs/search") return json(route, { lines: [], scanned: 0, matched: 0 })
      return json(route, {})
    }
    const match = path.match(/^\/docker\/containers\/([0-9a-f]+)(\/.*)?$/)
    if (match) {
      const [, id, rest = ""] = match
      const container = inventory.find((one) => one.id === id)
      if (!container) return notMocked(route)
      if (rest === "") return json(route, detailOf(container))
      if (rest === "/failure")
        return json(
          route,
          failures[id] ?? {
            ...(failures[N8N] as object),
            containerId: id,
            name: container.name,
            restarts: { count: 0, recent: 0, looping: false },
          },
        )
      if (rest === "/routes") return json(route, routes[id] ?? [])
      if (rest === "/changes")
        return json(route, id === N8N ? [{ path: "/home/node/.cache/n8n", kind: "added" }] : [])
      if (rest === "/anomalies") return json(route, { anomalies: [] })
      if (rest === "/stats/history") return json(route, historyOf(id, url))
      if (rest === "/raw")
        return json(route, {
          Id: id,
          Name: `/${container.name}`,
          Config: { Image: container.image, Env: detailOf(container).env },
          State: { Status: container.state },
        })
      return notMocked(route)
    }
    if (path.startsWith("/docker/")) return notMocked(route)
    return route.fallback()
  })
}
