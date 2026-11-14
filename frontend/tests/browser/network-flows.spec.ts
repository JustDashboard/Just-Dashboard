import { expect, test, type Locator, type Page } from "@playwright/test"
import { admin, json, loaded, mockNetwork, type Mutation } from "./network-fixture"
import {
  containerId,
  flowReport as report,
  flowRow as row,
  recordingReport,
  stamp,
  udpRow,
} from "./network-evidence-fixture"
import { risen } from "./risen"
import type { FlowReport, FlowRow } from "../../src/lib/network-flows"

const path = "/network/flows/"

async function install(page: Page, mutations: Mutation[] = [], data = report) {
  await mockNetwork(page, mutations, { overrides: { [path]: data } })
}
/** The recorder's `⋯` menu, where the verbs that are not its command live. */
async function recorderMenu(page: Page, item: string) {
  await page.getByRole("button", { name: "More recorder actions" }).click()
  await page.getByRole("menuitem", { name: item }).click()
}
const top = async (page: Page, locator: Locator) => {
  await risen(page)
  return (await locator.boundingBox())!
}
const reading = (page: Page) => page.getByRole("region", { name: "Selected socket hour" })
/** One of the page's three views, by the words on its tab. */
const view = (page: Page, name: string) =>
  page.getByRole("tablist", { name: "Views of socket history" }).getByRole("tab", { name }).click()
const fact = (scope: Locator, name: string) =>
  scope.locator("dl > div").filter({ has: scope.page().getByText(name, { exact: true }) })

test("history stays opt-in: off and empty, the page is what recording would keep", async ({
  page,
}) => {
  const mutations: Mutation[] = []
  await install(page, mutations)
  await page.goto("/network/flows")
  await loaded(page)
  await expect(page.locator('[data-slot="stat-grid"], [data-slot="stat-tile"]')).toHaveCount(0)
  const identity = page.getByRole("region", { name: "About socket history" })
  await expect(identity.getByText("Recorder off", { exact: true })).toBeVisible()
  await expect(identity).toContainText("recording has not been enabled")
  const off = page.locator('[data-slot="flow-off"]')
  await expect(off).toContainText(
    "Recording has not been enabled, so no contact or bandwidth conclusion can be drawn.",
  )
  // The picture says what would be sampled and kept, and nothing is given a figure.
  const picture = page.locator('[data-slot="wire-flow"]')
  await expect(picture.getByRole("region", { name: "Sampled" })).toContainText("This server")
  await expect(picture.getByRole("region", { name: "Kept" })).toContainText(
    "a row for each socket and hour",
  )
  await expect(page.getByRole("region", { name: "Socket hours" })).toHaveCount(0)
  await expect(page.getByRole("region", { name: "Recorded hours" })).toHaveCount(0)
  // What the record cannot say and the observer's own state are a view each.
  await view(page, "Kernel observer")
  await expect(page.getByRole("region", { name: "Kernel packet observer" })).toContainText(
    "UDP bytes, short-lived flows and dropped-event counts remain unknown.",
  )
  await view(page, "Latest sample")
  await expect(page.getByRole("region", { name: "Measurement boundaries" })).toContainText(
    "Empty results do not prove no contact or zero bandwidth.",
  )
  await view(page, "Socket hours")
  expect(mutations).toEqual([])
  await expect(page.getByRole("button", { name: "Start recording" })).toHaveCount(1)
  await off.getByRole("button", { name: "Start recording" }).click()
  await expect.poll(() => mutations.length).toBe(1)
  expect(mutations[0]).toEqual({
    method: "POST",
    path: "/network/flows/recording",
    body: { enabled: true },
  })
})

