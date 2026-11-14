import { expect as baseExpect, test, type Page } from "@playwright/test"
import { admin, json, loaded, mockNetwork, type Mutation } from "./network-fixture"
import {
  dnsView,
  dnsViewManaged,
  ebpfMissing,
  ens3Profile,
  mockNetworkWrites,
  networkManagerOwner,
  overrides,
  processTrafficWarming,
  rolledBack,
  type Refusal,
} from "./network-dns-fixture"
import { risen } from "./risen"

/**
 * The DNS and Traffic pages against a mocked API (`network-dns-fixture.ts`):
 * each page draws what `netx` reports, the forms send what the routes take —
 * the servers with their TLS names, the limits in kilobits, the whole list of
 * host records — and a refusal is drawn where the reader is looking.
 */

// The Traffic page redraws a dozen charts every two seconds, and a dev server
// on a busy machine takes seconds to get round to a section below them.
const expect = baseExpect.configure({ timeout: 20_000 })

type Setup = {
  session?: typeof admin
  /** Keep the two-second ring the charts draw; without it they stay empty and the page stays light. */
  charts?: boolean
  overrides?: Record<string, unknown>
  refuse?: Refusal
  forwardingRefusal?: boolean
}

async function open(page: Page, path: string, setup: Setup = {}) {
  const mutations: Mutation[] = []
  // Eleven charts redrawn every two seconds are most of what this page costs a
  // browser, and none of the checks but the first are about them.
  const quiet = setup.charts ? {} : { "/network/traffic/live": { now: 0, series: {} } }
  await mockNetwork(page, mutations, {
    overrides: { ...overrides, ...quiet, ...setup.overrides },
    session: setup.session,
  })
  await mockNetworkWrites(page, mutations, {
    refuse: setup.refuse,
    forwardingRefusal: setup.forwardingRefusal,
  })
  await page.goto(path)
  return mutations
}

/**
 * Every button on the page has a name a screen reader can say. A `ChoiceCard`
 * drawn without its `verb` renders only its children, so a card given a title
 * and a description came out as an empty button; this finds that, and any
 * icon-only control with no label, wherever it appears.
 */
async function expectNamedButtons(page: Page, scope = "body") {
  const unnamed = await page.locator(scope).evaluate((root) =>
    [...root.querySelectorAll("button, [role=button], a[href]")]
      .filter((el) => (el as HTMLElement).offsetParent !== null)
      .filter((el) => {
        const labelled = el.getAttribute("aria-labelledby")
        const byId = labelled
          ? labelled
              .split(" ")
              .map((id) => document.getElementById(id)?.textContent ?? "")
              .join("")
          : ""
        const name = (
          el.getAttribute("aria-label") ||
          byId ||
          el.textContent ||
          el.getAttribute("title") ||
          ""
        ).trim()
        return name === ""
      })
      .map((el) => el.outerHTML.slice(0, 120)),
  )
  expect(unnamed).toEqual([])
}

const readOnly = { ...admin, capabilities: ["read"], user: { ...admin.user, role: "readonly" } }

/** The Save on the page's floating bar, which every form on the settings view shares. */
const saveBar = (page: Page) => page.getByRole("region", { name: "Save changes" })
const save = (page: Page) => saveBar(page).getByRole("button", { name: /^Save/ })

/** Where a block stands, once the page has come to rest. */
async function boxOf(page: Page, selector: string) {
  await risen(page)
  const box = await page.locator(selector).first().boundingBox()
  if (!box) throw new Error(`${selector} is not drawn`)
  return { y: Math.round(box.y), height: Math.round(box.height) }
}

/** Holds the resolver's read until released, so the page can be measured while it waits. */
async function holdResolver(page: Page) {
  let release = () => {}
  const held = new Promise<void>((resolve) => (release = resolve))
  await page.route("**/api/v1/network/dns/", async (route) => {
    await held
    return route.fallback()
  })
  return release
}

