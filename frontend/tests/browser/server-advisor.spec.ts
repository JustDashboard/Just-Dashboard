import { expect, test, type Page } from "@playwright/test"
import { mockHost, json, health, user, now, iso } from "./host-fixture"

const temporary = {
  path: "/tmp/old-export.tar",
  size: 4096,
  allocated: 4096,
  modified: iso(now - 9 * 86400000),
  identity: "temporary-inode",
  links: 1,
}
const keeper = {
  ...temporary,
  path: "/var/data/a.bin",
  size: 1048576,
  allocated: 1048576,
  identity: "keeper-inode",
}
const copy = { ...keeper, path: "/var/data/b.bin", identity: "copy-inode" }
const process = {
  pid: 4200,
  ppid: 1,
  name: "batch-worker",
  cmdline: "/usr/bin/batch-worker --queue reports",
  username: "worker",
  status: "running",
  state: "running",
  cpuPercent: 175,
  cpuReady: true,
  cpuWindowSeconds: 1.2,
  rss: 2147483648,
  vms: 3221225472,
  swap: 1048576,
  memoryReady: true,
  ioReady: true,
  ioReadRate: 4000,
  ioWriteRate: 2000,
  threads: 4,
  nice: 0,
  createTime: iso(now - 86400000),
  manager: "systemd",
  managerName: "batch-worker.service",
}

async function mockAdvisor(
  page: Page,
  options: { reader?: boolean; partial?: boolean; workloads?: boolean; hostMount?: boolean } = {},
) {
  await mockHost(page)
  let cleaned = false
  let reads = 0
  const mutations: { path: string; body: unknown }[] = []
  await page.route("**/api/v1/**", async (route) => {
    const url = new URL(route.request().url())
    const path = url.pathname.replace(/^\/api\/v1/, "")
    if (path === "/auth/session" && options.reader)
      return json(route, { ...user, capabilities: ["read"] })
    if (path === "/system/health" && options.workloads)
      return json(route, {
        ...health,
        findings: [
          {
            id: "psi-cpu",
            level: "warning",
            title: "Tasks are waiting for CPU",
            detail: "32% of the last 10 seconds stalled",
            advice: "Inspect the actual consumers",
            value: 32,
            threshold: 30,
          },
        ],
      })
    if (path === "/system/advisor/storage") {
      reads++
      return json(route, {
        path: options.hostMount ? "/host" : url.searchParams.get("path"),
        requestedPath: options.hostMount ? url.searchParams.get("path") : undefined,
        hostFilesystem: options.hostMount,
        checkedAt: iso(now + reads * 1000),
        complete: !options.partial,
        entries: 185,
        allocated: cleaned ? 13000000 : 15000000,
        directories: [{ path: "/var/data", allocated: 12000000, entries: 120 }],
        inodeDirectories: [{ path: "/var/cache/small-files", allocated: 300000, entries: 800 }],
        largeFiles: [keeper, ...(cleaned ? [] : [copy])],
        temporaryFiles: cleaned ? [] : [temporary],
        duplicates:
          url.searchParams.get("duplicates") === "true" && !cleaned
            ? [{ sha256: "a".repeat(64), files: [keeper, copy], reclaimable: copy.allocated }]
            : [],
        silences: options.partial ? [{ path: "/var/private", reason: "Permission denied" }] : [],
        skippedMounts: 2,
        hashedBytes: 2097152,
        duplicateScanRequested: url.searchParams.get("duplicates") === "true",
        duplicateScanComplete: !options.partial,
        duplicateScope: "Exact copies from up to 2048 files, 1–128 MiB; up to 256 MiB read.",
        filesystem: {
          total: 80000000000,
          available: cleaned ? 11000000000 : 10000000000,
          freeInodes: 50000,
          totalInodes: 500000,
        },
      })
    }
    if (path === "/system/advisor/storage/cleanup") {
      const body = route.request().postDataJSON()
      mutations.push({ path, body })
      cleaned = true
      return json(route, {
        items: body.selections.map((item: { file: typeof temporary }) => ({
          path: item.file.path,
          removed: true,
          allocated: item.file.allocated,
        })),
        removedBytes: body.selections.reduce(
          (sum: number, item: { file: typeof temporary }) => sum + item.file.allocated,
          0,
        ),
      })
    }
    if (path === "/system/advisor/workloads")
      return json(route, {
        checkedAt: iso(now),
        sort: url.searchParams.get("sort"),
        processes: [process],
        total: 184,
        silences: ["CPU intervals unavailable for 2 processes (new, replaced or unreadable)."],
      })
    if (path === "/processes/4200/signal" || path === "/processes/4200/priority") {
      mutations.push({ path, body: route.request().postDataJSON() })
      return route.fulfill({ status: 204 })
    }
    return route.fallback()
  })
  return { mutations, reads: () => reads }
}

