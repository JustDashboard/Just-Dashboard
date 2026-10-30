import { expect, test, type Page } from "@playwright/test"
import { json, mockProject } from "./deploy-fixture"

/**
 * Domains → a domain's DNS record: the sheet a row's menu opens, and a
 * preflight finding's Create the record opens by address. It reads the dry
 * run, says what the zone holds and what the press writes, through which
 * saved credential and zone, and writes it — with replace only when the dry
 * run said the record is not this dashboard's.
 */

const ROUTE = "**/api/v1/deploy/7/domains/api.example.test/dns-record"

function plan(overrides: Record<string, unknown> = {}) {
  return {
    hostname: "api.example.test",
    environmentId: 12,
    environment: "production",
    tunnel: false,
    want: [
      { name: "api.example.test", type: "A", values: ["203.0.113.7"], ttl: 300 },
      { name: "api.example.test", type: "AAAA", values: ["2001:db8::7"], ttl: 300 },
    ],
    provider: "Cloudflare",
    providerKey: "cloudflare",
    zone: "example.test",
    candidates: [{ key: "cloudflare", provider: "Cloudflare", zone: "example.test" }],
    problems: [],
    changes: [
      {
        type: "A",
        name: "api.example.test",
        action: "create",
        current: [],
        want: ["203.0.113.7"],
        ttl: 300,
        detail: "Create A api.example.test → 203.0.113.7.",
        ours: false,
      },
      {
        type: "AAAA",
        name: "api.example.test",
        action: "create",
        current: [],
        want: ["2001:db8::7"],
        ttl: 300,
        detail: "Create AAAA api.example.test → 2001:db8::7.",
        ours: false,
      },
    ],
    writable: true,
    ready: false,
    foreign: false,
    summary:
      "Writes into example.test at Cloudflare with the Cloudflare credentials saved under Certificates: Create A api.example.test → 203.0.113.7; Create AAAA api.example.test → 2001:db8::7.",
    managed: [],
    ...overrides,
  }
}

function written() {
  const at = new Date().toISOString()
  const base = plan()
  return {
    ...base,
    writable: false,
    ready: true,
    changes: base.changes.map((change) => ({
      ...change,
      action: "keep",
      current: change.want,
      ours: true,
    })),
    managed: ["A", "AAAA"].map((type) => ({
      type,
      values: type === "A" ? ["203.0.113.7"] : ["2001:db8::7"],
      ttl: 300,
      provider: "cloudflare",
      zone: "example.test",
      createdBy: "tester",
      createdAt: at,
      updatedBy: "tester",
      updatedAt: at,
      current: true,
    })),
    written: base.changes,
    summary:
      "Written into example.test at Cloudflare: created A api.example.test → 203.0.113.7; created AAAA api.example.test → 2001:db8::7.",
  }
}

async function mockRecord(page: Page, dryRun: unknown, posts: unknown[] = []) {
  await page.route(ROUTE, async (route) => {
    if (route.request().method() === "POST") {
      posts.push(route.request().postData() ? route.request().postDataJSON() : null)
      return json(route, written())
    }
    return json(route, dryRun)
  })
}

test("a domain's menu opens its DNS record, which is created in its zone and then managed here", async ({
  page,
}) => {
  await mockProject(page)
  const posts: unknown[] = []
  await mockRecord(page, plan(), posts)
  await page.goto("/deploy/7/settings/domains")
  await page.getByRole("button", { name: "Actions for api.example.test" }).click()
  await page.getByRole("menuitem", { name: "Open the DNS record" }).click()

  const sheet = page.getByRole("dialog", { name: "api.example.test" })
  await expect(sheet.getByText("Missing", { exact: true })).toBeVisible()
  const records = sheet.getByRole("list", { name: "Records" })
  await expect(records.getByText("A  api.example.test  →  203.0.113.7")).toBeVisible()
  await expect(records.getByText("AAAA  api.example.test  →  2001:db8::7")).toBeVisible()
  await expect(records.getByText("To create")).toHaveCount(2)
  await expect(records.getByText("Nothing at the name yet")).toHaveCount(2)
  await expect(
    sheet.getByText(
      "Zone example.test at Cloudflare, with the credentials saved under Certificates",
    ),
  ).toBeVisible()

  await page.screenshot({ path: test.info().outputPath("dns-record-dry-run.png") })
  await sheet.getByRole("button", { name: "Create the record" }).click()
  await expect(sheet.getByText("Points here", { exact: true })).toBeVisible()
  await expect(sheet.getByText("Record managed here · created just now by tester")).toBeVisible()
  await expect(records.getByText("Managed here")).toHaveCount(2)
  await expect(sheet.getByRole("button", { name: "Create the record" })).toHaveCount(0)
  // Nothing was this dashboard's to replace, so nothing said it may.
  expect(posts).toEqual([null])
  await page.screenshot({ path: test.info().outputPath("dns-record-written.png") })
})

