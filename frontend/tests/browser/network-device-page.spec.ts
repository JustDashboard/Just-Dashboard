import { expect, test, type Locator, type Page } from "@playwright/test"
import {
  admin,
  iso,
  json,
  links,
  loaded,
  mockNetwork,
  namespaces,
  type Mutation,
} from "./network-fixture"
import { risen } from "./risen"

/**
 * The Interfaces list and a device's own page, against the mocked API: what
 * the overhaul promises of them. The list opens on one line and its cards
 * are one height; a device is a place with an address, its views in that
 * address and the list as a rail beside it; the address that used to open
 * the device's sheet lands on its page; every verb the sheet had still
 * writes what its route takes; and an account that may not change anything
 * is shown nothing it cannot use.
 */

const reader = { ...admin, capabilities: ["read"], user: { ...admin.user, role: "readonly" } }
const device = (page: Page) => page.locator("[data-slot=page]")
const identity = (page: Page, name: string) => page.getByRole("region", { name: `About ${name}` })
const saveBar = (page: Page) => page.getByRole("region", { name: "Save changes" })
const menu = (page: Page, name: string) => page.getByRole("button", { name: `More for ${name}` })

const downTunnel = {
  ...links.find((l) => l.name === "wg0"),
  name: "gre1",
  index: 43,
  kind: "gre",
  role: "tunnel",
  owner: "just-dashboard",
  managed: true,
  guard: undefined,
  adminUp: false,
  carrier: false,
  state: "down",
  remote: "198.51.100.7",
  local: "203.0.113.20",
  addresses: [],
}

const trail = {
  total: 5,
  entries: [
    {
      id: 5,
      ts: iso(4),
      username: "operator",
      action: "network.address.add",
      target: "jd-lab",
      detail: '{"cidr":"192.168.50.1/24"}',
      success: true,
    },
    {
      id: 4,
      ts: iso(50),
      username: "operator",
      action: "network.link.mtu",
      target: "jd-lab",
      detail: '{"mtu":9000}',
      success: false,
    },
    {
      id: 3,
      ts: iso(60 * 26),
      username: "operator",
      action: "network.link.create",
      target: "jd-lab",
      detail: '{"kind":"bridge"}',
      success: true,
    },
    {
      id: 2,
      ts: iso(60 * 30),
      username: "operator",
      action: "network.link.create",
      target: "vlan30",
      detail: '{"kind":"vlan"}',
      success: true,
    },
    {
      id: 1,
      ts: iso(60 * 40),
      username: "operator",
      action: "network.namespace.create",
      target: "jd-lab",
      detail: "",
      success: true,
    },
  ],
}

async function top(locator: Locator) {
  const box = await locator.boundingBox()
  if (!box) throw new Error("nothing to measure")
  return box.y
}

async function heights(locator: Locator) {
  return locator.evaluateAll((nodes) => nodes.map((node) => node.getBoundingClientRect().height))
}

/** Holds one read until `release` is called, so the page can be measured while it waits. */
async function hold(page: Page, path: string, body: unknown) {
  let release = () => {}
  const gate = new Promise<void>((resolve) => {
    release = resolve
  })
  await page.route(`**/api/v1${path}`, async (route) => {
    await gate
    await json(route, body)
  })
  return release
}

