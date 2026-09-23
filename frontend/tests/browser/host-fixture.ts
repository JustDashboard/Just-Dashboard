import type { Page, Route } from "@playwright/test"

/**
 * One mocked host — its identity, a live snapshot, an hour of recorded
 * history with one spike in it, storage, processes and a health verdict —
 * shared by the pages that describe the machine: the Overview and Metrics.
 */

export const now = Date.now()
export const iso = (ms: number) => new Date(ms).toISOString()

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
    lastLoginAt: iso(now),
    createdAt: iso(now),
  },
}

export const host = {
  hostname: "atlas",
  os: "linux",
  platform: "ubuntu",
  platformVersion: "24.04",
  kernelVersion: "6.8.0-45-generic",
  kernelArch: "x86_64",
  virtualization: "kvm guest",
  bootTime: iso(now - 86_400_000 * 12),
  uptimeSeconds: 86_400 * 12,
  processes: 184,
  cpuModel: "AMD EPYC 7B13",
  cpuCores: 4,
  cpuMhz: 2450,
}

export const snapshot = {
  ts: iso(now),
  cpu: {
    totalPercent: 23.4,
    perCore: [31, 18, 25, 19],
    loadAvg1: 0.92,
    loadAvg5: 0.71,
    loadAvg15: 0.6,
    cores: 4,
    modes: {
      user: 17,
      system: 5,
      nice: 0,
      iowait: 1.2,
      irq: 0,
      softirq: 0.2,
      steal: 0.1,
      idle: 76.5,
    },
  },
  memory: {
    total: 16 * 1024 ** 3,
    used: 9 * 1024 ** 3,
    free: 2 * 1024 ** 3,
    available: 6.5 * 1024 ** 3,
    cached: 4 * 1024 ** 3,
    buffers: 0.5 * 1024 ** 3,
    usedPercent: 56.3,
  },
  swap: { total: 2 * 1024 ** 3, used: 0.1 * 1024 ** 3, free: 1.9 * 1024 ** 3, usedPercent: 5 },
  mounts: [
    {
      device: "/dev/vda1",
      mountpoint: "/",
      fstype: "ext4",
      total: 80 * 1024 ** 3,
      used: 69.6 * 1024 ** 3,
      free: 10.4 * 1024 ** 3,
      usedPercent: 87,
      inodesTotal: 5_000_000,
      inodesUsed: 900_000,
      readBytes: 0,
      writeBytes: 0,
      readRate: 1.2 * 1024 ** 2,
      writeRate: 3.4 * 1024 ** 2,
      readOps: 40,
      writeOps: 110,
      readLatencyMs: 0.8,
      writeLatencyMs: 2.1,
      busyPercent: 12,
    },
    {
      device: "/dev/vdb",
      mountpoint: "/srv",
      fstype: "xfs",
      total: 500 * 1024 ** 3,
      used: 120 * 1024 ** 3,
      free: 380 * 1024 ** 3,
      usedPercent: 24,
      inodesTotal: 50_000_000,
      inodesUsed: 1_000_000,
      readBytes: 0,
      writeBytes: 0,
      readRate: 0,
      writeRate: 512 * 1024,
      readOps: 0,
      writeOps: 8,
      readLatencyMs: 0,
      writeLatencyMs: 1.1,
      busyPercent: 2,
    },
  ],
  net: [
    {
      interface: "eth0",
      bytesSent: 0,
      bytesRecv: 0,
      packetsSent: 0,
      packetsRecv: 0,
      errIn: 0,
      errOut: 0,
      dropIn: 0,
      dropOut: 0,
      sendRate: 420 * 1024,
      recvRate: 2.3 * 1024 ** 2,
      addrs: ["10.0.0.4/24"],
      isUp: true,
    },
  ],
  uptimeSeconds: 86_400 * 12,
  pressure: { supported: true, cpuSome: 0.8, memSome: 0, memFull: 0, ioSome: 2.4, ioFull: 0.3 },
  sockets: { tcpInUse: 312, tcpTimeWait: 1_840, tcpOrphan: 2, udpInUse: 14, used: 640 },
  procs: { running: 3, blocked: 0, total: 184 },
  files: { open: 4_640, max: 9_223_372 },
  sensors: [
    { name: "coretemp_Package id 0", tempC: 62, high: 95, critical: 100 },
    { name: "nvme_Composite", tempC: 41, high: 70, critical: 85 },
  ],
}

