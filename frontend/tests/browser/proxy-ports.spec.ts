import { expect, test, type Page } from "@playwright/test"
import { readFile } from "node:fs/promises"
import {
  bridgePair,
  bridgedDatabases,
  dockerIngress,
  firewalledDatabases,
  gitDaemons,
  hostPorts,
  ownedPorts,
  pastFirewallDatabases,
  privateUplink,
} from "./fixtures/proxy/ports"
import { json, mockProxy, user } from "./proxy-fixtures"
import { risen } from "./risen"

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
  // No external source enrolled, as on most hosts: a sheet says so.
  await page.route("**/api/v1/ports/external", (route) =>
    json(route, { checkedAt: new Date().toISOString(), evidence: [], scopes: [] }),
  )
}

/**
 * A verb behind the `⋯` at the end of the page's line, the menu left open on
 * it: the line has one command, and the refresh, its pause and the exports
 * are here.
 */
async function listVerb(page: Page, name: string) {
  await page.getByRole("button", { name: "More actions for the list" }).click()
  return page.getByRole("menuitem", { name })
}

/** One of the chips over the list, by its name: its count follows the name. */
function chip(page: Page, name: string) {
  return page.getByRole("button", { name: new RegExp(`^${name} \\d+$`) })
}

/** One of the identity line's facts, which the four tiles became. */
function fact(page: Page, text: string) {
  return page.locator("[data-slot='host-identity']").getByText(text, { exact: true })
}

