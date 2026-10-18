import { expect as baseExpect, test, type Page, type Route } from "@playwright/test"
import { mockDatabases, type DatabaseMock } from "./database-fixture"

/**
 * Seed data on the Generate page (`/databases/<id>/generate`): rows of mock
 * data planned for the tables, previewed table by table, and written behind
 * the statements they are written with. The API is mocked in the browser, and
 * the mock plans from what each request carries, so what the page asks for is
 * what is asserted.
 */

const expect = baseExpect.configure({ timeout: 15_000 })
test.describe.configure({ timeout: 90_000 })

async function json(route: Route, body: unknown, status = 200) {
  await route.fulfill({ status, contentType: "application/json", body: JSON.stringify(body) })
}

const TARGETS = {
  targets: [
    {
      id: "prisma",
      label: "Prisma",
      filename: "schema.prisma",
      description: "A schema.prisma to drop into an existing Prisma project.",
      language: "prisma",
      group: "ORM",
      engines: ["postgres", "mysql", "sqlite", "sqlserver"],
      unsupported: {},
      options: [],
    },
  ],
}

type MockColumn = {
  name: string
  type: string
  generator: string
  label: string
  choices?: { id: string; label: string; group: string }[]
  later?: string
  unique?: boolean
  reference?: { schema: string; table: string; columns: string[]; seeded: boolean }
  value?: (n: number, seed: number) => string | null
}

const PERSON = [
  { id: "email", label: "Email address", group: "Person" },
  { id: "fullName", label: "Full name", group: "Person" },
  { id: "word", label: "Word", group: "Text" },
  { id: "text", label: "Text", group: "Text" },
]

/** The tables the mock server knows, in the order it writes them. */
const SCHEMA: Record<
  string,
  { after: string[]; existing: number; problem?: string; columns: MockColumn[] }
> = {
  customers: {
    after: [],
    existing: 3,
    columns: [
      {
        name: "id",
        type: "integer",
        generator: "auto",
        label: "Numbered by the server",
        later: "numbered by the server",
      },
      {
        name: "email",
        type: "character varying(120)",
        generator: "email",
        label: "Email address",
        unique: true,
        choices: PERSON,
        value: (n, seed) => `ann.${seed % 100}.${n + 4}@example.com`,
      },
      {
        name: "tier",
        type: "text",
        generator: "choice",
        label: "One of its values",
        choices: [
          { id: "choice", label: "One of its values", group: "Other" },
          { id: "null", label: "Left empty", group: "Server" },
        ],
        value: (n) => (n % 3 === 2 ? null : ["free", "pro"][n % 2]),
      },
    ],
  },
  orders: {
    after: ["customers"],
    existing: 0,
    columns: [
      {
        name: "id",
        type: "integer",
        generator: "auto",
        label: "Numbered by the server",
        later: "numbered by the server",
      },
      {
        name: "customer_id",
        type: "integer",
        generator: "reference",
        label: "A row of customers",
        later: "a new customers row",
        reference: { schema: "public", table: "customers", columns: ["id"], seeded: true },
      },
      {
        name: "status",
        type: "text",
        generator: "word",
        label: "Word",
        choices: PERSON,
        value: (n) => `status ${n}`,
      },
      {
        name: "total_cents",
        type: "integer",
        generator: "integer",
        label: "Whole number",
        value: (n) => String(100 + n),
      },
    ],
  },
  "Mixed Case Table": {
    after: [],
    existing: 0,
    problem:
      "select is point, which no generator writes; it has no default and cannot be left empty",
    columns: [{ name: "select", type: "point", generator: "", label: "" }],
  },
}

const ORDER = ["customers", "orders", "Mixed Case Table"]

const OUTLINE = [
  { name: "customers", type: "table" },
  { name: "orders", type: "table" },
  { name: "Mixed Case Table", type: "table" },
  { name: "order_summary", type: "view" },
]

type SeedBody = {
  seed: number
  tables: { schema: string; name: string; rows: number; columns?: Record<string, string> }[]
}

/** What the mock server plans for a request: the shape the real one answers with. */
/** Columns the mock server's tables have lost since the page was opened. */
const dropped = new Set<string>()

