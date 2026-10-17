import { expect as baseExpect, test, type Page, type Route } from "@playwright/test"
import { mockDatabases, type DatabaseMock } from "./database-fixture"

/**
 * The plan on a database's Query page (`/databases/<id>/query`, Plan): which
 * steps took the time, a step's own time beside its subtree's, the plan read
 * slowest first, and a step whose rows came out ten times more or fewer than
 * the planner expected saying so under its name — only where the statement
 * was measured. A plan an engine only prints (Oracle here) is drawn as the
 * same tree. The API is mocked in the browser.
 */

const expect = baseExpect.configure({ timeout: 15_000 })
test.describe.configure({ timeout: 90_000 })

async function json(route: Route, body: unknown, status = 200) {
  await route.fulfill({ status, contentType: "application/json", body: JSON.stringify(body) })
}

const SQL =
  "select o.id, c.email from orders o join customers c on c.id = o.customer_id where o.status = 'paid' and o.region = 'eu' order by o.created_at desc limit 50"

type Step = Record<string, unknown> & { Plans?: Step[] }

/** PostgreSQL's plan of SQL, measured: the two conditions are one, and the planner multiplied them. */
const MEASURED: Step = {
  "Node Type": "Limit",
  "Total Cost": 1210.4,
  "Plan Rows": 50,
  "Actual Rows": 50,
  "Actual Total Time": 58.4,
  "Actual Loops": 1,
  Plans: [
    {
      "Node Type": "Sort",
      "Total Cost": 1210.3,
      "Plan Rows": 12,
      "Actual Rows": 50,
      "Actual Total Time": 58.1,
      "Actual Loops": 1,
      "Sort Key": ["o.created_at DESC"],
      "Sort Method": "top-N heapsort",
      Plans: [
        {
          "Node Type": "Hash Join",
          "Join Type": "Inner",
          "Total Cost": 1210.2,
          "Plan Rows": 12,
          "Actual Rows": 48_210,
          "Actual Total Time": 49.6,
          "Actual Loops": 1,
          "Hash Cond": "(o.customer_id = c.id)",
          Plans: [
            {
              "Node Type": "Seq Scan",
              "Relation Name": "orders",
              Alias: "o",
              "Total Cost": 980,
              "Plan Rows": 12,
              "Actual Rows": 48_210,
              "Actual Total Time": 31,
              "Actual Loops": 1,
              Filter: "((status = 'paid'::text) AND (region = 'eu'::text))",
              "Rows Removed by Filter": 151_790,
              "Shared Hit Blocks": 2_940,
            },
            {
              "Node Type": "Hash",
              "Total Cost": 88,
              "Plan Rows": 2_400,
              "Actual Rows": 2_400,
              "Actual Total Time": 2.1,
              "Actual Loops": 1,
              Plans: [
                {
                  "Node Type": "Seq Scan",
                  "Relation Name": "customers",
                  Alias: "c",
                  "Total Cost": 88,
                  "Plan Rows": 2_400,
                  "Actual Rows": 2_400,
                  "Actual Total Time": 1.2,
                  "Actual Loops": 1,
                },
              ],
            },
          ],
        },
      ],
    },
  ],
}

/** The same plan as it is planned, before anything runs: none of what running it measured. */
function planned(step: Step): Step {
  const { Plans, ...rest } = step
  const kept: Step = Object.fromEntries(
    Object.entries(rest).filter(([key]) => !/^(Actual|Rows Removed|Shared|Sort Method)/.test(key)),
  )
  return Plans ? { ...kept, Plans: Plans.map(planned) } : kept
}

