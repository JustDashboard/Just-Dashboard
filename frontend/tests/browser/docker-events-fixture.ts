import type { Page, Route } from "@playwright/test"

/**
 * A host whose Docker record has something in it worth reading: a database in
 * a restart loop for the last few minutes, a worker the kernel killed for
 * memory, an API somebody restarted from this dashboard, a container started
 * from a shell nobody can name, compose bringing monitoring up, and a quiet
 * cache that only ever started. It is what the Events page is for, and an
 * empty buffer — which is all the Docker suite mocked — renders none of it.
 */

const NOW = Date.now()
const MIN = 60_000
const HOUR = 60 * MIN

function at(msAgo: number) {
  return new Date(NOW - msAgo).toISOString()
}

const ID = {
  web: "a1".repeat(32),
  api: "b2".repeat(32),
  db: "c3".repeat(32),
  redis: "d4".repeat(32),
  worker: "e5".repeat(32),
  grafana: "f6".repeat(32),
  kuma: "a7".repeat(32),
  migrate: "b8".repeat(32),
}

type Source = "dashboard" | "compose" | "daemon" | "docker"

type Event = {
  time: string
  type: string
  action: string
  name: string
  id?: string
  image?: string
  stack?: string
  service?: string
  exitCode?: string
  message: string
  level: "info" | "notice" | "error"
  source: Source
  trigger?: { auditId: number; action: string; actor: string; confidence: string }
}

function container(
  key: keyof typeof ID,
  name: string,
  image: string,
  compose?: { stack: string; service: string },
) {
  const base = { type: "container", name, id: ID[key], image, ...compose }
  const own: Source = compose ? "compose" : "docker"
  return {
    create: (ago: number, source: Source = own): Event => ({
      ...base,
      time: at(ago),
      action: "create",
      message: `${name} was created`,
      level: "info",
      source,
    }),
    start: (ago: number, source: Source = own): Event => ({
      ...base,
      time: at(ago),
      action: "start",
      message: `${name} started`,
      level: "notice",
      source,
    }),
    stop: (ago: number, source: Source = own): Event => ({
      ...base,
      time: at(ago),
      action: "stop",
      message: `${name} was asked to stop`,
      level: "notice",
      source,
    }),
    kill: (ago: number, source: Source = own): Event => ({
      ...base,
      time: at(ago),
      action: "kill",
      message: `${name} was killed`,
      level: "notice",
      source,
    }),
    restart: (ago: number, source: Source = own): Event => ({
      ...base,
      time: at(ago),
      action: "restart",
      message: `${name} restarted`,
      level: "notice",
      source,
    }),
    destroy: (ago: number, source: Source = own): Event => ({
      ...base,
      time: at(ago),
      action: "destroy",
      message: `${name} was removed`,
      level: "notice",
      source,
    }),
    die: (ago: number, code: string): Event => ({
      ...base,
      time: at(ago),
      action: "die",
      exitCode: code,
      message: code === "0" ? `${name} exited cleanly` : `${name} exited with status ${code}`,
      level: code === "0" ? "notice" : "error",
      source: "daemon",
    }),
    oom: (ago: number): Event => ({
      ...base,
      time: at(ago),
      action: "oom",
      message: `${name} ran out of memory`,
      level: "error",
      source: "daemon",
    }),
    health: (ago: number, healthy: boolean): Event => ({
      ...base,
      time: at(ago),
      action: `health_status: ${healthy ? "healthy" : "unhealthy"}`,
      message: healthy ? `${name} is healthy again` : `${name} is failing its health check`,
      level: healthy ? "notice" : "error",
      source: "daemon",
    }),
  }
}

const web = container("web", "shop-web-1", "nginx:1.27-alpine", { stack: "shop", service: "web" })
const api = container("api", "shop-api-1", "node:22-alpine", { stack: "shop", service: "api" })
const db = container("db", "shop-db-1", "postgres:16", { stack: "shop", service: "db" })
const redis = container("redis", "redis", "redis:7-alpine")
const worker = container("worker", "worker", "python:3.12-slim")
const grafana = container("grafana", "monitoring-grafana-1", "grafana/grafana:11.2.0", {
  stack: "monitoring",
  service: "grafana",
})
const kuma = container("kuma", "uptime-kuma", "louislam/uptime-kuma:1")
const migrate = container("migrate", "shop-migrate-1", "node:22-alpine", {
  stack: "shop",
  service: "migrate",
})

