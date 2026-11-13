import type { Page, Route } from "@playwright/test"
import { answerLogs, mockLogSockets, type LogMocks } from "./processes-logs-fixture"

/**
 * A host that runs PM2, for the specs of the PM2 list and of an application's
 * own page: one account's daemon with a cluster of two (`api`), a worker PM2
 * gave up on (`worker`) and a Bun fork with no memory limit (`queue`), and
 * the process table's reading of each PID that is running.
 */

const now = new Date().toISOString()
const earlier = new Date(Date.now() - 3 * 3600_000).toISOString()

export const json = (route: Route, body: unknown, status = 200) =>
  route.fulfill({ status, contentType: "application/json", body: JSON.stringify(body) })

const account = (capabilities: string[], role: string) => ({
  authenticated: true,
  needsTotp: false,
  needsEnrollment: false,
  require2fa: false,
  capabilities,
  user: {
    id: 1,
    username: "operator",
    role,
    totpEnabled: true,
    disabled: false,
    mustChangePassword: false,
    lastLoginAt: now,
    createdAt: now,
  },
})

export const ADMIN = account(
  ["read", "service.control", "file.write", "terminal", "destructive", "system.admin"],
  "admin",
)
export const READ_ONLY = account(["read"], "viewer")

export type PM2Row = Record<string, unknown> & {
  id: number
  daemonId: string
  name: string
  pid: number
  status: string
}

export const pm2Process = (over: Partial<PM2Row> & Record<string, unknown>): PM2Row => ({
  id: 0,
  daemonId: "deploy",
  logsAvailable: true,
  name: "api",
  namespace: "default",
  status: "online",
  pid: 4021,
  cpu: 12.5,
  memory: 268435456,
  restarts: 3,
  unstableRestarts: 0,
  uptimeMs: 3 * 3600_000,
  execMode: "cluster_mode",
  instances: 2,
  scriptPath: "/srv/api/server.js",
  cwd: "/srv/api",
  outLogPath: "/home/deploy/.pm2/logs/api-out.log",
  errLogPath: "/home/deploy/.pm2/logs/api-err.log",
  nodeVersion: "24.0.0",
  user: "deploy",
  watching: false,
  interpreter: "node",
  version: "2.4.1",
  autorestart: true,
  maxMemoryRestart: 314572800,
  createdAtMs: Date.now() - 86400_000,
  logTimes: true,
  ...over,
})

export const DAEMON = {
  account: "deploy",
  home: "/home/deploy",
  dumpSavedAt: earlier,
  savedApps: ["api", "worker", "queue"],
  startupUnit: "pm2-deploy.service",
}

export const API_0 = pm2Process({})
export const API_2 = pm2Process({ id: 2, pid: 4022, cpu: 3.5, memory: 201326592 })
export const WORKER = pm2Process({
  id: 1,
  name: "worker",
  status: "errored",
  pid: 0,
  cpu: 0,
  memory: 0,
  restarts: 16,
  unstableRestarts: 15,
  uptimeMs: 0,
  execMode: "fork_mode",
  instances: 1,
  scriptPath: "/srv/worker/index.js",
  cwd: "/srv/worker",
  outLogPath: "/home/deploy/.pm2/logs/worker-out.log",
  errLogPath: "/home/deploy/.pm2/logs/worker-err.log",
  maxMemoryRestart: undefined,
  version: undefined,
})
export const QUEUE = pm2Process({
  id: 3,
  name: "queue",
  pid: 4030,
  cpu: 0.4,
  memory: 94371840,
  restarts: 0,
  execMode: "fork_mode",
  instances: 1,
  scriptPath: "/srv/queue/consumer.ts",
  cwd: "/srv/queue",
  outLogPath: "/home/deploy/.pm2/logs/queue-out.log",
  errLogPath: "/home/deploy/.pm2/logs/queue-err.log",
  interpreter: "bun",
  maxMemoryRestart: undefined,
  version: undefined,
})

export const PM2 = {
  available: true,
  daemons: [DAEMON],
  processes: [API_0, API_2, WORKER, QUEUE],
}

/** A process's recent windows, as the sampler keeps them. */
const history = (rss: number, cpu: number) =>
  Array.from({ length: 30 }, (_, i) => ({
    at: new Date(Date.now() - (30 - i) * 4000).toISOString(),
    cpu: cpu + (i % 5) * 2,
    rss: rss + i * 1048576,
    read: 2048 * (i % 3),
    write: 4096 * i,
  }))

/** The process table's reading of one PM2 process's PID. */
export const processRow = (app: PM2Row, over: Record<string, unknown> = {}) => ({
  pid: app.pid,
  ppid: 900,
  name: app.interpreter === "bun" ? "bun" : "node",
  cmdline: `${app.interpreter === "bun" ? "bun" : "node"} ${String(app.scriptPath)}`,
  username: "deploy",
  status: "sleeping",
  state: "sleeping",
  cpuPercent: app.cpu,
  cpuReady: true,
  memPercent: 3,
  rss: app.memory,
  vms: 1073741824,
  threads: 11,
  nice: 0,
  createTime: earlier,
  manager: "pm2",
  managerName: app.name,
  exe: app.interpreter === "bun" ? "/usr/local/bin/bun" : "/usr/bin/node",
  cwd: app.cwd,
  fileDescriptors: 44,
  fdReady: true,
  openFilesLimit: 1024,
  ioReady: true,
  ioReadRate: 2048,
  ioWriteRate: 16384,
  listening: [] as unknown[],
  connections: 0,
  history: history(Number(app.memory), Number(app.cpu)),
  ...over,
})