test("full tuple and container proof keep UDP bytes and loss unknown", async ({ page }) => {
  await install(page, [], {
    ...report,
    recordingSince: stamp,
    rows: [row, udpRow],
    retainedRows: 2,
    retainedFrom: row.hour,
    truncated: true,
  })
  await page.goto("/network/flows")
  await loaded(page)
  const table = page.getByRole("region", { name: "Socket hours" })
  const tcp = table.getByRole("row").filter({ hasText: "worker" })
  await expect(tcp).toContainText("192.0.2.1:443")
  await expect(tcp.getByText("128.0 KiB", { exact: true })).toBeVisible()
  const udp = table.getByRole("row").filter({ hasText: "resolved · PID 412" })
  await expect(udp.getByText("Unavailable for UDP", { exact: true })).toHaveCount(2)
  await expect(table.getByText("Displayed rows are capped", { exact: false })).toBeVisible()
  // What the rows shown add up to: UDP adds no bytes, and nothing measured a retransmit.
  const total = (name: string) =>
    table.locator('[data-slot="flow-total"]').filter({ hasText: name })
  await expect(total("TCP sent")).toContainText("128.0 KiB")
  await expect(total("TCP sent")).toHaveAttribute("title", "1 rows with measured sent deltas")
  await expect(total("Retransmits")).toContainText("Unknown")
  // The first row is read out in full; the container's whole identity is its evidence.
  await expect(fact(reading(page), "Owner evidence")).toContainText(containerId)
  await expect(fact(reading(page), "Outstanding-loss gauge max")).toContainText("Unknown")
  await expect(fact(reading(page), "Retransmits")).toContainText("Unknown")
  await udp.getByRole("button", { name: "resolved · PID 412" }).click()
  await expect(fact(reading(page), "Sent")).toContainText("Unavailable for UDP")
  await expect(fact(reading(page), "Received")).toContainText("Unavailable for UDP")
  await expect(fact(reading(page), "Outstanding-loss gauge max")).toContainText("Unknown")
  await expect(fact(reading(page), "Window")).toContainText("Observed span; no close event")
})

test("failed refresh retains dates and blocks recorder mutations and exports", async ({ page }) => {
  await install(page, [], { ...report, rows: [row] })
  await page.goto("/network/flows")
  await loaded(page)
  await page.route("**/api/v1/network/flows/**", (route) =>
    json(route, { error: { message: "Storage reading unavailable" } }, 503),
  )
  await recorderMenu(page, "Inspect again")
  await expect(page.getByText("Last known recorder state", { exact: true })).toBeVisible()
  await expect(page.getByRole("button", { name: "Start recording" })).toBeDisabled()
  await expect(page.getByRole("button", { name: "Export bounded JSON" })).toBeDisabled()
  await expect(page.locator(`time[datetime="${stamp}"]`).first()).toBeVisible()
  await expect(page.getByRole("button", { name: "Refresh", exact: true })).toBeVisible()
  await page.getByRole("button", { name: "More recorder actions" }).click()
  await expect(page.getByRole("menuitem", { name: "Erase retained history" })).toBeDisabled()
})

test("period filters send UTC boundaries and full container identity", async ({ page }) => {
  await install(page, [], recordingReport)
  const requests: URL[] = []
  page.on("request", (request) => {
    if (new URL(request.url()).pathname.endsWith(path)) requests.push(new URL(request.url()))
  })
  await page.goto("/network/flows")
  await loaded(page)
  await page.getByLabel("Day (UTC)").fill("2026-10-07")
  await page.getByLabel("Observed peer IP").fill("2001:db8::1")
  await page.getByLabel("Container identity").fill(containerId)
  await page.getByRole("button", { name: "Read period" }).click()
  await expect.poll(() => requests.at(-1)?.searchParams.get("containerId")).toBe(containerId)
  expect(requests.at(-1)?.searchParams.get("from")).toBe("2026-10-07T00:00:00.000Z")
  expect(requests.at(-1)?.searchParams.get("to")).toBe("2026-10-08T00:00:00.000Z")
  expect(requests.at(-1)?.searchParams.get("address")).toBe("2001:db8::1")
  await expect(page.getByRole("region", { name: "Recorded hours" })).toContainText("7 Oct 2026")
})

test("the period is exported from the table that shows it", async ({ page }) => {
  await install(page, [], recordingReport)
  await page.goto("/network/flows")
  await loaded(page)
  const [request, download] = await Promise.all([
    page.waitForRequest((r) => new URL(r.url()).pathname.endsWith("/network/flows/export")),
    page.waitForEvent("download"),
    page.getByRole("button", { name: "Export bounded JSON" }).click(),
  ])
  expect(Date.parse(new URL(request.url()).searchParams.get("from")!)).toBe(
    Date.parse(recordingReport.from),
  )
  expect(download.suggestedFilename()).toMatch(/^socket-observations-\d{4}-\d{2}-\d{2}\.json$/)
})

