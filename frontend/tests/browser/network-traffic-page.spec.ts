import { expect as baseExpect, test, type Locator, type Page } from "@playwright/test"
import { json, loaded, type Mutation } from "./network-fixture"
import { ebpf, shaping } from "./network-traffic-maturity-fixture"
import {
  cautious,
  ebpfAttached,
  holdReads,
  openTraffic,
  reader,
} from "./network-traffic-page-fixture"
import { risen } from "./risen"

// The Traffic page as the overhaul left it: an identity line, four views in
// the address, one-line cards, sheets only for forms, and a container's
// traffic and an eBPF program as pages with their list beside them. What each
// control sends is `network-traffic-maturity.spec.ts`'s and
// `network-sqm.spec.ts`'s; this is the page's shape and where things went.

const expect = baseExpect.configure({ timeout: 15_000 })
test.describe.configure({ timeout: 120_000 })

const top = async (target: Locator) => Math.round((await target.boundingBox())!.y)
const height = async (target: Locator) => Math.round((await target.boundingBox())!.height)
const overflow = (page: Page) =>
  page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth)

for (const viewport of [
  { width: 1280, height: 900 },
  { width: 390, height: 844 },
]) {
  test.describe(`at ${viewport.width}`, () => {
    test.use({ viewport })

    test("the page opens on the uplink, with no tile row on any view", async ({ page }) => {
      await openTraffic(page)
      await page.goto("/network/traffic")
      await loaded(page)
      const identity = page.getByRole("region", { name: "About the uplink" })
      await expect(identity).toContainText("ens3")
      await expect(identity).toContainText("Uplink")
      await expect(identity).toContainText(/[\d.]+ [KM]B\/s in, [\d.]+ [KM]B\/s out/)
      await expect(identity).toContainText("sampled every 2s")
      await expect(identity).toContainText("history recorded")
      await expect(identity).toContainText(/read \ds ago/)
      // It is the first thing on the page.
      expect(await top(identity)).toBeLessThan(viewport.width < 640 ? 130 : 80)
      for (const view of ["Budgets", "Shaping", "eBPF programs", "Bandwidth"]) {
        await page.getByRole("tab", { name: view, exact: true }).click()
        await expect(page.getByRole("tab", { name: view, exact: true })).toHaveAttribute(
          "aria-selected",
          "true",
        )
        await loaded(page)
        await expect(page.locator("[data-slot=stat-grid]")).toHaveCount(0)
        await expect(page.locator("[data-slot=stat-tile]")).toHaveCount(0)
        expect(await overflow(page), `sideways scroll on ${view}`).toBeLessThanOrEqual(0)
      }
    })

    test("the opening block is where it was before the readings landed", async ({ page }) => {
      await openTraffic(page)
      // The page draws as many devices as it drew last time, so the first
      // visit is the one that teaches it; the second is the one measured.
      await page.goto("/network/traffic")
      await expect(page.getByLabel("ens3 packets")).toBeVisible()
      const gate = await holdReads(page)
      await page.reload()
      const identity = page.getByRole("region", { name: "About the uplink" })
      const context = page.getByRole("group", { name: "Live context" })
      const devices = page.getByRole("region", { name: "Bandwidth by device" })
      const movers = page.getByRole("region", { name: "Who is moving them" })
      await expect(identity).toContainText("Reading the devices…")
      await expect(identity).toContainText("waiting for the first reading")
      await expect(context).toContainText("Reading the uplink…")
      await risen(page)
      const before = {
        identity: await identity.boundingBox(),
        context: await context.boundingBox(),
        devices: await top(devices),
        movers: await top(movers),
      }
      gate.release()
      await expect(identity).toContainText("history recorded")
      await expect(context).toContainText("packets a second")
      await expect(page.getByLabel("ens3 packets")).toBeVisible()
      await risen(page)
      expect(await identity.boundingBox()).toEqual(before.identity)
      expect(await context.boundingBox()).toEqual(before.context)
      expect(await top(devices)).toBe(before.devices)
      // The devices are drawn as many as last time, each at the height it lands at.
      expect(Math.abs((await top(movers)) - before.movers)).toBeLessThanOrEqual(2)
    })

    test("a budget is one height measured or not, and the next one does not move", async ({
      page,
    }) => {
      await openTraffic(page)
      await page.goto("/network/traffic?tab=budgets")
      const rows = page.locator("[data-slot=budget-row]")
      await expect(rows).toHaveCount(2)
      await risen(page)
      // ens3 is measured, with an estimate; wg0 is not measured at all. The
      // hairline between two budgets belongs to the upper one: a pixel.
      expect(
        Math.abs((await height(rows.nth(0))) - (await height(rows.nth(1)))),
      ).toBeLessThanOrEqual(1)
      await expect(rows.nth(0).getByRole("meter")).toBeVisible()
      await expect(rows.nth(1)).toContainText("Not measured against 5.0 GB in a day")
      await expect(rows.nth(1).getByRole("meter")).toBeVisible()
      expect(await overflow(page)).toBeLessThanOrEqual(0)
    })

    test("a queue is one line whatever it carries, with the evidence on its sheet", async ({
      page,
    }) => {
      await openTraffic(page)
      await page.goto("/network/traffic?tab=shaping")
      const cards = page.getByRole("list", { name: "Queues by device" }).getByRole("listitem")
      await expect(cards).toHaveCount(4)
      await risen(page)
      // ens3 is managed, drifted, with an upload profile and CAKE's delay;
      // wg0 has the kernel's default queue and nothing else to say.
      const heights = await Promise.all([0, 1, 2, 3].map((i) => height(cards.nth(i))))
      expect(new Set(heights).size).toBe(1)
      const gap = (await top(cards.nth(1))) - (await top(cards.nth(0)))
      expect((await top(cards.nth(3))) - (await top(cards.nth(2)))).toBe(gap)
      await expect(cards.nth(0)).toContainText("ens3")
      await expect(cards.nth(0)).toContainText("cake · uplink")
      await expect(cards.nth(0).getByLabel("Upload 18 Mbit/s").first()).toBeAttached()
      await expect(cards.nth(0).getByLabel("Download no limit").first()).toBeAttached()
      await expect(cards.nth(0)).toContainText("differs from saved limits")
      await expect(cards.nth(2)).toContainText("changes refused")
      // None of the evidence hangs under a card.
      await expect(cards.nth(0)).not.toContainText("rtt 50000")
      // The two comparisons are tables from `sm`, and three lines an algorithm on a phone.
      const comparison = page.getByLabel("Congestion control on live sockets")
      await expect(comparison.getByRole("table")).toHaveCount(viewport.width < 640 ? 0 : 2)
      await expect(comparison.getByRole("group", { name: "Now" })).toContainText(
        viewport.width < 640
          ? "round trips half inside 22 ms, 9 in 10 inside 61 ms"
          : "Round trip, half within",
      )
      await cards.nth(0).getByRole("button", { name: "Edit the limits on ens3" }).click()
      const evidence = page.getByRole("dialog").getByLabel("ens3 queue evidence")
      await expect(evidence).toContainText("35 dropped · 900 over limit")
      await expect(evidence).toContainText("rtt 50000 → 100000")
      await expect(evidence).toContainText("Upload CAKE · diffserv4 · dual-srchost")
      expect(await overflow(page)).toBeLessThanOrEqual(0)
    })
  })
}

