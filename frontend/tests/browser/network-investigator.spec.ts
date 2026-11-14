import { expect, test, type Locator, type Page } from "@playwright/test"
import { admin, json, mockNetwork } from "./network-fixture"
import { risen } from "./risen"
import type { PathRequest, PathResult } from "../../src/lib/network-investigator-types"

const id = "a".repeat(64)
const evidence = (
  id: string,
  title: string,
  basis: "observed" | "modeled" | "measured" | "unknown",
  state: string,
  summary: string,
) => ({
  id,
  title,
  basis,
  state,
  summary,
  scope: "selected source",
  owner: "native owner",
  checkedAt: new Date().toISOString(),
  facts: [],
  limitations: [],
})

function report(request: PathRequest): PathResult {
  return {
    request,
    scope: {
      vantage: request.sourceKind === "host" ? "dashboard_host" : "container_network_namespace",
      source: request.sourceKind === "host" ? "Dashboard host" : "web",
      sourceAddress:
        request.sourceAddress || (request.family === "inet" ? "192.0.2.3" : "2001:db8::3"),
      target: request.target,
      address: request.address || (request.family === "inet" ? "192.0.2.8" : "2001:db8::8"),
      family: request.family,
      protocol: request.protocol,
      port: request.port,
      limitations: ["Provider policy and remote service ownership remain unknown."],
    },
    startedAt: new Date().toISOString(),
    endedAt: new Date().toISOString(),
    addresses:
      request.family === "inet" ? ["192.0.2.8", "192.0.2.9"] : ["2001:db8::8", "2001:db8::9"],
    evidence: [
      evidence("dns", "DNS", "measured", "observed", "Native resolver answered this private name."),
      evidence("rules", "Policy rules", "observed", "observed", "Ordered native candidates."),
      evidence(
        "route",
        "Kernel route",
        "observed",
        "observed",
        "The kernel selected eth0; no connectivity is implied.",
      ),
      evidence(
        "firewall",
        "Firewall",
        "modeled",
        "modeled",
        "UFW OUT default models deny for this tuple.",
      ),
      evidence("nat", "NAT", "unknown", "unknown", "Foreign and provider NAT remain unknown."),
      evidence(
        "tunnel",
        "Tunnel / interface",
        "observed",
        "observed",
        "Native interface metadata identifies eth0.",
      ),
      evidence(
        "proxy",
        "Proxy",
        "unknown",
        "unknown",
        "Remote or foreign proxy configuration is unknown.",
      ),
      evidence(
        "owner",
        "Destination owner",
        "unknown",
        "unknown",
        "Remote ownership remains unknown.",
      ),
      evidence(
        "probe",
        "Connection measurement",
        request.measure ? "measured" : "unknown",
        request.measure ? "connected" : "not_requested",
        request.measure
          ? "TCP connected from this source at this time."
          : "No TCP connection was attempted.",
      ),
    ],
    comparison: request.measure
      ? "Host firewall adapter prediction: deny. Selected-source TCP measurement: connected. Unknown foreign/provider/application layers remain unknown."
      : "No connection measurement was requested.",
  }
}

// Connection path is a tool of the workbench; `/network/investigate` is the
// address other pages still name, and it lands there.
const pane = (page: Page) => page.locator("[data-tool=path]")
const run = (page: Page) => pane(page).getByRole("button", { name: "Run", exact: true })
const box = async (locator: Locator) => (await locator.boundingBox())!

async function setup(page: Page, readonly = false) {
  const calls: PathRequest[] = []
  await mockNetwork(page, [], {
    ...(readonly
      ? { session: { ...admin, capabilities: ["read"], user: { ...admin.user, role: "readonly" } } }
      : {}),
  })
  await page.route("**/api/v1/network/investigate/sources", (route) =>
    json(route, { containers: [{ id, name: "web" }] }),
  )
  await page.route("**/api/v1/network/investigate", (route) => {
    const request = route.request().postDataJSON() as PathRequest
    calls.push(request)
    return json(route, report(request))
  })
  return calls
}

