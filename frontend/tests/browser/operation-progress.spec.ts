import { expect, test, type Page } from "@playwright/test"
import { mockImages } from "./docker-images-fixture"
import { mockVolumes } from "./docker-volumes-fixture"
import { mockNetworks } from "./docker-networks-fixture"
import { DOCKER_DISK, mockDockerHost } from "./docker-fixture"
import { mockWorkbench, run } from "./workbench-fixture"
import { mockHost } from "./packages-fixture"
import { mockShowcase } from "./proxy-fixtures"
import { heldResponse, recordProgress } from "./progress-recording"

test.use({ viewport: { width: 1280, height: 900 } })
const progress = (page: Page) => page.locator("[data-slot=operation-progress]")

test("progress: a proxy request test keeps its wait visible and clears it on refusal", async ({
  page,
}) => {
  await mockShowcase(page)
  const held = await heldResponse(
    page,
    "/proxy/tools/request",
    { error: { code: "request", message: "The target did not answer" } },
    400,
  )
  await page.goto("/proxy/tls?tool=request")
  await page.getByLabel("Domain", { exact: true }).fill("example.test")
  await recordProgress(
    page,
    "proxy-request-test",
    () => page.getByRole("button", { name: "Send", exact: true }).click(),
    progress(page),
    async () => {
      held.release()
      await expect(page.getByText("The target did not answer").first()).toBeVisible()
    },
  )
})

for (const entry of [
  {
    name: "docker-reclaim",
    path: "/docker/prune",
    title: /^Reclaim/,
    action: "Reclaim",
    open: "all",
  },
  {
    name: "docker-images",
    path: "/docker/images/prune",
    title: "Remove unused images",
    action: "Remove",
    open: "images",
  },
  {
    name: "docker-build-cache",
    path: "/docker/build-cache/prune",
    title: "Empty the build cache",
    action: "Empty it",
    open: "cache",
  },
  {
    name: "docker-containers",
    path: "/docker/containers/prune",
    title: "Remove stopped containers",
    action: "Remove",
    open: "containers",
  },
  {
    name: "docker-untagged",
    path: "/docker/images/prune",
    title: "Prune untagged images",
    action: "Prune",
    open: "untagged",
  },
]) {
  test(`progress: ${entry.name} remains visible until Docker answers`, async ({ page }) => {
    await mockImages(page)
    const report = { kind: "images", spaceReclaimed: 1024 ** 3, items: [] }
    const held = await heldResponse(page, entry.path, entry.open === "all" ? [report] : report)
    await page.goto("/docker/images")
    const disk = page.getByRole("region", { name: "Docker on disk" })
    if (entry.open === "all") await disk.getByRole("button", { name: /^Reclaim / }).click()
    if (entry.open === "images")
      await disk.getByRole("button", { name: "Reclaim", exact: true }).nth(0).click()
    if (entry.open === "cache")
      await disk.getByRole("button", { name: "Reclaim", exact: true }).nth(1).click()
    if (entry.open === "containers")
      await disk.getByRole("button", { name: "Remove", exact: true }).click()
    if (entry.open === "untagged")
      await page.getByRole("button", { name: "Prune untagged", exact: true }).click()
    const dialog = page.getByRole("dialog", { name: entry.title })
    await recordProgress(
      page,
      entry.name,
      async () => {
        await dialog.getByRole("button", { name: entry.action, exact: true }).click()
        await expect(dialog.getByRole("button", { name: "Cancel", exact: true })).toBeDisabled()
        await expect(progress(page).getByRole("progressbar")).not.toHaveAttribute("aria-valuenow")
        await page.keyboard.press("Escape")
        await expect(dialog).toBeVisible()
      },
      progress(page),
      async () => {
        held.release()
        await expect(dialog).toHaveCount(0)
      },
    )
    expect(held.calls()).toBe(1)
  })
}