test("the view and the window are in the address, and survive a reload", async ({ page }) => {
  await openTraffic(page)
  await page.goto("/network/traffic?tab=shaping")
  await expect(page.getByRole("tab", { name: "Shaping" })).toHaveAttribute("aria-selected", "true")
  await expect(page.getByRole("list", { name: "Queues by device" })).toBeVisible()
  await page.getByRole("tab", { name: "Budgets" }).click()
  await expect(page).toHaveURL(/\/network\/traffic\?tab=budgets$/)
  await page.getByRole("tab", { name: "Bandwidth" }).click()
  await expect(page).toHaveURL(/\/network\/traffic$/)
  await page.getByRole("radio", { name: "24h" }).click()
  await expect(page).toHaveURL(/\/network\/traffic\?window=24h$/)
  await page.reload()
  await expect(page.getByRole("radio", { name: "24h" })).toBeChecked()
  await expect(page.getByLabel("ens3 throughput spread")).toContainText("19 in 20 under ↓ 2.0 MB/s")
  // An unknown view is the first one, not an empty page.
  await page.goto("/network/traffic?tab=queues")
  await expect(page.getByRole("tab", { name: "Bandwidth" })).toHaveAttribute(
    "aria-selected",
    "true",
  )
})

test("nothing on the page says p95, a median or RTT", async ({ page }) => {
  await openTraffic(page)
  for (const path of [
    "/network/traffic",
    "/network/traffic?window=24h",
    "/network/traffic?tab=shaping",
  ]) {
    await page.goto(path)
    await loaded(page)
    if (path.endsWith("24h")) await expect(page.getByLabel("ens3 throughput spread")).toBeVisible()
    if (path.endsWith("shaping")) {
      await expect(page.getByRole("columnheader", { name: "Round trip, half within" })).toHaveCount(
        2,
      )
    }
    await expect(page.locator("[data-slot=page]")).not.toContainText(
      /\bp(50|90|95|99)\b|[Mm]edian|RTT/,
    )
  }
})

