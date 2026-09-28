import { expect, test, type Page } from "@playwright/test"
import { readFile } from "node:fs/promises"
import {
  bridgePair,
  bridgedDatabases,
  dockerIngress,
  firewalledDatabases,
  gitDaemons,
  hostPorts,
  pastFirewallDatabases,
  privateUplink,
} from "./fixtures/proxy/ports"
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

  // The interface is searchable, and a filter that excludes it finds nothing;
  // the chips then say where the match is.
  await page.getByPlaceholder("Port, process, user or address").fill("tailscale0")
  await expect(page.getByText("No sockets match")).toBeVisible()
  await expect(page.getByRole("button", { name: "Private networks 1" })).toBeVisible()
  await page.getByRole("button", { name: "All 1" }).click()
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

  await page.getByRole("button", { name: "Actions for tcp 100.110.34.31:8443" }).click()
  await page.getByRole("menuitem", { name: "Firewall" }).click()
  await expect(page).toHaveURL(/\/security\/firewall$/)
})

test("the overview counts services and names a public database critical", async ({ page }) => {
  await mockHost(page)
  await page.goto("/proxy")

  // The ports page's own split: Internet-facing, with the private networks
  // in the hint, both figures the page it links to shows.
  const internet = page.getByRole("link", { name: "Internet-facing ports" })
  await expect(internet.getByText("3", { exact: true })).toBeVisible()
  await expect(internet.getByText("2 on private networks")).toBeVisible()

  const finding = page.getByRole("button", { name: /^Redis answers on 203\.0\.113\.5/ })
  await expect(finding.locator(".bg-destructive")).toHaveCount(1)
  await finding.click()
  await expect(
    page.getByText("6379/tcp redis-server on 203.0.113.5 (Public address · ens3)"),
  ).toBeVisible()
  await expect(page.getByText(/answers on every interface/)).toHaveCount(0)

  // The tile opens on the rows it counts.
  await internet.click()
  await expect(page).toHaveURL(/\/proxy\/ports\?reach=internet$/)
  await expect(rowsOf(page)).toHaveCount(3)
  await expect(tile(page, "Internet-facing").getByText("3", { exact: true })).toBeVisible()
  await expect(tile(page, "Private networks").getByText("2", { exact: true })).toBeVisible()
})

test("a port Docker publishes in both families is one row and one count", async ({ page }) => {
  // One docker-proxy per family holds each of 80 and 443, as on this host.
  await mockHost(page, [...hostPorts, ...dockerIngress])
  await page.goto("/proxy/ports")

  const rows = page.getByRole("table").locator("tbody tr")
  await expect(rows).toHaveCount(9)
  const http = rows.filter({ hasText: "PIDs 1883643, 1883650" })
  await expect(http).toHaveCount(1)
  await expect(http.locator("td").first().locator("p.font-mono")).toHaveText("0.0.0.0, ::")
  await expect(rows.filter({ hasText: "PIDs 1883666, 1883672" })).toHaveCount(1)
  await expect(tile(page, "Internet-facing").getByText("5", { exact: true })).toBeVisible()
  await expect(page.getByRole("button", { name: "Internet-facing 5" })).toBeVisible()

  await page.goto("/proxy")
  const internet = page.getByRole("link", { name: "Internet-facing ports" })
  await expect(internet.getByText("5", { exact: true })).toBeVisible()
})