/** An hour of 15-second buckets, quiet apart from one spike halfway through. */
export function history(from: number, to: number, base: number) {
  const step = 15_000
  const points = []
  for (let ts = from; ts <= to; ts += step) {
    const spike = Math.abs(ts - (from + to) / 2) < step
    const cpu = spike ? 88 : base + Math.sin(ts / 200_000) * 5
    points.push({
      ts: iso(ts),
      samples: 1,
      cpu,
      cpuPeak: spike ? 97 : cpu + 3,
      mem: 56,
      memPeak: 57,
      swap: 5,
      swapPeak: 5,
      rx: 2_000_000,
      rxPeak: spike ? 40_000_000 : 2_500_000,
      tx: 400_000,
      txPeak: 500_000,
      diskRead: 1_000_000,
      diskReadPeak: 1_200_000,
      diskWrite: 3_000_000,
      diskWritePeak: 3_500_000,
      load1: spike ? 5.2 : 0.9,
      load1Peak: spike ? 5.2 : 1,
      diskPercent: 87,
      memUsed: 9 * 1024 ** 3,
      cpuUser: cpu * 0.7,
      cpuSystem: cpu * 0.2,
      cpuIowait: cpu * 0.1,
      cpuSteal: 0.1,
      psiCpu: 0.8,
      psiCpuPeak: 1,
      psiMem: 0,
      psiMemPeak: 0,
      psiIo: 2.4,
      psiIoPeak: 3,
      diskReads: 40,
      diskReadsPeak: 60,
      diskWrites: 110,
      diskWritesPeak: 130,
      diskAwait: 1.5,
      diskAwaitPeak: spike ? 45 : 2,
      diskBusy: 12,
      diskBusyPeak: 15,
      tcpConns: 310,
      tcpConnsPeak: 320,
      tcpTimeWait: 1_800,
      load5: 0.7,
      load15: 0.6,
      memAvailable: 6.5 * 1024 ** 3,
      procs: 184,
      procsPeak: 190,
    })
  }
  return {
    from: iso(from),
    to: iso(to),
    stepSeconds: 15,
    sampleIntervalSeconds: 15,
    retentionSeconds: 604_800,
    earliest: iso(now - 86_400_000 * 6),
    points,
  }
}

export function storage(from: number, to: number) {
  const step = 60_000
  const mount = (mountpoint: string, usedPercent: number, inodesPercent: number) => ({
    mountpoint,
    points: Array.from({ length: Math.floor((to - from) / step) + 1 }, (_, i) => ({
      ts: iso(from + i * step),
      samples: 4,
      usedPercent,
      usedPercentPeak: usedPercent,
      used: 0,
      total: 0,
      inodesPercent,
    })),
  })
  return {
    from: iso(from),
    to: iso(to),
    stepSeconds: 60,
    sampleIntervalSeconds: 15,
    retentionSeconds: 604_800,
    earliest: iso(now - 86_400_000 * 6),
    mounts: [mount("/", 87, 18), mount("/srv", 24, 2)],
  }
}

export const processes = [
  {
    pid: 1204,
    name: "postgres",
    cmdline: "postgres: writer",
    username: "postgres",
    cpuPercent: 12.4,
    rss: 812 * 1024 ** 2,
    manager: "systemd",
    managerName: "postgresql.service",
  },
  {
    pid: 2210,
    name: "node",
    cmdline: "node server.js",
    username: "app",
    cpuPercent: 8.1,
    rss: 310 * 1024 ** 2,
    manager: "container",
    managerName: "api",
  },
  {
    pid: 3301,
    name: "caddy",
    cmdline: "caddy run",
    username: "caddy",
    cpuPercent: 1.2,
    rss: 64 * 1024 ** 2,
    manager: "systemd",
    managerName: "caddy.service",
  },
].map((p) => ({
  ...p,
  ppid: 1,
  status: "S",
  memPercent: (p.rss / (16 * 1024 ** 3)) * 100,
  vms: p.rss * 2,
  threads: 4,
  nice: 0,
  createTime: iso(now - 3_600_000),
  state: "sleeping",
  ioReadRate: 1024,
  ioWriteRate: 2048,
}))

export const health = {
  status: "warning",
  findings: [
    {
      id: "disk:/",
      level: "warning",
      title: "/ is filling up",
      detail: "87% used — 10.4 GB free",
      advice: "Scan the mount from the Filesystems panel to see what is taking the space.",
      metric: "disk",
      value: 87,
      threshold: 85,
    },
  ],
  checkedAt: iso(now),
  recorded: true,
}

export async function json(route: Route, body: unknown) {
  await route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify(body) })
}

/** Where a history request points, whether by range or by an explicit span. */
function windowOf(url: URL): { from: number; to: number; explicit: boolean } {
  const from = url.searchParams.get("from")
  const to = url.searchParams.get("to")
  if (from && to) return { from: Number(from) * 1000, to: Number(to) * 1000, explicit: true }
  const range = url.searchParams.get("range") ?? "1h"
  const hours = range.endsWith("d") ? Number(range.slice(0, -1)) * 24 : Number(range.slice(0, -1))
  return { from: now - hours * 3_600_000, to: now, explicit: false }
}

export async function mockHost(page: Page) {
  await page.routeWebSocket("**/api/v1/system/stream**", (socket) => {
    socket.send(JSON.stringify({ type: "host", data: host }))
    socket.send(JSON.stringify({ type: "metrics", data: snapshot }))
  })
  await page.route("**/api/v1/**", async (route) => {
    const url = new URL(route.request().url())
    const path = url.pathname.replace(/^\/api\/v1/, "")
    if (path === "/auth/session") return json(route, user)
    if (path === "/system/host") return json(route, host)
    if (path === "/system/metrics") return json(route, snapshot)
    if (path === "/system/health") return json(route, health)
    if (path === "/system/metrics/history") {
      const w = windowOf(url)
      // The prior window is quieter, so the tiles have a delta to show.
      const prior = w.explicit && w.to <= now - 3_000_000
      return json(route, history(w.from, w.to, prior ? 10 : 20))
    }
    if (path === "/system/metrics/storage") {
      const w = windowOf(url)
      return json(route, storage(w.from, w.to))
    }
    if (path === "/system/metrics/events") {
      return json(route, [
        {
          ts: iso(now - 1_800_000),
          kind: "deploy",
          title: "api deploy failed",
          detail: "release 42 · exit 1",
          severity: "error",
        },
      ])
    }
    if (path === "/processes/") return json(route, processes)
    return json(route, [])
  })
}