test("retention changes review erasure and keep recording as a separate intent", async ({
  page,
}) => {
  const mutations: Mutation[] = []
  await install(page, mutations)
  await page.goto("/network/flows")
  await loaded(page)
  await recorderMenu(page, "Sample interval and retention")
  const sheet = page.getByRole("dialog", { name: "Sample interval and retention" })
  await expect(sheet.getByRole("button", { name: "Review recording policy" })).toBeDisabled()
  await sheet.getByLabel("Retention (days)").fill("40")
  await expect(sheet.getByRole("alert")).toContainText("Retention must be 1–31 days.")
  await sheet.getByLabel("Retention (days)").fill("1")
  await sheet.getByRole("button", { name: "Review recording policy" }).click()
  await expect(page.getByRole("dialog")).toContainText("erased immediately")
  expect(mutations).toEqual([])
  await page.getByRole("button", { name: "Save policy", exact: true }).click()
  await expect.poll(() => mutations.length).toBe(1)
  expect(mutations[0]).toEqual({
    method: "PUT",
    path: "/network/flows/policy",
    body: { intervalSeconds: 30, retentionDays: 1 },
  })
})

test("erasing the record is reviewed first", async ({ page }) => {
  const mutations: Mutation[] = []
  await install(page, mutations, recordingReport)
  await page.goto("/network/flows")
  await loaded(page)
  await recorderMenu(page, "Erase retained history")
  await expect(page.getByRole("dialog")).toContainText("delete all retained socket observations")
  expect(mutations).toEqual([])
  await page.getByRole("button", { name: "Erase history", exact: true }).click()
  await expect.poll(() => mutations.length).toBe(1)
  expect(mutations[0]).toMatchObject({ method: "DELETE", path: "/network/flows/history" })
})

test("the page is three views in the address, and a node of the picture opens its view", async ({
  page,
}) => {
  await install(page, [], recordingReport)
  await page.goto("/network/flows")
  await loaded(page)
  const tabs = page.getByRole("tablist", { name: "Views of socket history" })
  await expect(tabs.getByRole("tab")).toHaveText([
    "Socket hours",
    "Kernel observer",
    "Latest sample",
  ])
  await expect(tabs.getByRole("tab", { name: "Socket hours" })).toHaveAttribute(
    "aria-selected",
    "true",
  )
  // One view at a time: the day's table and strip, and nothing of the other two.
  await expect(page.getByRole("region", { name: "Socket hours" })).toBeVisible()
  await expect(page.getByRole("region", { name: "Recorded hours" })).toBeVisible()
  await expect(page.getByRole("region", { name: "Kernel packet observer" })).toHaveCount(0)
  await expect(page.getByRole("region", { name: "Latest native sample" })).toHaveCount(0)

  await view(page, "Kernel observer")
  await expect(page).toHaveURL(/\/network\/flows\?tab=observer$/)
  await expect(page.getByRole("region", { name: "Kernel packet observer" })).toBeVisible()
  await expect(page.getByRole("region", { name: "Socket hours" })).toHaveCount(0)

  await view(page, "Latest sample")
  await expect(page).toHaveURL(/\/network\/flows\?tab=sample$/)
  await expect(page.getByRole("region", { name: "Latest native sample" })).toBeVisible()
  await expect(page.getByRole("region", { name: "Measurement boundaries" })).toBeVisible()

  // The picture is the same in every view, and its nodes are the way between them.
  const picture = page.locator('[data-slot="wire-flow"]')
  await picture.getByRole("button", { name: "Go to the recorded hours" }).click()
  await expect(page).toHaveURL(/\/network\/flows$/)
  await expect(page.getByRole("region", { name: "Recorded hours" })).toBeInViewport()
  await picture.getByRole("button", { name: "Go to the kernel observer" }).click()
  await expect(page).toHaveURL(/\/network\/flows\?tab=observer$/)
  await expect(page.getByRole("region", { name: "Kernel packet observer" })).toBeVisible()
  await picture
    .getByRole("button", { name: /^Go to the latest sample of / })
    .first()
    .click()
  await expect(page).toHaveURL(/\/network\/flows\?tab=sample$/)

  // The address opens on the view it names, and a name that is none opens the hours.
  await page.goto("/network/flows?tab=sample")
  await loaded(page)
  await expect(page.getByRole("region", { name: "Latest native sample" })).toBeVisible()
  await page.goto("/network/flows?tab=coverage")
  await loaded(page)
  await expect(page.getByRole("region", { name: "Socket hours" })).toBeVisible()
})