test("keeps model and selected-source measurement distinct with provider layers unknown", async ({
  page,
}) => {
  const calls = await setup(page)
  await page.goto("/network/investigate")
  await page.getByLabel("Destination", { exact: true }).fill("private.corp")
  await page.getByRole("checkbox", { name: "Measure a TCP connection" }).check()
  await run(page).click()
  const result = page.getByRole("region", { name: "Connection path report" })
  await expect(result).toContainText("prediction: deny")
  await expect(result).toContainText("TCP connected from this source at this time")
  await expect(result).toContainText("Unknown foreign/provider/application layers remain unknown")
  await expect(result).not.toContainText("Allowed")
  expect(calls).toHaveLength(1)
  expect(calls[0]).toMatchObject({
    sourceKind: "host",
    target: "private.corp",
    family: "inet",
    protocol: "tcp",
    port: 443,
    measure: true,
  })
  await expect(page.locator('[data-slot="page"]')).toHaveAttribute("data-register", "reading")
  await expect(page.locator('[data-slot="panel"]:not([data-plain])')).toHaveCount(0)
  // The four counts were a row of tiles; they are the key to the chain's colours.
  await expect(page.locator('[data-slot="stat-grid"]')).toHaveCount(0)
  const kinds = result.getByRole("list", { name: "Evidence kinds" })
  await expect(kinds).toContainText("Snapshot 3")
  await expect(kinds).toContainText("Model 1")
  await expect(kinds).toContainText("Measured 2")
  await expect(kinds).toContainText("Unknown 3")
})

test("pins a chosen native DNS candidate on the next investigation", async ({ page }) => {
  const calls = await setup(page)
  await page.goto("/network/investigate")
  await page.getByLabel("Destination", { exact: true }).fill("private.corp")
  await run(page).click()
  await page.getByLabel("Chosen DNS address", { exact: true }).click()
  await page.getByRole("option", { name: "192.0.2.9", exact: true }).click()
  await run(page).click()
  await expect.poll(() => calls.length).toBe(2)
  expect(calls[1].address).toBe("192.0.2.9")
  expect(calls[1].measure).toBe(false)
  await expect(page.getByRole("region", { name: "Connection path report" })).toContainText(
    "192.0.2.3 → 192.0.2.9",
  )
})

test("container UDP investigation retains source/family/port and sends no TCP substitute", async ({
  page,
}) => {
  const calls = await setup(page)
  await page.goto("/network/investigate")
  await page.getByLabel("Source", { exact: true }).click()
  await page.getByRole("option", { name: "web", exact: true }).click()
  await page.getByLabel("Family", { exact: true }).click()
  await page.getByRole("option", { name: "IPv6", exact: true }).click()
  await page.getByLabel("Protocol", { exact: true }).click()
  await page.getByRole("option", { name: "UDP", exact: true }).click()
  await page.getByLabel("Destination", { exact: true }).fill("2001:db8::8")
  await page.getByLabel("Port", { exact: true }).fill("53")
  await expect(page.getByRole("checkbox", { name: "Measure a TCP connection" })).toBeDisabled()
  await run(page).click()
  await expect.poll(() => calls.length).toBe(1)
  expect(calls[0]).toEqual({
    sourceKind: "container",
    containerId: id,
    target: "2001:db8::8",
    family: "inet6",
    protocol: "udp",
    port: 53,
    measure: false,
  })
})

