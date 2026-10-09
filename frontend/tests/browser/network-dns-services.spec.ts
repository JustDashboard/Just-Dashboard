import { expect, test, type Locator, type Page } from "@playwright/test"
import {
  dnsChange,
  dnsPolicyChange,
  dnsProvision,
  dnsRecords,
  mockDNSServicePage,
  type DNSServicePageControl,
} from "./network-dns-services-fixture"

const sheet = (page: Page) => page.locator("[data-slot=sheet-content]")
const posts = (control: DNSServicePageControl) =>
  control.mutations.filter((mutation) => mutation.path.endsWith("/apply"))
const section = (page: Page, name: string) =>
  sheet(page)
    .locator("section")
    .filter({ has: page.getByRole("heading", { name, exact: true }) })
const detail = (root: Locator, label: string) =>
  root
    .locator("dt")
    .filter({ hasText: new RegExp(`^${label}$`) })
    .locator("xpath=following-sibling::dd[1]")
const createReview = (page: Page) =>
  sheet(page).getByRole("button", { name: "Create retained review", exact: true })
const applyReview = (page: Page) =>
  sheet(page).getByRole("button", { name: "Apply reviewed native change", exact: true })

async function select(page: Page, label: string, option: string) {
  await sheet(page).getByLabel(label, { exact: true }).click()
  await page.getByRole("option", { name: option, exact: true }).click()
}

async function selectZone(page: Page) {
  await sheet(page).getByLabel("Authoritative zone", { exact: true }).click()
  await page.getByRole("option", { name: /^authority\.example\.test · / }).click()
  await expect(
    sheet(page).getByRole("button", { name: "Read zone records", exact: true }),
  ).toBeEnabled()
  await expect(
    sheet(page).getByText("_policy._tcp.authority.example.test", { exact: false }).first(),
  ).toBeVisible()
}

async function recordDraft(
  page: Page,
  verb: string,
  name: string,
  value: string,
  type: "A" | "AAAA" = "A",
) {
  await openInventory(page)
  await sheet(page).getByRole("button", { name: verb, exact: true }).click()
  await sheet(page).getByLabel("DNS record name", { exact: true }).fill(name)
  await select(page, "Record type", type)
  await sheet(page).getByLabel("Record value", { exact: true }).fill(value)
}

async function selectedClient(page: Page, address = "192.0.2.10") {
  await openInventory(page)
  await sheet(page).getByRole("button", { name: "Review client groups", exact: true }).click()
  await sheet(page).getByLabel("Existing Pi-hole client", { exact: true }).click()
  await page.getByRole("option").filter({ hasText: address }).click()
}

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
  expect(control.reads).toContain("/network/dns/services/changes/change-fixture/current")
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
  control.currentFailure = true
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

test("a local A override has an exact retained intent and human native readback", async ({
  page,
}) => {
  const control = await mockDNSServicePage(page)
  await recordDraft(page, "Review add local override", "local.example.test", "198.51.100.9")
  await expect(sheet(page).getByLabel("Authoritative zone", { exact: true })).toHaveCount(0)
  await expect(sheet(page).getByLabel("TTL (seconds)", { exact: true })).toHaveCount(0)
  await createReview(page).click()
  const reviewed = section(page, "Reviewed native change")
  await expect(detail(reviewed, "Record name")).toHaveText("local.example.test")
  await expect(detail(reviewed, "Record type")).toHaveText("A")
  await expect(detail(reviewed, "Record value")).toHaveText("198.51.100.9")
  expect(control.mutations).toMatchObject([
    {
      body: {
        action: "override_add",
        record: { name: "local.example.test", type: "A", value: "198.51.100.9" },
      },
    },
  ])
  expect(control.mutations[0].body).not.toHaveProperty("zone")
  expect(control.mutations[0].body).not.toHaveProperty("record.ttl")
  expect(posts(control)).toEqual([])
  await expect(applyReview(page)).toBeEnabled()
  const dialog = await confirmation(page)
  await dialog.getByRole("button", { name: "Apply native change", exact: true }).click()
  await expect(section(page, "Native readback")).toContainText("local.example.test")
  await expect(section(page, "Native readback")).toContainText("198.51.100.9")
  await expect(section(page, "Before the change")).not.toContainText("local.example.test")
  expect(posts(control)).toHaveLength(1)
  expect(control.unexpectedReads).toEqual([])
})

