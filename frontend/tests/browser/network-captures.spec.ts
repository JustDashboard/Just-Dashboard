import { expect, test, type Page } from "@playwright/test"
import { admin, json, mockNetwork } from "./network-fixture"
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
async function setup(page: Page, current: () => CaptureRun = () => run) {
  await mockNetwork(page)
  await page.route("**/api/v1/network/captures/", (route) => json(route, collection(current())))
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
    await page.goto(`/network/captures?capture=${id}`)
    const detail = page.getByRole("region", { name: "Selected packet capture" })
    await expect(detail).toContainText(run.request.source!)
    await expect(detail).toContainText("Unknown")
    await expect(detail).toContainText("Omitted from the artifact")
    await expect(detail).toContainText("do not establish the same flow")
    await expect(page.getByRole("link", { name: "Download original PCAP" })).toHaveAttribute(
      "href",
      new RegExp(`/network/captures/${id}/pcap$`),
    )
    await expect(page.getByRole("link", { name: "Export redacted support" })).toBeVisible()
    expect(
      await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth),
    ).toBe(true)
    if (process.env.JD_NETWORK_SHOTS)
      await page.screenshot({
        path: `${process.env.JD_NETWORK_SHOTS}/captures-${width}.png`,
        fullPage: true,
      })
    await page.reload()
    await expect(detail).toContainText(run.request.destination!)
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
})

test("failed polls keep dated retained evidence and expose a retry", async ({ page }) => {
  await setup(page)
  await page.goto(`/network/captures?capture=${id}`)
  await expect(page.getByRole("region", { name: "Selected packet capture" })).toContainText(
    run.name,
  )
  await page.route("**/api/v1/network/captures/", (route) =>
    json(route, { error: { message: "Capture inventory unavailable" } }, 503),
  )
  await page.getByRole("button", { name: "Refresh captures", exact: true }).click()
  await expect(
    page.getByText("Showing the last known packet captures", { exact: true }),
  ).toBeVisible()
  await expect(page.locator("time").first()).toHaveAttribute("datetime", /.+/)
  await expect(page.getByRole("region", { name: "Selected packet capture" })).toContainText(
    run.request.source!,
  )
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
  await page.goto(`/network/captures?capture=${id}`)
  await page.getByRole("button", { name: "Stop capture", exact: true }).click()
  await expect(page.getByText("Waiting for native cleanup", { exact: true })).toBeVisible()
  await expect(page.getByRole("button", { name: "Stopping…", exact: true })).toBeDisabled()
  await expect(page.getByRole("link", { name: "Download original PCAP" })).toHaveCount(0)
  current = { ...current, status: "interrupted", result: undefined }
  await page.reload()
  await expect(page.getByText("Final cleanup is unverified", { exact: true })).toBeVisible()
  await expect(page.getByRole("button", { name: "Stop capture", exact: true })).toHaveCount(0)
  expect(cancels).toBe(1)
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
  await page.goto(`/network/captures?capture=${id}`)
  await expect(
    page.getByText("Packet captures require the admin capability", { exact: true }),
  ).toBeVisible()
  await expect(page.getByRole("button", { name: "New capture", exact: true })).toHaveCount(0)
  expect(requests).toEqual([])
})
