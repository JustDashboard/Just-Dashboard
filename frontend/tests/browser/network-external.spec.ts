import { expect, test, type Page } from "@playwright/test"
import { admin, json, mockNetwork } from "./network-fixture"
import type { ExternalCheck, ExternalVantage } from "../../src/lib/network-external"

const vantage: ExternalVantage = {
  id: "a".repeat(32),
  name: "Controlled source A",
  location: "Declared region A",
  placement: "controlled_fixture",
  scopes: [
    {
      id: "service",
      target: "service.example",
      addresses: ["192.0.2.8", "2001:db8::8"],
      ports: [443],
      families: ["inet", "inet6"],
    },
  ],
  createdAt: "2026-10-08T10:00:00Z",
  enrolledAt: "2026-10-08T10:01:00Z",
  lastSeen: "2026-10-08T10:02:00Z",
}
const queued = (id: string, family: "inet" | "inet6" = "inet"): ExternalCheck => ({
  id,
  vantageId: vantage.id,
  request: { vantageId: vantage.id, scopeId: "service", family, port: 443, tls: true },
  status: "queued",
  createdAt: "2026-10-08T10:03:00Z",
  expiresAt: "2026-10-08T10:05:00Z",
  startedBy: "operator",
})
function measured(id: string, family: "inet" | "inet6"): ExternalCheck {
  const row = queued(id, family)
  const address = family === "inet" ? "192.0.2.8" : "2001:db8::8"
  return {
    ...row,
    status: "completed",
    completedAt: "2026-10-08T10:04:00Z",
    result: {
      checkId: id,
      vantageId: vantage.id,
      request: row.request,
      target: "service.example",
      addresses: [address],
      address,
      sourceAddress: family === "inet" ? "192.0.2.3" : "2001:db8::3",
      startedAt: "2026-10-08T10:03:01Z",
      endedAt: "2026-10-08T10:03:02Z",
      stages: ["dns", "tcp", "tls"].map((name, i) => ({
        name: name as "dns" | "tcp" | "tls",
        basis: "measured",
        state: ["resolved", "connected", "verified"][i],
        startedAt: "2026-10-08T10:03:01Z",
        endedAt: "2026-10-08T10:03:02Z",
        durationMs: 50,
        detail: "Signed fixture measurement from this source at this time",
      })),
      limitations: [
        "Operator-declared placement remains unverified; this fixture is not off-host acceptance.",
      ],
    },
  }
}
async function setup(page: Page, rows: ExternalCheck[] = [], readonly = false) {
  await mockNetwork(page, [], {
    session: readonly
      ? { ...admin, capabilities: ["read"], user: { ...admin.user, role: "readonly" } }
      : admin,
  })
  const state = { rows, unavailable: false }
  await page.route("**/api/v1/network/external/vantages", (route) => json(route, [vantage]))
  await page.route("**/api/v1/network/external/checks", (route) => {
    if (route.request().method() === "GET")
      return state.unavailable ? route.abort("connectionfailed") : json(route, state.rows)
    const row = { ...queued("new-check"), request: route.request().postDataJSON() }
    state.rows = [row, ...state.rows]
    return json(route, row, 202)
  })
  return state
}
async function select(page: Page, label: string, name: string) {
  await page.getByLabel(label, { exact: true }).click()
  await page.getByRole("option", { name, exact: true }).click()
}

test("scoped queue acceptance stays unknown and cancellation rejects future acceptance", async ({
  page,
}) => {
  const state = await setup(page)
  let cancelled = false
  await page.route("**/api/v1/network/external/checks/new-check/cancel", (route) => {
    cancelled = true
    state.rows = state.rows.map((row) => ({ ...row, status: "cancelled" }))
    return json(route, { cancelled: true, lateResultsRejected: true })
  })
  await page.goto("/network/external")
  await select(page, "Controlled source", "Controlled source A · Declared region A")
  await expect(page.getByText("Approved destinations:", { exact: false })).toContainText(
    "192.0.2.8",
  )
  await page.getByRole("button", { name: "Queue bounded check" }).click()
  await expect(page.getByRole("row").filter({ hasText: "queued" })).toContainText("Unknown")
  expect(state.rows[0].request).toEqual({
    vantageId: vantage.id,
    scopeId: "service",
    family: "inet",
    port: 443,
    tls: true,
  })
  await expect(
    page.getByText(
      "No accepted measurement. Queue, expiry or cancellation states do not establish service reachability.",
    ),
  ).toBeVisible()
  await page.getByRole("button", { name: "Cancel acceptance" }).click()
  await expect.poll(() => cancelled).toBe(true)
  await expect(page.getByRole("row").filter({ hasText: "cancelled" })).toContainText("Unknown")
})

