import { expect, test, type Locator, type Page } from "@playwright/test"
import { admin, json, loaded, mockNetwork, type Mutation } from "./network-fixture"
import { checks, measured, queued, vantage, vantages } from "./network-evidence-fixture"
import { risen } from "./risen"
import type { ExternalCheck, ExternalVantage } from "../../src/lib/network-external"

const readonly = { ...admin, capabilities: ["read"], user: { ...admin.user, role: "readonly" } }

async function setup(
  page: Page,
  rows: ExternalCheck[] = [],
  options: { session?: typeof admin; sources?: ExternalVantage[]; mutations?: Mutation[] } = {},
) {
  await mockNetwork(page, options.mutations ?? [], { session: options.session ?? admin })
  const state = { rows, unavailable: false }
  await page.route("**/api/v1/network/external/vantages", (route) =>
    json(route, options.sources ?? [vantage]),
  )
  await page.route("**/api/v1/network/external/checks", (route) => {
    if (route.request().method() === "GET")
      return state.unavailable ? route.abort("connectionfailed") : json(route, state.rows)
    const row = { ...queued("new-check"), request: route.request().postDataJSON() }
    state.rows = [row, ...state.rows]
    return json(route, row, 202)
  })
  return state
}
async function select(scope: Locator, label: string, name: string) {
  await scope.getByLabel(label, { exact: true }).click()
  await scope.page().getByRole("option", { name, exact: true }).click()
}
const top = async (page: Page, locator: Locator) => {
  await risen(page)
  return (await locator.boundingBox())!
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
  await page.getByRole("button", { name: "Run a check" }).click()
  const sheet = page.getByRole("dialog", { name: "Run a check" })
  await select(sheet, "Controlled source", "Controlled source A · Declared region A")
  await expect(sheet.getByText("Approved destinations:", { exact: false })).toContainText(
    "192.0.2.8",
  )
  await sheet.getByRole("button", { name: "Queue check" }).click()
  await expect(sheet).toBeHidden()
  const row = page.getByRole("row").filter({ hasText: "Queued" })
  await expect(row.getByText("Unknown", { exact: true })).toHaveCount(3)
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
  await row.getByRole("button", { name: "Cancel check" }).click()
  await expect.poll(() => cancelled).toBe(true)
  await expect(
    page.getByRole("row").filter({ hasText: "Cancelled" }).getByText("Unknown", { exact: true }),
  ).toHaveCount(3)
})

test("signed measurements preserve family and scope through a failed refresh", async ({ page }) => {
  const state = await setup(page, [measured("v4", "inet"), measured("v6", "inet6")])
  await page.goto("/network/external")
  await expect(page.getByRole("row").filter({ hasText: "IPv4" })).toContainText("Connected")
  await expect(page.getByRole("row").filter({ hasText: "IPv6" })).toContainText("Verified")
  await page
    .getByRole("row")
    .filter({ hasText: "IPv6" })
    .getByRole("button", { name: "Controlled source A", exact: true })
    .click()
  await expect(page.getByText("2001:db8::3 → 2001:db8::8:443")).toBeVisible()
  state.unavailable = true
  await page.waitForTimeout(5200)
  await expect(page.getByText("Showing the last known network state")).toBeVisible()
  await expect(page.getByText("Last known reach", { exact: true })).toBeVisible()
  await expect(page.getByRole("row").filter({ hasText: "IPv6" })).toContainText("Verified")
})

test("an expired or offline source cannot imply a measured failure", async ({ page }) => {
  await setup(page, [{ ...queued("expired"), status: "expired" }])
  await page.goto("/network/external")
  const row = page.getByRole("row").filter({ hasText: "Expired" })
  await expect(row.getByTitle("No accepted measurement")).toBeVisible()
  await expect(row.getByText("Unknown", { exact: true })).toHaveCount(3)
  await expect(row.getByText("Failed", { exact: true })).toHaveCount(0)
  await expect(
    page.getByRole("region", { name: "Selected check" }).getByText("No accepted measurement."),
  ).toBeVisible()
  // Neither the verdict nor the picture turns a missing agent into a failure.
  await expect(page.getByText("No measurement yet", { exact: true })).toBeVisible()
  await expect(
    page.getByRole("region", { name: "Measured from" }).getByText("no measurement"),
  ).toBeVisible()
  await expect(page.locator('[data-slot="wire-flow"] .text-destructive')).toHaveCount(0)
})

