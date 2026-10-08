import { expect, test } from "@playwright/test"
import { admin, json, loaded, mockNetwork, type Mutation } from "./network-fixture"
import type { FlowReport, FlowRow } from "../../src/lib/network-flows"

const stamp = "2026-10-08T12:00:00Z"
const containerId = "a".repeat(64)
const row: FlowRow = {
  id: "native-cookie-owner",
  hour: "2026-10-07T12:00:00Z",
  firstSeen: "2026-10-07T12:00:00Z",
  lastSeen: "2026-10-07T12:00:30Z",
  sourceId: containerId,
  sourceName: "fixture",
  namespace: "4:123",
  bootId: "fixture-boot",
  socket: {
    protocol: "tcp",
    state: "ESTAB",
    localAddress: "172.18.0.2",
    localEndpoint: "172.18.0.2:1234",
    localPort: 1234,
    remoteAddress: "192.0.2.1",
    remoteEndpoint: "192.0.2.1:443",
    remotePort: 443,
    cookie: "abcd",
    inode: "999",
    owner: {
      status: "verified_container",
      containerId,
      containerName: "worker",
      containerStartedAt: "2026-10-07T01:00:00Z",
    },
  },
  samples: 2,
  txBytes: "131072",
  rxBytes: "4096",
  retransmissions: null,
  lostGaugeMax: null,
  measuredIntervals: 1,
  txIntervals: 1,
  rxIntervals: 1,
  retransIntervals: 0,
  skippedIntervals: 1,
}
const report: FlowReport = {
  checkedAt: stamp,
  from: "2026-10-07T00:00:00Z",
  to: "2026-10-08T00:00:00Z",
  status: "off",
  settings: { enabled: false, intervalSeconds: 30, retentionDays: 7 },
  recordingSince: null,
  collectorStartedAt: stamp,
  lastCycle: null,
  rows: [],
  coverageHours: [],
  truncated: false,
  coverageTruncated: false,
  retainedRows: 0,
  retainedBytes: 0,
  retainedFrom: null,
  prunedRows: 0,
  coverage: ["Native TCP snapshots; first sight and restart do not contribute lifetime bytes."],
  kernelObserver: {
    status: "unavailable",
    reason: "UDP bytes, short-lived flows and dropped-event counts remain unknown.",
  },
}
const path = "/network/flows/"

async function install(
  page: Parameters<typeof mockNetwork>[0],
  mutations: Mutation[] = [],
  data = report,
) {
  await mockNetwork(page, mutations, { overrides: { [path]: data } })
}

test("history stays opt-in and has unknown totals before recording", async ({ page }) => {
  const mutations: Mutation[] = []
  await install(page, mutations)
  await page.goto("/network/flows")
  await loaded(page)
  await expect(page.getByText("Recorder off", { exact: true })).toBeVisible()
  await expect(page.getByText("Recording has not been enabled.", { exact: false })).toBeVisible()
  await expect(
    page.getByText("No contact or bandwidth conclusion can be drawn.", { exact: false }),
  ).toBeVisible()
  for (const label of ["Displayed TCP sent", "Displayed TCP received", "Displayed retransmits"])
    await expect(page.locator('[data-slot="stat-tile"]').filter({ hasText: label })).toContainText(
      "Unknown",
    )
  expect(mutations).toEqual([])
  await page.getByRole("button", { name: "Start recording" }).click()
  await expect.poll(() => mutations.length).toBe(1)
  expect(mutations[0]).toEqual({
    method: "POST",
    path: "/network/flows/recording",
    body: { enabled: true },
  })
})

test("full tuple and container proof keep UDP bytes and loss unknown", async ({ page }) => {
  const udp: FlowRow = {
    ...row,
    id: "udp",
    socket: { ...row.socket, protocol: "udp", remoteEndpoint: "192.0.2.1:53", remotePort: 53 },
    txBytes: null,
    rxBytes: null,
    retransmissions: null,
    lostGaugeMax: null,
    measuredIntervals: 0,
    txIntervals: 0,
    rxIntervals: 0,
    retransIntervals: 0,
  }
  await install(page, [], {
    ...report,
    recordingSince: stamp,
    rows: [row, udp],
    retainedRows: 2,
    retainedFrom: row.hour,
    truncated: true,
  })
  await page.goto("/network/flows")
  await loaded(page)
  await expect(page.getByText("192.0.2.1:443", { exact: false })).toBeVisible()
  await expect(page.getByText(containerId, { exact: true }).first()).toBeVisible()
  await expect(page.getByText("Unavailable for UDP", { exact: false })).toHaveCount(2)
  await expect(page.getByText("Outstanding-loss gauge max Unknown")).toHaveCount(2)
  await expect(page.getByText("Displayed rows are capped", { exact: true })).toBeVisible()
  await expect(page.getByText("128.0 KiB", { exact: true })).toBeVisible()
})

