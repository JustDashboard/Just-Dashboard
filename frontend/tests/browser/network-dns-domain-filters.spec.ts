import { expect, test, type Locator, type Page } from "@playwright/test"
import {
  dnsPolicyChange,
  mockDNSServicePage,
  type DNSServicePageControl,
} from "./network-dns-services-fixture"

const sheet = (page: Page) => page.locator("[data-slot=sheet-content]")
const detail = (root: Locator, label: string) =>
  root
    .locator("dt")
    .filter({ hasText: new RegExp(`^${label}$`) })
    .locator("xpath=following-sibling::dd[1]")
const createReview = (page: Page) =>
  sheet(page).getByRole("button", { name: "Create retained review", exact: true })
const applyReview = (page: Page) =>
  sheet(page).getByRole("button", { name: "Apply reviewed native change", exact: true })
const applies = (control: DNSServicePageControl) =>
  control.mutations.filter((item) => item.path.endsWith("/apply"))

function configuredGroups(control: DNSServicePageControl) {
  control.view.snapshot!.appClientEvidence = {
    state: "configured",
    basis: "native_configuration",
    summary: "Native group inventory; client filtering remains unmeasured.",
  }
}

async function openInventory(page: Page) {
  await page.getByRole("button", { name: "Inspect Fixture DNS", exact: true }).click()
  await expect(
    sheet(page).getByRole("heading", { name: "Configured listeners", exact: true }),
  ).toBeVisible()
}

async function draft(page: Page, remove = false) {
  await openInventory(page)
  await sheet(page)
    .getByRole("button", {
      name: remove ? "Review remove domain filter" : "Review add domain filter",
      exact: true,
    })
    .click()
  await sheet(page).getByLabel("Filter domain", { exact: true }).fill("filter.example.test")
}

async function confirm(page: Page) {
  await expect(applyReview(page)).toBeEnabled()
  await applyReview(page).click()
  return page.getByRole("dialog", { name: "Apply reviewed DNS change", exact: true })
}

for (const disposition of ["deny", "allow"] as const) {
  test(`AdGuard ${disposition} review retains domain-suffix semantics and exact readback`, async ({
    page,
  }) => {
    const control = await mockDNSServicePage(page)
    await draft(page)
    if (disposition === "allow") {
      await sheet(page).getByLabel("Filter disposition", { exact: true }).click()
      await page.getByRole("option", { name: "Allow", exact: true }).click()
    }
    await expect(
      sheet(page).getByText("AdGuard matches this domain and its subdomains."),
    ).toBeVisible()
    await createReview(page).click()
    expect(control.mutations).toHaveLength(1)
    expect(control.mutations[0].body).toEqual({
      action: "filter_add",
      filter: { domain: "filter.example.test", disposition, match: "suffix" },
    })
    await expect(detail(sheet(page), "Filter match")).toHaveText("This domain and its subdomains")
    await expect(detail(sheet(page), "Native selected filter")).toHaveText("Absent")
    const dialog = await confirm(page)
    await dialog.getByRole("button", { name: "Apply native change", exact: true }).click()
    await expect(
      sheet(page).getByRole("heading", { name: "Native readback", exact: true }),
    ).toBeVisible()
    await expect(detail(sheet(page), "Native selected filter")).toHaveText(["Absent", "Present"])
    await expect(detail(sheet(page), "Selected filter comment")).toHaveText([
      "Unreported",
      "Unreported",
    ])
    expect(applies(control)).toHaveLength(1)
    await expect(applyReview(page)).toBeDisabled()
  })
}

for (const membership of ["zero", "empty"] as const) {
  test(`Pi-hole exact addition retains ${membership} native memberships`, async ({ page }) => {
    const control = await mockDNSServicePage(page, { engine: "pihole" })
    configuredGroups(control)
    await draft(page)
    if (membership === "zero")
      await sheet(page)
        .getByRole("checkbox", { name: /Default filtering group · ID 0/ })
        .check()
    await expect(sheet(page).getByText("Pi-hole matches only this exact domain.")).toBeVisible()
    await createReview(page).click()
    expect(control.mutations[0].body).toEqual({
      action: "filter_add",
      filter: {
        domain: "filter.example.test",
        disposition: "deny",
        match: "exact",
        groups: membership === "zero" ? [0] : [],
      },
    })
    await expect(detail(sheet(page), "New filter group IDs")).toHaveText(
      membership === "zero" ? "0" : "None · no memberships",
    )
    const dialog = await confirm(page)
    await dialog.getByRole("button", { name: "Apply native change", exact: true }).click()
    await expect(detail(sheet(page), "Selected filter group IDs")).toHaveText(
      membership === "zero" ? "0" : "None",
    )
    await expect(detail(sheet(page), "Selected filter comment")).toHaveText([
      "Unreported",
      "None (native null)",
    ])
    expect(applies(control)).toHaveLength(1)
  })
}

for (const comment of [null, "", "Keep this native filter comment"] as const) {
  test(`Pi-hole removal retains ${comment === null ? "null" : comment === "" ? "empty" : "text"} comment and existing groups`, async ({
    page,
  }) => {
    const control = await mockDNSServicePage(page, { engine: "pihole" })
    control.filterComment = comment
    await draft(page, true)
    await createReview(page).click()
    expect(control.mutations[0].body).toEqual({
      action: "filter_remove",
      filter: { domain: "filter.example.test", disposition: "deny", match: "exact" },
    })
    await expect(detail(sheet(page), "Selected filter group IDs")).toHaveText("0 · 7")
    await expect(detail(sheet(page), "Selected filter comment")).toHaveText(
      comment === null ? "None (native null)" : comment || "Empty native comment",
    )
    expect(applies(control)).toEqual([])
  })
}