test("a read-only account sees every reading and no control it cannot use", async ({ page }) => {
  const mutations: Mutation[] = []
  await openTraffic(page, { mutations, session: reader })
  await page.goto("/network/traffic")
  await loaded(page)
  await expect(page.getByRole("region", { name: "Traffic by program" })).toContainText(
    "an administrator’s to see",
  )
  await page.getByRole("tab", { name: "Budgets" }).click()
  await expect(page.locator("[data-slot=budget-row]")).toHaveCount(2)
  await expect(page.getByRole("button", { name: "Set a transfer budget" })).toHaveCount(0)
  await expect(page.getByRole("button", { name: /Budget on/ })).toHaveCount(0)
  await page.getByRole("tab", { name: "Shaping" }).click()
  await expect(page.getByRole("switch", { name: "Use BBR congestion control" })).toBeDisabled()
  await expect(page.getByRole("button", { name: /Queue on/ })).toHaveCount(0)
  await expect(page.getByRole("button", { name: /Edit the limits/ })).toHaveCount(0)
  // The evidence is still theirs to read, on a sheet with no form in it.
  await page.getByRole("button", { name: "Open the queue on ens3" }).click()
  const sheet = page.getByRole("dialog")
  await expect(sheet.getByLabel("ens3 queue evidence")).toContainText("rtt 50000 → 100000")
  await expect(sheet.getByRole("textbox")).toHaveCount(0)
  await expect(sheet.getByRole("switch")).toHaveCount(0)
  await expect(sheet.getByRole("button", { name: "Apply" })).toHaveCount(0)
  await sheet.getByRole("button", { name: "Close", exact: true }).first().click()
  await page.getByRole("tab", { name: "eBPF programs" }).click()
  await expect(page.getByRole("link", { name: "Open program 9101" })).toBeVisible()
  expect(mutations).toEqual([])
})

test("clearing is offered only to an account that may clear", async ({ page }) => {
  await openTraffic(page, { session: cautious })
  await page.goto("/network/traffic?tab=budgets")
  await page.getByRole("button", { name: "Budget on ens3" }).click()
  await expect(page.getByRole("menuitem", { name: "Change" })).toBeVisible()
  await expect(page.getByRole("menuitem", { name: "Clear" })).toHaveCount(0)
  await page.keyboard.press("Escape")
  await page.getByRole("tab", { name: "Shaping" }).click()
  await page.getByRole("button", { name: "Queue on ens3" }).click()
  await expect(page.getByRole("menuitem", { name: "Edit limits" })).toBeVisible()
  await expect(page.getByRole("menuitem", { name: "Clear limits" })).toHaveCount(0)
})

test("a budget's menu changes that budget, the device fixed", async ({ page }) => {
  const mutations: Mutation[] = []
  await openTraffic(page, { mutations })
  await page.goto("/network/traffic?tab=budgets")
  await page.getByRole("button", { name: "Budget on ens3" }).click()
  await page.getByRole("menuitem", { name: "Change" }).click()
  const sheet = page.getByRole("dialog")
  await expect(sheet.getByRole("combobox")).toHaveCount(0)
  await expect(sheet.getByLabel("Budget", { exact: true })).toHaveValue("1.0 TB")
  await expect(sheet.getByRole("radio", { name: "month" })).toBeChecked()
  await sheet.getByLabel("Budget", { exact: true }).fill("2 TB")
  await sheet.getByRole("button", { name: "Set budget" }).click()
  await expect
    .poll(() => mutations.find((m) => m.method === "PUT"))
    .toEqual({
      method: "PUT",
      path: "/network/traffic/quotas/ens3",
      body: { period: "month", direction: "both", limitBytes: 2 * 2 ** 40 },
    })
  await expect(sheet).toHaveCount(0)
})

