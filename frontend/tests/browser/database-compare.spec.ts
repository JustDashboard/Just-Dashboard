import { expect as baseExpect, test, type Page, type Route } from "@playwright/test"
import { CONNECTIONS, mockDatabases, summaryOf, SHOP, type DatabaseMock } from "./database-fixture"

/**
 * Compare (`/databases/<id>/compare`): two schemas of one engine read side by
 * side, and the migration that turns this connection's into the other. The API
 * is mocked in the browser; what the requests carry is what is asserted.
 */

const expect = baseExpect.configure({ timeout: 15_000 })
test.describe.configure({ timeout: 90_000 })

async function json(route: Route, body: unknown, status = 200) {
  await route.fulfill({ status, contentType: "application/json", body: JSON.stringify(body) })
}

const STAGING = {
  ...SHOP,
  id: 3,
  name: "shop-staging",
  database: "shop_staging",
  environment: "staging",
}

const SCHEMAS = [
  { name: "public", default: true, tables: 4 },
  { name: "staging", tables: 5 },
  { name: "pg_catalog", system: true, tables: 140 },
]

const side = (id: number, name: string, schema: string) => ({
  id,
  name,
  driver: "postgres",
  flavor: "postgres",
  flavorLabel: "PostgreSQL",
  schema,
})

const ANSWER = {
  from: side(1, "shop", "public"),
  to: side(3, "shop-staging", "staging"),
  objects: [
    {
      kind: "table",
      name: "users",
      change: "changed",
      parts: [
        {
          part: "column",
          name: "email",
          change: "changed",
          from: "text",
          to: "varchar(120) NOT NULL",
        },
        {
          part: "column",
          name: "legacy_flag",
          change: "removed",
          from: "boolean DEFAULT false",
          to: "",
        },
        {
          part: "index",
          name: "users_email_idx",
          change: "added",
          from: "",
          to: "UNIQUE btree (email)",
        },
      ],
    },
    {
      kind: "table",
      name: "payments",
      change: "added",
      parts: [
        { part: "column", name: "id", change: "added", from: "", to: "bigint ALWAYS NOT NULL" },
        { part: "column", name: "amount", change: "added", from: "", to: "numeric(12,2) NOT NULL" },
        {
          part: "foreign key",
          name: "payments_order_id_fkey",
          change: "added",
          from: "",
          to: "(order_id) REFERENCES staging.orders (id)",
        },
      ],
    },
    {
      kind: "table",
      name: "audit_old",
      change: "removed",
      parts: [{ part: "column", name: "id", change: "removed", from: "integer NOT NULL", to: "" }],
    },
    {
      kind: "view",
      name: "v_users",
      change: "changed",
      parts: [
        {
          part: "definition",
          name: "v_users",
          change: "changed",
          from: " SELECT id,\n    email\n   FROM users;",
          to: " SELECT id,\n    email,\n    nickname\n   FROM users;",
        },
      ],
    },
    {
      kind: "enum",
      name: "mood",
      change: "changed",
      parts: [
        { part: "labels", name: "mood", change: "changed", from: "sad, meh, ok", to: "sad, ok" },
      ],
    },
    {
      kind: "sequence",
      name: "invoice_seq",
      change: "added",
      parts: [
        {
          part: "definition",
          name: "invoice_seq",
          change: "added",
          from: "",
          to: "CREATE SEQUENCE staging.invoice_seq",
        },
      ],
    },
  ],
  steps: [
    {
      title: "Create enum type tier",
      statements: ["CREATE TYPE \"public\".\"tier\" AS ENUM ('free', 'team')"],
      destructive: false,
      manual: false,
      note: "",
    },
    {
      title: "Create table payments",
      statements: [
        'CREATE TABLE "public"."payments" (\n  "id" bigint GENERATED ALWAYS AS IDENTITY NOT NULL,\n  "order_id" bigint NOT NULL,\n  "amount" numeric(12,2) NOT NULL,\n  CONSTRAINT "payments_pkey" PRIMARY KEY ("id")\n)',
      ],
      destructive: false,
      manual: false,
      note: "",
    },
    {
      title: "Change column email of users",
      statements: [
        'ALTER TABLE "public"."users" ALTER COLUMN "email" TYPE varchar(120), ALTER COLUMN "email" SET NOT NULL',
      ],
      destructive: true,
      manual: false,
      note: "The values are converted to the new type; a value that does not convert stops the statement.",
    },
    {
      title: "Drop column legacy_flag from users",
      statements: ['ALTER TABLE "public"."users" DROP COLUMN "legacy_flag"'],
      destructive: true,
      manual: false,
      note: "",
    },
    {
      title: "Change enum type mood",
      statements: [],
      destructive: false,
      manual: true,
      note: "Labels cannot be removed or reordered in place.",
    },
    {
      title: "Drop table audit_old",
      statements: ['DROP TABLE "public"."audit_old"'],
      destructive: true,
      manual: false,
      note: "",
    },
    {
      title: "Add foreign key payments_order_id_fkey to payments",
      statements: [
        'ALTER TABLE "public"."payments" ADD CONSTRAINT "payments_order_id_fkey" FOREIGN KEY ("order_id") REFERENCES "public"."orders" ("id")',
      ],
      destructive: false,
      manual: false,
      note: "",
    },
  ],
  notes: [],
}

