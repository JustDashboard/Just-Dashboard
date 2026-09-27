import { expect, test, type Page } from "@playwright/test"
import { bridgedDatabases, hostPorts, privateUplink } from "./fixtures/proxy/ports"
import { json, mockProxy } from "./proxy-fixtures"

/**
 * The ports page and what it feeds, against sockets shaped as a real host
 * lists them. A socket bound to one specific address — a tailnet IP, the
 * public IP — used to be drawn and counted as loopback, because only a
 * wildcard bind counted as exposed; a database on a public IP then raised
 * nothing anywhere. Each socket now says where it answers, with the network
 * and the interface named, and a service in both families counts once.
 */

async function mockHost(page: Page, listeners: unknown[] = hostPorts) {
  await mockProxy(page, { included: true })
  // Registered after the proxy tables, so it answers first.
  await page.route("**/api/v1/ports", (route) => json(route, listeners))
}

function tile(page: Page, label: string) {
  return page.locator('[data-slot="stat-tile"]').filter({ hasText: label })
}

test("each socket says where it answers, with the network named", async ({ page }) => {
  await mockHost(page)
  await page.goto("/proxy/ports")

  const rows = page.getByRole("table").locator("tbody tr")
  // Seven services on nine sockets: sshd and caddy each answer in both families.
  await expect(rows).toHaveCount(7)

  const caddy = rows.filter({ hasText: "caddy" })
  await expect(caddy.getByText("Tailnet only", { exact: true })).toBeVisible()
  await expect(caddy.getByText("tailscale0", { exact: true })).toBeVisible()
  await expect(caddy.getByText("100.110.34.31, fd7a:115c:a1e0::9e37:2220")).toBeVisible()

  const dhcp = rows.filter({ hasText: "systemd-network" })
  await expect(dhcp.getByText("Public address", { exact: true })).toBeVisible()
  await expect(dhcp.getByText("ens3", { exact: true })).toBeVisible()

  const sshd = rows.filter({ hasText: "sshd" })
  await expect(sshd.getByText("Every interface", { exact: true })).toBeVisible()
  await expect(sshd.getByText("0.0.0.0, ::", { exact: true })).toBeVisible()

  const exporter = rows.filter({ hasText: "node_exporter" })
  await expect(exporter.getByText("Docker bridge", { exact: true })).toBeVisible()
  await expect(exporter.getByText("docker0", { exact: true })).toBeVisible()

  // Redis on a public IP is critical, named as the database it is.
  const redis = rows.filter({ hasText: "203.0.113.5" }).getByText("Redis · Public address")
  await expect(redis).toHaveClass(/text-destructive/)

  // Only real loopback reads as this server's, and nothing reads "loopback".
  await expect(rows.getByText("This server only", { exact: true })).toHaveCount(2)
  await expect(page.getByRole("table").getByText(/loopback/i)).toHaveCount(0)
})

test("the chips split the services by who can connect, counting a pair once", async ({ page }) => {
  await mockHost(page)
  await page.goto("/proxy/ports")
  const rows = page.getByRole("table").locator("tbody tr")

  await expect(page.getByRole("button", { name: "All 7" })).toBeVisible()
  await page.getByRole("button", { name: "Internet-facing 3" }).click()
  await expect(rows).toHaveCount(3)
  for (const process of ["sshd", "systemd-network", "redis-server"]) {
    await expect(rows.filter({ hasText: process })).toHaveCount(1)
  }

  await page.getByRole("button", { name: "Private networks 2" }).click()
  await expect(rows).toHaveCount(2)
  await expect(rows.filter({ hasText: "caddy" })).toHaveCount(1)
  await expect(rows.filter({ hasText: "node_exporter" })).toHaveCount(1)

  await page.getByRole("button", { name: "This server 2" }).click()
  await expect(rows).toHaveCount(2)
  await expect(rows.filter({ hasText: "127.0.0.53" })).toHaveCount(1)
  await expect(rows.filter({ hasText: "127.0.0.1" })).toHaveCount(1)

  // The interface is searchable, and a filter that excludes it finds nothing.
  await page.getByPlaceholder("Port, process, user or address").fill("tailscale0")
  await expect(page.getByText("No sockets match")).toBeVisible()
  await page.getByRole("button", { name: "All 7" }).click()
  await expect(rows).toHaveCount(1)
  await expect(rows.filter({ hasText: "caddy" })).toHaveCount(1)
})