/** A row whose tooltip carries a note the one-line row has no room for. */
function titled(text: string) {
  return `[title*="${text}"]`
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

  // Only real loopback reads as this server's in the reach column.
  await expect(rows.getByText("This server only", { exact: true })).toHaveCount(2)
  await expect(rows.locator("td:nth-child(3)").getByText(/loopback/i)).toHaveCount(0)
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

test("the identity line counts services and says what each count is made of", async ({ page }) => {
  await mockHost(page)
  await page.goto("/proxy/ports")

  // The four tiles that opened the page are the line's facts now.
  await expect(page.locator('[data-slot="stat-grid"]')).toHaveCount(0)
  await expect(page.locator('[data-slot="stat-tile"]')).toHaveCount(0)
  await expect(fact(page, "7 listening")).toBeVisible()
  // The protocols and the private networks are the chips over the list, not
  // counted a second time on the line.
  await expect(fact(page, "5 TCP, 2 UDP")).toHaveCount(0)
  await expect(chip(page, "TCP")).toContainText("5")
  await expect(chip(page, "UDP")).toContainText("2")
  await expect(fact(page, "3 internet-facing")).toHaveAttribute(
    "title",
    "1 on all · 2 on a public IP",
  )
  await expect(fact(page, "3 internet-facing")).toHaveClass(/text-warning/)
  await expect(chip(page, "Private networks")).toContainText("2")
  await expect(chip(page, "Private networks")).toHaveAttribute("title", "tailnet · Docker")
  await expect(fact(page, "1 dangerous service exposed")).toHaveClass(/text-destructive/)
})

test("a host with nothing the internet can reach reads so", async ({ page }) => {
  await mockHost(
    page,
    hostPorts.filter((l) => l.reach !== "all" && l.reach !== "public"),
  )
  await page.goto("/proxy/ports")
  await expect(fact(page, "nothing the internet can reach")).toHaveClass(/text-success/)
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
  await expect(page).toHaveURL(/\/network\/firewall$/)
})

test("the overview counts observed internet-facing services without advisory findings", async ({
  page,
}) => {
  await mockHost(page)
  await page.goto("/proxy")

  // The ports page's own split: Internet-facing, a fact in the engine line,
  // with the private networks as its tooltip, both figures the page it
  // links to shows.
  const internet = page
    .locator("[data-slot='host-identity']")
    .getByRole("link", { name: "3 ports internet-facing" })
  await expect(internet).toBeVisible()
  await expect(internet).toHaveAttribute("title", "2 on private networks")

  await expect(page.getByRole("button", { name: /^Redis answers/ })).toHaveCount(0)

  // The fact opens on the rows it counts.
  await internet.click()
  await expect(page).toHaveURL(/\/proxy\/ports\?reach=internet$/)
  await expect(rowsOf(page)).toHaveCount(3)
  await expect(fact(page, "3 internet-facing")).toBeVisible()
  await expect(chip(page, "Private networks")).toContainText("2")
})

test("a port Docker publishes in both families is one row and one count", async ({ page }) => {
  // One docker-proxy per family holds each of 80 and 443, as on this host.
  await mockHost(page, [...hostPorts, ...dockerIngress])
  await page.goto("/proxy/ports")

  const rows = page.getByRole("table").locator("tbody tr")
  await expect(rows).toHaveCount(9)
  const http = rows.filter({ has: page.locator(titled("PID 1883643, 1883650")) })
  await expect(http).toHaveCount(1)
  await expect(http.locator("td").first()).toContainText("0.0.0.0, ::")
  await expect(rows.filter({ has: page.locator(titled("PID 1883666, 1883672")) })).toHaveCount(1)
  await expect(fact(page, "5 internet-facing")).toBeVisible()
  await expect(page.getByRole("button", { name: "Internet-facing 5" })).toBeVisible()

  await page.goto("/proxy")
  await expect(
    page
      .locator("[data-slot='host-identity']")
      .getByRole("link", { name: "5 ports internet-facing" }),
  ).toBeVisible()
})

test("the ports view retains observed firewall attribution", async ({ page }) => {
  await mockHost(page, firewalledDatabases)
  await page.goto("/proxy/ports")

  const rows = page.getByRole("table").locator("tbody tr")
  await expect(rows).toHaveCount(2)
  for (const label of ["Redis · Every interface", "PostgreSQL · Public address"]) {
    const status = rows.getByText(label)
    await expect(status).toHaveClass(/text-warning/)
    await expect(status).not.toHaveClass(/text-destructive/)
  }
  // The firewall's default is the reach's tooltip, and a line on the port's page.
  await expect(rows.locator(titled("Firewall's inbound default: deny"))).toHaveCount(2)
  await expect(fact(page, "2 dangerous services exposed")).toHaveClass(/text-warning/)
})

test("a database Docker publishes or a rule admits stays critical behind a firewall denying inbound", async ({
  page,
}) => {
  await mockHost(page, pastFirewallDatabases)
  await page.goto("/proxy/ports")

  const rows = page.getByRole("table").locator("tbody tr")
  await expect(rows).toHaveCount(3)
  const postgres = rows.filter({ has: page.locator(titled("PID 1883700, 1883706")) })
  await expect(postgres.getByText("PostgreSQL · Every interface")).toHaveClass(/text-destructive/)
  await expect(postgres.locator(titled("Published by Docker past the firewall"))).toHaveCount(1)
  await expect(postgres.locator(titled("inbound default"))).toHaveCount(0)
  const redis = rows.filter({ hasText: "redis-server" })
  await expect(redis.getByText("Redis · Every interface")).toHaveClass(/text-destructive/)
  await expect(redis.locator(titled("Firewall rule 10 admits it from anywhere"))).toHaveCount(1)
  await expect(redis.locator(titled("inbound default"))).toHaveCount(0)
  const mongo = rows.filter({ hasText: "mongod" })
  await expect(mongo.getByText("MongoDB · Every interface")).toHaveClass(/text-warning/)
  await expect(mongo.locator(titled("Firewall's inbound default: deny"))).toHaveCount(1)
  await expect(fact(page, "3 dangerous services exposed")).toHaveClass(/text-destructive/)

  // The port's page says it in words.
  await redis.getByRole("link", { name: /^Open tcp/ }).click()
  await expect(page).toHaveURL(/\/proxy\/ports\/tcp-6379$/)
  await expect(page.getByText("Firewall rule 10 admits it from anywhere")).toBeVisible()
})

test("a socket on the private uplink is not said to be out of the internet's reach", async ({
  page,
}) => {
  await mockHost(page, [
    ...hostPorts.filter((l) => l.reach !== "all" && l.reach !== "public"),
    privateUplink[0],
  ])
  await page.goto("/proxy/ports")

  const internet = fact(page, "1 internet-facing if the uplink is mapped")
  await expect(internet).toBeVisible()
  await expect(fact(page, "nothing the internet can reach")).toHaveCount(0)
  await expect(internet).not.toHaveClass(/text-success/)

  const row = page.getByRole("table").locator("tbody tr").filter({ hasText: "172.31.5.9" })
  const status = row.getByText("Docker API · Private uplink")
  await expect(status).toHaveClass(/text-warning/)
  await expect(
    row.locator("[title^='A private address on the interface with the default route']"),
  ).toHaveCount(1)
  await expect(row.getByText("enp0s31f6", { exact: true })).toBeVisible()
  // It is counted with the private networks, which the internet may not reach.
  await expect(chip(page, "Private networks")).toContainText("3")
})

test("a folded pair's addresses and protocol hold one line, whole on its page", async ({
  page,
}) => {
  await mockHost(page, [...hostPorts, ...bridgePair])

  await page.setViewportSize({ width: 390, height: 844 })
  await page.goto("/proxy/ports")
  const item = page
    .getByRole("list", { name: "Listening sockets" })
    .locator("li")
    .filter({ hasText: "46505" })
  await expect(item).toHaveCount(1)
  const line = item.locator(".font-mono", { hasText: "10.0.0.1" })
  await expect(line).toContainText("10.0.0.1, fe80::b482:4dff:fe92:4281")
  // The protocol beside the port is whole and on the screen.
  const protocol = item.getByText("tcp", { exact: true })
  await expect(protocol).toBeVisible()
  const box = await protocol.boundingBox()
  expect(box && box.x + box.width).toBeLessThanOrEqual(390)
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth > document.documentElement.clientWidth,
    ),
  ).toBe(false)

  await page.setViewportSize({ width: 1280, height: 900 })
  const cell = rowsOf(page).filter({ hasText: "46505" }).locator("td").first()
  // One line, both addresses in it and in its tooltip, inside the Endpoint column.
  const addresses = cell.locator("[title]")
  await expect(addresses).toHaveAttribute("title", "10.0.0.1, fe80::b482:4dff:fe92:4281")
  const inside = await addresses.evaluate((node) => {
    const td = node.closest("td")!.getBoundingClientRect()
    const own = node.getBoundingClientRect()
    return own.right <= td.right && own.left >= td.left
  })
  expect(inside).toBe(true)

  // Whole on the port's page, with the user beside them.
  await cell.getByRole("link").click()
  await expect(page).toHaveURL(/\/proxy\/ports\/tcp-46505$/)
  const identity = page.locator("[data-slot='host-identity']")
  await expect(identity.getByText("10.0.0.1, fe80::b482:4dff:fe92:4281")).toBeVisible()
  await expect(identity.getByText("ubuntu", { exact: true })).toBeVisible()
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
  await expect(fact(page, "3 dangerous services exposed")).toBeVisible()
})