test("read-only role sees the privacy boundary without requesting flow metadata", async ({
  page,
}) => {
  const requests: string[] = []
  await mockNetwork(page, [], {
    session: { ...admin, capabilities: ["read"], user: { ...admin.user, role: "readonly" } },
    overrides: { [path]: { ...report, rows: [row] } },
  })
  page.on("request", (r) => {
    if (r.url().includes("/api/v1/network/flows")) requests.push(r.url())
  })
  await page.goto("/network/flows")
  await expect(
    page.getByText("Socket history needs the admin capability", { exact: true }),
  ).toBeVisible()
  await expect(
    page.getByText("Peer and descriptor-owner history can expose private workload connections."),
  ).toBeVisible()
  // No control it cannot use, and nothing of the record.
  await expect(page.locator('[data-slot="page"]').getByRole("button")).toHaveCount(0)
  await expect(page.getByText("worker", { exact: true })).toHaveCount(0)
  expect(requests).toEqual([])
})

test("the day's recorded hours are a strip that reads one hour at a time", async ({ page }) => {
  await install(page, [], recordingReport)
  await page.goto("/network/flows")
  await loaded(page)
  const hours = page.getByRole("region", { name: "Recorded hours" })
  await expect(hours).toContainText("8 of 24")
  await expect(hours.getByRole("listitem")).toHaveCount(24)
  const line = hours.locator('[data-slot="flow-hour-reading"]')
  // It opens on the newest recorded hour.
  await expect(hours.getByRole("button", { name: /^12:00 UTC, 120 samples/ })).toHaveAttribute(
    "aria-pressed",
    "true",
  )
  await expect(line).toContainText("120 samples · 0 failed source reads")
  // Where the line stands under its strip, whatever the page has scrolled to.
  const under = async () => {
    const [strip, read] = [await top(page, hours), await top(page, line)]
    return { y: read.y - strip.y, height: read.height }
  }
  const before = await under()
  await hours.getByRole("button", { name: "03:00 UTC, 120 samples, incomplete" }).click()
  await expect(line).toContainText("1 failed source reads")
  await hours.getByRole("button", { name: "05:00 UTC, not recorded" }).click()
  await expect(line).toContainText("not recorded; what happened in it remains unknown")
  expect(await under()).toEqual(before)
  await hours.getByRole("button", { name: /^10:00 UTC/ }).click()
  await expect(line).toContainText("2 skipped intervals")
  await line.getByRole("button", { name: "Retained observer gaps" }).click()
  await expect(
    page
      .getByRole("dialog")
      .getByText("Observer quality was not retained for this reading.", { exact: true }),
  ).toBeVisible()
})

test("the latest sample reads each source on a line", async ({ page }) => {
  await install(page, [], recordingReport)
  await page.goto("/network/flows?tab=sample")
  await loaded(page)
  const sample = page.getByRole("region", { name: "Latest native sample" })
  await expect(sample).toContainText("ss utility, iproute2-6.1.0")
  await expect(sample).toContainText("Docker sources observed")
  const source = sample.getByRole("row").filter({ hasText: "This server" })
  await expect(source).toContainText("observed")
  await expect(source.getByRole("cell").nth(2)).toHaveText("11")
  await expect(source.getByRole("cell").nth(3)).toHaveText("3")
  // The picture draws the same source, wired to the recorder.
  await expect(
    page.locator('[data-slot="wire-flow"]').getByRole("region", { name: "Sampled" }),
  ).toContainText("11 TCP · 3 UDP")
})

test.describe("mobile socket evidence", () => {
  test.use({ viewport: { width: 390, height: 844 } })
  test("long identities and the tuple stay inside the reading", async ({ page }) => {
    await install(page, [], { ...report, recordingSince: stamp, rows: [row], retainedRows: 1 })
    await page.goto("/network/flows")
    await loaded(page)
    expect(
      await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth),
    ).toBe(true)
    await expect(fact(reading(page), "Owner evidence")).toContainText(containerId)
    await recorderMenu(page, "Sample interval and retention")
    const sheet = page.getByRole("dialog", { name: "Sample interval and retention" })
    await sheet.getByLabel("Retention (days)").fill("1")
    await sheet.getByRole("button", { name: "Review recording policy" }).click()
    const dialog = page.getByRole("dialog")
    await expect(dialog).toContainText("erased immediately")
    expect(await dialog.evaluate((el) => el.scrollWidth <= el.clientWidth)).toBe(true)
  })
})