type Sent = { path: string; query: URLSearchParams }

/** The shell's mocks plus a second PostgreSQL connection, its schemas, and the comparison. */
async function mockCompare(
  page: Page,
  options: DatabaseMock & { answer?: unknown; status?: number; fail?: unknown } = {},
) {
  await mockDatabases(page, options)
  const sent: Sent[] = []
  await page.route("**/api/v1/databases/", (route) =>
    json(route, [...CONNECTIONS.map((conn) => ({ ...conn, ...options.rows?.[conn.id] })), STAGING]),
  )
  await page.route(/\/api\/v1\/databases\/3$/, (route) => json(route, summaryOf(STAGING)))
  await page.route(/\/api\/v1\/databases\/[13]\/catalog/, (route) =>
    json(route, {
      schema: "public",
      defaultSchema: route.request().url().includes("/3/") ? "staging" : "public",
      schemas: SCHEMAS,
      objects: {},
      truncated: [],
      limit: 5000,
    }),
  )
  await page.route(/\/api\/v1\/databases\/1\/schema-diff/, (route) => {
    const url = new URL(route.request().url())
    sent.push({ path: url.pathname, query: url.searchParams })
    if (options.fail) return json(route, options.fail, options.status ?? 400)
    return json(route, options.answer ?? ANSWER)
  })
  return { sent }
}

const OPEN = "/databases/1/compare?to=3&toSchema=staging"

test("nothing is read until Compare is pressed, and then both sides are named in the request", async ({
  page,
}) => {
  const { sent } = await mockCompare(page)
  await page.goto(OPEN)
  await expect(page.getByText("Nothing compared yet")).toBeVisible()
  await expect(page.getByRole("combobox", { name: "From schema" })).toContainText("public")
  await expect(page.getByRole("combobox", { name: "To schema" })).toContainText("staging")
  expect(sent).toEqual([])

  await page.getByRole("button", { name: "Compare", exact: true }).click()
  await expect(page.getByText("6 differences: 2 added, 1 removed, 3 changed")).toBeVisible()
  expect(sent).toHaveLength(1)
  expect(Object.fromEntries(sent[0].query)).toEqual({
    to: "3",
    schema: "public",
    toSchema: "staging",
  })
})