test("a refused filter review retains domain, disposition and group zero", async ({ page }) => {
  const control = await mockDNSServicePage(page, { engine: "pihole" })
  configuredGroups(control)
  control.stageFailure = true
  await draft(page)
  await sheet(page)
    .getByRole("checkbox", { name: /Default filtering group · ID 0/ })
    .check()
  await createReview(page).click()
  await expect(sheet(page).getByText(/Fixture selected native policy review refused/)).toBeVisible()
  await expect(sheet(page).getByLabel("Filter domain", { exact: true })).toHaveValue(
    "filter.example.test",
  )
  await expect(
    sheet(page).getByRole("checkbox", { name: /Default filtering group · ID 0/ }),
  ).toBeChecked()
  expect(applies(control)).toEqual([])
})

test("unknown native groups hold a Pi-hole filter draft after refresh", async ({ page }) => {
  const control = await mockDNSServicePage(page, { engine: "pihole" })
  configuredGroups(control)
  await draft(page)
  control.view.snapshot!.appClientEvidence.state = "unknown"
  control.view.snapshot!.filterGroups = []
  await sheet(page).getByRole("button", { name: "Refresh native reading", exact: true }).click()
  await expect(createReview(page)).toBeDisabled()
  await expect(sheet(page).getByLabel("Filter domain", { exact: true })).toHaveValue(
    "filter.example.test",
  )
  await expect(sheet(page).getByText(/Refresh the native group inventory/)).toBeVisible()
  expect(control.mutations).toEqual([])
})

test("read-only domain controls cannot create a review", async ({ page }) => {
  const control = await mockDNSServicePage(page, { management: false })
  await openInventory(page)
  await sheet(page).getByRole("button", { name: "Review add domain filter", exact: true }).click()
  await expect(createReview(page)).toBeDisabled()
  await expect(sheet(page).getByLabel("Filter domain", { exact: true })).toBeDisabled()
  expect(control.mutations).toEqual([])
})

test("Technitium keeps custom-domain controls outside its supported actions", async ({ page }) => {
  const control = await mockDNSServicePage(page, { engine: "technitium" })
  await openInventory(page)
  await expect(
    sheet(page).getByRole("button", { name: "Review add domain filter", exact: true }),
  ).toHaveCount(0)
  await expect(
    sheet(page).getByRole("button", { name: "Review remove domain filter", exact: true }),
  ).toHaveCount(0)
  expect(control.mutations).toEqual([])
})

for (const failure of [
  "selected-drift",
  "missing-selection",
  "lost-reading",
  "retained-drift",
] as const) {
  test(`an open filter confirmation is held after ${failure}`, async ({ page }) => {
    await page.clock.install()
    const control = await mockDNSServicePage(page, { engine: "pihole" })
    const change = dnsPolicyChange(
      {
        action: "filter_remove",
        filter: { domain: "filter.example.test", disposition: "deny", match: "exact" },
      },
      control.view.connection,
      control.view.snapshot,
    )
    control.changes = [change]
    await openInventory(page)
    await sheet(page)
      .getByRole("button", { name: "Read DNS change change-fixture", exact: true })
      .click()
    const dialog = await confirm(page)
    if (failure === "selected-drift") {
      const current = structuredClone(change.before!)
      current.selectedFilter!.groups = []
      control.currentView = { ...control.view, snapshot: current }
    } else if (failure === "retained-drift")
      change.before!.selectedFilter!.comment = "Changed retained native metadata"
    else if (failure === "missing-selection") control.currentMissingSelection = true
    else control.currentFailure = true
    await page.clock.fastForward(5500)
    await expect(applyReview(page)).toBeDisabled()
    await dialog.getByRole("button", { name: "Apply native change", exact: true }).click()
    await expect(
      sheet(page)
        .getByText(/Refresh the retained review and its native owner/)
        .last(),
    ).toBeVisible()
    expect(applies(control)).toEqual([])
    expect(control.reads).toContain("/network/dns/services/changes/change-fixture/current")
  })
}

for (const engine of ["adguard", "pihole"] as const) {
  for (const width of [390, 1280, 1720]) {
    test(`${engine} domain form and retained review fit ${width}px`, async ({ page }, testInfo) => {
      await page.setViewportSize({ width, height: 1000 })
      const control = await mockDNSServicePage(page, { engine })
      if (engine === "pihole") configuredGroups(control)
      await draft(page)
      const form = sheet(page).getByRole("form", {
        name: "Review native domain filter",
        exact: true,
      })
      await form.scrollIntoViewIfNeeded()
      await expect
        .poll(async () => {
          const bounds = await sheet(page).boundingBox()
          return Boolean(bounds && bounds.x >= 0 && bounds.x + bounds.width <= width + 1)
        })
        .toBe(true)
      expect(
        await sheet(page).evaluate((element) => element.scrollWidth - element.clientWidth),
      ).toBeLessThanOrEqual(1)
      await page.screenshot({
        path: testInfo.outputPath(`domain-filter-form-${engine}-${width}.png`),
      })
      await createReview(page).click()
      await expect(
        sheet(page).getByRole("heading", { name: "Reviewed native change", exact: true }),
      ).toBeVisible()
      await expect
        .poll(async () => {
          const bounds = await sheet(page).boundingBox()
          return Boolean(bounds && bounds.x >= 0 && bounds.x + bounds.width <= width + 1)
        })
        .toBe(true)
      expect(
        await sheet(page).evaluate((element) => element.scrollWidth - element.clientWidth),
      ).toBeLessThanOrEqual(1)
      await page.screenshot({
        path: testInfo.outputPath(`domain-filter-review-${engine}-${width}.png`),
      })
      expect(control.mutations).toHaveLength(1)
      expect(applies(control)).toEqual([])
    })
  }
}
