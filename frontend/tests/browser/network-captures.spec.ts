import { expect, test, type Page } from "@playwright/test"
import { admin, json, mockNetwork } from "./network-fixture"
import { risen } from "./risen"
import type { CaptureList, CaptureRun } from "../../src/lib/network-captures"

const id = "c".repeat(32)
const incident = "d".repeat(32)
const run: CaptureRun = {
  id,
  name: "Private IPv6 incident",
  status: "completed",
  createdBy: "operator",
  request: {
    interface: "ens3",
    family: "inet6",
    protocol: "udp",
    source: "2001:db8:100:200:300:400:500:600",
    destination: "2001:db8:200:300:400:500:600:700",
    port: 8443,
    packets: 20,
    seconds: 10,
    maxBytes: 65536,
    snapshotLength: 128,
    incidentRunId: incident,
  },
  createdAt: "2026-10-08T12:00:00Z",
  startedAt: "2026-10-08T12:00:01Z",
  endedAt: "2026-10-08T12:00:11Z",
  limitations: ["Provider paths and traffic in other namespaces remain unknown."],
  result: {
    checkedAt: "2026-10-08T12:00:11Z",
    packets: 3,
    bytes: 412,
    linkType: 1,
    sha256: "a".repeat(64),
    stopReason: "time_limit",
    partialPacket: true,
    cleanup: { termSent: false, killSent: false, exitCode: 124 },
    interfaceIndex: 2,
    identityVerified: true,
    artifactAvailable: true,
  },
}
const collection = (current: CaptureRun): CaptureList => ({
  runs: [current],
  maxRunning: 2,
  maxRetained: 32,
  retentionHours: 24,
  maxArtifactBytes: 2097152,
})
const second: CaptureRun = {
  ...run,
  id: "e".repeat(32),
  name: "Resolver on docker0",
  createdAt: "2026-10-08T11:00:00Z",
  request: {
    interface: "docker0",
    family: "inet",
    protocol: "all",
    packets: 1000,
    seconds: 30,
    maxBytes: 1048576,
    snapshotLength: 128,
  },
  result: undefined,
  status: "failed",
}
async function setup(page: Page, current: () => CaptureRun = () => run, others: CaptureRun[] = []) {
  await mockNetwork(page)
  await page.route("**/api/v1/network/captures/", (route) =>
    json(route, { ...collection(current()), runs: [current(), ...others] }),
  )
  for (const other of others)
    await page.route(`**/api/v1/network/captures/${other.id}`, (route) => json(route, other))
  await page.route(`**/api/v1/network/captures/${id}`, (route) => json(route, current()))
  await page.route("**/api/v1/network/captures/interfaces", (route) =>
    json(route, [{ name: "ens3", index: 2, up: true }]),
  )
  await page.route("**/api/v1/network/diagnostics/", (route) =>
    json(route, [{ id: incident, name: "Related private path" }]),
  )
  await page.route(`**/api/v1/network/diagnostics/${incident}`, (route) =>
    json(route, { id: incident, name: "Related private path", startedAt: "2026-10-08T11:59:00Z" }),
  )
}
async function pick(page: Page, label: string, name: string) {
  await page.getByRole("combobox", { name: label, exact: true }).click()
  await page.getByRole("option", { name, exact: true }).click()
}

const detail = (page: Page) => page.getByRole("region", { name: "Packet capture", exact: true })

for (const width of [390, 1440]) {
  test(`retained IPv6 artifacts and unknown drops remain readable at ${width}px without recapture`, async ({
    page,
  }) => {
    await page.setViewportSize({ width, height: 1000 })
    const launches: string[] = []
    page.on("request", (request) => {
      if (request.method() === "POST" && request.url().includes("/network/captures"))
        launches.push(request.url())
    })
    await setup(page)
    // The address that selected a capture beside the list lands on its page.
    await page.goto(`/network/captures?capture=${id}`)
    await expect(page).toHaveURL(new RegExp(`/network/captures/${id}$`))
    await expect(detail(page)).toContainText(run.request.source!)
    await expect(detail(page)).toContainText("kernel drops unknown")
    await expect(detail(page)).toContainText("do not establish the same flow")
    await expect(detail(page).getByRole("link", { name: /Related private path/ })).toHaveAttribute(
      "href",
      `/network/runs/${incident}`,
    )
    await expect(page.getByRole("link", { name: "Download PCAP" })).toHaveAttribute(
      "href",
      new RegExp(`/network/captures/${id}/pcap$`),
    )
    await page.getByRole("button", { name: "Capture actions", exact: true }).click()
    await expect(page.getByRole("menuitem", { name: "Export redacted support" })).toHaveAttribute(
      "href",
      new RegExp(`/network/captures/${id}/support$`),
    )
    await page.keyboard.press("Escape")
    await page.getByRole("tab", { name: "Facts", exact: true }).click()
    await expect(page).toHaveURL(/\?tab=facts$/)
    await expect(detail(page)).toContainText("Unknown")
    await expect(detail(page)).toContainText("Omitted from the artifact")
    await expect(detail(page)).toContainText("a".repeat(64))
    expect(
      await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth),
    ).toBe(true)
    await page.reload()
    await expect(detail(page)).toContainText(run.request.destination!)
    expect(launches).toEqual([])
  })
}

