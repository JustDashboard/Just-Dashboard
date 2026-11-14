import { createHash } from "node:crypto"
import { expect, test, type Page, type Route, type WebSocketRoute } from "@playwright/test"
import { answerLogs, mockLogSockets, type LogMocks } from "./processes-logs-fixture"
import { risen } from "./risen"

/**
 * The Processes section, checked in a browser against a mocked host.
 *
 * What these assert is the part a type check cannot: that every verb the
 * backend offers is reachable from a row — terminate and kill on a process,
 * reset and flush on a PM2 application, clear-failed on a unit, disable on a
 * cron job — that a schedule is described in words beside its expression, and
 * that the pages read as the design system says: figures as tiles, one plain
 * panel, no framed block on the page, and every row drawn as the product it
 * is where it is one (§14) — nginx's mark on the nginx process and unit,
 * Postgres's on the failed unit, Node's on a PM2 application, Let's
 * Encrypt's on certbot's timer. Each page reads its service's logs where it
 * is — a unit's journal and runs, a PM2 application's output, cron's log and
 * a timer's runs — through the service logs every page embeds. The
 * screenshots at 1280 and 1720 are the eyes the assertions do not have.
 */

const now = new Date().toISOString()
const earlier = new Date(Date.now() - 3 * 3600_000).toISOString()

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
    lastLoginAt: now,
    createdAt: now,
  },
}

const process = (over: Record<string, unknown>) => ({
  pid: 100,
  ppid: 1,
  name: "node",
  cmdline: "node /srv/api/server.js",
  username: "deploy",
  status: "sleeping",
  cpuPercent: 12.5,
  memPercent: 4.2,
  rss: 268435456,
  vms: 1073741824,
  threads: 11,
  nice: 0,
  createTime: earlier,
  state: "sleeping",
  manager: "pm2",
  managerName: "api",
  ...over,
})

const processes = [
  process({ pid: 4021, name: "node", cpuPercent: 42.1 }),
  process({
    pid: 812,
    name: "nginx",
    cmdline: "nginx: master process /usr/sbin/nginx",
    username: "root",
    manager: "systemd",
    managerName: "nginx.service",
    cpuPercent: 1.2,
    rss: 8388608,
  }),
  process({
    pid: 77,
    name: "kworker/0:1",
    cmdline: "",
    username: "root",
    manager: "kernel",
    managerName: "kernel",
    cpuPercent: 0,
    rss: 0,
  }),
  process({
    pid: 5110,
    name: "python3",
    cmdline: "python3 /home/deploy/worker.py",
    manager: "unmanaged",
    managerName: "",
    state: "blocked",
    status: "disk-sleep",
    cpuPercent: 0.5,
  }),
  process({
    pid: 6000,
    name: "defunct",
    cmdline: "",
    manager: "unmanaged",
    managerName: "",
    state: "zombie",
    status: "zombie",
    cpuPercent: 0,
    rss: 0,
  }),
]

const inventory = {
  processes,
  total: processes.length,
  available: 143,
  truncated: false,
  ratesReady: true,
  users: [
    { value: "root", label: "root", count: 90 },
    { value: "deploy", label: "deploy", count: 53 },
  ],
  states: [
    { value: "sleeping", label: "Sleeping", count: 130 },
    { value: "running", label: "Running", count: 11 },
    { value: "blocked", label: "Blocked", count: 1 },
    { value: "zombie", label: "Zombie", count: 1 },
  ],
  managers: [
    { value: "systemd", label: "systemd", count: 60 },
    { value: "kernel", label: "Kernel", count: 50 },
    { value: "unmanaged", label: "Unmanaged", count: 20 },
    { value: "container", label: "Container", count: 9 },
    { value: "pm2", label: "PM2", count: 4 },
  ],
  groups: [
    {
      key: "pm2:api",
      manager: "pm2",
      name: "api",
      count: 4,
      cpuPercent: 61.3,
      memory: 1073741824,
      ioRate: 0,
      pid: 4021,
    },
    {
      key: "systemd:nginx.service",
      manager: "systemd",
      name: "nginx.service",
      count: 5,
      cpuPercent: 2.4,
      memory: 46137344,
      ioRate: 0,
      pid: 812,
    },
    {
      key: "container:c13e58c8f7b4",
      manager: "container",
      name: "c13e58c8f7b4",
      label: "postgres",
      count: 18,
      cpuPercent: 9.1,
      memory: 2147483648,
      ioRate: 0,
      pid: 2211,
    },
  ],
}

/** What the detail route adds to a row, per process: where it runs and what it listens on. */
const DETAIL: Record<number, Record<string, unknown>> = {
  4021: {
    exe: "/usr/bin/node",
    cwd: "/srv/api",
    fileDescriptors: 1020,
    openFilesLimit: 1024,
    listening: [{ proto: "tcp", address: "0.0.0.0", port: 3000 }],
    connections: 7,
    children: 2,
  },
  812: {
    exe: "/usr/sbin/nginx",
    cwd: "/",
    fileDescriptors: 38,
    openFilesLimit: 524288,
    listening: [
      { proto: "tcp", address: "0.0.0.0", port: 80 },
      { proto: "tcp", address: "0.0.0.0", port: 443 },
      { proto: "tcp6", address: "::", port: 443 },
    ],
    connections: 12,
    children: 4,
  },
}

/**
 * The process page's slower read, built from a row: nginx handles the
 * signals a daemon does and runs a library an upgrade replaced; node handles
 * fewer, holds a rotated log open and carries a credential in its
 * environment.
 */
function inspectFor(row: ReturnType<typeof process>) {
  const nginx = row.name === "nginx"
  return {
    pid: row.pid,
    createTime: row.createTime,
    argv: String(row.cmdline).split(" ").filter(Boolean),
    exe: nginx ? "/usr/sbin/nginx" : "/usr/bin/node",
    exeDeleted: false,
    root: "/",
    waitChannel: "do_epoll_wait",
    identity: {
      ready: true,
      user: { id: nginx ? 0 : 1001, name: row.username },
      effectiveUser: { id: nginx ? 0 : 1001, name: row.username },
      savedUser: { id: nginx ? 0 : 1001, name: row.username },
      group: { id: nginx ? 0 : 1001, name: nginx ? "root" : "deploy" },
      effectiveGroup: { id: nginx ? 0 : 1001, name: nginx ? "root" : "deploy" },
      groups: nginx
        ? []
        : [
            { id: 1001, name: "deploy" },
            { id: 27, name: "sudo" },
          ],
      session: row.pid,
      processGroup: row.pid,
    },
    scheduling: {
      ready: true,
      policy: "normal",
      nice: row.nice,
      cpus: "0-1",
      cpuCount: 2,
      hostCpus: 2,
      lastCpu: 1,
      userSeconds: 5400,
      systemSeconds: 1200,
      childrenUserSeconds: 0,
      childrenSystemSeconds: 0,
      ioWaitSeconds: 12,
      voluntarySwitches: 1_204_118,
      involuntarySwitches: 30_771,
      minorFaults: 811_004,
      majorFaults: 212,
    },
    memory: {
      ready: true,
      resident: row.rss,
      anonymous: Math.round(Number(row.rss) * 0.7),
      fileBacked: Math.round(Number(row.rss) * 0.25),
      sharedMemory: Math.round(Number(row.rss) * 0.05),
      peak: Number(row.rss) * 2,
      virtual: row.vms,
      virtualPeak: Number(row.vms) * 1.1,
      data: 120_000_000,
      stack: 135_168,
      text: 80_000_000,
      libraries: 9_000_000,
      locked: 0,
      pageTables: 1_200_000,
      swap: Math.round(Number(row.rss) * 0.04),
      proportional: Math.round(Number(row.rss) * 0.8),
      unique: Math.round(Number(row.rss) * 0.66),
      rollupReady: true,
      oomScore: nginx ? 2 : 671,
      oomScoreAdj: nginx ? -500 : 0,
      oomReady: true,
    },
    io: {
      ready: true,
      charsRead: 9_000_000_000,
      charsWritten: 3_100_000_000,
      readCalls: 4_400_000,
      writeCalls: 2_900_000,
      readBytes: 220_000_000,
      writeBytes: 1_800_000_000,
      cancelledWriteBytes: 0,
    },
    security: {
      ready: true,
      capabilities: nginx ? ["CAP_NET_BIND_SERVICE", "CAP_SETUID", "CAP_SETGID"] : [],
      allCapabilities: false,
      boundingAll: true,
      noNewPrivs: !nginx,
      seccomp: nginx ? "off" : "filter",
      seccompFilters: nginx ? 0 : 1,
      label: nginx ? "unconfined" : "",
    },
    signals: {
      ready: true,
      caught: nginx
        ? ["SIGHUP", "SIGINT", "SIGQUIT", "SIGUSR1", "SIGUSR2", "SIGTERM", "SIGCHLD", "SIGWINCH"]
        : ["SIGINT", "SIGTERM", "SIGUSR2"],
      ignored: ["SIGPIPE"],
      blocked: [],
      pending: [],
    },
    namespaces: [
      { kind: "pid", own: false },
      { kind: "net", own: false },
      { kind: "mnt", own: false },
    ],
    cgroup: nginx
      ? { path: "/system.slice/nginx.service", memoryLimit: 1073741824, memoryCurrent: 46137344 }
      : { path: "/user.slice/user-1001.slice/user@1001.service/app.slice/pm2-deploy.service" },
    limits: [
      { name: "Max cpu time", soft: "unlimited", hard: "unlimited", unit: "seconds" },
      { name: "Max open files", soft: nginx ? "524288" : "1024", hard: "524288", unit: "files" },
      { name: "Max processes", soft: "31616", hard: "31616", unit: "processes" },
      { name: "Max locked memory", soft: "8388608", hard: "8388608", unit: "bytes" },
    ],
    files: {
      ready: true,
      total: nginx ? 38 : 1020,
      files: nginx ? 9 : 14,
      sockets: nginx ? 24 : 990,
      pipes: 2,
      devices: 3,
      other: nginx ? 0 : 11,
      deleted: nginx ? 0 : 1,
      entries: [
        { fd: 0, kind: "device", target: "/dev/null" },
        { fd: 1, kind: "pipe", target: "pipe:[28812]" },
        { fd: 2, kind: "pipe", target: "pipe:[28813]" },
        { fd: 3, kind: "socket", target: "socket:[44120]" },
        nginx
          ? { fd: 4, kind: "file", target: "/var/log/nginx/access.log" }
          : { fd: 4, kind: "file", target: "/srv/api/logs/app.log", deleted: true },
        {
          fd: 5,
          kind: "file",
          target: nginx ? "/var/log/nginx/error.log" : "/srv/api/logs/app.log.1",
        },
        { fd: 6, kind: "other", target: "anon_inode:[eventpoll]" },
      ],
    },
    libraries: nginx
      ? [
          { path: "/usr/lib/x86_64-linux-gnu/libssl.so.3", deleted: true },
          { path: "/usr/lib/x86_64-linux-gnu/libcrypto.so.3", deleted: true },
          { path: "/usr/lib/x86_64-linux-gnu/libc.so.6" },
          { path: "/usr/lib/x86_64-linux-gnu/libpcre2-8.so.0" },
          { path: "/usr/lib/x86_64-linux-gnu/libz.so.1" },
        ]
      : [
          { path: "/usr/lib/x86_64-linux-gnu/libc.so.6" },
          { path: "/usr/lib/x86_64-linux-gnu/libstdc++.so.6" },
        ],
    connections: [
      { proto: "tcp", local: "10.0.0.5:3000", remote: "10.0.0.9:51234", state: "established" },
      { proto: "tcp", local: "10.0.0.5:3000", remote: "10.0.0.9:51240", state: "established" },
      { proto: "tcp", local: "127.0.0.1:48812", remote: "127.0.0.1:5432", state: "established" },
      { proto: "tcp", local: "10.0.0.5:3000", remote: "203.0.113.7:40112", state: "time_wait" },
    ],
    connectionsTotal: 4,
    environment: [
      "DATABASE_URL=postgres://api:hunter2@127.0.0.1/api",
      "HOME=/home/deploy",
      "NODE_ENV=production",
      "PATH=/usr/local/bin:/usr/bin:/bin",
      "PORT=3000",
    ],
    environmentReadable: true,
    threads: [
      { tid: row.pid, name: row.name, state: "sleeping", cpuSeconds: 4200, lastCpu: 1 },
      {
        tid: row.pid + 1,
        name: nginx ? "nginx" : "V8 Worker",
        state: "running",
        cpuSeconds: 1800,
        lastCpu: 0,
      },
      {
        tid: row.pid + 2,
        name: nginx ? "nginx" : "libuv-worker",
        state: "sleeping",
        cpuSeconds: 600,
        lastCpu: 1,
      },
    ],
    threadsTotal: 3,
  }
}