for (const width of [1280, 390]) {
  test.describe(`${width} wide`, () => {
    test.use({ viewport: { width, height: width === 390 ? 844 : 900 } })

    test("the list opens on one line of what is here, with no tile row", async ({ page }) => {
      await mockNetwork(page)
      await page.goto("/network/interfaces")
      await loaded(page)
      const line = page.getByRole("region", { name: "About the interfaces" })
      await expect(line).toContainText("9 devices")
      await expect(line).toContainText("uplink ens3")
      await expect(line).toContainText("2 made here")
      await expect(line).toContainText("All up")
      await expect(line.getByRole("button", { name: "New device", exact: true })).toBeVisible()
      await expect(page.locator('[data-slot="stat-grid"]')).toHaveCount(0)
      // The line is the first thing on the page.
      const first = page.locator("[data-slot=page] > :not(h1)").first()
      await expect(first).toHaveAttribute("data-slot", "host-identity")
      expect(
        await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth),
      ).toBe(true)
    })

    test("the list's line stands in one place while the devices are read", async ({ page }) => {
      await mockNetwork(page)
      const release = await hold(page, "/network/links", links)
      await page.goto("/network/interfaces")
      const line = page.locator("[data-slot=host-identity]")
      await expect(line).toContainText("Reading")
      await risen(page)
      const waiting = await line.boundingBox()
      release()
      await expect(line).toContainText("9 devices")
      await risen(page)
      const landed = await line.boundingBox()
      expect(landed?.y).toBe(waiting?.y)
      expect(landed?.height).toBe(waiting?.height)
    })

    test("device and namespace cards are one height whatever they carry", async ({ page }) => {
      await mockNetwork(page, [], {
        overrides: {
          "/network/links": [...links, downTunnel],
          "/network/namespaces": [
            ...namespaces,
            { name: "empty", kind: "named", id: 3, managed: false, devices: [] },
            {
              name: "gone",
              kind: "container",
              managed: false,
              image: "busybox",
              pid: 99,
              devices: [],
              readError: "nsenter: cannot open /proc/99/ns/net: No such file or directory",
            },
          ],
        },
      })
      await page.goto("/network/interfaces")
      await loaded(page)
      await page.getByRole("button", { name: /Everything/ }).click()
      // With addresses and without, made here or not, up or down, a container's veth.
      const devices = page.locator("[data-slot=choice-list]:not([aria-label]) > li")
      await expect(devices.filter({ hasText: "gre1" })).toHaveCount(1)
      expect(new Set(await heights(devices)).size).toBe(1)

      await page.getByRole("switch", { name: "Show containers' namespaces" }).click()
      const cards = page.getByRole("list", { name: "Namespaces" }).locator("> li")
      await expect(cards).toHaveCount(5)
      // Removable, not removable, empty, unread: one height, and the count in one column.
      expect(new Set(await heights(cards)).size).toBe(1)
      const counts = cards.locator("[data-slot=namespace-members]")
      const rights = await counts.evaluateAll((nodes) =>
        nodes.map((node) => Math.round(node.getBoundingClientRect().right)),
      )
      expect(new Set(rights).size).toBe(1)
      await expect(cards.filter({ hasText: "lab" }).first()).toContainText("1 device")
      await expect(cards.filter({ hasText: "gone" })).toContainText("devices unknown")
      // A card does not grow when it is pressed: its devices are its sheet's.
      const below = cards.nth(1)
      const before = await top(below)
      await page.getByRole("button", { name: "Inspect lab", exact: true }).click()
      await expect(page.getByRole("dialog")).toContainText("lab-n")
      await page.keyboard.press("Escape")
      await expect(page.getByRole("dialog")).toHaveCount(0)
      expect(await top(below)).toBe(before)
    })

    test("the address that opened the sheet lands on the device's page", async ({ page }) => {
      await mockNetwork(page)
      await page.goto("/network/interfaces?device=wg0")
      await expect(page).toHaveURL(/\/network\/interfaces\/wg0$/)
      await loaded(page)
      await expect(identity(page, "wg0")).toContainText("Tunnel")
      await expect(page.getByRole("dialog")).toHaveCount(0)
      await expect(page.locator('[data-slot="stat-grid"]')).toHaveCount(0)
      // The list is one press back.
      await page.getByRole("link", { name: "Interfaces", exact: true }).first().click()
      await expect(page).toHaveURL(/\/network\/interfaces$/)
    })

    test("a device's line stands in one place while the devices are read", async ({ page }) => {
      await mockNetwork(page)
      const release = await hold(page, "/network/links", links)
      await page.goto("/network/interfaces/ens3")
      const line = page.locator("[data-slot=device-identity]")
      await expect(line).toContainText("Reading")
      await risen(page)
      const waiting = await line.boundingBox()
      release()
      await expect(line).toContainText("Uplink")
      await risen(page)
      const landed = await line.boundingBox()
      expect(landed?.y).toBe(waiting?.y)
      expect(landed?.height).toBe(waiting?.height)
    })

    test("a device's views are in the address, and one it does not have opens on traffic", async ({
      page,
    }) => {
      await mockNetwork(page, [], { overrides: { "/audit/": trail } })
      await page.goto("/network/interfaces/jd-lab?tab=addresses")
      await loaded(page)
      await expect(page.getByRole("tab", { name: /Addresses/ })).toHaveAttribute(
        "aria-selected",
        "true",
      )
      await expect(device(page).getByRole("list", { name: "Addresses" })).toContainText(
        "192.168.50.1",
      )
      for (const [name, tab] of [
        ["Ports", "ports"],
        ["Settings", "settings"],
        ["History", "history"],
      ]) {
        await page.getByRole("tab", { name: new RegExp(name) }).click()
        await expect(page).toHaveURL(new RegExp(`/network/interfaces/jd-lab\\?tab=${tab}$`))
      }
      await page.getByRole("tab", { name: "Traffic" }).click()
      await expect(page).toHaveURL(/\/network\/interfaces\/jd-lab$/)
      // A card has no ports: the address asks for a view it does not have.
      await page.goto("/network/interfaces/ens3?tab=ports")
      await loaded(page)
      await expect(page.getByRole("tab", { name: "Ports" })).toHaveCount(0)
      await expect(page.getByRole("tab", { name: "Traffic" })).toHaveAttribute(
        "aria-selected",
        "true",
      )
      expect(
        await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth),
      ).toBe(true)
    })

    test("address rows are one height, guarded or removable, and adding one moves none", async ({
      page,
    }) => {
      await mockNetwork(page, [], {
        overrides: {
          "/network/links": links.map((l) =>
            l.name === "jd-lab"
              ? {
                  ...l,
                  addresses: [
                    { cidr: "192.168.50.1/24", family: "inet", scope: "global", managed: true },
                    {
                      cidr: "2001:db8:50::1/64",
                      family: "inet6",
                      scope: "global",
                      managed: true,
                      guard: "Your browser is answered from this address.",
                    },
                    {
                      cidr: "fe80::42:aff:fe00:1/64",
                      family: "inet6",
                      scope: "link",
                      origin: "link-local",
                    },
                  ],
                }
              : l,
          ),
        },
      })
      await page.goto("/network/interfaces/jd-lab?tab=addresses")
      await loaded(page)
      const rows = device(page).locator("[data-slot=address-row]")
      await expect(rows).toHaveCount(3)
      expect(new Set(await heights(rows)).size).toBe(1)
      await expect(
        rows.nth(0).getByRole("button", { name: "Remove 192.168.50.1/24" }),
      ).toBeVisible()
      await expect(rows.nth(1)).toContainText("guarded")
      await expect(rows.nth(1).getByRole("button", { name: /^Remove/ })).toHaveCount(0)
      await expect(rows.nth(2).getByRole("button")).toHaveCount(0)
      const second = await top(rows.nth(1))
      await device(page).getByLabel("Address in CIDR form").fill("not an address")
      expect(await top(rows.nth(1))).toBe(second)
    })

    test("the page is one pane that never scrolls sideways, on every view", async ({ page }) => {
      await mockNetwork(page, [], { overrides: { "/audit/": trail } })
      for (const tab of ["", "addresses", "ports", "settings", "history"]) {
        await page.goto(`/network/interfaces/jd-lab${tab ? `?tab=${tab}` : ""}`)
        await loaded(page)
        expect(
          await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth),
          `${tab || "traffic"} at ${width}`,
        ).toBe(true)
      }
    })
  })
}