test("a Pi-hole AAAA override removal retains its exact address without authoritative TTL", async ({
  page,
}) => {
  const control = await mockDNSServicePage(page, { engine: "pihole" })
  control.view.snapshot!.localOverrides = [
    { name: "retired.example.test", type: "AAAA", value: "2001:db8::9" },
  ]
  await recordDraft(
    page,
    "Review remove local override",
    "retired.example.test",
    "2001:db8::9",
    "AAAA",
  )
  await createReview(page).click()
  await expect(applyReview(page)).toBeEnabled()
  expect(control.mutations[0].body).toEqual({
    action: "override_remove",
    record: { name: "retired.example.test", type: "AAAA", value: "2001:db8::9" },
  })
  await expect(section(page, "Before the change")).toContainText("retired.example.test")
  const dialog = await confirmation(page)
  await dialog.getByRole("button", { name: "Apply native change", exact: true }).click()
  await expect(section(page, "Native readback")).toBeVisible()
  await expect(section(page, "Native readback")).not.toContainText("retired.example.test")
  expect(posts(control)).toHaveLength(1)
})

test("an authoritative AAAA add reads its explicit zone and retains exact TTL and native metadata", async ({
  page,
}) => {
  const control = await mockDNSServicePage(page, { engine: "technitium" })
  await recordDraft(
    page,
    "Review add zone record",
    "new.authority.example.test",
    "2001:db8::9",
    "AAAA",
  )
  await selectZone(page)
  await sheet(page).getByLabel("TTL (seconds)", { exact: true }).fill("86400")
  await expect(
    sheet(page)
      .getByText(/Unreported/i)
      .first(),
  ).toBeVisible()
  await expect(
    sheet(page).getByRole("button", {
      name: /^Use record _policy\._tcp\.authority\.example\.test TXT(?:\s|$)/,
    }),
  ).toHaveCount(0)
  await createReview(page).click()
  await expect(applyReview(page)).toBeEnabled()
  expect(control.mutations[0].body).toEqual({
    action: "record_add",
    zone: "authority.example.test",
    record: { name: "new.authority.example.test", type: "AAAA", value: "2001:db8::9", ttl: 86400 },
  })
  const reviewed = section(page, "Reviewed native change")
  await expect(detail(reviewed, "Authoritative zone")).toHaveText("authority.example.test")
  await expect(detail(reviewed, "Record TTL")).toContainText("86400")
  await expect(section(page, "Before the change")).toContainText(
    "_policy._tcp.authority.example.test",
  )
  const dialog = await confirmation(page)
  await dialog.getByRole("button", { name: "Apply native change", exact: true }).click()
  await expect(section(page, "Native readback")).toContainText("new.authority.example.test")
  await expect(section(page, "Native readback")).toContainText("2001:db8::9")
  await expect(section(page, "Native readback")).toContainText("86400")
  await expect(section(page, "Native readback")).toContainText(
    "_policy._tcp.authority.example.test",
  )
  expect(control.reads).toContain(
    "/network/dns/services/dns-fixture/zones/authority.example.test/records",
  )
  expect(control.reads).toContain("/network/dns/services/changes/change-fixture/current")
  expect(posts(control)).toHaveLength(1)
  expect(control.unexpectedReads).toEqual([])
})

test("using an editable native row preserves exact owner, family, value and TTL for removal", async ({
  page,
}) => {
  const control = await mockDNSServicePage(page, { engine: "technitium" })
  await openInventory(page)
  await sheet(page).getByRole("button", { name: "Review remove zone record", exact: true }).click()
  await selectZone(page)
  await sheet(page)
    .getByRole("button", {
      name: "Use record existing.authority.example.test A 192.0.2.91",
      exact: true,
    })
    .click()
  await expect(sheet(page).getByLabel("DNS record name", { exact: true })).toHaveValue(
    "existing.authority.example.test",
  )
  await expect(sheet(page).getByLabel("Record value", { exact: true })).toHaveValue("192.0.2.91")
  await expect(sheet(page).getByLabel("TTL (seconds)", { exact: true })).toHaveValue("300")
  await createReview(page).click()
  expect(control.mutations[0].body).toEqual({
    action: "record_remove",
    zone: "authority.example.test",
    record: { name: "existing.authority.example.test", type: "A", value: "192.0.2.91", ttl: 300 },
  })
  await expect(applyReview(page)).toBeEnabled()
  const dialog = await confirmation(page)
  await dialog.getByRole("button", { name: "Apply native change", exact: true }).click()
  await expect(section(page, "Before the change")).toContainText("existing.authority.example.test")
  await expect(section(page, "Native readback")).not.toContainText(
    "existing.authority.example.test",
  )
  await expect(section(page, "Native readback")).toContainText("v6.authority.example.test")
  expect(posts(control)).toHaveLength(1)
})

