import { expect, test, type Page } from "@playwright/test"
import { dnsFilters, mockDNSServicePage } from "./network-dns-services-fixture"

const sheet = (page: Page) => page.locator("[data-slot=sheet-content]")
const filters = (page: Page) =>
  sheet(page).getByRole("region", { name: "Native DNS filter inventory", exact: true })
const open = async (page: Page) => {
  await page.getByRole("button", { name: "Inspect Fixture DNS", exact: true }).click()
  await expect(
    filters(page).getByRole("heading", { name: "Filter subscriptions", exact: true }),
  ).toBeVisible()
}

for (const engine of ["adguard", "pihole", "technitium"] as const) {
  test(`${engine} filter inventory reads on a read-only connection and keeps metadata provenance`, async ({
    page,
  }) => {
    const control = await mockDNSServicePage(page, { engine, management: false })
    expect(control.reads.some((path) => path.endsWith("/filters"))).toBe(false)
    await open(page)
    const root = filters(page)
    await expect(root.getByText("Filter runtime · unknown", { exact: true })).toBeVisible()
    await expect(root.getByText("Native protection: Disabled", { exact: true })).toBeVisible()
    await expect(
      root.getByText("Origin: https://filters.example.test", { exact: true }),
    ).toHaveCount(2)
    await expect(
      root.getByText(`Entry fingerprint: ${"a".repeat(64)}`, { exact: true }),
    ).toBeVisible()
    await expect(
      root.getByText(`Entry fingerprint: ${"b".repeat(64)}`, { exact: true }),
    ).toBeVisible()
    if (engine !== "technitium") {
      await expect(root.getByText("Native rule count: 0", { exact: true })).toBeVisible()
      await expect(root.getByText(/native ID 0 · Disabled/)).toBeVisible()
    }
    if (engine === "pihole") {
      await expect(root.getByText("Native group IDs: None assigned", { exact: true })).toHaveCount(
        2,
      )
      await expect(root.getByText("Native list status code: 0", { exact: true })).toBeVisible()
      await expect(root.getByText("Native update time: 0", { exact: true })).toHaveCount(2)
    }
    if (engine === "technitium") {
      await expect(
        root.getByText("Configured inventory · unsupported", { exact: true }),
      ).toBeVisible()
      await expect(
        root.getByText("No readable entry inventory; this does not establish an empty policy.", {
          exact: true,
        }),
      ).toBeVisible()
      await expect(root.getByLabel("0 custom filter rules entries", { exact: true })).toHaveCount(0)
    }
    expect(control.reads.filter((path) => path.endsWith("/filters"))).toEqual([
      "/network/dns/services/dns-fixture/filters",
    ])
    expect(control.mutations).toEqual([])
    expect(control.unexpectedReads).toEqual([])
  })
}

test("failed and native-unavailable filter refreshes retain prior rows, age and read-only retry", async ({
  page,
}) => {
  const control = await mockDNSServicePage(page)
  await open(page)
  const root = filters(page)
  const observed = await root.locator("time").getAttribute("datetime")
  control.filtersFailure = true
  await root.getByRole("button", { name: "Read native filters", exact: true }).click()
  await expect(root.getByText("Last filter reading retained", { exact: true })).toBeVisible()
  await expect(root.getByText(/Fixture filter inventory unavailable/)).toBeVisible()
  await expect(root.getByText(/native ID 0 · Disabled/)).toBeVisible()
  expect(observed).toBe("2026-10-09T04:00:00Z")
  await expect(root.locator("time")).toHaveAttribute("datetime", observed!)
  control.filtersFailure = false
  control.filterPayload = {
    connection: control.view.connection,
    state: "unavailable",
    error: "Native filters temporarily unavailable.",
  }
  await root.getByRole("button", { name: "Read native filters", exact: true }).click()
  await expect(root.getByText(/Native filters temporarily unavailable/)).toBeVisible()
  await expect(
    root.getByText(`Entry fingerprint: ${"a".repeat(64)}`, { exact: true }),
  ).toBeVisible()
  control.filterPayload = undefined
  await root.getByRole("button", { name: "Read native filters", exact: true }).click()
  await expect(root.getByText("Last filter reading retained", { exact: true })).toHaveCount(0)
  expect(control.mutations).toEqual([])
})

