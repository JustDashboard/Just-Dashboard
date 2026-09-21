import { expect, test, type Page, type Route } from "@playwright/test"

/**
 * The metrics page after its design-system pass, against a mocked host.
 *
 * What is checked is the shape the redesign settled on and the features it
 * added, not the chart library: every block on the page is plain but the one
 * holding a table, the ten headline readings are tiles, the moments list can
 * zoom the charts, a zoom is a link, the window exports as a file, and the
 * live feed can be paused.
 * The screenshots at 1280 and 1720 are the eyes the assertions do not have.
 */

const now = Date.now()
const iso = (ms: number) => new Date(ms).toISOString()

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
    lastLoginAt: iso(now),
    createdAt: iso(now),
  },
}

const host = {
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

const snapshot = {
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
function history(from: number, to: number, base: number) {
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

function storage(from: number, to: number) {
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

const processes = [
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

const health = {
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

async function json(route: Route, body: unknown) {
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

async function mockHost(page: Page) {
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

test.beforeEach(async ({ page }) => {
  await mockHost(page)
})

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
    Array.from(
      document.querySelectorAll("[data-slot=page] [data-slot=panel]:not([data-plain])"),
    )
      .filter((el) => !el.querySelector("[data-slot=table-container]"))
      .map((el) => el.outerHTML.slice(0, 120)),
  )
}

test("the metrics page is readings on the page, not boxes", async ({ page }) => {
  await page.goto("/metrics")
  await expect(page.getByRole("heading", { name: "Metrics" })).toBeVisible()
  await expect(page.getByText("CPU peaked at 97%")).toBeVisible({ timeout: 20_000 })

  // Ten headline tiles, hairlines between, nothing around: the tenth is the
  // temperature because this host reports sensors.
  await expect(page.locator("[data-slot=stat-tile]")).toHaveCount(10)
  await expect(page.locator("[data-slot=stat-tile]", { hasText: "Temperature" })).toContainText(
    "62°C",
  )

  // Nothing draws a frame but the Interfaces table (§2): the readings, the
  // charts and the findings are all on the page's own ground.
  expect(await framedNonTables(page), "a framed block that is not a table").toEqual([])

  // What the machine is made of, as a row of facts under the title.
  await expect(page.getByText("AMD EPYC 7B13")).toBeVisible()
  await expect(page.getByText("sampled every 15s, kept 7d")).toBeVisible()

  // The verdict's findings are a plain list, and the failed deploy is a moment.
  await expect(page.getByText("/ is filling up")).toBeVisible()
  await expect(page.getByText("api deploy failed")).toBeVisible()

  // Top processes from the process table, ordered by CPU by default.
  await expect(page.getByText("postgres", { exact: true })).toBeVisible()

  // Sensors appear under Hardware, hottest first.
  await expect(page.getByRole("heading", { name: "Temperatures" })).toBeVisible()
  await expect(page.getByText("nvme_Composite")).toBeVisible()
})

test("a moment zooms the charts and the zoom is a link", async ({ page }) => {
  await page.goto("/metrics")
  await page.getByRole("button", { name: /CPU peaked at 97%/ }).click({ timeout: 20_000 })

  await expect(page).toHaveURL(/[?&]from=\d+&to=\d+/)
  await expect(page.getByText(/^\d+[smhd] window$/)).toBeVisible()
  await expect(page.getByRole("button", { name: "Copy link to this window" })).toBeVisible()

  // Opening the link lands on the same span rather than the named range.
  const url = page.url()
  await page.goto(url)
  await expect(page.getByText(/^\d+[smhd] window$/)).toBeVisible()

  await page.getByRole("button", { name: "Zoom out" }).click()
  await expect(page).not.toHaveURL(/from=/)
})

test("the window exports as a spreadsheet", async ({ page }) => {
  await page.goto("/metrics")
  await expect(page.getByText("CPU peaked at 97%")).toBeVisible({ timeout: 20_000 })
  // The tiles compare this window with the one before it.
  await expect(page.locator("[data-slot=stat-tile]", { hasText: "CPU" }).first()).toContainText("+")

  const download = page.waitForEvent("download")
  await page.getByRole("button", { name: "Export CSV" }).click()
  const file = await download
  expect(file.suggestedFilename()).toMatch(/^atlas-metrics-1h-.*\.csv$/)
  const text = await (await file.createReadStream()).toArray().then((c) => c.join(""))
  expect(text.split("\n")[0]).toMatch(/^time,cpu,cpuPeak,/)
})

test("the live feed can be paused", async ({ page }) => {
  await page.goto("/metrics")
  await expect(page.getByRole("heading", { name: "Metrics" })).toBeVisible()
  await expect(page.getByRole("button", { name: "Pause live feed" })).toHaveCount(0)

  await page.getByRole("radio", { name: "Live" }).click()
  await page.getByRole("button", { name: "Pause live feed" }).click()
  await expect(page.getByRole("button", { name: "Resume live feed" })).toBeVisible()

  // Picking a recorded range drops the pause with it.
  await page.getByRole("radio", { name: "1h" }).click()
  await expect(page.getByRole("button", { name: /live feed/ })).toHaveCount(0)
})

for (const width of [1280, 1720]) {
  test(`looks right at ${width}`, async ({ page }) => {
    await page.setViewportSize({ width, height: 3400 })
    await page.goto("/metrics")
    await expect(page.getByText("CPU peaked at 97%")).toBeVisible({ timeout: 20_000 })
    await expect(page.locator(".recharts-cartesian-grid").first()).toBeAttached({
      timeout: 20_000,
    })
    // The page never scrolls sideways.
    const overflow = await page.evaluate(
      () => document.documentElement.scrollWidth > document.documentElement.clientWidth,
    )
    expect(overflow).toBe(false)
    await page.screenshot({ path: `test-results/metrics-${width}.png`, fullPage: true })
  })
}