test("progress: docker-overview-cleanup reports a failed request and allows retry", async ({
  page,
}) => {
  await mockDockerHost(page)
  await page.route("**/api/v1/docker/disk-usage", (route) =>
    route.fulfill({
      json: {
        ...DOCKER_DISK,
        buildCacheSize: 2 * 1024 ** 3,
        buildCache: { ...DOCKER_DISK.buildCache, reclaimable: 2 * 1024 ** 3 },
      },
    }),
  )
  await page.route("**/api/v1/docker/cleanup/preview", (route) =>
    route.fulfill({
      json: {
        categories: [
          {
            key: "build-cache",
            label: "Build cache",
            items: 48,
            reclaimable: 1024 ** 3 * 2,
            recommended: true,
            destroys: false,
            cost: "The next build recreates its cache",
            examples: [],
          },
        ],
      },
    }),
  )
  const held = await heldResponse(
    page,
    "/docker/cleanup",
    { error: { code: "docker", message: "Docker refused the cleanup" } },
    500,
  )
  await page.goto("/docker")
  await page.getByRole("button", { name: /^Reclaim / }).click()
  const dialog = page.getByRole("dialog", { name: /^Reclaim/ })
  await recordProgress(
    page,
    "docker-overview-cleanup",
    () => dialog.getByRole("button", { name: "Remove", exact: true }).click(),
    progress(page),
    async () => {
      held.release()
      await expect(dialog.getByRole("button", { name: "Remove", exact: true })).toBeEnabled()
      await expect(page.getByText("Docker refused the cleanup")).toBeVisible()
    },
  )
})

test("progress: docker-volumes-prune waits in the active dialog", async ({ page }) => {
  await mockVolumes(page)
  const held = await heldResponse(page, "/docker/volumes/prune", { spaceReclaimed: 1, items: [] })
  await page.goto("/docker/volumes")
  await page.getByRole("button", { name: /^Prune / }).click()
  const dialog = page.getByRole("dialog", { name: "Prune volumes" })
  await recordProgress(
    page,
    "docker-volumes",
    () => dialog.getByRole("button", { name: "Prune", exact: true }).click(),
    progress(page),
    async () => {
      held.release()
      await expect(dialog).toHaveCount(0)
    },
  )
})

test("progress: docker-networks-prune keeps its reviewed selection", async ({ page }) => {
  await mockNetworks(page)
  const held = await heldResponse(page, "/docker/networks/prune", {
    items: ["old-staging"],
    skipped: [],
  })
  await page.goto("/docker/networks")
  await page.getByRole("button", { name: "Remove unused", exact: true }).click()
  const dialog = page.getByRole("dialog", { name: "Remove unused networks" })
  await recordProgress(
    page,
    "docker-networks",
    () => dialog.getByRole("button", { name: "Remove 2", exact: true }).click(),
    progress(page),
    async () => {
      held.release()
      await expect(dialog).toHaveCount(0)
    },
  )
})

test("progress: a scheduled backup reports a wait instead of inventing its stage", async ({
  page,
}) => {
  await mockWorkbench(page)
  let running = true
  const startedAt = new Date().toISOString()
  await page.route("**/api/v1/backups/1/runs", (route) =>
    route.fulfill({
      json: {
        runs: [
          {
            ...run(10, 1, 4000, running ? "running" : "success", 0),
            startedAt,
            log: "",
            endedAt: undefined,
          },
        ],
        running,
      },
    }),
  )
  await page.goto("/backups/1")
  await recordProgress(
    page,
    "backup-run",
    async () => {},
    progress(page),
    async () => {
      running = false
      await expect(progress(page)).toHaveCount(0, { timeout: 10000 })
    },
  )
})

test("progress: package jobs remain visible when their output is quiet", async ({ page }) => {
  await mockHost(page)
  const job = {
    id: "package-progress",
    kind: "packages.install",
    title: "Install htop",
    status: "running",
    startedAt: new Date().toISOString(),
    startedBy: "operator",
    lines: 0,
  }
  await page.route("**/api/v1/jobs/package-progress", (route) =>
    route.fulfill({ json: { job, lines: [] } }),
  )
  let socket: { send: (text: string) => void } | undefined
  await page.routeWebSocket(/\/jobs\/package-progress\/stream/, (ws) => {
    socket = ws
  })
  await page.goto("/packages")
  await page.route("**/api/v1/jobs/", (route) => route.fulfill({ json: [job] }))
  // RecentJobs polls on mount; reload after supplying the running operation.
  await page.reload()
  await recordProgress(
    page,
    "packages-job",
    () => page.getByRole("button", { name: /Install htop/ }).click(),
    progress(page),
    async () => {
      await expect.poll(() => Boolean(socket)).toBe(true)
      socket!.send(
        JSON.stringify({
          type: "job",
          data: { ...job, status: "succeeded", endedAt: new Date().toISOString() },
        }),
      )
    },
  )
})