test("partial unknown filter data replaces prior configured entries without an empty-policy count", async ({
  page,
}) => {
  const control = await mockDNSServicePage(page, { engine: "technitium" })
  await open(page)
  const next = dnsFilters(control.view.connection)
  next.state = "partial"
  next.inventory!.sources = {
    evidence: {
      state: "unknown",
      basis: "unavailable",
      summary: "Native subscription metadata is unreadable.",
    },
    identity: next.inventory!.sources.identity,
    entries: [],
  }
  delete next.inventory!.fingerprint
  control.filterPayload = next
  await filters(page).getByRole("button", { name: "Read native filters", exact: true }).click()
  await expect(
    filters(page).getByText("Filter inventory is partial", { exact: true }),
  ).toBeVisible()
  await expect(
    filters(page).getByText("Configured inventory · unknown", { exact: true }),
  ).toBeVisible()
  await expect(
    filters(page).getByText("Origin: https://filters.example.test", { exact: true }),
  ).toHaveCount(0)
  await expect(
    filters(page).getByLabel("0 filter subscriptions entries", { exact: true }),
  ).toHaveCount(0)
  await expect(
    filters(page).getByText(
      "No readable entry inventory; this does not establish an empty policy.",
      { exact: true },
    ),
  ).toHaveCount(2)
  expect(control.mutations).toEqual([])
})

test("a refreshed connection generation clears old filters and refuses a mismatched response", async ({
  page,
}) => {
  const control = await mockDNSServicePage(page)
  await open(page)
  control.filterPayload = dnsFilters({ ...control.view.connection })
  control.view.connection = { ...control.view.connection, generation: 2 }
  await sheet(page).getByRole("button", { name: "Read native engine", exact: true }).click()
  await expect(filters(page).getByText(/Native DNS filter connection changed/)).toBeVisible()
  await expect(
    filters(page).getByText(`Entry fingerprint: ${"a".repeat(64)}`, { exact: true }),
  ).toHaveCount(0)
  control.filterPayload = undefined
  await filters(page).getByRole("button", { name: "Read native filters", exact: true }).click()
  await expect(
    filters(page).getByRole("heading", { name: "Filter subscriptions", exact: true }),
  ).toBeVisible()
  expect(control.mutations).toEqual([])
})

test("malformed private filter origins fail without displaying returned secrets", async ({
  page,
}) => {
  const control = await mockDNSServicePage(page)
  const next = dnsFilters(control.view.connection)
  next.inventory!.sources.entries[0].origin =
    "https://user:fixture-filter-secret@filters.example.test/private?secret=1"
  control.filterPayload = next
  await page.getByRole("button", { name: "Inspect Fixture DNS", exact: true }).click()
  await expect(filters(page).getByText(/outside its bounded contract/)).toBeVisible()
  await expect(page.getByText(/fixture-filter-secret/)).toHaveCount(0)
  await expect(
    filters(page).getByRole("heading", { name: "Filter subscriptions", exact: true }),
  ).toHaveCount(0)
  expect(control.mutations).toEqual([])
})

test("reader accounts never request native filter metadata", async ({ page }) => {
  const control = await mockDNSServicePage(page, { reader: true })
  await expect(page.getByRole("heading", { name: "Resolver chain", exact: true })).toBeVisible()
  expect(control.reads).toEqual([])
  expect(control.mutations).toEqual([])
})

for (const width of [390, 1280, 1720]) {
  for (const engine of ["adguard", "pihole", "technitium"] as const) {
    test(`${engine} native filter reading fits ${width}px`, async ({ page }, testInfo) => {
      await page.setViewportSize({ width, height: 1000 })
      const control = await mockDNSServicePage(page, { engine, management: false })
      await open(page)
      await filters(page).evaluate((node) => node.scrollIntoView({ block: "start" }))
      const overflow = await sheet(page).evaluate((node) => node.scrollWidth - node.clientWidth)
      expect(overflow).toBeLessThanOrEqual(1)
      await page.screenshot({ path: testInfo.outputPath(`native-filters-${engine}-${width}.png`) })
      if (width === 390) {
        await filters(page)
          .getByRole("heading", { name: "Custom filter rules", exact: true })
          .evaluate((node) => node.scrollIntoView({ block: "start" }))
        await page.screenshot({
          path: testInfo.outputPath(`native-filter-rules-${engine}-${width}.png`),
        })
      }
      expect(control.mutations).toEqual([])
    })
  }
}