const observerReport: FlowReport = {
  ...report,
  status: "waiting",
  recordingSince: stamp,
  settings: { ...report.settings, enabled: true, kernelObserverEnabled: false },
  kernelObserver: {
    status: "off",
    reason: "Exact helper support is checked only on explicit attachment.",
    checkedAt: stamp,
    attachmentsRetained: false,
    ringBytes: 1048576,
    eventsPerSecond: 10000,
    socketCapacity: 4096,
    pendingCapacity: 8192,
    dockerStatus: "unavailable",
    dockerCheckedAt: stamp,
    quality: {
      events: "4",
      ringDrops: "2",
      budgetOmissions: "3",
      headerGaps: "0",
      identityGaps: "0",
      stateAdmissionGaps: "0",
      parserGaps: "0",
      byteGaps: "1",
      pendingOmissions: "0",
      attributionGaps: "4",
      readerBudgetPauses: "0",
      unsavedEvents: "0",
      timestampGaps: "4",
      shutdownTailUnknown: true,
    },
  },
}

test("kernel activation is separate, reviewed and never triggered by reading or ordinary history", async ({
  page,
}) => {
  const mutations: Mutation[] = []
  await install(page, mutations, observerReport)
  await page.goto("/network/flows")
  await loaded(page)
  await recorderMenu(page, "Inspect again")
  await page.getByRole("button", { name: "Read period" }).click()
  expect(mutations).toEqual([])
  await view(page, "Kernel observer")
  await page.getByRole("button", { name: "Review observer activation" }).click()
  await expect(page.getByRole("dialog")).toContainText("bounded event and storage budgets")
  expect(mutations).toEqual([])
  await page.getByRole("button", { name: "Activate observer", exact: true }).click()
  await expect.poll(() => mutations.length).toBe(1)
  expect(mutations[0]).toEqual({
    method: "POST",
    path: "/network/flows/observer",
    body: { enabled: true },
  })
  await page.reload()
  await loaded(page)
  expect(mutations).toHaveLength(1)
})

test("observer needs history first and remains unavailable to an admin without destructive capability", async ({
  page,
}) => {
  await install(page, [], {
    ...observerReport,
    settings: { ...observerReport.settings, enabled: false },
  })
  await page.goto("/network/flows?tab=observer")
  await loaded(page)
  await expect(page.getByRole("button", { name: "Review observer activation" })).toBeDisabled()
  await expect(
    page.getByText("Start history recording before reviewing observer activation.", {
      exact: true,
    }),
  ).toBeVisible()
  await mockNetwork(page, [], {
    session: { ...admin, capabilities: ["system.admin", "read"] },
    overrides: { [path]: observerReport },
  })
  await page.reload()
  await loaded(page)
  await expect(page.getByRole("button", { name: "Review observer activation" })).toHaveCount(0)
  // Nor the two other verbs the server keeps for an account that may destroy.
  await page.getByRole("button", { name: "More recorder actions" }).click()
  await expect(page.getByRole("menuitem", { name: "Inspect again" })).toBeVisible()
  await expect(page.getByRole("menuitem", { name: "Sample interval and retention" })).toHaveCount(0)
  await expect(page.getByRole("menuitem", { name: "Erase retained history" })).toHaveCount(0)
})

test("retained partial attachments survive a failed stop and offer a truthful retry", async ({
  page,
}) => {
  const active: FlowReport = {
    ...observerReport,
    settings: { ...observerReport.settings, kernelObserverEnabled: true },
    kernelObserver: {
      ...observerReport.kernelObserver,
      status: "partial",
      attachmentsRetained: true,
      reason: "Owned link detach needs retry.",
    },
  }
  await install(page, [], active)
  await page.route("**/api/v1/network/flows/observer", (route) =>
    json(route, { error: { message: "Detach failed; owned links remain" } }, 503),
  )
  await page.goto("/network/flows?tab=observer")
  await loaded(page)
  // Ordinary recording cannot stop under an attached observer, and the page says why.
  await expect(page.getByRole("button", { name: "Stop recording" })).toBeDisabled()
  await expect(
    page.getByText("Stop the kernel observer before stopping ordinary history recording."),
  ).toBeVisible()
  await page.getByRole("button", { name: "Review observer stop" }).click()
  await page.getByRole("button", { name: "Stop observer", exact: true }).click()
  await expect(page.getByText("Partial observer coverage", { exact: true })).toBeVisible()
  await expect(page.getByText("Owned attachments remain.", { exact: false })).toBeVisible()
  await expect(
    page.getByText("Detach failed; owned links remain Inspect observer state before trying again."),
  ).toBeVisible()
  await page.getByRole("button", { name: "Cancel", exact: true }).click()
  await expect(page.getByRole("button", { name: "Review observer stop" })).toBeEnabled()
  await expect(page.getByText("Observer off", { exact: true })).toHaveCount(0)
})