function planFor(body: SeedBody) {
  const asked = new Map(body.tables.map((t) => [t.name, t]))
  const warnings: string[] = []
  const tables = ORDER.filter((name) => asked.has(name)).map((name) => {
    const want = asked.get(name)!
    const table = SCHEMA[name]
    for (const column of Object.keys(want.columns ?? {})) {
      if (dropped.has(`${name}.${column}`)) {
        warnings.push(`${name} has no column ${column} now; what was chosen for it is set aside`)
      }
    }
    const columns = table.columns
      .filter((c) => !dropped.has(`${name}.${c.name}`))
      .map((c) => {
        const generator = want.columns?.[c.name] ?? c.generator
        const choice = c.choices?.find((x) => x.id === generator)
        return {
          name: c.name,
          type: c.type,
          generator,
          inferred: c.generator,
          label: choice?.label ?? c.label,
          choices: c.choices ?? [],
          nullable: c.name === "tier",
          unique: c.unique ?? false,
          reference: c.reference,
          later: c.later,
        }
      })
    const rows = table.problem ? 0 : want.rows
    return {
      schema: "public",
      name,
      rows,
      asked: want.rows,
      existing: table.existing,
      after: table.after.filter((parent) => asked.has(parent)),
      columns,
      preview: table.problem
        ? []
        : Array.from({ length: Math.min(rows, 8) }, (_, n) =>
            columns.map((column) => {
              const c = table.columns.find((x) => x.name === column.name)!
              return column.generator === "null" ? null : (c.value?.(n, body.seed) ?? null)
            }),
          ),
      statement: table.problem
        ? ""
        : `INSERT INTO "public"."${name}" (${columns
            .filter((c) => c.generator !== "auto")
            .map((c) => `"${c.name}"`)
            .join(", ")}) VALUES ($1)`,
      problem: table.problem,
      notes: [],
    }
  })
  return {
    seed: body.seed,
    tables,
    rows: tables.reduce((sum, t) => sum + t.rows, 0),
    atomic: true,
    warnings,
  }
}

async function mockSeed(page: Page, options: DatabaseMock & { capabilities?: string[] } = {}) {
  await mockDatabases(page, options)
  dropped.clear()
  const sent = { plans: [] as SeedBody[], writes: [] as SeedBody[] }
  if (options.capabilities) {
    await page.route("**/api/v1/auth/session", (route) =>
      json(route, {
        authenticated: true,
        needsTotp: false,
        needsEnrollment: false,
        require2fa: false,
        capabilities: options.capabilities,
        user: { id: 1, username: "operator", displayName: "Operator", role: "limited" },
      }),
    )
  }
  await page.route("**/api/v1/databases/orm/targets", (route) => json(route, TARGETS))
  await page.route(/\/api\/v1\/databases\/1\/.+/, async (route) => {
    const request = route.request()
    const url = new URL(request.url())
    const rest = url.pathname.replace(/^\/api\/v1\/databases\/1/, "")
    if (rest === "/catalog") {
      return json(route, {
        schema: "public",
        defaultSchema: "public",
        schemas: [{ name: "public", default: true, tables: 3 }],
        objects: {},
        truncated: [],
        limit: 5000,
      })
    }
    if (rest === "/outline") {
      const entries = OUTLINE.map((e) => ({ id: `public.${e.name}`, schema: "public", ...e }))
      return json(route, {
        schema: "public",
        tables: {},
        entries,
        truncated: false,
        total: entries.length,
        limit: 5000,
      })
    }
    if (rest === "/seed/plan") {
      const body = request.postDataJSON() as SeedBody
      sent.plans.push(body)
      return json(route, planFor(body))
    }
    if (rest === "/seed") {
      const body = request.postDataJSON() as SeedBody
      sent.writes.push(body)
      const plan = planFor(body)
      return json(route, {
        seed: body.seed,
        atomic: true,
        warnings: [],
        inserted: plan.rows - 1,
        skipped: 1,
        duration: "40ms",
        tables: plan.tables.map((t) => ({
          schema: t.schema,
          name: t.name,
          rows: t.rows,
          inserted: t.name === "orders" ? t.rows - 1 : t.rows,
          skipped: t.name === "orders" ? 1 : 0,
          errors:
            t.name === "orders" ? [{ row: 4, line: 4, message: "violates check constraint" }] : [],
          problem: t.problem,
          notes: [],
          statement: t.statement,
        })),
      })
    }
    return route.fallback()
  })
  return sent
}

const seedCard = (page: Page) => page.locator("[data-target=seed]")

