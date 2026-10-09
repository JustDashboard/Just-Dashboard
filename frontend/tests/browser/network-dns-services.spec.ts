import { expect, test, type Page } from "@playwright/test"
import { dnsChange, dnsProvision, mockDNSServicePage } from "./network-dns-services-fixture"

const sheet = (page: Page) => page.locator("[data-slot=sheet-content]")
const posts = (control: Awaited<ReturnType<typeof mockDNSServicePage>>) =>
  control.mutations.filter((mutation) => mutation.path.endsWith("/apply"))

async function openInventory(page: Page) {
  await page.getByRole("button", { name: "Inspect Fixture DNS", exact: true }).click()
  await expect(
    sheet(page).getByRole("heading", { name: "Configured listeners", exact: true }),
  ).toBeVisible()
}

async function openChange(page: Page) {
  await openInventory(page)
  await sheet(page)
    .getByRole("button", { name: "Read DNS change change-fixture", exact: true })
    .click()
  await expect(
    sheet(page).getByRole("button", { name: "Apply reviewed native change", exact: true }),
  ).toBeVisible()
}

async function confirmation(page: Page) {
  await sheet(page)
    .getByRole("button", { name: "Apply reviewed native change", exact: true })
    .click()
  return page.getByRole("dialog", { name: "Apply reviewed DNS change", exact: true })
}

test("private native services are absent for a reader and make no native GET", async ({ page }) => {
  const control = await mockDNSServicePage(page, { reader: true })
  await expect(page.getByRole("heading", { name: "Resolver chain", exact: true })).toBeVisible()
  await expect(page.getByRole("button", { name: "Connect engine", exact: true })).toHaveCount(0)
  expect(control.reads).toEqual([])
})

test("connection defaults read-only and sends only the selected native credential", async ({
  page,
}) => {
  const control = await mockDNSServicePage(page, { empty: true })
  await page.getByRole("button", { name: "Connect engine", exact: true }).click()
  await expect(
    sheet(page).getByRole("switch", { name: "Allow reviewed native changes", exact: true }),
  ).not.toBeChecked()
  await sheet(page).getByRole("button", { name: "Select Technitium", exact: true }).click()
  await sheet(page).getByLabel("Connection name", { exact: true }).fill("Private token DNS")
  await sheet(page).getByLabel("Management origin", { exact: true }).fill("http://127.0.0.1:5380")
  await sheet(page)
    .getByLabel("Native API token", { exact: true })
    .fill("fixture-request-only-token")
  await sheet(page)
    .getByRole("button", { name: "Connect and read native service", exact: true })
    .click()
  await expect(
    sheet(page).getByRole("heading", { name: "Configured listeners", exact: true }),
  ).toBeVisible()
  expect(control.mutations[0].body).toMatchObject({
    engine: "technitium",
    management: false,
    credential: { token: "fixture-request-only-token" },
  })
  expect(control.mutations[0].body).not.toHaveProperty("credential.password")
  await expect(page.getByText("fixture-request-only-token", { exact: true })).toHaveCount(0)
})

test("replacement fixes native owner/origin, re-enters sealed trust and retains details after failure", async ({
  page,
}) => {
  const control = await mockDNSServicePage(page)
  await openInventory(page)
  await sheet(page).getByRole("button", { name: "Update connection", exact: true }).click()
  await expect(sheet(page).getByLabel("Management origin", { exact: true })).toBeDisabled()
  await expect(sheet(page).getByLabel("Native username", { exact: true })).toHaveValue("")
  await expect(sheet(page).getByLabel("Native password", { exact: true })).toHaveValue("")
  await expect(sheet(page).getByLabel("Custom CA certificate", { exact: true })).toHaveValue("")
  await sheet(page).getByLabel("Connection name", { exact: true }).fill("Retained connection draft")
  await sheet(page).getByLabel("Native username", { exact: true }).fill("fixture-user")
  await sheet(page)
    .getByLabel("Native password", { exact: true })
    .fill("fixture-replacement-password")
  await sheet(page)
    .getByRole("button", { name: "Replace and read native service", exact: true })
    .click()
  await expect(
    sheet(page)
      .getByText(/Re-enter the custom CA certificate, up to 32 KiB/)
      .first(),
  ).toBeVisible()
  expect(control.mutations).toEqual([])
  await sheet(page).getByRole("switch", { name: "Use a custom CA", exact: true }).uncheck()
  control.connectFailure = true
  await sheet(page)
    .getByRole("button", { name: "Replace and read native service", exact: true })
    .click()
  await expect(sheet(page).getByText(/Fixture credential read failed/)).toBeVisible()
  await expect(sheet(page).getByLabel("Connection name", { exact: true })).toHaveValue(
    "Retained connection draft",
  )
  await expect(sheet(page).getByLabel("Native username", { exact: true })).toHaveValue(
    "fixture-user",
  )
  await expect(sheet(page).getByLabel("Native password", { exact: true })).toHaveValue("")
  expect(control.mutations[0]).toMatchObject({
    method: "PUT",
    path: "/network/dns/services/dns-fixture",
    body: {
      engine: "adguard",
      endpoint: "https://192.0.2.10:8443",
      credential: { username: "fixture-user", password: "fixture-replacement-password" },
    },
  })
})

