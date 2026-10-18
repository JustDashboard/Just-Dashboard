import { expect, test, type Page } from "@playwright/test"
import { iso, json, mockHost, now } from "./host-fixture"

/** Reviewed manual container configuration preserves the submitted data and confirms replacement. */

async function mockDockerRemedies(page: Page, compose = false) {
  await mockHost(page)
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
  const recommendation = {
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
  const finding = {
    ...recommendation,
    id: "container.logs.web-id",
    severity: "warning",
    class: "storage",
    title: "web has written 500 MB of logs",
    detail: "An unrotated 500 MB log file is using this host's disk.",
    action: "cap-logs",
    actionLabel: "Cap the log size",
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
        status: "warning",
        checked: 1,
        checkedAt: iso(now),
        findings: [finding, recommendation],
        attention: { total: 2, issues: 1, recommendations: 1, critical: 0, warning: 1, info: 0 },
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
    if (path === "/docker/containers/web-id/failure")
      return json(route, {
        containerId: "web-id",
        name: "web",
        checkedAt: new Date().toISOString(),
        state: "running",
        headline: "web is running",
        confidence: "observed",
        evidence: [],
        restarts: { count: 0, recent: 0, looping: false },
        suggestions: [],
      })
    if (path === "/docker/containers/web-id/spec")
      return json(route, { name: "web", image: "nginx:1.27", start: true })
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

test("reviewed configuration replacement preserves credentials and needs explicit confirmation", async ({
  page,
}) => {
  await mockDockerRemedies(page)
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
      return json(route, {
        containerId: "web-id",
        name: "web",
        checkedAt: new Date().toISOString(),
        state: "running",
        headline: "web is running",
        confidence: "observed",
        evidence: [],
        restarts: { count: 0, recent: 0, looping: false },
        suggestions: [],
      })
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

test("Compose configuration opens its owning file without recommendation cards", async ({
  page,
}) => {
  await mockDockerRemedies(page, true)
  await page.goto("/docker/containers/web-id?tab=configure")
  await expect(page.getByRole("link", { name: "Open Compose file" })).toHaveAttribute(
    "href",
    "/docker/stacks/website?tab=compose",
  )
  await expect(page.getByLabel("Replacement specification", { exact: true })).toHaveCount(0)
})