test("a record not written here is replaced only by saying so, and a finding's link opens the sheet", async ({
  page,
}) => {
  await mockProject(page)
  const posts: unknown[] = []
  await mockRecord(
    page,
    plan({
      foreign: true,
      summary:
        "Writes into example.test at Cloudflare with the Cloudflare credentials saved under Certificates: Change A api.example.test from 198.51.100.9 to 203.0.113.7. It replaces a record this dashboard did not write.",
      changes: [
        {
          type: "A",
          name: "api.example.test",
          action: "update",
          current: ["198.51.100.9"],
          want: ["203.0.113.7"],
          ttl: 300,
          detail: "Change A api.example.test from 198.51.100.9 to 203.0.113.7.",
          ours: false,
        },
      ],
    }),
    posts,
  )
  // Where a preflight finding's Create the record leads; the address is
  // dropped once the sheet is open.
  await page.goto("/deploy/7/settings/domains?record=api.example.test")
  const sheet = page.getByRole("dialog", { name: "api.example.test" })
  await expect(sheet.getByText("Names another server", { exact: true })).toBeVisible()
  await expect(sheet.getByText("Now 198.51.100.9")).toBeVisible()
  await expect(sheet.getByText("To replace")).toBeVisible()
  await expect(page).toHaveURL(/\/deploy\/7\/settings\/domains$/)
  await page.screenshot({ path: test.info().outputPath("dns-record-replace.png") })
  await sheet.getByRole("button", { name: "Replace the record" }).click()
  await expect(sheet.getByText("Points here", { exact: true })).toBeVisible()
  expect(posts).toEqual([{ replace: true }])
})

test("a record no saved credential reaches says so and leads to Certificates", async ({ page }) => {
  await mockProject(page)
  await mockRecord(
    page,
    plan({
      writable: false,
      provider: undefined,
      providerKey: undefined,
      zone: undefined,
      changes: [],
      refusalCode: "no_credentials",
      refusal:
        "No DNS provider has credentials saved on this server, so the record for api.example.test cannot be written from here.",
      summary:
        "No DNS provider has credentials saved on this server, so the record for api.example.test cannot be written from here.",
    }),
  )
  await page.setViewportSize({ width: 390, height: 844 })
  await page.goto("/deploy/7/settings/domains?record=api.example.test")
  const sheet = page.getByRole("dialog", { name: "api.example.test" })
  await expect(sheet.getByText("Cannot be written from here", { exact: true })).toBeVisible()
  await expect(sheet.getByText("No DNS credentials are saved", { exact: true })).toBeVisible()
  // What the press cannot write is drawn to copy where the zone is kept.
  const byHand = sheet.getByRole("list", { name: "Records to create by hand" })
  await expect(byHand.getByText("A  api.example.test  →  203.0.113.7")).toBeVisible()
  await expect(byHand.getByText("To create by hand")).toHaveCount(2)
  await expect(sheet.getByText(/No DNS provider has credentials saved/)).toHaveCount(1)
  await expect(sheet.getByRole("link", { name: "Open Certificates" })).toHaveAttribute(
    "href",
    "/proxy/certificates",
  )
  await expect(sheet.getByRole("button", { name: /the record$/ })).toHaveCount(0)
  await page.screenshot({ path: test.info().outputPath("dns-record-refused-mobile.png") })
  expect(
    await page.evaluate(() => document.documentElement.scrollWidth - innerWidth),
  ).toBeLessThanOrEqual(1)
})
