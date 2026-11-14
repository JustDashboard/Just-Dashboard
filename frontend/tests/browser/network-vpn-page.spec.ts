import { expect, test, type Page } from "@playwright/test"
import { admin, json, mockNetwork, vpn, type Mutation } from "./network-fixture"
import { history, now, openMaturity } from "./network-vpn-maturity-fixture"
import { risen } from "./risen"

// The VPN page as the deployment pages read: each VPN on its own identity
// line, the tunnel's picture under it, machines as cards of one height, the
// tailnet's offers on one save bar, and a tunnel's record on a page of its own.

/** An administrator whose session may not run destructive routes. */
const careful = {
  ...admin,
  capabilities: admin.capabilities.filter((capability) => capability !== "destructive"),
}

const archived = [
  {
    file: "wg1.conf.1790000000",
    name: "wg1",
    archivedAt: now() - 86400,
    listenPort: 51821,
    addresses: ["10.40.0.1/24"],
    endpoint: "203.0.113.20:51821",
    peers: 2,
    restorable: true,
  },
]

const identity = (page: Page, name: string) => page.getByRole("region", { name: `About ${name}` })

/** Where a list's cards stand under its first one, and their heights, at rest. */
async function boxes(page: Page, list: string) {
  await risen(page)
  return page
    .getByRole("list", { name: list, exact: true })
    .locator("[data-slot=choice-row]")
    .evaluateAll((rows) => {
      const first = rows[0]?.getBoundingClientRect().top ?? 0
      return rows.map((row) => {
        const box = row.getBoundingClientRect()
        return { top: Math.round(box.top - first), height: Math.round(box.height) }
      })
    })
}

for (const width of [1280, 390]) {
  test.describe(`at ${width}`, () => {
    test.use({ viewport: { width, height: 900 } })

    test("the page opens on a tunnel's line, with no row of figures over it", async ({ page }) => {
      await openMaturity(page, [])
      await page.goto("/network/vpn")
      const line = identity(page, "wg0")
      await expect(line).toContainText("2 of 5 online")
      await expect(line).toContainText("3 need a look")
      await expect(line).toContainText("10.8.0.0/24")
      await expect(line).toContainText("UDP 51820")
      await expect(line).toContainText("moved 31.6 GB")
      await expect(line.getByRole("button", { name: "Add a device", exact: true })).toBeVisible()
      await expect(line.getByRole("button", { name: "wg0 actions" })).toBeVisible()
      await expect(page.locator("[data-slot=stat-grid]")).toHaveCount(0)
      // Nothing stands between the top of the page and the line.
      await risen(page)
      const frame = await page.locator("[data-slot=page]").boundingBox()
      const box = await line.boundingBox()
      expect(box!.y - frame!.y).toBeLessThanOrEqual(40)

      const tailnet = page.getByRole("region", { name: "About this server on the tailnet" })
      await expect(tailnet).toContainText("vps-edge-01.tail1234.ts.net")
      await expect(tailnet).toContainText("4 of 6 online")
      await expect(tailnet).toContainText("ana@example.com")
      expect(
        await page.evaluate(
          () => document.documentElement.scrollWidth - document.documentElement.clientWidth,
        ),
      ).toBeLessThanOrEqual(0)
    })

    test("the tunnel's line stands in the same place while the page is read", async ({ page }) => {
      await mockNetwork(page)
      let answer: () => void = () => undefined
      const held = new Promise<void>((resolve) => (answer = resolve))
      await page.route("**/api/v1/network/vpn", async (route) => {
        await held
        await json(route, vpn)
      })
      await page.goto("/network/vpn")
      const line = page.locator("[data-layout=project]").first()
      await expect(line).toBeVisible()
      await expect(page.locator("[data-slot=wire-flow][data-loading]")).toBeVisible()
      await risen(page)
      const before = await line.boundingBox()
      const picture = await page.locator("[data-slot=wire-flow]").first().boundingBox()

      answer()
      await expect(identity(page, "wg0")).toContainText("online")
      await risen(page)
      const after = await identity(page, "wg0").boundingBox()
      expect(after!.y).toBe(before!.y)
      expect(after!.x).toBe(before!.x)
      const drawn = await page.locator("[data-slot=wire-flow]").first().boundingBox()
      expect(drawn!.y).toBe(picture!.y)
    })

    test("machines are cards of one height, with and without a reading", async ({ page }) => {
      await openMaturity(page, [])
      await page.goto("/network/vpn")
      await expect(page.getByRole("list", { name: "Tailnet machines" })).toContainText("old-laptop")
      const rows = await boxes(page, "Tailnet machines")
      expect(rows).toHaveLength(6)
      // ana-macbook moved traffic over a direct path; old-laptop has neither.
      expect(new Set(rows.map((row) => row.height)).size).toBe(1)
      const steps = rows.slice(1).map((row, i) => row.top - rows[i].top)
      expect(new Set(steps).size).toBe(1)
      await expect(page.locator("[data-slot=table-container]")).toHaveCount(0)

      // Narrowing the list leaves the rows that stay where the first ones were.
      await page.getByRole("button", { name: /^Offline/ }).click()
      const offline = await boxes(page, "Tailnet machines")
      expect(offline).toHaveLength(2)
      expect(offline[0]).toEqual(rows[0])
      expect(offline[1]).toEqual(rows[1])
    })
  })
}