test.describe("the rail", () => {
  test.use({ viewport: { width: 1440, height: 900 } })

  test("lists the devices beside the one on screen and walks them with the view kept", async ({
    page,
  }) => {
    await mockNetwork(page)
    await page.goto("/network/interfaces/tailscale0?tab=addresses")
    await loaded(page)
    const rail = page.getByRole("navigation", { name: "Devices" })
    await expect(rail).toBeVisible()
    await expect(rail.getByRole("button", { name: /^tailscale0,/ })).toHaveAttribute(
      "aria-current",
      "page",
    )
    // The list's own order: the uplink, then the tunnels, then the bridges.
    const names = await rail
      .locator("[data-workspace-item]")
      .evaluateAll((nodes) => nodes.map((node) => node.getAttribute("data-workspace-item")))
    expect(names.slice(0, 4)).toEqual(["ens3", "tailscale0", "wg0", "jd-lab"])
    expect(names).not.toContain("lo")

    await page.keyboard.press("Alt+ArrowDown")
    await expect(page).toHaveURL(/\/network\/interfaces\/wg0\?tab=addresses$/)
    await expect(rail.getByRole("button", { name: /^wg0,/ })).toHaveAttribute(
      "aria-current",
      "page",
    )
    await page.getByRole("button", { name: "Previous device" }).click()
    await expect(page).toHaveURL(/\/network\/interfaces\/tailscale0\?tab=addresses$/)
    await rail.getByRole("button", { name: /^jd-lab,/ }).click()
    await expect(page).toHaveURL(/\/network\/interfaces\/jd-lab\?tab=addresses$/)
    await expect(identity(page, "jd-lab")).toContainText("made here")
  })

  test("carries the list's search and its Everything, and pins a device the filter hides", async ({
    page,
  }) => {
    await mockNetwork(page)
    await page.goto("/network/interfaces")
    await loaded(page)
    await page.getByLabel("Search devices", { exact: true }).fill("10.0")
    await page.getByRole("link", { name: "Open docker0", exact: true }).click()
    await expect(page).toHaveURL(/\/network\/interfaces\/docker0$/)
    const rail = page.getByRole("navigation", { name: "Devices" })
    await expect(rail.getByLabel("Search the devices", { exact: true })).toHaveValue("10.0")
    await expect(rail.getByRole("button", { name: /^ens3,/ })).toHaveCount(0)
    await expect(rail.getByRole("button", { name: /^docker0,/ })).toHaveCount(1)

    await rail.getByLabel("Search the devices", { exact: true }).fill("")
    await expect(rail.getByRole("button", { name: /^ens3,/ })).toHaveCount(1)
    // A container's veth is behind Everything: opened by its address, it is
    // still on its own rail, first.
    await page.goto("/network/interfaces/veth6e4f828")
    await loaded(page)
    await expect(rail.locator("[data-workspace-item]").first()).toHaveAttribute(
      "data-workspace-item",
      "veth6e4f828",
    )
    await rail.getByRole("button", { name: /^Everything/ }).click()
    await expect(rail.getByRole("button", { name: /^lo,/ })).toHaveCount(1)
    // And the list it came from has the same filter when it is gone back to.
    await page.getByRole("link", { name: "Interfaces", exact: true }).first().click()
    await expect(page.getByRole("link", { name: "Open lo", exact: true })).toBeVisible()
  })

  test("is the page's own pane on a phone: no rail, the arrows in the header", async ({ page }) => {
    await page.setViewportSize({ width: 390, height: 844 })
    await mockNetwork(page)
    await page.goto("/network/interfaces/ens3")
    await loaded(page)
    await expect(page.getByRole("navigation", { name: "Devices" })).toBeHidden()
    await page.getByRole("button", { name: "Next device" }).click()
    await expect(page).toHaveURL(/\/network\/interfaces\/tailscale0$/)
  })
})

