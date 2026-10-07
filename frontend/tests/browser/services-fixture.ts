import type { Page, Route } from "@playwright/test"
import { answerLogs, mockLogSockets, type LogMocks } from "./processes-logs-fixture"

/**
 * A host's services, as `GET /systemd/` answers them with their live
 * readings: the busy ones (Docker, MongoDB, Prometheus) and the quiet ones,
 * two that failed — Postgres hit its start limit, certbot's nightly run
 * exited 1 — one in a restart loop, the oneshots a timer fired today, and the
 * disabled and masked units nothing starts.
 */

const NOW = Date.now()
const SEC = 1000
const MIN = 60 * SEC
const HOUR = 60 * MIN
const DAY = 24 * HOUR
const unix = (msAgo: number) => Math.floor((NOW - msAgo) / 1000)
const MB = 1024 * 1024

/** Booted three days ago, so a unit started at boot has been up as long. */
const BOOT = 3 * DAY + 4 * HOUR

type Over = Record<string, unknown>

function unit(name: string, description: string, over: Over = {}) {
  return {
    name,
    description,
    loadState: "loaded",
    activeState: "active",
    subState: "running",
    unitFileState: "enabled",
    enabled: true,
    type: "simple",
    activeSince: unix(BOOT - 9 * SEC),
    changedAt: unix(BOOT - 9 * SEC),
    restarts: 0,
    result: "success",
    cpuReady: true,
    cpuPercent: 0,
    ...over,
  }
}

const running = (
  name: string,
  description: string,
  cpu: number,
  memory: number,
  tasks: number,
  pid: number,
  over: Over = {},
) =>
  unit(name, description, {
    cpuPercent: cpu,
    memoryBytes: memory,
    tasks,
    mainPid: pid,
    fragmentPath: `/usr/lib/systemd/system/${name}`,
    ...over,
  })

const exited = (name: string, description: string, over: Over = {}) =>
  unit(name, description, { subState: "exited", type: "oneshot", cpuReady: false, ...over })

const inactive = (name: string, description: string, over: Over = {}) =>
  unit(name, description, {
    activeState: "inactive",
    subState: "dead",
    unitFileState: "static",
    activeSince: undefined,
    cpuReady: false,
    cpuPercent: undefined,
    ...over,
  })