test("the tiles count services and say what each count is made of", async ({ page }) => {
  await mockHost(page)
  await page.goto("/proxy/ports")

  await expect(tile(page, "Listening").getByText("7", { exact: true })).toBeVisible()
  await expect(tile(page, "Listening").getByText("5 TCP · 2 UDP")).toBeVisible()
  await expect(tile(page, "Internet-facing").getByText("3", { exact: true })).toBeVisible()
  await expect(tile(page, "Internet-facing").getByText("1 on all · 2 on a public IP")).toBeVisible()
  await expect(tile(page, "Private networks").getByText("2", { exact: true })).toBeVisible()
  await expect(tile(page, "Private networks").getByText("tailnet · Docker")).toBeVisible()
  await expect(tile(page, "Databases exposed").getByText("1", { exact: true })).toHaveClass(
    /text-destructive/,
  )
})

test("a host with nothing the internet can reach reads so", async ({ page }) => {
  await mockHost(
    page,
    hostPorts.filter((l) => l.reach !== "all" && l.reach !== "public"),
  )
  await page.goto("/proxy/ports")
  const internet = tile(page, "Internet-facing")
  await expect(internet.getByText("0", { exact: true })).toHaveClass(/text-success/)
  await expect(internet.getByText("nothing the internet can reach")).toBeVisible()
  // A chip with nothing behind it is not offered.
  await expect(page.getByRole("button", { name: /^Internet-facing/ })).toHaveCount(0)
  // caddy on the tailnet is a notice, not an alarm.
  const caddy = page.getByRole("table").locator("tbody tr").filter({ hasText: "caddy" })
  await expect(caddy.getByText("Tailnet only", { exact: true })).toHaveClass(
    /text-muted-foreground/,
  )
})

test("a socket on one address can be taken to the firewall", async ({ page }) => {
  await mockHost(page)
  await page.goto("/proxy/ports")

  await page.getByRole("button", { name: "Actions for tcp port 8443" }).click()
  await page.getByRole("menuitem", { name: "Firewall" }).click()
  await expect(page).toHaveURL(/\/security\/firewall$/)
})

test("the overview counts services and names a public database critical", async ({ page }) => {
  await mockHost(page)
  await page.goto("/proxy")

  const exposed = page.locator('[data-slot="stat-tile"]').filter({ hasText: "Exposed ports" })
  await expect(exposed.getByText("5", { exact: true })).toBeVisible()
  await expect(exposed.getByText("of 7 listening, off the machine")).toBeVisible()

  const finding = page.getByRole("button", { name: /^Redis answers on 203\.0\.113\.5/ })
  await expect(finding.locator(".bg-destructive")).toHaveCount(1)
  await finding.click()
  await expect(
    page.getByText("6379/tcp redis-server on 203.0.113.5 (Public address · ens3)"),
  ).toBeVisible()
  await expect(page.getByText(/answers on every interface/)).toHaveCount(0)
})

