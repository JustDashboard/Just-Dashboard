import { expect, test, type Page, type Route } from "@playwright/test"
import { answerLogs, mockLogSockets, type LogMocks } from "./processes-logs-fixture"

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
}

const pm2 = {
  available: true,
  daemons: [
    {
      account: "deploy",
      home: "/home/deploy",
      dumpSavedAt: earlier,
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
    if (/^\/processes\/\d+$/.test(path)) {
      const pid = Number(path.split("/")[2])
      const row = processes.find((p) => p.pid === pid)
      if (!row) return route.fulfill({ status: 404, body: "{}" })
      return json(route, {
        ...row,
        exe: "/usr/bin/node",
        cwd: "/srv/api",
        fileDescriptors: 1020,
        openFilesLimit: 1024,
        children: 2,
        listening: [{ proto: "tcp", address: "0.0.0.0", port: 3000 }],
        connections: 7,
      })
    }
    if (path === "/pm2/") return json(route, pm2)
    if (path === "/systemd/") return json(route, units)
    if (path === "/systemd/timers") return json(route, timers)
    if (path === "/systemd/nginx.service") return json(route, unitDetail)
    if (path === "/systemd/postgresql.service") return json(route, failedDetail)
    if (path === "/cron/users") return json(route, ["root", "deploy"])
    if (path === "/cron/user/root") return json(route, crontab)
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
  // table's cadence and cap at its right end.
  const identity = page.locator("[data-slot='host-identity']")
  await expect(identity).toContainText("srv-1")
  await expect(identity).toContainText("Ubuntu 24.04")
  await expect(identity.getByRole("button", { name: /Every 4s/ })).toBeVisible()

  // Figures over the whole host, not over the filtered rows — and the
  // products the listed processes are, after the figure.
  await expect(page.locator("[data-slot='stat-tile']").first()).toContainText("143")
  await expect(
    page.locator("[data-slot='stat-tile']").first().locator("img[src='/logos/nginx.svg']"),
  ).toBeVisible()
  // A row is its product: nginx's mark on the nginx row, Node's on node, and
  // a glyph rather than a guess on a kernel worker.
  await expect(page.locator("td img[src='/logos/nginx.svg']")).toHaveCount(1)
  await expect(page.locator("td img[src='/logos/nodejs.svg']").first()).toBeVisible()
  await expect(page.getByText("exited, but the parent has not reaped them")).toBeVisible()
  await expect(page.getByText("waiting on a disk or a lock")).toBeVisible()

  // The process table frames itself because it is a table (§2); nothing else
  // on the page may.
  expect(await framedNonTables(page), "a framed block that is not a table").toEqual([])

  const labels = await menuLabels(page, "4021")
  expect(labels).toEqual(
    expect.arrayContaining([
      "Inspect",
      "Terminate",
      "Kill",
      "Pause",
      "Hang up",
      "Open api",
      "Copy PID",
    ]),
  )

  // The sheet: sockets, the parent chain, and named buttons for the two
  // verbs that matter.
  await page.getByRole("button", { name: "node", exact: true }).first().click()
  await expect(page.getByText("tcp 0.0.0.0:3000")).toBeVisible()
  await expect(page.getByText("7 open connections")).toBeVisible()
  await expect(page.getByText("1020 of 1024")).toBeVisible()
  await expect(page.getByRole("button", { name: "Terminate" })).toBeVisible()
  await expect(page.getByRole("button", { name: "Kill" })).toBeVisible()
  await page.getByRole("tab", { name: "Tree" }).click()
  await expect(page.getByRole("button", { name: /PM2 v6.0.5: God Daemon/ })).toBeVisible()
  await expect(page).toHaveURL(/pid=4021/)
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
  await expect(identity.getByText("Resurrects on boot")).toBeVisible()
  await expect(page.getByText("15 unstable — crashing soon after start")).toBeVisible()
  // Each application as what runs it.
  await expect(page.locator("td img[src='/logos/nodejs.svg']")).toHaveCount(2)

  const labels = await menuLabels(page, "worker")
  expect(labels).toEqual(
    expect.arrayContaining(["Logs", "Reset restart counter", "Flush logs", "Delete from PM2"]),
  )
  const api = await menuLabels(page, "api")
  expect(api).toContain("Scale…")

  await page.getByRole("button", { name: "Start application" }).click()
  await page.getByLabel("Script or ecosystem file").fill("/srv/api/server.js")
  await page.getByLabel("Name").fill("api2")
  await expect(page.getByText("pm2 start /srv/api/server.js --name api2")).toBeVisible()
  await page.keyboard.press("Escape")

  await page.getByRole("button", { name: "Startup and bulk" }).click()
  const bulk = await page.locator("[role='menuitem']").allInnerTexts()
  expect(bulk).toEqual(["Save startup list", "Reload all", "Restart all", "Stop all"])
})

test("services list failed units first and the sheet offers reload where it applies", async ({
  page,
}) => {
  await mockHost(page)
  await page.goto("/processes/services")
  await expect(page.getByText("listed first below")).toBeVisible()
  const names = await page
    .locator("[data-slot='table-row'] [data-slot='table-cell']:first-child button")
    .allInnerTexts()
  expect(names[0]).toBe("postgresql.service")
  // A unit is the product it runs, and the Failed tile says what failed
  // before the table does.
  await expect(page.locator("td img[src='/logos/postgresql.svg']")).toBeVisible()
  await expect(page.locator("td img[src='/logos/nginx.svg']")).toBeVisible()
  await expect(
    page.locator("[data-slot='stat-tile']").nth(1).locator("img[src='/logos/postgresql.svg']"),
  ).toBeVisible()

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
  await expect(page.getByText("Every day at 03:00")).toBeVisible()
  await expect(page.getByText("Every hour at :17")).toBeVisible()
  await expect(page.getByText("runs certbot.service")).toBeVisible()

  // Five readings over the three lists and cron's log: what fires next
  // across cron and the timers together, and the counts none of the lists
  // says alone — among them what cron actually started in the last day.
  const tiles = page.locator("[data-slot='stat-tile']")
  await expect(tiles).toHaveCount(5)
  await expect(tiles.nth(0)).toContainText("Next run")
  await expect(tiles.nth(1)).toContainText("1 disabled · root")
  await expect(tiles.nth(2)).toContainText("Cron runs")
  await expect(tiles.nth(3)).toContainText("of 2 · 1 enabled on boot")
  await expect(tiles.nth(4)).toContainText("1 file owned by packages")
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

  await page.getByRole("button", { name: "Add job" }).click()
  await page.getByLabel("Command").fill("/usr/local/bin/report")
  const dialog = page.getByRole("dialog")
  await expect(dialog.getByText("0 3 * * *", { exact: true })).toBeVisible()
  await expect(dialog.getByText("Every day at 03:00")).toBeVisible()
  await expect(dialog.getByText(/^Next: /)).toBeVisible()
  await page.keyboard.press("Escape")
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
  await page.goto("/processes/services?unit=postgresql.service")
  const sheet = page.getByRole("dialog")
  await sheet.getByRole("tab", { name: "Journal", exact: true }).click()
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

test("sshd's journal is an administrator's, and the sheet says so rather than asking", async ({
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
  await page.goto("/processes/services?unit=ssh.service")
  const sheet = page.getByRole("dialog")
  await sheet.getByRole("tab", { name: "Journal", exact: true }).click()
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
  await page.goto("/processes/services?unit=postgresql.service")
  const sheet = page.getByRole("dialog")
  await sheet.getByRole("tab", { name: "Journal", exact: true }).click()
  await expect(sheet.getByText(refusal)).toBeVisible()
  // A socket retrying a refusal would say the tunnel dropped.
  expect(logs.sockets).toEqual([])
  expect(logs.searches).toEqual([])
})

test("on a phone a unit's lines start at the sheet's edge and its controls stay in reach", async ({
  page,
}) => {
  await page.setViewportSize({ width: 390, height: 844 })
  const logs = await mockHost(page)
  await page.goto("/processes/services?unit=postgresql.service")
  const sheet = page.getByRole("dialog")
  await sheet.getByRole("tab", { name: "Journal", exact: true }).click()
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
  const sheet = page.getByRole("dialog")
  await sheet.getByRole("tab", { name: "Logs", exact: true }).click()
  await expect.poll(() => logs.sockets.at(-1)?.get("source")).toBe("pm2:deploy/1/worker")
  await expect(sheet.getByText("Restarted 16 times")).toBeVisible()
  await expect(sheet.getByText("15 unstable")).toBeVisible()
  await expect(sheet.getByLabel("Log lines").getByText(/ECONNREFUSED/)).toBeVisible()
  // Errored, so there is no last start to read around.
  await expect(sheet.getByRole("button", { name: "Around the last start" })).toHaveCount(0)
  await page.keyboard.press("Escape")

  await page.getByRole("button", { name: "api", exact: true }).click()
  await sheet.getByRole("tab", { name: "Logs", exact: true }).click()
  await expect.poll(() => logs.sockets.at(-1)?.get("source")).toBe("pm2:deploy/0/api")
  const around = sheet.getByRole("button", { name: "Around the last start" })
  const before = logs.searches.length
  await around.click()
  await expect(around).toHaveAttribute("aria-pressed", "true")
  await expect(sheet.getByRole("button", { name: "History", exact: true })).toHaveAttribute(
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
  await sheet.getByRole("button", { name: "Live", exact: true }).click()
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
  // What cron started in the last day, and the runs whose output went nowhere.
  const tile = page.locator("[data-slot='stat-tile']").filter({ hasText: "Cron runs" })
  await expect(tile).toContainText("143")
  await expect(tile).toContainText("2 runs with their output discarded")

  // A timer opens in place on the runs of the service it fires.
  const certbot = page.getByRole("button", { name: "certbot.timer", exact: true })
  await certbot.click()
  await expect(certbot).toHaveAttribute("aria-expanded", "true")
  const runs = page.getByRole("list", { name: "Runs" })
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
  await certbot.click()
  await expect(runs).toHaveCount(0)
})

test.describe("with no hover available", () => {
  test.use({ hasTouch: true, viewport: { width: 390, height: 844 } })

  test("every row's verbs are reachable on a phone", async ({ page }) => {
    await mockHost(page)
    for (const path of ["/processes", "/processes/pm2", "/processes/services"]) {
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

for (const width of [1280, 1720]) {
  test(`looks right at ${width}`, async ({ page }) => {
    await mockHost(page)
    for (const [name, path] of [
      ["live", "/processes"],
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
  await page.keyboard.press("Enter")
  await expect(page.getByRole("dialog")).toBeVisible()
  await page.keyboard.press("Escape")
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