function byDashboard(event: Event, auditId: number, actor: string, action: string): Event {
  return {
    ...event,
    source: "dashboard",
    trigger: { auditId, action, actor, confidence: "likely" },
  }
}

function other(
  ago: number,
  type: string,
  action: string,
  name: string,
  message: string,
  source: Source = "docker",
  level: Event["level"] = "info",
): Event {
  return { time: at(ago), type, action, name, message, level, source }
}

/** The database's last seven minutes: exit 1, restart policy, again. */
function dbLoop(): Event[] {
  const out: Event[] = []
  for (let i = 0; i < 8; i++) {
    const ago = 30_000 + i * 50_000
    out.push(db.die(ago + 2_000, "1"), db.start(ago, "daemon"))
  }
  out.push(db.health(7.5 * MIN, false))
  return out
}

export const EVENTS: Event[] = [
  ...dbLoop(),
  // A restart pressed in this dashboard: kill, die, stop, start, restart.
  byDashboard(api.kill(41 * MIN), 812, "wayy", "docker.container.restart"),
  api.die(41 * MIN - 400, "143"),
  byDashboard(api.stop(41 * MIN - 600), 812, "wayy", "docker.container.restart"),
  byDashboard(api.start(41 * MIN - 1_400), 812, "wayy", "docker.container.restart"),
  byDashboard(api.restart(41 * MIN - 1_500), 812, "wayy", "docker.container.restart"),
  // The kernel took the worker for memory, and nothing brought it back.
  worker.oom(25 * MIN),
  worker.die(25 * MIN - 300, "137"),
  // A one-off migration compose ran and removed.
  migrate.create(63 * MIN),
  migrate.start(63 * MIN - 800),
  migrate.die(62 * MIN, "0"),
  migrate.destroy(61 * MIN),
  // Grafana failed its check for five minutes and recovered.
  grafana.health(2 * HOUR + 10 * MIN, false),
  grafana.health(2 * HOUR + 5 * MIN, true),
  // Monitoring brought up by compose from a shell.
  other(3 * HOUR, "image", "pull", "grafana/grafana:11.2.0", "pulled grafana/grafana:11.2.0"),
  other(
    3 * HOUR - 20_000,
    "network",
    "create",
    "monitoring_default",
    "created network monitoring_default",
  ),
  grafana.create(3 * HOUR - 30_000),
  grafana.start(3 * HOUR - 32_000),
  // Somebody ran `docker run` on the host: nothing in the audit log names it.
  other(5 * HOUR, "image", "pull", "louislam/uptime-kuma:1", "pulled louislam/uptime-kuma:1"),
  other(5 * HOUR - 10_000, "volume", "create", "kuma-data", "created volume kuma-data"),
  kuma.create(5 * HOUR - 20_000),
  kuma.start(5 * HOUR - 22_000),
  // The web server restarted from this dashboard earlier in the day.
  byDashboard(web.stop(7 * HOUR), 790, "ana", "docker.container.restart"),
  web.die(7 * HOUR - 500, "0"),
  byDashboard(web.start(7 * HOUR - 1_500), 790, "ana", "docker.container.restart"),
  byDashboard(web.restart(7 * HOUR - 1_600), 790, "ana", "docker.container.restart"),
  // Yesterday's deploy of the shop.
  other(20 * HOUR, "image", "pull", "postgres:16", "pulled postgres:16", "docker", "notice"),
  db.create(20 * HOUR - 30_000),
  db.start(20 * HOUR - 32_000),
  api.create(20 * HOUR - 40_000),
  api.start(20 * HOUR - 42_000),
  web.create(20 * HOUR - 50_000),
  web.start(20 * HOUR - 52_000),
  redis.start(22 * HOUR),
  worker.start(23 * HOUR),
].sort((a, b) => Date.parse(b.time) - Date.parse(a.time))