test("the differences are grouped by kind and open to the two sides of each part", async ({
  page,
}) => {
  await mockCompare(page)
  await page.goto(OPEN)
  await page.getByRole("button", { name: "Compare", exact: true }).click()

  const differences = page.getByRole("region", { name: "Differences" })
  await expect(differences.locator("[data-kind=table]")).toContainText("Tables")
  await expect(differences.locator("[data-kind=view]")).toContainText("Views")
  await expect(differences.locator("[data-kind=enum]")).toContainText("Enum types")

  const users = differences.getByRole("button", { name: /^Changed users.*2 columns · 1 index/ })
  await users.click()
  const email = differences.locator("[data-part=column]").filter({ hasText: "email" })
  await expect(email).toContainText("text")
  await expect(email).toContainText("varchar(120) NOT NULL")
  // A column only one side has says the other side has none.
  const legacy = differences.locator("[data-part=column]").filter({ hasText: "legacy_flag" })
  await expect(legacy.getByLabel("Not in to")).toBeVisible()

  // A definition is read as text on both sides, with the changed lines marked.
  await differences.getByRole("button", { name: /^Changed v_users/ }).click()
  await expect(differences.locator("[data-side=from]")).toContainText("email")
  await expect(differences.locator("[data-side=to]")).toContainText("nickname")

  // A filter narrows to one change.
  await page.getByRole("button", { name: /^Removed \d+$/ }).click()
  await expect(differences.getByRole("button", { name: /^Removed audit_old/ })).toBeVisible()
  await expect(differences.getByRole("button", { name: /^Added payments/ })).toHaveCount(0)
  await page.getByRole("button", { name: /^All \d+$/ }).click()
  await expect(differences.getByRole("button", { name: /^Added payments/ })).toBeVisible()
})

test("the migration is steps that mark what destroys and what is left to the reader", async ({
  page,
}) => {
  await mockCompare(page)
  await page.goto(OPEN)
  await page.getByRole("button", { name: "Compare", exact: true }).click()

  const migration = page.getByRole("region", { name: "Migration" })
  await expect(migration.getByText("7 steps · 3 destructive · 1 for you to do")).toBeVisible()
  await expect(migration.getByText("This script removes things")).toBeVisible()
  await expect(migration.getByText("Some changes are left to you")).toBeVisible()

  await migration.getByRole("tab", { name: "Steps" }).click()
  const steps = migration.getByRole("list", { name: "Steps" })
  await expect(steps.getByRole("listitem")).toHaveCount(7)
  await expect(steps.getByRole("listitem").nth(3)).toContainText("Destructive")
  await expect(steps.getByRole("listitem").nth(3)).toContainText('DROP COLUMN "legacy_flag";')
  await expect(steps.getByRole("listitem").nth(4)).toContainText("For you")
  await expect(steps.getByRole("listitem").nth(4)).toContainText("Labels cannot be removed")
})

test("the script is opened in Query on this connection, or saved as a file", async ({ page }) => {
  await mockCompare(page)
  await page.goto(OPEN)
  await page.getByRole("button", { name: "Compare", exact: true }).click()

  const migration = page.getByRole("region", { name: "Migration" })
  await expect(migration.locator("[data-slot=code-view]")).toBeVisible()
  const download = page.waitForEvent("download")
  await migration.getByRole("button", { name: "Download" }).click()
  expect((await download).suggestedFilename()).toBe("shop-public-to-shop-staging-staging.sql")
  await expect(migration.getByRole("button", { name: "Copy" })).toBeVisible()

  await migration.getByRole("button", { name: "Open in Query" }).click()
  await expect(page).toHaveURL(/\/databases\/1\/query\?/)
  expect(new URL(page.url()).searchParams.get("sql")).toContain('DROP TABLE "public"."audit_old"')
})

test("a script too long for an address is copied or saved, and says so", async ({ page }) => {
  await mockCompare(page, {
    answer: {
      ...ANSWER,
      steps: [
        ...ANSWER.steps,
        {
          title: "A long one",
          statements: ["-- padding\n".repeat(900) + "SELECT 1"],
          destructive: false,
          manual: false,
          note: "",
        },
      ],
    },
  })
  await page.goto(OPEN)
  await page.getByRole("button", { name: "Compare", exact: true }).click()
  const migration = page.getByRole("region", { name: "Migration" })
  await expect(migration.getByRole("button", { name: "Open in Query" })).toBeDisabled()
  await expect(migration.getByText("too long to be handed to Query")).toBeVisible()
  await expect(migration.getByRole("button", { name: "Download" })).toBeEnabled()
})