for (const empty of [false, true]) {
  test(`Pi-hole existing-client groups retain ${empty ? "an explicit empty list" : "exact native IDs"} and unchanged comment`, async ({
    page,
  }) => {
    const control = await mockDNSServicePage(page, { engine: "pihole" })
    await selectedClient(page)
    const defaultGroup = sheet(page).getByRole("checkbox", {
      name: /^Default filtering group · ID 0(?:\s|$)/,
    })
    const officeGroup = sheet(page).getByRole("checkbox", {
      name: /^No filtering group · ID 7(?:\s|$)/,
    })
    await expect(officeGroup).toBeChecked()
    await officeGroup.uncheck()
    if (!empty) {
      await defaultGroup.check()
      await officeGroup.check()
    }
    await createReview(page).click()
    expect(control.mutations[0].body).toEqual({
      action: "client_groups",
      client: { address: "192.0.2.10", groups: empty ? [] : [0, 7] },
    })
    const reviewed = section(page, "Reviewed native change")
    await expect(detail(reviewed, "Client address")).toHaveText("192.0.2.10")
    await expect(detail(reviewed, "New group IDs")).toHaveText(
      empty ? /None|empty|No groups/i : /0.*7/,
    )
    const before = section(page, "Before the change")
    await expect(detail(before, "Selected client address")).toHaveText("192.0.2.10")
    await expect(detail(before, "Selected client group IDs")).toHaveText("7")
    await expect(detail(before, "Client comment")).toHaveText(
      "Keep this existing native client comment",
    )
    await expect(applyReview(page)).toBeEnabled()
    const dialog = await confirmation(page)
    await dialog.getByRole("button", { name: "Apply native change", exact: true }).click()
    const after = section(page, "Native readback")
    await expect(detail(after, "Selected client group IDs")).toHaveText(
      empty ? /None|empty|No groups/i : /0.*7/,
    )
    await expect(detail(after, "Client comment")).toHaveText(
      "Keep this existing native client comment",
    )
    expect(posts(control)).toHaveLength(1)
  })
}

test("refreshing native clients preserves edited group choices and an empty-group client remains selectable", async ({
  page,
}) => {
  const control = await mockDNSServicePage(page, { engine: "pihole" })
  await selectedClient(page)
  const group = sheet(page).getByRole("checkbox", {
    name: /^No filtering group · ID 7(?:\s|$)/,
  })
  await group.uncheck()
  await sheet(page).getByRole("button", { name: "Refresh native reading", exact: true }).click()
  await expect(group).not.toBeChecked()
  await sheet(page).getByLabel("Existing Pi-hole client", { exact: true }).click()
  await page.getByRole("option").filter({ hasText: "198.51.100.0/24" }).click()
  await expect(group).not.toBeChecked()
  await createReview(page).click()
  expect(control.mutations[0].body).toEqual({
    action: "client_groups",
    client: { address: "198.51.100.0/24", groups: [] },
  })
  expect(posts(control)).toEqual([])
})

for (const comment of [null, ""] as const) {
  test(`native ${comment === null ? "null" : "empty"} client comment stays explicit before and after group assignment`, async ({
    page,
  }) => {
    const control = await mockDNSServicePage(page, { engine: "pihole" })
    control.clientComment = comment
    await selectedClient(page)
    await sheet(page)
      .getByRole("checkbox", { name: /^No filtering group · ID 7(?:\s|$)/ })
      .uncheck()
    await createReview(page).click()
    const label = comment === null ? "None (native null)" : "Empty native comment"
    await expect(detail(section(page, "Before the change"), "Client comment")).toHaveText(label)
    expect(control.changes[0].before!.selectedClient!.comment).toBe(comment)
    const originalFingerprint = control.changes[0].before!.selectedClient!.commentFingerprint
    await expect(applyReview(page)).toBeEnabled()
    const dialog = await confirmation(page)
    await dialog.getByRole("button", { name: "Apply native change", exact: true }).click()
    await expect(detail(section(page, "Native readback"), "Client comment")).toHaveText(label)
    expect(control.changes[0].after!.selectedClient!.comment).toBe(comment)
    expect(control.changes[0].after!.selectedClient!.commentFingerprint).toBe(originalFingerprint)
    expect(control.mutations[0].body).toEqual({
      action: "client_groups",
      client: { address: "192.0.2.10", groups: [] },
    })
    expect(posts(control)).toHaveLength(1)
  })
}

