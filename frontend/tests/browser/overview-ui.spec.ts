import { expect, test } from "@playwright/test"
import { fleet, mockOverview } from "./overview-fixture"
import { iso, json, now } from "./host-fixture"

/**
 * The host Overview, against a server with something in every place: the
 * machine drawn as itself, each moving reading carrying its last hour, one
 * Health list holding what every module found, the projects as the fleet
 * draws them, who is spending the machine beside the day's activity, and a
 * service tile per module naming what it counts with the products.
 */

test.beforeEach(async ({ page }) => {
  await mockOverview(page)
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

test("what every module found is one Health list, worst first", async ({ page }) => {
  await page.goto("/")
  const health = page.locator("[data-slot=panel]", {
    has: page.getByRole("heading", { name: "Health" }),
  })
  const findings = health.locator("[data-slot=accordion-trigger]")
  // The failing project outranks the recorder's disk warning; the
  // certificate past its renewal and the security updates follow it.
  await expect(findings.first()).toContainText("docs-site is failing its health check")
  await expect(health).toContainText("/ is filling up")
  await expect(health).toContainText("status.example.test: certificate expires in 12d")
  await expect(health).toContainText("2 security updates waiting")
  // The verdict is the worst of them, in the panel and on the identity line.
  await expect(health.getByText("Critical")).toBeVisible()
  await expect(page.locator('[data-slot="host-identity"]').getByText("Critical")).toBeVisible()

  // Each one opens the page that fixes it.
  await health.getByText("2 security updates waiting").click()
  await health.getByRole("button", { name: "Open packages" }).click()
  await expect(page).toHaveURL(/\/packages$/)
})

test("the projects are the fleet's cards, worst first", async ({ page }) => {
  await page.goto("/")
  const section = page.locator("section", {
    has: page.getByRole("heading", { name: "Deployments" }),
  })
  const cards = section.getByRole("list", { name: "Deployment projects" }).locator(":scope > li")
  await expect(cards).toHaveCount(fleet.length)
  await expect(cards.first()).toContainText("docs-site")
  await expect(cards.first()).toContainText("Failed")
  // Drawn as what each one is: the template's product, the framework.
  await expect(section.locator('img[src="/logos/n8n.svg"]').first()).toBeAttached()
  await expect(section.locator('img[src="/logos/astro.svg"]').first()).toBeAttached()
  await expect(section.getByRole("link", { name: "All projects" })).toHaveAttribute(
    "href",
    "/deploy",
  )
  await expect(section.getByRole("link", { name: "New project" })).toHaveAttribute(
    "href",
    "/deploy/new",
  )
})

test("a longer fleet shows two rows and says how many it left out", async ({ page }) => {
  const more = [7, 8].map((n) => ({ ...fleet[0], id: 100 + n, name: `extra-${n}` }))
  await mockOverview(page, { deployments: [...fleet, ...more] })
  await page.goto("/")
  const section = page.locator("section", {
    has: page.getByRole("heading", { name: "Deployments" }),
  })
  await expect(
    section.getByRole("list", { name: "Deployment projects" }).locator(":scope > li"),
  ).toHaveCount(6)
  await expect(section.getByText("Showing 6 of 8")).toBeVisible()
})

test("an empty fleet asks for its first project", async ({ page }) => {
  await mockOverview(page, { deployments: [] })
  await page.goto("/")
  const empty = page.locator("[data-slot=empty-state]", {
    hasText: "Deploy your first project",
  })
  await expect(empty).toBeVisible()
  await expect(empty.getByRole("link", { name: "New project" })).toHaveAttribute(
    "href",
    "/deploy/new",
  )
})

test("who is spending the machine sits beside the day's activity", async ({ page }) => {
  await page.goto("/")
  const processes = page.locator("[data-slot=panel]", {
    has: page.getByRole("heading", { name: "Top processes" }),
  })
  await expect(processes.locator("li").first()).toContainText("postgres")
  await expect(processes.locator('img[src="/logos/postgresql.svg"]')).toBeAttached()

  const activity = page.locator("[data-slot=panel]", {
    has: page.getByRole("heading", { name: "Recent activity" }),
  })
  // Newest first, and the day rather than the hour: the package run ten
  // hours ago is still on it.
  await expect(activity.locator("li").first()).toContainText("shop-stack deployed")
  await expect(activity).toContainText("Installed 4 package updates")
})

for (const width of [1280, 390]) {
  test(`activity shows product identities and truthful outcomes at ${width}`, async ({ page }) => {
    await page.setViewportSize({ width, height: 900 })
    await page.route("**/api/v1/system/metrics/events**", (route) =>
      json(route, [
        {
          ts: iso(now - 180_000),
          kind: "deploy",
          title: "shop-stack",
          detail: "deploy running (push) by operator",
          severity: "info",
        },
        {
          ts: iso(now - 120_000),
          kind: "action",
          title: "docker.container.restart shop-web",
          detail: "by operator",
          severity: "error",
        },
        {
          ts: iso(now - 60_000),
          kind: "action",
          title: "terminal.kill d4a0e8350b641a59",
          detail: "by operator",
          severity: "info",
        },
      ]),
    )
    await page.goto("/")
    const activity = page.locator("[data-slot=panel]", {
      has: page.getByRole("heading", { name: "Recent activity", exact: true }),
    })
    const rows = activity.getByRole("list", { name: "Recent server activity" }).locator("li")
    await expect(rows).toHaveCount(3)
    await expect(rows.first()).toContainText("Close terminal")
    await expect(rows.first()).toContainText("Accepted")
    await expect(rows.first().locator('img[src="/logos/terminal.svg"]')).toBeVisible()
    await expect(rows.first().locator('[title="terminal.kill d4a0e8350b641a59"]')).toBeVisible()
    await expect(rows.nth(1)).toContainText("Failed")
    await expect(rows.nth(1).locator('img[src="/logos/docker.svg"]')).toBeVisible()
    await expect(rows.nth(2)).toContainText("Running")
    await expect(activity.getByText("Succeeded", { exact: true })).toHaveCount(0)
    await expect(rows.first().locator("time")).toHaveAttribute("datetime", iso(now - 60_000))
    const bounds = await activity.evaluate((panel) => ({
      width: panel.clientWidth,
      scrollWidth: panel.scrollWidth,
    }))
    expect(bounds.scrollWidth).toBe(bounds.width)
    await expect(activity.getByRole("link", { name: "Audit log" })).toHaveAttribute(
      "href",
      "/audit",
    )
    await activity.getByRole("link", { name: "Audit log" }).click()
    await expect(page).toHaveURL(/\/audit$/)
  })
}

test("an empty activity panel keeps the way to the audit trail", async ({ page }) => {
  await page.route("**/api/v1/system/metrics/events**", (route) => json(route, []))
  await page.goto("/")
  const activity = page.locator("[data-slot=panel]", {
    has: page.getByRole("heading", { name: "Recent activity", exact: true }),
  })
  await expect(activity).toContainText("Nothing in the last 24 hours.")
  await expect(activity.getByRole("link", { name: "Audit log" })).toBeVisible()
})

test("a service tile names what it counts with the products", async ({ page }) => {
  await page.goto("/")
  const main = page.locator("[data-slot=page]")
  const docker = main.getByRole("link", { name: "Docker", exact: true })
  await expect(docker).toContainText("9 running")
  // The running images, not the stopped ones.
  await expect(docker.locator('img[src="/logos/postgresql.svg"]')).toBeAttached()
  await expect(docker.locator('img[src="/logos/redis.svg"]')).toBeAttached()
  await expect(docker.locator('img[src="/logos/meilisearch.svg"]')).toHaveCount(0)
  await expect(
    main
      .getByRole("link", { name: "Security", exact: true })
      .locator('img[src="/logos/tailscale.svg"]'),
  ).toBeAttached()

  // Git took the Deployments tile, which has a section of its own now.
  await expect(main.getByRole("link", { name: "Deployments", exact: true })).toHaveCount(0)
  const git = main.getByRole("link", { name: "Git", exact: true })
  await expect(git).toContainText("4 repositories")
  await expect(git).toContainText("1 uncommitted · 1 behind")
  for (const forge of ["gitlab", "codeberg", "github"]) {
    await expect(git.locator(`img[src="/logos/${forge}.svg"]`)).toBeAttached()
  }
})

for (const width of [1280, 1720]) {
  test(`looks right at ${width}`, async ({ page }) => {
    // Tall enough for the whole page: the shell scrolls inside itself, so a
    // full-page screenshot would stop at the window.
    await page.setViewportSize({ width, height: 2300 })
    await page.goto("/")
    await expect(page.getByRole("img", { name: "CPU over the last hour" })).toBeVisible({
      timeout: 20_000,
    })
    await expect(page.getByText("4 repositories")).toBeVisible()
    expect(
      await page.evaluate(
        () => document.documentElement.scrollWidth > document.documentElement.clientWidth,
      ),
    ).toBe(false)
    // The two halves of the process row meet on one hairline.
    const [processes, activity] = await Promise.all(
      ["Top processes", "Recent activity"].map((name) =>
        page
          .locator("[data-slot=panel-header]", { has: page.getByRole("heading", { name }) })
          .boundingBox(),
      ),
    )
    expect(processes?.y).toBe(activity?.y)
    expect(processes?.height).toBe(activity?.height)
    await page.screenshot({ path: `test-results/overview-${width}.png`, fullPage: true })
  })
}