test("a database behind a firewall denying inbound is the posture's warning everywhere", async ({
  page,
}) => {
  await mockHost(page, firewalledDatabases)
  await page.goto("/proxy/ports")

  const rows = page.getByRole("table").locator("tbody tr")
  await expect(rows).toHaveCount(2)
  for (const label of ["Redis · Every interface", "PostgreSQL · Public address"]) {
    const status = rows.getByText(label)
    await expect(status).toHaveClass(/text-warning/)
    await expect(status).not.toHaveClass(/text-destructive/)
  }
  await expect(rows.getByText("Firewall's inbound default: deny")).toHaveCount(2)
  await expect(tile(page, "Databases exposed").getByText("2", { exact: true })).toHaveClass(
    /text-warning/,
  )

  await page.goto("/proxy")
  const finding = page.getByRole("button", {
    name: /^2 database or control ports answer off this machine/,
  })
  await expect(finding.locator(".bg-warning")).toHaveCount(1)
  await expect(finding.locator(".bg-destructive")).toHaveCount(0)
  await finding.click()
  await expect(
    page.getByText(
      "6379/tcp redis-server, 5432/tcp postgres on 57.131.21.87 (Public address · ens3), though the firewall's inbound default is deny",
    ),
  ).toBeVisible()
})

test("a database Docker publishes or a rule admits stays critical behind a firewall denying inbound", async ({
  page,
}) => {
  await mockHost(page, pastFirewallDatabases)
  await page.goto("/proxy/ports")

  const rows = page.getByRole("table").locator("tbody tr")
  await expect(rows).toHaveCount(3)
  const postgres = rows.filter({ hasText: "PIDs 1883700, 1883706" })
  await expect(postgres.getByText("PostgreSQL · Every interface")).toHaveClass(/text-destructive/)
  await expect(postgres.getByText("Published by Docker past the firewall")).toBeVisible()
  await expect(postgres.getByText(/inbound default/)).toHaveCount(0)
  const redis = rows.filter({ hasText: "redis-server" })
  await expect(redis.getByText("Redis · Every interface")).toHaveClass(/text-destructive/)
  await expect(redis.getByText("Firewall rule 10 admits it from anywhere")).toBeVisible()
  await expect(redis.getByText(/inbound default/)).toHaveCount(0)
  const mongo = rows.filter({ hasText: "mongod" })
  await expect(mongo.getByText("MongoDB · Every interface")).toHaveClass(/text-warning/)
  await expect(mongo.getByText("Firewall's inbound default: deny")).toBeVisible()
  await expect(tile(page, "Databases exposed").getByText("3", { exact: true })).toHaveClass(
    /text-destructive/,
  )

  await page.goto("/proxy")
  const finding = page.getByRole("button", {
    name: /^3 database or control ports answer on every interface/,
  })
  await expect(finding.locator(".bg-destructive")).toHaveCount(1)
  await finding.click()
  await expect(
    page.getByText(
      "5432/tcp docker-proxy (published by Docker past the firewall), 6379/tcp redis-server (firewall rule 10 admits it from anywhere), 27017/tcp mongod (the firewall's inbound default is deny)",
    ),
  ).toBeVisible()
  await expect(page.getByText(/, though the firewall's inbound default is deny/)).toHaveCount(0)

  // On a phone the reasons sit whole under each reach, inside the screen.
  await page.setViewportSize({ width: 390, height: 844 })
  await page.goto("/proxy/ports")
  const list = page.getByRole("list")
  for (const text of [
    "Published by Docker past the firewall",
    "Firewall rule 10 admits it from anywhere",
    "Firewall's inbound default: deny",
  ]) {
    const line = list.getByText(text)
    await expect(line).toBeVisible()
    const box = await line.boundingBox()
    expect(box && box.x + box.width).toBeLessThanOrEqual(390)
  }
  const overflow = await page.evaluate(
    () => document.documentElement.scrollWidth > document.documentElement.clientWidth,
  )
  expect(overflow).toBe(false)
})

test("a socket on the private uplink is not said to be out of the internet's reach", async ({
  page,
}) => {
  await mockHost(page, [
    ...hostPorts.filter((l) => l.reach !== "all" && l.reach !== "public"),
    privateUplink[0],
  ])
  await page.goto("/proxy/ports")

  const internet = tile(page, "Internet-facing")
  await expect(internet.getByText("+1 if the uplink is mapped")).toBeVisible()
  await expect(internet.getByText("nothing the internet can reach")).toHaveCount(0)
  await expect(internet.getByText("0", { exact: true })).not.toHaveClass(/text-success/)

  const row = page.getByRole("table").locator("tbody tr").filter({ hasText: "172.31.5.9" })
  const status = row.getByText("the Docker API · Private uplink")
  await expect(status).toHaveClass(/text-warning/)
  await expect(
    row.locator("[title^='A private address on the interface with the default route']"),
  ).toHaveCount(1)
  await expect(row.getByText("enp0s31f6", { exact: true })).toBeVisible()
  // It is counted with the private networks, which the internet may not reach.
  await expect(tile(page, "Private networks").getByText("3", { exact: true })).toBeVisible()
})

test("a folded pair's addresses, protocol and user are whole on a phone and a desktop", async ({
  page,
}) => {
  await mockHost(page, [...hostPorts, ...bridgePair])
  const whole = async (el: import("@playwright/test").Locator) => {
    await expect(el).toBeVisible()
    return el.evaluate((node) => node.scrollWidth <= node.clientWidth)
  }

  await page.setViewportSize({ width: 390, height: 844 })
  await page.goto("/proxy/ports")
  const item = page.getByRole("list").locator("li").filter({ hasText: "46505" })
  await expect(item).toHaveCount(1)
  const line = item.locator("p.font-mono", { hasText: "10.0.0.1" })
  await expect(line).toContainText("10.0.0.1")
  await expect(line).toContainText("fe80::b482:4dff:fe92:4281")
  expect(await whole(line)).toBe(true)
  // The protocol, under the port, and the user, beside the process, are
  // whole and on the screen.
  const protocol = item.getByText("tcp", { exact: true })
  await expect(protocol).toBeVisible()
  const box = await protocol.boundingBox()
  expect(box && box.x + box.width).toBeLessThanOrEqual(390)
  await expect(item.getByText("· ubuntu")).toBeVisible()
  // The caddy pair's long IPv6 address is whole too.
  const caddy = page
    .getByRole("list")
    .locator("li")
    .filter({ hasText: "caddy" })
    .locator("p.font-mono", { hasText: "100.110.34.31" })
  expect(await whole(caddy)).toBe(true)
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth > document.documentElement.clientWidth,
    ),
  ).toBe(false)

  await page.setViewportSize({ width: 1280, height: 900 })
  const cell = page
    .getByRole("table")
    .locator("tbody tr")
    .filter({ hasText: "46505" })
    .locator("td")
    .first()
  const addresses = cell.locator("p.font-mono")
  // One address a line, both whole and inside the Endpoint column.
  await expect(addresses).toHaveText("10.0.0.1, fe80::b482:4dff:fe92:4281")
  expect(await whole(addresses)).toBe(true)
  const fits = await addresses.evaluate((node) => {
    const td = node.closest("td")!.getBoundingClientRect()
    const own = node.getBoundingClientRect()
    const lines = Math.round(own.height / parseFloat(getComputedStyle(node).lineHeight))
    return { inside: own.right <= td.right && own.left >= td.left, lines }
  })
  expect(fits.inside).toBe(true)
  expect(fits.lines).toBeGreaterThanOrEqual(2)
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
  for (const label of ["the Docker API · Private uplink", "Elasticsearch · VPN only"]) {
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

function rowsOf(page: Page) {
  return page.getByRole("table").locator("tbody tr")
}

/** The ports of the table's socket rows, top to bottom. */
function portOrder(page: Page) {
  return rowsOf(page).locator("td:first-child span.numeric").allTextContents()
}

test("reach and protocol combine, and the address bar carries the view", async ({ page }) => {
  await mockHost(page)
  await page.goto("/proxy/ports?reach=internet&proto=udp")
  const rows = rowsOf(page)
  const reach = page.getByRole("group", { name: "Reach" })
  const protocol = page.getByRole("group", { name: "Protocol" })

  // Internet-facing UDP is the DHCP client alone; each chip counts what it
  // would show beside the other filter.
  await expect(rows).toHaveCount(1)
  await expect(rows.filter({ hasText: "systemd-network" })).toHaveCount(1)
  await expect(reach.getByRole("button", { name: "Internet-facing 1" })).toHaveAttribute(
    "aria-pressed",
    "true",
  )
  await expect(reach.getByRole("button", { name: "This server 1" })).toBeVisible()
  await expect(protocol.getByRole("button", { name: "UDP 1" })).toHaveAttribute(
    "aria-pressed",
    "true",
  )
  await protocol.getByRole("button", { name: "TCP 2" }).click()
  await expect(rows).toHaveCount(2)
  await expect(page).toHaveURL(/\/proxy\/ports\?reach=internet&proto=tcp$/)
  // A second press on the chosen protocol lists both again.
  await protocol.getByRole("button", { name: "TCP 2" }).click()
  await expect(rows).toHaveCount(3)
  await expect(page).toHaveURL(/\/proxy\/ports\?reach=internet$/)

  // The search and the sort go into the address too, readably.
  await page.getByLabel("Search sockets").fill(":22")
  await page.getByRole("columnheader", { name: "Endpoint" }).getByRole("button").click()
  await expect(page).toHaveURL(/\/proxy\/ports\?q=:22&reach=internet&sort=port$/)
  await expect(rows).toHaveCount(1)

  // Opened in a tab with no memory of this one, the link is the same view.
  const link = page.url()
  const other = await page.context().newPage()
  await mockHost(other)
  await other.goto(link)
  await expect(other.getByLabel("Search sockets")).toHaveValue(":22")
  await expect(rowsOf(other)).toHaveCount(1)
  await expect(rowsOf(other).filter({ hasText: "sshd" })).toHaveCount(1)
  await expect(other.getByRole("columnheader", { name: "Endpoint" })).toHaveAttribute(
    "aria-sort",
    "ascending",
  )

  // A link naming part of the view names all of it: what this tab typed
  // does not narrow what the link asks for.
  await page.goto("/proxy/ports?reach=private")
  await expect(page.getByLabel("Search sockets")).toHaveValue("")
  await expect(rows).toHaveCount(2)
  await page.goto("/proxy/ports")
  await expect(rows).toHaveCount(2)
  await expect(page).toHaveURL(/\/proxy\/ports\?reach=private$/)
})

test("each column sorts both ways and says which way to a screen reader", async ({ page }) => {
  await mockHost(page)
  await page.goto("/proxy/ports")
  const heading = (name: string) => page.getByRole("columnheader", { name })

  // Worst first: the critical Redis, what the internet reaches, the private
  // networks, then this server's own.
  await expect(heading("Reach")).toHaveAttribute("aria-sort", "descending")
  await expect(heading("Endpoint")).toHaveAttribute("aria-sort", "none")
  expect(await portOrder(page)).toEqual(["6379", "22", "68", "8443", "9100", "53", "5432"])

  await heading("Endpoint").getByRole("button").click()
  await expect(heading("Endpoint")).toHaveAttribute("aria-sort", "ascending")
  await expect(heading("Reach")).toHaveAttribute("aria-sort", "none")
  expect(await portOrder(page)).toEqual(["22", "53", "68", "5432", "6379", "8443", "9100"])
  await heading("Endpoint").getByRole("button").click()
  await expect(heading("Endpoint")).toHaveAttribute("aria-sort", "descending")
  expect(await portOrder(page)).toEqual(["9100", "8443", "6379", "5432", "68", "53", "22"])

  await heading("Application").getByRole("button").click()
  await expect(heading("Application")).toHaveAttribute("aria-sort", "ascending")
  await expect(rowsOf(page).first()).toContainText("caddy")
  await heading("Reach").getByRole("button").click()
  await expect(heading("Reach")).toHaveAttribute("aria-sort", "descending")
  expect((await portOrder(page))[0]).toBe("6379")

  // A phone has no column headings; the same orders are one menu.
  await page.setViewportSize({ width: 390, height: 844 })
  await page.getByRole("combobox", { name: "Sort" }).click()
  await page.getByRole("option", { name: "Port, high to low" }).click()
  const items = page.getByRole("list", { name: "Listening sockets" }).locator("li")
  await expect(items.first()).toContainText("9100")
  await expect(page).toHaveURL(/sort=-port/)
})

test("twenty git-daemon sockets fold under one application, and the kernel's ephemeral ports can be set aside", async ({
  page,
}) => {
  await mockHost(page, [...hostPorts, ...gitDaemons])
  await page.goto("/proxy/ports")
  const rows = rowsOf(page)
  await expect(rows).toHaveCount(27)

  await page.getByRole("button", { name: "Group by application" }).click()
  await expect(rows).toHaveCount(8)
  const group = rows.filter({ hasText: "20 sockets" })
  await expect(group).toHaveCount(1)
  await expect(group).toContainText("33012, 34333, 35654 +17")
  await expect(group).toContainText("20 PIDs")
  const toggle = group.getByRole("button", { name: "git-daemon, 20 sockets" })
  await expect(toggle).toHaveAttribute("aria-expanded", "false")
  await toggle.click()
  await expect(toggle).toHaveAttribute("aria-expanded", "true")
  await expect(rows).toHaveCount(28)
  // The row itself is the pointer's target too.
  await group.locator("td").first().click()
  await expect(rows).toHaveCount(8)
  await page.getByRole("button", { name: "Group by application" }).click()
  await expect(rows).toHaveCount(27)

  // Every git-daemon socket is on loopback inside 32768–60999.
  const hide = page.getByRole("button", { name: "Hide loopback ephemeral ports 20" })
  await hide.click()
  await expect(hide).toHaveAttribute("aria-pressed", "true")
  await expect(rows).toHaveCount(7)
  await expect(page.getByText("20 on loopback ports 32768–60999 hidden")).toBeVisible()
  await expect(page.getByText("7 of 27 sockets")).toBeVisible()
  // The tiles still count the host.
  await expect(tile(page, "Listening").getByText("27", { exact: true })).toBeVisible()

  // A search for a hidden socket says it is hidden rather than absent.
  await page.getByLabel("Search sockets").fill(":33012")
  await expect(page.getByText("No sockets match")).toBeVisible()
  await expect(
    page.getByText("1 loopback socket on ephemeral ports would, but it is hidden."),
  ).toBeVisible()
  await page.getByRole("button", { name: "Show 1 hidden socket" }).click()
  await expect(rows).toHaveCount(1)
  await expect(rows.first()).toContainText("git-daemon")
  await page.getByLabel("Search sockets").fill("nothing-listens-as-this")
  await page.getByRole("button", { name: "Clear filters" }).click()
  await expect(rows).toHaveCount(27)
  await expect(page.getByLabel("Search sockets")).toHaveValue("")
})

test("a host whose kernel range is unknown is not offered to hide by it", async ({ page }) => {
  await mockHost(page, [...hostPorts, ...gitDaemons])
  await page.route("**/api/v1/ports/meta", (route) => json(route, { ephemeralRange: null }))
  await page.goto("/proxy/ports")
  await expect(rowsOf(page)).toHaveCount(27)
  await expect(page.getByRole("button", { name: /^Hide loopback ephemeral ports/ })).toHaveCount(0)
})

test("a failed poll keeps the rows and says they are the last list that arrived", async ({
  page,
}) => {
  await page.clock.install()
  await mockProxy(page, { included: true })
  let requests = 0
  await page.route("**/api/v1/ports", async (route) => {
    if (++requests !== 2) return json(route, hostPorts)
    await route.fulfill({
      status: 500,
      contentType: "application/json",
      body: JSON.stringify({ error: { code: "internal", message: "Reading /proc failed." } }),
    })
  })
  await page.goto("/proxy/ports")
  const rows = rowsOf(page)
  await expect(rows).toHaveCount(7)
  await expect(page.getByText("Updated just now")).toBeVisible()
  await expect(page.getByText(/Refreshing failed/)).toHaveCount(0)

  await page.clock.runFor(15_001)
  await expect(
    page.getByText("Refreshing failed, so this is the last list that arrived"),
  ).toBeVisible()
  await expect(
    page.getByText(/Reading \/proc failed\. The page tries again every 15/),
  ).toBeVisible()
  await expect(page.getByText(/^Last updated \d+s ago$/)).toBeVisible()
  await expect(rows).toHaveCount(7)
  expect(requests).toBe(2)

  await page.getByRole("button", { name: "Try again" }).click()
  await expect(page.getByText(/Refreshing failed/)).toHaveCount(0)
  await expect(page.getByText(/^Updated (just now|\d+s ago)$/)).toBeVisible()
  expect(requests).toBe(3)
})

test("refreshing can be paused, resumed at once, and asked for", async ({ page }) => {
  await page.clock.install()
  await mockProxy(page, { included: true })
  let requests = 0
  await page.route("**/api/v1/ports", async (route) => {
    requests++
    await json(route, hostPorts)
  })
  await page.goto("/proxy/ports")
  await expect(rowsOf(page)).toHaveCount(7)
  expect(requests).toBe(1)

  await page.getByRole("button", { name: "Pause refreshing" }).click()
  await expect(page.getByText("Paused · updated just now")).toBeVisible()
  await expect(page.getByRole("button", { name: "Refresh now" })).toHaveCount(0)
  await page.clock.runFor(60_000)
  await expect(page.getByText("Paused · updated 1m ago")).toBeVisible()
  expect(requests).toBe(1)

  await page.getByRole("button", { name: "Resume refreshing" }).click()
  await expect(page.getByText("Updated just now")).toBeVisible()
  expect(requests).toBe(2)
  await page.getByRole("button", { name: "Refresh now" }).click()
  await expect.poll(() => requests).toBe(3)
  await page.clock.runFor(15_001)
  await expect.poll(() => requests).toBe(4)
})

test("a row's endpoint and command can be copied", async ({ page, context }) => {
  await context.grantPermissions(["clipboard-read", "clipboard-write"])
  await mockHost(page)
  await page.goto("/proxy/ports")

  await page.getByRole("button", { name: "Actions for tcp 0.0.0.0:22" }).click()
  await page.getByRole("menuitem", { name: "Copy endpoint" }).click()
  await expect(page.getByText("Copied 0.0.0.0:22")).toBeVisible()
  expect(await page.evaluate(() => navigator.clipboard.readText())).toBe("0.0.0.0:22")

  await page.getByRole("button", { name: "Actions for tcp 0.0.0.0:22" }).click()
  await page.getByRole("menuitem", { name: "Copy command" }).click()
  expect(await page.evaluate(() => navigator.clipboard.readText())).toBe(
    "sshd: /usr/sbin/sshd -D [listener] 0 of 10-100 startups",
  )

  // A folded pair's endpoint is its first address, IPv4 as the row lists it.
  await page.getByLabel("Search sockets").fill("caddy")
  await page.getByRole("button", { name: "Actions for tcp 100.110.34.31:8443" }).click()
  await page.getByRole("menuitem", { name: "Copy endpoint" }).click()
  expect(await page.evaluate(() => navigator.clipboard.readText())).toBe("100.110.34.31:8443")
})

test("the rows shown export as CSV and JSON, one line per socket", async ({ page }) => {
  await mockHost(page)
  await page.goto("/proxy/ports?reach=private")
  await expect(rowsOf(page)).toHaveCount(2)

  const csv = page.waitForEvent("download")
  await page.getByRole("button", { name: "Export" }).click()
  await page.getByRole("menuitem", { name: "CSV" }).click()
  const csvFile = await csv
  expect(csvFile.suggestedFilename()).toMatch(/^listening-sockets-[\d-]+\.csv$/)
  const lines = (await readFile(await csvFile.path(), "utf8")).trimEnd().split("\n")
  // caddy's two families and node_exporter: three sockets on two rows.
  expect(lines).toHaveLength(4)
  expect(lines[0]).toBe(
    "protocol,family,address,port,endpoint,reach,network,interface,process,pid,ppid,user,cmdline,level",
  )
  expect(lines.some((line) => line.includes("[fd7a:115c:a1e0::9e37:2220]:8443"))).toBe(true)

  const json = page.waitForEvent("download")
  await page.getByRole("button", { name: "Export" }).click()
  await page.getByRole("menuitem", { name: "JSON" }).click()
  const jsonFile = await json
  expect(jsonFile.suggestedFilename()).toMatch(/^listening-sockets-[\d-]+\.json$/)
  const sockets = JSON.parse(await readFile(await jsonFile.path(), "utf8"))
  expect(sockets.map((s: { process: string }) => s.process).sort()).toEqual([
    "caddy",
    "caddy",
    "node_exporter",
  ])
})

test("a host with no sockets, and a filter with no match, each say which it is", async ({
  page,
}) => {
  await mockHost(page, [])
  await page.goto("/proxy/ports")
  await expect(page.getByText("Nothing is listening")).toBeVisible()
  await expect(page.getByText("No sockets match")).toHaveCount(0)

  await mockHost(page, hostPorts)
  await page.goto("/proxy/ports?q=user:nobody-here")
  await expect(page.getByText("No sockets match")).toBeVisible()
  await expect(page.getByText("Nothing is listening")).toHaveCount(0)
  await page.getByRole("button", { name: "Clear filters" }).click()
  await expect(rowsOf(page)).toHaveCount(7)
  await expect(page).toHaveURL(/\/proxy\/ports$/)
})

test("the attention finding opens the ports page on its ports alone", async ({ page }) => {
  await mockHost(page)
  await page.goto("/proxy/ports?q=sshd&reach=internet")
  await expect(rowsOf(page)).toHaveCount(1)

  await page.goto("/proxy")
  await page.getByRole("button", { name: /^Redis answers on 203\.0\.113\.5/ }).click()
  await page.getByRole("button", { name: "Open ports" }).click()
  await expect(page).toHaveURL(/\/proxy\/ports\?q=port:6379$/)
  await expect(page.getByLabel("Search sockets")).toHaveValue("port:6379")
  await expect(
    page.getByRole("group", { name: "Reach" }).getByRole("button", { name: "All 1" }),
  ).toHaveAttribute("aria-pressed", "true")
  await expect(rowsOf(page)).toHaveCount(1)
  await expect(rowsOf(page).first()).toContainText("redis-server")
})

test("the Internet-facing tile opens on what it counts, not the ports a finding opened", async ({
  page,
}) => {
  await mockHost(page)
  await page.goto("/proxy")
  await page.getByRole("button", { name: /^Redis answers on 203\.0\.113\.5/ }).click()
  await page.getByRole("button", { name: "Open ports" }).click()
  await expect(page).toHaveURL(/\/proxy\/ports\?q=port:6379$/)
  await expect(rowsOf(page)).toHaveCount(1)

  // The tab now remembers `port:6379`; the tile asks for its own rows anyway.
  await page.goBack()
  const internet = page.getByRole("link", { name: "Internet-facing ports" })
  await expect(internet.getByText("3", { exact: true })).toBeVisible()
  await internet.click()
  await expect(page).toHaveURL(/\/proxy\/ports\?reach=internet$/)
  await expect(page.getByLabel("Search sockets")).toHaveValue("")
  await expect(
    page.getByRole("group", { name: "Reach" }).getByRole("button", { name: "Internet-facing 3" }),
  ).toHaveAttribute("aria-pressed", "true")
  await expect(rowsOf(page)).toHaveCount(3)
  await expect(rowsOf(page).filter({ hasText: "redis-server" })).toHaveCount(1)
})

async function mockConnections(page: Page) {
  await page.route("**/api/v1/connections", (route) =>
    json(route, {
      total: 5,
      listening: 9,
      loopback: 0,
      peers: [
        {
          address: "203.0.113.50",
          count: 3,
          established: 3,
          ports: [22, 51234],
          processes: ["sshd", "curl"],
          private: false,
        },
      ],
    }),
  )
}

test("a connection's service port opens what listens on it; a port the kernel picked does not", async ({
  page,
}) => {
  await mockHost(page)
  await mockConnections(page)
  await page.goto("/security/connections")
  const row = page.getByRole("row").filter({ hasText: "203.0.113.50" })
  await expect(row.getByText("51234")).toBeVisible()
  await expect(row.getByRole("link", { name: "What listens on port 51234" })).toHaveCount(0)
  await row.getByRole("link", { name: "What listens on port 22" }).click()
  await expect(page).toHaveURL(/\/proxy\/ports\?q=:22$/)
  await expect(rowsOf(page)).toHaveCount(1)
  await expect(rowsOf(page).first()).toContainText("sshd")
})

test("the Listening tile opens every socket, not the port a link opened a moment ago", async ({
  page,
}) => {
  await mockHost(page)
  await mockConnections(page)
  await page.goto("/security/connections")
  await page
    .getByRole("row")
    .filter({ hasText: "203.0.113.50" })
    .getByRole("link", { name: "What listens on port 22" })
    .click()
  await expect(page).toHaveURL(/\/proxy\/ports\?q=:22$/)
  await expect(rowsOf(page)).toHaveCount(1)

  // The tab now remembers `:22`; the tile asks for the whole list anyway.
  await page.goBack()
  await page.getByRole("link", { name: "Listening ports" }).click()
  await expect(page.getByLabel("Search sockets")).toHaveValue("")
  await expect(rowsOf(page)).toHaveCount(7)
  await expect(page).toHaveURL(/\/proxy\/ports$/)
})

test("the list's controls are named, and a phone draws one list with no overflow", async ({
  page,
}) => {
  await mockHost(page, [...hostPorts, ...gitDaemons])
  await page.setViewportSize({ width: 390, height: 844 })
  await page.goto("/proxy/ports")
  // One shape at a time: no hidden table behind the phone's list.
  await expect(page.getByRole("table")).toHaveCount(0)
  const list = page.getByRole("list", { name: "Listening sockets" })
  await expect(list.locator("li")).toHaveCount(27)
  // Each row's menu is named after its socket.
  await expect(page.getByRole("button", { name: "Actions for tcp 0.0.0.0:22" })).toHaveCount(1)
  await expect(list.getByRole("button", { name: "More actions" })).toHaveCount(0)
  await page.getByRole("button", { name: "Group by application" }).click()
  await expect(list.locator("li")).toHaveCount(8)
  await page.getByRole("button", { name: "Hide loopback ephemeral ports 20" }).click()
  for (const width of [390, 1280]) {
    await page.setViewportSize({ width, height: 900 })
    const unnamed = await page.evaluate(
      () =>
        [...document.querySelectorAll("button")].filter(
          (b) =>
            b.offsetParent !== null &&
            !b.textContent?.trim() &&
            !b.getAttribute("aria-label") &&
            !b.getAttribute("aria-labelledby"),
        ).length,
    )
    expect(unnamed).toBe(0)
    expect(
      await page.evaluate(
        () => document.documentElement.scrollWidth > document.documentElement.clientWidth,
      ),
    ).toBe(false)
  }
  await expect(page.getByRole("table")).toHaveCount(1)
  await expect(page.getByRole("list", { name: "Listening sockets" })).toHaveCount(0)
})