test("what needs a look is a mark on the peer it is about, and a sentence in its sheet", async ({
  page,
}) => {
  await openMaturity(page, [])
  await page.goto("/network/vpn")
  await expect(page.getByRole("note", { name: "Office findings" })).toHaveText(
    "handshake stale · past its budget",
  )
  await expect(page.getByRole("note", { name: "Backup server findings" })).toHaveText(
    "transport inside a tunnel",
  )
  await expect(page.locator("[data-slot=peer-finding-mark]")).toHaveCount(2)
  await expect(page.getByText(/things? on wg0 needs? a look/)).toHaveCount(0)
  await page.getByRole("button", { name: "Open Backup server" }).click()
  await expect(page.getByRole("dialog").getByLabel("Backup server findings")).toContainText(
    "goes into the WireGuard device wg0",
  )
})

test("a long column draws six peers and sends the rest to the tunnel's page, which draws them all", async ({
  page,
}) => {
  const base = vpn.wireguard.interfaces[0]
  const phones = Array.from({ length: 9 }, (_, i) => ({
    ...base.peers[0],
    id: 20 + i,
    name: `Phone ${i + 1}`,
    publicKey: `PhoneKey${i}AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=`,
    address: `10.8.0.${50 + i}/32`,
  }))
  const crowded = { ...base, peers: [...phones, ...base.peers.filter((p) => p.kind === "site")] }
  await mockNetwork(page, [], {
    overrides: {
      "/network/vpn": { ...vpn, wireguard: { ...vpn.wireguard, interfaces: [crowded] } },
    },
  })
  await page.goto("/network/vpn")
  const devices = page.getByRole("region", { name: "Devices" })
  await expect(devices.getByRole("button", { name: /^Open Phone/ })).toHaveCount(6)
  await expect(devices).toContainText("3 more")
  await devices.getByRole("link", { name: "3 more" }).click()
  await expect(page).toHaveURL(/\/network\/vpn\/wg0$/)
  await expect(
    page.getByRole("region", { name: "Devices" }).getByRole("button", { name: /^Open Phone/ }),
  ).toHaveCount(9)
  await expect(page.getByRole("button", { name: "Add a device" })).toHaveCount(2)
})

test("a tunnel's menu takes it down, brings it up and removes it, each after its confirmation", async ({
  page,
}) => {
  const mutations: Mutation[] = []
  await mockNetwork(page, mutations)
  await page.goto("/network/vpn")
  const menu = page.getByRole("button", { name: "wg0 actions" })
  await menu.click()
  await expect(page.getByRole("menuitem")).toHaveText([
    "Join a site",
    "History",
    "Take down",
    "Create another tunnel",
    "Remove",
  ])
  await page.getByRole("menuitem", { name: "Take down" }).click()
  expect(mutations).toEqual([])
  await page.getByRole("button", { name: "Take down", exact: true }).click()
  await expect.poll(() => mutations.at(-1)?.path).toBe("/network/vpn/wireguard/wg0/down")

  await menu.click()
  await page.getByRole("menuitem", { name: "Remove" }).click()
  await expect(page.getByRole("dialog")).toContainText("moved aside rather than deleted")
  await page.getByRole("button", { name: "Remove", exact: true }).click()
  await expect
    .poll(() => mutations.at(-1))
    .toMatchObject({ method: "DELETE", path: "/network/vpn/wireguard/wg0" })

  await menu.click()
  await page.getByRole("menuitem", { name: "Join a site" }).click()
  await expect(page.getByRole("dialog", { name: "Join a site" })).toBeVisible()
  await page.keyboard.press("Escape")

  await menu.click()
  await page.getByRole("menuitem", { name: "Create another tunnel" }).click()
  await expect(page.getByRole("dialog", { name: "Create WireGuard tunnel" })).toBeVisible()
})