export const UNITS = [
  unit("postgresql.service", "PostgreSQL RDBMS", {
    activeState: "failed",
    subState: "failed",
    type: "notify",
    activeSince: undefined,
    changedAt: unix(12 * MIN),
    result: "start-limit-hit",
    restarts: 4,
    exitCode: "exited",
    exitStatus: 1,
    cpuReady: false,
    cpuPercent: undefined,
    fragmentPath: "/usr/lib/systemd/system/postgresql.service",
  }),
  unit("certbot.service", "Certbot", {
    activeState: "failed",
    subState: "failed",
    unitFileState: "static",
    type: "oneshot",
    activeSince: undefined,
    changedAt: unix(3 * HOUR + 12 * MIN),
    result: "exit-code",
    exitCode: "exited",
    exitStatus: 1,
    cpuReady: false,
    cpuPercent: undefined,
  }),
  unit("redis-server.service", "Advanced key-value store", {
    activeState: "activating",
    subState: "auto-restart",
    type: "notify",
    activeSince: undefined,
    changedAt: unix(4 * SEC),
    result: "exit-code",
    restarts: 7,
    exitCode: "exited",
    exitStatus: 1,
    cpuReady: false,
    cpuPercent: undefined,
  }),
  running("docker.service", "Docker Application Container Engine", 3.4, 412 * MB, 38, 1204, {
    type: "notify",
  }),
  running("mongod.service", "MongoDB Database Server", 6.8, 1.21 * 1024 * MB, 71, 1388),
  running(
    "prometheus.service",
    "Monitoring system and time series database",
    5.1,
    524 * MB,
    14,
    1410,
  ),
  running("containerd.service", "containerd container runtime", 2.2, 182 * MB, 41, 1102, {
    type: "notify",
  }),
  running("pm2-deploy.service", "PM2 process manager", 1.9, 311 * MB, 22, 1530, {
    type: "forking",
  }),
  running("grafana-server.service", "Grafana instance", 1.4, 163 * MB, 18, 1452, {
    type: "notify",
    restarts: 1,
    activeSince: unix(2 * HOUR + 40 * MIN),
    changedAt: unix(2 * HOUR + 40 * MIN),
  }),
  running(
    "nginx.service",
    "A high performance web server and a reverse proxy server",
    0.9,
    46 * MB,
    5,
    812,
    {
      type: "forking",
      activeSince: unix(26 * MIN),
      changedAt: unix(26 * MIN),
    },
  ),
  running("tailscaled.service", "Tailscale node agent", 0.6, 49 * MB, 16, 1077, {
    type: "notify",
  }),
  running("systemd-journald.service", "Journal Service", 0.4, 241 * MB, 1, 402, {
    type: "notify",
    unitFileState: "static",
  }),
  running("php8.3-fpm.service", "The PHP 8.3 FastCGI Process Manager", 0.3, 96 * MB, 6, 1290, {
    type: "notify",
  }),
  running("fail2ban.service", "Fail2Ban Service", 0.2, 38 * MB, 5, 1311),
  running(
    "prometheus-node-exporter.service",
    "Prometheus exporter for machine metrics",
    0.8,
    19 * MB,
    7,
    1420,
  ),
  running("ssh.service", "OpenBSD Secure Shell server", 0, 6.2 * MB, 1, 1035, {
    type: "notify",
  }),
  running("cron.service", "Regular background program processing daemon", 0, 1.1 * MB, 1, 1067),
  running("dbus.service", "D-Bus System Message Bus", 0.1, 4.6 * MB, 1, 840, {
    type: "notify",
    unitFileState: "static",
  }),
  running("systemd-resolved.service", "Network Name Resolution", 0, 12 * MB, 1, 611, {
    type: "notify",
  }),
  running("systemd-networkd.service", "Network Configuration", 0, 9.4 * MB, 1, 598, {
    type: "notify",
  }),
  running("systemd-logind.service", "User Login Management", 0, 7.8 * MB, 1, 905, {
    type: "notify",
    unitFileState: "static",
  }),
  running("systemd-timesyncd.service", "Network Time Synchronization", 0, 6.9 * MB, 2, 640, {
    type: "notify",
  }),
  running(
    "systemd-udevd.service",
    "Rule-based Manager for Device Events and Files",
    0,
    8.1 * MB,
    1,
    455,
    {
      type: "notify",
      unitFileState: "static",
    },
  ),
  running("rsyslog.service", "System Logging Service", 0, 5.2 * MB, 4, 870, { type: "notify" }),
  running("unattended-upgrades.service", "Unattended Upgrades Shutdown", 0, 22 * MB, 2, 1150),
  running("snapd.service", "Snap Daemon", 0.1, 31 * MB, 12, 960, { type: "notify" }),
  exited("ufw.service", "Uncomplicated firewall"),
  exited("apparmor.service", "Load AppArmor profiles"),
  exited("cloud-final.service", "Cloud-init: Final Stage"),
  inactive("apt-daily.service", "Daily apt download activities", {
    type: "oneshot",
    changedAt: unix(41 * MIN),
  }),
  inactive("logrotate.service", "Rotate log files", {
    type: "oneshot",
    changedAt: unix(2 * HOUR + 3 * MIN),
  }),
  inactive("man-db.service", "Daily man-db regeneration", {
    type: "oneshot",
    changedAt: unix(5 * HOUR),
  }),
  inactive("mysql.service", "MySQL Community Server", {
    unitFileState: "disabled",
    enabled: false,
    type: "notify",
    changedAt: unix(6 * HOUR + 30 * MIN),
    fragmentPath: "/usr/lib/systemd/system/mysql.service",
  }),
  inactive("fstrim.service", "Discard unused blocks on filesystems from /etc/fstab", {
    type: "oneshot",
    changedAt: unix(BOOT - 30 * SEC),
  }),
  inactive("apache2.service", "The Apache HTTP Server", {
    unitFileState: "disabled",
    enabled: false,
    type: "forking",
    changedAt: unix(BOOT - 10 * SEC),
  }),
  inactive("bluetooth.service", "Bluetooth service", {
    unitFileState: "masked",
    enabled: false,
    changedAt: unix(BOOT - 10 * SEC),
  }),
  inactive("rescue.service", "Rescue Shell", { changedAt: unix(BOOT - 10 * SEC) }),
  inactive("emergency.service", "Emergency Shell", { changedAt: unix(BOOT - 10 * SEC) }),
  // Named by another unit, with no file on this host.
  inactive("display-manager.service", "display-manager.service", {
    loadState: "not-found",
    unitFileState: "",
    enabled: false,
    changedAt: undefined,
    result: undefined,
  }),
]