/** Oracle's DBMS_XPLAN print of SQL. */
const ORACLE_PLAN = [
  "Plan hash value: 2851340561",
  " ",
  "---------------------------------------------------------------------------------",
  "| Id  | Operation           | Name      | Rows  | Bytes | Cost (%CPU)| Time     |",
  "---------------------------------------------------------------------------------",
  "|   0 | SELECT STATEMENT    |           |    48 |  3120 |   412   (1)| 00:00:01 |",
  "|   1 |  SORT ORDER BY      |           |    48 |  3120 |   412   (1)| 00:00:01 |",
  "|*  2 |   HASH JOIN         |           |    48 |  3120 |   411   (1)| 00:00:01 |",
  "|*  3 |    TABLE ACCESS FULL| ORDERS    |    48 |  1920 |   398   (1)| 00:00:01 |",
  "|   4 |    TABLE ACCESS FULL| CUSTOMERS |  2400 | 60000 |    13   (0)| 00:00:01 |",
  "---------------------------------------------------------------------------------",
  " ",
  "Predicate Information (identified by operation id):",
  "---------------------------------------------------",
  " ",
  '   2 - access("O"."CUSTOMER_ID"="C"."ID")',
  '   3 - filter("O"."STATUS"=\'paid\' AND "O"."REGION"=\'eu\')',
]

/** Connection 2 stands for an Oracle server here. */
const ORACLE: DatabaseMock = {
  rows: { 2: { name: "ledger", driver: "oracle", port: "1521", database: "FREEPDB1" } },
  summaries: {
    2: {
      flavor: "oracle",
      flavorLabel: "Oracle",
      versionNumber: "23.5",
      version: "Oracle Database 23.5",
    },
  },
}

const READ = { destructive: false, level: "read", reasons: [] as string[] }

/** The Query page's routes for connections 1 and 2. Hands back what the page asked to explain. */
async function mockPlans(page: Page, options: DatabaseMock & { explainFails?: string } = {}) {
  const shell = await mockDatabases(page, options)
  const explained: Record<string, unknown>[] = []
  await page.route(/\/api\/v1\/databases\/(1|2)\/.+/, async (route) => {
    const request = route.request()
    const url = new URL(request.url())
    const [, id, rest] = /^\/api\/v1\/databases\/(\d+)(\/.*)$/.exec(url.pathname) ?? []
    const body = () => (request.postDataJSON() ?? {}) as Record<string, unknown>
    const schema = id === "1" ? "public" : "APP"
    if (rest === "/catalog") {
      return json(route, {
        schema,
        defaultSchema: schema,
        schemas: [{ name: schema, default: true, tables: 2 }],
        objects: {},
        truncated: [],
        limit: 5000,
      })
    }
    if (rest === "/outline") {
      const tables = {
        orders: ["id", "customer_id", "status", "region"],
        customers: ["id", "email"],
      }
      return json(route, {
        schema,
        tables: Object.fromEntries(Object.entries(tables).map(([t, c]) => [`${schema}.${t}`, c])),
        entries: Object.keys(tables).map((name) => ({
          id: `${schema}.${name}`,
          schema,
          name,
          type: "table",
        })),
        truncated: false,
        total: 2,
        limit: 5000,
      })
    }
    if (rest === "/history" || rest === "/queries") return json(route, [])
    if (rest === "/classify") {
      const sql = String(body().query ?? "")
      return json(route, { ...READ, statements: [{ sql, line: 1, risk: READ }] })
    }
    if (rest === "/explain") {
      const asked = body()
      explained.push(asked)
      if (options.explainFails) {
        return json(route, { error: { code: "bad_request", message: options.explainFails } }, 400)
      }
      const result = (rows: string[][], column: string) => ({
        columns: [column],
        types: ["TEXT"],
        kinds: ["text"],
        rows,
        rowCount: rows.length,
        rowsAffected: 0,
        duration: "2ms",
        truncated: false,
        statement: String(asked.query),
      })
      if (id === "2") {
        return json(route, {
          result: result(
            ORACLE_PLAN.map((line) => [line]),
            "PLAN_TABLE_OUTPUT",
          ),
          format: "text",
          analyzed: false,
          rolledBack: false,
        })
      }
      const plan = asked.analyze
        ? [{ Plan: MEASURED, "Planning Time": 0.6, "Execution Time": 58.9 }]
        : [{ Plan: planned(MEASURED), "Planning Time": 0.5 }]
      return json(route, {
        result: result([[JSON.stringify(plan)]], "QUERY PLAN"),
        plan,
        format: "json",
        analyzed: Boolean(asked.analyze),
        rolledBack: false,
      })
    }
    return route.fallback()
  })
  return { ...shell, explained }
}