test("record draft validation keeps invalid TTL, family and owner fields without staging", async ({
  page,
}) => {
  const control = await mockDNSServicePage(page, { engine: "technitium" })
  await recordDraft(
    page,
    "Review add zone record",
    "new.authority.example.test",
    "2001:db8::9",
    "AAAA",
  )
  await selectZone(page)
  const ttl = sheet(page).getByLabel("TTL (seconds)", { exact: true })
  await ttl.fill("0")
  await createReview(page).click()
  await expect(
    sheet(page).getByRole("link", {
      name: /^TTL \(seconds\): Enter the exact TTL from 1 to 86400/i,
    }),
  ).toBeVisible()
  await expect(ttl).toHaveValue("0")
  expect(control.mutations).toEqual([])
  await ttl.fill("600")
  await sheet(page).getByLabel("DNS record name", { exact: true }).fill("outside.example.test")
  await createReview(page).click()
  await expect(
    sheet(page).getByRole("link", {
      name: /^Authoritative zone: Select an explicit containing Technitium primary zone/i,
    }),
  ).toBeVisible()
  await expect(sheet(page).getByLabel("DNS record name", { exact: true })).toHaveValue(
    "outside.example.test",
  )
  expect(control.mutations).toEqual([])
  await sheet(page)
    .getByLabel("DNS record name", { exact: true })
    .fill("new.authority.example.test")
  await sheet(page).getByLabel("Record value", { exact: true }).fill("198.51.100.9")
  await createReview(page).click()
  await expect(
    sheet(page).getByRole("link", {
      name: /^Record value: Use a canonical unicast IP of the selected record family/i,
    }),
  ).toBeVisible()
  await expect(sheet(page).getByLabel("Record value", { exact: true })).toHaveValue("198.51.100.9")
  await expect(ttl).toHaveValue("600")
  expect(control.mutations).toEqual([])
})

test("a refused native record preview keeps its complete draft and sends no apply", async ({
  page,
}) => {
  const control = await mockDNSServicePage(page)
  control.stageFailure = true
  await recordDraft(
    page,
    "Review add local override",
    "retained.example.test",
    "2001:db8::9",
    "AAAA",
  )
  await createReview(page).click()
  await expect(sheet(page).getByText(/Fixture selected native policy review refused/)).toBeVisible()
  await expect(sheet(page).getByLabel("DNS record name", { exact: true })).toHaveValue(
    "retained.example.test",
  )
  await expect(sheet(page).getByLabel("Record type", { exact: true })).toContainText("AAAA")
  await expect(sheet(page).getByLabel("Record value", { exact: true })).toHaveValue("2001:db8::9")
  expect(control.mutations).toHaveLength(1)
  expect(posts(control)).toEqual([])
})

test("record errors focus the summary and its link names and focuses the described field", async ({
  page,
}) => {
  const control = await mockDNSServicePage(page)
  await recordDraft(page, "Review add local override", "retained.example.test", "not-an-ip")
  await createReview(page).click()
  const link = sheet(page).getByRole("link", { name: /^Record value: Use a canonical unicast IP/ })
  const summary = sheet(page)
    .locator('[role="alert"][tabindex="-1"]')
    .filter({ has: page.getByRole("link", { name: /^Record value: Use a canonical unicast IP/ }) })
  await expect(summary).toBeFocused()
  const value = sheet(page).getByLabel("Record value", { exact: true })
  await expect(value).toHaveAttribute("aria-invalid", "true")
  await expect(value).toHaveAccessibleDescription(
    /canonical unicast IP of the selected record family/,
  )
  await link.click()
  await expect(value).toBeFocused()
  await expect(value).toHaveValue("not-an-ip")
  await expect(sheet(page).getByLabel("DNS record name", { exact: true })).toHaveValue(
    "retained.example.test",
  )
  expect(control.mutations).toEqual([])
})