test.describe("DNS", () => {
  test("opens on the resolver's identity line and the chain, with no tile row", async ({
    page,
  }) => {
    await open(page, "/network/dns")
    const identity = page.getByRole("region", { name: "About the resolver" })
    await expect(identity).toContainText("systemd-resolved")
    await expect(identity).toContainText("Answering")
    await expect(identity).toContainText("127.0.0.53")
    await expect(identity).toContainText("Cloudflare over opportunistic TLS")
    await expect(identity).toContainText("78% from cache")
    await expect(identity.getByRole("button", { name: "Resolve a name" })).toBeVisible()
    await expect(page.locator("[data-slot=stat-grid]")).toHaveCount(0)

    // The cache share and the lookups are on the resolver's node.
    const chain = page.getByRole("region", { name: "Resolver chain" })
    await expect(chain.locator("[data-slot=wire-flow]")).toBeVisible()
    await expect(chain.locator("[data-slot=dns-resolver-readings]")).toHaveText(
      "78% from cache9,631 lookups",
    )
    await expect(chain.getByText("MagicDNS")).toBeVisible()
    await expect(chain.getByText("in use").first()).toBeVisible()
    await expect(
      chain.getByRole("region", { name: "Upstream servers" }).getByRole("listitem"),
    ).toHaveCount(4)

    // What the other two tiles said is two readings in words.
    await expect(page.getByText("11 failed, 3 timed out of 9,631")).toBeVisible()
    await expect(
      page.getByText("4,214 from cache · 1,188 upstream · 214 held · 2 in flight"),
    ).toBeVisible()
    await expect(page.getByText("312 secure, none bogus")).toBeVisible()
    await expect(
      page.getByText("4,871 unsigned · 4 indeterminate · set to allow-downgrade"),
    ).toBeVisible()

    const port53 = page.getByRole("region", { name: "Answering on port 53" })
    await expect(port53.getByText("AdGuard Home")).toBeVisible()
    await expect(port53.getByText("systemd-resolved").first()).toBeVisible()
  })

  test("the views are tabs named in the address, and a reload opens the same one", async ({
    page,
  }) => {
    await open(page, "/network/dns")
    const tabs = page.getByRole("tablist", { name: "Views of DNS" })
    await expect(tabs.getByRole("tab")).toHaveText([
      "Resolver",
      "Lookups",
      "Settings",
      "Services",
      "Log",
    ])
    await expect(tabs.getByRole("tab", { name: "Resolver" })).toHaveAttribute(
      "aria-selected",
      "true",
    )
    await tabs.getByRole("tab", { name: "Settings" }).click()
    await expect(page).toHaveURL(/\/network\/dns\?tab=settings$/)
    await expect(page.getByRole("heading", { name: "Upstreams" })).toBeVisible()
    await page.reload()
    await expect(page.getByRole("heading", { name: "Upstreams" })).toBeVisible()
    await tabs.getByRole("tab", { name: "Log" }).click()
    await expect(page).toHaveURL(/\?tab=log$/)
    await expect(
      page
        .getByRole("combobox", { name: "DNS log" })
        .or(page.getByText("systemd-resolved.service"))
        .first(),
    ).toBeVisible()
    await tabs.getByRole("tab", { name: "Resolver" }).click()
    await expect(page).toHaveURL(/\/network\/dns$/)
  })

  test("the addresses other pages use still land on their section", async ({ page }) => {
    // The ad-blocking choice's anchor: the upstreams are in the settings now.
    await open(page, "/network/dns#dns-upstreams")
    await expect(page.getByRole("tab", { name: "Settings" })).toHaveAttribute(
      "aria-selected",
      "true",
    )
    await expect(page.locator("#dns-upstreams")).toBeInViewport()
    // The resolver owner's hand-off, written by the server: per-link DNS is on the first view.
    await page.goto("/network/dns#dns-links")
    await expect(page.getByRole("tab", { name: "Resolver" })).toHaveAttribute(
      "aria-selected",
      "true",
    )
    await expect(page.locator("#dns-links")).toBeInViewport()
    // A tab nobody has is the resolver, not an empty page.
    await page.goto("/network/dns?tab=upstreams")
    await expect(page.getByRole("region", { name: "Resolver chain" })).toBeVisible()
  })

  for (const width of [1280, 390]) {
    test(`the page holds its shape while it loads at ${width}`, async ({ page }) => {
      await page.setViewportSize({ width, height: 900 })
      // A first visit, so the browser has seen how many nodes each block draws.
      await open(page, "/network/dns")
      await expect(page.getByText("MagicDNS")).toBeVisible()
      await loaded(page)
      const blocks = [
        "[data-slot=host-identity]",
        "[role=tablist]",
        "[data-slot=dns-chain]",
        "[data-slot=reading-lines]",
        "[aria-label='Answering on port 53']",
        "#dns-links",
        "#dns-links [data-slot=choice-list], #dns-links ul",
      ]
      const release = await holdResolver(page)
      await page.reload()
      await expect(page.locator("[data-slot=dns-chain][data-loading]")).toBeVisible()
      await expect(page.getByRole("button", { name: "Resolve a name" })).toBeVisible()
      const waiting = []
      for (const block of blocks) waiting.push(await boxOf(page, block))
      release()
      await expect(page.getByText("MagicDNS")).toBeVisible()
      await loaded(page)
      for (const [index, block] of blocks.entries()) {
        expect(await boxOf(page, block), block).toEqual(waiting[index])
      }
    })

    test(`cards with and without optional data are one height at ${width}`, async ({ page }) => {
      await page.setViewportSize({ width, height: 900 })
      await open(page, "/network/dns")
      // ens3 has servers alone; tailscale0 has routing domains as well.
      const links = page.locator("#dns-links [data-slot=choice-row]")
      await expect(links).toHaveCount(2)
      await risen(page)
      const [bare, routed] = await Promise.all([
        links.nth(0).boundingBox(),
        links.nth(1).boundingBox(),
      ])
      expect(bare!.height).toBe(routed!.height)
      expect(routed!.y - bare!.y).toBe(bare!.height + 8)
      // A listener that is an ad-blocker and one that is not stand on the same lines.
      const nodes = page.getByRole("region", { name: "Answering on port 53" }).getByRole("listitem")
      await expect(nodes).toHaveCount(3)
      const heights = await nodes.evaluateAll((items) =>
        items.map((item) => Math.round(item.getBoundingClientRect().height)),
      )
      expect(new Set(heights).size).toBe(1)
      // Every upstream is four lines, whether or not a provider names it.
      const upstreams = await page
        .getByRole("region", { name: "Upstream servers" })
        .getByRole("listitem")
        .evaluateAll((items) =>
          items.map((item) => Math.round(item.getBoundingClientRect().height)),
        )
      expect(new Set(upstreams).size).toBe(1)
      expect(
        await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth),
      ).toBe(true)
    })
  }

  test("a failed first read says so under the identity line, and the lookups still work", async ({
    page,
  }) => {
    await mockNetwork(page, [], { overrides })
    await mockNetworkWrites(page, [])
    await page.route("**/api/v1/network/dns/", (route) =>
      json(route, { error: { code: "internal", message: "resolvectl did not answer" } }, 500),
    )
    await page.goto("/network/dns")
    await expect(page.getByRole("region", { name: "About the resolver" })).toContainText("Not read")
    await expect(page.getByText("Resolvectl did not answer")).toBeVisible()
    await page.getByRole("button", { name: "Resolve a name" }).click()
    await page.getByRole("textbox", { name: "Name", exact: true }).fill("example.com")
    await page.getByRole("button", { name: "Resolve", exact: true }).click()
    await expect(page.getByRole("region", { name: "Answers for example.com" })).toBeVisible()
  })

  test("Resolve a name opens the lookups with the caret in the name", async ({ page }) => {
    await open(page, "/network/dns")
    await page.getByRole("button", { name: "Resolve a name" }).click()
    await expect(page).toHaveURL(/tab=lookups/)
    await expect(page.getByRole("textbox", { name: "Name", exact: true })).toBeFocused()
  })

  test("picking Quad9 with DNS over TLS required posts the servers with their TLS name", async ({
    page,
  }) => {
    const mutations = await open(page, "/network/dns?tab=settings")
    await page.getByRole("button", { name: /^Quad9/ }).click()
    await page
      .getByRole("radiogroup", { name: "DNS over TLS" })
      .getByRole("radio", { name: "Required" })
      .click()
    await expect(saveBar(page)).toContainText("2 unsaved changes")
    await expect(saveBar(page)).toContainText("Resolver · Applies immediately")
    await save(page).click()
    await page.getByRole("dialog").getByRole("button", { name: "Apply" }).click()
    await expect(page.getByText("resolved in")).toBeVisible()
    await expect(saveBar(page)).toHaveCount(0)
    const post = mutations.find((m) => m.method === "POST" && m.path === "/network/dns/")
    expect(post?.body).toMatchObject({
      servers: [
        "9.9.9.9#dns.quad9.net",
        "149.112.112.112#dns.quad9.net",
        "2620:fe::fe#dns.quad9.net",
        "2620:fe::9#dns.quad9.net",
      ],
      dnsOverTLS: "yes",
    })
  })

  test("DNSSEC and the cache are segments that post resolved's own words", async ({ page }) => {
    const mutations = await open(page, "/network/dns?tab=settings")
    await page
      .getByRole("radiogroup", { name: "DNSSEC" })
      .getByRole("radio", { name: "Always" })
      .click()
    await expect(page.getByText("Validated; an answer that fails is refused.")).toBeVisible()
    await page
      .getByRole("radiogroup", { name: "Cache mode" })
      .getByRole("radio", { name: "Positive only" })
      .click()
    await save(page).click()
    await page.getByRole("dialog").getByRole("button", { name: "Apply" }).click()
    await expect(page.getByText("resolved in")).toBeVisible()
    expect(
      mutations.find((m) => m.method === "POST" && m.path === "/network/dns/")?.body,
    ).toMatchObject({ dnssec: "yes", cache: "no-negative", dnsOverTLS: "opportunistic" })
  })

  test("required TLS is refused before it is asked while a server has no name", async ({
    page,
  }) => {
    const mutations = await open(page, "/network/dns?tab=settings")
    await page
      .getByRole("textbox", { name: "Servers", exact: true })
      .fill("9.9.9.9\n149.112.112.112#dns.quad9.net")
    await page
      .getByRole("radiogroup", { name: "DNS over TLS" })
      .getByRole("radio", { name: "Required" })
      .click()
    await expect(page.getByText("needs a name on every server: 9.9.9.9 has none")).toBeVisible()
    await expect(save(page)).toBeDisabled()
    expect(mutations).toEqual([])
  })

  test("closing the confirmation saves nothing and keeps the draft", async ({ page }) => {
    const mutations = await open(page, "/network/dns?tab=settings")
    await page.getByRole("button", { name: /^Quad9/ }).click()
    await save(page).click()
    await page.getByRole("dialog").getByRole("button", { name: "Cancel" }).click()
    await expect(page.getByRole("dialog")).toHaveCount(0)
    await expect(saveBar(page)).toContainText("1 unsaved change")
    expect(mutations.map((m) => m.path)).toEqual(["/network/dns/verification-plan"])
    // Discard puts the servers back.
    await saveBar(page).getByRole("button", { name: "Discard" }).click()
    await expect(page.getByRole("textbox", { name: "Servers", exact: true })).toHaveValue(
      "1.1.1.1#cloudflare-dns.com\n1.0.0.1#cloudflare-dns.com",
    )
    await expect(saveBar(page)).toHaveCount(0)
  })

  test("a draft outlives a look at another view", async ({ page }) => {
    await open(page, "/network/dns?tab=settings")
    await page.getByRole("button", { name: /^Quad9/ }).click()
    await page.getByRole("tab", { name: "Resolver" }).click()
    await expect(page.getByRole("region", { name: "Resolver chain" })).toBeVisible()
    await expect(saveBar(page)).toContainText("1 unsaved change")
    await page.getByRole("tab", { name: "Settings" }).click()
    await expect(page.getByRole("textbox", { name: "Servers", exact: true })).toHaveValue(
      /9\.9\.9\.9#dns\.quad9\.net/,
    )
  })

  test("resetting to the system's goes through a confirmation and a DELETE", async ({ page }) => {
    const mutations = await open(page, "/network/dns?tab=settings", {
      overrides: { "/network/dns/": dnsViewManaged },
    })
    await page.getByRole("button", { name: /Reset to the system/ }).click()
    await page.getByRole("dialog").getByRole("button", { name: "Reset" }).click()
    await expect(page.getByText("resolved in")).toBeVisible()
    expect(mutations.some((m) => m.method === "DELETE" && m.path === "/network/dns/")).toBe(true)
  })

  test("a host where systemd-resolved is not running reads, and says why", async ({ page }) => {
    await open(page, "/network/dns?tab=settings", {
      overrides: {
        "/network/dns/": {
          ...dnsView,
          resolvConf: { ...dnsView.resolvConf, mode: "static", managedBy: "netplan" },
          resolved: { ...dnsView.resolved, active: false, statistics: undefined },
        },
      },
    })
    await expect(page.getByText("These settings are read here, not written")).toBeVisible()
    await expect(page.getByRole("textbox", { name: "Servers", exact: true })).toBeDisabled()
    await expect(page.getByRole("button", { name: /^Quad9/ })).toHaveCount(0)
    // The identity line says what programs ask instead of naming a resolver that is not answering.
    const identity = page.getByRole("region", { name: "About the resolver" })
    await expect(identity).toContainText("/etc/resolv.conf")
    await expect(identity).toContainText("systemd-resolved is not running")
    // The host's own records are another file, and stay editable.
    await expect(page.getByRole("button", { name: "Add record" })).toBeEnabled()
  })

  test("an administrator without the destructive capability is offered no resolver change", async ({
    page,
  }) => {
    const mutations = await open(page, "/network/dns?tab=settings", {
      overrides: { "/network/dns/": dnsViewManaged },
      session: {
        ...admin,
        capabilities: admin.capabilities.filter((capability) => capability !== "destructive"),
      },
    })
    await expect(
      page.getByText("An administrator with destructive permission can change the host resolver."),
    ).toBeVisible()
    await expect(page.getByRole("button", { name: /Reset to the system/ })).toHaveCount(0)
    await expect(page.getByRole("textbox", { name: "Servers", exact: true })).toBeDisabled()
    // The host's records are not a destructive route.
    await expect(page.getByRole("button", { name: "Add record" })).toBeEnabled()
    expect(mutations).toEqual([])
  })

  test("a read-only account sees the settings and the page, and no control it cannot use", async ({
    page,
  }) => {
    const requests: string[] = []
    page.on("request", (request) => {
      if (/\/dns\/(services|evidence)/.test(request.url())) requests.push(request.url())
    })
    const mutations = await open(page, "/network/dns?tab=settings", { session: readOnly })
    await expect(page.getByText(/Read only: changing the resolver/)).toBeVisible()
    await expect(page.getByRole("textbox", { name: "Servers", exact: true })).toBeDisabled()
    for (const name of [/^Quad9/, "Add record", /Remove record/, /Reset to the system/]) {
      await expect(page.getByRole("button", { name })).toHaveCount(0)
    }
    await expect(page.getByRole("switch")).toHaveCount(0)
    await expect(page.getByRole("textbox", { name: "Verification names" })).toHaveCount(0)
    await expect(saveBar(page)).toHaveCount(0)
    // It may still ask the host what its records resolve to.
    await expect(page.getByRole("button", { name: "Check local resolution" })).toBeEnabled()

    await page.getByRole("tab", { name: "Resolver" }).click()
    await expect(page.locator("#dns-links [data-slot=choice-row]")).toHaveCount(2)
    await expect(page.getByRole("button", { name: /Edit split DNS/ })).toHaveCount(0)
    await expect(page.getByRole("button", { name: "Check certificates" })).toBeEnabled()

    await page.getByRole("tab", { name: "Lookups" }).click()
    await expect(page.getByRole("textbox", { name: "Name", exact: true })).toBeVisible()
    await expect(page.getByRole("radiogroup", { name: "Lookup" })).toHaveCount(0)

    await page.getByRole("tab", { name: "Services" }).click()
    await expect(page.getByText("AdGuard Home")).toBeVisible()
    await expect(page.getByRole("button", { name: /Connect|Inspect|Refresh/ })).toHaveCount(0)
    await expect(page.getByRole("link", { name: /web page on port 3000/ })).toBeVisible()

    await page.getByRole("button", { name: "DNS actions" }).click()
    await expect(page.getByRole("menuitem")).toHaveText(["Refresh"])
    expect(requests).toEqual([])
    expect(mutations).toEqual([])
  })

  test("with no ad-blocker the services offer the two real ways to get one", async ({ page }) => {
    await open(page, "/network/dns?tab=services", {
      overrides: { "/network/dns/": dnsViewManaged },
    })
    await expect(page.getByRole("link", { name: "Deploy a DNS filter" })).toHaveAttribute(
      "href",
      "/deploy/new",
    )
    await page.getByRole("button", { name: "Show the ad-blocking resolvers" }).click()
    await expect(page).toHaveURL(/tab=settings#dns-upstreams$/)
    await expect(page.getByRole("button", { name: /^AdGuard DNS/ })).toBeInViewport()
  })

  test("the services name a detected filter and, with no engine, offer one Connect", async ({
    page,
  }) => {
    await open(page, "/network/dns?tab=services")
    // The ad-blocker's own page is a link on the address this browser used.
    await expect(page.getByRole("link", { name: /web page on port 3000/ })).toHaveAttribute(
      "href",
      "http://127.0.0.1:3000",
    )
    await expect(page.getByText("Answering on port 53")).toBeVisible()
    await expect(page.getByText("No DNS engine is connected")).toBeVisible()
    await expect(page.getByRole("button", { name: "Connect engine", exact: true })).toHaveCount(1)
    await page.getByRole("button", { name: "Connect engine", exact: true }).click()
    await expect(
      page.getByRole("dialog").getByRole("heading", { name: "Connect native DNS engine" }),
    ).toBeVisible()
  })

  test("the page's menu opens the engine and owned-setup sheets from any view", async ({
    page,
  }) => {
    await open(page, "/network/dns")
    await page.getByRole("button", { name: "DNS actions" }).click()
    await page.getByRole("menuitem", { name: "Review an owned setup" }).click()
    await expect(page).toHaveURL(/tab=services$/)
    await expect(
      page.getByRole("dialog").getByRole("heading", { name: "Review owned DNS setup" }),
    ).toBeVisible()
    await page.keyboard.press("Escape")
    await page.getByRole("button", { name: "DNS actions" }).click()
    await page.getByRole("menuitem", { name: "Connect a DNS engine" }).click()
    await expect(
      page.getByRole("dialog").getByRole("heading", { name: "Connect native DNS engine" }),
    ).toBeVisible()
  })

  test("the lookup has one row per resolver, the failed one with its reason, and no bar", async ({
    page,
  }) => {
    const mutations = await open(page, "/network/dns?tab=lookups")
    await page.getByRole("textbox", { name: "Name", exact: true }).fill("example.com")
    await page.getByRole("button", { name: "Resolve", exact: true }).click()
    const race = page.getByRole("region", { name: "Answers for example.com" })
    await expect(race.getByRole("listitem")).toHaveCount(1)
    await page.getByRole("switch", { name: "Compare named resolvers" }).check()
    const resolve = page.getByRole("button", { name: "Resolve", exact: true })
    await expect(resolve).toBeDisabled()
    const destinations = page.getByRole("group", { name: "Destinations" })
    for (const checkbox of await destinations.getByRole("checkbox").all()) await checkbox.check()
    await expect(resolve).toBeDisabled()
    await page.getByRole("checkbox", { name: "Acknowledge private-name disclosure" }).check()
    await resolve.click()
    await expect(race.getByRole("listitem")).toHaveCount(7)
    await expect(race.getByText("no answer within 2 s")).toBeVisible()
    await expect(race.getByText("6 of 7 answered")).toBeVisible()
    await expect(race.getByRole("img")).toHaveCount(0)
    expect(mutations.filter((m) => m.path === "/network/dns/lookup").at(-1)?.body).toEqual({
      name: "example.com",
      type: "A",
      mode: "compare",
      destinations: [
        "127.0.0.53",
        "1.1.1.1",
        "8.8.8.8",
        "9.9.9.9",
        "94.140.14.14",
        "198.51.100.2",
        "194.242.2.3",
      ],
      acknowledgeDisclosure: true,
    })
  })

  test("a disclosure acknowledged for one name does not carry to the next", async ({ page }) => {
    await open(page, "/network/dns?tab=lookups")
    const name = page.getByRole("textbox", { name: "Name", exact: true })
    await name.fill("example.com")
    await page.getByRole("button", { name: "Resolve", exact: true }).click()
    await expect(page.getByRole("region", { name: "Answers for example.com" })).toBeVisible()
    await page.getByRole("switch", { name: "Compare named resolvers" }).check()
    await page.getByRole("checkbox", { name: "Cloudflare 1.1.1.1" }).check()
    const acknowledged = page.getByRole("checkbox", { name: "Acknowledge private-name disclosure" })
    await acknowledged.check()
    await expect(page.getByRole("button", { name: "Resolve", exact: true })).toBeEnabled()
    await name.fill("nas.corp.example")
    await expect(acknowledged).not.toBeChecked()
    await expect(page.getByRole("button", { name: "Resolve", exact: true })).toBeDisabled()
  })

  test("the three lookups share the name, and each keeps its answer", async ({ page }) => {
    const mutations = await open(page, "/network/dns?tab=lookups")
    const tools = page.getByRole("radiogroup", { name: "Lookup" })
    await page.getByRole("textbox", { name: "Name", exact: true }).fill("www.example.com")
    await page.getByRole("button", { name: "Resolve", exact: true }).click()
    await expect(page.getByRole("region", { name: "Answers for www.example.com" })).toBeVisible()
    await tools.getByRole("radio", { name: "DNSSEC chain" }).click()
    await expect(page).toHaveURL(/tab=lookups&tool=dnssec$/)
    await expect(page.getByRole("textbox", { name: "Name", exact: true })).toHaveValue(
      "www.example.com",
    )
    await page.getByRole("button", { name: "Trace", exact: true }).click()
    const chain = page.getByRole("region", { name: "Chain of trust for www.example.com" })
    await expect(chain.getByText("Secure to the root")).toBeVisible()
    await expect(chain.getByText("DS digest recomputed and matched").first()).toBeVisible()
    await expect(chain.getByText("Matches an IANA root anchor")).toBeVisible()
    expect(mutations.find((m) => m.path === "/network/dns/dnssec-chain")?.body).toEqual({
      name: "www.example.com",
    })
    await tools.getByRole("radio", { name: "Resolve" }).click()
    await expect(page.getByRole("region", { name: "Answers for www.example.com" })).toBeVisible()
    await page.reload()
    await expect(page.getByRole("button", { name: "Resolve", exact: true })).toBeVisible()
    await page.goto("/network/dns?tab=lookups&tool=dnssec")
    await expect(page.getByRole("button", { name: "Trace", exact: true })).toBeVisible()
  })

  test("saving host records sends the whole list, the new row included", async ({ page }) => {
    const mutations = await open(page, "/network/dns?tab=settings")
    await page.getByRole("button", { name: "Add record" }).click()
    await page.getByLabel("Address of record 3").fill("192.0.2.44")
    await page.getByLabel("Names of record 3").fill("printer.lan printer")
    await expect(saveBar(page)).toContainText("Host records · Applies immediately")
    await save(page).click()
    await expect.poll(() => mutations.find((m) => m.method === "PUT")).toBeTruthy()
    expect(mutations.find((m) => m.method === "PUT")?.body).toEqual({
      records: [
        { address: "192.0.2.10", names: ["nas.lan", "nas"] },
        { address: "10.0.4.20", names: ["grafana.lan"] },
        { address: "192.0.2.44", names: ["printer.lan", "printer"] },
      ],
    })
    // Saved, the host is asked what each name resolves to.
    await expect(page.getByRole("region", { name: "Local resolution" })).toBeVisible()
    // The file's own lines are shown and cannot be edited.
    await expect(page.getByRole("cell", { name: "localhost", exact: true })).toBeVisible()
  })

  test("one Save writes the resolver and the host records, the resolver first", async ({
    page,
  }) => {
    const mutations = await open(page, "/network/dns?tab=settings")
    await page.getByRole("button", { name: /^Quad9/ }).click()
    await page.getByRole("button", { name: "Add record" }).click()
    await page.getByLabel("Address of record 3").fill("192.0.2.44")
    await page.getByLabel("Names of record 3").fill("printer.lan")
    await expect(saveBar(page)).toContainText("2 unsaved changes")
    await expect(saveBar(page)).toContainText("Resolver and Host records")
    await save(page).click()
    await page.getByRole("dialog").getByRole("button", { name: "Apply" }).click()
    await expect.poll(() => mutations.find((m) => m.method === "PUT")).toBeTruthy()
    expect(mutations.filter((m) => m.method !== "GET").map((m) => `${m.method} ${m.path}`)).toEqual(
      [
        "POST /network/dns/verification-plan",
        "POST /network/dns/",
        "POST /network/dns/hosts/preview",
        "PUT /network/dns/hosts",
      ],
    )
  })

  test("a half-written host record says which field is missing", async ({ page }) => {
    const mutations = await open(page, "/network/dns?tab=settings")
    await page.getByRole("button", { name: "Add record" }).click()
    await page.getByLabel("Address of record 3").fill("192.0.2.44")
    await save(page).click()
    await expect(page.getByText("Give it a name.")).toBeVisible()
    expect(mutations).toEqual([])
  })

  test("names the resolver owner, what programs ask and where DNS is changed", async ({ page }) => {
    await open(page, "/network/dns")
    const owner = page.getByRole("definition").filter({ hasText: "systemd-resolved" }).first()
    await expect(owner).toBeVisible()
    await expect(page.getByText("Reaches what programs ask")).toBeVisible()
    await expect(page.getByRole("list", { name: "Owner evidence" })).toContainText(
      "points at ../run/systemd/resolve/stub-resolv.conf",
    )
    await expect(page.getByRole("link", { name: "Per-link DNS" })).toHaveAttribute(
      "href",
      "/network/dns#dns-links",
    )
  })

  test("a NetworkManager-owned file says the drop-in does not reach programs and hands off", async ({
    page,
  }) => {
    await open(page, "/network/dns", {
      overrides: { "/network/dns/": { ...dnsView, owner: networkManagerOwner } },
    })
    await expect(page.getByText("Does not reach what programs ask")).toBeVisible()
    await expect(page.getByText("The sources disagree")).toBeVisible()
    await expect(
      page.getByText(/programs ask 198\.51\.100\.53 because NetworkManager/),
    ).toBeVisible()
    await expect(page.getByRole("link", { name: "Open the interfaces" })).toHaveAttribute(
      "href",
      "/network/interfaces",
    )
    // The settings say it before anything is changed there.
    await page.getByRole("tab", { name: "Settings" }).click()
    await expect(page.getByText("NetworkManager writes /etc/resolv.conf")).toBeVisible()
  })

  test("saving shows the server's verification plan first and each check's result after", async ({
    page,
  }) => {
    const mutations = await open(page, "/network/dns?tab=settings")
    await page.getByRole("button", { name: /^Quad9/ }).click()
    await page.getByRole("textbox", { name: "Verification names" }).fill("nas.lan\nexample.com")
    await save(page).click()
    const dialog = page.getByRole("dialog")
    const plan = dialog.getByRole("list", { name: "Verification plan" })
    await expect(plan).toContainText("Read back")
    await expect(plan).toContainText("~lan on ens3")
    await expect(dialog.getByRole("list", { name: "Not checked" })).toContainText(
      "default route through the global upstreams",
    )
    expect(mutations.find((m) => m.path === "/network/dns/verification-plan")?.body).toMatchObject({
      verificationNames: ["nas.lan", "example.com"],
    })
    await dialog.getByRole("button", { name: "Apply" }).click()
    const results = page.getByRole("list", { name: "Verification results" })
    await expect(results).toContainText("104.16.132.229")
    await expect(results.getByText("Passed")).toHaveCount(3)
    await expect(results).toBeInViewport()
    expect(
      mutations.find((m) => m.method === "POST" && m.path === "/network/dns/")?.body,
    ).toMatchObject({
      verificationNames: ["nas.lan", "example.com"],
    })
  })

  test("a change put back by a failed check says which check failed, and stays a draft", async ({
    page,
  }) => {
    await open(page, "/network/dns?tab=settings", {
      refuse: {
        path: /^\/network\/dns\/$/,
        status: 409,
        code: "dns_upstream_unreachable",
        message:
          "systemd-resolved is not running with the settings written: global servers are 9.9.9.9. The previous settings were put back.",
        extra: rolledBack,
      },
    })
    await page.getByRole("button", { name: /^Quad9/ }).click()
    await save(page).click()
    await page.getByRole("dialog").getByRole("button", { name: "Apply" }).click()
    await expect(page.getByText("Put back: a required check failed")).toBeVisible()
    const results = page.getByRole("list", { name: "Verification results" })
    await expect(results.getByText("Failed")).toBeVisible()
    await expect(results).toContainText(
      "A later drop-in or the host's resolved.conf overrides them",
    )
    await expect(saveBar(page)).toContainText("1 unsaved change")
  })

  test("clearing the fallback list sends it as cleared, not as the host's default", async ({
    page,
  }) => {
    const mutations = await open(page, "/network/dns?tab=settings")
    await page
      .getByRole("switch", {
        name: "No fallback servers: turns off resolved's built-in fallback",
      })
      .check()
    await save(page).click()
    await expect(page.getByRole("dialog")).toContainText("Fallback: none")
    await page.getByRole("dialog").getByRole("button", { name: "Apply" }).click()
    await expect(page.getByText("resolved in")).toBeVisible()
    expect(
      mutations.find((m) => m.method === "POST" && m.path === "/network/dns/")?.body,
    ).toMatchObject({
      fallback: [],
      clear: ["fallback"],
    })
  })

  test("split DNS for a link goes through its native profile as a temporary apply", async ({
    page,
  }) => {
    const mutations = await open(page, "/network/dns", {
      overrides: { "/network/native/profiles/ens3": ens3Profile },
    })
    await page.getByRole("button", { name: "Edit split DNS for ens3" }).click()
    const panel = page.getByRole("dialog")
    await panel.getByLabel("DNS servers").fill("10.0.0.53\nfd00::53")
    await panel.getByLabel("Routing domains").fill("corp.example")
    await expect(panel.getByRole("list", { name: "What resolved will do" })).toContainText(
      "Names under corp.example go only to ens3's servers",
    )
    await panel.getByRole("button", { name: "Apply temporary split DNS" }).click()
    const put = page.waitForRequest(
      (request) =>
        request.method() === "PUT" && request.url().endsWith("/network/native/profiles/ens3"),
    )
    await page.getByRole("dialog").getByRole("button", { name: "Apply temporary settings" }).click()
    expect((await put).headers()["x-jd-network-apply"]).toBe("pending")
    await expect.poll(() => mutations.find((m) => m.method === "PUT")).toBeTruthy()
    const body = mutations.find((m) => m.method === "PUT")?.body as {
      generation: string
      intent: typeof ens3Profile.intent
    }
    expect(body.generation).toBe("gen-ens3-1")
    expect(body.intent.ipv4).toMatchObject({
      dns: ["10.0.0.53"],
      domains: ["~corp.example"],
      addresses: ["198.51.100.20/24"],
      routes: ens3Profile.intent.ipv4.routes,
    })
    expect(body.intent.ipv6).toMatchObject({ dns: ["fd00::53"], domains: ["~corp.example"] })
  })

  test("a link whose native owner is not edited here says why instead of offering a form", async ({
    page,
  }) => {
    await open(page, "/network/dns")
    await page.getByRole("button", { name: "Edit split DNS for tailscale0" }).click()
    await expect(page.getByText("This link's DNS is not edited here")).toBeVisible()
    await expect(page.getByRole("button", { name: "Apply temporary split DNS" })).toHaveCount(0)
  })

  test("the certificate check lists each server's trust, and outlives a look at another view", async ({
    page,
  }) => {
    const mutations = await open(page, "/network/dns")
    await page.getByRole("button", { name: "Check certificates" }).click()
    const checks = page.getByRole("region", { name: "Certificate checks" })
    await expect(checks.getByText("Trusted", { exact: true })).toBeVisible()
    await expect(checks.getByText("Untrusted", { exact: true })).toBeVisible()
    await expect(checks).toContainText("not valid for cloudflare-dns.com")
    await expect(checks).toContainText("198.51.100.2")
    await expect(page.getByText(/1 of 2 trusted · checked/)).toBeVisible()
    expect(mutations.find((m) => m.path === "/network/dns/tls-check")?.body).toEqual({})
    await page.getByRole("tab", { name: "Services" }).click()
    await page.getByRole("tab", { name: "Resolver" }).click()
    await expect(checks.getByText("Untrusted", { exact: true })).toBeVisible()
  })

  test("a host record that a line before the block shadows is previewed before it is saved", async ({
    page,
  }) => {
    const mutations = await open(page, "/network/dns?tab=settings")
    await page.getByRole("button", { name: "Add record" }).click()
    await page.getByLabel("Address of record 3").fill("192.0.2.44")
    await page.getByLabel("Names of record 3").fill("vps-edge-01")
    await save(page).click()
    await expect(page.getByText(/line 2, before the block, gives vps-edge-01/)).toBeVisible()
    await expect(page.getByText("Save again to write them as they are.")).toBeVisible()
    expect(mutations.some((m) => m.method === "PUT")).toBe(false)
    await save(page).click()
    await expect.poll(() => mutations.find((m) => m.method === "PUT")).toBeTruthy()
    const resolution = page.getByRole("region", { name: "Local resolution" })
    await expect(resolution.getByText("Resolves as written").first()).toBeVisible()
    await expect(resolution.getByText("Resolves elsewhere")).toBeVisible()
  })

  test("Preview overlaps shows them without saving", async ({ page }) => {
    const mutations = await open(page, "/network/dns?tab=settings")
    await page.getByRole("button", { name: "Add record" }).click()
    await page.getByLabel("Address of record 3").fill("192.0.2.44")
    await page.getByLabel("Names of record 3").fill("vps-edge-01")
    await page.getByRole("button", { name: "Preview overlaps" }).click()
    await expect(page.getByText(/line 2, before the block, gives vps-edge-01/)).toBeVisible()
    expect(mutations.map((m) => m.path)).toEqual(["/network/dns/hosts/preview"])
  })

  test("comparing over DNS over TLS with the DO bit sends both and shows transport and AD", async ({
    page,
  }) => {
    const mutations = await open(page, "/network/dns?tab=lookups")
    await page.getByRole("textbox", { name: "Name", exact: true }).fill("example.com")
    await page.getByRole("button", { name: "Resolve", exact: true }).click()
    await page.getByRole("switch", { name: "Compare named resolvers" }).check()
    await page
      .getByRole("radiogroup", { name: "Transport" })
      .getByRole("radio", { name: "DNS over TLS" })
      .click()
    await page.getByRole("checkbox", { name: "Request DNSSEC records" }).check()
    await page.getByRole("checkbox", { name: "Cloudflare 1.1.1.1" }).check()
    await page.getByRole("checkbox", { name: "Acknowledge private-name disclosure" }).check()
    await page.getByRole("button", { name: "Resolve", exact: true }).click()
    const race = page.getByRole("region", { name: "Answers for example.com" })
    await expect(race).toContainText("TLS TLS 1.3 · cloudflare-dns.com · AD set · 1 RRSIG")
    expect(mutations.filter((m) => m.path === "/network/dns/lookup").at(-1)?.body).toEqual({
      name: "example.com",
      type: "A",
      mode: "compare",
      destinations: ["1.1.1.1"],
      acknowledgeDisclosure: true,
      transport: "tls",
      dnssec: true,
    })
  })

  test("a private name with unseen forwarding is sent only after it is acknowledged", async ({
    page,
  }) => {
    const mutations = await open(page, "/network/dns?tab=lookups", { forwardingRefusal: true })
    await page.getByRole("textbox", { name: "Name", exact: true }).fill("nas.lan")
    await page.getByRole("button", { name: "Resolve", exact: true }).click()
    await expect(page.getByText(/whose own forwarding this page cannot see/)).toBeVisible()
    await page.getByRole("checkbox", { name: "Acknowledge unknown forwarding" }).check()
    await page.getByRole("button", { name: "Resolve", exact: true }).click()
    await expect(page.getByRole("region", { name: "Answers for nas.lan" })).toBeVisible()
    expect(mutations.filter((m) => m.path === "/network/dns/lookup").at(-1)?.body).toEqual({
      name: "nas.lan",
      type: "A",
      mode: "effective",
      acknowledgeForwarding: true,
    })
  })

  test("the log is a view of its own, of what answers here", async ({ page }) => {
    await open(page, "/network/dns?tab=log")
    await expect(page.getByText("systemd-resolved.service").first()).toBeVisible()
  })

  test("every button on every view has an accessible name", async ({ page }) => {
    await open(page, "/network/dns")
    await expect(page.getByText("MagicDNS")).toBeVisible()
    await expectNamedButtons(page, "[data-slot=page]")
    for (const [tab, landmark] of [
      ["Lookups", page.getByRole("textbox", { name: "Name", exact: true })],
      ["Settings", page.getByRole("button", { name: /^Quad9/ })],
      ["Services", page.getByText("No DNS engine is connected")],
      ["Log", page.getByText("systemd-resolved.service").first()],
    ] as const) {
      await page.getByRole("tab", { name: tab }).click()
      await expect(landmark).toBeVisible()
      await expectNamedButtons(page, "[data-slot=page]")
    }
  })

  for (const width of [390]) {
    test(`on a phone the identity line, the strip and the settings fit the window at ${width}`, async ({
      page,
    }) => {
      await page.setViewportSize({ width, height: 844 })
      await open(page, "/network/dns?tab=settings")
      await expect(page.getByRole("button", { name: /^Quad9/ })).toBeVisible()
      const fits = () =>
        page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)
      expect(await fits()).toBe(true)
      await expect(page.getByRole("button", { name: "Resolve a name" })).toBeInViewport()
      for (const tab of ["Lookups", "Services", "Resolver"]) {
        await page.getByRole("tab", { name: tab }).click()
        await risen(page)
        expect(await fits(), tab).toBe(true)
      }
    })
  }
})