test("a database is coloured by who can connect, and counted once however it is bound", async ({
  page,
}) => {
  await mockHost(page, bridgedDatabases)
  await page.goto("/proxy/ports")

  // Postgres on every interface in both families is one row, and critical;
  // Redis on the Docker bridges and MongoDB on a link-local address are
  // warnings, as the posture calls them.
  const rows = page.getByRole("table").locator("tbody tr")
  await expect(rows).toHaveCount(4)
  const postgres = rows.filter({ hasText: "0.0.0.0, ::" }).getByText("PostgreSQL · Every interface")
  await expect(postgres).toHaveClass(/text-destructive/)
  for (const [address, bridge, label] of [
    ["10.0.0.1", "docker0", "Redis · Docker bridge"],
    ["10.0.2.1", "br-b05f8e098ad7", "Redis · Docker bridge"],
    ["fe80::b482:4dff:fe92:4281", "docker0", "MongoDB · Docker bridge"],
  ]) {
    const row = rows.filter({ hasText: address })
    const status = row.getByText(label)
    await expect(status).toHaveClass(/text-warning/)
    await expect(status).not.toHaveClass(/text-destructive/)
    await expect(row.getByText(bridge, { exact: true })).toBeVisible()
  }

  // Three databases on five sockets.
  await expect(tile(page, "Databases exposed").getByText("3", { exact: true })).toBeVisible()

  await page.goto("/proxy")
  const finding = page.getByRole("button", {
    name: /^3 database or control ports answer off this machine/,
  })
  await expect(finding.locator(".bg-destructive")).toHaveCount(1)
})

test("databases only a bridge can reach are a warning everywhere", async ({ page }) => {
  await mockHost(
    page,
    bridgedDatabases.filter((l) => l.port !== 5432),
  )
  await page.goto("/proxy/ports")
  await expect(tile(page, "Databases exposed").getByText("2", { exact: true })).toHaveClass(
    /text-warning/,
  )

  await page.goto("/proxy")
  const finding = page.getByRole("button", {
    name: /^2 database or control ports answer off this machine/,
  })
  await expect(finding.locator(".bg-warning")).toHaveCount(1)
  await finding.click()
  await expect(
    page.getByText(
      "6379/tcp redis-server on 10.0.0.1 (Docker bridge · docker0), 10.0.2.1 (Docker bridge · br-b05f8e098ad7)",
      { exact: false },
    ),
  ).toBeVisible()
})

test("the tiles' hints and the longest reach fit a phone", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 })
  await mockHost(page, [...hostPorts, ...privateUplink])
  await page.goto("/proxy/ports")
  // Every tile's hint is whole, not cut at the tile's edge.
  for (const text of [
    "1 on all · 2 on a public IP",
    "tailnet · Docker +2",
    "counted once per port",
  ]) {
    const hint = page.getByText(text)
    await expect(hint).toBeVisible()
    expect(await hint.evaluate((el) => el.scrollWidth > el.clientWidth)).toBe(false)
  }
  // The phone's rows draw the longest labels whole and inside the screen.
  const rows = page.getByRole("list")
  for (const label of ["the Docker API · Private network", "Elasticsearch · VPN only"]) {
    const status = rows.getByText(label)
    await expect(status).toBeVisible()
    const box = await status.boundingBox()
    expect(box && box.x + box.width).toBeLessThanOrEqual(390)
  }
  await expect(rows.getByText("enp0s31f6", { exact: true })).toBeVisible()
  const overflow = await page.evaluate(
    () => document.documentElement.scrollWidth > document.documentElement.clientWidth,
  )
  expect(overflow).toBe(false)
})

test("a listing that timed out can be tried again", async ({ page }) => {
  await mockProxy(page, { included: true })
  let requests = 0
  await page.route("**/api/v1/ports", async (route) => {
    if (++requests > 1) return json(route, hostPorts)
    await route.fulfill({
      status: 504,
      contentType: "application/json",
      body: JSON.stringify({
        error: {
          code: "timeout",
          message: "Listing the host's sockets took longer than 10 seconds.",
          retryable: true,
        },
      }),
    })
  })
  await page.goto("/proxy/ports")

  await expect(
    page.getByText("Listing the host's sockets took longer than 10 seconds."),
  ).toBeVisible()
  await page.getByRole("button", { name: "Try again" }).click()
  await expect(page.getByRole("table").locator("tbody tr").first()).toBeVisible()
  expect(requests).toBe(2)
})