const editor = (page: Page) => page.locator("[data-slot=sql-editor] .monaco-editor").first()
const commands = (page: Page) => page.locator("[data-slot=query-commands]")
const results = (page: Page) => page.locator("[data-slot=query-results]")
const tree = (page: Page) => results(page).locator("[data-slot=plan-tree]")
const steps = (page: Page) =>
  tree(page)
    .locator("[data-plan-step]")
    .evaluateAll((items) => items.map((item) => item.getAttribute("data-plan-step")))

/** Opens a connection's editor, waits for Monaco and sets the statement through it. */
async function openWith(page: Page, sql: string, path = "/databases/1/query") {
  await page.goto(path)
  // Monaco is loaded on demand: on a busy runner its first paint is the slow part.
  await expect(editor(page)).toBeVisible({ timeout: 45_000 })
  await editor(page).click()
  await page.evaluate((text) => {
    type Held = {
      getDomNode(): HTMLElement | null
      setValue(value: string): void
      getModel(): { getFullModelRange(): { getEndPosition(): unknown } }
      setPosition(position: unknown): void
      focus(): void
    }
    const monaco = (window as unknown as { monaco: { editor: { getEditors(): Held[] } } }).monaco
    const held = monaco.editor
      .getEditors()
      .find((candidate) => candidate.getDomNode()?.closest("[data-slot=sql-editor]"))
    if (!held) throw new Error("no editor on the page")
    held.setValue(text)
    held.setPosition(held.getModel().getFullModelRange().getEndPosition())
    held.focus()
  }, sql)
}

async function measure(page: Page) {
  await commands(page).getByRole("button", { name: "More ways to explain" }).click()
  await page.getByRole("menuitem", { name: /Explain and measure/ }).click()
  await expect(results(page)).toContainText("measured")
  await expect(tree(page)).toBeVisible()
}

/** Neither the page nor the plan in it scrolls sideways. */
function sideways(page: Page) {
  return page.evaluate(() =>
    [
      document.documentElement,
      ...document.querySelectorAll("[data-slot=page]"),
      ...document.querySelectorAll("[data-slot=plan-tree]"),
    ]
      .map((el) => (el.matches("[data-slot=plan-tree]") ? el : (el.parentElement ?? el)))
      .some((el) => el.scrollWidth > el.clientWidth + 1),
  )
}