test("failed refresh retains dates and blocks recorder mutations and exports", async ({ page }) => {
  await install(page, [], { ...report, rows: [row] })
  await page.goto("/network/flows")
  await loaded(page)
  await page.route("**/api/v1/network/flows/**", (route) =>
    json(route, { error: { message: "Storage reading unavailable" } }, 503),
  )
  await page.getByRole("button", { name: "Inspect again" }).click()
  await expect(page.getByText("Last known recorder state", { exact: true })).toBeVisible()
  await expect(page.getByRole("button", { name: "Start recording" })).toBeDisabled()
  await expect(page.getByRole("button", { name: "Export bounded JSON" })).toBeDisabled()
  await expect(page.locator(`time[datetime="${stamp}"]`).first()).toBeVisible()
  await expect(page.getByRole("button", { name: "Refresh", exact: true })).toBeVisible()
})

test("period filters send UTC boundaries and full container identity", async ({ page }) => {
  await install(page)
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
})

test("retention changes review erasure and keep recording as a separate intent", async ({
  page,
}) => {
  const mutations: Mutation[] = []
  await install(page, mutations)
  await page.goto("/network/flows")
  await loaded(page)
  await page.getByRole("button", { name: "Sample interval and retention" }).click()
  await page.getByLabel("Retention (days)").fill("1")
  await page.getByRole("button", { name: "Review recording policy" }).click()
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
  await expect(page.getByRole("button", { name: "Start recording" })).toHaveCount(0)
  await expect(page.getByText("worker", { exact: true })).toHaveCount(0)
  expect(requests).toEqual([])
})

test.describe("mobile socket evidence", () => {
  test.use({ viewport: { width: 390, height: 844 } })
  test("long identities and tuple table scroll inside the reading", async ({ page }) => {
    await install(page, [], { ...report, recordingSince: stamp, rows: [row], retainedRows: 1 })
    await page.goto("/network/flows")
    await loaded(page)
    expect(
      await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth),
    ).toBe(true)
    await page.getByRole("button", { name: "Sample interval and retention" }).click()
    await page.getByLabel("Retention (days)").fill("1")
    await page.getByRole("button", { name: "Review recording policy" }).click()
    const dialog = page.getByRole("dialog")
    await expect(dialog).toBeVisible()
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
  await page.getByRole("button", { name: "Inspect again" }).click()
  await page.getByRole("button", { name: "Read period" }).click()
  expect(mutations).toEqual([])
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
  await page.goto("/network/flows")
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
  await page.goto("/network/flows")
  await loaded(page)
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
  await page.goto("/network/flows")
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

test("kernel subtotals keep TCP channels separate, show UDP bytes and retain dated gaps", async ({
  page,
}) => {
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
  await install(page, [], {
    ...observerReport,
    rows: [
      row,
      kernel,
      {
        ...kernel,
        id: "kernel-udp",
        socket: { ...kernel.socket, protocol: "udp" },
        observedTxBytes: "257",
      },
    ],
  })
  await page.goto("/network/flows")
  await loaded(page)
  await expect(
    page.locator('[data-slot="stat-tile"]').filter({ hasText: "Displayed TCP sent" }),
  ).toContainText("128.0 KiB")
  await expect(page.getByText("Kernel transport payload", { exact: true })).toHaveCount(2)
  await expect(page.getByText("Sent subtotal 257 B", { exact: true })).toBeVisible()
  await expect(
    page.getByText("UTC timestamp uncertain; inspect observer clock evidence", { exact: true }),
  ).toHaveCount(2)
  await expect(page.getByText("Received subtotal Unknown", { exact: true })).toHaveCount(2)
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
  await page.goto("/network/flows")
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
    await page.goto("/network/flows")
    await loaded(page)
    await page.getByRole("button", { name: "Observer bounds and retained quality" }).click()
    expect(
      await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth),
    ).toBe(true)
    if (process.env.JD_NETWORK_SCREENSHOTS)
      await page.screenshot({
        path: `${process.env.JD_NETWORK_SCREENSHOTS}/kernel-observer-${width}.png`,
        fullPage: true,
      })
    if (process.env.JD_NETWORK_SCREENSHOTS) {
      await page.getByText("Ring delivery drops", { exact: true }).scrollIntoViewIfNeeded()
      await page.screenshot({
        path: `${process.env.JD_NETWORK_SCREENSHOTS}/kernel-observer-quality-${width}.png`,
        fullPage: true,
      })
    }
    await page.getByRole("button", { name: "Review observer activation" }).click()
    const dialog = page.getByRole("dialog")
    await expect(dialog).toBeVisible()
    expect(await dialog.evaluate((el) => el.scrollWidth <= el.clientWidth)).toBe(true)
  })
}