for (const removed of ["client", "group"] as const) {
  test(`a selected ${removed} removed by a fresh native reading cannot stage its retained group draft`, async ({
    page,
  }) => {
    const control = await mockDNSServicePage(page, { engine: "pihole" })
    await selectedClient(page)
    if (removed === "client")
      control.view.snapshot!.clients = control.view.snapshot!.clients.filter(
        (client) => !client.addresses.includes("192.0.2.10"),
      )
    else
      control.view.snapshot!.filterGroups = control.view.snapshot!.filterGroups.filter(
        (group) => group.id !== 7,
      )
    await sheet(page).getByRole("button", { name: "Refresh native reading", exact: true }).click()
    if (removed === "client")
      await expect(
        sheet(page).getByLabel("Native client settings", { exact: true }),
      ).not.toContainText("Office client")
    else
      await expect(
        sheet(page).getByRole("checkbox", { name: /^No filtering group · ID 7(?:\s|$)/ }),
      ).toHaveCount(0)
    await createReview(page).click()
    const field =
      removed === "client"
        ? sheet(page).getByLabel("Existing Pi-hole client", { exact: true })
        : sheet(page).getByRole("group", { name: "Native groups", exact: true })
    const message =
      removed === "client"
        ? /Select a client still present in the current native reading/
        : /Choose groups still present in the current native reading/
    const link = sheet(page).getByRole("link", { name: message })
    const summary = sheet(page)
      .locator('[role="alert"][tabindex="-1"]')
      .filter({ has: page.getByRole("link", { name: message }) })
    await expect(summary).toBeFocused()
    await expect(field).toHaveAccessibleDescription(message)
    await link.click()
    await expect(field).toBeFocused()
    await expect(sheet(page).getByText(/Selected group IDs: 7\./)).toBeVisible()
    expect(control.mutations).toEqual([])
  })
}

for (const [name, patch] of [
  ["internal", { internal: true }],
  ["signed", { dnssec: "SignedWithNSEC3" }],
  ["secondary", { type: "Secondary" }],
  ["unreported on an unverified version", { nativeVersion: "15.7", internal: null }],
] as const) {
  test(`a ${name} zone remains visible without editable record rows or review`, async ({
    page,
  }) => {
    const control = await mockDNSServicePage(page, { engine: "technitium" })
    control.records = dnsRecords(patch)
    control.view.snapshot!.version = control.records.nativeVersion
    control.view.snapshot!.zones[0] = {
      name: control.records.zone,
      type: control.records.type,
      disabled: control.records.disabled,
      dnssec: control.records.dnssec,
    }
    await recordDraft(page, "Review add zone record", "new.authority.example.test", "192.0.2.9")
    await selectZone(page)
    await expect(sheet(page).getByRole("button", { name: /^Use record / })).toHaveCount(0)
    await expect(createReview(page)).toBeDisabled()
    expect(control.mutations).toEqual([])
    expect(control.reads).toContain(
      "/network/dns/services/dns-fixture/zones/authority.example.test/records",
    )
  })
}

test("AdGuard override enabled, disabled and unreported state remain distinct", async ({
  page,
}) => {
  const control = await mockDNSServicePage(page)
  control.view.snapshot!.localOverrides = [
    { name: "enabled.example.test", type: "A", value: "192.0.2.91", enabled: true },
    { name: "disabled.example.test", type: "A", value: "192.0.2.92", enabled: false },
    { name: "unreported.example.test", type: "A", value: "192.0.2.93" },
  ]
  await openInventory(page)
  const overrides = sheet(page).getByLabel("Local overrides", { exact: true })
  for (const [name, state] of [
    ["enabled.example.test", /Enabled/],
    ["disabled.example.test", /Disabled/],
    ["unreported.example.test", /unreported|unspecified/i],
  ] as const) {
    const row = overrides.locator("li").filter({ hasText: name })
    await expect(row).toContainText(state)
  }
  expect(control.mutations).toEqual([])
})

test("native refusal of a disabled AdGuard override preserves the removal draft", async ({
  page,
}) => {
  const control = await mockDNSServicePage(page)
  control.view.snapshot!.localOverrides = [
    { name: "disabled.example.test", type: "A", value: "192.0.2.92", enabled: false },
  ]
  await recordDraft(page, "Review remove local override", "disabled.example.test", "192.0.2.92")
  await createReview(page).click()
  await expect(
    sheet(page).getByText(/Fixture disabled native override cannot be removed/),
  ).toBeVisible()
  await expect(sheet(page).getByLabel("DNS record name", { exact: true })).toHaveValue(
    "disabled.example.test",
  )
  await expect(sheet(page).getByLabel("Record value", { exact: true })).toHaveValue("192.0.2.92")
  expect(control.mutations[0].body).toEqual({
    action: "override_remove",
    record: { name: "disabled.example.test", type: "A", value: "192.0.2.92" },
  })
  expect(control.changes).toEqual([])
  expect(posts(control)).toEqual([])
})