test("progress: reduced motion keeps a visible track and the phone keeps feedback in view", async ({
  page,
}) => {
  await mockImages(page)
  await page.setViewportSize({ width: 390, height: 844 })
  await page.emulateMedia({ reducedMotion: "reduce" })
  const held = await heldResponse(page, "/docker/prune", [
    { kind: "images", spaceReclaimed: 0, items: [] },
  ])
  await page.goto("/docker/images")
  await page
    .getByRole("region", { name: "Docker on disk" })
    .getByRole("button", { name: /^Reclaim / })
    .click()
  const dialog = page.getByRole("dialog", { name: /^Reclaim/ })
  await dialog.getByRole("button", { name: "Reclaim", exact: true }).click()
  const working = progress(page)
  await expect(working).toBeInViewport()
  const bar = working.getByRole("progressbar")
  await expect(bar).not.toHaveAttribute("aria-valuenow")
  await expect(bar.locator("span")).toHaveCSS("animation-name", "none")
  const track = await bar.boundingBox()
  const fill = await bar.locator("span").boundingBox()
  expect(fill!.width).toBeGreaterThanOrEqual(track!.width - 1)
  expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(390)
  held.release()
  await expect(dialog).toHaveCount(0)
})

test("progress: a refused prune report never adds a completed success message", async ({
  page,
}) => {
  await mockImages(page)
  const held = await heldResponse(page, "/docker/prune", [
    { kind: "build-cache", spaceReclaimed: 0, items: [], error: "Docker refused the prune" },
  ])
  await page.goto("/docker/images")
  await page
    .getByRole("region", { name: "Docker on disk" })
    .getByRole("button", { name: /^Reclaim / })
    .click()
  const dialog = page.getByRole("dialog", { name: /^Reclaim/ })
  await dialog.getByRole("button", { name: "Reclaim", exact: true }).click()
  await expect(progress(page)).toBeVisible()
  held.release()
  await expect(dialog).toHaveCount(0)
  await expect(page.getByText(/Nothing was reclaimed.*Docker refused the prune/)).toBeVisible()
  await expect(page.getByText(/^Reclaim .* completed$/)).toHaveCount(0)
})

test("progress: a cleanup that partly worked reports which category refused", async ({ page }) => {
  await mockDockerHost(page)
  await page.route("**/api/v1/docker/disk-usage", (route) =>
    route.fulfill({
      json: {
        ...DOCKER_DISK,
        buildCacheSize: 2 * 1024 ** 3,
        buildCache: { ...DOCKER_DISK.buildCache, reclaimable: 2 * 1024 ** 3 },
      },
    }),
  )
  await page.route("**/api/v1/docker/cleanup/preview", (route) =>
    route.fulfill({
      json: {
        categories: [
          {
            key: "build-cache",
            label: "Build cache",
            items: 48,
            reclaimable: 2 * 1024 ** 3,
            recommended: true,
            destroys: false,
            cost: "The next build recreates its cache",
            examples: [],
          },
        ],
      },
    }),
  )
  const held = await heldResponse(page, "/docker/cleanup", {
    reclaimed: 1024 ** 3,
    reports: [
      { kind: "images", spaceReclaimed: 1024 ** 3, items: ["old-image"] },
      {
        kind: "build-cache",
        spaceReclaimed: 0,
        items: [],
        error: "Docker refused the cache prune",
      },
    ],
  })
  await page.goto("/docker")
  await page.getByRole("button", { name: /^Reclaim / }).click()
  const dialog = page.getByRole("dialog", { name: /^Reclaim/ })
  await dialog.getByRole("button", { name: "Remove", exact: true }).click()
  await expect(progress(page)).toBeVisible()
  held.release()
  await expect(dialog).toHaveCount(0)
  await expect(page.getByText(/some parts did not run/)).toBeVisible()
  await expect(page.getByText(/build-cache: Docker refused the cache prune/)).toBeVisible()
  await expect(page.getByText(/^Reclaim .* completed$/)).toHaveCount(0)
})
