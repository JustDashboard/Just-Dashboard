import { expect, test } from "@playwright/test"
import { iso, json, mockHost, now } from "./host-fixture"

/**
 * The host Overview after its 0.7.0 pass, against the Metrics page's mocked
 * host: the machine is drawn as itself, each reading that moves carries its
 * last hour inside its tile rather than in a panel of sparklines under them,
 * and a service tile names what it counts with the products themselves.
 */

const containers = [
  ["api", "ghcr.io/acme/api:1.4", "running"],
  ["db", "postgres:16", "running"],
  ["cache", "redis:7", "running"],
  ["old", "n8nio/n8n:1.110.1", "exited"],
].map(([name, image, state], i) => ({
  id: `c${i}`,
  names: [name],
  name,
  image,
  imageId: "",
  command: "",
  state,
  status: state,
  createdAt: iso(now),
  uptimeSeconds: 60,
  ports: [],
  labels: {},
  networks: [],
  exposure: [],
}))

test.beforeEach(async ({ page }) => {
  await mockHost(page)
  // Registered after the host's catch-all, so they answer first.
  await page.route("**/api/v1/docker/containers/**", (route) => json(route, containers))
  await page.route("**/api/v1/databases/", (route) =>
    json(route, [{ id: 1, name: "app", driver: "postgres" }]),
  )
  await page.route("**/api/v1/packages/updates", (route) =>
    json(route, {
      available: true,
      packages: [{}],
      securityCount: 0,
      rebootRequired: false,
      securityFiltering: true,
    }),
  )
  await page.route("**/api/v1/exposure", (route) =>
    json(route, { grade: "tailscale", allowlist: ["100.64.0.0/10"], interfaces: [{}] }),
  )
  await page.route("**/api/v1/deploy/**", (route) =>
    json(route, { deployments: [], activeWork: [] }),
  )
  await page.route("**/api/v1/docker/health", (route) =>
    json(route, { runtime: { status: "ok", running: 3, unhealthy: 0, restarting: 0 } }),
  )
  await page.route("**/api/v1/dashboard/update**", (route) =>
    json(route, {
      version: "0.7.0",
      available: false,
      releases: [],
      history: [],
      breaking: false,
      check: { enabled: true, repo: "a/b", ref: "main" },
      install: { supported: true },
    }),
  )
})

test("the host is drawn as itself, and its facts are one line", async ({ page }) => {
  await page.goto("/")
  await expect(page.getByRole("heading", { name: "atlas" })).toHaveClass(/sr-only/)
  await expect(
    page.locator('[data-slot="host-identity"]').getByText("atlas", { exact: true }),
  ).toBeVisible()
  await expect(page.getByText(/^Ubuntu 24\.04 6\.8\.0-45-generic$/)).toBeVisible()
  // The distribution, the processor and the hypervisor, each its own mark.
  for (const logo of ["ubuntu", "amd", "qemu"]) {
    await expect(page.locator(`img[src="/logos/${logo}.svg"]`).first()).toBeVisible()
  }
  await expect(page.getByText("184 processes")).toBeVisible()
})

test("each moving reading carries its last hour in its tile", async ({ page }) => {
  await page.goto("/")
  const cpu = page.locator("[data-slot=stat-tile]", { hasText: "CPU" }).first()
  await expect(cpu.getByRole("img", { name: "CPU over the last hour" })).toBeVisible({
    timeout: 20_000,
  })
  await expect(page.getByRole("img", { name: "Network over the last hour" })).toBeVisible()
  // The panel of sparklines that repeated the tiles is gone.
  await expect(page.getByRole("heading", { name: "Last hour" })).toHaveCount(0)
})

test("a service tile names what it counts with the products", async ({ page }) => {
  await page.goto("/")
  const main = page.locator("[data-slot=page]")
  const docker = main.getByRole("link", { name: "Docker" })
  await expect(docker).toContainText("3 running")
  // The running images, not the stopped one.
  await expect(docker.locator('img[src="/logos/postgresql.svg"]')).toBeAttached()
  await expect(docker.locator('img[src="/logos/redis.svg"]')).toBeAttached()
  await expect(docker.locator('img[src="/logos/n8n.svg"]')).toHaveCount(0)
  await expect(
    main.getByRole("link", { name: "Security" }).locator('img[src="/logos/tailscale.svg"]'),
  ).toBeAttached()
})

for (const width of [1280, 1720]) {
  test(`looks right at ${width}`, async ({ page }) => {
    await page.setViewportSize({ width, height: 1400 })
    await page.goto("/")
    await expect(page.getByRole("img", { name: "CPU over the last hour" })).toBeVisible({
      timeout: 20_000,
    })
    expect(
      await page.evaluate(
        () => document.documentElement.scrollWidth > document.documentElement.clientWidth,
      ),
    ).toBe(false)
    await page.screenshot({ path: `test-results/overview-${width}.png`, fullPage: true })
  })
}