async function openInvestigation(page: Page, title = "/ is filling up") {
  await page.goto("/metrics")
  await page.getByRole("button", { name: title, exact: false }).click()
  await page.getByRole("button", { name: "Investigate", exact: true }).click()
  return page.getByRole("dialog", { name: title, exact: true })
}

test("storage evidence is requested on demand and reviewed cleanup retains a copy", async ({
  page,
}) => {
  const state = await mockAdvisor(page)
  await page.goto("/metrics")
  await expect(page.getByRole("button", { name: "/ is filling up", exact: false })).toBeVisible({
    timeout: 15000,
  })
  expect(state.reads()).toBe(0)
  const sheet = await openInvestigation(page)
  await expect(sheet.getByText("Largest directories", { exact: true })).toBeVisible()
  await sheet.getByRole("button", { name: "Check exact copies" }).click()
  await sheet.getByRole("checkbox", { name: `Select ${copy.path}` }).check()
  await expect(sheet.getByRole("checkbox", { name: `Select ${keeper.path}` })).toHaveCount(0)
  await sheet.getByRole("button", { name: "Remove selected files" }).click()
  expect(state.mutations).toHaveLength(0)
  const confirmation = page.getByRole("dialog", { name: "Remove selected files", exact: true })
  await expect(confirmation.getByText(`Keep: ${keeper.path}`)).toBeVisible()
  await confirmation.getByRole("button", { name: "Remove files", exact: true }).click()
  await expect.poll(() => state.mutations.length).toBe(1)
  const body = state.mutations[0].body as {
    selections: { file: typeof copy; keeper: typeof keeper }[]
  }
  expect(body.selections).toHaveLength(1)
  expect(body.selections[0].file.identity).toBe(copy.identity)
  expect(body.selections[0].keeper.path).toBe(keeper.path)
  await expect(sheet.getByText(`${copy.path}: removed`, { exact: true })).toBeVisible()
  await expect(sheet.getByText("10.2 GB", { exact: true })).toBeVisible()
})

test("partial scans stay explicit and readers can inspect without cleanup controls", async ({
  page,
}) => {
  await mockAdvisor(page, { reader: true, partial: true })
  const sheet = await openInvestigation(page)
  await expect(
    sheet.getByText("This scan is partial. Unread or unvisited paths can contain more data."),
  ).toBeVisible()
  await expect(sheet.getByText("/var/private: Permission denied")).toBeVisible()
  await expect(sheet.getByRole("checkbox", { name: `Select ${temporary.path}` })).toBeDisabled()
  await expect(sheet.getByRole("button", { name: "Remove selected files" })).toBeDisabled()
  await sheet.getByRole("button", { name: "Scan here" }).click()
  await expect(sheet.getByText("/var/data", { exact: true }).first()).toBeVisible()
})

test("workload actions use measured process identity and wait for confirmation", async ({
  page,
}) => {
  const state = await mockAdvisor(page, { workloads: true })
  const sheet = await openInvestigation(page, "Tasks are waiting for CPU")
  await expect(sheet.getByText("175.0% · 1.2s")).toBeVisible()
  await expect(sheet.getByRole("link", { name: "Manage systemd" })).toHaveAttribute(
    "href",
    "/processes/services?unit=batch-worker.service",
  )
  await sheet.getByRole("button", { name: "Lower priority" }).click()
  expect(state.mutations).toHaveLength(0)
  await page
    .getByRole("dialog", { name: "Lower process priority" })
    .getByRole("button", { name: "Lower priority" })
    .click()
  await expect.poll(() => state.mutations.length).toBe(1)
  expect(state.mutations[0]).toEqual({
    path: "/processes/4200/priority",
    body: { nice: 5, startedAt: process.createTime },
  })
  await sheet.getByRole("button", { name: "Terminate", exact: true }).click()
  await page
    .getByRole("dialog", { name: "Terminate process" })
    .getByRole("button", { name: "Send SIGTERM" })
    .click()
  await expect.poll(() => state.mutations.length).toBe(2)
  expect(state.mutations[1]).toEqual({
    path: "/processes/4200/signal",
    body: { signal: "SIGTERM", startedAt: process.createTime },
  })
})

test("storage investigation fits desktop and mobile widths", async ({ page }, testInfo) => {
  await mockAdvisor(page)
  const sheet = await openInvestigation(page)
  for (const width of [1280, 1720, 390]) {
    await page.setViewportSize({ width, height: 950 })
    await expect(sheet.getByRole("button", { name: "Scan again" })).toBeVisible()
    await expect
      .poll(() => sheet.evaluate((element) => element.scrollWidth <= element.clientWidth))
      .toBe(true)
    await expect
      .poll(async () => {
        const bounds = await sheet.boundingBox()
        return !!bounds && bounds.x >= -1 && bounds.x + bounds.width <= width + 1
      })
      .toBe(true)
    await page.screenshot({ path: testInfo.outputPath(`advisor-${width}.png`) })
  }
})