test("Seed data plans every table when it is chosen, parents first, and shows each one's rows", async ({
  page,
}) => {
  const sent = await mockSeed(page)
  await page.goto("/databases/1/generate")

  // A card of its own, beside the code generators, and nothing asked until it is chosen.
  await expect(page.getByRole("region", { name: "Data" }).getByRole("button")).toHaveCount(1)
  await page.waitForTimeout(600)
  expect(sent.plans).toHaveLength(0)
  await seedCard(page).click()
  await expect.poll(() => sent.plans.length).toBe(1)
  const first = sent.plans[0]
  expect(first.seed).toBeGreaterThan(0)
  // Every table, and no view: a view takes no rows of its own.
  expect(first.tables).toEqual([
    { schema: "public", name: "customers", rows: 25 },
    { schema: "public", name: "orders", rows: 25 },
    { schema: "public", name: "Mixed Case Table", rows: 25 },
  ])

  // A tab for each table, in the order they are written; one left out says so.
  const tabs = page.getByRole("tablist", { name: "Tables seeded" }).getByRole("tab")
  await expect(tabs).toHaveText(["customers25", "orders25", "Mixed Case Table, left out"])
  await expect(page.getByText("2 tables · 50 rows · 1 table left out")).toBeVisible()
  const grid = page.getByRole("table", { name: "The first rows of customers" })
  await expect(grid.getByRole("row")).toHaveCount(9)
  await expect(grid).toContainText(`ann.${first.seed % 100}.4@example.com`)
  // What the server decides stands as what decides it; a NULL the seed writes is a NULL.
  await expect(grid.getByText("numbered by the server").first()).toBeVisible()
  await expect(grid.getByText("NULL").first()).toBeVisible()
  await expect(page.getByText("3 rows there now")).toBeVisible()
  await expect(page.getByText("Points at no other table being filled")).toBeVisible()

  await tabs.nth(1).click()
  await expect(page.getByText("Written after customers")).toBeVisible()
  await expect(
    page
      .getByRole("table", { name: "The first rows of orders" })
      .getByText("a new customers row")
      .first(),
  ).toBeVisible()
  await expect(page.getByText('INSERT INTO "public"."orders"')).toBeVisible()
  // The tabs are a tablist the keyboard walks.
  await tabs.nth(1).press("ArrowRight")
  await expect(tabs.nth(2)).toBeFocused()
  await expect(page.getByText("Left out of this seed")).toBeVisible()
  await expect(page.getByText("select is point, which no generator writes")).toBeVisible()
})

test("rows, tables and a column's generator are asked for again by themselves", async ({
  page,
}) => {
  const sent = await mockSeed(page)
  await page.goto("/databases/1/generate")
  await seedCard(page).click()
  await expect.poll(() => sent.plans.length).toBe(1)

  await page.getByLabel("Rows per table").fill("40")
  await expect.poll(() => sent.plans.at(-1)?.tables.map((t) => t.rows)).toEqual([40, 40, 40])
  // A number the server would refuse is said, and not sent.
  const before = sent.plans.length
  await page.getByLabel("Rows per table").fill("0")
  await expect(page.getByText("Rows: a whole number from 1 to 10,000.")).toBeVisible()
  await page.waitForTimeout(900)
  expect(sent.plans).toHaveLength(before)
  await page.getByLabel("Rows per table").fill("40")

  // One table's own count.
  await page.getByLabel("Rows for customers").fill("7")
  await expect
    .poll(() => sent.plans.at(-1)?.tables.map((t) => [t.name, t.rows]))
    .toEqual([
      ["customers", 7],
      ["orders", 40],
      ["Mixed Case Table", 40],
    ])

  // A column's generator, chosen from its head; the one it opened with again is no choice.
  await page.getByRole("button", { name: "email is filled with Email address" }).click()
  await page.getByRole("menuitemradio", { name: "Full name" }).click()
  await expect.poll(() => sent.plans.at(-1)?.tables[0].columns).toEqual({ email: "fullName" })
  await page.getByRole("button", { name: "email is filled with Full name" }).click()
  await page.getByRole("menuitemradio", { name: "Email address" }).click()
  await expect.poll(() => sent.plans.at(-1)?.tables[0].columns).toBeUndefined()

  // The tables filled can be chosen.
  await page.getByRole("button", { name: /Every table/ }).click()
  await page
    .getByRole("list", { name: "The tables filled" })
    .getByText("orders", { exact: true })
    .click()
  await page
    .getByRole("list", { name: "The tables filled" })
    .getByText("Mixed Case Table", { exact: true })
    .click()
  await page.keyboard.press("Escape")
  await expect.poll(() => sent.plans.at(-1)?.tables.map((t) => t.name)).toEqual(["customers"])

  // A table's own count the server would refuse holds the plan, and the write.
  await page.getByLabel("Rows for customers").fill("0")
  await expect(page.getByText("Rows for customers: a whole number from 1 to 10,000.")).toBeVisible()
  await expect(page.getByRole("button", { name: /^Write \d+ rows?$/ })).toBeDisabled()
  await page.getByLabel("Rows for customers").fill("7")
  await expect(page.getByRole("button", { name: /^Write \d+ rows?$/ })).toBeEnabled()

  // New values: the same tables with another seed.
  const seed = sent.plans.at(-1)!.seed
  await page.getByRole("button", { name: "New values" }).click()
  await expect.poll(() => sent.plans.at(-1)?.seed).not.toBe(seed)
})