test("a measured plan says where the time went and which estimates were far off", async ({
  page,
}) => {
  const { explained } = await mockPlans(page)
  await openWith(page, SQL)
  await measure(page)
  expect(explained[0]).toMatchObject({ analyze: true, format: "json" })

  // The two steps the planner thought would hand on 12 rows, and that handed on 48,210.
  const far = tree(page).locator("[data-slot=plan-estimate]")
  await expect(far).toHaveCount(2)
  await expect(tree(page).locator('[data-plan-step="0.0.0"]')).toContainText(
    "est 12 · actual 48,210 (×4,000)",
  )
  const orders = tree(page).locator('[data-plan-step="0.0.0.0"]')
  await expect(orders).toContainText("est 12 · actual 48,210 (×4,000)")
  // The sort under the limit is off by four, which is not far.
  await expect(tree(page).locator('[data-plan-step="0.0"]')).not.toContainText("est ")

  // A step's figure is the time it took itself; the limit over everything took next to none.
  await expect(orders).toContainText("31 ms")
  await expect(tree(page).locator('[data-plan-step="0"]')).toContainText("0.3 ms")

  // Slowest first, out of the tree; and back.
  await expect
    .poll(() => steps(page))
    .toEqual(["0", "0.0", "0.0.0", "0.0.0.0", "0.0.0.1", "0.0.0.1.0"])
  await results(page).getByRole("radio", { name: "Slowest first" }).click()
  await expect
    .poll(() => steps(page))
    .toEqual(["0.0.0.0", "0.0.0", "0.0", "0.0.0.1.0", "0.0.0.1", "0"])
  await results(page).getByRole("radio", { name: "Plan order" }).click()
  await expect
    .poll(() => steps(page))
    .toEqual(["0", "0.0", "0.0.0", "0.0.0.0", "0.0.0.1", "0.0.0.1.0"])

  // A step opens on its condition, then its time in words beside what is under it.
  await tree(page)
    .getByRole("button", { name: /Hash Join/ })
    .click()
  const join = tree(page).locator('[data-plan-step="0.0.0"]')
  await expect(join).toContainText("Hash Cond: (o.customer_id = c.id)")
  await expect(join).toContainText("Time in this step17 ms · 28%")
  await expect(join).toContainText("With the steps under it50 ms · 85%")
  await expect(join).toContainText("Rows expected12")
  // Opened, it stays open in the other order.
  await results(page).getByRole("radio", { name: "Slowest first" }).click()
  await expect(join).toContainText("Hash Cond: (o.customer_id = c.id)")
  await tree(page)
    .getByRole("button", { name: /Hash Join/ })
    .click()
  await expect(join).not.toContainText("Hash Cond")
})

test("a plan that was only planned has no time, no order and nothing far off", async ({ page }) => {
  const { explained } = await mockPlans(page)
  await openWith(page, SQL)
  await commands(page).getByRole("button", { name: "Explain", exact: true }).click()
  await expect(tree(page)).toBeVisible()
  expect(explained[0]).toEqual({ query: SQL, format: "json" })

  await expect(results(page)).toContainText("estimated")
  await expect(tree(page).locator("[data-plan-step]")).toHaveCount(6)
  await expect(tree(page).locator("[data-slot=plan-estimate]")).toHaveCount(0)
  await expect(results(page).getByRole("radio", { name: "Slowest first" })).toHaveCount(0)
  const head = tree(page).locator("div.sticky")
  await expect(head).toContainText("Cost")
  await expect(head).toContainText("Rows")
  await expect(head).not.toContainText("Time")
  // The rows are the planner's.
  await expect(tree(page).locator('[data-plan-step="0.0.0.0"]')).toContainText("12")
})

test("a plan Oracle prints is drawn as the tree of its steps", async ({ page }) => {
  const { explained } = await mockPlans(page, ORACLE)
  await openWith(page, SQL, "/databases/2/query")
  // Oracle has neither a JSON plan nor a measured one.
  await expect(commands(page).getByRole("button", { name: "More ways to explain" })).toHaveCount(0)
  await commands(page).getByRole("button", { name: "Explain", exact: true }).click()
  await expect(tree(page)).toBeVisible()
  expect(explained[0]).toEqual({ query: SQL, format: "text" })

  await expect(tree(page).locator("[data-plan-step]")).toHaveCount(5)
  await expect(tree(page).locator('[data-plan-step="0.0.0.0"]')).toContainText("TABLE ACCESS FULL")
  await expect(tree(page).locator('[data-plan-step="0.0.0.0"]')).toContainText("ORDERS")
  await expect(tree(page).locator('[data-plan-step="0.0.0.1"]')).toContainText("2,400")
  await expect(tree(page).locator("div.sticky")).not.toContainText("Time")
  await tree(page)
    .getByRole("button", { name: /HASH JOIN/ })
    .click()
  await expect(tree(page).locator('[data-plan-step="0.0.0"]')).toContainText(
    'access("O"."CUSTOMER_ID"="C"."ID")',
  )
  // The print itself is one press away.
  await results(page).getByRole("radio", { name: "Text" }).click()
  await expect(results(page)).toContainText("Plan hash value: 2851340561")
})