test("inventory preserves separate native evidence, views, clients, zones, overrides and bounded queries", async ({
  page,
}) => {
  await mockDNSServicePage(page)
  await openInventory(page)
  for (const name of [
    "Authoritative zones",
    "Local overrides",
    "Native views",
    "Named networks",
    "Native filtering groups",
    "Native query history",
  ])
    await expect(sheet(page).getByRole("heading", { name, exact: true })).toBeVisible()
  for (const text of [
    "authority.example.test",
    "printer.home.test",
    "Office view",
    "inside.example.test",
    "outside.example.test",
    "query.example.test",
    "Office client",
  ])
    await expect(sheet(page).getByText(text, { exact: false }).first()).toBeVisible()
  await expect(sheet(page).getByText("Runtime · unknown", { exact: true })).toBeVisible()
  await expect(
    sheet(page).getByText("App client evidence · unsupported", { exact: true }),
  ).toBeVisible()
  await expect(sheet(page).getByText(/Native access · partial/)).toBeVisible()
  await expect(
    sheet(page)
      .getByText(/Basis: native fixture inventory/)
      .first(),
  ).toBeVisible()
  await expect(sheet(page).getByText(/\[::1\]:53/)).toBeVisible()
})

test("read-only connections cannot stage native changes", async ({ page }) => {
  const control = await mockDNSServicePage(page, { management: false })
  await openInventory(page)
  await expect(
    sheet(page).getByRole("button", { name: "Create retained review", exact: true }),
  ).toBeDisabled()
  await expect(sheet(page).getByText(/This connection is read-only/)).toBeVisible()
  expect(control.mutations).toEqual([])
})

test("upstream draft becomes a retained review before any native apply", async ({ page }) => {
  const control = await mockDNSServicePage(page)
  await openInventory(page)
  await sheet(page).getByRole("button", { name: "Review upstreams", exact: true }).click()
  await sheet(page)
    .getByLabel("Classic DNS upstream endpoints", { exact: true })
    .fill("192.0.2.54:53\n[2001:db8::54]:53")
  await sheet(page).getByRole("button", { name: "Create retained review", exact: true }).click()
  await expect(
    sheet(page).getByRole("heading", { name: "Reviewed native change", exact: true }),
  ).toBeInViewport()
  await expect(
    sheet(page).getByRole("button", { name: "Apply reviewed native change", exact: true }),
  ).toBeEnabled()
  expect(control.mutations).toHaveLength(1)
  expect(control.mutations[0].body).toEqual({
    action: "upstreams",
    upstreams: ["192.0.2.54:53", "[2001:db8::54]:53"],
  })
  expect(posts(control)).toEqual([])
})

test("an expired retained review stays readable and cannot apply", async ({ page }) => {
  const control = await mockDNSServicePage(page)
  const change = dnsChange()
  change.expiresAt = new Date(Date.now() - 1000).toISOString()
  control.changes = [change]
  await openChange(page)
  await expect(
    sheet(page).getByRole("button", { name: "Apply reviewed native change", exact: true }),
  ).toBeDisabled()
  await expect(sheet(page).getByText(/This review expired/)).toBeVisible()
  expect(posts(control)).toEqual([])
})

test("a failed native read retains inventory and blocks staging", async ({ page }) => {
  const control = await mockDNSServicePage(page)
  await openInventory(page)
  control.inspectFailure = true
  await sheet(page).getByRole("button", { name: "Refresh native reading", exact: true }).click()
  await expect(sheet(page).getByText("Last native reading retained", { exact: true })).toBeVisible()
  await expect(
    sheet(page).getByRole("heading", { name: "Authoritative zones", exact: true }),
  ).toBeVisible()
  await expect(
    sheet(page).getByRole("button", { name: "Create retained review", exact: true }),
  ).toBeDisabled()
  expect(posts(control)).toEqual([])
})