test("schemas that match say so", async ({ page }) => {
  await mockCompare(page, { answer: { ...ANSWER, objects: [], steps: [] } })
  await page.goto(OPEN)
  await page.getByRole("button", { name: "Compare", exact: true }).click()
  await expect(page.getByText("The schemas match")).toBeVisible()
  await expect(page.getByRole("region", { name: "Migration" })).toHaveCount(0)
})

test("what the server could not read is said above the result", async ({ page }) => {
  await mockCompare(page, {
    answer: {
      ...ANSWER,
      notes: ["The functions of shop-staging were not compared: permission denied."],
    },
  })
  await page.goto(OPEN)
  await page.getByRole("button", { name: "Compare", exact: true }).click()
  await expect(page.getByText("Not everything was compared")).toBeVisible()
  await expect(page.getByText("permission denied")).toBeVisible()
})

test("a refusal is shown where the result would be, with a way to ask again", async ({ page }) => {
  const { sent } = await mockCompare(page, {
    fail: {
      error: {
        code: "bad_request",
        message: "shop-staging is CockroachDB, whose schema this does not compare",
      },
    },
    status: 400,
  })
  await page.goto(OPEN)
  await page.getByRole("button", { name: "Compare", exact: true }).click()
  await expect(page.getByText("whose schema this does not compare")).toBeVisible()
  await page.getByRole("button", { name: /try again|retry/i }).click()
  await expect.poll(() => sent.length).toBe(2)
})

test("the same schema twice is refused before anything is asked", async ({ page }) => {
  const { sent } = await mockCompare(page)
  await page.goto("/databases/1/compare?to=1&toSchema=public")
  await expect(page.getByText("Both sides are the same schema")).toBeVisible()
  await expect(page.getByRole("button", { name: "Compare", exact: true })).toBeDisabled()
  expect(sent).toEqual([])

  await page.goto("/databases/1/compare")
  await expect(page.getByText("Choose the connection to compare with.")).toBeVisible()
  await expect(page.getByRole("button", { name: "Compare", exact: true })).toBeDisabled()
})

test("swapping the sides opens the comparison from the other connection", async ({ page }) => {
  await mockCompare(page)
  await page.goto(OPEN)
  await page.getByRole("button", { name: "Swap sides" }).click()
  await expect(page).toHaveURL(/\/databases\/3\/compare\?/)
  const url = new URL(page.url())
  expect(url.searchParams.get("to")).toBe("1")
  expect(url.searchParams.get("schema")).toBe("staging")
  expect(url.searchParams.get("toSchema")).toBe("public")
})

test("a role that may only read compares, and a protected connection says it cannot be run on", async ({
  page,
}) => {
  await mockCompare(page, { viewer: true, rows: { 1: { readOnly: true } } })
  await page.goto(OPEN)
  await page.getByRole("button", { name: "Compare", exact: true }).click()
  const migration = page.getByRole("region", { name: "Migration" })
  await expect(migration.getByText("shop is protected")).toBeVisible()
  await expect(migration.getByRole("button", { name: "Open in Query" })).toBeEnabled()
})

test("an engine that cannot compare has no Compare page and asks nothing", async ({ page }) => {
  const { sent } = await mockCompare(page, {
    summaries: { 1: { capabilities: { schemaDiff: false } } },
  })
  await page.goto("/databases/1/compare")
  await expect(page.getByText("PostgreSQL has no schema comparison")).toBeVisible()
  await expect(
    page.getByRole("navigation", { name: "Sidebar" }).getByRole("link", { name: "Compare" }),
  ).toHaveCount(0)
  expect(sent).toEqual([])
})

test("the rail lists Compare in the Schema group for an engine that can", async ({ page }) => {
  await mockCompare(page)
  await page.goto("/databases/1")
  const link = page
    .getByRole("navigation", { name: "Sidebar" })
    .getByRole("link", { name: "Compare" })
  await expect(link).toHaveAttribute("href", "/databases/1/compare")
})