const systemdLink = {
  pid: 1,
  name: "systemd",
  cmdline: "/sbin/init",
  username: "root",
  state: "sleeping",
  cpuPercent: 0,
  cpuReady: true,
  rss: 12582912,
  createTime: earlier,
}

/** A process's recent windows, as the sampler keeps them. */
const history = Array.from({ length: 30 }, (_, i) => ({
  at: new Date(Date.now() - (30 - i) * 4000).toISOString(),
  cpu: 30 + (i % 5) * 4,
  rss: 268435456 + i * 1048576,
  read: 0,
  write: 4096 * i,
}))

const pm2 = {
  available: true,
  daemons: [
    {
      account: "deploy",
      home: "/home/deploy",
      dumpSavedAt: earlier,
      savedApps: ["api", "worker"],
      startupUnit: "pm2-deploy.service",
    },
  ],
  processes: [
    {
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
      autorestart: true,
      maxMemoryRestart: 314572800,
      createdAtMs: Date.now() - 86400_000,
      logTimes: true,
    },
    {
      id: 2,
      daemonId: "deploy",
      logsAvailable: true,
      name: "api",
      namespace: "default",
      status: "online",
      pid: 4022,
      cpu: 3.5,
      memory: 201326592,
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
      autorestart: true,
      maxMemoryRestart: 314572800,
      createdAtMs: Date.now() - 86400_000,
      logTimes: true,
    },
    {
      id: 1,
      daemonId: "deploy",
      logsAvailable: true,
      name: "worker",
      namespace: "default",
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
      nodeVersion: "24.0.0",
      user: "deploy",
      watching: false,
      autorestart: true,
    },
  ],
}

const units = {
  available: true,
  units: [
    {
      name: "postgresql.service",
      description: "PostgreSQL RDBMS",
      loadState: "loaded",
      activeState: "failed",
      subState: "failed",
      unitFileState: "enabled",
      enabled: true,
    },
    {
      name: "nginx.service",
      description: "A high performance web server",
      loadState: "loaded",
      activeState: "active",
      subState: "running",
      unitFileState: "enabled",
      enabled: true,
    },
    {
      name: "apt-daily.service",
      description: "Daily apt download activities",
      loadState: "loaded",
      activeState: "inactive",
      subState: "dead",
      unitFileState: "static",
      enabled: true,
    },
  ],
}

const unitDetail = {
  unit: {
    ...units.units[1],
    mainPid: 812,
    memoryBytes: 8388608,
    tasks: 3,
    activeSince: Math.floor(Date.now() / 1000) - 7200,
    fragmentPath: "/lib/systemd/system/nginx.service",
    result: "success",
    restarts: 0,
  },
  properties: {
    User: "",
    Group: "",
    WorkingDirectory: "",
    Restart: "on-failure",
    CanReload: "yes",
    MemoryMax: "infinity",
    TasksMax: "4915",
    ExecStart: "{ path=/usr/sbin/nginx ; argv[]=/usr/sbin/nginx -g daemon on; master_process on; }",
  },
}

const timers = {
  available: true,
  timers: [
    {
      unit: "certbot.timer",
      activates: "certbot.service",
      activeState: "active",
      subState: "waiting",
      unitFileState: "enabled",
      enabled: true,
      next: new Date(Date.now() + 5 * 3600_000).toISOString(),
      last: earlier,
    },
    {
      unit: "fstrim.timer",
      activates: "fstrim.service",
      activeState: "inactive",
      subState: "dead",
      unitFileState: "disabled",
      enabled: false,
    },
  ],
}

const crontab = {
  user: "root",
  source: "crontab -u root",
  raw: "MAILTO=ops@example.com\n# nightly backup\n0 3 * * * /usr/local/bin/backup\n# 0 4 * * * /usr/local/bin/prune\n",
  jobs: [
    {
      line: 3,
      schedule: "0 3 * * *",
      command: "/usr/local/bin/backup",
      comment: "nightly backup",
      raw: "0 3 * * * /usr/local/bin/backup",
      disabled: false,
    },
    {
      line: 4,
      schedule: "0 4 * * *",
      command: "/usr/local/bin/prune",
      raw: "# 0 4 * * * /usr/local/bin/prune",
      disabled: true,
    },
  ],
  env: ["MAILTO=ops@example.com"],
  comments: ["nightly backup"],
}

const guardedCrontab = {
  ...crontab,
  revision: createHash("sha256").update(crontab.raw).digest("hex"),
  timeZone: "UTC",
}

async function json(route: Route, body: unknown) {
  await route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify(body) })
}

/** A failed unit, for the sheet that reads its runs. */
const failedDetail = {
  unit: {
    ...units.units[0],
    mainPid: 0,
    memoryBytes: 0,
    tasks: 0,
    fragmentPath: "/lib/systemd/system/postgresql.service",
    result: "start-limit-hit",
    restarts: 4,
  },
  properties: { Restart: "on-failure", CanReload: "yes", MemoryMax: "infinity", TasksMax: "" },
}

async function mockHost(page: Page): Promise<LogMocks> {
  const logs: LogMocks = { sockets: [], searches: [] }
  await mockLogSockets(page, logs)
  await page.route("**/api/v1/**", async (route) => {
    const url = new URL(route.request().url())
    const path = url.pathname.replace(/^\/api\/v1/, "")
    const method = route.request().method()
    if (answerLogs(route, path, url, logs)) return
    if (path === "/auth/session") return json(route, user)
    if (path === "/system/host") {
      return json(route, {
        hostname: "srv-1",
        platform: "ubuntu",
        platformVersion: "24.04",
        kernelVersion: "6.14.0",
        kernelArch: "x86_64",
        processes: 143,
      })
    }
    if (path === "/system/metrics") {
      return json(route, {
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
      })
    }
    if (path === "/updates/self") return json(route, { current: "0.6.7", latest: "0.6.7" })
    if (path === "/processes/inventory") return json(route, inventory)
    if (path === "/processes/4021/tree") {
      return json(route, {
        ancestors: [
          {
            pid: 1,
            name: "systemd",
            cmdline: "/sbin/init",
            username: "root",
            state: "sleeping",
            cpuPercent: 0,
            rss: 1048576,
            createTime: earlier,
          },
          {
            pid: 900,
            name: "PM2 v6.0.5: God Daemon",
            cmdline: "PM2 v6.0.5: God Daemon (/home/deploy/.pm2)",
            username: "deploy",
            state: "sleeping",
            cpuPercent: 0.1,
            rss: 52428800,
            createTime: earlier,
          },
        ],
        children: [],
      })
    }
    if (path === "/processes/812/tree") {
      return json(route, {
        ancestors: [systemdLink],
        children: [1, 2, 3, 4].map((n) => ({
          pid: 812 + n,
          name: "nginx",
          cmdline: "nginx: worker process",
          username: "www-data",
          state: "sleeping",
          cpuPercent: n === 1 ? 2.4 : 0.2,
          cpuReady: true,
          rss: 9437184,
          createTime: earlier,
        })),
      })
    }
    if (/^\/processes\/\d+\/tree$/.test(path)) {
      return json(route, { ancestors: [systemdLink], children: [] })
    }
    if (/^\/processes\/\d+\/inspect$/.test(path)) {
      const pid = Number(path.split("/")[2])
      const row = processes.find((p) => p.pid === pid)
      if (!row) return route.fulfill({ status: 404, body: "{}" })
      const read = inspectFor(row)
      if (!row.cmdline) {
        read.files = { ...read.files, ready: false, total: 0, files: 0, deleted: 0, entries: [] }
        read.environment = []
        read.waitChannel = row.state === "zombie" ? "" : read.waitChannel
      }
      return json(route, read)
    }
    if (/^\/processes\/\d+$/.test(path)) {
      const pid = Number(path.split("/")[2])
      const row = processes.find((p) => p.pid === pid)
      if (!row) return route.fulfill({ status: 404, body: "{}" })
      // A kernel thread and a zombie have no executable, directory or
      // descriptors to read.
      const empty = !row.cmdline
      return json(route, {
        ...row,
        ...(empty
          ? { fdReady: false }
          : {
              exe: "/usr/bin/node",
              cwd: "/srv/api",
              fileDescriptors: 1020,
              fdReady: true,
              openFilesLimit: 1024,
            }),
        ioReady: true,
        listening: [],
        ...DETAIL[pid],
        history,
      })
    }
    if (path === "/pm2/") return json(route, pm2)
    if (path === "/systemd/") return json(route, units)
    if (path === "/systemd/timers") return json(route, timers)
    if (path === "/systemd/nginx.service") return json(route, unitDetail)
    if (path === "/systemd/postgresql.service") return json(route, failedDetail)
    if (path === "/cron/users") return json(route, ["root", "deploy"])
    if (path === "/cron/user/root") return json(route, guardedCrontab)
    if (path === "/cron/system") {
      return json(route, [
        {
          user: "",
          source: "/etc/crontab",
          raw: "17 * * * * root cd / && run-parts --report /etc/cron.hourly\n",
          jobs: [
            {
              line: 1,
              schedule: "17 * * * *",
              user: "root",
              command: "cd / && run-parts --report /etc/cron.hourly",
              raw: "17 * * * * root cd / && run-parts --report /etc/cron.hourly",
              disabled: false,
            },
          ],
          env: [],
          comments: [],
        },
      ])
    }
    if (method !== "GET") return json(route, { exitCode: 0 })
    return json(route, [])
  })
  return logs
}

/** Every verb this page offers has to be a word, reachable from a row. */
async function menuLabels(page: Page, rowName: string): Promise<string[]> {
  const row = page.getByRole("row", { name: new RegExp(rowName) }).first()
  await row.hover()
  await row.getByRole("button", { name: "More actions" }).click()
  const items = await page.locator("[role='menuitem']").allInnerTexts()
  await page.keyboard.press("Escape")
  return items
}

/**
 * §2: the only block on a page that may draw a frame is a table. A grid owns a
 * scroll region, and an edge is what says where it ends — a row whose actions
 * sit past the boundary otherwise reads as a row with no actions. Everything
 * else in the page's flow stays plain, which is what the frame is read against.
 *
 * Asserted structurally rather than as a count, so the rule keeps holding as
 * pages gain and lose tables.
 */
async function framedNonTables(page: Page) {
  return page.evaluate(() =>
    Array.from(document.querySelectorAll("[data-slot=page] [data-slot=panel]:not([data-plain])"))
      .filter((el) => !el.querySelector("[data-slot=table-container]"))
      .map((el) => el.outerHTML.slice(0, 120)),
  )
}