test("the picture says each line's last outcome and a source opens its check", async ({ page }) => {
  await setup(page, checks, { sources: vantages })
  await page.goto("/network/external")
  await loaded(page)
  await expect(page.locator('[data-slot="stat-grid"], [data-slot="stat-tile"]')).toHaveCount(0)
  await expect(page.getByRole("region", { name: "About external checks" })).toContainText(
    "Reached from 1 of 2 sources",
  )
  const picture = page.locator('[data-slot="wire-flow"]')
  const node = (id: string) => picture.locator(`[data-node="${id}"]`)
  await expect(node(`source:${vantages[0].id}`)).toContainText("reached")
  await expect(node(`source:${vantages[1].id}`)).toContainText("TCP refused")
  await expect(node(`source:${vantages[2].id}`)).toContainText("awaiting enrollment")
  await expect(node("service:service.example")).toContainText(":443")
  await expect(node("service:mail.example")).toContainText("not checked yet")
  await picture.getByRole("button", { name: "Check from Controlled source B" }).click()
  const sheet = page.getByRole("dialog", { name: "Run a check" })
  await expect(sheet.getByLabel("Controlled source", { exact: true })).toContainText(
    "Controlled source B",
  )
  // Source B's scope approves IPv4 only: the other family is shown and refused.
  await expect(sheet.getByRole("radio", { name: "IPv6" })).toBeDisabled()
  // One check at a time from each source.
  await sheet.getByRole("button", { name: "Cancel" }).click()
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
  await page.getByRole("button", { name: "Enroll a source" }).click()
  const sheet = page.getByRole("dialog")
  await sheet.getByLabel("Source name", { exact: true }).fill("New region")
  await sheet.getByLabel("Service name or address", { exact: true }).fill("service.example")
  await sheet.getByLabel("Approved literal addresses", { exact: true }).fill("192.0.2.8")
  await sheet.getByRole("button", { name: "Create one-use enrollment" }).click()
  await expect(sheet.getByRole("alert").filter({ hasText: "Approve both families" })).toBeVisible()
  await expect(sheet.getByLabel("Source name", { exact: true })).toHaveValue("New region")
  refused = false
  await sheet.getByRole("button", { name: "Create one-use enrollment" }).click()
  expect(submitted).toMatchObject({
    name: "New region",
    scopes: [
      { target: "service.example", addresses: ["192.0.2.8"], ports: [443], families: ["inet"] },
    ],
  })
  await expect(sheet.getByLabel("One-use enrollment token", { exact: true })).toHaveValue(
    "b".repeat(64),
  )
  await expect(sheet.getByLabel("Server signing key", { exact: true })).toHaveValue(
    "public-server-key",
  )
  // Closing the sheet does not lose the credential: the source's menu shows it again.
  await page.keyboard.press("Escape")
  await expect(sheet).toBeHidden()
  await page.getByRole("button", { name: "Actions for Controlled source A" }).click()
  await page.getByRole("menuitem", { name: "Show credential" }).click()
  await expect(sheet.getByLabel("One-use enrollment token", { exact: true })).toHaveValue(
    "b".repeat(64),
  )
  await sheet.getByRole("button", { name: "Dismiss credential" }).click()
  await expect(page.getByTestId("external-enrollment")).toHaveCount(0)
  await page.reload()
  await expect(page.getByTestId("external-enrollment")).toHaveCount(0)
})

test("readonly accounts cannot queue checks or create machine credentials", async ({ page }) => {
  await setup(page, [], { session: readonly })
  const requests: string[] = []
  page.on("request", (request) => {
    if (request.url().includes("/api/v1/network/external")) requests.push(request.url())
  })
  await page.goto("/network/external")
  await expect(
    page.getByText(
      "External measurements and agent management require system administrator access.",
    ),
  ).toBeVisible()
  // No control it cannot use: nothing to queue, enrol, compare or revoke.
  await expect(page.locator('[data-slot="page"]').getByRole("button")).toHaveCount(0)
  expect(requests).toEqual([])
})