test("a tunnel that is down offers to bring it up", async ({ page }) => {
  const mutations: Mutation[] = []
  const down = { ...vpn.wireguard.interfaces[0], up: false }
  await mockNetwork(page, mutations, {
    overrides: { "/network/vpn": { ...vpn, wireguard: { ...vpn.wireguard, interfaces: [down] } } },
  })
  await page.goto("/network/vpn")
  await expect(identity(page, "wg0")).toContainText("Down")
  await page.getByRole("button", { name: "wg0 actions" }).click()
  await expect(page.getByRole("menuitem", { name: "Take down" })).toHaveCount(0)
  await page.getByRole("menuitem", { name: "Bring up" }).click()
  await expect.poll(() => mutations.at(-1)?.path).toBe("/network/vpn/wireguard/wg0/up")
})

test("a tunnel written by hand says so and offers nothing that would change it", async ({
  page,
}) => {
  const foreign = { ...vpn.wireguard.interfaces[0], managed: false }
  await mockNetwork(page, [], {
    overrides: {
      "/network/vpn": { ...vpn, wireguard: { ...vpn.wireguard, interfaces: [foreign] } },
    },
  })
  await page.goto("/network/vpn")
  const line = identity(page, "wg0")
  await expect(line).toContainText("written by hand")
  await expect(line.getByRole("button", { name: "Add a device" })).toHaveCount(0)
  await expect(page.getByRole("button", { name: "Add a device" })).toHaveCount(0)
  await expect(page.getByRole("switch", { name: "wg0 as an exit node" })).toHaveCount(0)
  await page.getByRole("button", { name: "wg0 actions" }).click()
  await expect(page.getByRole("menuitem")).toHaveText(["History", "Create another tunnel"])
})

test("an administrator without the destructive capability sees no control it would be refused", async ({
  page,
}) => {
  await openMaturity(page, [])
  await mockSession(page, careful)
  await page.goto("/network/vpn")
  await page.getByRole("button", { name: "wg0 actions" }).click()
  await expect(page.getByRole("menuitem")).toHaveText([
    "Join a site",
    "History",
    "Create another tunnel",
  ])
  await page.keyboard.press("Escape")
  // Offers can be added; taking one back is the destructive half.
  await expect(page.getByRole("button", { name: /^Stop offering/ })).toHaveCount(0)
  await expect(page.getByRole("radio", { name: "Off", exact: true })).toBeDisabled()
  await expect(page.getByRole("textbox", { name: "A network to offer" })).toBeEnabled()

  await page.getByRole("button", { name: "Open Office" }).click()
  const sheet = page.getByRole("dialog")
  await expect(sheet.getByRole("button", { name: "Edit", exact: true })).toBeVisible()
  await expect(sheet.getByRole("button", { name: "Show QR code" })).toBeVisible()
  await expect(sheet.getByRole("button", { name: "Remove", exact: true })).toHaveCount(0)
  await expect(sheet.getByRole("button", { name: "Forget the saved copy" })).toHaveCount(0)
  await expect(sheet.getByLabel("Office budget", { exact: true })).toContainText("3.0 GB of 2.0 GB")
  await expect(sheet.getByRole("button", { name: "Clear the budget" })).toHaveCount(0)
  await expect(sheet.getByRole("button", { name: "Set budget" })).toBeVisible()
})

/** The same page for another account: the session route answers before the fixture's. */
async function mockSession(page: Page, session: typeof admin) {
  await page.route("**/api/v1/auth/session", (route) => json(route, session))
}