test("a refused launch preserves its full bounded IPv6 filter and incident before explicit retry", async ({
  page,
}) => {
  await setup(page)
  const launches: unknown[] = []
  await page.route("**/api/v1/network/captures/", (route) => {
    if (route.request().method() === "GET") return json(route, collection(run))
    launches.push(route.request().postDataJSON())
    return launches.length === 1
      ? json(route, { error: { message: "Native interface replaced; inspect again" } }, 409)
      : json(route, run, 202)
  })
  await page.goto("/network/captures")
  await page.getByRole("button", { name: "New capture", exact: true }).click()
  const dialog = page.getByRole("dialog")
  await expect(dialog).toContainText("Original packet bytes will be retained")
  await page.getByLabel("Capture name", { exact: true }).fill(run.name)
  await pick(page, "Native interface", "ens3")
  await pick(page, "Address family", "IPv6")
  await pick(page, "Protocol", "UDP")
  await page.getByLabel("Source address", { exact: true }).fill(run.request.source!)
  await page.getByLabel("Destination address", { exact: true }).fill(run.request.destination!)
  await page.getByLabel("Port", { exact: true }).fill("8443")
  await page.getByLabel("Packet limit", { exact: true }).fill("20")
  await page.getByLabel("Time limit in seconds", { exact: true }).fill("10")
  await page.getByLabel("Artifact byte limit", { exact: true }).fill("65536")
  await page.getByText("Snapshot and incident", { exact: true }).click()
  await pick(page, "Related saved diagnostic", "Related private path")
  await page.getByRole("button", { name: "Start capture", exact: true }).click()
  await expect(dialog).toContainText("Native interface replaced")
  await expect(page.getByLabel("Capture name", { exact: true })).toHaveValue(run.name)
  await expect(page.getByLabel("Source address", { exact: true })).toHaveValue(run.request.source!)
  await expect(page.getByLabel("Artifact byte limit", { exact: true })).toHaveValue("65536")
  expect(launches).toEqual([{ name: run.name, request: run.request }])
  await page.getByRole("button", { name: "Start capture", exact: true }).click()
  await expect(dialog).toHaveCount(0)
  expect(launches).toHaveLength(2)
  expect(launches[1]).toEqual(launches[0])
  // A started capture opens on its own page.
  await expect(page).toHaveURL(new RegExp(`/network/captures/${id}$`))
})

test("failed polls keep dated retained evidence and expose a retry", async ({ page }) => {
  await setup(page)
  await page.goto("/network/captures")
  await expect(page.getByRole("link", { name: `Open ${run.name}` })).toBeVisible()
  await expect(page.getByRole("region", { name: "About packet captures" })).toContainText(
    "412 B of packet data",
  )
  await page.route("**/api/v1/network/captures/", (route) =>
    json(route, { error: { message: "Capture inventory unavailable" } }, 503),
  )
  await page.getByRole("button", { name: "Capture actions", exact: true }).click()
  await page.getByRole("menuitem", { name: "Refresh", exact: true }).click()
  await expect(
    page.getByText("Showing the last known packet captures", { exact: true }),
  ).toBeVisible()
  await expect(page.locator("time").first()).toHaveAttribute("datetime", /.+/)
  await expect(page.getByRole("link", { name: `Open ${run.name}` })).toBeVisible()
  await expect(page.getByRole("button", { name: "Refresh", exact: true })).toBeVisible()
})

