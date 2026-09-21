import { expect, test, type Page, type Route } from "@playwright/test"

/**
 * The Processes section, checked in a browser against a mocked host.
 *
 * What these assert is the part a type check cannot: that every verb the
 * backend offers is reachable from a row — terminate and kill on a process,
 * reset and flush on a PM2 application, clear-failed on a unit, disable on a
 * cron job — that a schedule is described in words beside its expression, and
 * that the pages read as the design system says: figures as tiles, one plain
 * panel, no framed block on the page. The screenshots at 1280 and 1720 are
 * the eyes the assertions do not have.
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

async function mockHost(page: Page) {
  await page.route("**/api/v1/**", async (route) => {
    const url = new URL(route.request().url())
    const path = url.pathname.replace(/^\/api\/v1/, "")
    const method = route.request().method()
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
}

/** Every verb this page offers has to be a word, reachable from a row. */
async function menuLabels(page: Page, rowName: string): Promise<string[]> {
  const row = page.getByRole("row", { name: new RegExp(rowName) }).first()
  await row.hover()
  await row.getByRole("button", { name: "More actions" }).click()
  // The word, not the sentence under it.
  const items = await page.locator("[role='menuitem'] span.font-medium").allInnerTexts()
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
    Array.from(
      document.querySelectorAll("[data-slot=page] [data-slot=panel]:not([data-plain])"),
    )
      .filter((el) => !el.querySelector("[data-slot=table-container]"))
      .map((el) => el.outerHTML.slice(0, 120)),
  )
}

test("the live table reads the host and every process verb is a word", async ({ page }) => {
  await mockHost(page)
  await page.goto("/processes")
  await expect(page.getByRole("heading", { name: "Live" })).toBeVisible()

  // Figures over the whole host, not over the filtered rows.
  await expect(page.locator("[data-slot='stat-tile']").first()).toContainText("143")
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

test("PM2 says whether it survives a reboot and offers the housekeeping verbs", async ({
  page,
}) => {
  await mockHost(page)
  await page.goto("/processes/pm2")
  await expect(page.getByText("Resurrects on boot")).toBeVisible()
  await expect(page.getByText("15 unstable — crashing soon after start")).toBeVisible()

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
  const bulk = await page.locator("[role='menuitem'] span.font-medium").allInnerTexts()
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