test("the live table reads the host and every process verb is a word", async ({ page }) => {
  await mockHost(page)
  await page.goto("/processes")
  await expect(page.getByRole("heading", { name: "Live" })).toBeVisible()

  // The machine first, as the identity line the Overview opens on, with the
  // table's cadence, Pause and the shortcuts at its right end — the line of
  // two buttons that stood above it is gone.
  const identity = page.locator("[data-slot='host-identity']")
  await expect(identity).toContainText("srv-1")
  await expect(identity).toContainText("Ubuntu 24.04")
  await expect(identity).toContainText("143 processes")
  await expect(identity.getByRole("button", { name: /Every 4s/ })).toBeVisible()
  await expect(identity.getByRole("button", { name: "Pause updates" })).toBeVisible()
  await expect(page.locator("[data-slot='page-context']")).toHaveCount(0)

  // No tiles: who is using the machine is the band of workloads, and the
  // counts the tiles held are the state chips in the table's head.
  await expect(page.locator("[data-slot='stat-tile']")).toHaveCount(0)
  const band = page.locator("[data-slot='workloads']")
  await expect(band.getByRole("button", { name: "Only api's processes" }).first()).toBeVisible()
  await expect(band.getByRole("button", { name: "Only postgres's processes" })).toHaveCount(2)
  const states = page.locator("[aria-label='State']")
  await expect(states.getByRole("button", { name: /Zombie/ })).toContainText("1")
  await expect(states.getByRole("button", { name: /Blocked/ })).toContainText("1")

  // A row is its product: nginx's mark on the nginx row, Node's on node, and
  // a glyph rather than a guess on a kernel thread — which says what it is
  // rather than borrowing a command line it does not have.
  await expect(page.locator("td img[src='/logos/nginx.svg']")).toHaveCount(1)
  await expect(page.locator("td img[src='/logos/nodejs.svg']").first()).toBeVisible()
  await expect(page.getByRole("row", { name: /kworker/ })).toContainText("kernel thread")
  await expect(page.getByRole("row", { name: /defunct/ })).toContainText("waiting for its parent")

  // The process table frames itself because it is a table (§2); nothing else
  // on the page may.
  expect(await framedNonTables(page), "a framed block that is not a table").toEqual([])

  const labels = await menuLabels(page, "4021")
  expect(labels).toEqual(
    expect.arrayContaining([
      "Open",
      "Terminate",
      "Kill",
      "Pause",
      "Hang up",
      "Interrupt",
      "Send SIGUSR1",
      "Open api",
      "Copy PID",
    ]),
  )

  // A row opens the process's own page: the readings over their recent
  // shape, where it sits — its supervisor, its parent, its sockets — and
  // named buttons for the two verbs that matter.
  await page.getByRole("button", { name: "node", exact: true }).first().click()
  await expect(page).toHaveURL(/\/processes\/4021$/)
  const readings = page.getByTestId("process-readings")
  await expect(readings.locator("[data-slot='stat-tile']")).toHaveCount(4)
  await expect(
    readings.locator("[data-slot='stat-tile']", { hasText: "CPU" }).locator("svg"),
  ).toBeVisible()
  await expect(
    readings.locator("[data-slot='stat-tile']", { hasText: "Open files" }),
  ).toContainText("of 1024")
  const picture = page.getByRole("group", { name: "Where it runs" })
  await expect(picture.getByText(":3000")).toBeVisible()
  await expect(picture.getByText("every interface")).toBeVisible()
  await expect(picture.getByRole("link", { name: "Open api" })).toHaveAttribute(
    "href",
    "/processes/pm2/api",
  )
  await expect(
    picture.getByRole("button", { name: /PM2 v6.0.5: God Daemon \(900\)/ }),
  ).toBeVisible()
  await expect(page.getByRole("button", { name: "Terminate", exact: true })).toBeVisible()
  await expect(page.getByRole("button", { name: "Kill", exact: true })).toBeVisible()
  await expect(page.getByRole("dialog")).toHaveCount(0)
})

// The ranking and the user were two selects on a toolbar of chips, the one
// thing Live's table did that Services' does not.
test("the live table's ranking and its users are chips, as its other filters are", async ({
  page,
}) => {
  await mockHost(page)
  const asked: URLSearchParams[] = []
  page.on("request", (request) => {
    const url = new URL(request.url())
    if (url.pathname.endsWith("/processes/inventory")) asked.push(url.searchParams)
  })
  await page.goto("/processes")
  await expect(page.getByRole("row", { name: /nginx/ })).toBeVisible()
  await expect(page.getByRole("combobox")).toHaveCount(0)

  const focus = page.locator("[aria-label='Focus']")
  await expect(focus.getByRole("button")).toHaveText([
    "Automatic",
    "CPU",
    "Memory",
    "Disk I/O",
    "Uptime",
  ])
  await expect(focus.getByRole("button", { name: "Automatic" })).toHaveAttribute(
    "aria-pressed",
    "true",
  )
  await focus.getByRole("button", { name: "Memory" }).click()
  await expect.poll(() => asked.at(-1)?.get("sort")).toBe("memory")
  await expect(page.getByText(/sorted by highest memory/)).toBeVisible()
  await expect(focus.getByRole("button", { name: "Memory" })).toHaveAttribute(
    "aria-pressed",
    "true",
  )

  // Each account with its count; a second press lets it go.
  const users = page.locator("[aria-label='User']")
  await expect(users.getByRole("button")).toHaveText([/root\s*90/, /deploy\s*53/])
  await users.getByRole("button", { name: /deploy/ }).click()
  await expect.poll(() => asked.at(-1)?.get("user")).toBe("deploy")
  await users.getByRole("button", { name: /deploy/ }).click()
  await expect.poll(() => asked.at(-1)?.get("user")).toBeNull()
})

// A kernel thread ignores a signal from user space and a zombie has already
// exited: both used to offer a Kill that reported success and changed nothing.
test("a kernel thread and a zombie are not offered signals they cannot take", async ({ page }) => {
  await mockHost(page)
  await page.goto("/processes")
  const kernel = await menuLabels(page, "kworker")
  expect(kernel).not.toContain("Terminate")
  expect(kernel).not.toContain("Kill")
  const zombie = await menuLabels(page, "defunct")
  expect(zombie).not.toContain("Kill")
  expect(zombie).toContain("Open parent (1)")

  // The old address of a process still opens it, now on its own page.
  await page.goto("/processes?pid=6000")
  await expect(page).toHaveURL(/\/processes\/6000$/)
  await expect(page.getByText("Exited, and not yet reaped")).toBeVisible()
  await expect(page.getByText("Almost out of file descriptors")).toHaveCount(0)
  await expect(page.getByRole("button", { name: "Kill", exact: true })).toHaveCount(0)
  await page.getByRole("button", { name: "Open parent (1)" }).click()
  await expect(page).toHaveURL(/\/processes\/1$/)
})

// A process is a page of its own, and the table it was opened from stands
// beside it: the next process is one press, Alt+↓ walks the list, and the
// view on screen goes with the reader.
test("a process's page keeps the table beside it and carries the view between processes", async ({
  page,
}) => {
  await mockHost(page)
  await page.setViewportSize({ width: 1440, height: 900 })
  await page.goto("/processes/4021")
  const rail = page.getByRole("navigation", { name: "Processes" })
  await expect(rail).toBeVisible()
  await expect(rail.getByRole("button", { name: "node, PID 4021" })).toHaveAttribute(
    "aria-current",
    "page",
  )
  await page.getByRole("tab", { name: /Files/ }).click()
  await expect(page).toHaveURL(/\/processes\/4021\?tab=files$/)
  await rail.getByRole("button", { name: "nginx, PID 812" }).click()
  await expect(page).toHaveURL(/\/processes\/812\?tab=files$/)
  await expect(page.getByRole("tab", { name: /Files/ })).toHaveAttribute("aria-selected", "true")
  await expect(page.getByText("/var/log/nginx/access.log")).toBeVisible()
  await page.locator("body").click({ position: { x: 900, y: 10 } })
  await page.keyboard.press("Alt+ArrowDown")
  await expect(page).toHaveURL(/\/processes\/77\?tab=files$/)
  await page.getByRole("link", { name: "Processes", exact: true }).click()
  await expect(page).toHaveURL(/\/processes$/)
})

// Everything else the kernel keeps about a process has a view: its threads,
// what it holds open, who it talks to, its environment with credentials
// hidden, its limits and privileges, and the signals it handles.
test("every view of a process reads what the kernel keeps about it", async ({ page }) => {
  await mockHost(page)
  await page.setViewportSize({ width: 1440, height: 900 })
  await page.goto("/processes/4021")
  await expect(page.getByText("Holding a deleted file open")).toBeVisible()
  await expect(page.getByText("Almost out of file descriptors")).toBeVisible()

  await page.getByRole("tab", { name: /Threads/ }).click()
  await expect(page.getByRole("cell", { name: "V8 Worker" })).toBeVisible()

  await page.getByRole("tab", { name: /Files/ }).click()
  await page.getByRole("button", { name: /Deleted/ }).click()
  await expect(page.getByText("/srv/api/logs/app.log", { exact: true })).toBeVisible()
  await expect(page.getByText("anon_inode:[eventpoll]")).toHaveCount(0)

  await page.getByRole("tab", { name: /Network/ }).click()
  await expect(page.getByRole("cell", { name: "127.0.0.1:5432" })).toBeVisible()

  await page.getByRole("tab", { name: /Environment/ }).click()
  await expect(page.getByRole("cell", { name: "NODE_ENV" })).toBeVisible()
  await expect(page.getByText("hunter2")).toHaveCount(0)
  await page.getByRole("button", { name: "Reveal DATABASE_URL" }).click()
  await expect(page.getByText(/hunter2/)).toBeVisible()

  await page.getByRole("tab", { name: "Limits and privileges" }).click()
  await expect(page.getByRole("cell", { name: /open files/ })).toContainText("1020 open now")
  await expect(page.getByText("filtered by 1 filter")).toBeVisible()
  await expect(page.getByRole("tab", { name: "Logs" })).toBeVisible()
})

// Control says what each signal will do to this process before it is sent:
// nginx handles a hang-up and reloads, a node server that does not is ended
// by one. A signal can take the process's children with it, and the
// out-of-memory bias and the supervisor's own verbs are on the same view.
test("control says what a signal will do and sends it with the process's children", async ({
  page,
}) => {
  await mockHost(page)
  const writes: { path: string; body: unknown }[] = []
  page.on("request", (request) => {
    if (request.method() === "GET") return
    const path = new URL(request.url()).pathname.replace(/^\/api\/v1/, "")
    writes.push({ path, body: request.postDataJSON() })
  })
  await page.setViewportSize({ width: 1440, height: 900 })
  await page.goto("/processes/4021?tab=control")
  const signals = page.getByRole("list", { name: "Signals" })
  await expect(signals.getByRole("listitem").filter({ hasText: "SIGHUP" })).toContainText("ends it")

  await page.goto("/processes/812?tab=control")
  await expect(signals.getByRole("listitem").filter({ hasText: "SIGHUP" })).toContainText("handled")
  await expect(signals.getByRole("listitem").filter({ hasText: "SIGKILL" })).toContainText(
    "always ends it",
  )
  await expect(page.getByText("What supervises it")).toBeVisible()
  await expect(page.getByRole("button", { name: "Restart", exact: true })).toBeVisible()

  await page.getByRole("switch", { name: "Include its children" }).click()
  await page.getByRole("button", { name: "Send SIGTERM to nginx" }).click()
  const dialog = page.getByRole("alertdialog").or(page.getByRole("dialog"))
  await expect(dialog).toContainText("4 processes it started")
  await dialog.getByRole("button", { name: "Send", exact: true }).click()
  await expect
    .poll(() => writes.find((w) => w.path === "/processes/812/signal")?.body)
    .toMatchObject({ signal: "SIGTERM", tree: true, startedAt: earlier })

  await page.getByRole("button", { name: /Never end it/ }).click()
  await expect
    .poll(() => writes.find((w) => w.path === "/processes/812/oom")?.body)
    .toMatchObject({ adj: -1000, startedAt: earlier })
})