test("before a plan the pane says how to ask for one, and a refusal is said in the engine's words", async ({
  page,
}) => {
  await mockPlans(page, { explainFails: 'ERROR: relation "orderz" does not exist' })
  await openWith(page, SQL)
  await results(page).getByRole("tab", { name: "Plan" }).click()
  await expect(results(page)).toContainText("No plan yet")
  await expect(results(page).getByRole("button", { name: "Explain and measure" })).toBeVisible()

  await results(page).getByRole("button", { name: "Explain", exact: true }).click()
  const alert = results(page).getByRole("alert")
  await expect(alert).toContainText("The engine would not explain this")
  await expect(alert).toContainText('relation "orderz" does not exist')
  await expect(tree(page)).toHaveCount(0)
})

test("a role that only reads explains a plan and cannot measure one", async ({ page }) => {
  const { explained } = await mockPlans(page, { viewer: true })
  await openWith(page, SQL)
  await expect(commands(page).getByRole("button", { name: "More ways to explain" })).toHaveCount(0)
  await results(page).getByRole("tab", { name: "Plan" }).click()
  await expect(results(page)).toContainText("No plan yet")
  await expect(results(page).getByRole("button", { name: "Explain and measure" })).toHaveCount(0)

  await commands(page).getByRole("button", { name: "Explain", exact: true }).click()
  await expect(tree(page).locator("[data-plan-step]")).toHaveCount(6)
  expect(explained).toEqual([{ query: SQL, format: "json" }])
  await expect(results(page).getByRole("radio", { name: "Slowest first" })).toHaveCount(0)
})

test("at a phone's width a measured plan keeps its figures and does not scroll sideways", async ({
  page,
}) => {
  await page.setViewportSize({ width: 390, height: 844 })
  await mockPlans(page)
  await openWith(page, SQL)
  await measure(page)
  await expect(tree(page).locator("[data-slot=plan-estimate]").first()).toBeVisible()
  await expect(tree(page).locator('[data-plan-step="0.0.0.0"]')).toContainText("31 ms")
  expect(await sideways(page)).toBe(false)

  await results(page).getByRole("radio", { name: "Slowest first" }).click()
  await expect.poll(async () => (await steps(page))[0]).toBe("0.0.0.0")
  await tree(page)
    .getByRole("button", { name: /Seq Scan\s*orders o/ })
    .click()
  await expect(tree(page).locator('[data-plan-step="0.0.0.0"]')).toContainText("Time in this step")
  expect(await sideways(page)).toBe(false)
})

test("the plan's states, for the pull request @shots", async ({ page }) => {
  test.skip(!process.env.JD_SHOTS, "set JD_SHOTS to a directory to take the screenshots")
  const shot = (name: string) => page.screenshot({ path: `${process.env.JD_SHOTS}/${name}.png` })
  await page.setViewportSize({ width: 1440, height: 900 })
  await mockPlans(page, ORACLE)
  await openWith(page, SQL)
  await measure(page)
  await shot("measured-1440")
  await results(page).getByRole("radio", { name: "Slowest first" }).click()
  await shot("slowest-first-1440")
  await results(page).getByRole("radio", { name: "Plan order" }).click()
  await tree(page)
    .getByRole("button", { name: /Hash Join/ })
    .click()
  await expect(tree(page)).toContainText("Time in this step")
  await shot("step-open-1440")
  await commands(page).getByRole("button", { name: "Explain", exact: true }).click()
  await expect(results(page)).toContainText("estimated")
  await shot("estimated-1440")

  await page.setViewportSize({ width: 390, height: 844 })
  await openWith(page, SQL)
  await measure(page)
  await shot("measured-390")

  await page.setViewportSize({ width: 1440, height: 900 })
  await openWith(page, SQL, "/databases/2/query")
  await commands(page).getByRole("button", { name: "Explain", exact: true }).click()
  await expect(tree(page)).toBeVisible()
  await shot("oracle-1440")
})
