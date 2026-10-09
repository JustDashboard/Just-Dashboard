import { expect, test, type Page } from "@playwright/test"
import { admin, json, loaded, mockNetwork, type Mutation } from "./network-fixture"
import { forwardPreview, gateway, overrides, pressure, protection } from "./network-gateway-fixture"

/**
 * The Gateway and Protection pages against a mocked API
 * (`network-gateway-fixture.ts`): what each renders from the shapes `netx`
 * writes, what its forms send, and the one refusal with a fix in the page —
 * "forwarding is off". The checks are about what the pages do with the
 * answers, which is the part a Go test cannot see.
 */

const open = async (page: Page, path: string, mutations: Mutation[] = [], data = overrides) => {
  await mockNetwork(page, mutations, { overrides: data })
  await page.goto(path)
}

/**
 * Review screenshots, for the eyes the checks do not have. Written only when
 * asked for, into the directory named — never into the tree.
 */
test.describe("screenshots", () => {
  const dir = process.env.JD_NETWORK_SHOTS
  test.skip(!dir, "set JD_NETWORK_SHOTS to a directory to capture them")

  for (const width of [390, 1440]) {
    test.describe(`${width} wide`, () => {
      test.use({ viewport: { width, height: 1000 } })
      for (const path of ["/network/gateway", "/network/protection"]) {
        test(`${path} at ${width}`, async ({ page }) => {
          await open(page, path)
          await loaded(page)
          await page.waitForTimeout(1500)
          const name = path.replace(/^\/network\//, "")
          await page.screenshot({ path: `${dir}/${name}-${width}.png`, fullPage: true })
          for (let part = 1; part <= 8; part++) {
            const moved = await page.locator("[data-slot=page]").evaluate((element) => {
              let parent = element.parentElement
              while (parent && !/auto|scroll/.test(getComputedStyle(parent).overflowY)) {
                parent = parent.parentElement
              }
              if (!parent || parent.scrollTop + parent.clientHeight >= parent.scrollHeight - 1)
                return false
              parent.scrollTop += parent.clientHeight - 80
              return true
            })
            if (!moved) break
            await page.waitForTimeout(300)
            await page.screenshot({ path: `${dir}/${name}-${width}-scroll-${part}.png` })
          }
        })
      }

      test(`the forward editor and the blocklist dialog at ${width}`, async ({ page }) => {
        await open(page, "/network/gateway")
        await page.getByRole("button", { name: "Edit :8080 → 10.0.4.5:80" }).click()
        await page.waitForTimeout(600)
        await page.screenshot({ path: `${dir}/gateway-forward-${width}.png` })
        await page.keyboard.press("Escape")
        await page.goto("/network/protection")
        await page.getByRole("button", { name: "New blocklist" }).click()
        await page.waitForTimeout(600)
        await page.screenshot({ path: `${dir}/protection-blocklist-${width}.png` })
      })
    })
  }
})

test("the gateway draws the picture, the readings and both lists", async ({ page }) => {
  await open(page, "/network/gateway")
  await expect(page.getByRole("region", { name: "Arrives" })).toBeVisible()
  await expect(page.getByRole("region", { name: "Goes to" })).toBeVisible()
  // A forward is a public port on the left and its target on the right.
  const arrives = page.getByRole("region", { name: "Arrives" })
  await expect(arrives.getByText(":8080")).toBeVisible()
  await expect(page.getByRole("region", { name: "Goes to" }).getByText("10.0.4.5:80")).toBeVisible()
  // Each is a lit row that opens its editor, with a switch of its own.
  for (const title of [
    ":8080 → 10.0.4.5:80",
    ":25565 → 10.0.1.9:25565",
    ":27015 → 10.0.1.9:27015",
    ":8443 → 10.0.4.7:443",
  ]) {
    await expect(page.getByRole("button", { name: `Edit ${title}` })).toBeVisible()
    await expect(page.getByRole("switch", { name: `${title} in force` })).toBeVisible()
  }
  await expect(
    page.getByRole("switch", { name: ":8443 → 10.0.4.7:443 in force" }),
  ).not.toBeChecked()
  // The hand-offs live in Proxy & TLS.
  for (const [name, href] of [
    ["Open reverse proxy sites", "/proxy/sites"],
    ["Open TCP and UDP streams", "/proxy/streams"],
    ["Open certificates", "/proxy/certificates"],
  ]) {
    await expect(page.getByRole("link", { name })).toHaveAttribute("href", href)
  }
})

test("a forward made by hand sends exactly what the form says", async ({ page }) => {
  const mutations: Mutation[] = []
  await open(page, "/network/gateway", mutations)
  await page.getByRole("button", { name: "Forward a port" }).first().click()
  const sheet = page.getByRole("dialog")
  await sheet.getByLabel("Name").fill("Grafana")
  await sheet.getByRole("radio", { name: "TCP", exact: true }).click()
  await sheet.getByLabel("Arrive on").click()
  await page.getByRole("option", { name: "ens3" }).click()
  await sheet.getByLabel("Public port").fill("3000")
  await sheet.getByLabel("Target address").fill("10.0.4.12")
  await sheet.getByLabel("Target port").fill("3001")
  await sheet.getByRole("button", { name: /Always/ }).click()
  await sheet.getByLabel("Allowed sources").fill("203.0.113.0/24, 198.51.100.7")
  await sheet.getByRole("button", { name: "Forward port" }).click()
  const saves = () => mutations.filter((m) => m.path !== "/network/gateway/preview")
  await expect.poll(() => saves().length).toBe(1)
  expect(saves()[0]).toEqual({
    method: "POST",
    path: "/network/gateway/forwards",
    body: {
      name: "Grafana",
      protocol: "tcp",
      interface: "ens3",
      ports: "3000",
      target: "10.0.4.12",
      targetPort: "3001",
      sourceNat: "always",
      sources: ["203.0.113.0/24", "198.51.100.7"],
    },
  })
})

test("switching a forward off asks first and sends the whole forward with enabled false", async ({
  page,
}) => {
  const mutations: Mutation[] = []
  await open(page, "/network/gateway", mutations)
  await page.getByRole("switch", { name: ":8080 → 10.0.4.5:80 in force" }).click()
  await expect(page.getByRole("dialog")).toContainText("stops being passed on")
  expect(mutations).toEqual([])
  await page.getByRole("dialog").getByRole("button", { name: "Switch off" }).click()
  await expect.poll(() => mutations.length).toBe(1)
  expect(mutations[0]).toEqual({
    method: "PUT",
    path: "/network/gateway/forwards/1",
    body: {
      name: "Website",
      protocol: "tcp",
      interface: "",
      ports: "8080",
      target: "10.0.4.5",
      targetPort: "80",
      sourceNat: "auto",
      sources: [],
      enabled: false,
    },
  })
})

test("forwarding being off is fixed in the form, and the form sends itself again", async ({
  page,
}) => {
  const mutations: Mutation[] = []
  await open(page, "/network/gateway", mutations)
  // The first save is refused; once forwarding is on the same save goes through.
  let refused = false
  await page.route("**/api/v1/network/gateway/forwards", async (route) => {
    if (route.request().method() !== "POST") return route.fallback()
    mutations.push({
      method: "POST",
      path: "/network/gateway/forwards",
      body: route.request().postDataJSON(),
    })
    if (refused) return json(route, { id: 9 })
    refused = true
    return json(
      route,
      {
        error: {
          code: "forwarding_off",
          message:
            "IPv4 forwarding is off, so nothing could pass through this server to the address. Turn it on on the Routing page first.",
        },
      },
      409,
    )
  })
  await page.getByRole("button", { name: "Forward a port" }).first().click()
  const sheet = page.getByRole("dialog")
  await sheet.getByLabel("Name").fill("Grafana")
  await sheet.getByLabel("Public port").fill("3000")
  await sheet.getByLabel("Target address").fill("10.0.4.12")
  await sheet.getByRole("button", { name: "Forward port" }).click()
  await sheet.getByRole("button", { name: "Turn on IPv4 forwarding" }).click()
  // The editor's preview reads while the refused form stays open; it changes nothing.
  const changes = () => mutations.filter((m) => m.path !== "/network/gateway/preview")
  await expect.poll(() => changes().length).toBe(3)
  expect(changes().map((m) => `${m.method} ${m.path}`)).toEqual([
    "POST /network/gateway/forwards",
    "POST /network/forwarding/ipv4/on",
    "POST /network/gateway/forwards",
  ])
  expect(changes()[2].body).toEqual(changes()[0].body)
  await expect(sheet).toBeHidden()
})

test("a firewall that will not let the gateway write says why and turns the commands off", async ({
  page,
}) => {
  await open(page, "/network/gateway", [], {
    ...overrides,
    "/network/gateway": {
      ...gateway,
      capability: {
        writable: false,
        reason: "firewalld filters forwarded traffic in its own nftables table.",
        firewall: "nftables",
        docker: false,
        blocker: {
          family: "inet",
          table: "filter",
          chain: "forward",
          rule: "ct mark 0x4a000000/0xff000000 accept",
        },
      },
    },
  })
  const notice = page.getByText("This firewall does not let the gateway write")
  await expect(notice).toBeVisible()
  await expect(page.getByText("ct mark 0x4a000000/0xff000000 accept")).toBeVisible()
  await expect(page.getByRole("button", { name: "Forward a port" })).toBeDisabled()
  await expect(page.getByRole("button", { name: "Share a network" })).toBeDisabled()
})

test("a NAT entry the VPN page made is read-only here and leads there", async ({ page }) => {
  await open(page, "/network/gateway")
  const owned = page.getByRole("link", { name: "Open WireGuard on the VPN page" })
  await expect(owned).toHaveAttribute("href", "/network/vpn")
  await expect(page.getByText("made by WireGuard wg0")).toBeVisible()
  // No switch to change it and no editor to remove it from.
  await expect(page.getByRole("switch", { name: /10\.8\.0\.0\/24/ })).toHaveCount(0)
  await expect(page.getByRole("switch", { name: /10\.20\.0\.0\/24/ })).toBeVisible()
  // The one made by hand opens an editor that can remove it.
  await page.getByRole("button", { name: "Edit 10.20.0.0/24 out through ens3" }).click()
  await expect(page.getByRole("dialog").getByRole("button", { name: "Remove" })).toBeVisible()
})

test("a NAT entry made by hand sends exactly what the form says", async ({ page }) => {
  const mutations: Mutation[] = []
  await open(page, "/network/gateway", mutations)
  await page.getByRole("button", { name: "Share a network" }).click()
  const sheet = page.getByRole("dialog")
  await sheet.getByLabel("Name").fill("Office VLAN")
  await sheet.getByLabel("Network").fill("10.30.0.0/24")
  await sheet.getByLabel("Leaves through").click()
  await page.getByRole("option", { name: "ens3" }).click()
  await sheet.getByRole("button", { name: "Share network" }).click()
  const saves = () => mutations.filter((m) => m.path !== "/network/gateway/preview")
  await expect.poll(() => saves().length).toBe(1)
  expect(saves()[0]).toEqual({
    method: "POST",
    path: "/network/gateway/nat",
    body: {
      name: "Office VLAN",
      source: "10.30.0.0/24",
      interface: "ens3",
      toAddress: "",
      mode: "masquerade",
      translated: "",
      destinations: [],
    },
  })
})

test("a country blocklist sends lower-case codes", async ({ page }) => {
  const mutations: Mutation[] = []
  await open(page, "/network/protection", mutations)
  await page.getByRole("button", { name: "New blocklist" }).click()
  const dialog = page.getByRole("dialog")
  const search = dialog.getByRole("searchbox", { name: "Search countries" })
  await search.fill("china")
  await dialog.getByRole("checkbox", { name: /China/ }).click()
  await search.fill("russia")
  await dialog.getByRole("checkbox", { name: /Russia/ }).click()
  await dialog.getByRole("button", { name: "Make list" }).click()
  await expect.poll(() => mutations.length).toBe(1)
  expect(mutations[0]).toEqual({
    method: "POST",
    path: "/network/protection/blocklists",
    body: {
      name: "China, Russia",
      kind: "country",
      entries: [],
      countries: ["cn", "ru"],
      url: "",
      preset: "",
      refresh: "24h",
    },
  })
})

test("a feed is picked by its preset and a manual list by its addresses", async ({ page }) => {
  const mutations: Mutation[] = []
  await open(page, "/network/protection", mutations)
  await page.getByRole("button", { name: "New blocklist" }).click()
  let dialog = page.getByRole("dialog")
  await dialog.getByRole("button", { name: /A feed/ }).click()
  await dialog.getByRole("button", { name: /FireHOL level 1/ }).click()
  await dialog.getByRole("button", { name: "Make list" }).click()
  await expect.poll(() => mutations.length).toBe(1)
  expect(mutations[0].body).toEqual({
    name: "FireHOL level 1",
    kind: "feed",
    entries: [],
    countries: [],
    url: "",
    preset: "firehol-level1",
    refresh: "24h",
  })
  await page.getByRole("button", { name: "New blocklist" }).click()
  dialog = page.getByRole("dialog")
  await dialog.getByRole("button", { name: /Addresses I type/ }).click()
  await dialog.getByLabel("Addresses and networks").fill("198.51.100.9\n203.0.113.0/24")
  await dialog.getByLabel("Name").fill("Noise")
  await dialog.getByRole("button", { name: "Make list" }).click()
  await expect.poll(() => mutations.length).toBe(2)
  expect(mutations[1].body).toEqual({
    name: "Noise",
    kind: "manual",
    entries: ["198.51.100.9", "203.0.113.0/24"],
    countries: [],
    url: "",
    preset: "",
  })
})

test("the protection page reads the lists, the limits and the trusted set", async ({ page }) => {
  await open(page, "/network/protection")
  await expect(page.getByRole("button", { name: "Edit China and Russia" })).toBeVisible()
  await expect(page.getByText("China · Russia")).toBeVisible()
  // The last fetch's failure is said on the card, in words.
  await expect(page.getByText("the feed answered 503")).toBeVisible()
  await expect(page.getByRole("button", { name: "Edit SSH" })).toBeVisible()
  await expect(
    page.getByText("22/tcp · 10 new connections a minute per address, bursts of 5 · drop"),
  ).toBeVisible()
  await expect(page.getByRole("meter", { name: "Connection table fullness" })).toHaveAttribute(
    "aria-valuenow",
    "18",
  )
  await expect(page.getByText("your allowlist")).toBeVisible()
  await expect(page.getByRole("button", { name: "Stop trusting 198.51.100.23" })).toBeVisible()
})

test("a list holding the reader's address and an untrusted reader are each said", async ({
  page,
}) => {
  await open(page, "/network/protection", [], {
    ...overrides,
    "/network/protection": {
      ...protection,
      clientTrusted: false,
      blocklists: protection.blocklists.map((l) => (l.id === 2 ? { ...l, containsYou: true } : l)),
    },
  })
  await expect(page.getByText("Spamhaus DROP holds your own address")).toBeVisible()
  await expect(page.getByText("Your own address is not trusted")).toBeVisible()
})

test("staged kernel settings post exactly the settings that changed", async ({ page }) => {
  const mutations: Mutation[] = []
  await open(page, "/network/protection", mutations)
  await page.getByRole("button", { name: /Below recommendation/ }).click()
  // IPv6 redirects: running On, recommended Off.
  await page
    .getByRole("radiogroup", { name: "Accept ICMP redirects (IPv6)" })
    .getByRole("radio", { name: "Off" })
    .click()
  await page.getByLabel("SYN backlog", { exact: true }).fill("4096")
  const bar = page.getByRole("region", { name: "Apply kernel settings" })
  await expect(bar).toContainText("2 unsaved changes")
  await bar.getByRole("button", { name: "Apply" }).click()
  const dialog = page.getByRole("dialog")
  await expect(dialog).toContainText("SYN backlog")
  expect(mutations).toEqual([])
  await dialog.getByRole("button", { name: "Apply" }).click()
  await expect.poll(() => mutations.length).toBe(1)
  expect(mutations[0]).toEqual({
    method: "POST",
    path: "/network/protection/settings",
    body: {
      values: {
        "net.ipv6.conf.all.accept_redirects": "0",
        "net.ipv4.tcp_max_syn_backlog": "4096",
      },
    },
  })
})

test("a number outside its range holds the apply back", async ({ page }) => {
  await open(page, "/network/protection")
  await page.getByLabel("SYN-ACK retries", { exact: true }).fill("9")
  await expect(page.getByText("A whole number from 1 to 5.")).toBeVisible()
  await expect(
    page
      .getByRole("region", { name: "Apply kernel settings" })
      .getByRole("button", { name: "Apply" }),
  ).toBeDisabled()
})

test("a rate limit sends what the form says", async ({ page }) => {
  const mutations: Mutation[] = []
  await open(page, "/network/protection", mutations)
  await page.getByRole("button", { name: "New limit" }).click()
  const dialog = page.getByRole("dialog")
  await dialog.getByLabel("Name").fill("Postgres")
  await dialog.getByLabel("Ports").fill("5432")
  await dialog.getByLabel("New connections").fill("30")
  await dialog.getByLabel("Open at once, per address").fill("20")
  await dialog.getByRole("radio", { name: "Reject" }).click()
  await dialog.getByRole("button", { name: "Make limit" }).click()
  await expect.poll(() => mutations.length).toBe(1)
  expect(mutations[0]).toEqual({
    method: "POST",
    path: "/network/protection/limits",
    body: {
      name: "Postgres",
      protocol: "tcp",
      ports: "5432",
      rate: 30,
      per: "minute",
      burst: 0,
      perSource: true,
      maxConnections: 20,
      globalConnections: 0,
      profile: "",
      action: "reject",
    },
  })
})

/**
 * A card that is given a title but no `verb` draws nothing and names nothing
 * (`ChoiceCard` without a verb is only its children), so every control in every
 * dialog these pages open is asked for its accessible name: none may be empty.
 */
test.describe("every control in every dialog has a name", () => {
  const dialogs: [string, string, (page: Page) => Promise<void>][] = [
    [
      "a new forward",
      "/network/gateway",
      (page) => page.getByRole("button", { name: "Forward a port" }).first().click(),
    ],
    [
      "an existing forward",
      "/network/gateway",
      (page) => page.getByRole("button", { name: "Edit :8080 → 10.0.4.5:80" }).click(),
    ],
    [
      "a new NAT entry",
      "/network/gateway",
      (page) => page.getByRole("button", { name: "Share a network" }).click(),
    ],
    [
      "an existing NAT entry",
      "/network/gateway",
      (page) => page.getByRole("button", { name: "Edit 10.20.0.0/24 out through ens3" }).click(),
    ],
    [
      "a new blocklist",
      "/network/protection",
      (page) => page.getByRole("button", { name: "New blocklist" }).click(),
    ],
    [
      "an existing country list",
      "/network/protection",
      (page) => page.getByRole("button", { name: "Edit China and Russia" }).click(),
    ],
    [
      "an existing manual list",
      "/network/protection",
      (page) => page.getByRole("button", { name: "Edit Scanners I found" }).click(),
    ],
    [
      "a new limit",
      "/network/protection",
      (page) => page.getByRole("button", { name: "New limit" }).click(),
    ],
    [
      "an existing limit",
      "/network/protection",
      (page) => page.getByRole("button", { name: "Edit SSH" }).click(),
    ],
  ]
  for (const [label, path, opener] of dialogs) {
    test(label, async ({ page }) => {
      await open(page, path)
      await opener(page)
      const dialog = page.getByRole("dialog")
      await expect(dialog).toBeVisible()
      // A dialog with controls that did render is the premise of the rest.
      expect(await dialog.getByRole("button").count()).toBeGreaterThan(1)
      for (const role of ["button", "checkbox", "combobox", "switch", "radio", "link"] as const) {
        await expect(dialog.getByRole(role, { name: /^$/ })).toHaveCount(0)
      }
    })
  }

  test("each kind of a new blocklist", async ({ page }) => {
    await open(page, "/network/protection")
    await page.getByRole("button", { name: "New blocklist" }).click()
    const dialog = page.getByRole("dialog")
    for (const kind of [/A feed/, /Addresses I type/, /Countries/]) {
      await dialog.getByRole("button", { name: kind }).click()
      for (const role of ["button", "checkbox", "combobox", "radio"] as const) {
        await expect(dialog.getByRole(role, { name: /^$/ })).toHaveCount(0)
      }
    }
  })
})

/** Answers one POST path with a body, recording the request like the fixture does. */
async function answer(page: Page, path: string, body: unknown, mutations: Mutation[] = []) {
  await page.route(`**/api/v1${path}`, async (route) => {
    if (route.request().method() !== "POST") return route.fallback()
    mutations.push({ method: "POST", path, body: route.request().postDataJSON() })
    return json(route, body)
  })
}

test.describe("installed, measured and previewed", () => {
  test("a forward's row says what is installed and what was measured", async ({ page }) => {
    await open(page, "/network/gateway")
    // Verified from outside is said on the row; a drifted decision is tagged.
    await expect(page.getByText("reached from an external source")).toBeVisible()
    const minecraft = page.getByRole("listitem").filter({ hasText: "Minecraft" })
    await expect(minecraft.getByText("auto decision drifted")).toBeVisible()
    // A forward whose admission rule is missing says so in its title.
    const game = page.getByRole("listitem").filter({ hasText: "Game server" })
    await expect(game.getByText("Admission missing")).toBeVisible()
    await expect(page.getByText("Live counters belong to table generation 41")).toBeVisible()
    // The one-to-one mapping reads as one.
    await expect(page.getByText("Mail host · one-to-one with 203.0.113.25/32")).toBeVisible()
  })

  test("the editor checks the target, shows external evidence and re-decides", async ({ page }) => {
    const mutations: Mutation[] = []
    await mockNetwork(page, mutations, { overrides })
    await answer(
      page,
      "/network/gateway/verify",
      {
        status: "refused",
        detail: "The target refused the connection.",
        target: "10.0.1.9:25565",
        protocol: "tcp",
        checkedAt: new Date().toISOString(),
        basis: "A TCP connection from this server to the target.",
        current: true,
      },
      mutations,
    )
    await page.goto("/network/gateway")
    await page.getByRole("button", { name: "Edit :25565 → 10.0.1.9:25565" }).click()
    const sheet = page.getByRole("dialog")
    await expect(sheet.getByText("The host's networks changed since auto decided")).toBeVisible()
    await sheet.getByRole("button", { name: "Check the target" }).click()
    await expect(sheet.getByText("10.0.1.9:25565 refused the connection")).toBeVisible()
    expect(mutations.at(-1)).toEqual({
      method: "POST",
      path: "/network/gateway/verify",
      body: { forwardId: 2 },
    })
    await expect(sheet.getByRole("link", { name: "Run one from External checks" })).toHaveAttribute(
      "href",
      "/network/external",
    )
    await sheet.getByRole("button", { name: "Decide again" }).click()
    await expect.poll(() => mutations.at(-1)?.path).toBe("/network/gateway/forwards/2")
    expect(mutations.at(-1)?.method).toBe("PUT")
    // The verified forward shows its external source instead.
    await page.keyboard.press("Escape")
    await page.getByRole("button", { name: "Edit :8080 → 10.0.4.5:80" }).click()
    await expect(
      page.getByRole("dialog").getByText("Controlled source A connected to 203.0.113.20:8080"),
    ).toBeVisible()
  })

  test("a new forward's editor previews what saving would do", async ({ page }) => {
    const mutations: Mutation[] = []
    await mockNetwork(page, mutations, { overrides })
    await answer(page, "/network/gateway/preview", forwardPreview, mutations)
    await page.goto("/network/gateway")
    await page.getByRole("button", { name: "Forward a port" }).first().click()
    const sheet = page.getByRole("dialog")
    await sheet.getByLabel("Name").fill("Grafana")
    await sheet.getByLabel("Public port").fill("3000")
    await sheet.getByLabel("Target address").fill("10.0.4.12")
    const impacts = sheet.getByRole("region", { name: "What saving does" })
    await expect(impacts.getByText("caddy listens on 0.0.0.0:3000")).toBeVisible()
    await expect(impacts.getByText("Arriving: Passes every checked layer")).toBeVisible()
    const preview = mutations.find((m) => m.path === "/network/gateway/preview")
    expect(preview?.body).toEqual({
      kind: "forward",
      id: 0,
      forward: {
        name: "Grafana",
        protocol: "both",
        interface: "",
        ports: "3000",
        target: "10.0.4.12",
        targetPort: "",
        sourceNat: "auto",
        sources: [],
      },
    })
    // Previewing changes nothing.
    expect(mutations.filter((m) => m.path !== "/network/gateway/preview")).toEqual([])
  })

  test("a one-to-one NAT entry sends its mode and public side", async ({ page }) => {
    const mutations: Mutation[] = []
    await open(page, "/network/gateway", mutations)
    await page.getByRole("button", { name: "Share a network" }).click()
    const sheet = page.getByRole("dialog")
    await sheet.getByLabel("Name").fill("Mail")
    await sheet.getByLabel("Network").fill("10.0.4.26")
    await sheet.getByLabel("Leaves through").click()
    await page.getByRole("option", { name: "ens3" }).click()
    await sheet.getByRole("button", { name: /One-to-one/ }).click()
    await sheet.getByLabel("Public address or network").fill("203.0.113.26")
    await sheet.getByRole("button", { name: "Share network" }).click()
    await expect
      .poll(() => mutations.filter((m) => m.path === "/network/gateway/nat").length)
      .toBe(1)
    expect(mutations.find((m) => m.path === "/network/gateway/nat")?.body).toEqual({
      name: "Mail",
      source: "10.0.4.26",
      interface: "ens3",
      toAddress: "",
      mode: "one-to-one",
      translated: "203.0.113.26",
      destinations: [],
    })
  })

  test("policy evidence shows each entry's modeled flow and what was not read", async ({
    page,
  }) => {
    await open(page, "/network/gateway")
    await expect(page.getByText("Each entry\u2019s flow, as modeled")).toBeVisible()
    await expect(page.getByText("Arriving: A checked layer may drop it")).toBeVisible()
  })
})

test.describe("protection lifecycle", () => {
  test("lists say their schedule, last change, integrity and staleness", async ({ page }) => {
    await open(page, "/network/protection")
    await expect(page.getByText("+12 −3 at the last change")).toBeVisible()
    const drop = page.getByRole("listitem").filter({ hasText: "Spamhaus DROP" })
    await expect(drop.getByText("signed", { exact: true })).toBeVisible()
    await expect(drop.getByText(/every 6 hours · next fetch/)).toBeVisible()
    const firehol = page.getByRole("listitem").filter({ hasText: "FireHOL level 1" })
    await expect(firehol.getByText(/failing \(3 tries\) · stale/)).toBeVisible()
    await expect(page.getByText(/Covers 412M IPv4 addresses \(9\.59% of IPv4\)/)).toBeVisible()
  })

  test("a list's preview names this server's networks, open sessions and geography", async ({
    page,
  }) => {
    const mutations: Mutation[] = []
    await mockNetwork(page, mutations, { overrides })
    await answer(
      page,
      "/network/protection/preview",
      {
        valid: true,
        networks: 2,
        coverage: {
          ipv4Addresses: 65_792,
          ipv4Share: 0.0000153,
          ipv6Slash48s: 0,
          ipv4Networks: 2,
          ipv6Networks: 0,
        },
        sources: [],
        trustedOverlap: ["100.64.0.0/10 (trusted)"],
        localOverlap: [{ network: "172.17.0.0/16", what: "the network of docker0" }],
        connections: { tracked: 4, sources: 2, sample: ["198.51.100.9"], truncated: false },
        impacts: [
          {
            severity: "warning",
            kind: "local",
            message:
              "172.17.0.0/16 is the network of docker0; traffic from it would be refused before anything answers.",
          },
          {
            severity: "info",
            kind: "connections",
            message:
              "4 connections from 2 of these sources are open now. They continue: a list refuses new connections only.",
          },
        ],
      },
      mutations,
    )
    await page.goto("/network/protection")
    await page.getByRole("button", { name: "New blocklist" }).click()
    const dialog = page.getByRole("dialog")
    await dialog.getByRole("button", { name: /Addresses I type/ }).click()
    await dialog.getByLabel("Addresses and networks").fill("172.17.0.0/16\n198.51.100.0/24")
    await dialog.getByRole("button", { name: "Preview what it blocks" }).click()
    const preview = dialog.getByRole("region", { name: "What saving would block" })
    await expect(preview.getByText("the network of docker0")).toBeVisible()
    await expect(preview.getByText(/4 connections from 2 of these sources/)).toBeVisible()
    await expect(preview.getByText("Keeps passing: 100.64.0.0/10 (trusted).")).toBeVisible()
    expect(mutations.filter((m) => m.path === "/network/protection/blocklists")).toEqual([])
    // A country list explains what its geography is before anything is fetched.
    await dialog.getByRole("button", { name: /Countries/ }).click()
    await dialog.getByRole("searchbox", { name: "Search countries" }).fill("china")
    await dialog.getByRole("checkbox", { name: /China/ }).click()
    await expect(dialog.getByText("Approximate geography.")).toBeVisible()
  })

  test("open sessions from a listed network are counted, then ended on confirmation", async ({
    page,
  }) => {
    const mutations: Mutation[] = []
    await mockNetwork(page, mutations, { overrides })
    await answer(
      page,
      "/network/protection/sessions/preview",
      {
        network: "198.51.100.0/24",
        blocklist: "Scanners I found",
        connections: { tracked: 3, sources: 2, sample: ["198.51.100.9"], truncated: false },
        basis: "Ending a session removes its connection-tracking entry.",
      },
      mutations,
    )
    await page.goto("/network/protection")
    await page.getByRole("button", { name: "Edit China and Russia" }).click()
    const dialog = page.getByRole("dialog")
    await expect(dialog.getByText("Where it came from")).toBeVisible()
    const sessions = dialog.getByRole("region", { name: "Open sessions" })
    await sessions.getByLabel("Network").fill("198.51.100.0/24")
    await expect(sessions.getByRole("button", { name: "End them" })).toBeDisabled()
    await sessions.getByRole("button", { name: "Count sessions" }).click()
    await expect(sessions.getByText(/3 open from 2 addresses/)).toBeVisible()
    await sessions.getByRole("button", { name: "End them" }).click()
    await page
      .getByRole("dialog")
      .filter({ hasText: "End the open sessions" })
      .getByRole("button", { name: "End sessions" })
      .click()
    await expect
      .poll(() => mutations.find((m) => m.path === "/network/protection/sessions/revoke")?.body)
      .toEqual({ network: "198.51.100.0/24", blocklistId: 1 })
  })

  test("exceptions list what they let through and a new one says why and until when", async ({
    page,
  }) => {
    const mutations: Mutation[] = []
    await open(page, "/network/protection", mutations)
    await expect(page.getByText("120 let through")).toBeVisible()
    await expect(page.getByText(/Partner monitoring · FireHOL level 1 · ends/)).toBeVisible()
    await page.getByRole("button", { name: "New exception" }).click()
    const dialog = page.getByRole("dialog")
    await dialog.getByLabel("Address or network").fill("203.0.113.40")
    await dialog.getByLabel("Why").fill("Vendor support session")
    await dialog.getByRole("button", { name: "Make exception" }).click()
    await expect.poll(() => mutations.length).toBe(1)
    const body = mutations[0].body as Record<string, string>
    expect(mutations[0].path).toBe("/network/protection/exceptions")
    expect({ ...body, expiresAt: undefined }).toEqual({
      address: "203.0.113.40",
      scope: "all",
      reason: "Vendor support session",
      expiresAt: undefined,
    })
    // A day from now, to the second, in UTC.
    expect(body.expiresAt).toMatch(/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}Z$/)
    const hours = (Date.parse(body.expiresAt) - Date.now()) / 3_600_000
    expect(hours).toBeGreaterThan(23.9)
    expect(hours).toBeLessThan(24.1)
  })

  test("a stale kept address is said, and confirming it records a review", async ({ page }) => {
    const mutations: Mutation[] = []
    await open(page, "/network/protection", mutations)
    await expect(page.getByText("1 kept address is stale")).toBeVisible()
    await expect(
      page.getByText(/Old office · kept by operator · no sign-in seen in 90 days/),
    ).toBeVisible()
    await page.getByRole("button", { name: "Confirm 198.51.100.23 is still needed" }).click()
    await expect.poll(() => mutations.length).toBe(1)
    expect(mutations[0]).toEqual({
      method: "PUT",
      path: "/network/protection/trusted",
      body: { address: "198.51.100.23", reason: "Old office", expiresAt: "", confirm: true },
    })
  })

  test("a service profile fills a new limit and a limit shows what is open now", async ({
    page,
  }) => {
    const mutations: Mutation[] = []
    await open(page, "/network/protection", mutations)
    await page.getByRole("button", { name: "New limit" }).click()
    let dialog = page.getByRole("dialog")
    await dialog.getByRole("button", { name: /Exposed database/ }).click()
    await expect(dialog.getByLabel("Name")).toHaveValue("Exposed database")
    await expect(dialog.getByLabel("Open at once, everyone")).toHaveValue("200")
    await dialog.getByRole("button", { name: "Make limit" }).click()
    await expect.poll(() => mutations.length).toBe(1)
    expect(mutations[0].body).toEqual({
      name: "Exposed database",
      protocol: "tcp",
      ports: "5432",
      rate: 30,
      per: "minute",
      burst: 10,
      perSource: true,
      maxConnections: 20,
      globalConnections: 200,
      profile: "database",
      action: "reject",
    })
    await page.getByRole("button", { name: "Edit Website" }).click()
    dialog = page.getByRole("dialog")
    await expect(dialog.getByRole("region", { name: "Open now" })).toContainText(
      "4,020 connections to 443 of 5,000 allowed for everyone",
    )
    await expect(page.getByText(/tracking 41 sources/)).toBeVisible()
  })

  test("the connection table panel says what points at a cause", async ({ page }) => {
    await open(page, "/network/protection")
    const panel = page.getByLabel("Connection table pressure")
    await expect(panel.getByText(/Half-open TCP connections are a large share/)).toBeVisible()
    await expect(panel.getByText("198.51.100.77 holds 3600 entries (31%).")).toBeVisible()
    await expect(panel.getByRole("region", { name: "By state" })).toContainText("tcp SYN_RECV")
    await expect(panel.getByText(/Since boot the kernel dropped 12 new connections/)).toBeVisible()
  })

  test("a read-only reader never asks for the connection table", async ({ page }) => {
    let asked = false
    await mockNetwork(page, [], {
      overrides,
      session: { ...admin, capabilities: ["read"] },
    })
    await page.route("**/api/v1/network/protection/pressure", (route) => {
      asked = true
      return json(route, pressure)
    })
    await page.goto("/network/protection")
    await expect(page.getByRole("button", { name: "Edit SSH" })).toBeVisible()
    expect(asked).toBe(false)
    await expect(page.getByLabel("Connection table pressure")).toHaveCount(0)
  })

  test("a workload profile stages its values and per-interface values are shown", async ({
    page,
  }) => {
    await open(page, "/network/protection")
    await page.getByRole("combobox", { name: "Stage a workload profile" }).click()
    await page.getByRole("option", { name: "VPN or container gateway" }).click()
    const bar = page.getByRole("region", { name: "Apply kernel settings" })
    // Of its four values two differ from what runs: the table size and the backlog,
    // and new-interface filtering (running Off).
    await expect(bar).toContainText("3 unsaved changes")
    await page.getByRole("button", { name: /Per-interface values/ }).click()
    await expect(page.getByText(/device Strict, all Off/)).toBeVisible()
  })
})
