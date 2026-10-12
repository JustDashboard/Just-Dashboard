import { expect, test, type Page } from "@playwright/test"
import { mockDatabases } from "./database-fixture"

const fix = 'CREATE INDEX "orders_customer_id_idx" ON "public"."orders" ("customer_id");'
const findings = [
  {
    id: "no-backup",
    level: "warning",
    category: "reliability",
    title: "No backup of this database has been taken from here",
    detail:
      "No dump is recorded by this dashboard. Backups made by another tool are not checked here.",
    link: "/databases/1/backups",
  },
  {
    id: "unindexed-foreign-key",
    level: "warning",
    category: "performance",
    title: "53 foreign keys with no index",
    detail: "Deleting a referenced row scans the entire referencing table.",
    sql: fix,
    targets: [{ kind: "table", schema: "public", name: "orders" }],
  },
  {
    id: "container-no-memory-limit",
    level: "notice",
    category: "reliability",
    title: "The container has no memory limit",
    detail: "A configuration preference, without observed memory pressure.",
  },
  {
    id: "no-stat-statements",
    level: "notice",
    category: "maintenance",
    title: "Statement statistics are not being collected",
    detail: "Optional query instrumentation is not enabled.",
  },
]

async function mockFindings(page: Page, options: { viewer?: boolean; protected?: boolean } = {}) {
  await mockDatabases(page, {
    viewer: options.viewer,
    rows: options.protected ? { 1: { readOnly: true } } : undefined,
  })
  await page.routeWebSocket("**/api/v1/system/stream**", () => {})
  await page.route("**/api/v1/databases/1/advisor", (route) =>
    route.fulfill({
      json: {
        checkedAt: new Date().toISOString(),
        tablesChecked: 53,
        tablesOmitted: 0,
        engineChecks: 3,
        silences: [],
        findings,
      },
    }),
  )
}

const attention = (page: Page) => page.getByRole("region", { name: "Needs attention", exact: true })

for (const width of [390, 1280, 1720]) {
  test(`findings expose their consequence and action without a disclosure at ${width}px`, async ({
    page,
  }) => {
    await page.setViewportSize({ width, height: 900 })
    await mockFindings(page)
    await page.goto("/databases/1")
    const block = attention(page)
    await expect(block.locator("[data-finding]")).toHaveCount(2)
    await expect(
      block.getByText("Deleting a referenced row scans the entire referencing table.", {
        exact: true,
      }),
    ).toBeVisible()
    await expect(block.getByRole("button", { name: "Open Backups", exact: true })).toBeVisible()
    await expect(
      block.getByRole("button", { name: "Review the fix in Query", exact: true }),
    ).toBeVisible()
    await expect(block.getByText("The container has no memory limit", { exact: true })).toHaveCount(
      0,
    )
    await expect(
      block.getByText("Statement statistics are not being collected", { exact: true }),
    ).toHaveCount(0)
    await expect(block.locator("[data-slot=accordion-trigger][aria-expanded=true]")).toHaveCount(0)
    expect(await block.locator("button button").count()).toBe(0)
    for (const button of await block.getByRole("button").all()) {
      const box = await button.boundingBox()
      expect(box).not.toBeNull()
      expect(box!.x).toBeGreaterThanOrEqual(0)
      expect(box!.x + box!.width).toBeLessThanOrEqual(width)
    }
    const dot = await block.locator("[data-finding]").first().locator(".bg-warning").boundingBox()
    expect(dot!.width).toBeGreaterThan(0)
    expect(dot!.height).toBeGreaterThan(0)
  })
}

test("the backup action reaches the controls for this database", async ({ page }) => {
  await mockFindings(page)
  await page.goto("/databases/1")
  await attention(page).getByRole("button", { name: "Open Backups", exact: true }).click()
  await expect(page).toHaveURL(/\/databases\/1\/backups$/)
  await expect(page.getByRole("button", { name: "Back up now", exact: true })).toBeVisible()
})

test("reviewing a fix reaches Query with the complete statement and executes nothing", async ({
  page,
}) => {
  const writes: string[] = []
  page.on("request", (request) => {
    if (request.url().includes("/api/v1/databases/") && request.method() !== "GET")
      writes.push(request.url())
  })
  await mockFindings(page)
  await page.goto("/databases/1")
  await attention(page)
    .getByRole("button", { name: "Review the fix in Query", exact: true })
    .click()
  await expect(page).toHaveURL(/\/databases\/1\/query\?/)
  expect(new URL(page.url()).searchParams.get("sql")).toBe(fix)
  await expect(
    page
      .getByRole("button", { name: "Run", exact: true })
      .and(page.locator('[title="Ctrl+Enter"]')),
  ).toBeVisible()
  expect(writes).toEqual([])
})

for (const mode of ["viewer", "protected"] as const) {
  test(`${mode} databases offer schema inspection instead of a write action`, async ({ page }) => {
    await mockFindings(page, { viewer: mode === "viewer", protected: mode === "protected" })
    await page.goto("/databases/1")
    await expect(
      attention(page).getByRole("button", { name: "Review the fix in Query", exact: true }),
    ).toHaveCount(0)
    await attention(page).getByRole("button", { name: "Inspect schema", exact: true }).click()
    await expect(page).toHaveURL(/\/databases\/1\/schema\?/)
    const query = new URL(page.url()).searchParams
    expect(query.get("schema")).toBe("public")
    expect(query.get("table")).toBe("orders")
  })
}