async function mockDockerAdvisor(page: Page, compose = false) {
  await mockAdvisor(page)
  const mutations: { method: string; path: string; body: unknown }[] = []
  const container = {
    id: "web-id",
    name: "web",
    names: ["web"],
    image: "nginx:1.27",
    state: "running",
    status: "Up 2 hours",
    ports: [],
    labels: {},
    networks: [],
    createdAt: iso(now),
    memoryLimit: 0,
  }
  const finding = {
    id: "container.norestart.web-id",
    severity: "recommendation",
    class: "configuration",
    title: "web has no restart policy",
    detail: "The service will not start after a reboot.",
    target: "web",
    targetId: "web-id",
    scope: "container",
    action: "set-restart",
    actionLabel: "Set a restart policy",
  }
  await page.route("**/api/v1/docker/**", async (route) => {
    const path = new URL(route.request().url()).pathname.replace(/^\/api\/v1/, "")
    if (route.request().method() !== "GET") {
      mutations.push({
        method: route.request().method(),
        path,
        body: route.request().postDataJSON(),
      })
      return json(route, { warnings: [] })
    }
    if (path === "/docker/health")
      return json(route, {
        status: "notice",
        checked: 1,
        checkedAt: iso(now),
        findings: [finding],
        attention: { total: 1, issues: 0, recommendations: 1, critical: 0, warning: 0, info: 0 },
        runtime: {
          status: "ok",
          total: 1,
          running: 1,
          healthy: 0,
          noHealthcheck: 1,
          starting: 0,
          unhealthy: 0,
          paused: 0,
          restarting: 0,
          dead: 0,
          removing: 0,
          exited: 0,
          created: 0,
        },
      })
    if (path === "/docker/containers/") return json(route, [container])
    if (path === "/docker/containers/web-id")
      return json(route, {
        ...container,
        composeStack: compose ? "website" : undefined,
        env: [],
        mounts: [],
        writableBytes: 0,
        health: { status: "none", failingStreak: 0, log: [] },
      })
    if (path === "/docker/disk-usage")
      return json(route, {
        layersSize: 1000,
        imagesSize: 1500,
        containersSize: 100,
        volumesSize: 500,
        buildCacheSize: 200,
        sharedLayers: 500,
        writable: [],
        definitions: [],
        images: { total: 1, active: 1, size: 1000, reclaimable: 0 },
        containers: { total: 1, active: 1, size: 100, reclaimable: 0 },
        volumes: { total: 0, active: 0, size: 0, reclaimable: 0 },
        buildCache: { total: 0, active: 0, size: 0, reclaimable: 0 },
      })
    if (path === "/docker/ping" || path === "/docker/info")
      return json(route, { available: true, serverVersion: "29.8.1" })
    return json(route, [])
  })
  return mutations
}

test("Docker overview applies a restart policy in place and singleton dismissal works", async ({
  page,
}) => {
  const mutations = await mockDockerAdvisor(page)
  await page.goto("/docker")
  await page.getByRole("button", { name: "web has no restart policy", exact: false }).click()
  await page.getByRole("button", { name: "Set a restart policy", exact: true }).click()
  const confirmation = page.getByRole("dialog", { name: "Set a restart policy", exact: true })
  await expect(
    confirmation.getByText(/keeping its writable layer and current process/),
  ).toBeVisible()
  expect(mutations).toHaveLength(0)
  await confirmation.getByRole("button", { name: "Apply policy" }).click()
  await expect.poll(() => mutations.length).toBe(1)
  expect(mutations[0]).toEqual({
    method: "PATCH",
    path: "/docker/containers/web-id/restart-policy",
    body: { policy: "unless-stopped" },
  })
  await page.getByRole("button", { name: "Dismiss finding" }).click()
  await expect(
    page.getByRole("button", { name: "web has no restart policy", exact: false }),
  ).toHaveCount(0)
  await page.getByRole("button", { name: "Rescan", exact: true }).click()
  await expect(
    page.getByRole("button", { name: "web has no restart policy", exact: false }),
  ).toBeVisible()
})

test("Docker findings direct Compose configuration changes to their owner", async ({ page }) => {
  const mutations = await mockDockerAdvisor(page, true)
  await page.goto("/docker")
  await page.getByRole("button", { name: "web has no restart policy", exact: false }).click()
  await page.getByRole("button", { name: "Set a restart policy", exact: true }).click()
  await expect(page).toHaveURL(/\/docker\/stacks\/website\?tab=compose&remedy=set-restart$/)
  expect(mutations).toHaveLength(0)
})