test("unavailable private DNS shows skipped later evidence and retains a failed next draft", async ({
  page,
}) => {
  await setup(page)
  let attempts = 0
  await page.route("**/api/v1/network/investigate", (route) => {
    attempts++
    if (attempts > 1)
      return route.fulfill({
        status: 409,
        contentType: "application/json",
        body: JSON.stringify({
          error: { code: "source_unavailable", message: "Container identity changed" },
        }),
      })
    const result = report(route.request().postDataJSON())
    result.scope.address = undefined
    result.addresses = []
    result.evidence = [
      evidence(
        "dns",
        "DNS",
        "unknown",
        "unavailable",
        "Native private suffix resolver is unavailable; no public fallback was queried.",
      ),
      evidence(
        "route",
        "Kernel route",
        "unknown",
        "skipped",
        "No selected destination exists; route lookup was not queried.",
      ),
      evidence(
        "probe",
        "Connection measurement",
        "unknown",
        "skipped",
        "No connection probe was attempted.",
      ),
    ]
    result.comparison = "No selected address exists; connectivity was not measured."
    return json(route, result)
  })
  await page.goto("/network/investigate")
  await page.getByLabel("Destination", { exact: true }).fill("private.corp")
  await run(page).click()
  await expect(page.getByRole("region", { name: "Connection path report" })).toContainText(
    "no public fallback",
  )
  await expect(page.getByRole("region", { name: "Connection path report" })).toContainText(
    "not measured",
  )
  await page.getByLabel("Destination", { exact: true }).fill("next.private")
  await run(page).click()
  await expect(
    page.getByRole("alert").filter({ hasText: "Container identity changed" }),
  ).toBeVisible()
  await expect(page.getByLabel("Destination", { exact: true })).toHaveValue("next.private")
  await expect(page.getByRole("region", { name: "Connection path report" })).toContainText(
    "private.corp",
  )
})

test("read-only accounts are shown no diagnostic they cannot start, and no source is read", async ({
  page,
}) => {
  const calls = await setup(page, true)
  let inventories = 0
  await page.route("**/api/v1/network/investigate/sources", (route) => {
    inventories++
    return json(route, { containers: [] })
  })
  await page.goto("/network/investigate?target=private.corp")
  await expect(page).toHaveURL(/\/network\/tools\?tool=path&target=private\.corp$/)
  await expect(
    page.getByText("Server diagnostics need the admin capability", { exact: true }),
  ).toBeVisible()
  await expect(pane(page)).toHaveCount(0)
  await expect(page.getByLabel("Destination", { exact: true })).toHaveCount(0)
  await expect(page.getByRole("button", { name: "Connection path", exact: true })).toHaveCount(0)
  await expect(page.getByRole("button", { name: "Run and save", exact: true })).toHaveCount(0)
  await expect(page.getByRole("link", { name: "Saved runs", exact: true })).toHaveCount(0)
  expect(calls).toHaveLength(0)
  expect(inventories).toBe(0)
})