// An upgrade that replaced OpenSSL under a running nginx is a restart the
// operator does not know they need; the page says so, and draws the workers
// it started in the picture.
test("a process running code an upgrade replaced says a restart picks it up", async ({ page }) => {
  await mockHost(page)
  await page.setViewportSize({ width: 1440, height: 900 })
  await page.goto("/processes/812")
  await expect(page.getByText("Running 2 libraries an upgrade replaced")).toBeVisible()
  await expect(
    page.getByText(/Restarting nginx.service loads the new code, which matters/),
  ).toBeVisible()
  // Its four workers are one node — nginx × 4 — and the list under the
  // picture has each of them.
  const picture = page.getByRole("group", { name: "Where it runs" })
  await expect(
    picture.getByRole("button", { name: "Show the 4 nginx processes it started" }),
  ).toBeVisible()
  await expect(picture.getByText(":80 :443")).toBeVisible()
  await expect(picture.getByRole("link", { name: "Open nginx.service" })).toBeVisible()
  await picture.getByRole("button", { name: "Show the 4 nginx processes it started" }).click()
  await page.getByRole("button", { name: /nginx 815/ }).click()
  await expect(page).toHaveURL(/\/processes\/815$/)
})

// The first read lands after the band is drawn: its blocks keep their height
// and the table under them stays where it was.
test("the workloads hold their place while the first read is out", async ({ page }) => {
  await mockHost(page)
  let release = () => {}
  const held = new Promise<void>((resolve) => (release = resolve))
  await page.route("**/api/v1/processes/inventory*", async (route) => {
    await held
    await route.fallback()
  })
  await page.setViewportSize({ width: 1440, height: 900 })
  await page.goto("/processes")
  const band = page.locator("[data-slot='workloads']")
  await expect(band.getByText("Reading the workloads…").first()).toBeVisible()
  await risen(page)
  const before = await band.boundingBox()
  release()
  await expect(band.getByRole("button", { name: "Only api's processes" }).first()).toBeVisible()
  await risen(page)
  expect(await band.boundingBox()).toEqual(before)
})

