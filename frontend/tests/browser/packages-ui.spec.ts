import { expect, test, type Page, type Route } from "@playwright/test"

/**
 * The Packages page, checked in a browser against a mocked host.
 *
 * What these assert is what a type check cannot: that the page reads the way
 * the design system says (design-system.md §15) — the host's identity line
 * with its distribution as the mark and the manager and index age as facts,
 * four figures as tiles carrying the products they count, three views under
 * one underlined strip, and no framed block but a table anywhere on the page;
 * that every package is drawn as the software it is (§14) and an upgrade shows
 * the part of the version it changes; that the one decision worth making in a
 * hurry (security updates waiting) carries its own button above the fold; that
 * a row's fixed properties sit at its edge; and that the search, the install
 * verb and the package sheet are all reachable from the strip. The screenshots
 * at 1280 and 1720 are the eyes the assertions do not have.
 */

const now = new Date().toISOString()
const twoDaysAgo = new Date(Date.now() - 2 * 24 * 3600_000).toISOString()

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

const installed = [
  {
    name: "nginx",
    version: "1.24.0-2ubuntu7",
    summary: "small, powerful, scalable web/proxy server",
    size: 1_540_000,
    section: "httpd",
    explicit: true,
    upgradable: "1.24.0-2ubuntu7.1",
  },
  {
    name: "openssl",
    version: "3.0.13-0ubuntu3",
    summary: "Secure Sockets Layer toolkit - cryptographic utility",
    size: 2_030_000,
    section: "utils",
    explicit: false,
    essential: true,
    upgradable: "3.0.13-0ubuntu3.4",
    security: true,
  },
  {
    name: "postgresql-16",
    version: "16.3-0ubuntu0.24.04.1",
    summary: "The World's Most Advanced Open Source Relational Database",
    size: 48_000_000,
    section: "database",
    explicit: true,
  },
  {
    name: "curl",
    version: "8.5.0-2ubuntu10.1",
    summary: "command line tool for transferring data with URL syntax",
    size: 530_000,
    section: "web",
    explicit: true,
    upgradable: "8.5.0-2ubuntu10.4",
  },
  {
    name: "libc6",
    version: "2.39-0ubuntu8",
    summary: "GNU C Library: Shared libraries",
    size: 13_000_000,
    section: "libs",
    explicit: false,
    essential: true,
  },
  {
    name: "gcc-13",
    version: "13.2.0-23ubuntu4",
    summary: "GNU C compiler",
    size: 96_000_000,
    section: "devel",
    explicit: false,
  },
]

const inventory = {
  available: true,
  manager: "apt",
  packages: installed,
  explicitCount: 3,
  totalSize: 4_300_000_000,
  upgradeCount: 3,
  securityCount: 1,
  canInstall: true,
  canPurge: true,
  indexAge: twoDaysAgo,
  canRefresh: true,
  readAt: now,
}

const report = {
  available: true,
  manager: "apt",
  packages: [
    {
      name: "openssl",
      current: "3.0.13-0ubuntu3",
      candidate: "3.0.13-0ubuntu3.4",
      origin: "Ubuntu:24.04/noble-security",
      security: true,
    },
    {
      name: "nginx",
      current: "1.24.0-2ubuntu7",
      candidate: "1.24.0-2ubuntu7.1",
      origin: "Ubuntu:24.04/noble-updates",
      security: false,
    },
    {
      name: "curl",
      current: "8.5.0-2ubuntu10.1",
      candidate: "8.5.0-2ubuntu10.4",
      origin: "Ubuntu:24.04/noble-updates",
      security: false,
    },
  ],
  securityCount: 1,
  securityFiltering: true,
  rebootRequired: false,
  lastChecked: now,
}

const host = {
  hostname: "web-1",
  os: "linux",
  platform: "ubuntu",
  platformVersion: "24.04",
  kernelVersion: "6.8.0-45-generic",
  kernelArch: "x86_64",
  virtualization: "kvm",
  bootTime: now,
  uptimeSeconds: 86400,
  processes: 212,
  cpuModel: "AMD EPYC 7B13",
  cpuCores: 4,
  cpuMhz: 2450,
}

const search = [
  {
    name: "htop",
    version: "3.3.0-4build1",
    summary: "interactive processes viewer",
    repository: "universe",
    installed: false,
  },
  {
    name: "btop",
    version: "1.3.0-1",
    summary: "Modern and colorful command line resource monitor",
    repository: "universe",
    installed: true,
    installedVersion: "1.3.0-1",
  },
]

const detail = (name: string) => {
  const row = installed.find((p) => p.name === name)
  return {
    name,
    version: row?.upgradable ?? row?.version ?? "3.3.0-4build1",
    installedVersion: row?.version,
    installed: Boolean(row),
    summary: row?.summary ?? "interactive processes viewer",
    description: "A cross-platform interactive process viewer.\nIt lets you see what is running.",
    homepage: `https://example.org/${name}`,
    license: "GPL-2.0",
    section: row?.section ?? "utils",
    repository: "universe",
    arch: "amd64",
    size: row?.size ?? 400_000,
    dependencies: ["libc6", "libncursesw6", "libtinfo6"],
    essential: row?.essential,
    upgradable: row?.upgradable,
  }
}