test("no budget, a failed read and a missing tool are each said with their next step", async ({
  page,
}) => {
  await openTraffic(page, {
    overrides: {
      "/network/traffic/quotas": [],
      "/network/ebpf": { ...ebpf, installed: false, package: "bpftool" },
    },
    fail: ["/network/shaping"],
  })
  await page.goto("/network/traffic?tab=budgets")
  await expect(page.getByText("No device has a transfer budget")).toBeVisible()
  await page.getByRole("button", { name: "Set a budget", exact: true }).click()
  await expect(page.getByRole("form", { name: "Set a transfer budget" })).toBeVisible()
  await page.keyboard.press("Escape")
  await page.getByRole("tab", { name: "Shaping" }).click()
  await expect(page.getByRole("region", { name: "Queues" })).toContainText(
    "/network/shaping could not be read",
  )
  await expect(
    page.getByRole("region", { name: "Queues" }).getByRole("button", { name: "Try again" }),
  ).toBeVisible()
  // The comparison is another read, and still drawn.
  await expect(page.getByLabel("Congestion control on live sockets")).toContainText("bbr")
  await page.getByRole("tab", { name: "eBPF programs" }).click()
  await expect(page.getByText("bpftool is not installed")).toBeVisible()
  await expect(page.getByRole("button", { name: "Install bpftool" })).toBeVisible()
})

test("the eBPF figures are one line, the type is in the address, and a program is a page", async ({
  page,
}) => {
  await openTraffic(page, { overrides: { "/network/ebpf": ebpfAttached } })
  await page.goto("/network/traffic?tab=ebpf")
  await expect(page.getByLabel("eBPF inventory")).toHaveText(
    "4 programs of 3 types · 20.0 KB of locked memory · 1 attachment on ens3 · 41 cgroup attachments",
  )
  const table = page.getByRole("table", { name: "Loaded eBPF programs" })
  await expect(table.getByRole("row")).toHaveCount(5)
  await expect(page.getByRole("region", { name: "Attached to devices" })).toContainText(
    "xdp_drop_bad · program 77",
  )
  await page.getByRole("button", { name: /cgroup_skb/ }).click()
  await expect(page).toHaveURL(/tab=ebpf&type=cgroup_skb$/)
  await expect(table.getByRole("row")).toHaveCount(3)
  await page.getByRole("link", { name: "Open program 9101" }).click()
  await expect(page).toHaveURL(/\/network\/traffic\/ebpf\/9101\?type=cgroup_skb$/)
  const identity = page.getByRole("region", { name: "About program 9101" })
  await expect(identity).toContainText("jd_flow_egress")
  await expect(identity).toContainText("attached in 2 places")
  await expect(identity).toContainText("this dashboard’s observer")
  await expect(page.getByLabel("Program 9101 in the kernel")).toContainText(
    "4,096 B, 2,100 B native · 512 instructions verified",
  )
  await expect(page.getByLabel("Program 9101 in the kernel")).toContainText("not counted")
  await expect(page.getByRole("region", { name: "Where program 9101 is attached" })).toContainText(
    "/sys/fs/cgroup/system.slice",
  )
  await expect(page.getByRole("region", { name: "Maps of program 9101" })).toContainText(
    "jd_events",
  )
  // The rail is the list it was opened from: the two cgroup_skb programs.
  const rail = page.getByRole("navigation", { name: "Programs" })
  await expect(rail.getByRole("listitem")).toHaveCount(2)
  await expect(rail.getByRole("button", { name: "jd_flow_egress, program 9101" })).toHaveAttribute(
    "aria-current",
    "page",
  )
  await page.keyboard.press("Alt+ArrowUp")
  await expect(page).toHaveURL(/\/network\/traffic\/ebpf\/4633\?type=cgroup_skb$/)
  await rail.getByRole("link", { name: "Every type" }).click()
  await expect(rail.getByRole("listitem")).toHaveCount(4)
  await page.goto("/network/traffic/ebpf/9101?type=cgroup_skb")
  await page.getByRole("link", { name: "eBPF programs" }).click()
  await expect(page).toHaveURL(/\/network\/traffic\?tab=ebpf&type=cgroup_skb$/)
})

test("a program that is gone and an address that is no program say so", async ({ page }) => {
  await openTraffic(page)
  await page.route("**/api/v1/network/ebpf/4242", (route) =>
    json(route, { error: { code: "not_found", message: "no such program" } }, 404),
  )
  await page.goto("/network/traffic/ebpf/4242")
  await expect(page.getByText("No program with ID 4242 is loaded")).toBeVisible()
  await expect(page.getByRole("link", { name: "Back to the programs" })).toHaveAttribute(
    "href",
    "/network/traffic?tab=ebpf",
  )
  await page.goto("/network/traffic/ebpf/caddy")
  await expect(page.getByText("That is not a program ID")).toBeVisible()
})