test("a failed zone-record refresh keeps the exact record draft and blocks staging", async ({
  page,
}) => {
  const control = await mockDNSServicePage(page, { engine: "technitium" })
  await recordDraft(page, "Review add zone record", "new.authority.example.test", "192.0.2.9")
  await selectZone(page)
  await sheet(page).getByLabel("TTL (seconds)", { exact: true }).fill("600")
  control.recordsFailure = true
  await sheet(page).getByRole("button", { name: "Read zone records", exact: true }).click()
  await expect(sheet(page).getByText(/Fixture authoritative record read failed/)).toBeVisible()
  await expect(sheet(page).getByLabel("DNS record name", { exact: true })).toHaveValue(
    "new.authority.example.test",
  )
  await expect(sheet(page).getByLabel("Record value", { exact: true })).toHaveValue("192.0.2.9")
  await expect(sheet(page).getByLabel("TTL (seconds)", { exact: true })).toHaveValue("600")
  await expect(createReview(page)).toBeDisabled()
  expect(control.mutations).toEqual([])
})

for (const failure of [
  "drift",
  "unavailable",
  "missing-selection",
  "read-failure",
  "generation",
] as const) {
  test(`an open zone-record confirmation is held after selection-aware ${failure}`, async ({
    page,
  }) => {
    await page.clock.install()
    const control = await mockDNSServicePage(page, { engine: "technitium" })
    const change = dnsPolicyChange(
      {
        action: "record_remove",
        zone: "authority.example.test",
        record: {
          name: "existing.authority.example.test",
          type: "A",
          value: "192.0.2.91",
          ttl: 300,
        },
      },
      control.view.connection,
      control.view.snapshot,
      control.records,
    )
    control.changes = [change]
    await openChange(page)
    await expect(applyReview(page)).toBeEnabled()
    const dialog = await confirmation(page)
    if (failure === "drift") {
      control.records.records[0].comments = "Native comment changed after review"
      control.records.fingerprint = "f".repeat(64)
    } else if (failure === "unavailable") {
      control.currentView = {
        connection: control.view.connection,
        state: "unavailable",
        error: "Fixture selected native owner unavailable.",
      }
    } else if (failure === "missing-selection") control.currentMissingSelection = true
    else if (failure === "generation") control.view.connection.generation++
    else control.currentFailure = true
    await page.clock.fastForward(5500)
    await expect(applyReview(page)).toBeDisabled()
    await dialog.getByRole("button", { name: "Apply native change", exact: true }).click()
    await expect(
      sheet(page)
        .getByText(/Refresh the retained review and its native owner/)
        .last(),
    ).toBeVisible()
    await expect(section(page, "Before the change")).toContainText(
      "existing.authority.example.test",
    )
    expect(posts(control)).toEqual([])
    expect(control.reads).toContain("/network/dns/services/changes/change-fixture/current")
  })
}

test("an open client-group confirmation refuses raw comment drift although group IDs match", async ({
  page,
}) => {
  await page.clock.install()
  const control = await mockDNSServicePage(page, { engine: "pihole" })
  const change = dnsPolicyChange(
    { action: "client_groups", client: { address: "192.0.2.10", groups: [] } },
    control.view.connection,
    control.view.snapshot,
  )
  control.changes = [change]
  await openChange(page)
  await expect(applyReview(page)).toBeEnabled()
  const dialog = await confirmation(page)
  const fresh = structuredClone(change.before!)
  fresh.selectedClient!.comment = "Native comment changed without group reassignment"
  fresh.selectedClient!.commentFingerprint = "b".repeat(64)
  fresh.selectionFingerprint = "c".repeat(64)
  fresh.policyFingerprint = "d".repeat(64)
  control.currentView = { connection: control.view.connection, state: "available", snapshot: fresh }
  await page.clock.fastForward(5500)
  await expect(applyReview(page)).toBeDisabled()
  await dialog.getByRole("button", { name: "Apply native change", exact: true }).click()
  await expect(
    sheet(page)
      .getByText(/Refresh the retained review and its native owner/)
      .last(),
  ).toBeVisible()
  expect(posts(control)).toEqual([])
})