test.describe("a device's page", () => {
  test.use({ viewport: { width: 1280, height: 900 } })

  test("draws what the device is wired to, each end opening what it is", async ({ page }) => {
    await mockNetwork(page)
    await page.goto("/network/interfaces/ens3")
    await loaded(page)
    const away = device(page).getByRole("region", { name: "Leads to" })
    await expect(away).toContainText("The internet")
    const here = device(page).getByRole("region", { name: "On this server" })
    await expect(here.getByRole("link", { name: "Open vlan30" })).toHaveAttribute(
      "href",
      "/network/interfaces/vlan30",
    )
    // Where it answers opens its addresses.
    await here.getByRole("button", { name: "ens3’s addresses" }).click()
    await expect(page).toHaveURL(/\/network\/interfaces\/ens3\?tab=addresses$/)

    await page.goto("/network/interfaces/jd-lab")
    await loaded(page)
    await expect(
      device(page).getByRole("region", { name: "Leads to" }).getByRole("link", {
        name: "Open vlan30",
      }),
    ).toBeVisible()
  })

  test("says what it has carried since it was made, in words", async ({ page }) => {
    await mockNetwork(page)
    await page.goto("/network/interfaces/ens3")
    await loaded(page)
    const since = device(page).locator("[data-slot=reading-lines]")
    await expect(since.locator("[data-slot=reading-line]")).toHaveCount(3)
    await expect(since).toContainText("Received")
    await expect(since).toContainText("packets")
    await expect(since).toContainText("Faults")
    await expect(device(page).getByText("Last 15 minutes")).toBeVisible()
  })

  test("sets a device that is down up from its line, and one that is up down from its menu", async ({
    page,
  }) => {
    const mutations: Mutation[] = []
    await mockNetwork(page, mutations, { overrides: { "/network/links": [...links, downTunnel] } })
    await page.goto("/network/interfaces/gre1")
    await loaded(page)
    await expect(identity(page, "gre1")).toContainText("Down")
    await identity(page, "gre1").getByRole("button", { name: "Set up", exact: true }).click()
    await expect
      .poll(() => mutations)
      .toEqual([{ method: "POST", path: "/network/links/gre1/up", body: null }])

    await page.goto("/network/interfaces/jd-lab")
    await loaded(page)
    // A device that is up has no command on its line: setting it down is in the menu.
    await expect(identity(page, "jd-lab").getByRole("button", { name: "Set up" })).toHaveCount(0)
    await menu(page, "jd-lab").click()
    await page.getByRole("menuitem", { name: "Set down" }).click()
    const confirm = page.getByRole("dialog", { name: "Set jd-lab down" })
    await confirm.getByRole("button", { name: "Set down", exact: true }).click()
    await expect
      .poll(() => mutations.at(-1))
      .toEqual({ method: "POST", path: "/network/links/jd-lab/down", body: null })
    await expect(confirm).toHaveCount(0)
  })

  test("deletes a device the dashboard made and goes back to the list", async ({ page }) => {
    const mutations: Mutation[] = []
    await mockNetwork(page, mutations)
    await page.goto("/network/interfaces/jd-lab")
    await loaded(page)
    await menu(page, "jd-lab").click()
    await page.getByRole("menuitem", { name: "Delete" }).click()
    await page
      .getByRole("dialog", { name: "Delete jd-lab" })
      .getByRole("button", { name: "Delete", exact: true })
      .click()
    await expect
      .poll(() => mutations)
      .toEqual([{ method: "DELETE", path: "/network/links/jd-lab", body: null }])
    await expect(page).toHaveURL(/\/network\/interfaces$/)
    // The uplink was not made here: it has nothing to delete.
    await page.goto("/network/interfaces/ens3")
    await loaded(page)
    await menu(page, "ens3").click()
    await expect(page.getByRole("menuitem", { name: "Delete" })).toHaveCount(0)
  })

  test("adds and removes a static address", async ({ page }) => {
    const mutations: Mutation[] = []
    await mockNetwork(page, mutations)
    await page.goto("/network/interfaces/jd-lab?tab=addresses")
    await loaded(page)
    await device(page).getByLabel("Address in CIDR form").fill("192.168.50.2/24")
    await device(page).getByRole("button", { name: "Add", exact: true }).click()
    await expect
      .poll(() => mutations)
      .toEqual([
        {
          method: "POST",
          path: "/network/links/jd-lab/addresses",
          body: { cidr: "192.168.50.2/24" },
        },
      ])
    await expect(device(page).getByLabel("Address in CIDR form")).toHaveValue("")
    await device(page).getByRole("button", { name: "Remove 192.168.50.1/24" }).click()
    await page
      .getByRole("dialog", { name: "Remove 192.168.50.1/24" })
      .getByRole("button", { name: "Remove", exact: true })
      .click()
    await expect
      .poll(() => mutations.at(-1))
      .toMatchObject({ method: "DELETE", path: "/network/links/jd-lab/addresses" })
  })

  test("saves the MTU and a bridge move through the page's one Save, the move confirmed", async ({
    page,
  }) => {
    const mutations: Mutation[] = []
    await mockNetwork(page, mutations)
    await page.goto("/network/interfaces/vlan30?tab=settings")
    await loaded(page)
    await expect(saveBar(page)).toHaveCount(0)
    const mtu = device(page).getByRole("textbox", { name: "MTU", exact: true })
    await mtu.fill("14x")
    await expect(device(page).getByText("A whole number of bytes.")).toBeVisible()
    await expect(saveBar(page).getByRole("button", { name: /^Save/ })).toBeDisabled()
    await mtu.fill("1400")
    await device(page).getByRole("combobox", { name: "Bridge membership" }).click()
    await page.getByRole("option", { name: "No bridge" }).click()
    await expect(saveBar(page)).toContainText("2 unsaved changes")
    await expect(saveBar(page)).toContainText("Applies immediately")
    await saveBar(page).getByRole("button", { name: /^Save/ }).click()
    const confirm = page.getByRole("dialog", { name: "Remove vlan30 from its bridge" })
    await confirm.getByRole("button", { name: "Apply", exact: true }).click()
    await expect
      .poll(() => mutations)
      .toEqual([
        { method: "POST", path: "/network/links/vlan30/mtu", body: { mtu: 1400 } },
        { method: "POST", path: "/network/links/vlan30/master", body: { master: "" } },
      ])
  })

  test("a bridge move that is not confirmed is not written and stays to be saved", async ({
    page,
  }) => {
    const mutations: Mutation[] = []
    await mockNetwork(page, mutations)
    await page.goto("/network/interfaces/vlan30?tab=settings")
    await loaded(page)
    await device(page).getByRole("combobox", { name: "Bridge membership" }).click()
    await page.getByRole("option", { name: "No bridge" }).click()
    await saveBar(page).getByRole("button", { name: /^Save/ }).click()
    await page
      .getByRole("dialog", { name: "Remove vlan30 from its bridge" })
      .getByRole("button", { name: "Cancel" })
      .click()
    await expect(saveBar(page)).toContainText("1 unsaved change")
    expect(mutations).toEqual([])
    await saveBar(page).getByRole("button", { name: "Discard" }).click()
    await expect(saveBar(page)).toHaveCount(0)
    await expect(device(page).getByRole("combobox", { name: "Bridge membership" })).toContainText(
      "jd-lab",
    )
  })

  test("reads what was done to this device, and says when the server refused", async ({ page }) => {
    await mockNetwork(page, [], { overrides: { "/audit/": trail } })
    await page.goto("/network/interfaces/jd-lab?tab=history")
    await loaded(page)
    const rows = device(page).locator("[data-slot=history-row]")
    // Its own three: not vlan30's, and not the namespace that shares its name.
    await expect(rows).toHaveCount(3)
    await expect(rows.nth(0)).toContainText("Address added")
    await expect(rows.nth(0)).toContainText("192.168.50.1/24")
    await expect(rows.nth(1)).toContainText("MTU changed (refused)")
    await expect(rows.nth(2)).toContainText("Made")
    expect(new Set(await heights(rows)).size).toBe(1)
    await expect(page.getByRole("tab", { name: /History/ })).toContainText("3")
  })

  test("a device nobody changed says so, with the way to the whole trail", async ({ page }) => {
    await mockNetwork(page, [], { overrides: { "/audit/": { entries: [], total: 0 } } })
    await page.goto("/network/interfaces/ens3?tab=history")
    await loaded(page)
    await expect(device(page).getByText("Nothing was changed from the dashboard")).toBeVisible()
    await expect(device(page).getByRole("link", { name: "Open the audit log" })).toHaveAttribute(
      "href",
      "/audit",
    )
  })

  test("a name no device answers to says so, with the way back", async ({ page }) => {
    await mockNetwork(page)
    await page.goto("/network/interfaces/nope0")
    await loaded(page)
    await expect(page.getByText("No device named nope0")).toBeVisible()
    await expect(page.getByRole("link", { name: "Back to the interfaces" })).toHaveAttribute(
      "href",
      "/network/interfaces",
    )
    await expect(page.getByRole("tablist")).toHaveCount(0)
  })

  test("a failed first read is an error with a retry, not an empty device", async ({ page }) => {
    await mockNetwork(page)
    let fail = true
    await page.route("**/api/v1/network/links", (route) =>
      fail
        ? json(
            route,
            { error: { code: "unavailable", message: "ip link did not answer", retryable: true } },
            503,
          )
        : json(route, links),
    )
    await page.goto("/network/interfaces/ens3")
    await expect(page.getByText("ip link did not answer")).toBeVisible()
    await expect(page.getByText("No device named ens3")).toHaveCount(0)
    fail = false
    await page.getByRole("button", { name: "Try again" }).click()
    await expect(identity(page, "ens3")).toContainText("Uplink")
  })
})