test("a stream's link opens the tuple, and the report joins the stream and the backend its connection reached", async ({
  page,
}) => {
  const calls: PathRequest[] = []
  await mockNetwork(page, [])
  await page.route("**/api/v1/network/investigate/sources", (route) =>
    json(route, { containers: [] }),
  )
  await page.route("**/api/v1/network/investigate", (route) => {
    const request = route.request().postDataJSON() as PathRequest
    calls.push(request)
    const base = report(request)
    return json(route, {
      ...base,
      evidence: [
        ...base.evidence,
        {
          ...evidence(
            "stream",
            "Stream proxy",
            "observed",
            "live",
            "nginx forwards TCP on port 6432/tcp to 10.0.0.5:5432.",
          ),
          ownerPath: "/proxy/streams?stream=pg",
          facts: [
            { label: "Access", value: "allow 10.0.0.0/8, deny everyone else" },
            { label: "Client sessions now", value: "3" },
            {
              label: "backend 10.0.0.5:5432",
              value: "2 connections from nginx now; 11 sessions in the last hour, 1 failed",
            },
          ],
          limitations: [
            "Configured forwards and access rules are the stream file's; the connections and sessions are nginx's, not this tuple's.",
          ],
        },
        {
          ...evidence(
            "stream_traversal",
            "Stream backend leg",
            "measured",
            "forwarded",
            "nginx forwarded the measured connection to 10.0.0.5:5432 and logged it as completed.",
          ),
          facts: [{ label: "Backend", value: "10.0.0.5:5432" }],
        },
      ],
    })
  })
  await page.goto(
    "/network/investigate?target=127.0.0.1&port=6432&protocol=tcp&family=inet&measure=1",
  )
  // The old address lands on the tool with every parameter it carried.
  await expect(page).toHaveURL(
    /\/network\/tools\?tool=path&target=127\.0\.0\.1&port=6432&protocol=tcp&family=inet&measure=1$/,
  )
  await expect(
    page
      .getByRole("navigation", { name: "Diagnostics" })
      .getByRole("button", { name: "Connection path", exact: true }),
  ).toHaveAttribute("aria-pressed", "true")
  await expect(page.getByLabel("Destination", { exact: true })).toHaveValue("127.0.0.1")
  await expect(page.getByLabel("Port", { exact: true })).toHaveValue("6432")
  await expect(page.getByLabel("Protocol", { exact: true })).toContainText("TCP")
  await expect(page.getByLabel("Family", { exact: true })).toContainText("IPv4")
  await expect(page.getByRole("checkbox", { name: "Measure a TCP connection" })).toBeChecked()
  await run(page).click()
  await expect.poll(() => calls.length).toBe(1)
  expect(calls[0]).toMatchObject({
    target: "127.0.0.1",
    port: 6432,
    protocol: "tcp",
    measure: true,
  })

  const result = page.getByRole("region", { name: "Connection path report" })
  const stream = result.locator("#evidence-stream")
  await expect(stream.getByRole("heading", { name: "Stream proxy" })).toBeVisible()
  await expect(stream).toContainText("Snapshot · live")
  await expect(stream).toContainText(
    "2 connections from nginx now; 11 sessions in the last hour, 1 failed",
  )
  await expect(stream.getByRole("link", { name: "Open owner" })).toHaveAttribute(
    "href",
    "/proxy/streams?stream=pg",
  )
  const leg = result.locator("#evidence-stream_traversal")
  await expect(leg).toContainText("Measured · forwarded")
  await expect(leg).toContainText("nginx forwarded the measured connection to 10.0.0.5:5432")
})

test("a container handed over by name is the source once the inventory names it", async ({
  page,
}) => {
  const calls = await setup(page)
  await page.goto("/network/investigate?container=web")
  await expect(page).toHaveURL(/\/network\/tools\?tool=path&container=web$/)
  await expect(page.getByLabel("Source", { exact: true })).toContainText("web")
  await page.getByLabel("Destination", { exact: true }).fill("192.0.2.8")
  await run(page).click()
  await expect.poll(() => calls.length).toBe(1)
  expect(calls[0]).toMatchObject({ sourceKind: "container", containerId: id, target: "192.0.2.8" })
})

test("the report is a chain of hops, each in the colour of what it rests on", async ({ page }) => {
  await setup(page)
  await page.goto("/network/tools?tool=path&target=private.corp")
  await run(page).click()
  const result = page.getByRole("region", { name: "Connection path report" })
  const hops = result.getByRole("list", { name: "Hops" }).locator("> li")
  await expect(hops).toHaveCount(9)
  await expect(hops.locator("[data-slot=hop-mark]")).toHaveCount(9)
  expect(
    await hops.evaluateAll((items) => items.map((item) => item.getAttribute("data-basis"))),
  ).toEqual([
    "measured",
    "observed",
    "observed",
    "modeled",
    "unknown",
    "observed",
    "unknown",
    "unknown",
    "unknown",
  ])
  // One colour per kind, none for what is not known; the wire to the next hop
  // is dashed wherever either end is unknown.
  const colours = await hops.evaluateAll((items) =>
    items.map(
      (item) => getComputedStyle(item.querySelector("[data-slot=hop-mark] > span > span")!).color,
    ),
  )
  expect(colours[1]).toBe(colours[2])
  expect(new Set([colours[0], colours[1], colours[3], colours[4]]).size).toBe(4)
  expect(
    await hops.evaluateAll((items) =>
      items.map(
        (item) => item.querySelector("[data-slot=hop-wire]")?.getAttribute("data-line") ?? null,
      ),
    ),
  ).toEqual(["solid", "solid", "solid", "dashed", "dashed", "dashed", "dashed", "dashed", null])
  await expect(result.locator("#evidence-probe")).toContainText("Unknown · not requested")
  await expect(result.locator("#evidence-nat").getByRole("heading", { name: "NAT" })).toBeVisible()
  await expect(result.locator("#evidence-owner").getByRole("link")).toHaveCount(0)
  await expect(
    result.getByText("Provider policy and remote service ownership remain unknown."),
  ).toBeVisible()
})