test.describe("Traffic", () => {
  // Eleven charts redraw every two seconds; a busy machine needs the room.
  test.describe.configure({ timeout: 60_000 })

  test("opens on the per-device charts, then who is moving the bytes", async ({ page }) => {
    await open(page, "/network/traffic", { charts: true })
    await expect(page.getByRole("region", { name: "Bandwidth by device" })).toBeVisible()
    await expect(page.getByRole("radio", { name: "24h" })).toBeVisible()
    const programs = page.getByRole("region", { name: "Traffic by program" })
    const line = programs.locator("[data-slot=reading-line]")
    await expect(line).toContainText("caddy")
    await programs.getByRole("button", { name: "Who postgres talks to" }).hover()
    await expect(line).toContainText("postgres")
    const containers = page.getByRole("region", { name: "Traffic by container" })
    await containers.getByRole("link", { name: "Open adguardhome's traffic" }).hover()
    await expect(containers.locator("[data-slot=reading-line]")).toContainText("adguardhome")
  })

  test("pressing a program opens the peers it talks to", async ({ page }) => {
    await open(page, "/network/traffic")
    await page.getByRole("button", { name: "Who caddy talks to" }).click()
    const peers = page.getByRole("region", { name: "Who caddy talks to" })
    await expect(peers.getByText("203.0.113.77")).toBeVisible()
    await page.getByRole("button", { name: "Who caddy talks to" }).first().click()
    await expect(peers).toBeHidden()
  })

  test("the first read of the programs says it is measuring", async ({ page }) => {
    await open(page, "/network/traffic", {
      overrides: { "/network/traffic/processes": processTrafficWarming },
    })
    await expect(page.getByText("Measuring the first interval…")).toBeVisible()
  })

  test("the containers follow the window the charts are set to", async ({ page }) => {
    await open(page, "/network/traffic")
    const asked = page.waitForRequest(
      (request) =>
        request.url().includes("/network/traffic/containers") &&
        request.url().includes("window=24h"),
    )
    await page.getByRole("radio", { name: "24h" }).click()
    await asked
    await expect(page.getByText("in the last day")).toBeVisible()
  })

  test("editing a device's limits posts kilobits", async ({ page }) => {
    const mutations = await open(page, "/network/traffic?tab=shaping")
    await page.getByRole("button", { name: "Edit the limits on ens3" }).click()
    const sheet = page.getByRole("dialog")
    await sheet.getByRole("button", { name: "Use cake" }).click()
    await sheet.getByLabel("Upload limit").fill("20")
    await sheet.getByLabel("Download limit").fill("2.5")
    await sheet.getByRole("button", { name: "Apply" }).click()
    await expect.poll(() => mutations.find((m) => m.path === "/network/shaping/ens3")).toBeTruthy()
    expect(mutations.find((m) => m.path === "/network/shaping/ens3")?.body).toEqual({
      qdisc: "cake",
      egressKbit: 20_000,
      ingressKbit: 2500,
    })
  })

  test("under a megabit on the uplink is refused in the form, and by the server when it is asked", async ({
    page,
  }) => {
    await open(page, "/network/traffic?tab=shaping", {
      refuse: {
        path: /\/network\/shaping\/tailscale0$/,
        status: 409,
        code: "would_lock_you_out",
        message:
          "A limit under 1 Mbit/s on tailscale0 would cut off your connection to this dashboard.",
      },
    })
    await page.getByRole("button", { name: "Edit the limits on ens3" }).click()
    const sheet = page.getByRole("dialog")
    await sheet.getByLabel("Upload limit").fill("0.5")
    await expect(sheet.getByText("could lock you out")).toBeVisible()
    await expect(sheet.getByRole("button", { name: "Apply" })).toBeDisabled()
    await sheet.getByRole("button", { name: "Cancel" }).click()

    // A refusal the form could not foresee comes back from the server and
    // is drawn under the fields, with the sheet still open.
    await page.getByRole("button", { name: "Edit the limits on tailscale0" }).click()
    await sheet.getByLabel("Upload limit").fill("10")
    await sheet.getByRole("button", { name: "Apply" }).click()
    await expect(sheet.getByRole("alert")).toContainText("would cut off your connection")
    await expect(sheet).toBeVisible()
  })

  test("clearing a shaped device asks first and sends a DELETE", async ({ page }) => {
    const mutations = await open(page, "/network/traffic?tab=shaping")
    await page.getByRole("button", { name: "Queue on wg0" }).click()
    await page.getByRole("menuitem", { name: "Clear limits" }).click()
    await page.getByRole("dialog").getByRole("button", { name: "Clear" }).click()
    await expect
      .poll(() => mutations.some((m) => m.method === "DELETE" && m.path === "/network/shaping/wg0"))
      .toBe(true)
  })

  test("BBR is a switch that posts its state", async ({ page }) => {
    const mutations = await open(page, "/network/traffic?tab=shaping")
    await page.getByRole("switch", { name: "Use BBR congestion control" }).click()
    await expect
      .poll(() => mutations.find((m) => m.path === "/network/shaping/bbr")?.body)
      .toEqual({ on: true })
  })

  test("a device that cannot be shaped says why instead of offering an Edit", async ({ page }) => {
    await open(page, "/network/traffic?tab=shaping")
    const card = page.getByRole("listitem").filter({ hasText: "veth6e4f828" })
    await expect(card).toContainText("cannot be shaped")
    await expect(page.getByRole("button", { name: "Edit the limits on veth6e4f828" })).toHaveCount(
      0,
    )
    await card.getByRole("button", { name: "Open the queue on veth6e4f828" }).click()
    const sheet = page.getByRole("dialog")
    await expect(sheet.getByText(/Shape the interface this one leads to/)).toBeVisible()
    await expect(sheet.getByRole("button", { name: "Apply" })).toHaveCount(0)
  })

  test("the eBPF programs are listed and the type chips narrow them", async ({ page }) => {
    await open(page, "/network/traffic?tab=ebpf")
    const table = page.getByRole("table").last()
    await expect(table.getByRole("row")).toHaveCount(11)
    await expect(page.getByText("xdp_drop_bad").first()).toBeVisible()
    await page.getByRole("button", { name: /cgroup_skb/ }).click()
    await expect(table.getByRole("row")).toHaveCount(5)
  })

  test("without bpftool the eBPF section offers to install it", async ({ page }) => {
    await open(page, "/network/traffic?tab=ebpf", { overrides: { "/network/ebpf": ebpfMissing } })
    await expect(page.getByRole("button", { name: "Install bpftool" })).toBeVisible()
  })

  test("every button on the page, and in the limits sheet, has an accessible name", async ({
    page,
  }) => {
    await open(page, "/network/traffic?tab=shaping")
    await expect(page.getByRole("button", { name: "Edit the limits on ens3" })).toBeVisible()
    await expectNamedButtons(page, "[data-slot=page]")
    await page.getByRole("button", { name: "Edit the limits on wg0" }).click()
    await expect(page.getByRole("dialog")).toBeVisible()
    await expectNamedButtons(page, "[role=dialog]")
  })
})

