import { expect as baseExpect, test, type Page } from "@playwright/test"
import { loaded, mockNetwork, type Mutation } from "./network-fixture"
import {
  dnsView,
  dnsViewManaged,
  ebpfMissing,
  mockNetworkWrites,
  overrides,
  processTrafficWarming,
  type Refusal,
} from "./network-dns-fixture"

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
  /** Keep the two-second ring the charts draw; without it they stay empty and the page stays light. */
  charts?: boolean
  overrides?: Record<string, unknown>
  refuse?: Refusal
}

async function open(page: Page, path: string, setup: Setup = {}) {
  const mutations: Mutation[] = []
  // Eleven charts redrawn every two seconds are most of what this page costs a
  // browser, and none of the checks but the first are about them.
  const quiet = setup.charts ? {} : { "/network/traffic/live": { now: 0, series: {} } }
  await mockNetwork(page, mutations, {
    overrides: { ...overrides, ...quiet, ...setup.overrides },
  })
  await mockNetworkWrites(page, mutations, { refuse: setup.refuse })
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

test.describe("DNS", () => {
  test("opens on the resolver chain with the readings and what answers on port 53", async ({
    page,
  }) => {
    await open(page, "/network/dns")
    await expect(page.getByRole("heading", { name: "Resolver chain" })).toBeVisible()
    await expect(page.getByText("MagicDNS")).toBeVisible()
    await expect(page.getByText("in use").first()).toBeVisible()
    await expect(page.getByText("78", { exact: true }).first()).toBeVisible()
    const port53 = page.getByRole("region", { name: "Answering on port 53" })
    await expect(port53.getByText("AdGuard Home")).toBeVisible()
    await expect(port53.getByText("systemd-resolved").first()).toBeVisible()
    // The ad-blocker's own page is a link on the address this browser used.
    await expect(page.getByRole("link", { name: /web page on port 3000/ })).toHaveAttribute(
      "href",
      "http://127.0.0.1:3000",
    )
  })

  test("picking Quad9 with DNS over TLS required posts the servers with their TLS name", async ({
    page,
  }) => {
    const mutations = await open(page, "/network/dns")
    await page.getByRole("button", { name: /^Quad9/ }).click()
    await page.getByRole("button", { name: "DNS over TLS required" }).click()
    await page.getByRole("button", { name: "Apply", exact: true }).click()
    await page.getByRole("dialog").getByRole("button", { name: "Apply" }).click()
    await expect(page.getByText("resolved in")).toBeVisible()
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

  test("required TLS is refused before it is asked while a server has no name", async ({
    page,
  }) => {
    const mutations = await open(page, "/network/dns")
    await page
      .getByRole("textbox", { name: "Servers" })
      .fill("9.9.9.9\n149.112.112.112#dns.quad9.net")
    await page.getByRole("button", { name: "DNS over TLS required" }).click()
    await expect(page.getByText("needs a name on every server: 9.9.9.9 has none")).toBeVisible()
    await expect(page.getByRole("button", { name: "Apply", exact: true })).toBeDisabled()
    expect(mutations).toEqual([])
  })

  test("resetting to the system's goes through a confirmation and a DELETE", async ({ page }) => {
    const mutations = await open(page, "/network/dns", {
      overrides: { "/network/dns/": dnsViewManaged },
    })
    await page.getByRole("button", { name: /Reset to the system/ }).click()
    await page.getByRole("dialog").getByRole("button", { name: "Reset" }).click()
    await expect(page.getByText("resolved in")).toBeVisible()
    expect(mutations.some((m) => m.method === "DELETE" && m.path === "/network/dns/")).toBe(true)
  })

  test("a host where systemd-resolved is not running reads, and says why", async ({ page }) => {
    await open(page, "/network/dns", {
      overrides: {
        "/network/dns/": {
          ...dnsView,
          resolvConf: { ...dnsView.resolvConf, mode: "static", managedBy: "netplan" },
          resolved: { ...dnsView.resolved, active: false, statistics: undefined },
        },
      },
    })
    await expect(page.getByText("These settings are read here, not written")).toBeVisible()
    await expect(page.getByRole("button", { name: "Apply", exact: true })).toBeDisabled()
  })

  test("with no ad-blocker the page offers the two real ways to get one", async ({ page }) => {
    await open(page, "/network/dns", { overrides: { "/network/dns/": dnsViewManaged } })
    await expect(page.getByRole("button", { name: "Show the ad-blocking resolvers" })).toBeVisible()
    await expect(page.getByRole("link", { name: "Deploy a DNS filter" })).toHaveAttribute(
      "href",
      "/deploy/new",
    )
  })

  test("the lookup race has one row per resolver, the failed one with its reason", async ({
    page,
  }) => {
    const mutations = await open(page, "/network/dns")
    await page.getByRole("switch", { name: "Include public resolvers" }).check()
    await page.getByLabel("Name", { exact: true }).fill("example.com")
    await page.getByRole("button", { name: "Resolve" }).click()
    const race = page.getByRole("region", { name: "Answers for example.com" })
    await expect(race.getByRole("listitem")).toHaveCount(7)
    await expect(race.getByText("no answer within 2 s")).toBeVisible()
    await expect(race.getByText("6 of 7 answered")).toBeVisible()
    expect(mutations.find((m) => m.path === "/network/dns/lookup")?.body).toEqual({
      name: "example.com",
      type: "A",
      includePublic: true,
    })
  })

  test("saving host records sends the whole list, the new row included", async ({ page }) => {
    const mutations = await open(page, "/network/dns")
    await page.getByRole("button", { name: "Add record" }).click()
    await page.getByLabel("Address of record 3").fill("192.0.2.44")
    await page.getByLabel("Names of record 3").fill("printer.lan printer")
    await page.getByRole("button", { name: "Save", exact: true }).click()
    await expect.poll(() => mutations.find((m) => m.method === "PUT")).toBeTruthy()
    expect(mutations.find((m) => m.method === "PUT")?.body).toEqual({
      records: [
        { address: "192.0.2.10", names: ["nas.lan", "nas"] },
        { address: "10.0.4.20", names: ["grafana.lan"] },
        { address: "192.0.2.44", names: ["printer.lan", "printer"] },
      ],
    })
    // The file's own lines are shown and cannot be edited.
    await expect(page.getByRole("cell", { name: "localhost", exact: true })).toBeVisible()
  })

  test("a half-written host record says which field is missing", async ({ page }) => {
    const mutations = await open(page, "/network/dns")
    await page.getByRole("button", { name: "Add record" }).click()
    await page.getByLabel("Address of record 3").fill("192.0.2.44")
    await page.getByRole("button", { name: "Save", exact: true }).click()
    await expect(page.getByText("Give it a name.")).toBeVisible()
    expect(mutations).toEqual([])
  })

  test("every button on the page has an accessible name", async ({ page }) => {
    await open(page, "/network/dns")
    await expect(page.getByRole("button", { name: /^Quad9/ })).toBeVisible()
    await expectNamedButtons(page, "[data-slot=page]")
  })
})

test.describe("Traffic", () => {
  // Eleven charts redraw every two seconds; a busy machine needs the room.
  test.describe.configure({ timeout: 60_000 })

  test("opens on the per-device charts, then who is moving the bytes", async ({ page }) => {
    await open(page, "/network/traffic", { charts: true })
    await expect(page.getByRole("heading", { name: "Bandwidth by device" })).toBeVisible()
    await expect(page.getByRole("radio", { name: "24h" })).toBeVisible()
    const programs = page.getByRole("region", { name: "Traffic by program" })
    await expect(programs.getByText("caddy")).toBeVisible()
    await expect(programs.getByText("postgres")).toBeVisible()
    const containers = page.getByRole("region", { name: "Traffic by container" })
    await expect(containers.getByText("adguardhome")).toBeVisible()
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
    const mutations = await open(page, "/network/traffic")
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
    await open(page, "/network/traffic", {
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
    const mutations = await open(page, "/network/traffic")
    await page.getByRole("button", { name: "Clear the limits on wg0" }).click()
    await page.getByRole("dialog").getByRole("button", { name: "Clear" }).click()
    await expect
      .poll(() => mutations.some((m) => m.method === "DELETE" && m.path === "/network/shaping/wg0"))
      .toBe(true)
  })

  test("BBR is a switch that posts its state", async ({ page }) => {
    const mutations = await open(page, "/network/traffic")
    await page.getByRole("switch", { name: "Use BBR congestion control" }).click()
    await expect
      .poll(() => mutations.find((m) => m.path === "/network/shaping/bbr")?.body)
      .toEqual({ on: true })
  })

  test("a device that cannot be shaped says why instead of offering an Edit", async ({ page }) => {
    await open(page, "/network/traffic")
    await expect(page.getByText(/Shape the interface this one leads to/)).toBeVisible()
    await expect(page.getByRole("button", { name: "Edit the limits on veth6e4f828" })).toHaveCount(
      0,
    )
  })

  test("the eBPF programs are listed and the type chips narrow them", async ({ page }) => {
    await open(page, "/network/traffic")
    const table = page.getByRole("table").last()
    await expect(table.getByRole("row")).toHaveCount(11)
    await expect(page.getByText("xdp_drop_bad").first()).toBeVisible()
    await page.getByRole("button", { name: /cgroup_skb/ }).click()
    await expect(table.getByRole("row")).toHaveCount(5)
  })

  test("without bpftool the eBPF section offers to install it", async ({ page }) => {
    await open(page, "/network/traffic", { overrides: { "/network/ebpf": ebpfMissing } })
    await expect(page.getByRole("button", { name: "Install bpftool" })).toBeVisible()
  })

  test("every button on the page, and in the limits sheet, has an accessible name", async ({
    page,
  }) => {
    await open(page, "/network/traffic")
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
      for (const path of ["/network/dns", "/network/traffic"]) {
        test(`${path} at ${width}`, async ({ page }) => {
          // The charts have their own capture above; held quiet, the page under
          // them can be walked without the browser being busy drawing them.
          await open(page, path, { charts: path === "/network/dns" })
          await loaded(page)
          if (path === "/network/dns") {
            await page.getByLabel("Name", { exact: true }).fill("example.com")
            await page.getByRole("button", { name: "Resolve" }).click()
          }
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