test("a layer saved with the old Ports address still opens the Ports page", async ({ page }) => {
  await setup(page)
  // A run saved before the investigator named the Ports page by its address.
  await page.route("**/api/v1/network/investigate", (route) => {
    const base = report(route.request().postDataJSON())
    return json(route, {
      ...base,
      evidence: base.evidence.map((item) =>
        item.id === "owner" ? { ...item, ownerPath: "/ports" } : item,
      ),
    })
  })
  await page.goto("/network/tools?tool=path&target=private.corp")
  await run(page).click()
  await expect(
    page
      .getByRole("region", { name: "Connection path report" })
      .locator("#evidence-owner")
      .getByRole("link", { name: "Open owner" }),
  ).toHaveAttribute("href", "/proxy/ports")
})

for (const width of [390, 1280]) {
  test(`the tool holds still while sources load and a run is in flight at ${width}px`, async ({
    page,
  }) => {
    await page.setViewportSize({ width, height: 900 })
    await setup(page)
    let releaseSources: (() => void) | undefined
    const sourcesHeld = new Promise<void>((resolve) => (releaseSources = resolve))
    await page.route("**/api/v1/network/investigate/sources", async (route) => {
      await sourcesHeld
      await json(route, { containers: [{ id, name: "web" }] })
    })
    let release: (() => void) | undefined
    const held = new Promise<void>((resolve) => (release = resolve))
    await page.route("**/api/v1/network/investigate", async (route) => {
      await held
      await json(route, report(route.request().postDataJSON()))
    })
    await page.goto("/network/tools?tool=path&target=private.corp")
    await expect(run(page)).toBeVisible()
    await risen(page)
    const empty = pane(page).locator("[data-slot=tool-empty]")
    const before = { run: await box(run(page)), empty: await box(empty) }
    releaseSources!()
    await page.getByLabel("Source", { exact: true }).click()
    await expect(page.getByRole("option", { name: "web", exact: true })).toBeVisible()
    await page.keyboard.press("Escape")
    expect(await box(run(page))).toEqual(before.run)

    // The example fills the destination and sends nothing.
    await page.getByLabel("Destination", { exact: true }).fill("")
    await empty.getByRole("button", { name: "Try example.com" }).click()
    await expect(page.getByLabel("Destination", { exact: true })).toHaveValue("example.com")

    await run(page).click()
    await expect(empty).toContainText("Running diagnostic")
    expect(await box(run(page))).toEqual(before.run)
    expect(await box(empty)).toEqual(before.empty)
    release!()
    await expect(page.getByRole("region", { name: "Connection path report" })).toBeVisible()
    expect(await box(run(page))).toEqual(before.run)
    expect(
      await page.evaluate(
        () => document.documentElement.scrollWidth - document.documentElement.clientWidth,
      ),
    ).toBeLessThanOrEqual(0)
  })
}

test("Connection path left the Network rail and stands among the reachability tools", async ({
  page,
}) => {
  await setup(page)
  await page.setViewportSize({ width: 1280, height: 900 })
  await page.goto("/network/tools")
  await expect(page.getByRole("link", { name: "Connection path", exact: true })).toHaveCount(0)
  const rail = page.getByRole("navigation", { name: "Diagnostics" })
  const names = await rail.getByRole("button").allInnerTexts()
  expect(names.slice(4, 7)).toEqual(["Route lookup", "Connection path", "Path MTU"])
  await rail.getByRole("button", { name: "Connection path", exact: true }).click()
  await expect(run(page)).toBeVisible()
  await expect(page.getByLabel("Destination", { exact: true })).toHaveValue("")
})