test("stop waits for native cleanup and reload never replays an interrupted capture", async ({
  page,
}) => {
  let current: CaptureRun = {
    ...run,
    status: "running",
    endedAt: undefined,
    result: { ...run.result!, artifactAvailable: false },
  }
  await setup(page, () => current)
  let cancels = 0
  await page.route(`**/api/v1/network/captures/${id}/cancel`, (route) => {
    cancels++
    current = { ...current, status: "cancelling" }
    return json(route, current)
  })
  await page.goto(`/network/captures/${id}`)
  await page.getByRole("button", { name: "Stop capture", exact: true }).click()
  await expect(page.getByText("Waiting for native cleanup", { exact: true })).toBeVisible()
  await expect(page.getByRole("button", { name: "Stopping…", exact: true })).toBeDisabled()
  await expect(page.getByRole("link", { name: "Download PCAP" })).toHaveCount(0)
  current = { ...current, status: "interrupted", result: undefined }
  await page.reload()
  await expect(page.getByText("Final cleanup is unverified", { exact: true })).toBeVisible()
  await expect(page.getByRole("button", { name: "Stop capture", exact: true })).toHaveCount(0)
  expect(cancels).toBe(1)
})

test("deleting a capture confirms first and returns to the list", async ({ page }) => {
  const deletes: string[] = []
  await setup(page)
  await page.route(`**/api/v1/network/captures/${id}`, (route) => {
    if (route.request().method() === "DELETE") {
      deletes.push(route.request().url())
      return route.fulfill({ status: 204 })
    }
    return json(route, run)
  })
  await page.goto(`/network/captures/${id}`)
  await page.getByRole("button", { name: "Capture actions", exact: true }).click()
  await page.getByRole("menuitem", { name: "Delete", exact: true }).click()
  expect(deletes).toHaveLength(0)
  await page
    .getByRole("dialog", { name: "Delete packet capture" })
    .getByRole("button", { name: "Delete capture", exact: true })
    .click()
  await expect(page).toHaveURL(/\/network\/captures$/)
  expect(deletes).toHaveLength(1)
})

test("read accounts do not request private capture metadata, interfaces or artifacts", async ({
  page,
}) => {
  const requests: string[] = []
  page.on("request", (request) => {
    if (request.url().includes("/api/v1/network/captures")) requests.push(request.url())
  })
  await mockNetwork(page, [], {
    session: { ...admin, capabilities: ["read"], user: { ...admin.user, role: "readonly" } },
  })
  for (const path of [`/network/captures?capture=${id}`, "/network/captures"]) {
    await page.goto(path)
    await expect(
      page.getByText("Packet captures require the admin capability", { exact: true }),
    ).toBeVisible()
    await expect(page.getByRole("button", { name: "New capture", exact: true })).toHaveCount(0)
    await expect(page.getByRole("button", { name: "Capture actions" })).toHaveCount(0)
  }
  expect(requests).toEqual([])
})

for (const width of [390, 1280]) {
  test(`capture rows keep one height with and without a result at ${width}px`, async ({ page }) => {
    await page.setViewportSize({ width, height: 900 })
    await setup(page, () => run, [second])
    await page.goto("/network/captures")
    const rows = page.getByRole("list", { name: "Packet captures" }).getByRole("listitem")
    await expect(rows).toHaveCount(2)
    await risen(page)
    const [a, b] = [await rows.nth(0).boundingBox(), await rows.nth(1).boundingBox()]
    expect(a!.height).toBe(b!.height)
    // No row of figures opens the page any more.
    await expect(page.locator("[data-slot='stat-grid']")).toHaveCount(0)
    expect(
      await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth),
    ).toBe(true)
  })
}

test("the opening line stands in one place while the list loads and once it has", async ({
  page,
}) => {
  await setup(page)
  let release = () => {}
  const held = new Promise<void>((resolve) => (release = resolve))
  await page.route("**/api/v1/network/captures/", async (route) => {
    await held
    return json(route, collection(run))
  })
  await page.goto("/network/captures")
  const line = page.getByRole("region", { name: "About packet captures" })
  await expect(line).toBeVisible()
  await risen(page)
  const loading = await line.boundingBox()
  release()
  await expect(page.getByRole("link", { name: `Open ${run.name}` })).toBeVisible()
  await risen(page)
  expect(await line.boundingBox()).toEqual(loading)
})

test("the rail walks the captures with Alt+arrows and keeps the open view", async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 })
  await setup(page, () => run, [second])
  await page.goto(`/network/captures/${id}?tab=facts`)
  const rail = page.getByRole("navigation", { name: "Packet captures" })
  await expect(rail.getByRole("link", { name: run.name })).toHaveAttribute("aria-current", "page")
  await expect(detail(page)).toContainText("Omitted from the artifact")
  await page.keyboard.press("Alt+ArrowDown")
  await expect(page).toHaveURL(new RegExp(`/network/captures/${second.id}\\?tab=facts$`))
  await expect(rail.getByRole("link", { name: second.name })).toHaveAttribute(
    "aria-current",
    "page",
  )
  await expect(detail(page)).toContainText("docker0")
})