test("the tailnet's offers are edited, counted on the save bar and written in one request", async ({
  page,
}) => {
  const mutations: Mutation[] = []
  await openMaturity(page, mutations)
  await page.goto("/network/vpn")
  const form = page.getByRole("form", { name: "Tailscale offers" })
  const bar = page.getByRole("region", { name: "Save changes" })
  await expect(bar).toHaveCount(0)
  await expect(form.getByRole("radio", { name: "Offered" })).toBeChecked()

  await form.getByRole("radio", { name: "Off", exact: true }).click()
  await expect(bar).toContainText("1 unsaved change")
  await form.getByRole("button", { name: "Stop offering 10.0.4.0/24" }).click()
  await expect(form.getByRole("list", { name: "Offered subnet routes" })).toContainText(
    "withdrawn on save",
  )
  await form.getByRole("textbox", { name: "A network to offer" }).fill("10.99.0.0/24")
  await expect(bar).toContainText("3 unsaved changes")
  await expect(bar).toContainText("Applies immediately")
  expect(mutations.filter((m) => m.method !== "GET")).toEqual([])

  // A withdrawal marked by mistake is taken back before anything is sent.
  await form.getByRole("button", { name: "Keep offering 10.0.4.0/24" }).click()
  await expect(bar).toContainText("2 unsaved changes")
  await form.getByRole("button", { name: "Stop offering 192.168.60.0/24" }).click()

  await bar.getByRole("button", { name: /^Save/ }).click()
  await expect
    .poll(() => mutations.find((m) => m.path === "/network/vpn/tailscale")?.body)
    .toEqual({ advertiseExitNode: false, advertiseRoutes: ["10.0.4.0/24", "10.99.0.0/24"] })
  expect(mutations.filter((m) => m.path === "/network/vpn/tailscale")).toHaveLength(1)
  await expect(form.getByRole("textbox", { name: "A network to offer" })).toHaveValue("")
  // What was saved stays on screen until the page reads it back.
  await expect(form.getByRole("radio", { name: "Off", exact: true })).toBeChecked()
  await expect(form.getByRole("list", { name: "Offered subnet routes" })).toContainText(
    "10.99.0.0/24",
  )
  await expect(bar).toHaveCount(0, { timeout: 10_000 })
})

test("a route that is no network holds the save, and Discard puts the offers back", async ({
  page,
}) => {
  const mutations: Mutation[] = []
  await openMaturity(page, mutations)
  await page.goto("/network/vpn")
  const form = page.getByRole("form", { name: "Tailscale offers" })
  const bar = page.getByRole("region", { name: "Save changes" })
  const draft = form.getByRole("textbox", { name: "A network to offer" })
  await draft.fill("office")
  await expect(form.getByText("Use a network such as 10.0.4.0/24.")).toBeVisible()
  await expect(form.getByRole("button", { name: "Offer it" })).toBeDisabled()
  await expect(bar.getByRole("button", { name: /^Save/ })).toBeDisabled()
  await draft.fill("0.0.0.0/0")
  await expect(form.getByText("Every address is the exit node, not a subnet route.")).toBeVisible()
  await form.getByRole("radio", { name: "Off", exact: true }).click()
  await bar.getByRole("button", { name: "Discard" }).click()
  await expect(draft).toHaveValue("")
  await expect(form.getByRole("radio", { name: "Offered" })).toBeChecked()
  await expect(bar).toHaveCount(0)
  expect(mutations.filter((m) => m.method !== "GET")).toEqual([])
})

test("a Tailscale waiting to be signed in says so and offers the address it waits at", async ({
  page,
}) => {
  await mockNetwork(page, [], {
    overrides: {
      "/network/vpn": {
        ...vpn,
        tailscale: {
          ...vpn.tailscale,
          running: false,
          backendState: "NeedsLogin",
          authUrl: "https://login.tailscale.example/a/abc123",
          self: null,
          peers: [],
          tailnet: "",
        },
      },
    },
  })
  await page.goto("/network/vpn")
  const line = page.getByRole("region", { name: "About this server on the tailnet" })
  await expect(line).toContainText("Needs sign-in")
  await expect(line).not.toContainText("NeedsLogin")
  await expect(line.getByRole("link", { name: "Sign in" })).toHaveAttribute(
    "href",
    "https://login.tailscale.example/a/abc123",
  )
  // A daemon that is not running knows no machine: no empty list claims the tailnet has none.
  await expect(page.getByText("No other machine on this tailnet")).toHaveCount(0)
  await expect(page.getByRole("radio", { name: "Offered" })).toBeDisabled()
})