test("failed refresh blocks observer activation and a review already open", async ({ page }) => {
  const mutations: Mutation[] = []
  await install(page, mutations, observerReport)
  await page.clock.install()
  await page.goto("/network/flows?tab=observer")
  await loaded(page)
  await page.getByRole("button", { name: "Review observer activation" }).click()
  await page.route("**/api/v1/network/flows/?*", (route) =>
    json(route, { error: { message: "Stored state unavailable" } }, 503),
  )
  // The explicit background poll can fail while a review remains open.
  await page.clock.fastForward(30001)
  await expect(page.getByText("Last known observer state", { exact: true })).toBeVisible()
  await page.getByRole("button", { name: "Activate observer", exact: true }).click()
  expect(mutations).toEqual([])
  await page.getByRole("button", { name: "Cancel", exact: true }).click()
  await expect(page.getByRole("button", { name: "Review observer activation" })).toBeDisabled()
})

const kernel: FlowRow = {
  ...row,
  id: "kernel-cookie-owner",
  evidence: "kernel_transport_payload_observed",
  observedTxBytes: "131072",
  observedRxBytes: null,
  observedPackets: "4",
  observedTxPackets: "3",
  observedRxPackets: "1",
  observedTxKnownPackets: "2",
  observedRxKnownPackets: "0",
  observedTxByteGaps: "1",
  observedRxByteGaps: "1",
  observedSyn: "1",
  observedFin: "1",
  observedRst: "0",
  timestampUncertain: true,
}
const kernelUdp: FlowRow = {
  ...kernel,
  id: "kernel-udp",
  socket: { ...kernel.socket, protocol: "udp" },
  observedTxBytes: "257",
}

test("kernel subtotals keep TCP channels separate, show UDP bytes and retain dated gaps", async ({
  page,
}) => {
  await install(page, [], { ...observerReport, rows: [row, kernel, kernelUdp] })
  await page.goto("/network/flows")
  await loaded(page)
  const table = page.getByRole("region", { name: "Socket hours" })
  const total = (name: string) =>
    table.locator('[data-slot="flow-total"]').filter({ hasText: name })
  // The native total counts the one native TCP row and none of the kernel's bytes.
  await expect(total("TCP sent")).toContainText("128.0 KiB")
  await expect(total("TCP sent")).toHaveAttribute("title", "1 rows with measured sent deltas")
  await expect(total("Kernel sent")).toContainText("128.3 KiB")
  await expect(total("Kernel received")).toContainText("Unknown")
  await expect(total("Byte gaps")).toContainText("2 sent · 2 received")
  await expect(table.getByRole("cell", { name: "Kernel payload", exact: true })).toHaveCount(2)
  const udp = table.getByRole("row").filter({ hasText: "UDP" })
  await expect(udp.getByText("257 B", { exact: true })).toBeVisible()
  await expect(udp.getByText("Unknown", { exact: true })).toBeVisible()
  await udp.getByRole("button", { name: "worker" }).click()
  await expect(reading(page)).toContainText("Kernel transport payload")
  await expect(fact(reading(page), "Sent subtotal")).toContainText("257 B")
  await expect(fact(reading(page), "Received subtotal")).toContainText("Unknown")
  await expect(fact(reading(page), "Observed flags")).toContainText(
    "SYN 1 · FIN 1 · RST 0; lifecycle completeness unknown",
  )
  await expect(fact(reading(page), "Window")).toContainText(
    "UTC timestamp uncertain; inspect observer clock evidence",
  )
  await expect(fact(reading(page), "Sent known lengths")).toContainText("2/3 · byte gaps 1")
  await view(page, "Kernel observer")
  await page.getByRole("button", { name: "Observer bounds and retained quality" }).click()
  await expect(page.getByText("Ring delivery drops", { exact: true })).toBeVisible()
  await expect(page.locator(`time[datetime="${stamp}"]`).first()).toBeVisible()
  await expect(
    page.getByText("The final events at shutdown remain unknown.", { exact: true }),
  ).toBeVisible()
})