/**
 * Review screenshots, for the eyes the checks do not have. Written only when
 * asked for, into the directory named — never into the tree.
 */
test.describe("screenshots", () => {
  const dir = process.env.JD_NETWORK_SHOTS
  test.skip(!dir, "set JD_NETWORK_SHOTS to a directory to capture them")
  test.describe.configure({ timeout: 120_000 })

  for (const width of [390, 1440]) {
    test.describe(`${width} wide`, () => {
      test.use({ viewport: { width, height: 1000 } })
      // The program detail, with the charts quiet: it is the part of the page the
      // live ring would otherwise hold the browser too busy to press.
      test(`/network/traffic peers at ${width}`, async ({ page }) => {
        await open(page, "/network/traffic")
        await page.getByRole("button", { name: "Who caddy talks to" }).click()
        await page.waitForTimeout(1200)
        await page.getByRole("region", { name: "Who caddy talks to" }).scrollIntoViewIfNeeded()
        await page.waitForTimeout(600)
        await page.screenshot({ path: `${dir}/traffic-peers-${width}.png` })
      })
      test(`/network/traffic charts at ${width}`, async ({ page }) => {
        await open(page, "/network/traffic", { charts: true })
        await page.waitForTimeout(3000)
        await page.screenshot({ path: `${dir}/traffic-charts-${width}.png` })
      })
      for (const path of ["/network/traffic"]) {
        test(`${path} at ${width}`, async ({ page }) => {
          // The charts have their own capture above; held quiet, the page under
          // them can be walked without the browser being busy drawing them.
          await open(page, path)
          await loaded(page)
          await page.waitForTimeout(2200)
          const name = path.replace(/^\/network\//, "")
          // The page scrolls inside the shell, so a full-page capture is only
          // the window: walk down it a screen at a time, from the top.
          const scroll = (to: number | "next") =>
            page.locator("[data-slot=page]").evaluate((element, target) => {
              let parent = element.parentElement
              while (parent && !/auto|scroll/.test(getComputedStyle(parent).overflowY)) {
                parent = parent.parentElement
              }
              if (!parent) return false
              if (target === "next") {
                if (parent.scrollTop + parent.clientHeight >= parent.scrollHeight - 1) return false
                parent.scrollTop += parent.clientHeight - 80
              } else parent.scrollTop = target
              return true
            }, to)
          await scroll(0)
          for (let part = 1; part <= 10; part++) {
            await page.waitForTimeout(300)
            await page.screenshot({ path: `${dir}/${name}-${width}-${part}.png` })
            if (!(await scroll("next"))) break
          }
        })
      }
    })
  }
})