test("an open confirmation refuses changed retained record metadata even with reused fingerprints", async ({
  page,
}) => {
  await page.clock.install()
  const control = await mockDNSServicePage(page, { engine: "technitium" })
  control.changes = [
    dnsPolicyChange(
      {
        action: "record_remove",
        zone: "authority.example.test",
        record: {
          name: "existing.authority.example.test",
          type: "A",
          value: "192.0.2.91",
          ttl: 300,
        },
      },
      control.view.connection,
      control.view.snapshot,
      control.records,
    ),
  ]
  await openChange(page)
  await expect(applyReview(page)).toBeEnabled()
  const dialog = await confirmation(page)
  control.changes[0].before!.records!.records[0].comments = "Retained record metadata changed"
  await page.clock.fastForward(5500)
  await expect(section(page, "Before the change")).toContainText("Retained record metadata changed")
  await dialog.getByRole("button", { name: "Apply native change", exact: true }).click()
  await expect(
    sheet(page).getByText(/This review changed or already has an attempted apply/),
  ).toBeVisible()
  expect(posts(control)).toEqual([])
})

test("an uncertain client-group apply stays single-use after reload with selected review readable", async ({
  page,
}) => {
  const control = await mockDNSServicePage(page, { engine: "pihole" })
  control.changes = [
    dnsPolicyChange(
      { action: "client_groups", client: { address: "192.0.2.10", groups: [] } },
      control.view.connection,
      control.view.snapshot,
    ),
  ]
  control.apply = "lost"
  await openChange(page)
  await expect(applyReview(page)).toBeEnabled()
  const dialog = await confirmation(page)
  await dialog.getByRole("button", { name: "Apply native change", exact: true }).click()
  await expect(sheet(page).getByText(/this review will not be applied again/)).toBeVisible()
  await page.reload()
  await openInventory(page)
  await sheet(page)
    .getByRole("button", { name: "Read DNS change change-fixture", exact: true })
    .click()
  await expect(section(page, "Before the change")).toContainText(
    "Keep this existing native client comment",
  )
  await expect(
    sheet(page)
      .getByText(/Fixture native outcome needs review/)
      .first(),
  ).toBeVisible()
  await expect(applyReview(page)).toHaveCount(0)
  expect(posts(control)).toHaveLength(1)
})

for (const width of [390, 1280, 1720]) {
  test(`native records and reviewed readback stay flat and within ${width}px`, async ({
    page,
  }, info) => {
    await page.setViewportSize({ width, height: 960 })
    const control = await mockDNSServicePage(page, { engine: "technitium" })
    await recordDraft(
      page,
      "Review add zone record",
      "new.authority.example.test",
      "2001:db8::9",
      "AAAA",
    )
    await selectZone(page)
    await sheet(page).getByLabel("TTL (seconds)", { exact: true }).fill("600")
    await expect(page.locator("[data-slot=page]").last()).toHaveAttribute(
      "data-register",
      "reading",
    )
    expect(
      await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth),
    ).toBe(true)
    expect(await sheet(page).evaluate((el) => el.scrollWidth <= el.clientWidth)).toBe(true)
    await page.screenshot({
      path: info.outputPath(`dns-record-inventory-${width}.png`),
      fullPage: true,
    })
    await createReview(page).click()
    await expect(applyReview(page)).toBeEnabled()
    await expect(section(page, "Before the change")).toContainText(
      "_policy._tcp.authority.example.test",
    )
    expect(await sheet(page).evaluate((el) => el.scrollWidth <= el.clientWidth)).toBe(true)
    await page.screenshot({
      path: info.outputPath(`dns-record-review-${width}.png`),
      fullPage: true,
    })
    const dialog = await confirmation(page)
    expect(await dialog.evaluate((el) => el.scrollWidth <= el.clientWidth)).toBe(true)
    await dialog.getByRole("button", { name: "Apply native change", exact: true }).click()
    await expect(section(page, "Native readback")).toContainText("new.authority.example.test")
    expect(await sheet(page).evaluate((el) => el.scrollWidth <= el.clientWidth)).toBe(true)
    await page.screenshot({
      path: info.outputPath(`dns-record-readback-${width}.png`),
      fullPage: true,
    })
    expect(posts(control)).toHaveLength(1)
  })
}