test("a tailnet with nobody else on it says so in one designed block", async ({ page }) => {
  await mockNetwork(page, [], {
    overrides: { "/network/vpn": { ...vpn, tailscale: { ...vpn.tailscale, peers: [] } } },
  })
  await page.goto("/network/vpn")
  const empty = page
    .locator("[data-slot=empty-state]")
    .filter({ hasText: "No other machine on this tailnet" })
  await expect(empty).toBeVisible()
  await expect(empty.getByRole("link", { name: "tailscale.com/download" })).toBeVisible()
})

test("a read that fails says the page was not read and offers it again", async ({ page }) => {
  await mockNetwork(page)
  let fail = true
  await page.route("**/api/v1/network/vpn", (route) =>
    fail
      ? json(route, { error: { code: "unavailable", message: "VPN state unavailable" } }, 503)
      : json(route, vpn),
  )
  await page.goto("/network/vpn")
  await expect(page.getByText("Not read", { exact: true })).toBeVisible()
  await expect(page.getByText("VPN state unavailable")).toBeVisible()
  fail = false
  await page.getByRole("button", { name: "Try again" }).click()
  await expect(identity(page, "wg0")).toContainText("online")
})

test.describe("a tunnel's own page", () => {
  test("the name and the menu's History lead to it, and the address carries the view", async ({
    page,
  }) => {
    await openMaturity(page, [], { "/network/vpn/archive": archived })
    await page.goto("/network/vpn")
    await page.getByRole("button", { name: "wg0 actions" }).click()
    await page.getByRole("menuitem", { name: "History" }).click()
    await expect(page).toHaveURL(/\/network\/vpn\/wg0\?tab=history$/)
    await expect(page.getByRole("tab", { name: /^History/ })).toHaveAttribute(
      "aria-selected",
      "true",
    )
    const lifecycle = page.getByLabel("wg0 lifecycle")
    await expect(lifecycle).toContainText("Created")
    await expect(lifecycle).toContainText("rolled back")

    await page.locator("[data-slot=page-context]").getByRole("link", { name: "VPN" }).click()
    await expect(page).toHaveURL(/\/network\/vpn$/)
    await identity(page, "wg0").getByRole("link", { name: "wg0" }).click()
    await expect(page).toHaveURL(/\/network\/vpn\/wg0$/)
    await expect(page.getByRole("tab", { name: "Overview" })).toHaveAttribute(
      "aria-selected",
      "true",
    )
    // The picture draws every peer, the exit and the endpoint under the line.
    await expect(page.getByRole("button", { name: "Open Office" })).toBeVisible()
    await expect(page.getByRole("switch", { name: "wg0 as an exit node" })).toBeChecked()
    await expect(page.getByRole("button", { name: "Check endpoint" })).toBeVisible()

    await page.getByRole("tab", { name: "Traffic" }).click()
    await expect(page).toHaveURL(/\/network\/vpn\/wg0\?tab=traffic$/)
    await expect(page.getByText("wg0 · every peer · 24h")).toBeVisible()
    await expect(page.locator("[data-slot=rank-row]").first()).toContainText("Ana's phone")
    await page.getByRole("button", { name: "Open Ana's phone" }).click()
    await expect(page.getByRole("dialog")).toContainText("10.8.0.2/32")
  })

  test("the rail lists the other tunnels, and Alt+↓ walks it with the view carried", async ({
    page,
  }) => {
    await openMaturity(page, [], {
      "/network/vpn/archive": archived,
      "/network/vpn/wireguard/wg1/history": { ...history(), iface: "wg1" },
    })
    await page.goto("/network/vpn/wg0?tab=history")
    const rail = page.getByRole("navigation", { name: "Tunnels" })
    await expect(rail.getByRole("button", { name: "wg0" })).toHaveAttribute("aria-current", "page")
    await expect(rail.getByRole("button", { name: "wg1" })).toContainText("removed")
    await page.keyboard.press("Alt+ArrowDown")
    await expect(page).toHaveURL(/\/network\/vpn\/wg1\?tab=history$/)
    await expect(rail.getByRole("button", { name: "wg1" })).toHaveAttribute("aria-current", "page")
    await expect(page.getByRole("button", { name: "Next tunnel" })).toBeDisabled()
    await page.getByRole("button", { name: "Previous tunnel" }).click()
    await expect(page).toHaveURL(/\/network\/vpn\/wg0\?tab=history$/)
  })

  test("a removed tunnel keeps its record and is restored from its page", async ({ page }) => {
    const mutations: Mutation[] = []
    await openMaturity(page, mutations, {
      "/network/vpn/archive": archived,
      "/network/vpn/wireguard/wg1/history": { ...history(), iface: "wg1" },
    })
    await page.route("**/api/v1/network/vpn/archive/*/restore", (route) => {
      mutations.push({ method: "POST", path: new URL(route.request().url()).pathname, body: null })
      return json(route, {
        interface: { ...vpn.wireguard.interfaces[0], name: "wg1" },
        warnings: [],
        firewall: { opened: true },
      })
    })
    await page.goto("/network/vpn")
    await page.getByRole("link", { name: "Open wg1's history" }).click()
    await expect(page).toHaveURL(/\/network\/vpn\/wg1\?tab=history$/)
    const line = identity(page, "wg1")
    await expect(line).toContainText("Removed")
    await expect(line).toContainText("udp 51821 · 10.40.0.1/24 · 2 peers")
    await expect(page.getByLabel("wg1 lifecycle")).toContainText("Created")
    await expect(page.getByRole("tab", { name: "Overview" })).toHaveCount(0)
    await line.getByRole("button", { name: "Restore wg1" }).click()
    await page.getByRole("button", { name: "Restore", exact: true }).click()
    await expect
      .poll(() => mutations.find((m) => m.path.endsWith("/restore"))?.path)
      .toBe("/api/v1/network/vpn/archive/wg1.conf.1790000000/restore")
  })

  test("a name no tunnel answers to says so, with the way back", async ({ page }) => {
    await openMaturity(page, [], {
      "/network/vpn/wireguard/wg9/history": { ...history(), iface: "wg9", points: [], events: [] },
    })
    await page.goto("/network/vpn/wg9")
    await expect(page.getByText("No tunnel named wg9 on this server")).toBeVisible()
    await page.getByRole("link", { name: "Back to the VPN" }).click()
    await expect(page).toHaveURL(/\/network\/vpn$/)
  })

  test("it is an administrator's, and reads nothing for anybody else", async ({ page }) => {
    let reads = 0
    await mockNetwork(page, [], {
      session: { ...admin, capabilities: ["read"] } as typeof admin,
    })
    await page.route("**/api/v1/network/vpn**", async (route) => {
      reads++
      await json(route, {})
    })
    await page.goto("/network/vpn/wg0")
    await expect(page.getByText("VPN needs the admin capability")).toBeVisible()
    expect(reads).toBe(0)
  })

  test.describe("on a phone", () => {
    test.use({ viewport: { width: 390, height: 844 } })
    test("it is one pane that scrolls as a whole, with nothing cut off sideways", async ({
      page,
    }) => {
      await openMaturity(page, [], { "/network/vpn/archive": archived })
      await page.goto("/network/vpn/wg0")
      await expect(identity(page, "wg0")).toContainText("2 of 5 online")
      await expect(page.getByRole("navigation", { name: "Tunnels" })).toBeHidden()
      await expect(page.getByRole("button", { name: "Next tunnel" })).toBeVisible()
      await expect(page.getByRole("note", { name: "Office findings" })).toBeVisible()
      expect(
        await page.evaluate(
          () => document.documentElement.scrollWidth - document.documentElement.clientWidth,
        ),
      ).toBeLessThanOrEqual(0)
      // No view is its own scroller here: the page is.
      const scrollers = await page
        .getByRole("tabpanel")
        .evaluateAll((panels) => panels.filter((p) => p.scrollHeight > p.clientHeight + 1).length)
      expect(scrollers).toBe(0)
    })
  })
})