/** The containers Docker lists now: the migration is gone, the worker is down. */
export const CONTAINERS = [
  live("web", "shop-web-1", "nginx:1.27-alpine", "running", 7 * HOUR, "healthy", ["shop", "web"]),
  live("api", "shop-api-1", "node:22-alpine", "running", 41 * MIN, undefined, ["shop", "api"]),
  live("db", "shop-db-1", "postgres:16", "restarting", 0, "unhealthy", ["shop", "db"]),
  live("redis", "redis", "redis:7-alpine", "running", 22 * HOUR),
  live("worker", "worker", "python:3.12-slim", "exited", 0, undefined, undefined, "137"),
  live(
    "grafana",
    "monitoring-grafana-1",
    "grafana/grafana:11.2.0",
    "running",
    3 * HOUR,
    "healthy",
    ["monitoring", "grafana"],
  ),
  live("kuma", "uptime-kuma", "louislam/uptime-kuma:1", "running", 5 * HOUR, "healthy"),
]

function live(
  key: keyof typeof ID,
  name: string,
  image: string,
  state: string,
  upMs: number,
  health?: string,
  compose?: [string, string],
  exit?: string,
) {
  return {
    id: ID[key],
    names: [name],
    name,
    image,
    imageId: `sha256:${key}`,
    command: "",
    state,
    status:
      state === "running"
        ? "Up"
        : state === "restarting"
          ? "Restarting (1) 20 seconds ago"
          : `Exited (${exit}) 25 minutes ago`,
    health,
    createdAt: at(20 * HOUR),
    startedAt: state === "running" ? at(upMs) : undefined,
    uptimeSeconds: Math.round(upMs / 1000),
    ports: [],
    labels: compose
      ? { "com.docker.compose.project": compose[0], "com.docker.compose.service": compose[1] }
      : {},
    networks: ["bridge"],
    composeStack: compose?.[0],
    composeService: compose?.[1],
    exposure: [],
    hasHealthcheck: Boolean(health),
    restartPolicy: key === "db" ? "always" : "unless-stopped",
    inspected: state === "running",
  }
}

export const SINCE = at(26 * HOUR)

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
    username: "wayy",
    role: "admin",
    totpEnabled: true,
    disabled: false,
    mustChangePassword: false,
    lastLoginAt: at(0),
    createdAt: at(0),
  },
}

async function json(route: Route, body: unknown) {
  await route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify(body) })
}

export type EventsMock = {
  /** Every `/docker/events` the page asked for, as its query. */
  asked: URLSearchParams[]
  /** Sends events down the open stream, as the daemon emitting them would. */
  emit: (events: Event[]) => void
}

/**
 * The Events page's API: the buffer, the stream that replays it and then
 * follows the daemon, and the containers Docker lists now.
 */
export async function mockEvents(
  page: Page,
  options: { events?: Event[]; containers?: unknown[]; listening?: boolean } = {},
): Promise<EventsMock> {
  const events = options.events ?? EVENTS
  const asked: URLSearchParams[] = []
  const sockets: { send: (message: string) => void }[] = []

  await page.routeWebSocket(/\/api\/v1\/docker\/events\/stream/, (socket) => {
    sockets.push(socket)
    // The stream replays the buffer without the audit log laid against it.
    const replay = events.slice(0, 200).map((e) => ({ ...e, trigger: undefined }))
    socket.send(JSON.stringify({ type: "events", data: replay }))
  })
  await page.routeWebSocket(/\/api\/v1\/docker\/containers\/stream/, (socket) => {
    socket.send(JSON.stringify({ type: "containers", data: options.containers ?? CONTAINERS }))
  })

  await page.route("**/api/v1/**", async (route) => {
    const url = new URL(route.request().url())
    const path = url.pathname.replace(/^\/api\/v1/, "")
    switch (path) {
      case "/auth/session":
        return json(route, user)
      case "/dashboard/update":
        return json(route, { current: "0.7.1", latest: "0.7.1" })
      case "/docker/ping":
        return json(route, { available: true, serverVersion: "28.5.1" })
      case "/docker/containers/":
        return json(route, options.containers ?? CONTAINERS)
      case "/docker/events": {
        asked.push(url.searchParams)
        const limit = Number(url.searchParams.get("limit") ?? 200)
        return json(route, {
          listening: options.listening ?? true,
          since: SINCE,
          buffered: events.length,
          events: events.slice(0, limit),
        })
      }
      default:
        return route.fulfill({
          status: 503,
          contentType: "application/json",
          body: JSON.stringify({ error: { code: "not_available", message: "Not mocked" } }),
        })
    }
  })

  return {
    asked,
    emit: (batch) => {
      for (const socket of sockets) socket.send(JSON.stringify({ type: "events", data: batch }))
    },
  }
}