test("on a phone nothing scrolls sideways, with the differences open", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 })
  await mockCompare(page)
  await page.goto(OPEN)
  await page.getByRole("button", { name: "Compare", exact: true }).click()
  const differences = page.getByRole("region", { name: "Differences" })
  await differences.getByRole("button", { name: /^Changed users/ }).click()
  await differences.getByRole("button", { name: /^Changed v_users/ }).click()
  await expect(differences.locator("[data-side=to]")).toContainText("nickname")
  const sideways = await page.evaluate(() =>
    [
      document.documentElement,
      document.body,
      ...document.querySelectorAll("[data-slot=page]"),
    ].some((el) => el.scrollWidth > el.clientWidth + 1),
  )
  expect(sideways).toBe(false)
})

test("the pages, for the record @shots", async ({ page }) => {
  test.skip(!process.env.JD_SHOTS, "set JD_SHOTS to a directory to save the pages")
  const out = process.env.JD_SHOTS as string
  const settle = (selector: string) =>
    page
      .locator(selector)
      .first()
      .evaluate((el) => Promise.all(el.getAnimations({ subtree: true }).map((a) => a.finished)))

  await page.setViewportSize({ width: 1440, height: 900 })
  await mockCompare(page, { rows: { 1: { environment: "production" } } })
  await page.goto(OPEN)
  await expect(page.getByText("Nothing compared yet")).toBeVisible()
  await page.screenshot({ path: `${out}/chosen-1440.png` })

  await page.getByRole("button", { name: "Compare", exact: true }).click()
  await expect(page.getByText("6 differences: 2 added, 1 removed, 3 changed")).toBeVisible()
  const differences = page.getByRole("region", { name: "Differences" })
  await differences.getByRole("button", { name: /^Changed users/ }).click()
  await differences.getByRole("button", { name: /^Changed v_users/ }).click()
  await expect(differences.locator("[data-side=to]")).toContainText("nickname")
  await settle("[data-slot=page]")
  await page.screenshot({ path: `${out}/differences-1440.png` })

  await page.getByRole("region", { name: "Migration" }).scrollIntoViewIfNeeded()
  await expect(
    page.getByRole("region", { name: "Migration" }).locator("[data-slot=code-view]"),
  ).toBeVisible()
  await page.screenshot({ path: `${out}/migration-sql-1440.png` })

  await page.getByRole("region", { name: "Migration" }).getByRole("tab", { name: "Steps" }).click()
  await expect(page.getByRole("list", { name: "Steps" })).toBeVisible()
  await page.getByRole("list", { name: "Steps" }).scrollIntoViewIfNeeded()
  await page.screenshot({ path: `${out}/migration-steps-1440.png` })

  await page.getByRole("button", { name: /^Removed \d+$/ }).click()
  await page.getByRole("region", { name: "Differences" }).scrollIntoViewIfNeeded()
  await page.screenshot({ path: `${out}/removed-only-1440.png` })

  const matching = await page.context().newPage()
  await mockCompare(matching, { answer: { ...ANSWER, objects: [], steps: [] } })
  await matching.setViewportSize({ width: 1440, height: 900 })
  await matching.goto(OPEN)
  await matching.getByRole("button", { name: "Compare", exact: true }).click()
  await expect(matching.getByText("The schemas match")).toBeVisible()
  await matching.screenshot({ path: `${out}/match-1440.png` })

  const phone = await page.context().newPage()
  await mockCompare(phone)
  await phone.setViewportSize({ width: 390, height: 844 })
  await phone.goto(OPEN)
  await phone.getByRole("button", { name: "Compare", exact: true }).click()
  await expect(phone.getByRole("region", { name: "Differences" })).toBeVisible()
  await phone
    .getByRole("region", { name: "Differences" })
    .getByRole("button", { name: /^Changed users/ })
    .click()
  await phone.screenshot({ path: `${out}/differences-390.png` })
})