test.describe("an account that may only read", () => {
  test.use({ viewport: { width: 1280, height: 900 } })

  test("sees the list with nothing it cannot use", async ({ page }) => {
    await mockNetwork(page, [], { session: reader })
    await page.goto("/network/interfaces")
    await loaded(page)
    await expect(page.getByRole("region", { name: "About the interfaces" })).toContainText(
      "9 devices",
    )
    await expect(page.getByRole("button", { name: "New device" })).toHaveCount(0)
    await expect(page.getByRole("button", { name: "New namespace" })).toHaveCount(0)
    await expect(page.getByRole("button", { name: "More for lab" })).toHaveCount(0)
    // It can still open a device and a namespace.
    await expect(page.getByRole("link", { name: "Open ens3", exact: true })).toBeVisible()
    await expect(page.getByRole("button", { name: "Inspect lab", exact: true })).toBeVisible()
  })

  test("sees a device with no command, no field and no history, and asks for none of it", async ({
    page,
  }) => {
    const asked: string[] = []
    await mockNetwork(page, [], {
      session: reader,
      overrides: { "/network/links": [...links, downTunnel] },
    })
    page.on("request", (request) => {
      const path = new URL(request.url()).pathname
      if (path.includes("/audit") || path.includes("/native/profiles/")) asked.push(path)
    })
    await page.goto("/network/interfaces/jd-lab")
    await loaded(page)
    await expect(identity(page, "jd-lab")).toContainText("Up")
    await expect(menu(page, "jd-lab")).toHaveCount(0)
    await expect(page.getByRole("tab", { name: /History/ })).toHaveCount(0)

    await page.getByRole("tab", { name: /Addresses/ }).click()
    await expect(device(page).getByRole("list", { name: "Addresses" })).toContainText(
      "192.168.50.1",
    )
    await expect(device(page).getByLabel("Address in CIDR form")).toHaveCount(0)
    await expect(device(page).getByRole("button", { name: /^Remove/ })).toHaveCount(0)

    await page.getByRole("tab", { name: "Settings" }).click()
    await expect(device(page).getByRole("heading", { name: "Device", exact: true })).toBeVisible()
    await expect(device(page).getByText("jd-lab's own VLANs")).toBeVisible()
    const pane = page.getByRole("tabpanel")
    await expect(pane.getByRole("textbox")).toHaveCount(0)
    await expect(pane.getByRole("combobox")).toHaveCount(0)
    await expect(pane.locator("button:disabled, input:disabled")).toHaveCount(0)

    // A device that is down offers it no Set up.
    await page.goto("/network/interfaces/gre1")
    await loaded(page)
    await expect(identity(page, "gre1")).toContainText("Down")
    await expect(page.getByRole("button", { name: "Set up" })).toHaveCount(0)
    expect(asked).toEqual([])
  })
})