export const ROWS: Record<number, ReturnType<typeof processRow>> = {
  4021: processRow(API_0, {
    listening: [{ proto: "tcp", address: "0.0.0.0", port: 3000 }],
    connections: 7,
  }),
  4022: processRow(API_2, {
    listening: [{ proto: "tcp", address: "0.0.0.0", port: 3000 }],
    connections: 5,
  }),
  4030: processRow(QUEUE),
}

const inspect = (row: ReturnType<typeof processRow>, admin: boolean) => ({
  pid: row.pid,
  createTime: row.createTime,
  argv: row.cmdline.split(" "),
  environmentReadable: true,
  environment: [
    "NODE_ENV=production",
    "PORT=3000",
    `DATABASE_URL=${admin ? "postgres://api:fixture-secret@127.0.0.1/api" : "••••••••"}`,
    "PM2_HOME=/home/deploy/.pm2",
    "NODE_APP_INSTANCE=0",
  ],
})

const host = {
  hostname: "srv-1",
  platform: "ubuntu",
  platformVersion: "24.04",
  kernelVersion: "6.14.0",
  kernelArch: "x86_64",
  processes: 143,
}

const metrics = {
  ts: now,
  cpu: {
    totalPercent: 23.4,
    perCore: [20, 26],
    loadAvg1: 0.42,
    loadAvg5: 0.5,
    loadAvg15: 0.6,
    cores: 2,
    modes: { user: 15, system: 6, iowait: 2, steal: 0, idle: 77 },
  },
  memory: {
    total: 8589934592,
    used: 4294967296,
    free: 1073741824,
    available: 3221225472,
    cached: 2147483648,
    buffers: 0,
    usedPercent: 50,
  },
  swap: { total: 0, used: 0, free: 0, usedPercent: 0 },
  mounts: [],
  net: [],
  uptimeSeconds: 86400 * 3,
  pressure: { supported: true, cpuSome: 1, memSome: 0, ioSome: 0 },
  sockets: { tcpInUse: 40 },
  procs: { running: 11, blocked: 1, total: 143 },
}

export type PM2Mocks = LogMocks & {
  /** Every call that was not a read, as `METHOD /path?query`, with its body. */
  writes: { call: string; body: unknown }[]
}

/**
 * Answers every call the PM2 pages make. `inventory` is read on each request,
 * so a spec can change what PM2 runs between two polls; `hold` keeps the list
 * out until the spec lets it go, for the loading state; `fail` answers the
 * list with that status instead.
 */
export async function mockPM2(
  page: Page,
  options: {
    session?: typeof ADMIN
    inventory?: () => unknown
    rows?: Record<number, unknown>
    hold?: Promise<void>
    fail?: number
  } = {},
): Promise<PM2Mocks> {
  const mocks: PM2Mocks = { sockets: [], searches: [], writes: [] }
  const session = options.session ?? ADMIN
  const rows = options.rows ?? ROWS
  await mockLogSockets(page, mocks)
  await page.route("**/api/v1/**", async (route) => {
    const request = route.request()
    const url = new URL(request.url())
    const path = url.pathname.replace(/^\/api\/v1/, "")
    if (answerLogs(route, path, url, mocks)) return
    if (path === "/auth/session") return json(route, session)
    if (path === "/system/host") return json(route, host)
    if (path === "/system/metrics") return json(route, metrics)
    if (path === "/updates/self") return json(route, { current: "0.8.7", latest: "0.8.7" })
    if (request.method() !== "GET") {
      mocks.writes.push({
        call: `${request.method()} ${path}${url.search}`,
        body: request.postDataJSON() as unknown,
      })
      return json(route, { exitCode: 0 })
    }
    if (path === "/pm2/") {
      if (options.hold) await options.hold
      if (options.fail) {
        return json(
          route,
          { code: "command_failed", message: "pm2 jlist: exit status 1" },
          options.fail,
        )
      }
      return json(route, options.inventory ? options.inventory() : PM2)
    }
    const inspected = /^\/processes\/(\d+)\/inspect$/.exec(path)
    if (inspected) {
      const row = rows[Number(inspected[1])] as ReturnType<typeof processRow> | undefined
      if (!row) return json(route, {}, 404)
      return json(route, inspect(row, session.capabilities.includes("system.admin")))
    }
    const read = /^\/processes\/(\d+)$/.exec(path)
    if (read) {
      const row = rows[Number(read[1])]
      return row ? json(route, row) : json(route, {}, 404)
    }
    return json(route, [])
  })
  return mocks
}
