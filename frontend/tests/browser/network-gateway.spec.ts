import { expect, test, type Page } from "@playwright/test"
import { json, mockNetwork, type Mutation } from "./network-fixture"
import { gateway, overrides, protection } from "./network-gateway-fixture"

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
          await page.waitForLoadState("networkidle")
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
  await expect.poll(() => mutations.length).toBe(1)
  expect(mutations[0]).toEqual({
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
  await expect.poll(() => mutations.length).toBe(3)
  expect(mutations.map((m) => `${m.method} ${m.path}`)).toEqual([
    "POST /network/gateway/forwards",
    "POST /network/forwarding/ipv4/on",
    "POST /network/gateway/forwards",
  ])
  expect(mutations[2].body).toEqual(mutations[0].body)
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
  await expect.poll(() => mutations.length).toBe(1)
  expect(mutations[0]).toEqual({
    method: "POST",
    path: "/network/gateway/nat",
    body: { name: "Office VLAN", source: "10.30.0.0/24", interface: "ens3", toAddress: "" },
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
  await dialog.getByLabel("Open at once").fill("20")
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