// The band answers who; a press narrows the table to that workload, and the
// chip it leaves in the toolbar is how it is let go.
test("a workload narrows the table to its processes", async ({ page }) => {
  await mockHost(page)
  const groups: string[] = []
  page.on("request", (request) => {
    const url = new URL(request.url())
    if (url.pathname.endsWith("/processes/inventory"))
      groups.push(url.searchParams.get("group") ?? "")
  })
  await page.goto("/processes")
  const api = page
    .locator("[data-slot='workloads']")
    .getByRole("button", { name: "Only api's processes" })
    .first()
  await api.click()
  await expect(api).toHaveAttribute("aria-pressed", "true")
  await expect.poll(() => groups.at(-1)).toBe("pm2:api")
  const chip = page.getByRole("button", { name: /Showing api's processes/ })
  await expect(chip).toBeVisible()
  await chip.click()
  await expect.poll(() => groups.at(-1)).toBe("")
  await expect(api).toHaveAttribute("aria-pressed", "false")
})

// A process page open on a process that exits used to keep its last reading
// under live verbs, polled into a 404 nobody saw.
test("a process that exits while its page is open says so and stops", async ({ page }) => {
  await mockHost(page)
  let gone = false
  await page.route("**/api/v1/processes/4021", (route) =>
    gone
      ? route.fulfill({
          status: 404,
          contentType: "application/json",
          body: '{"error":"not_found"}',
        })
      : route.fallback(),
  )
  await page.goto("/processes/4021")
  await expect(page.getByRole("button", { name: "Kill", exact: true })).toBeVisible()
  gone = true
  await expect(page.getByText("This process has exited")).toBeVisible({ timeout: 6000 })
  await expect(page.getByRole("button", { name: "Kill", exact: true })).toHaveCount(0)
  await expect(page.locator("[data-slot='stat-tile']", { hasText: "Memory" })).toBeVisible()
})

/**
 * A process name is whatever the kernel reports, and a headless Chrome reports
 * its entire argv — two hundred characters of `--disable-*` flags. `RowLink`
 * carried `truncate`, but a button is inline-block: it sized to that text and
 * painted it straight across Owner, State, CPU and Memory, seven hundred pixels
 * past the cell it lives in.
 *
 * The assertion is over the whole table rather than over the name, because the
 * same shape — a capped wrapper around something that sizes to its content —
 * is what every title column in the app is built from.
 */
test("a process named by its whole command line stays inside its cell", async ({ page }) => {
  const argv =
    "chrome-headless-shell --type=gpu-process --no-sandbox --disable-dev-shm-usage " +
    "--disable-breakpad --headless --ozone-platform=headless --use-angle=swiftshader-webgl " +
    "--enable-unsafe-swiftshader --gpu-preferences=YAAAAAA"

  await mockHost(page)
  await page.route("**/api/v1/processes/inventory*", (route) =>
    json(route, { ...inventory, processes: [process({ pid: 9001, name: argv }), ...processes] }),
  )
  await page.setViewportSize({ width: 1720, height: 1000 })
  await page.goto("/processes")
  await expect(page.getByRole("button", { name: argv, exact: true })).toBeVisible()

  const bleeding = await page.evaluate(() => {
    const out: string[] = []
    for (const cell of document.querySelectorAll("td")) {
      const edge = cell.getBoundingClientRect().right
      for (const el of cell.querySelectorAll("*")) {
        const box = el.getBoundingClientRect()
        if (box.width > 0 && box.right - edge > 1) {
          out.push(`${el.textContent?.slice(0, 40)} +${Math.round(box.right - edge)}px`)
        }
      }
    }
    return out
  })
  expect(bleeding, "content painted past the cell holding it").toEqual([])
})

test("the wheel over a half-shown table brings all of it on screen before its rows move", async ({
  page,
}) => {
  const many = Array.from({ length: 80 }, (_, i) =>
    process({ pid: 2000 + i, name: `worker-${i}`, cpuPercent: 80 - i / 2 }),
  )
  await mockHost(page)
  await page.route("**/api/v1/processes/inventory*", (route) =>
    json(route, { ...inventory, processes: many, total: many.length }),
  )
  await page.setViewportSize({ width: 1280, height: 800 })
  await page.goto("/processes")
  await expect(page.getByRole("button", { name: "worker-0", exact: true })).toBeVisible()

  const table = page
    .locator('[data-slot="table-container"]')
    .filter({ has: page.getByRole("button", { name: "worker-0", exact: true }) })
  const read = () =>
    table.evaluate((region) => {
      const port = document.querySelector<HTMLElement>("[data-workspace-shell-scroll]")!
      const box = region.getBoundingClientRect()
      const view = port.getBoundingClientRect()
      return {
        top: box.top - view.top,
        bottom: view.bottom - box.bottom,
        page: port.scrollTop,
        rows: region.scrollTop,
        x: box.left + box.width / 2,
        y: (Math.max(box.top, view.top) + Math.min(box.bottom, view.bottom)) / 2,
      }
    })

  const start = await read()
  expect(start.bottom, "the table starts below the fold").toBeLessThan(0)
  await page.mouse.move(start.x, start.y)
  await page.mouse.wheel(0, 120)
  await expect.poll(async () => (await read()).bottom).toBeGreaterThanOrEqual(0)
  const revealed = await read()
  expect(revealed.top).toBeGreaterThanOrEqual(0)
  expect(revealed.rows, "the rows held still while the page moved").toBe(0)

  await page.mouse.move(revealed.x, revealed.y)
  await page.mouse.wheel(0, 120)
  await expect.poll(async () => (await read()).rows).toBeGreaterThan(0)
  expect((await read()).page, "a table in view keeps the wheel").toBe(revealed.page)

  // Clipped at the top, the wheel upward brings it back down into view. The
  // page ends just under this table, so it is given room to scroll past it.
  await table.evaluate((region) => {
    const port = document.querySelector<HTMLElement>("[data-workspace-shell-scroll]")!
    port.append(Object.assign(document.createElement("div"), { style: "height: 100vh" }))
    port.scrollTop += region.getBoundingClientRect().top - port.getBoundingClientRect().top + 120
  })
  const clipped = await read()
  expect(clipped.top).toBeLessThan(0)
  await page.mouse.move(clipped.x, clipped.y)
  await page.mouse.wheel(0, -120)
  await expect.poll(async () => (await read()).top).toBeGreaterThanOrEqual(0)
  expect((await read()).rows).toBe(clipped.rows)
})

test("PM2 says whether it survives a reboot and offers the housekeeping verbs", async ({
  page,
}) => {
  await mockHost(page)
  await page.goto("/processes/pm2")
  // PM2 as the thing the page is about: its mark on the identity line, the
  // account and its Node among the facts, the verdict at the end.
  const identity = page.locator("[data-slot='host-identity']")
  await expect(identity.locator("img[src='/logos/pm2.svg']")).toBeVisible()
  await expect(identity).toContainText("deploy")
  await expect(identity).toContainText("Node 24.0.0")
  await expect(identity).toContainText("2 applications")
  await expect(identity.getByText("Resurrects on boot")).toBeVisible()
  await expect(identity.getByRole("button", { name: "Save list" })).toHaveCount(0)

  // No tiles: what PM2 takes of the machine is the band, the counts are the
  // state chips in the table's head, and the crashing worker is the first row.
  await expect(page.locator("[data-slot='stat-tile']")).toHaveCount(0)
  const band = page.locator("[data-slot='pm2-band']")
  await expect(band.getByRole("button", { name: "Only api" })).toHaveCount(2)
  await band.getByRole("button", { name: "Only api" }).first().hover()
  await expect(band.locator("[data-slot=bar-reading-line]").first()).toContainText("×2")
  const states = page.locator("[aria-label='State']")
  await expect(states.getByRole("button", { name: /Online/ })).toContainText("2")
  await expect(states.getByRole("button", { name: /Errored/ })).toContainText("1")
  const rows = page.locator("tbody tr")
  await expect(rows.first()).toContainText("worker")
  await expect(rows.first()).toContainText("15 unstable")
  await expect(rows.first()).toContainText("PM2 stopped retrying")
  // Each application as what runs it, memory against its restart limit.
  await expect(page.locator("td img[src='/logos/nodejs.svg']")).toHaveCount(3)
  await expect(page.getByRole("row", { name: /api #0/ })).toContainText("of 300 MB")

  // A press on the band narrows the table to the application, and its chip
  // lets it go.
  await band.getByRole("button", { name: "Only api" }).first().click()
  await expect(rows).toHaveCount(2)
  await page.getByRole("button", { name: /Showing api only/ }).click()
  await expect(rows).toHaveCount(3)
  await states.getByRole("button", { name: /Errored/ }).click()
  await expect(rows).toHaveCount(1)
  await states.getByRole("button", { name: /Errored/ }).click()

  const labels = await menuLabels(page, "worker")
  expect(labels).toEqual(
    expect.arrayContaining(["Logs", "Reset restart counter", "Flush logs", "Delete from PM2"]),
  )
  const api = await menuLabels(page, "api")
  expect(api).toContain("Scale…")

  // Starting one: what runs it and how many are cards, and the command is
  // the one that will run.
  await page.getByRole("button", { name: "Start application" }).click()
  const start = page.getByRole("dialog")
  await page.getByLabel("Script or ecosystem file").fill("/srv/api/server.js")
  await page.getByLabel("Name", { exact: true }).fill("api2")
  await expect(start).toContainText("pm2 start /srv/api/server.js --name api2")
  await start.getByRole("button", { name: /Python/ }).click()
  await start.getByRole("button", { name: /One per core/ }).click()
  await expect(start).toContainText(
    "pm2 start /srv/api/server.js --name api2 --interpreter python3 -i max",
  )
  // An ecosystem file is handed over whole.
  await page.getByLabel("Script or ecosystem file").fill("/srv/ecosystem.config.js")
  await expect(start.getByRole("button", { name: /Python/ })).toHaveCount(0)
  await expect(start).toContainText("pm2 start /srv/ecosystem.config.js --only api2")
  await page.keyboard.press("Escape")

  await page.getByRole("button", { name: "Startup and bulk" }).click()
  const bulk = await page.locator("[role='menuitem']").allInnerTexts()
  expect(bulk).toEqual(["Save startup list", "Reload all", "Restart all", "Stop all"])
})

test("PM2's verdict reads the saved list, and saving it again is one press", async ({ page }) => {
  await mockHost(page)
  const saved: string[] = []
  await page.route("**/api/v1/pm2/**", (route) => {
    const path = new URL(route.request().url()).pathname
    if (path.endsWith("/pm2/save")) {
      saved.push(path)
      return json(route, { exitCode: 0 })
    }
    return json(route, {
      ...pm2,
      daemons: [{ ...pm2.daemons[0], savedApps: ["api", "old-cron"] }],
    })
  })
  await page.goto("/processes/pm2")
  // The worker was started after the save: a reboot would not bring it back,
  // and the row says so.
  const identity = page.locator("[data-slot='host-identity']")
  await expect(identity.getByText("worker is not in the saved list")).toBeVisible()
  await expect(page.getByRole("row", { name: /worker/ })).toContainText("not saved")
  await identity.getByRole("button", { name: "Save list" }).click()
  await expect.poll(() => saved.length).toBe(1)
})

// An application was a sheet over this list until 0.8.7; the page it became
// has a spec of its own (`pm2-application.spec.ts`). What stays here is that
// the address this section's pages linked to still lands.
test("a PM2 application has a page of its own, and the sheet's address lands on it", async ({
  page,
}) => {
  await mockHost(page)
  const scaled: unknown[] = []
  page.on("request", (request) => {
    if (request.method() === "POST" && request.url().includes("/pm2/api/scale")) {
      scaled.push(request.postDataJSON())
    }
  })
  // A bare name is what the live table's owner link carried; a cluster's
  // name is every worker, and the page is the application's.
  await page.goto("/processes/pm2?app=api")
  await expect(page).toHaveURL(/\/processes\/pm2\/api$/)
  await expect(page.getByRole("dialog")).toHaveCount(0)
  const line = page.locator("[data-slot='pm2-app-identity']")
  await expect(line).toContainText("cluster of 2")
  // Four readings as sentences, from the process table's read of each PID
  // it has, over its history; the sheet drew four tiles.
  await expect(page.locator("[data-slot='stat-tile']")).toHaveCount(0)
  const readings = page.getByTestId("pm2-readings").locator("[data-slot='reading-line']")
  await expect(readings).toHaveCount(4)
  await expect(readings.nth(1)).toContainText("of the 300 MB")
  await expect(readings.nth(3)).toContainText("Restarted 6 times")
  await expect(readings.first().locator("svg").last()).toBeVisible()
  const picture = page.getByRole("group", { name: "Where it runs" })
  await expect(picture.getByText(":3000")).toBeVisible()
  await expect(picture.getByText(/every interface/)).toBeVisible()
  await expect(picture.getByText("Worker 1 of 2")).toBeVisible()

  // Its other worker is a row under Instances, opening its process.
  await page.getByRole("tab", { name: /Instances/ }).click()
  await expect(page).toHaveURL(/tab=instances/)
  await expect(page.getByRole("link", { name: "Open worker 2, PID 4022" })).toHaveAttribute(
    "href",
    "/processes/4022",
  )

  // Scaling is a stepper over the workers, said before it is done.
  await page.getByRole("button", { name: "Scale…", exact: true }).click()
  const scale = page.getByRole("dialog", { name: /Scale api/ })
  await expect(scale.getByRole("button", { name: "Scale to 2" })).toBeDisabled()
  await scale.getByRole("button", { name: "One worker more" }).click()
  await expect(scale).toContainText("Starts 1 worker")
  await scale.getByRole("button", { name: "Scale to 3" }).click()
  await expect.poll(() => scaled).toEqual([{ instances: 3 }])

  // The worker PM2 gave up on says so, and the press reads its log.
  await page.goto("/processes/pm2?app=deploy:1")
  await expect(page).toHaveURL(/\/processes\/pm2\/worker/)
  await expect(line.getByText("errored", { exact: true })).toBeVisible()
  await expect(readings.first()).toContainText("Not running")
  await page.getByRole("tab", { name: "Logs", exact: true }).click()
  await expect(page.getByRole("tab", { name: "Logs", exact: true })).toHaveAttribute(
    "aria-selected",
    "true",
  )
})

test("services list failed units first and the sheet offers reload where it applies", async ({
  page,
}) => {
  await mockHost(page)
  await page.goto("/processes/services")
  // No tiles: what they counted is the state chips in the table's head, the
  // failed one in its tone, and the verdict at the end of the identity line.
  // This host's backend predates the readings, and the page still opens.
  await expect(page.locator("[data-slot='stat-tile']")).toHaveCount(0)
  await expect(
    page.locator("[aria-label='State']").getByRole("button", { name: /Failed/ }),
  ).toContainText("1")
  await expect(page.locator("[data-slot='host-identity']")).toContainText("1 service failed")
  expect(await framedNonTables(page)).toEqual([])
  const names = await page
    .locator("[data-slot='table-row'] [data-slot='table-cell']:first-child button")
    .allInnerTexts()
  expect(names[0]).toBe("postgresql.service")
  // A unit is the product it runs.
  await expect(page.locator("td img[src='/logos/postgresql.svg']")).toBeVisible()
  await expect(page.locator("td img[src='/logos/nginx.svg']")).toBeVisible()

  const failed = await menuLabels(page, "postgresql")
  expect(failed).toEqual(
    expect.arrayContaining(["Journal", "Clear failed state", "Disable on boot"]),
  )

  await page.getByRole("button", { name: "nginx.service" }).click()
  await expect(page.getByRole("button", { name: "Stop" })).toBeVisible()
  await page.getByRole("button", { name: "More actions" }).last().click()
  await expect(page.getByRole("menuitem", { name: /Reload configuration/ })).toBeVisible()
  await expect(page.getByRole("menuitem", { name: /Open unit file/ })).toBeVisible()
})

test("scheduled says what each schedule means and builds a new one in words", async ({ page }) => {
  await mockHost(page)
  await page.goto("/processes/scheduled")
  const jobs = page.getByRole("table").first()
  await expect(jobs.getByText("Every day at 03:00")).toBeVisible()
  await expect(page.getByText("runs certbot.service")).toBeVisible()

  // No tiles: the machine's identity line counts the three places schedules
  // live, and what fires next counts down at its end.
  await expect(page.locator("[data-slot='stat-tile']")).toHaveCount(0)
  const identity = page.locator("[data-slot='host-identity']")
  await expect(identity).toContainText("1 cron job for root, 1 disabled")
  await expect(identity).toContainText("1 of 2 timers armed")
  await expect(identity).toContainText("1 system cron line")
  const next = page.locator("[data-slot='next-up']")
  await expect(next).toContainText("Next run")
  await expect(next).toContainText(/\d+[smhd]/)

  // The next day of all three as an hour-by-hour chart, and under it each
  // schedule in the order it runs, with its clock time and its schedule in
  // words; each kind a chip that counts and narrows.
  const chart = page.getByRole("img", { name: /from 3 schedules over the next 24 hours/ })
  await expect(chart).toBeVisible()
  await expect(chart.locator("[data-hour]")).toHaveCount(24)
  const band = page.getByRole("list", { name: "Schedules" })
  await expect(band.locator(":scope > li")).toHaveCount(3)
  // /etc/crontab's run-parts line is the hourly scripts, every hour at :17.
  const hourly = band.locator(":scope > li").filter({ hasText: "Hourly scripts" })
  await expect(hourly).toContainText("Every hour at :17")
  await expect(hourly).toContainText(/:17/)
  // Pointing at it says it on the chart's line.
  await hourly.hover()
  await expect(
    page.locator("[aria-live=polite]").filter({ hasText: "Hourly scripts" }),
  ).toBeVisible()
  const kinds = page.locator("[aria-label='Kind']")
  await kinds.getByRole("button", { name: /Timers/ }).click()
  await expect(band.locator(":scope > li")).toHaveCount(1)
  await expect(band).toContainText("certbot")
  await kinds.getByRole("button", { name: /Timers/ }).click()
  // What the axis does not draw is said under it.
  await expect(page.getByText("2 switched off")).toBeVisible()

  // The crontab's head counts its jobs as chips that narrow the table.
  const states = page.locator("[aria-label='Job state']")
  await states.getByRole("button", { name: /Disabled/ }).click()
  await expect(jobs.getByRole("row", { name: /prune/ })).toBeVisible()
  await expect(jobs.getByRole("row", { name: /backup/ })).toHaveCount(0)
  await states.getByRole("button", { name: /All/ }).click()

  // certbot's timer is Let's Encrypt's renewal; a script of the operator's
  // own keeps the clock.
  await expect(page.locator("td img[src='/logos/lets-encrypt.svg']")).toBeVisible()

  // Disable and Edit are the daily verbs and sit inline; the rest are words
  // in the menu.
  const backup = page.getByRole("row", { name: /backup/ }).first()
  await expect(backup.getByRole("button", { name: "Disable" })).toBeVisible()
  await expect(backup.getByRole("button", { name: "Edit" })).toBeVisible()
  expect(await menuLabels(page, "backup")).toEqual(["Copy command", "Remove"])
  await expect(
    page.getByRole("row", { name: /prune/ }).first().getByRole("button", { name: "Enable" }),
  ).toBeVisible()

  // The editor is a sheet that draws the week the schedule makes as it is
  // written.
  await page.getByRole("button", { name: "Add job" }).click()
  const editor = page.getByRole("dialog", { name: "Add cron job" })
  await editor.getByLabel("Command").fill("/usr/local/bin/report")
  const preview = editor.locator("[data-slot='schedule-preview']")
  await expect(preview.getByText("0 3 * * *", { exact: true })).toBeVisible()
  await expect(preview.getByText("Every day at 03:00")).toBeVisible()
  await expect(preview.getByRole("img", { name: /over the next seven days/ })).toBeVisible()
  await expect(preview.getByText(/^Next: /)).toBeVisible()
  await editor.getByRole("radio", { name: "Hourly" }).click()
  await expect(preview.getByText("0 * * * *", { exact: true })).toBeVisible()
  await expect(editor.getByRole("button", { name: "Add job" })).toBeEnabled()
  await page.keyboard.press("Escape")

  // A job opens on its week, its command and the runs cron logged for it.
  await jobs.getByRole("button", { name: "/usr/local/bin/backup" }).click()
  const sheet = page.getByRole("dialog")
  await expect(page).toHaveURL(/job=cron(%3A|:)3/)
  await expect(sheet.getByRole("img", { name: /over the next seven days/ })).toBeVisible()
  await expect(sheet.locator("[data-slot='stat-tile']").first()).toContainText("Next run")
  await expect(sheet.getByRole("button", { name: "Disable" })).toBeVisible()
  await page.keyboard.press("Escape")
  await expect(page).not.toHaveURL(/job=/)
})

test("scheduled says what systemd runs instead and when each timer fires", async ({ page }) => {
  await mockHost(page)
  await page.route("**/api/v1/systemd/timers", (route) =>
    json(route, {
      ...timers,
      timers: timers.timers.map((timer) =>
        timer.unit === "certbot.timer"
          ? {
              ...timer,
              description: "Run certbot twice daily",
              calendar: ["*-*-* 00,12:00:00"],
              randomDelay: "12h",
            }
          : timer,
      ),
    }),
  )
  await page.route("**/api/v1/cron/system", (route) =>
    json(route, [
      {
        user: "",
        source: "/etc/cron.d/certbot",
        raw: "",
        env: [],
        comments: [],
        timeZone: "UTC",
        jobs: [
          {
            line: 12,
            schedule: "0 */12 * * *",
            user: "root",
            command:
              "test -x /usr/bin/certbot -a \\! -d /run/systemd/system && perl -e 'sleep int(rand(43200))' && certbot -q renew",
            comment: "certbot.timer.",
            raw: "",
            disabled: false,
            timeZone: "UTC",
          },
        ],
      },
      {
        user: "",
        source: "/etc/crontab",
        raw: "",
        env: [],
        comments: [],
        timeZone: "UTC",
        jobs: [
          {
            line: 19,
            schedule: "* * * * *",
            user: "user-name",
            command: "command to be executed",
            comment: "|  |  |  |  |",
            raw: "",
            disabled: true,
            timeZone: "UTC",
          },
          {
            line: 20,
            schedule: "25 6 * * *",
            user: "root",
            command: "test -x /usr/sbin/anacron || { cd / && run-parts --report /etc/cron.daily; }",
            raw: "",
            disabled: false,
            timeZone: "UTC",
          },
        ],
      },
    ]),
  )
  await page.goto("/processes/scheduled")

  // A timer's schedule is said in words in its row, with the delay its
  // package adds.
  const timer = page.locator("[data-process-row='certbot.timer']")
  await expect(timer).toContainText("Twice a day, at 00:00 and 12:00, up to 12 hours later")
  await expect(timer).toContainText("Run certbot twice daily")

  // One table of the package files, grouped by file: /etc/crontab's worked
  // example is no job, the daily line is the daily scripts, and certbot's
  // line says the timer runs its work.
  const files = page
    .locator("[data-slot=panel]")
    .filter({ has: page.getByRole("heading", { name: /^System cron files/ }) })
  await expect(files.getByRole("table")).toHaveCount(1)
  await expect(files).not.toContainText("command to be executed")
  await expect(files.getByRole("row", { name: /Daily scripts/ })).toContainText(
    "Every day at 06:25",
  )
  const certbot = files.getByRole("row", { name: /certbot:/ })
  await expect(certbot).toContainText("left to systemd")
  await expect(certbot).toContainText("certbot.timer")
  // The band does not count it as a run: it is said under the list.
  await expect(page.getByText("1 cron line left to systemd")).toBeVisible()

  await certbot.getByRole("button", { name: /certbot:/ }).click()
  const sheet = page.getByRole("dialog")
  await expect(sheet.getByText("systemd does this work here")).toBeVisible()
  await sheet.getByRole("link", { name: "certbot.timer" }).click()
  await expect(page).toHaveURL(/timer=certbot\.timer/)
  await expect(page.getByRole("dialog")).toContainText(
    "Twice a day, at 00:00 and 12:00, up to 12 hours later",
  )
})

const RUN_PREDICATES = [
  "starting",
  "started",
  "exited",
  "killed",
  "failed",
  "stopped",
  "deactivated",
  "restart_scheduled",
  "oom",
  "start_limit",
  "resources",
  "core_dumped",
]
  .map((event) => `event:${event}`)
  // The manager's own lines: the forced lens would name the program's too.
  .concat("program:systemd")

test("a unit's journal reads its runs, folds a loop and opens one run's own lines", async ({
  page,
}) => {
  const logs = await mockHost(page)
  await page.goto("/processes/services/postgresql.service?tab=journal")
  const sheet = page.getByRole("main")
  // The unit's own journal on the lens socket; the whole journal is the
  // logs page's, so there is no unit to pick here.
  await expect.poll(() => logs.sockets.at(-1)?.get("source")).toBe("journal:postgresql.service")
  await expect(sheet.getByLabel("Unit", { exact: true })).toHaveCount(0)

  await sheet.getByRole("button", { name: "Runs", exact: true }).click()
  const runs = sheet.getByRole("list", { name: "Runs" })
  const rows = runs.locator(":scope > li")
  await expect(rows).toHaveCount(3)
  // One read of the week's lifecycle lines through the systemd lens — its
  // tail, so a loop's latest runs are the ones returned.
  const asked = logs.searches.filter((s) => s.get("lens") === "systemd")
  expect(asked).toHaveLength(1)
  expect(asked[0].get("source")).toBe("journal:postgresql.service")
  expect(asked[0].getAll("f")).toEqual(RUN_PREDICATES)
  expect(asked[0].get("limit")).toBe("2000")
  expect(asked[0].has("order")).toBe(false)
  const since = Date.parse(asked[0].get("since")!)
  expect(Date.now() - since).toBeGreaterThan(6.9 * 86_400_000)
  expect(Date.now() - since).toBeLessThan(7.1 * 86_400_000)

  // The loop is said once, with systemd having given up on it.
  await expect(sheet.getByText("systemd stopped restarting postgresql.service")).toBeVisible()
  // Newest first: the crash the start limit refused to follow, the three
  // identical crashes before it as one row, and yesterday's run that was
  // stopped, with what it cost.
  await expect(rows.nth(0)).toContainText("failed")
  await expect(rows.nth(0)).toContainText("exit 1")
  await expect(rows.nth(0)).toContainText("start limit hit")
  await expect(rows.nth(0)).toContainText("restart #4")
  await expect(rows.nth(1)).toContainText("exit 1")
  await expect(rows.nth(1)).toContainText("restart #3")
  await expect(rows.nth(2)).toContainText("stopped")
  await expect(rows.nth(2)).toContainText("1.2 GB peak")
  await expect(rows.nth(2)).toContainText("1m 4s CPU")
  await rows.nth(1).getByRole("button", { name: "List the 3 identical runs" }).click()
  await expect(rows.nth(1).locator("ul > li")).toHaveCount(3)

  // The week is read again on a press, not every minute.
  await sheet.getByRole("button", { name: "Read the runs again" }).click()
  await expect.poll(() => logs.searches.filter((s) => s.get("lens") === "systemd").length).toBe(2)
  // The fold the reader opened stays open over the new read.
  await expect(rows.nth(1).locator("ul > li")).toHaveCount(3)

  // A run opens History on its own lines: its span, narrowed to its invocation.
  const before = logs.searches.length
  await rows.nth(0).getByRole("button").first().click()
  await expect(sheet.getByRole("button", { name: "History", exact: true })).toHaveAttribute(
    "aria-pressed",
    "true",
  )
  await expect
    .poll(() => logs.searches.slice(before).find((s) => s.getAll("f").includes("invocation:c4")))
    .toBeTruthy()
  const history = logs.searches.slice(before).find((s) => s.getAll("f").includes("invocation:c4"))!
  expect(history.get("source")).toBe("journal:postgresql.service")
  expect(Date.parse(history.get("until")!) - Date.parse(history.get("since")!)).toBeLessThan(10_000)
  await expect(sheet.getByLabel("Log lines").getByText(/pg_filenode\.map/)).toBeVisible()
  await expect(sheet.getByRole("button", { name: "Clear the invocation filter" })).toBeVisible()
})

test("sshd's journal is an administrator's, and the page says so rather than asking", async ({
  page,
}) => {
  const logs = await mockHost(page)
  // Registered after the host's, so answered first.
  await page.route("**/api/v1/auth/session", (route) =>
    json(route, {
      ...user,
      capabilities: ["read", "service.control"],
      user: { ...user.user, username: "viewer", role: "operator" },
    }),
  )
  await page.route("**/api/v1/systemd/ssh.service", (route) =>
    json(route, {
      ...unitDetail,
      unit: { ...unitDetail.unit, name: "ssh.service", description: "OpenBSD Secure Shell server" },
    }),
  )
  await page.goto("/processes/services/ssh.service?tab=journal")
  const sheet = page.getByRole("main")
  await expect(sheet.getByText("Login records need an administrator")).toBeVisible()
  // The server refuses the read before the socket upgrades: nothing is asked.
  expect(logs.sockets).toEqual([])
  expect(logs.searches).toEqual([])
})

test("a journal the server refuses is said to be refused, in its words, and nothing is read", async ({
  page,
}) => {
  const logs = await mockHost(page)
  const refusal =
    "Login and sudo records need an administrator: failed logins can hold passwords typed into the username prompt."
  // Registered after the host's, so answered first.
  await page.route(
    (url) => url.pathname.endsWith("/api/v1/logs/source"),
    (route) =>
      route.fulfill({
        status: 403,
        contentType: "application/json",
        body: JSON.stringify({ error: { code: "forbidden", message: refusal } }),
      }),
  )
  await page.goto("/processes/services/postgresql.service?tab=journal")
  const sheet = page.getByRole("main")
  await expect(sheet.getByText(refusal)).toBeVisible()
  // A socket retrying a refusal would say the tunnel dropped.
  expect(logs.sockets).toEqual([])
  expect(logs.searches).toEqual([])
})

test("on a phone a unit's lines start at the pane's edge and its controls stay in reach", async ({
  page,
}) => {
  await page.setViewportSize({ width: 390, height: 844 })
  const logs = await mockHost(page)
  await page.goto("/processes/services/postgresql.service?tab=journal")
  const sheet = page.getByRole("main")
  await expect.poll(() => logs.sockets.at(-1)?.get("source")).toBe("journal:postgresql.service")

  const lines = sheet.getByLabel("Log lines")
  const row = lines.locator("[data-row-key]").first()
  await expect(row).toBeVisible()
  // The unit and the event before it, the message on a line of its own
  // under them, starting at the edge rather than past the pane's.
  const pane = (await lines.boundingBox())!
  const message = (await row.locator(":scope > :last-child").boundingBox())!
  expect(message.x - pane.x).toBeLessThan(24)
  expect(message.x + 40).toBeLessThan(pane.x + pane.width)
  // The switches for how the lines are drawn are one menu, so the level chips
  // keep their room, and Wrap — what a narrow pane needs — is in it.
  await expect(sheet.getByRole("button", { name: "Wrap" })).toBeHidden()
  await sheet.getByRole("button", { name: "View" }).click()
  await expect(page.getByRole("menuitemcheckbox", { name: "Wrap" })).toBeVisible()
  await page.keyboard.press("Escape")
  const modes = sheet.getByRole("navigation", { name: "Log mode" })
  await expect(modes.getByRole("button", { name: "Runs" })).toBeInViewport()
})

test("a PM2 application's logs are its own lensed stream, beside PM2's count of its restarts", async ({
  page,
}) => {
  const logs = await mockHost(page)
  await page.goto("/processes/pm2")
  await page.getByRole("button", { name: "worker", exact: true }).click()
  await expect(page).toHaveURL(/\/processes\/pm2\/worker/)
  await page.getByRole("tab", { name: "Logs", exact: true }).click()
  const view = page.getByRole("tabpanel")
  await expect.poll(() => logs.sockets.at(-1)?.get("source")).toBe("pm2:deploy/1/worker")
  await expect(view.getByText("Restarted 16 times")).toBeVisible()
  await expect(view.getByText("15 unstable")).toBeVisible()
  await expect(view.getByLabel("Log lines").getByText(/ECONNREFUSED/)).toBeVisible()
  // Errored, so there is no last start to read around.
  await expect(view.getByRole("button", { name: "Around the last start" })).toHaveCount(0)

  // The view goes along to the next application; a cluster reads its first worker.
  await page.goto("/processes/pm2/api?tab=logs")
  await expect.poll(() => logs.sockets.at(-1)?.get("source")).toBe("pm2:deploy/0/api")
  const around = view.getByRole("button", { name: "Around the last start" })
  const before = logs.searches.length
  await around.click()
  await expect(around).toHaveAttribute("aria-pressed", "true")
  await expect(view.getByRole("button", { name: "History", exact: true })).toHaveAttribute(
    "aria-pressed",
    "true",
  )
  await expect.poll(() => logs.searches.length).toBeGreaterThan(before)
  const window = logs.searches.at(-1)!
  // Up three hours: the window is the three minutes around that start.
  const started = Date.now() - 3 * 3600_000
  expect(Math.abs(Date.parse(window.get("since")!) - (started - 120_000))).toBeLessThan(30_000)
  expect(Math.abs(Date.parse(window.get("until")!) - (started + 60_000))).toBeLessThan(30_000)
  // Live lets the window go.
  await view.getByRole("button", { name: "Live", exact: true }).click()
  await expect(around).toHaveAttribute("aria-pressed", "false")
})

test("scheduled reads cron's own log and each timer's runs where they are", async ({ page }) => {
  const logs = await mockHost(page)
  await page.goto("/processes/scheduled")
  // cron's log is its unit's journal, through the cron lens: its PAM
  // sessions hidden by the lens's default, as a chip that says so.
  await expect
    .poll(() => logs.sockets.find((s) => s.get("source") === "journal:cron.service")?.getAll("f"))
    .toEqual(["event:!session"])
  const cronLog = page
    .locator("[data-slot=panel]")
    .filter({ has: page.getByRole("heading", { name: "Cron log", exact: true }) })
  await expect(cronLog.getByLabel("Log lines").getByText(/usr\/local\/bin\/backup/)).toBeVisible()
  await expect(cronLog.getByText("output lost", { exact: true })).toBeVisible()
  // What cron started in the last day, and the runs whose output went
  // nowhere: the lens's readings are counts on its chips, where a tile stood.
  await expect(cronLog.getByRole("button", { name: /^Runs/ })).toContainText("143")
  await expect(cronLog.getByRole("button", { name: /^Output discarded/ })).toContainText("2")

  // A timer opens its sheet on the runs of the service it fires.
  await page.getByRole("button", { name: "certbot.timer", exact: true }).click()
  const sheet = page.getByRole("dialog")
  await expect(page).toHaveURL(/timer=certbot\.timer/)
  await expect(sheet.locator("[data-slot='stat-tile']").first()).toContainText("Next run")
  const runs = sheet.getByRole("list", { name: "Runs" })
  const rows = runs.locator(":scope > li")
  await expect(rows).toHaveCount(3)
  const asked = logs.searches.find((s) => s.get("lens") === "systemd")!
  expect(asked.get("source")).toBe("journal:certbot.service")
  expect(asked.getAll("f")).toEqual(RUN_PREDICATES)
  // The last two nights ran the same, the night before failed.
  await expect(rows.nth(0)).toContainText("succeeded")
  await expect(rows.nth(0).getByRole("button", { name: "List the 2 identical runs" })).toBeVisible()
  await expect(rows.nth(1)).toContainText("failed")
  await expect(rows.nth(1)).toContainText("exit 1")
  await expect(rows.nth(1)).toContainText("2.11s CPU")
  await expect(sheet.getByRole("button", { name: "Run now" })).toBeVisible()
  await page.keyboard.press("Escape")
  await expect(runs).toHaveCount(0)
})

test.describe("with no hover available", () => {
  test.use({ hasTouch: true, viewport: { width: 390, height: 844 } })

  test("every row's verbs are reachable on a phone", async ({ page }) => {
    await mockHost(page)
    for (const path of [
      "/processes",
      "/processes/4021",
      "/processes/812?tab=control",
      "/processes/pm2",
      "/processes/services",
      "/processes/scheduled",
    ]) {
      await page.goto(path)
      await page.waitForLoadState("networkidle")
      const hidden = await page.evaluate(() => {
        const bad: string[] = []
        for (const el of document.querySelectorAll<HTMLElement>("li button, tr button")) {
          if (parseFloat(getComputedStyle(el).opacity) < 0.1) {
            bad.push(el.getAttribute("aria-label") ?? el.outerHTML.slice(0, 120))
          }
        }
        return bad
      })
      expect(hidden, `controls hidden behind hover on ${path}`).toEqual([])
      const overflow = await page.evaluate(
        () => document.documentElement.scrollWidth > document.documentElement.clientWidth,
      )
      expect(overflow, `${path} scrolls sideways`).toBe(false)
    }
  })
})

// On a phone a process's page is the one pane: no rail, the way back and the
// arrows in reach, the page scrolling as a whole, and the supervisor's verbs
// under its sentence rather than squeezing it to a word a line.
test.describe("a process on a phone", () => {
  test.use({ viewport: { width: 390, height: 844 } })

  test("is one pane whose supervisor's verbs stay in reach", async ({ page }) => {
    await mockHost(page)
    await page.goto("/processes/812?tab=control")
    await expect(page.getByRole("navigation", { name: "Processes" })).toBeHidden()
    await expect(page.getByRole("link", { name: "Processes", exact: true })).toBeInViewport()
    await expect(page.getByRole("button", { name: "Next process" })).toBeInViewport()
    const sentence = page.getByText("systemd starts this process again when it exits.")
    await expect(sentence).toBeVisible()
    expect((await sentence.boundingBox())!.width).toBeGreaterThan(200)
    const restart = page.getByRole("button", { name: "Restart", exact: true })
    await restart.scrollIntoViewIfNeeded()
    await expect(restart).toBeInViewport()
    const overflow = await page.evaluate(
      () => document.documentElement.scrollWidth > document.documentElement.clientWidth,
    )
    expect(overflow).toBe(false)
  })
})

for (const width of [1280, 1720]) {
  test(`looks right at ${width}`, async ({ page }) => {
    await mockHost(page)
    for (const [name, path] of [
      ["live", "/processes"],
      ["process", "/processes/812"],
      ["pm2", "/processes/pm2"],
      ["services", "/processes/services"],
      ["scheduled", "/processes/scheduled"],
    ] as const) {
      await page.setViewportSize({ width, height: 1000 })
      await page.goto(path)
      await page.waitForLoadState("networkidle")
      const overflow = await page.evaluate(
        () => document.documentElement.scrollWidth > document.documentElement.clientWidth,
      )
      expect(overflow, `${path} scrolls sideways at ${width}`).toBe(false)
      const unnamed = await page.evaluate(() => {
        const bad: string[] = []
        for (const el of document.querySelectorAll<HTMLElement>("button")) {
          if (el.offsetParent === null) continue
          if ((el.textContent ?? "").trim().length > 0) continue
          if (!el.getAttribute("aria-label") && !el.querySelector(".sr-only")) {
            bad.push(el.outerHTML.slice(0, 120))
          }
        }
        return bad
      })
      expect(unnamed, `unlabelled icon-only controls on ${path}`).toEqual([])
      await page.screenshot({ path: `test-results/processes-${name}-${width}.png`, fullPage: true })
    }
  })
}

test("workspace: process focus holds row order and paused searches remain usable", async ({
  page,
}) => {
  await mockHost(page)
  await page.goto("/processes")
  const rows = page.locator("[data-workspace-item]")
  await expect(rows.first()).toBeVisible()
  await rows.first().focus()
  await expect(page.getByText("Row order held while inspecting")).toBeVisible()
  await page.keyboard.press("ArrowDown")
  await expect(rows.nth(1)).toBeFocused()
  const second = await rows.nth(1).getAttribute("data-workspace-item")
  await page.keyboard.press("Enter")
  await expect(page).toHaveURL(new RegExp(`/processes/${second?.split("-")[0]}$`))
  await page.goBack()
  await expect(rows.first()).toBeVisible()
  await page.getByRole("button", { name: "Pause updates", exact: true }).click()
  await expect(page.getByText("Updates paused", { exact: true })).toBeVisible()
  await page.keyboard.press("Control+f")
  await expect(page.locator("[data-page-search]")).toBeFocused()
  await page.locator("[data-page-search]").fill("node")
  await expect(rows.first()).toBeVisible()
  await page.getByRole("button", { name: "Resume updates", exact: true }).click()
  await expect(page.getByText("Updates paused", { exact: true })).toHaveCount(0)
})

test("workspace: changing process rankings keep focused rows steady and Pause stops polling", async ({
  page,
}) => {
  await page.clock.install()
  await mockHost(page)
  let reads = 0
  let reordered = false
  await page.route("**/api/v1/processes/inventory*", (route) => {
    reads++
    return json(route, {
      ...inventory,
      processes: reordered ? [...inventory.processes].reverse() : inventory.processes,
    })
  })
  await page.goto("/processes")
  const rows = page.locator("[data-workspace-item]:visible")
  await expect(rows.first()).toBeVisible()
  const before = await rows.evaluateAll((rows) =>
    rows.map((row) => row.getAttribute("data-workspace-item")),
  )
  await rows.first().focus()
  reordered = true
  const initialReads = reads
  await page.clock.fastForward(5000)
  await expect.poll(() => reads).toBeGreaterThan(initialReads)
  await expect
    .poll(() =>
      rows.evaluateAll((rows) => rows.map((row) => row.getAttribute("data-workspace-item"))),
    )
    .toEqual(before)
  await page.getByRole("button", { name: "Pause updates", exact: true }).click()
  await page.clock.fastForward(1000)
  const pausedReads = reads
  await page.clock.fastForward(30000)
  expect(reads).toBe(pausedReads)
  await page.keyboard.press("F5")
  await expect.poll(() => reads).toBeGreaterThan(pausedReads)
})

test("automatic focus retains the table, scroll and readings while its next ranking is slow", async ({
  page,
}) => {
  await mockHost(page)
  let stream: WebSocketRoute | undefined
  await page.routeWebSocket("**/api/v1/system/stream**", (socket) => {
    stream = socket
  })
  const many = Array.from({ length: 80 }, (_, i) =>
    process({ pid: 2000 + i, name: `worker-${i}`, cpuPercent: 80 - i / 2 }),
  )
  let release: (() => void) | undefined
  await page.route("**/api/v1/processes/inventory*", async (route) => {
    const sort = new URL(route.request().url()).searchParams.get("sort")
    if (sort === "memory") await new Promise<void>((resolve) => (release = resolve))
    return json(route, { ...inventory, processes: many, total: many.length })
  })
  await page.setViewportSize({ width: 1720, height: 1000 })
  await page.goto("/processes")
  await expect(page.getByText(/sorted by highest disk I\/O/)).toBeVisible()
  await expect.poll(() => stream).toBeTruthy()
  const table = page.locator("[data-slot=table-container]:visible")
  const node = (await table.elementHandle())!
  await table.evaluate((element) => (element.scrollTop = 250))
  const scroll = await table.evaluate((element) => element.scrollTop)
  expect(scroll).toBeGreaterThan(0)
  stream!.send(
    JSON.stringify({
      type: "metrics",
      ts: Date.now(),
      data: {
        ts: new Date().toISOString(),
        cpu: { totalPercent: 25, cores: 2, loadAvg1: 0.4 },
        memory: { total: 8589934592, available: 3221225472, usedPercent: 50 },
        swap: { total: 0, used: 0, usedPercent: 0 },
        pressure: { supported: true, cpuSome: 0, memSome: 10, ioSome: 0 },
        procs: { blocked: 0, running: 11, total: 143 },
        uptimeSeconds: 86400,
        mounts: [],
        net: [],
      },
    }),
  )
  await expect.poll(() => release).toBeTruthy()
  expect(await node.evaluate((element) => element.isConnected)).toBe(true)
  expect(await table.evaluate((element) => element.scrollTop)).toBe(scroll)
  await expect(page.getByText(/sorted by highest disk I\/O/)).toBeVisible()
  release!()
  await expect(page.getByText(/sorted by highest memory/)).toBeVisible()
  expect(await node.evaluate((element) => element.isConnected)).toBe(true)
  expect(await table.evaluate((element) => element.scrollTop)).toBe(scroll)
})

for (const [surface, width] of (["live", "pm2", "services", "timers"] as const).flatMap((surface) =>
  [390, 1720].map((width) => [surface, width] as const),
)) {
  test(`${surface} at ${width}: a background state change preserves hovered rows and their open menu`, async ({
    page,
  }) => {
    await page.clock.install()
    await mockHost(page)
    await page.setViewportSize({ width, height: 1000 })
    if (width === 390) await page.emulateMedia({ reducedMotion: "reduce" })
    const fixtures = {
      live: {
        path: "/processes",
        endpoint: "**/api/v1/processes/inventory*",
        selector: "[data-workspace-item]:visible",
        data: inventory,
        changed: {
          ...inventory,
          processes: [...processes].reverse().map((p) => ({ ...p, state: "stopped" })),
        },
        reading: "stopped",
      },
      pm2: {
        path: "/processes/pm2",
        endpoint: "**/api/v1/pm2/",
        selector: "[data-process-row]:visible, [data-workspace-item]:visible",
        data: pm2,
        changed: {
          ...pm2,
          processes: [...pm2.processes].reverse().map((p) => ({ ...p, status: "stopped" })),
        },
        reading: "stopped",
      },
      services: {
        path: "/processes/services",
        endpoint: "**/api/v1/systemd/",
        selector: "[data-process-row]:visible, [data-workspace-item]:visible",
        data: units,
        changed: {
          ...units,
          units: [...units.units].reverse().map((u) => ({ ...u, activeState: "inactive" })),
        },
        reading: "inactive",
      },
      timers: {
        path: "/processes/scheduled",
        endpoint: "**/api/v1/systemd/timers",
        selector: "[data-process-row]:visible",
        data: timers,
        changed: {
          ...timers,
          timers: [...timers.timers].reverse().map((t) => ({ ...t, activeState: "inactive" })),
        },
        reading: "stopped",
      },
    }
    const fixture = fixtures[surface]
    let changed = false
    let failed = false
    let reads = 0
    await page.route(fixture.endpoint, (route) => {
      reads++
      if (failed)
        return route.fulfill({
          status: 503,
          contentType: "application/json",
          body: JSON.stringify({
            error: { code: "unavailable", message: "Temporary read failure" },
          }),
        })
      return json(route, changed ? fixture.changed : fixture.data)
    })
    await page.goto(fixture.path)
    const rows = page.locator(fixture.selector)
    await expect(rows.first()).toBeVisible()
    const names = () =>
      rows.evaluateAll((elements) =>
        elements.map(
          (element) =>
            element.getAttribute("data-process-row") ?? element.getAttribute("data-workspace-item"),
        ),
      )
    const before = await names()
    const first = (await rows.first().elementHandle())!
    await rows.first().hover()
    // Hold starts on hover, before pressing a control gives the row focus.
    changed = true
    const initialReads = reads
    await page.clock.fastForward(31000)
    await expect.poll(() => reads).toBeGreaterThan(initialReads)
    await expect(rows.first()).toContainText(fixture.reading)
    expect(await names()).toEqual(before)
    expect(await first.evaluate((element) => element.isConnected)).toBe(true)
    expect(
      await rows.evaluateAll((elements) =>
        elements.some((row) => row.classList.contains("animate-rise")),
      ),
    ).toBe(false)
    await rows.first().getByRole("button", { name: "More actions" }).click()
    const menu = page.getByRole("menu")
    await expect(menu).toBeVisible()
    const menuNode = (await menu.elementHandle())!
    changed = false
    const menuReads = reads
    await page.clock.fastForward(31000)
    await expect.poll(() => reads).toBeGreaterThan(menuReads)
    await expect(menu).toBeVisible()
    expect(await menuNode.evaluate((element) => element.isConnected)).toBe(true)
    expect(await names()).toEqual(before)
    failed = true
    const failedReads = reads
    await page.clock.fastForward(31000)
    await expect.poll(() => reads).toBeGreaterThan(failedReads)
    await expect(menu).toBeVisible()
    expect(await first.evaluate((element) => element.isConnected)).toBe(true)
    failed = false
    changed = true
    const finalReads = reads
    await page.clock.fastForward(31000)
    await expect.poll(() => reads).toBeGreaterThan(finalReads)
    await expect(rows.first()).toContainText(fixture.reading)
    await page.keyboard.press("Escape")
    await expect(menu).toBeHidden()
    // The menu restores trigger focus asynchronously before inspection can be released.
    await expect(rows.first().getByRole("button", { name: "More actions" })).toBeFocused()
    await page.evaluate(() => (document.activeElement as HTMLElement)?.blur())
    await page.mouse.move(0, 0)
    await expect
      .poll(names)
      .toEqual(surface === "pm2" ? ["deploy:0", "deploy:2", "deploy:1"] : [...before].reverse())
  })
}

test("cron disable updates its existing row and keeps keyboard focus on the toggle", async ({
  page,
}) => {
  await mockHost(page)
  let disabled = false
  await page.route("**/api/v1/cron/user/root", (route) => {
    if (route.request().method() === "PUT") {
      disabled = true
      return json(route, { exitCode: 0 })
    }
    return json(route, {
      ...guardedCrontab,
      jobs: crontab.jobs.map((job, index) =>
        index === 0 && disabled ? { ...job, disabled: true, raw: `# ${job.raw}` } : job,
      ),
    })
  })
  await page.goto("/processes/scheduled")
  const row = page.getByRole("row", { name: /backup/ }).first()
  const node = (await row.elementHandle())!
  const button = (await row.getByRole("button", { name: "Disable", exact: true }).elementHandle())!
  await row.getByRole("button", { name: "Disable", exact: true }).click()
  await expect(row.getByRole("button", { name: "Enable", exact: true })).toBeFocused()
  expect(await node.evaluate((element) => element.isConnected)).toBe(true)
  expect(await button.evaluate((element) => element.isConnected)).toBe(true)
})

test("a timer opened by deep link holds the list's first loaded order while its sheet is open", async ({
  page,
}) => {
  await page.clock.install()
  await mockHost(page)
  let reversed = false
  let reads = 0
  await page.route("**/api/v1/systemd/timers", (route) => {
    reads++
    return json(route, {
      ...timers,
      timers: reversed ? [...timers.timers].reverse() : timers.timers,
    })
  })
  await page.goto("/processes/scheduled?timer=certbot.timer")
  await expect(page.getByRole("dialog")).toBeVisible()
  const names = () =>
    page
      .locator("[data-process-row]")
      .evaluateAll((elements) =>
        elements.map((element) => element.getAttribute("data-process-row")),
      )
  await expect.poll(names).toEqual(["certbot.timer", "fstrim.timer"])
  reversed = true
  const initialReads = reads
  await page.clock.fastForward(31000)
  await expect.poll(() => reads).toBeGreaterThan(initialReads)
  expect(await names()).toEqual(["certbot.timer", "fstrim.timer"])
  await expect(page.getByRole("dialog")).toBeVisible()
})

for (const surface of ["pm2", "services"] as const) {
  test(`${surface}: start and stop refresh the existing table in place`, async ({ page }) => {
    await mockHost(page)
    await page.setViewportSize({ width: 1720, height: 1000 })
    const isPM2 = surface === "pm2"
    let started = false
    await page.route(`**/api/v1/${isPM2 ? "pm2" : "systemd"}/**`, (route) => {
      const path = new URL(route.request().url()).pathname
      if (route.request().method() === "POST" && /\/(start|stop)$/.test(path)) {
        started = path.endsWith("/start")
        return json(route, { exitCode: 0 })
      }
      if (path.endsWith(isPM2 ? "/pm2/" : "/systemd/")) {
        return json(
          route,
          isPM2
            ? {
                ...pm2,
                processes: pm2.processes.map((p) =>
                  p.id === 1 ? { ...p, status: started ? "online" : "stopped" } : p,
                ),
              }
            : {
                ...units,
                units: units.units.map((u) =>
                  u.name === "apt-daily.service"
                    ? {
                        ...u,
                        activeState: started ? "active" : "inactive",
                        subState: started ? "running" : "dead",
                      }
                    : u,
                ),
              },
        )
      }
      return route.fallback()
    })
    await page.goto(`/processes/${surface}`)
    const row = page.getByRole("row", { name: isPM2 ? /worker/ : /apt-daily/ })
    await expect(row).toBeVisible()
    const node = (await row.elementHandle())!
    const table = (await page.locator("[data-slot=table-container]:visible").elementHandle())!
    await row.getByRole("button", { name: "Start", exact: true }).click()
    await expect(row).toContainText(isPM2 ? "online" : "running")
    expect(await node.evaluate((element) => element.isConnected)).toBe(true)
    expect(await table.evaluate((element) => element.isConnected)).toBe(true)
    await row.getByRole("button", { name: "Stop", exact: true }).click()
    await page.getByRole("dialog").getByRole("button", { name: "Stop", exact: true }).click()
    await expect(row).toContainText(isPM2 ? "stopped" : "inactive")
    expect(await node.evaluate((element) => element.isConnected)).toBe(true)
    expect(await table.evaluate((element) => element.isConnected)).toBe(true)
  })
}

test("cron edits preserve a nonzero hourly minute and keep a conflicted draft", async ({
  page,
}) => {
  await mockHost(page)
  const raw = "MAILTO=ops@example.com\n15 * * * * /usr/local/bin/hourly\n"
  const revision = createHash("sha256").update(raw).digest("hex")
  const writes: { content: string; expectedRevision: string }[] = []
  await page.route("**/api/v1/cron/user/root", async (route) => {
    if (route.request().method() === "PUT") {
      writes.push(route.request().postDataJSON())
      return route.fulfill({
        status: 409,
        contentType: "application/json",
        body: JSON.stringify({
          error: "content_conflict",
          message: "Crontab changed since it was opened",
        }),
      })
    }
    return json(route, {
      ...guardedCrontab,
      raw,
      revision,
      timeZone: "America/New_York",
      jobs: [
        {
          line: 2,
          schedule: "15 * * * *",
          command: "/usr/local/bin/hourly",
          raw: "15 * * * * /usr/local/bin/hourly",
          disabled: false,
          timeZone: "America/New_York",
        },
      ],
    })
  })
  await page.goto("/processes/scheduled")
  await page
    .getByRole("row", { name: /hourly/ })
    .getByRole("button", { name: "Edit", exact: true })
    .click()
  const form = page.getByRole("dialog", { name: "Edit cron job" })
  await expect(form.getByLabel("Expression", { exact: true })).toHaveValue("15 * * * *")
  await form.getByLabel("Command", { exact: true }).fill("/usr/local/bin/hourly-updated")
  await form.getByRole("button", { name: "Save", exact: true }).click()
  await expect.poll(() => writes.length).toBe(1)
  expect(writes[0]).toEqual({
    content: "MAILTO=ops@example.com\n15 * * * * /usr/local/bin/hourly-updated\n",
    expectedRevision: revision,
  })
  await expect(form).toContainText("Crontab was not saved")
  await expect(form.getByLabel("Command", { exact: true })).toHaveValue(
    "/usr/local/bin/hourly-updated",
  )
  const storage = await page.evaluate(() => JSON.stringify({ ...sessionStorage, ...localStorage }))
  expect(storage).not.toContain("MAILTO=ops@example.com")
  expect(storage).not.toContain("/usr/local/bin/hourly-updated")
})

test("adding a cron job waits for the existing tab and sends its revision", async ({ page }) => {
  await mockHost(page)
  let release!: () => void
  const waiting = new Promise<void>((resolve) => {
    release = resolve
  })
  const writes: { content: string; expectedRevision: string }[] = []
  await page.route("**/api/v1/cron/user/root", async (route) => {
    if (route.request().method() === "PUT") {
      writes.push(route.request().postDataJSON())
      return json(route, { ok: true })
    }
    await waiting
    return json(route, guardedCrontab)
  })
  await page.goto("/processes/scheduled")
  await expect(page.getByRole("button", { name: "Add job", exact: true })).toBeDisabled()
  release()
  await page.getByRole("button", { name: "Add job", exact: true }).click()
  const form = page.getByRole("dialog", { name: "Add cron job" })
  await form.getByLabel("Command", { exact: true }).fill("/usr/local/bin/new-job")
  await form.getByRole("button", { name: "Add job", exact: true }).click()
  await expect.poll(() => writes.length).toBe(1)
  expect(writes[0].content).toContain(guardedCrontab.raw)
  expect(writes[0].content).toContain("/usr/local/bin/new-job")
  expect(writes[0].expectedRevision).toBe(guardedCrontab.revision)
})