test("signed measurements preserve family and scope through a failed refresh", async ({ page }) => {
  const state = await setup(page, [measured("v4", "inet"), measured("v6", "inet6")])
  await page.goto("/network/external")
  await expect(page.getByRole("row").filter({ hasText: "IPv4" })).toContainText("Connected")
  await expect(page.getByRole("row").filter({ hasText: "IPv6" })).toContainText("Verified")
  await page
    .getByRole("row")
    .filter({ hasText: "IPv6" })
    .getByRole("button", { name: "Controlled source A" })
    .click()
  await expect(page.getByText("2001:db8::3 → 2001:db8::8:443")).toBeVisible()
  state.unavailable = true
  await page.waitForTimeout(5200)
  await expect(page.getByText("Showing the last known network state")).toBeVisible()
  await expect(page.getByRole("row").filter({ hasText: "IPv6" })).toContainText("Verified")
})

test("an expired or offline source cannot imply a measured failure", async ({ page }) => {
  await setup(page, [{ ...queued("expired"), status: "expired" }])
  await page.goto("/network/external")
  const row = page.getByRole("row").filter({ hasText: "expired" })
  await expect(row).toContainText("No accepted measurement")
  await expect(row.getByText("Unknown", { exact: true })).toHaveCount(3)
  await expect(row.getByText("Failed", { exact: true })).toHaveCount(0)
})

test("one-use enrollment is shown once and keeps a failed draft", async ({ page }) => {
  await setup(page)
  let refused = true
  let submitted: unknown
  await page.route("**/api/v1/network/external/enrollments", (route) => {
    submitted = route.request().postDataJSON()
    if (refused)
      return json(
        route,
        { error: { code: "invalid_scope", message: "Approve both families before enrollment" } },
        400,
      )
    return json(
      route,
      {
        vantage,
        token: "b".repeat(64),
        expiresAt: new Date(Date.now() + 600000).toISOString(),
        serverKey: "public-server-key",
      },
      201,
    )
  })
  await page.goto("/network/external")
  await page.getByText("Enroll a controlled source", { exact: true }).click()
  await page.getByLabel("Source name", { exact: true }).fill("New region")
  await page.getByLabel("Service name or address", { exact: true }).fill("service.example")
  await page.getByLabel("Approved literal addresses", { exact: true }).fill("192.0.2.8")
  await page.getByRole("button", { name: "Create one-use enrollment" }).click()
  await expect(page.getByRole("alert").filter({ hasText: "Approve both families" })).toBeVisible()
  await expect(page.getByLabel("Source name", { exact: true })).toHaveValue("New region")
  refused = false
  await page.getByRole("button", { name: "Create one-use enrollment" }).click()
  expect(submitted).toMatchObject({
    name: "New region",
    scopes: [
      { target: "service.example", addresses: ["192.0.2.8"], ports: [443], families: ["inet"] },
    ],
  })
  await expect(page.getByLabel("One-use enrollment token", { exact: true })).toHaveValue(
    "b".repeat(64),
  )
  await page.getByRole("button", { name: "Dismiss credential" }).click()
  await expect(page.getByTestId("external-enrollment")).toHaveCount(0)
  await page.reload()
  await expect(page.getByTestId("external-enrollment")).toHaveCount(0)
})

test("readonly accounts cannot queue checks or create machine credentials", async ({ page }) => {
  await setup(page, [], true)
  await page.goto("/network/external")
  await expect(
    page.getByText(
      "External measurements and agent management require system administrator access.",
    ),
  ).toBeVisible()
  await expect(page.getByRole("button", { name: "Queue bounded check" })).toBeDisabled()
  await page.getByText("Enroll a controlled source", { exact: true }).click()
  await expect(page.getByRole("button", { name: "Create one-use enrollment" })).toBeDisabled()
})

test("paired IPv4 and IPv6 evidence retains source and provider limits", async ({ page }) => {
  await setup(page, [measured("v4", "inet"), measured("v6", "inet6")])
  let pair: URL | undefined
  await page.route("**/api/v1/network/external/compare?**", (route) => {
    pair = new URL(route.request().url())
    return json(route, {
      before: measured("v4", "inet"),
      after: measured("v6", "inet6"),
      comparison:
        "Different sources/families are different paths; geography and provider causes remain unverified.",
    })
  })
  await page.goto("/network/external")
  await page.getByLabel("First measurement", { exact: true }).click()
  await page.getByRole("option").filter({ hasText: "IPv4" }).click()
  await page.getByLabel("Second measurement", { exact: true }).click()
  await page.getByRole("option").filter({ hasText: "IPv6" }).click()
  await page.getByRole("button", { name: "Compare retained measurements" }).click()
  await expect(
    page.getByRole("status").filter({ hasText: "provider causes remain unverified" }),
  ).toBeVisible()
  expect(pair?.searchParams.get("before")).toBe("v4")
  expect(pair?.searchParams.get("after")).toBe("v6")
})