export const SYSTEMD_LIST = {
  available: true,
  ratesReady: true,
  manager: {
    version: "257",
    state: "degraded",
    bootedAt: unix(BOOT),
    bootSeconds: 14.2,
  },
  units: UNITS,
}

/** Two minutes of nginx at a five-second step, rising into the request burst at the end. */
const NGINX_HISTORY = Array.from({ length: 36 }, (_, i) => ({
  t: unix((36 - i) * 5 * SEC),
  cpu: 0.4 + Math.abs(Math.sin(i / 3)) * 0.8 + (i > 28 ? 1.4 : 0),
  memory: (44 + Math.sin(i / 5) * 1.5 + (i > 28 ? 2 : 0)) * MB,
}))

const nginx = UNITS.find((u) => u.name === "nginx.service")!

export const NGINX_DETAIL = {
  unit: { ...nginx, history: NGINX_HISTORY },
  properties: {
    Id: "nginx.service",
    Type: "forking",
    User: "",
    Group: "",
    WorkingDirectory: "",
    Restart: "on-failure",
    RestartUSec: "5s",
    CanReload: "yes",
    MemoryMax: "infinity",
    MemoryPeak: String(61 * MB),
    TasksMax: "4915",
    NRestarts: "0",
    ExecStart:
      "{ path=/usr/sbin/nginx ; argv[]=/usr/sbin/nginx -g daemon on; master_process on; ; ignore_errors=no ; start_time=[n/a] ; stop_time=[n/a] ; pid=0 ; code=(null) ; status=0/0 }",
    ExecReload:
      "{ path=/usr/sbin/nginx ; argv[]=/usr/sbin/nginx -g daemon on; master_process on; -s reload ; ignore_errors=no ; start_time=[n/a] ; stop_time=[n/a] ; pid=0 ; code=(null) ; status=0/0 }",
    FragmentPath: "/usr/lib/systemd/system/nginx.service",
    DropInPaths: "/etc/systemd/system/nginx.service.d/override.conf",
    WantedBy: "multi-user.target",
    After: "network-online.target remote-fs.target nss-lookup.target",
    TriggeredBy: "",
  },
}

const postgres = UNITS.find((u) => u.name === "postgresql.service")!

export const POSTGRES_DETAIL = {
  unit: { ...postgres, history: [] },
  properties: {
    Id: "postgresql.service",
    Type: "notify",
    User: "postgres",
    Group: "postgres",
    Restart: "on-failure",
    RestartUSec: "100ms",
    CanReload: "yes",
    MemoryMax: "infinity",
    MemoryPeak: String(1.18 * 1024 * MB),
    TasksMax: "",
    NRestarts: "4",
    ExecStart:
      "{ path=/usr/lib/postgresql/16/bin/postgres ; argv[]=/usr/lib/postgresql/16/bin/postgres -D /var/lib/postgresql/16/main -c config_file=/etc/postgresql/16/main/postgresql.conf ; ignore_errors=no ; start_time=[n/a] ; stop_time=[n/a] ; pid=0 ; code=(null) ; status=0/0 }",
    FragmentPath: "/usr/lib/systemd/system/postgresql.service",
    DropInPaths: "",
    WantedBy: "multi-user.target",
    TriggeredBy: "",
  },
}

const redis = UNITS.find((u) => u.name === "redis-server.service")!

export const REDIS_DETAIL = {
  unit: { ...redis, history: [] },
  properties: {
    Id: "redis-server.service",
    Type: "notify",
    User: "redis",
    Group: "redis",
    Restart: "always",
    RestartUSec: "5s",
    CanReload: "no",
    MemoryMax: String(512 * MB),
    TasksMax: "4915",
    NRestarts: "7",
    ExecStart:
      "{ path=/usr/bin/redis-server ; argv[]=/usr/bin/redis-server /etc/redis/redis.conf --supervised systemd --daemonize no ; ignore_errors=no ; start_time=[n/a] ; stop_time=[n/a] ; pid=0 ; code=(null) ; status=0/0 }",
    FragmentPath: "/usr/lib/systemd/system/redis-server.service",
    WantedBy: "multi-user.target",
  },
}

const DETAILS: Record<string, object> = {
  "nginx.service": NGINX_DETAIL,
  "postgresql.service": POSTGRES_DETAIL,
  "redis-server.service": REDIS_DETAIL,
}