test("revoking is offered only to an account the server lets revoke", async ({ page }) => {
  const mutations: Mutation[] = []
  await setup(page, [], {
    mutations,
    session: { ...admin, capabilities: ["read", "system.admin"] },
  })
  await page.goto("/network/external")
  await page.getByRole("button", { name: "Actions for Controlled source A" }).click()
  await expect(page.getByRole("menuitem", { name: "Check from here" })).toBeVisible()
  await expect(page.getByRole("menuitem", { name: "Revoke" })).toHaveCount(0)
  await page.keyboard.press("Escape")

  await mockNetwork(page, mutations, { session: admin })
  await page.route("**/api/v1/network/external/vantages", (route) =>
    route.request().method() === "GET" ? json(route, [vantage]) : route.fallback(),
  )
  await page.reload()
  await page.getByRole("button", { name: "Actions for Controlled source A" }).click()
  await page.getByRole("menuitem", { name: "Revoke" }).click()
  await expect(page.getByRole("dialog")).toContainText("retained evidence remains for seven days")
  expect(mutations).toEqual([])
  await page.getByRole("button", { name: "Revoke source", exact: true }).click()
  await expect.poll(() => mutations.length).toBe(1)
  expect(mutations[0]).toMatchObject({
    method: "DELETE",
    path: `/network/external/vantages/${vantage.id}`,
  })
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
  await page.getByRole("button", { name: "Compare", exact: true }).click()
  const sheet = page.getByRole("dialog", { name: "Compare measurements" })
  await sheet.getByLabel("First measurement", { exact: true }).click()
  await page.getByRole("option").filter({ hasText: "IPv4" }).click()
  await sheet.getByLabel("Second measurement", { exact: true }).click()
  await page.getByRole("option").filter({ hasText: "IPv6" }).click()
  await sheet.getByRole("button", { name: "Compare", exact: true }).click()
  await expect(
    sheet.getByRole("status").filter({ hasText: "provider causes remain unverified" }),
  ).toBeVisible()
  expect(pair?.searchParams.get("before")).toBe("v4")
  expect(pair?.searchParams.get("after")).toBe("v6")
})

test("a row's menu starts a comparison from that measurement", async ({ page }) => {
  await setup(page, [measured("v4", "inet"), measured("v6", "inet6")])
  await page.goto("/network/external")
  await page
    .getByRole("row")
    .filter({ hasText: "IPv6" })
    .getByRole("button", { name: /Actions for/ })
    .click()
  await page.getByRole("menuitem", { name: "Compare with…" }).click()
  const sheet = page.getByRole("dialog", { name: "Compare measurements" })
  await expect(sheet.getByLabel("First measurement", { exact: true })).toContainText("IPv6")
  await expect(sheet.getByRole("button", { name: "Compare", exact: true })).toBeDisabled()
})

for (const width of [1280, 390]) {
  test(`checks and sources keep one height in every state at ${width}px`, async ({ page }) => {
    await page.setViewportSize({ width, height: 900 })
    await setup(page, checks, { sources: vantages })
    await page.goto("/network/external")
    await loaded(page)
    await risen(page)
    expect(
      await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth),
    ).toBe(true)
    // A measured check, a refused one and one that expired with nothing measured.
    const rows =
      width >= 768
        ? page.getByRole("region", { name: "Checks", exact: true }).locator("tbody tr")
        : page.getByRole("list", { name: "Checks" }).getByRole("listitem")
    await expect(rows).toHaveCount(checks.length)
    const boxes = await rows.evaluateAll((items) =>
      items.map((item) => item.getBoundingClientRect()).map((b) => ({ y: b.y, height: b.height })),
    )
    for (const [i, box] of boxes.entries()) {
      expect(box.height).toBe(boxes[0].height)
      if (i > 0) expect(box.y).toBeCloseTo(boxes[i - 1].y + boxes[i - 1].height, 0)
    }
    // An enrolled source that polled, one with an address on record and one never enrolled.
    const cards = page
      .getByRole("list", { name: "Controlled sources" })
      .locator('[data-slot="choice-row"]')
    await expect(cards).toHaveCount(vantages.length)
    const heights = await cards.evaluateAll((items) =>
      items.map((item) => item.getBoundingClientRect().height),
    )
    expect(new Set(heights).size).toBe(1)
  })

  test(`the opening holds its place while the sources load at ${width}px`, async ({ page }) => {
    await page.setViewportSize({ width, height: 900 })
    await mockNetwork(page)
    let release!: () => void
    const held = new Promise<void>((resolve) => {
      release = resolve
    })
    await page.route("**/api/v1/network/external/vantages", async (route) => {
      await held
      return json(route, vantages.slice(0, 2))
    })
    await page.route("**/api/v1/network/external/checks", async (route) => {
      await held
      return json(route, checks)
    })
    await page.goto("/network/external")
    const identity = page.getByRole("region", { name: "About external checks" })
    const picture = page.locator('[data-slot="wire-flow"]')
    const table = page.getByRole("region", { name: "Checks", exact: true })
    await expect(picture.locator('[data-slot="skeleton"]').first()).toBeVisible()
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
    expect(after.picture.y).toBe(before.picture.y)
    expect(after.picture.height).toBe(before.picture.height)
    expect(after.table.y).toBe(before.table.y)
  })
}