test("interrupted restart needs a new explicit opt-in and historical IDs do not imply retained attachments", async ({
  page,
}) => {
  const mutations: Mutation[] = []
  await install(page, mutations, {
    ...observerReport,
    kernelObserver: {
      ...observerReport.kernelObserver,
      status: "interrupted",
      programIds: [123],
      linkIds: [456],
      reason: "Previous process ended; explicit opt-in required.",
    },
  })
  await page.goto("/network/flows?tab=observer")
  await loaded(page)
  await expect(page.getByText("Previous observer interrupted", { exact: true })).toBeVisible()
  await expect(page.getByRole("button", { name: "Review observer activation" })).toBeEnabled()
  await expect(page.getByRole("button", { name: "Review observer stop" })).toHaveCount(0)
  expect(mutations).toEqual([])
})

for (const width of [390, 1280, 1720]) {
  test(`observer evidence and its review stay within ${width}px`, async ({ page }) => {
    await page.setViewportSize({ width, height: 960 })
    await install(page, [], {
      ...observerReport,
      kernelObserver: {
        ...observerReport.kernelObserver,
        digest: "a".repeat(64),
        bootId: "b".repeat(64),
        targetCgroup: "/" + "long-fixture-owner-".repeat(16),
        quality: { ...observerReport.kernelObserver.quality!, ringDrops: "9007199254740993" },
      },
    })
    await page.goto("/network/flows?tab=observer")
    await loaded(page)
    await page.getByRole("button", { name: "Observer bounds and retained quality" }).click()
    expect(
      await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth),
    ).toBe(true)
    await page.getByRole("button", { name: "Review observer activation" }).click()
    const dialog = page.getByRole("dialog")
    await expect(dialog).toBeVisible()
    expect(await dialog.evaluate((el) => el.scrollWidth <= el.clientWidth)).toBe(true)
  })
}

for (const width of [1280, 390]) {
  test(`socket hours keep one height whatever measured them at ${width}px`, async ({ page }) => {
    await page.setViewportSize({ width, height: 900 })
    // A native TCP row, a UDP row with no bytes, and two kernel rows with their gaps.
    const rows = [row, udpRow, kernel, kernelUdp]
    await install(page, [], { ...recordingReport, rows })
    await page.goto("/network/flows")
    await loaded(page)
    await risen(page)
    expect(
      await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth),
    ).toBe(true)
    const table = page.getByRole("region", { name: "Socket hours" })
    const lines =
      width >= 1024
        ? table.locator("tbody tr")
        : table.getByRole("list", { name: "Socket hours" }).getByRole("listitem")
    await expect(lines).toHaveCount(rows.length)
    const boxes = await lines.evaluateAll((items) =>
      items.map((item) => item.getBoundingClientRect()).map((b) => ({ y: b.y, height: b.height })),
    )
    for (const [i, box] of boxes.entries()) {
      expect(box.height).toBe(boxes[0].height)
      if (i > 0) expect(box.y).toBeCloseTo(boxes[i - 1].y + boxes[i - 1].height, 0)
    }
  })

  test(`the opening holds its place while the record loads at ${width}px`, async ({ page }) => {
    await page.setViewportSize({ width, height: 900 })
    await mockNetwork(page)
    let release!: () => void
    const held = new Promise<void>((resolve) => {
      release = resolve
    })
    await page.route("**/api/v1/network/flows/?*", async (route) => {
      await held
      return json(route, recordingReport)
    })
    await page.goto("/network/flows")
    const identity = page.getByRole("region", { name: "About socket history" })
    const picture = page.locator('[data-slot="wire-flow"]')
    const table = page.getByRole("region", { name: "Socket hours" })
    await expect(picture.locator('[data-slot="skeleton"]').first()).toBeVisible()
    // The waiting picture is the shared one's own, not grey nodes of this page's.
    await expect(picture).toHaveAttribute("data-loading", "true")
    const before = {
      identity: await top(page, identity),
      picture: await top(page, picture),
      table: await top(page, table),
    }
    release()
    await loaded(page)
    const after = {
      identity: await top(page, identity),
      picture: await top(page, picture),
      table: await top(page, table),
    }
    expect(after.identity).toEqual(before.identity)
    expect(after.picture).toEqual(before.picture)
    expect(after.table.y).toBe(before.table.y)
  })
}