test("an open confirmation refuses a changed same-ID reviewed intent", async ({ page }) => {
  await page.clock.install()
  const control = await mockDNSServicePage(page)
  control.changes = [dnsChange()]
  await openChange(page)
  const dialog = await confirmation(page)
  control.changes[0] = {
    ...control.changes[0],
    request: { action: "protection", protection: true },
  }
  await page.clock.fastForward(5500)
  await expect(sheet(page).getByText("Enable native protection", { exact: true })).toBeVisible()
  await dialog.getByRole("button", { name: "Apply native change", exact: true }).click()
  await expect(
    sheet(page).getByText(/This review changed or already has an attempted apply/),
  ).toBeVisible()
  expect(posts(control)).toEqual([])
})

test("an open confirmation refuses a failed native owner refresh", async ({ page }) => {
  await page.clock.install()
  const control = await mockDNSServicePage(page)
  control.changes = [dnsChange()]
  await openChange(page)
  const dialog = await confirmation(page)
  control.inspectFailure = true
  await page.clock.fastForward(5500)
  await expect(
    sheet(page)
      .getByText(/Fixture owner reading failed/)
      .first(),
  ).toBeVisible()
  await dialog.getByRole("button", { name: "Apply native change", exact: true }).click()
  await expect(
    sheet(page)
      .getByText(/Refresh the retained review and its native owner/)
      .last(),
  ).toBeVisible()
  expect(posts(control)).toEqual([])
})

test("HTTP 200 needs-review is retained without verified success", async ({ page }) => {
  const control = await mockDNSServicePage(page)
  control.changes = [dnsChange()]
  control.apply = "needs_review"
  await openChange(page)
  const dialog = await confirmation(page)
  await dialog.getByRole("button", { name: "Apply native change", exact: true }).click()
  await expect(
    sheet(page)
      .getByText(/Fixture native outcome needs review/)
      .first(),
  ).toBeVisible()
  await expect(page.getByText("Native readback verified", { exact: true })).toHaveCount(0)
  await expect(
    sheet(page).getByRole("button", { name: "Apply reviewed native change", exact: true }),
  ).toHaveCount(0)
  expect(posts(control)).toHaveLength(1)
})

test("a lost apply reply remains single-use after page reload", async ({ page }) => {
  const control = await mockDNSServicePage(page)
  control.changes = [dnsChange()]
  control.apply = "lost"
  await openChange(page)
  const dialog = await confirmation(page)
  await dialog.getByRole("button", { name: "Apply native change", exact: true }).click()
  await expect(sheet(page).getByText(/this review will not be applied again/)).toBeVisible()
  await page.reload()
  await openInventory(page)
  await sheet(page)
    .getByRole("button", { name: "Read DNS change change-fixture", exact: true })
    .click()
  await expect(
    sheet(page)
      .getByText(/Fixture native outcome needs review/)
      .first(),
  ).toBeVisible()
  await expect(
    sheet(page).getByRole("button", { name: "Apply reviewed native change", exact: true }),
  ).toHaveCount(0)
  expect(posts(control)).toHaveLength(1)
})

test("owned resource preview and removal name the exact bridge/container and both volumes", async ({
  page,
}) => {
  const control = await mockDNSServicePage(page)
  const setup = dnsProvision()
  setup.state = "verified"
  setup.resources.phase = "verified"
  setup.resources.containerId = "fixture-container-id"
  setup.resources.networkId = "fixture-network-id"
  control.provisions = [setup]
  await page.getByRole("button", { name: "Refresh", exact: true }).last().click()
  await page
    .getByRole("button", { name: "Review owned DNS setup Owned fixture DNS", exact: true })
    .click()
  await expect(
    sheet(page).getByText("jd-dns-fixture-config · jd-dns-fixture-data", { exact: true }),
  ).toBeVisible()
  await sheet(page)
    .getByRole("button", { name: "Remove owned service and data", exact: true })
    .click()
  const dialog = page.getByRole("dialog", {
    name: "Remove owned DNS service and data",
    exact: true,
  })
  for (const name of [
    "jd-dns-fixture",
    "jd-dns-fixture-net",
    "jd-dns-fixture-config",
    "jd-dns-fixture-data",
  ])
    await expect(dialog.getByText(name, { exact: false }).first()).toBeVisible()
  await dialog.getByRole("button", { name: "Remove service and data", exact: true }).click()
  await expect(sheet(page).getByText("removed", { exact: true }).first()).toBeVisible()
  expect(control.mutations.filter((mutation) => mutation.method === "DELETE")).toMatchObject([
    { path: "/network/dns/services/provisions/setup-fixture" },
  ])
})