/** nginx's master and its four workers, as the process inventory narrows to its unit. */
const NGINX_PROCESSES = [812, 813, 814, 815, 816].map((pid, i) => ({
  pid,
  ppid: i === 0 ? 1 : 812,
  name: "nginx",
  cmdline:
    i === 0
      ? "nginx: master process /usr/sbin/nginx -g daemon on; master_process on;"
      : "nginx: worker process",
  username: i === 0 ? "root" : "www-data",
  status: "sleeping",
  state: "sleeping",
  cpuPercent: i === 0 ? 0 : [0.4, 0.2, 0.2, 0.1][i - 1],
  cpuReady: true,
  memPercent: 0.1,
  rss: (i === 0 ? 6 : 10) * MB,
  vms: 60 * MB,
  threads: 1,
  nice: 0,
  createTime: new Date(NOW - 26 * MIN).toISOString(),
  manager: "systemd",
  managerName: "nginx.service",
}))

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

export type ServiceMocks = LogMocks & {
  /** Every `/processes/inventory` query the page made. */
  inventories: URLSearchParams[]
  /** Every unit action posted, as `name/action`. */
  actions: string[]
}

/**
 * The host behind the Services page: the session, the machine and its
 * metrics, the unit list and three units' detail, nginx's processes, and the
 * log routes the sheet's journal reads.
 */
export async function mockServices(page: Page, list: object = SYSTEMD_LIST): Promise<ServiceMocks> {
  const mocks: ServiceMocks = { sockets: [], searches: [], inventories: [], actions: [] }
  await mockLogSockets(page, mocks)
  await page.route("**/api/v1/**", async (route) => {
    const url = new URL(route.request().url())
    const path = url.pathname.replace(/^\/api\/v1/, "")
    const method = route.request().method()
    if (answerLogs(route, path, url, mocks)) return
    if (path === "/auth/session") return json(route, user)
    if (path === "/system/host") {
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
    }
    if (path === "/system/metrics") {
      return json(route, {
        ts: new Date().toISOString(),
        cpu: {
          totalPercent: 28.4,
          perCore: [31, 26, 29, 27],
          loadAvg1: 1.12,
          loadAvg5: 0.94,
          loadAvg15: 0.88,
          cores: 4,
          modes: { user: 20, system: 6, iowait: 1, steal: 0, idle: 72 },
        },
        memory: {
          total: 8 * 1024 * MB,
          used: 4.6 * 1024 * MB,
          free: 1024 * MB,
          available: 3.1 * 1024 * MB,
          cached: 2 * 1024 * MB,
          buffers: 0,
          usedPercent: 57,
        },
        swap: { total: 0, used: 0, free: 0, usedPercent: 0 },
        mounts: [],
        net: [],
        uptimeSeconds: BOOT / 1000,
        pressure: { supported: true, cpuSome: 1, memSome: 0, ioSome: 0 },
        sockets: { tcpInUse: 40 },
        procs: { running: 4, blocked: 0, total: 312 },
      })
    }
    if (path === "/updates/self") return json(route, { current: "0.7.1", latest: "0.7.1" })
    if (path === "/systemd/") return json(route, list)
    if (path === "/systemd/daemon-reload" && method === "POST") return json(route, { exitCode: 0 })
    if (path === "/processes/inventory") {
      mocks.inventories.push(url.searchParams)
      const group = url.searchParams.get("group")
      const rows = group === "systemd:nginx.service" ? NGINX_PROCESSES : []
      return json(route, {
        processes: rows,
        total: rows.length,
        available: 312,
        truncated: false,
        ratesReady: true,
        users: [],
        states: [],
        managers: [],
      })
    }
    const action = path.match(/^\/systemd\/([^/]+)\/([a-z-]+)$/)
    if (action && method === "POST") {
      mocks.actions.push(`${decodeURIComponent(action[1])}/${action[2]}`)
      return json(route, { exitCode: 0, stdout: "", stderr: "", command: "systemctl" })
    }
    const detail = path.match(/^\/systemd\/([^/]+)$/)
    if (detail) {
      const name = decodeURIComponent(detail[1])
      const known = DETAILS[name]
      if (known) return json(route, known)
      const listed = UNITS.find((u) => u.name === name)
      if (listed) return json(route, { unit: { ...listed, history: [] }, properties: {} })
      return route.fulfill({ status: 404, body: "{}" })
    }
    return json(route, {})
  })
  return mocks
}