test("a container's traffic is a page on the window the charts were set to", async ({ page }) => {
  const asked: URL[] = []
  page.on("request", (r) => {
    if (r.url().includes("/traffic/containers/")) asked.push(new URL(r.url()))
  })
  await openTraffic(page)
  await page.goto("/network/traffic?window=24h")
  const band = page.getByRole("region", { name: "Traffic by container" })
  await band.getByRole("link", { name: "Open postgres's traffic" }).click()
  await expect(page).toHaveURL(/\/network\/traffic\/containers\/postgres\?window=24h$/)
  await expect.poll(() => asked.at(-1)?.searchParams.get("window")).toBe("24h")
  const identity = page.getByRole("region", { name: "About postgres" })
  await expect(identity).toContainText("running")
  await expect(identity).toContainText("registry:2")
  await expect(identity).toContainText("infra/registry")
  await expect(identity).toContainText("on infra_default")
  await expect(identity.getByRole("link", { name: "Container" })).toHaveAttribute(
    "href",
    `/docker/containers/${"a".repeat(64)}`,
  )
  await expect(page.getByLabel("What postgres moved")).toHaveText(
    "↓ 190.7 MB ↑ 85.8 MB in the last day · now ↓ 48.8 KB/s ↑ 19.5 KB/s",
  )
  await expect(page.getByRole("region", { name: "Who postgres talked to" })).toContainText(
    "198.51.100.99",
  )
  // The rail is the band's ranking, and Alt+↓ walks it on the same window.
  const rail = page.getByRole("navigation", { name: "Containers" })
  await expect(rail.getByRole("listitem")).toHaveCount(7)
  await expect(rail.getByRole("button", { name: /^postgres,/ })).toHaveAttribute(
    "aria-current",
    "page",
  )
  await page.keyboard.press("Alt+ArrowDown")
  await expect(page).toHaveURL(/\/network\/traffic\/containers\/grafana\?window=24h$/)
  await page.getByRole("radio", { name: "7d" }).click()
  await expect(page).toHaveURL(/\/network\/traffic\/containers\/grafana\?window=7d$/)
  await expect.poll(() => asked.at(-1)?.searchParams.get("window")).toBe("7d")
  await page.getByRole("main").getByRole("link", { name: "Traffic", exact: true }).click()
  await expect(page).toHaveURL(/\/network\/traffic\?window=7d$/)
})

test("a container with nothing recorded offers a longer window, and a reader sees no peers", async ({
  page,
}) => {
  await openTraffic(page, { session: reader })
  await page.goto("/network/traffic/containers/registry")
  await expect(page.getByRole("region", { name: "Who registry talked to" })).toContainText(
    "an administrator’s to see",
  )
  await page.route("**/api/v1/network/traffic/containers/gone**", (route) =>
    json(route, { error: { code: "not_found", message: "nothing is recorded for gone" } }, 404),
  )
  await page.goto("/network/traffic/containers/gone")
  await expect(page.getByText("Nothing is recorded for gone in the last hour")).toBeVisible()
  await page.getByRole("radio", { name: "24h" }).click()
  await expect(page.getByText("Nothing is recorded for gone in the last day")).toBeVisible()
})

test.describe("at a phone's width", () => {
  test.use({ viewport: { width: 390, height: 844 } })

  test("a container's and a program's page are one pane with the arrows, and never scroll sideways", async ({
    page,
  }) => {
    await openTraffic(page)
    await page.goto("/network/traffic/containers/postgres")
    await expect(page.getByRole("region", { name: "Who postgres talked to" })).toContainText(
      "198.51.100.99",
    )
    await expect(page.getByRole("navigation", { name: "Containers" })).toBeHidden()
    expect(await overflow(page)).toBeLessThanOrEqual(0)
    await page.getByRole("button", { name: "Next container" }).click()
    await expect(page).toHaveURL(/\/containers\/grafana$/)
    await page.goto("/network/traffic/ebpf/9101")
    await expect(page.getByRole("region", { name: "Maps of program 9101" })).toContainText(
      "jd_events",
    )
    await expect(page.getByRole("navigation", { name: "Programs" })).toBeHidden()
    expect(await overflow(page)).toBeLessThanOrEqual(0)
    await page.getByRole("button", { name: "Previous program" }).click()
    await expect(page).toHaveURL(/\/ebpf\/4633$/)
  })

  test("the shaping sheet shows its evidence and its form without scrolling sideways", async ({
    page,
  }) => {
    await openTraffic(page, {
      overrides: {
        "/network/shaping": { ...shaping, devices: shaping.devices.slice(0, 1) },
      },
    })
    await page.goto("/network/traffic?tab=shaping")
    await page.getByRole("button", { name: "Edit the limits on ens3" }).click()
    const sheet = page.getByRole("dialog")
    await expect(sheet.getByLabel("ens3 queue evidence")).toBeVisible()
    await expect(sheet.getByLabel("Upload limit")).toHaveValue("18")
    expect(await overflow(page)).toBeLessThanOrEqual(0)
  })
})