test("reviewed configuration replacement preserves credentials and needs explicit confirmation", async ({
  page,
}) => {
  await mockDockerAdvisor(page)
  const spec = {
    name: "web",
    image: "nginx:latest",
    env: [{ name: "TOKEN", value: "preserved-fixture" }],
    limits: { memoryMb: 256 },
    mounts: [{ type: "volume", source: "web-data", target: "/data" }],
    start: true,
  }
  const mutations: { path: string; body: unknown }[] = []
  await page.route("**/api/v1/docker/containers/**", async (route) => {
    const path = new URL(route.request().url()).pathname.replace(/^\/api\/v1/, "")
    if (path.endsWith("/spec")) return json(route, spec)
    if (path.endsWith("/preview"))
      return json(route, {
        run: "docker run reviewed",
        compose: "services:\n  web:\n    image: nginx:1.27.5",
      })
    if (path.endsWith("/recreate")) {
      mutations.push({ path, body: route.request().postDataJSON() })
      return json(route, { id: "replacement-id", name: "web", warnings: [], started: true })
    }
    if (path.endsWith("/failure"))
      return json(route, { cause: "none", evidence: [], suggestions: [] })
    if (path.endsWith("/web-id"))
      return json(route, {
        id: "web-id",
        name: "web",
        image: spec.image,
        state: "running",
        env: [],
        mounts: spec.mounts,
        ports: [],
        labels: {},
        networks: [],
        memoryLimit: 268435456,
      })
    return route.fallback()
  })
  await page.goto("/docker/containers/web-id?tab=configure")
  const editor = page.getByLabel("Replacement specification", { exact: true })
  await expect(editor).toHaveValue(/preserved-fixture/)
  const edited = { ...spec, image: "nginx:1.27.5" }
  await editor.fill(JSON.stringify(edited, null, 2))
  await page.getByRole("button", { name: "Preview replacement", exact: true }).click()
  await expect(page.getByText("Reviewed Compose equivalent", { exact: true })).toBeVisible()
  expect(mutations).toHaveLength(0)
  await page
    .getByRole("button", { name: "Replace with reviewed configuration", exact: true })
    .click()
  const dialog = page.getByRole("dialog", { name: "Replace web", exact: true })
  await expect(dialog.getByText(/permanently lost/)).toBeVisible()
  expect(mutations).toHaveLength(0)
  await dialog.getByRole("button", { name: "Replace container", exact: true }).click()
  await expect.poll(() => mutations.length).toBe(1)
  expect(mutations[0]).toEqual({
    path: "/docker/containers/web-id/recreate",
    body: { spec: edited },
  })
  await expect(page).toHaveURL(/\/docker\/containers\/replacement-id$/)
})

test("Health names unassessed sources instead of claiming every check passed", async ({ page }) => {
  await mockAdvisor(page)
  await page.route("**/api/v1/system/health", (route) =>
    json(route, {
      ...health,
      status: "ok",
      findings: [],
      silences: ["Service manager could not be read; failed services were not assessed."],
    }),
  )
  await page.goto("/metrics")
  const assessment = page.locator("[data-slot=panel]", {
    has: page.getByRole("heading", { name: "Health", exact: true }),
  })
  await expect(assessment.getByText("Partial assessment", { exact: true })).toBeVisible({
    timeout: 15000,
  })
  await expect(assessment.getByText("Not assessed", { exact: true })).toBeVisible()
  await expect(
    assessment.getByText("No findings in the completed checks", { exact: true }),
  ).toBeVisible()
})

test("host storage investigation opens the verified filesystem in Files", async ({ page }) => {
  await mockAdvisor(page, { hostMount: true })
  const sheet = await openInvestigation(page)
  await expect(sheet.getByText("Host filesystem mounted at /host.", { exact: false })).toBeVisible()
  await expect(sheet.getByRole("link", { name: "Open in Files", exact: true })).toHaveAttribute(
    "href",
    "/files?path=%2Fhost",
  )
})

test("an unavailable first Health read stays visible and can be retried", async ({ page }) => {
  await mockAdvisor(page)
  let unavailable = true
  await page.route("**/api/v1/system/health", async (route) => {
    if (unavailable)
      return route.fulfill({
        status: 503,
        contentType: "application/json",
        body: JSON.stringify({
          error: { code: "internal", message: "Health source unavailable" },
        }),
      })
    return route.fallback()
  })
  await page.goto("/metrics")
  await expect(page.getByText("Health source unavailable", { exact: true })).toBeVisible()
  unavailable = false
  await page.getByRole("button", { name: "Try again", exact: true }).click()
  await expect(page.getByRole("button", { name: "/ is filling up", exact: false })).toBeVisible()
  await expect(page.getByText("Health source unavailable", { exact: true })).toHaveCount(0)
})