test("databases only a bridge can reach are a warning everywhere", async ({ page }) => {
  await mockHost(
    page,
    bridgedDatabases.filter((l) => l.port !== 5432),
  )
  await page.goto("/proxy/ports")
  await expect(fact(page, "2 dangerous services exposed")).toHaveClass(/text-warning/)
})

test("the identity line's facts and the longest reach fit a phone", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 })
  await mockHost(page, [...hostPorts, ...privateUplink])
  await page.goto("/proxy/ports")
  // Every fact is whole and on the screen, with what it is made of as its tooltip.
  for (const [text, title] of [
    ["3 internet-facing", "1 on all · 2 on a public IP"],
    [/^\d+ dangerous services? exposed$/, "Counted once per port"],
  ] as const) {
    const hint = page.locator("[data-slot='host-identity']").getByText(text)
    await expect(hint).toHaveAttribute("title", title)
    expect(await hint.evaluate((el) => el.scrollWidth > el.clientWidth)).toBe(false)
    const box = await hint.boundingBox()
    expect(box && box.x + box.width).toBeLessThanOrEqual(390)
  }
  // The phone's rows draw the longest labels whole and inside the screen.
  const rows = page.getByRole("list")
  for (const label of ["Docker API · Private uplink", "Elasticsearch · VPN only"]) {
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
  let timeout = true
  await page.route("**/api/v1/ports", async (route) => {
    requests++
    if (!timeout) return json(route, hostPorts)
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
  timeout = false
  const beforeRetry = requests
  await page.getByRole("button", { name: "Try again" }).click()
  await expect(page.getByRole("table").locator("tbody tr").first()).toBeVisible()
  expect(requests).toBeGreaterThan(beforeRetry)
})

function rowsOf(page: Page) {
  return page.getByRole("table").locator("tbody tr")
}

/** The ports of the table's socket rows, top to bottom. */
function portOrder(page: Page) {
  return rowsOf(page).locator("td:first-child a").allTextContents()
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
  // The identity line still counts the host.
  await expect(fact(page, "27 listening")).toBeVisible()

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
  let fail = false
  await page.route("**/api/v1/ports", async (route) => {
    requests++
    if (!fail) return json(route, hostPorts)
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

  fail = true
  const beforePoll = requests
  await page.clock.runFor(15_001)
  await expect(
    page.getByText("Refreshing failed, so this is the last list that arrived"),
  ).toBeVisible()
  await expect(
    page.getByText(/Reading \/proc failed\. The page tries again every 15/),
  ).toBeVisible()
  await expect(page.getByText(/^Last updated \d+s ago$/)).toBeVisible()
  await expect(rows).toHaveCount(7)
  expect(requests).toBeGreaterThan(beforePoll)

  fail = false
  await page.getByRole("button", { name: "Try again" }).click()
  await expect(page.getByText(/Refreshing failed/)).toHaveCount(0)
  await expect(page.getByText(/^Updated (just now|\d+s ago)$/)).toBeVisible()
  expect(requests).toBeGreaterThan(beforePoll + 1)
})

test("refreshing can be paused, resumed at once, and asked for", async ({ page }) => {
  await page.clock.install()
  await mockProxy(page, { included: true })
  let requests = 0
  await page.route("**/api/v1/ports", async (route) => {
    requests++
    // Real requests settle after their timer fires; the virtual clock must allow that too.
    await new Promise((resolve) => setTimeout(resolve, 100))
    await json(route, hostPorts)
  })
  await page.goto("/proxy/ports")
  await expect(rowsOf(page)).toHaveCount(7)
  // One read: the page draws the section's shared list rather than asking again.
  const initialRequests = requests
  expect(initialRequests).toBe(1)

  await (await listVerb(page, "Pause refreshing")).click()
  await expect(page.getByText("Paused · updated just now")).toBeVisible()
  // Paused, there is nothing to refresh: the menu offers the way back.
  await expect(await listVerb(page, "Resume refreshing")).toBeVisible()
  await expect(page.getByRole("menuitem", { name: "Refresh now" })).toHaveCount(0)
  await page.keyboard.press("Escape")
  // Polling schedules its next timer after the response settles. A single minute jump
  // can outrun network responses and skip the remaining timers on a busy runner.
  for (let interval = 0; interval < 4; interval++) {
    const response = page.waitForResponse(
      (reply) => new URL(reply.url()).pathname === "/api/v1/ports",
    )
    await page.clock.runFor(15_001)
    await (await response).finished()
    await page.evaluate(() => Promise.resolve())
  }
  await expect(page.getByText("Paused · updated 1m ago")).toBeVisible()
  // The section still reads the list every 15 seconds; the rows on screen hold still.
  await expect(rowsOf(page)).toHaveCount(7)
  expect(requests).toBeGreaterThanOrEqual(initialRequests + 3)
  expect(requests).toBeLessThanOrEqual(initialRequests + 4)
  const pausedRequests = requests

  await (await listVerb(page, "Resume refreshing")).click()
  await expect(page.getByText("Updated just now")).toBeVisible()
  await expect.poll(() => requests).toBe(pausedRequests + 1)
  await (await listVerb(page, "Refresh now")).click()
  await expect.poll(() => requests).toBe(pausedRequests + 2)
  await expect(page.getByText("Updated just now")).toBeVisible()
  await page.clock.runFor(15_001)
  await expect.poll(() => requests).toBe(pausedRequests + 3)
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
  await page.getByRole("button", { name: "More actions for the list" }).click()
  await page.getByRole("menuitem", { name: "Export as CSV" }).click()
  const csvFile = await csv
  expect(csvFile.suggestedFilename()).toMatch(/^listening-sockets-[\d-]+\.csv$/)
  const lines = (await readFile(await csvFile.path(), "utf8")).trimEnd().split("\n")
  // caddy's two families and node_exporter: three sockets on two rows.
  expect(lines).toHaveLength(4)
  expect(lines[0]).toBe(
    "protocol,family,address,port,endpoint,reach,network,interface,process,pid,ppid,user,cmdline,level,owner",
  )
  expect(lines.some((line) => line.includes("[fd7a:115c:a1e0::9e37:2220]:8443"))).toBe(true)

  const json = page.waitForEvent("download")
  await page.getByRole("button", { name: "More actions for the list" }).click()
  await page.getByRole("menuitem", { name: "Export as JSON" }).click()
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

test("the internet-facing fact resets remembered socket filters", async ({ page }) => {
  await mockHost(page)
  await page.goto("/proxy/ports?q=port:6379")
  await expect(rowsOf(page)).toHaveCount(1)
  await page.goto("/proxy")
  const internet = page
    .locator("[data-slot='host-identity']")
    .getByRole("link", { name: "3 ports internet-facing" })
  await expect(internet).toBeVisible()
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
  await page.goto("/network/connections")
  const row = page.locator("[data-slot=choice-row]").filter({ hasText: "203.0.113.50" })
  await expect(row.getByText("51234")).toBeVisible()
  await expect(row.getByRole("link", { name: "What listens on port 51234" })).toHaveCount(0)
  await row.getByRole("link", { name: "What listens on port 22" }).click()
  await expect(page).toHaveURL(/\/proxy\/ports\?q=:22$/)
  await expect(rowsOf(page)).toHaveCount(1)
  await expect(rowsOf(page).first()).toContainText("sshd")
})

test("the listening count opens every socket, not the port a link opened a moment ago", async ({
  page,
}) => {
  await mockHost(page)
  await mockConnections(page)
  await page.goto("/network/connections")
  await page
    .locator("[data-slot=choice-row]")
    .filter({ hasText: "203.0.113.50" })
    .getByRole("link", { name: "What listens on port 22" })
    .click()
  await expect(page).toHaveURL(/\/proxy\/ports\?q=:22$/)
  await expect(rowsOf(page)).toHaveCount(1)

  // The tab now remembers `:22`; the count on the picture's hub asks for the whole list anyway.
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

test("each socket names who it answers for, not only the process holding it", async ({ page }) => {
  await mockHost(page, ownedPorts)
  await page.goto("/proxy/ports")
  const rows = page.getByRole("table").locator("tbody tr")
  // Twelve sockets, nine services: each pair of families is one row.
  await expect(rows).toHaveCount(9)

  const ingress = rows.filter({ hasText: "just-dashboard-ingress" })
  await expect(ingress.getByText("Container · caddy:2-alpine", { exact: true })).toBeVisible()
  await expect(ingress.locator('img[src="/logos/caddy.svg"]')).toHaveCount(1)
  await expect(ingress.getByText("0.0.0.0, ::")).toBeVisible()

  // Docker's NAT alone publishes it: no process, and it still gets past ufw.
  const nat = rows.filter({ hasText: "epgjauto-db-1" })
  await expect(
    nat.getByText("Container · postgres:16-alpine · stack epgjauto", { exact: true }),
  ).toBeVisible()
  await expect(
    nat.locator(titled("Forwarded by Docker's NAT rules; no process listens")),
  ).toHaveCount(1)
  await expect(nat.getByText("PostgreSQL · Every interface")).toHaveClass(/text-destructive/)
  await expect(nat.locator(titled("Published by Docker past the firewall"))).toHaveCount(1)
  await expect(nat.getByText("unknown user")).toHaveCount(0)

  await expect(
    rows.filter({ hasText: "jd-e175-r4" }).getByText("Container · c9051a2ac152 · deployment shop"),
  ).toBeVisible()

  // systemd holding ssh.socket's socket alone is ssh.service waiting, not init.
  const ssh = rows.filter({ hasText: "ssh.socket" })
  await expect(ssh.getByText("ssh.service", { exact: true })).toBeVisible()
  await expect(ssh.getByText("ssh.socket → ssh.service", { exact: true })).toBeVisible()
  await expect(page.getByRole("table").getByText("systemd", { exact: true })).toHaveCount(0)

  await expect(
    rows.filter({ has: page.locator(titled("nginx: master")) }).getByText("nginx.service", {
      exact: true,
    }),
  ).toBeVisible()
  const pm2 = rows.filter({ hasText: "PM2 · node" })
  await expect(pm2.getByText("api", { exact: true })).toBeVisible()
  await expect(pm2.locator('img[src="/logos/nodejs.svg"]')).toHaveCount(1)
  // node calls its main thread MainThread; the program is named instead.
  const byHand = rows.filter({ hasText: "Login session c521" })
  await expect(byHand.getByText("node", { exact: true })).toBeVisible()
  await expect(page.getByRole("table").getByText("MainThread", { exact: true })).toHaveCount(0)

  // The dashboard's backend and the proxy its session arrives through.
  const own = rows.filter({ hasText: "This dashboard" })
  await expect(own).toHaveCount(2)
  await expect(own.filter({ hasText: "jd-server" })).toHaveCount(1)
  await expect(own.filter({ hasText: "just-dashboard-proxy-1" })).toHaveCount(1)
})

test("'Open container' opens the container a port is published for", async ({ page }) => {
  await mockHost(page, ownedPorts)
  await page.goto("/proxy/ports")
  const ingress = page
    .getByRole("table")
    .locator("tbody tr")
    .filter({ hasText: "just-dashboard-ingress" })
  await ingress.getByRole("button", { name: "Open container" }).click()
  await expect(page).toHaveURL(/\/docker\/containers\/just-dashboard-ingress$/)
})

test("a row's owner opens its own page: the deployment, the unit, the PM2 app", async ({
  page,
}) => {
  await mockHost(page, ownedPorts)
  const rows = page.getByRole("table").locator("tbody tr")

  await page.goto("/proxy/ports")
  await page.getByRole("button", { name: "Actions for tcp 127.0.0.1:39069" }).click()
  // docker-proxy is the container's plumbing: in the menu, behind its owner.
  await expect(page.getByRole("menuitem", { name: "Process" })).toBeVisible()
  await page.getByRole("menuitem", { name: "Open deployment" }).click()
  await expect(page).toHaveURL(/\/deploy\/12$/)

  await page.goto("/proxy/ports")
  const ssh = rows.filter({ hasText: "ssh.socket" })
  // Init holding the socket is systemd listening; its process page is init's.
  await expect(ssh.getByRole("button", { name: "Process" })).toHaveCount(0)
  await page.getByRole("button", { name: "Actions for tcp 0.0.0.0:22" }).click()
  await expect(page.getByRole("menuitem", { name: "Copy endpoint" })).toBeVisible()
  await expect(page.getByRole("menuitem", { name: "Process" })).toHaveCount(0)
  await page.keyboard.press("Escape")
  await ssh.getByRole("button", { name: "Open unit" }).click()
  await expect(page).toHaveURL(/\/processes\/services\/ssh\.service$/)

  await page.goto("/proxy/ports")
  await rows.filter({ hasText: "PM2 · node" }).getByRole("button", { name: "Open app" }).click()
  await expect(page).toHaveURL(/\/processes\/pm2\?app=api$/)

  await page.goto("/proxy/ports")
  await rows.filter({ hasText: "nginx.service" }).getByRole("button", { name: "Process" }).click()
  await expect(page).toHaveURL(/\/processes\/812$/)
})

test("a search finds a socket by its container, image, deployment, unit or the dashboard's tag", async ({
  page,
}) => {
  await mockHost(page, ownedPorts)
  await page.goto("/proxy/ports")
  const rows = page.getByRole("table").locator("tbody tr")
  const search = page.getByLabel("Search sockets")
  for (const [query, count] of [
    ["ingress", 1],
    ["postgres:16", 1],
    ["shop", 1],
    ["ssh.socket", 1],
    // jd-server and the proxy are tagged the dashboard's; the ingress is named for it.
    ["dashboard", 3],
    ["node", 2],
  ] as const) {
    await search.fill(query)
    await expect(rows, query).toHaveCount(count)
  }
})

test("owners are whole on a phone, the dashboard's own marked", async ({ page }) => {
  await mockHost(page, ownedPorts)
  await page.setViewportSize({ width: 390, height: 844 })
  await page.goto("/proxy/ports")
  const list = page.getByRole("list", { name: "Listening sockets" })
  await expect(list.locator("li")).toHaveCount(9)
  await expect(list.getByText("This dashboard")).toHaveCount(2)
  // The tag waits a line, so the owner's name is not cut to make room.
  const backend = list.getByText("jd-server", { exact: true })
  expect(await backend.evaluate((e) => e.scrollWidth <= e.clientWidth)).toBe(true)
  await list
    .locator("li")
    .filter({ hasText: "just-dashboard-ingress" })
    .getByRole("button", { name: /^Actions for/ })
    .click()
  await page.getByRole("menuitem", { name: "Open container" }).click()
  await expect(page).toHaveURL(/\/docker\/containers\/just-dashboard-ingress$/)
  await page.goBack()
  await expect(list.locator("li")).toHaveCount(9)
  // How a published port runs, and that no process holds it, are its page's.
  await list.locator("li").filter({ hasText: "epgjauto-db-1" }).getByRole("link").first().click()
  await expect(page).toHaveURL(/\/proxy\/ports\/tcp-5432/)
  await expect(page.getByText("Container · postgres:16-alpine · stack epgjauto")).toBeVisible()
  await page.getByRole("tab", { name: "Owner" }).click()
  await expect(page.getByText("Forwarded by Docker's NAT rules; no process listens.")).toBeVisible()
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth > document.documentElement.clientWidth,
    ),
  ).toBe(false)
})

/** Clients on sshd: the optional data a row may or may not have. */
const withClients = hostPorts.map((socket) =>
  socket.process === "sshd" && socket.family === "ipv4"
    ? { ...socket, clients: { count: 3, peers: [{ address: "198.51.100.7", count: 3 }] } }
    : socket,
)

for (const width of [1280, 390]) {
  test(`at ${width} every row is one height, and a row gaining clients moves nothing under it`, async ({
    page,
  }) => {
    await page.setViewportSize({ width, height: 1000 })
    let listeners: unknown[] = [...hostPorts, ...ownedPorts]
    await mockHost(page)
    await page.route("**/api/v1/ports", (route) => json(route, listeners))
    await page.goto("/proxy/ports")
    const rows = page.locator("[data-slot='port-row']")
    await expect(rows).toHaveCount(16)
    await risen(page)
    const heights = await rows.evaluateAll((all) =>
      all.map((row) => Math.round(row.getBoundingClientRect().height)),
    )
    // Tags, a NAT owner, a long command, both families, an empty reach: one height.
    expect(new Set(heights).size).toBe(1)

    const second = rows.nth(1)
    const before = await second.boundingBox()
    listeners = [...withClients, ...ownedPorts]
    await (await listVerb(page, "Refresh now")).click()
    if (width === 1280) {
      await expect(rowsOf(page).filter({ hasText: "sshd" }).locator("td").nth(4)).toHaveText("3")
    }
    await risen(page)
    expect(await second.boundingBox()).toEqual(before)
  })
}

test("the identity line is where it will stay while the list is read", async ({ page }) => {
  await page.setViewportSize({ width: 1280, height: 900 })
  await mockHost(page)
  let release: () => void = () => {}
  const held = new Promise<void>((resolve) => (release = resolve))
  await page.route("**/api/v1/ports", async (route) => {
    await held
    await json(route, hostPorts)
  })
  await page.goto("/proxy/ports")
  const identity = page.locator("[data-slot='host-identity']")
  await expect(identity.getByText("Reading the sockets…")).toBeVisible()
  await risen(page)
  const loading = await identity.boundingBox()
  release()
  await expect(fact(page, "7 listening")).toBeVisible()
  await risen(page)
  const loaded = await identity.boundingBox()
  expect(loaded?.y).toBe(loading?.y)
  expect(loaded?.x).toBe(loading?.x)
  expect(loaded?.height).toBe(loading?.height)
})

test("the picture wires each network to what listens there, a database the internet reaches in red", async ({
  page,
}) => {
  await mockHost(page)
  await page.goto("/proxy/ports")
  const picture = page.locator("[data-slot='ports-picture']")
  const networks = picture.getByRole("list", { name: "Who can reach it" })
  for (const name of ["The internet", "The tailnet", "Docker's networks", "Only this server"]) {
    await expect(networks.getByRole("button", { name })).toBeVisible()
  }
  const listens = picture.getByRole("list", { name: "What listens" })
  await expect(listens.getByText("redis-server", { exact: true })).toHaveClass(/text-destructive/)
  // The wire to it is red; nobody is connected anywhere, so no wire moves.
  await expect(picture.locator("path.stroke-destructive").first()).toBeAttached()

  // A network narrows the list as its chip does; pressed again, it lets go.
  await networks.getByRole("button", { name: "The tailnet" }).click()
  await expect(rowsOf(page)).toHaveCount(2)
  await expect(page).toHaveURL(/reach=private/)
  await networks.getByRole("button", { name: "The tailnet" }).click()
  await expect(rowsOf(page)).toHaveCount(7)

  // A program's port opens its page.
  await listens.getByRole("link", { name: ":6379" }).click()
  await expect(page).toHaveURL(/\/proxy\/ports\/tcp-6379$/)
})

test("a port pressed straight after a filter still opens its page", async ({ page }) => {
  await mockHost(page)
  // The port's page as a busy server gives it: nothing fetched ahead, and the
  // navigation most of a second on its way.
  await page.route(/\/proxy\/ports\/tcp-6379/, async (route) => {
    if (route.request().headers()["next-router-prefetch"]) return route.abort()
    await new Promise((resolve) => setTimeout(resolve, 700))
    await route.continue()
  })
  await page.goto("/proxy/ports")
  const picture = page.locator("[data-slot='ports-picture']")
  await picture.getByRole("button", { name: "The tailnet" }).click()
  await expect(rowsOf(page)).toHaveCount(2)
  await expect(page).toHaveURL(/reach=private/)

  // The filter let go and the port pressed inside the quarter-second the page
  // waits before it writes its view to the address. That write, landing while
  // the navigation was on its way, used to cancel it.
  await expect(picture.getByRole("link", { name: ":6379" })).toBeVisible()
  await page.evaluate(async () => {
    const picture = document.querySelector("[data-slot='ports-picture']")!
    const press = (selector: string, text: string) =>
      [...picture.querySelectorAll<HTMLElement>(selector)]
        .find((el) => el.textContent?.includes(text))!
        .click()
    press("button", "The tailnet")
    await new Promise((resolve) => setTimeout(resolve, 60))
    press("a", ":6379")
  })
  await expect(page).toHaveURL(/\/proxy\/ports\/tcp-6379$/)
  // The view was written before the page was left, so Back finds it let go.
  await page.goBack()
  await expect(page).toHaveURL(/\/proxy\/ports$/)
  await expect(rowsOf(page)).toHaveCount(7)
})

test("the old sheet addresses, the page's and the host diagnostic's, open the port's page", async ({
  page,
}) => {
  await mockHost(page)
  await page.goto("/proxy/ports?q=%3A22&socket=tcp-ipv4-0.0.0.0-22")
  await expect(page).toHaveURL(/\/proxy\/ports\/tcp-22-0\.0\.0\.0$/)
  await expect(page.locator("[data-slot='host-identity']").getByText("sshd")).toBeVisible()

  await page.goto("/proxy/ports?socket=udp:127.0.0.53:53")
  await expect(page).toHaveURL(/\/proxy\/ports\/udp-53-127\.0\.0\.53$/)
  await expect(
    page.locator("[data-slot='host-identity']").getByText("systemd-resolve").first(),
  ).toBeVisible()

  // A port nothing listens on says so, and keeps its history.
  await page.goto("/proxy/ports/tcp-9999")
  await expect(page.getByText("Nothing listens there now")).toBeVisible()
  await expect(page.locator("section#changes")).toBeVisible()
})

test("a port's page: its views in the address, the list beside it, and the next port a key away", async ({
  page,
}) => {
  await page.setViewportSize({ width: 1440, height: 900 })
  await mockHost(page, withClients)
  await page.goto("/proxy/ports")
  await rowsOf(page).filter({ hasText: "sshd" }).getByRole("link").click()
  await expect(page).toHaveURL(/\/proxy\/ports\/tcp-22$/)

  const identity = page.locator("[data-slot='host-identity']")
  await expect(identity.getByText("0.0.0.0, ::")).toBeVisible()
  await expect(identity.getByText("Every interface")).toHaveClass(/text-warning/)
  await expect(page.getByRole("list", { name: "The way to this port" })).toContainText(
    "The internet",
  )

  await page.getByRole("tab", { name: /^Clients/ }).click()
  await expect(page).toHaveURL(/\/proxy\/ports\/tcp-22\?tab=clients$/)
  await expect(page.getByRole("list", { name: "Top peers" })).toContainText("198.51.100.7")
  await expect(page.getByRole("link", { name: "Open in Connections" })).toHaveAttribute(
    "href",
    "/network/connections?q=22",
  )

  // The rail is the list, in the list's order, the port on screen filled.
  const rail = page.getByRole("navigation", { name: "Ports" })
  await expect(rail.getByRole("button", { name: "22/tcp, sshd" })).toHaveAttribute(
    "aria-current",
    "page",
  )
  await expect(rail.getByRole("listitem")).toHaveCount(7)

  // Alt+↓ is the next row, on the same view.
  await page.keyboard.press("Alt+ArrowDown")
  await expect(page).toHaveURL(/\/proxy\/ports\/udp-68\?tab=clients$/)
  await expect(page.getByRole("tab", { name: /^Clients/ })).toHaveAttribute("aria-selected", "true")
  await page.getByRole("button", { name: "Previous port" }).click()
  await expect(page).toHaveURL(/\/proxy\/ports\/tcp-22\?tab=clients$/)

  await page.getByRole("tab", { name: "Owner" }).click()
  await expect(
    page.getByText("sshd: /usr/sbin/sshd -D [listener] 0 of 10-100 startups"),
  ).toBeVisible()
  await page.getByRole("main").getByRole("link", { name: "Ports", exact: true }).click()
  await expect(page).toHaveURL(/\/proxy\/ports$/)
})

test("every verb a row had is on the port's page too", async ({ page, context }) => {
  await context.grantPermissions(["clipboard-read", "clipboard-write"])
  await mockHost(page, ownedPorts)
  await page.goto("/proxy/ports/tcp-80")
  await expect(page.getByRole("button", { name: "Open container" })).toBeVisible()
  await page.getByRole("button", { name: "Actions for tcp 0.0.0.0:80" }).click()
  for (const name of ["Open deployment", "Copy endpoint", "Firewall", "Restart container"]) {
    const item = page.getByRole("menuitem", { name })
    if (name === "Open deployment" && (await item.count()) === 0) continue
    await expect(item).toBeVisible()
  }
  await page.getByRole("menuitem", { name: "Copy endpoint" }).click()
  expect(await page.evaluate(() => navigator.clipboard.readText())).toBe("0.0.0.0:80")

  await page.getByRole("button", { name: "Actions for tcp 0.0.0.0:80" }).click()
  await page.getByRole("menuitem", { name: "Restart container" }).click()
  await expect(page.getByText(/Docker stops just-dashboard-ingress/)).toBeVisible()
})

test("an account that only reads is offered nothing it cannot do", async ({ page }) => {
  await mockHost(page, [...hostPorts, ...ownedPorts])
  await page.route("**/api/v1/auth/session", (route) =>
    json(route, { ...user, capabilities: ["read"] }),
  )
  await page.goto("/proxy/ports")
  await expect(fact(page, "16 listening")).toBeVisible()
  await expect(page.getByRole("button", { name: "Find a free port" })).toHaveCount(0)
  await page.getByRole("button", { name: "Actions for tcp 0.0.0.0:80" }).click()
  await expect(page.getByRole("menuitem", { name: "Copy endpoint" })).toBeVisible()
  for (const name of [
    /^Restart/,
    /^Stop/,
    /^Kill/,
    /^Terminate/,
    /^Allow only/,
    /^Deny/,
    /^Publish/,
    /^Forward/,
  ]) {
    await expect(page.getByRole("menuitem", { name })).toHaveCount(0)
  }
  await page.keyboard.press("Escape")

  await page.goto("/proxy/ports/tcp-6379")
  await expect(page.locator("[data-slot='host-identity']").getByText("redis-server")).toBeVisible()
  await expect(page.getByText("From outside", { exact: true })).toHaveCount(0)
  await expect(page.getByRole("button", { name: "Identify", exact: true })).toHaveCount(0)
  await page.getByRole("button", { name: "Actions for tcp 203.0.113.5:6379" }).click()
  await expect(page.getByRole("menuitem", { name: /^Kill|^Terminate/ })).toHaveCount(0)
})
