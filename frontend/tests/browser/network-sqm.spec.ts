import { expect, test, type Page } from "@playwright/test"
import { admin, json, mockNetwork } from "./network-fixture"
import type { ShapingView } from "../../src/lib/types"

const verified = { status: "verified" as const, checkedAt: "2026-10-08T12:00:00Z" }
const queue = {
  kind: "cake",
  packets: 5056,
  bytes: 7274228,
  drops: 392,
  overlimits: 8875,
  requeues: 0,
  backlog: 0,
}
const profile = {
  diffserv: "besteffort" as const,
  flowMode: "dual-dsthost" as const,
  nat: false,
  preserveDscp: false,
  overhead: 22,
  mpu: 64,
  linkLayer: "noatm" as const,
  rttMillis: 100,
}
const shaping: ShapingView = {
  devices: [
    {
      name: "ens3",
      kind: "physical",
      root: { ...queue, kind: "fq_codel" },
      leaf: null,
      ingress: true,
      managed: true,
      qdisc: "fq_codel",
      egressKbit: 0,
      ingressKbit: 9000,
      uplink: true,
      clientPath: false,
      shapeable: true,
      guard: "",
      verification: verified,
      sqm: { ...profile, ifb: "jds0123456789ab", queue, helper: verified, boot: verified },
    },
    {
      name: "jds0123456789ab",
      kind: "ifb",
      root: queue,
      leaf: null,
      ingress: false,
      managed: false,
      qdisc: "",
      egressKbit: 0,
      ingressKbit: 0,
      uplink: false,
      clientPath: false,
      shapeable: false,
      guard: "Managed download buffer; edit its source interface.",
    },
  ],
  bbr: {
    active: false,
    available: true,
    algorithms: ["cubic", "bbr"],
    congestion: "cubic",
    defaultQdisc: "fq_codel",
    managed: false,
  },
  qdiscs: ["fq_codel", "cake", "fq"],
}

async function mockSQM(page: Page, reader = false) {
  await mockNetwork(page, [], {
    session: reader
      ? { ...admin, capabilities: ["read"], user: { ...admin.user, role: "readonly" } }
      : admin,
    overrides: {
      "/network/shaping": shaping,
      "/network/traffic/processes": { enabled: false, programs: [], totals: {} },
      "/network/traffic/containers": { containers: [], totals: {}, available: false },
      "/network/traffic/live": { now: 0, series: {} },
      "/network/ebpf": { installed: false, programs: [], attachments: [], maps: [] },
      // Explicit SQM requests require pending mode even when the global
      // optional pending preference cannot be enabled by this fixture.
      "/network/changes/current": { available: false, owned: false, change: null },
    },
  })
  await page.goto("/network/traffic")
  await expect(page.getByText("Download SQM · CAKE on", { exact: false })).toBeVisible()
}

test("read users inspect SQM and its native evidence without mutation controls", async ({
  page,
}) => {
  await mockSQM(page, true)
  await expect(page.getByText(/IFB: 5,056 packets/)).toBeVisible()
  await expect(page.getByText(/loaded boot command verified/)).toBeVisible()
  await expect(page.getByRole("button", { name: /Edit the limits/ })).toHaveCount(0)
  await expect(page.getByRole("button", { name: /Clear the limits/ })).toHaveCount(0)
  await expect(page.getByRole("switch", { name: /BBR/ })).toBeDisabled()
})

test("download SQM sends only the bounded operator profile and requires pending apply", async ({
  page,
}) => {
  await mockSQM(page)
  let body: unknown
  let applyHeader: string | undefined
  await page.route("**/api/v1/network/shaping/ens3", async (route) => {
    body = route.request().postDataJSON()
    applyHeader = route.request().headers()["x-jd-network-apply"]
    await json(route, shaping.devices[0])
  })
  await page.getByRole("button", { name: "Edit the limits on ens3" }).click()
  await page.getByLabel("Download limit", { exact: true }).fill("8")
  await page.getByRole("button", { name: "Apply", exact: true }).click()
  await expect(page.getByRole("dialog")).toHaveCount(0)
  expect(body).toEqual({ qdisc: "fq_codel", egressKbit: 0, ingressKbit: 8000, sqm: profile })
  expect(applyHeader).toBe("pending")
})

for (const status of [409, 500]) {
  test(`SQM retains the exact draft after a ${status} mutation failure`, async ({ page }) => {
    await mockSQM(page)
    await page.route("**/api/v1/network/shaping/ens3", (route) =>
      json(
        route,
        { error: { code: "conflict", message: "The IFB owner could not be verified." } },
        status,
      ),
    )
    await page.getByRole("button", { name: "Edit the limits on ens3" }).click()
    await page.getByLabel("Download limit", { exact: true }).fill("8.5")
    await page.getByLabel("Overhead bytes", { exact: true }).fill("30")
    await page.getByRole("button", { name: "Apply", exact: true }).click()
    await expect(page.getByRole("alert")).toContainText("The IFB owner could not be verified.")
    await expect(page.getByLabel("Download limit", { exact: true })).toHaveValue("8.5")
    await expect(page.getByLabel("Overhead bytes", { exact: true })).toHaveValue("30")
    await expect(page.getByRole("switch", { name: "Use download SQM", exact: true })).toBeChecked()
  })
}

test("a failed later queue poll retains evidence and blocks changes until retry succeeds", async ({
  page,
}) => {
  await page.clock.install()
  await mockSQM(page)
  let failed = true
  await page.route("**/api/v1/network/shaping", (route) =>
    failed
      ? json(route, { error: { code: "read_failed", message: "tc ownership read failed" } }, 500)
      : json(route, shaping),
  )
  await page.getByRole("button", { name: "Edit the limits on ens3" }).click()
  await page.clock.fastForward(16000)
  await expect(page.getByText(/tc ownership read failed/)).toBeVisible()
  await expect(page.getByText(/Last successful read/)).toBeVisible()
  await expect(page.getByRole("button", { name: "Apply", exact: true })).toBeDisabled()
  await expect(page.getByText(/IFB: 5,056 packets/)).toBeVisible()
  await page.getByRole("button", { name: "Cancel", exact: true }).click()
  failed = false
  await page.getByRole("button", { name: "Refresh", exact: true }).click()
  await expect(page.getByRole("button", { name: "Edit the limits on ens3" })).toBeEnabled()
})

test("SQM profile validation and the sheet fit a phone", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 })
  await mockSQM(page)
  await page.getByRole("button", { name: "Edit the limits on ens3" }).click()
  await page.getByLabel("Overhead bytes", { exact: true }).fill("22.5")
  await expect(page.getByRole("button", { name: "Apply", exact: true })).toBeDisabled()
  await expect(page.getByRole("alert")).toContainText("whole number")
  await page.getByLabel("Overhead bytes", { exact: true }).fill("22")
  await expect(page.getByRole("button", { name: "Apply", exact: true })).toBeEnabled()
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(
    true,
  )
})