const usage = (name: string) => ({
  package: name,
  commands: [name],
  services: name === "nginx" ? ["nginx.service"] : undefined,
  configFiles: name === "nginx" ? ["/etc/nginx/nginx.conf"] : undefined,
  manPages: [{ name, section: "8", path: `/usr/share/man/man8/${name}.8.gz` }],
  manual: `${name.toUpperCase()}(8)\n\nNAME\n       ${name} - a program\n`,
  manualFor: name,
  empty: false,
})

async function json(route: Route, body: unknown) {
  await route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify(body) })
}

async function mockHost(page: Page) {
  await page.route("**/api/v1/**", async (route) => {
    const url = new URL(route.request().url())
    const path = url.pathname.replace(/^\/api\/v1/, "")
    const method = route.request().method()
    if (path === "/auth/session") return json(route, user)
    if (path === "/updates/self") return json(route, { current: "0.6.7", latest: "0.6.7" })
    if (path === "/system/host") return json(route, host)
    if (path === "/packages/") return json(route, inventory)
    if (path === "/packages/updates") return json(route, report)
    if (path === "/packages/search") return json(route, search)
    if (path === "/packages/install" && method === "POST") {
      return json(route, {
        id: "job-1",
        kind: "packages.install",
        title: "Install htop",
        target: "htop",
        status: "running",
        exitCode: 0,
        startedAt: now,
        lines: 0,
      })
    }
    const usageMatch = /^\/packages\/([^/]+)\/usage$/.exec(path)
    if (usageMatch) return json(route, usage(decodeURIComponent(usageMatch[1])))
    const detailMatch = /^\/packages\/([^/]+)$/.exec(path)
    if (detailMatch) return json(route, detail(decodeURIComponent(detailMatch[1])))
    if (method !== "GET") return json(route, { exitCode: 0 })
    return json(route, [])
  })
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

test("the page opens on the host and its figures, with nothing framed", async ({ page }) => {
  await mockHost(page)
  await page.goto("/packages")
  await page.waitForLoadState("networkidle")

  // The distribution as the mark, the manager beside its name, the index age
  // as a fact and what is owed as the verdict at the line's end.
  const identity = page.locator("[data-slot='host-identity']")
  await expect(identity).toContainText("Ubuntu 24.04")
  await expect(identity).toContainText("apt")
  await expect(identity).toContainText("index refreshed")
  await expect(identity).toContainText("1 security update")
  await expect(identity.locator("img[src='/logos/ubuntu.svg']")).toHaveCount(1)
  await expect(identity.getByRole("button", { name: "Refresh index" })).toBeVisible()

  const tiles = page.locator("[data-slot='stat-tile']")
  await expect(tiles).toHaveCount(4)
  await expect(tiles.nth(0)).toContainText("6")
  await expect(tiles.nth(1)).toContainText("3")
  await expect(tiles.nth(2)).toContainText("3")
  await expect(tiles.nth(2)).toContainText("1 security")
  await expect(tiles.nth(3)).toContainText("gcc-13 is the largest")
  // The tiles say which software they count: what was asked for, what is behind.
  await expect(tiles.nth(1).locator("img[src='/logos/postgresql.svg']")).toHaveCount(1)
  await expect(tiles.nth(2).locator("img[src='/logos/curl.svg']")).toHaveCount(1)

  // The decision, above the fold, with its own button.
  await expect(page.getByRole("button", { name: "Install security updates" })).toBeVisible()

  // Each view is a toolbar, a hairline and a framed table (§2) — and nothing
  // else on the page carries an edge.
  expect(await framedNonTables(page), "a framed block that is not a table").toEqual([])
  // The pill-shaped tab list is gone; the strip is the product's underlined one.
  expect(await page.locator("[data-slot='tabs-list']").count()).toBe(0)
})

test("each package is drawn as the software it is", async ({ page }) => {
  await mockHost(page)
  await page.goto("/packages")
  await page.waitForLoadState("networkidle")
  await page.getByRole("button", { name: /^Everything/ }).click()

  const logo = (name: string) => page.getByRole("row", { name }).locator("img[src^='/logos/']")
  await expect(logo("nginx")).toHaveAttribute("src", "/logos/nginx.svg")
  await expect(logo("postgresql-16")).toHaveAttribute("src", "/logos/postgresql.svg")
  await expect(logo("curl")).toHaveAttribute("src", "/logos/curl.svg")
  // A library nothing names keeps its section's glyph on the same tile.
  await expect(logo("libc6")).toHaveCount(0)
  await expect(page.getByRole("row", { name: /libc6/ }).locator("svg").first()).toBeVisible()
})

test("the installed view filters and puts a row's properties at its edge", async ({ page }) => {
  await mockHost(page)
  await page.goto("/packages")
  await page.waitForLoadState("networkidle")

  const strip = page.getByRole("navigation", { name: "Package views" })
  await strip.getByRole("button", { name: "Installed" }).click()

  // Installed by hand is the default scope where the manager records it.
  await expect(page.getByRole("row", { name: /nginx/ })).toBeVisible()
  await expect(page.getByRole("row", { name: /libc6/ })).toHaveCount(0)

  await page.getByRole("button", { name: /^Everything/ }).click()
  const openssl = page.getByRole("row", { name: /openssl/ })
  await expect(openssl).toBeVisible()
  // The tags are the row's last cell, not part of its name.
  const lastCell = openssl.getByRole("cell").last()
  await expect(lastCell).toContainText("security")
  await expect(lastCell).toContainText("essential")

  await page.getByRole("button", { name: /^Behind/ }).click()
  await expect(page.getByRole("row", { name: /postgresql-16/ })).toHaveCount(0)
  await expect(page.getByRole("row", { name: /curl/ })).toBeVisible()

  // A row opens its sheet, and the sheet's second tab is the point of it.
  await page.getByRole("row", { name: /nginx/ }).getByRole("button", { name: "nginx" }).click()
  const sheet = page.getByRole("dialog")
  await expect(sheet).toContainText("nginx")
  await sheet.getByRole("tab", { name: "How to use it" }).click()
  await expect(sheet).toContainText("systemctl start nginx.service")
  await expect(sheet).toContainText("/etc/nginx/nginx.conf")
  // The section eyebrows carry no glyph — the word is the whole label.
  expect(await sheet.locator("section > p.eyebrow").count()).toBeGreaterThan(0)
  expect(await sheet.locator("section > div > svg").count()).toBe(0)
})

test("updates and add software are reachable from the strip", async ({ page }) => {
  await mockHost(page)
  await page.goto("/packages")
  await page.waitForLoadState("networkidle")

  const strip = page.getByRole("navigation", { name: "Package views" })
  await strip.getByRole("button", { name: /^Updates/ }).click()
  await expect(page.getByText("3 packages behind · 1 security")).toBeVisible()
  await expect(page.getByRole("button", { name: "Upgrade all 3" })).toBeVisible()
  const security = page.getByRole("row", { name: /openssl/ })
  await expect(security.getByRole("cell").last()).toContainText("security")
  // The upgrade keeps "3.0.13-0ubuntu3" and changes ".4", in amber for a fix.
  await expect(security.getByText(".4", { exact: true })).toHaveClass(/text-warning/)
  // The origin is the archive that published it.
  await expect(security.locator("img[src='/logos/ubuntu.svg']")).toHaveCount(1)

  await strip.getByRole("button", { name: "Add software" }).click()
  // An empty search offers software to look for, each drawn as itself.
  const suggestion = page.getByRole("button", { name: "postgresql", exact: true })
  await expect(suggestion.locator("img[src='/logos/postgresql.svg']")).toHaveCount(1)
  await suggestion.click()
  await expect(page.getByPlaceholder(/What do you need/)).toHaveValue("postgresql")
  await page.getByPlaceholder(/What do you need/).fill("htop")
  await expect(page.getByText("2 matches")).toBeVisible()
  const htop = page.getByRole("listitem").filter({ hasText: "htop" }).first()
  await expect(htop.getByRole("button", { name: "Install" })).toBeVisible()
  // An installed result offers no install, and says so as a state.
  const btop = page.getByRole("listitem").filter({ hasText: "btop" }).first()
  await expect(btop.getByRole("button", { name: "Install" })).toHaveCount(0)
  await expect(btop).toContainText("Installed")

  await htop.getByRole("button", { name: "Install" }).click()
  await expect(htop).toContainText("Started")
  // The job's output opens at the top of the page.
  await expect(page.getByText("Install htop")).toBeVisible()
})

for (const width of [1280, 1720]) {
  test(`looks right at ${width}`, async ({ page }) => {
    await mockHost(page)
    await page.setViewportSize({ width, height: 1000 })
    await page.goto("/packages")
    await page.waitForLoadState("networkidle")
    const strip = page.getByRole("navigation", { name: "Package views" })
    for (const [name, label] of [
      ["installed", "Installed"],
      ["updates", "Updates"],
      ["install", "Add software"],
    ] as const) {
      await strip.getByRole("button", { name: new RegExp(`^${label}`) }).click()
      const overflow = await page.evaluate(
        () => document.documentElement.scrollWidth > document.documentElement.clientWidth,
      )
      expect(overflow, `${name} scrolls sideways at ${width}`).toBe(false)
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
      expect(unnamed, `unlabelled icon-only controls on ${name}`).toEqual([])
      await page.screenshot({ path: `test-results/packages-${name}-${width}.png`, fullPage: true })
    }
  })
}