test("a choice for a column the table has since lost is said once and not sent again", async ({
  page,
}) => {
  const sent = await mockSeed(page)
  await page.goto("/databases/1/generate")
  await seedCard(page).click()
  await page.getByRole("button", { name: "email is filled with Email address" }).click()
  await page.getByRole("menuitemradio", { name: "Full name" }).click()
  await expect.poll(() => sent.plans.at(-1)?.tables[0].columns).toEqual({ email: "fullName" })

  // The column goes; the next plan carries the choice once, is answered with
  // a warning and not a refusal, and the one after carries it no more.
  dropped.add("customers.email")
  await page.getByLabel("Rows per table").fill("12")
  await expect(page.getByText("customers has no column email now")).toBeVisible()
  await page.getByLabel("Rows per table").fill("13")
  await expect.poll(() => sent.plans.at(-1)?.tables.map((t) => t.rows)).toEqual([13, 13, 13])
  expect(sent.plans.at(-1)?.tables[0].columns).toBeUndefined()
})

test("writing shows the statements, sends the plan on screen, and says what went in", async ({
  page,
}) => {
  const sent = await mockSeed(page)
  await page.goto("/databases/1/generate")
  await seedCard(page).click()
  await page.getByLabel("Rows per table").fill("10")
  await expect(page.getByRole("button", { name: "Write 20 rows" })).toBeEnabled()
  await page.getByRole("button", { name: "Write 20 rows" }).click()

  const review = page.getByRole("dialog", { name: "Write 20 rows into 2 tables" })
  await expect(review.getByRole("list", { name: "What is written" })).toContainText("customers")
  await expect(review.getByText('INSERT INTO "public"."customers"')).toBeVisible()
  await expect(
    review.getByText("One transaction: if any of it fails, nothing is written."),
  ).toBeVisible()
  const planned = sent.plans.at(-1)!
  await review.getByRole("button", { name: "Write 20 rows" }).click()
  await expect.poll(() => sent.writes.length).toBe(1)
  expect(sent.writes[0]).toEqual(planned)

  await expect(review).toBeHidden()
  const written = page.getByRole("list", { name: "What was written" })
  await expect(page.getByText("Wrote 19 rows into 2 tables")).toBeVisible()
  await expect(written).toContainText("9 written · 1 refused")
  await expect(written).toContainText("Row 4: violates check constraint")
  await expect(written.getByRole("link", { name: "customers" })).toHaveAttribute(
    "href",
    /\/databases\/1\/data\?.*table=customers/,
  )
  // The next rows are new ones: the seed number moved on and the plan was read again.
  await expect.poll(() => sent.plans.at(-1)?.seed).not.toBe(planned.seed)
  await page.getByRole("button", { name: "Dismiss" }).click()
  await expect(written).toBeHidden()
})

test("a protected connection and a read-only role are shown the rows and cannot write them", async ({
  page,
}) => {
  await mockSeed(page, { rows: { 1: { readOnly: true } } })
  await page.goto("/databases/1/generate")
  await seedCard(page).click()
  const command = page.getByRole("button", { name: "Write 50 rows" })
  await expect(command).toBeDisabled()
  await expect(command).toHaveAttribute("title", /shop is protected/)

  const page2 = await page.context().newPage()
  await mockSeed(page2, { capabilities: ["read"] })
  await page2.goto("/databases/1/generate")
  await seedCard(page2).click()
  const viewer = page2.getByRole("button", { name: "Write 50 rows" })
  await expect(viewer).toBeDisabled()
  await expect(viewer).toHaveAttribute("title", "Writing rows needs a role that may change data.")
})

test("an engine without seed data has no card for it", async ({ page }) => {
  await mockSeed(page, { summaries: { 1: { capabilities: { seed: false } } } })
  await page.goto("/databases/1/generate")
  await expect(page.locator("[data-target=prisma]")).toBeVisible()
  await expect(seedCard(page)).toHaveCount(0)
})

for (const [label, viewport] of [
  ["", { width: 1280, height: 800 }],
  [" at a phone's width", { width: 390, height: 844 }],
] as const) {
  test(`Seed data stays a reading page and nothing scrolls sideways${label}`, async ({ page }) => {
    await page.setViewportSize(viewport)
    await mockSeed(page)
    await page.goto("/databases/1/generate")
    if (viewport.width < 1280)
      await page.getByRole("button", { name: /^All \d+ generators$/ }).click()
    await seedCard(page).click()
    await expect(page.getByRole("table", { name: "The first rows of customers" })).toBeVisible()
    const seen = await page.evaluate(() => ({
      registers: [...document.querySelectorAll<HTMLElement>("[data-slot='page']")].map(
        (el) => el.dataset.register ?? "(unset)",
      ),
      sideways: [document.documentElement, ...document.querySelectorAll("[data-slot=page]")]
        .map((el) => el.parentElement ?? el)
        .some((el) => el.scrollWidth > el.clientWidth + 1),
    }))
    expect(seen.registers).toEqual(["reading"])
    expect(seen.sideways).toBe(false)
  })
}
