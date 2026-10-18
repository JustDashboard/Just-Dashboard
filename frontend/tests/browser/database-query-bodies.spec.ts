import { expect as baseExpect, test, type Page, type Route } from "@playwright/test"
import { mockDatabases, type DatabaseMock } from "./database-fixture"

/**
 * Stored programs in the SQL editor (`/databases/<id>/query`): a statement
 * that holds a body of statements of its own — a MySQL procedure written with
 * DELIMITER, a SQL Server batch ended by GO, an Oracle PL/SQL block ended by
 * a / line — is one statement, under the cursor as on the server.
 *
 * Where a statement ends is read by the page itself (`readScript`), and what
 * the page sends is what is asserted: the whole body from a cursor inside
 * it, the separators with everything, never a piece of one. The server's
 * reading of each text is stood in for by its answers, written out below as
 * the server gives them.
 */

const expect = baseExpect.configure({ timeout: 15_000 })
test.describe.configure({ timeout: 90_000 })

async function json(route: Route, body: unknown, status = 200) {
  await route.fulfill({ status, contentType: "application/json", body: JSON.stringify(body) })
}

/* -------------------------------------------------------------- scripts */

const MYSQL = `DELIMITER $$
CREATE PROCEDURE archive_old_posts(IN days INT)
BEGIN
  INSERT INTO posts_archive SELECT * FROM posts WHERE published_at < NOW() - INTERVAL days DAY;
  DELETE FROM posts WHERE published_at < NOW() - INTERVAL days DAY;
END$$
DELIMITER ;
CALL archive_old_posts(365);`
/** The procedure, as the cursor in its body sends it: no delimiter, no DELIMITER lines. */
const PROCEDURE = `CREATE PROCEDURE archive_old_posts(IN days INT)
BEGIN
  INSERT INTO posts_archive SELECT * FROM posts WHERE published_at < NOW() - INTERVAL days DAY;
  DELETE FROM posts WHERE published_at < NOW() - INTERVAL days DAY;
END`
const CALL = "CALL archive_old_posts(365)"
/** Everything, as Run everything sends it: the DELIMITER lines go too. */
const MYSQL_ALL = MYSQL.replace(/;$/, "")

const MSSQL = `CREATE OR ALTER PROCEDURE dbo.restock @sku nvarchar(20), @qty int AS
BEGIN
  SET NOCOUNT ON;
  UPDATE dbo.stock SET on_hand = on_hand + @qty WHERE sku = @sku;
END
EXEC dbo.restock @sku = N'A-100', @qty = 10
GO`
const REPEATED = "INSERT INTO dbo.stock_log (note) VALUES ('counted')\nGO 3"
const COUNTED = "INSERT INTO dbo.stock_log (note) VALUES ('counted')"

const ORACLE = `BEGIN
  UPDATE accounts SET balance = balance * 1.01 WHERE kind = 'savings';
  COMMIT;
END;
/
SELECT COUNT(*) FROM accounts WHERE kind = 'savings'`
/** The block as it is sent: with the semicolon after its END, without its / line. */
const BLOCK = `BEGIN
  UPDATE accounts SET balance = balance * 1.01 WHERE kind = 'savings';
  COMMIT;
END;`
const SAVINGS = "SELECT COUNT(*) FROM accounts WHERE kind = 'savings'"
const ORACLE_ALL = ORACLE
const ORACLE_NO_SLASH = ORACLE.replace("END;\n/\n", "END;\n")

const NO_DELIMITER = "DELIMITER\nCREATE PROCEDURE p() BEGIN SELECT 1; END"

/* -------------------------------------------------- the server's answers */

const BODY = "holds a block of statements, which runs whatever is written inside it"
const READ = { destructive: false, level: "read", reasons: [] as string[] }
const PROCEDURE_RISK = {
  destructive: true,
  level: "high",
  reasons: ["deletes rows", BODY, "inserts rows", "creates a database object"],
}
const CALL_RISK = {
  destructive: true,
  level: "high",
  reasons: ["statement is not a recognised read"],
}
const ROUTINE_RISK = {
  destructive: true,
  level: "high",
  reasons: [
    "updates rows",
    BODY,
    "SQL Server runs statements with no separator between them, and this text contains EXEC",
  ],
}
const INSERT_RISK = { destructive: false, level: "medium", reasons: ["inserts rows"] }
const BLOCK_RISK = { destructive: true, level: "high", reasons: ["updates rows", BODY] }

type Answer = { sql: string; line: number; risk: typeof READ }

/** What `POST /classify` says of each text the page is expected to send. */
const READINGS: Record<string, Answer[]> = {
  [PROCEDURE]: [{ sql: PROCEDURE, line: 1, risk: PROCEDURE_RISK }],
  [CALL]: [{ sql: CALL, line: 1, risk: CALL_RISK }],
  [MYSQL_ALL]: [
    { sql: PROCEDURE, line: 2, risk: PROCEDURE_RISK },
    { sql: CALL, line: 8, risk: CALL_RISK },
  ],
  [MSSQL]: [{ sql: MSSQL.replace(/\nGO$/, ""), line: 1, risk: ROUTINE_RISK }],
  [REPEATED]: [1, 2, 3].map(() => ({ sql: COUNTED, line: 1, risk: INSERT_RISK })),
  [BLOCK]: [{ sql: BLOCK, line: 1, risk: BLOCK_RISK }],
  [SAVINGS]: [{ sql: SAVINGS, line: 1, risk: READ }],
  [ORACLE_ALL]: [
    { sql: BLOCK, line: 1, risk: BLOCK_RISK },
    { sql: SAVINGS, line: 6, risk: READ },
  ],
  [ORACLE_NO_SLASH]: [{ sql: ORACLE_NO_SLASH, line: 1, risk: BLOCK_RISK }],
}

const UNREADABLE: Record<string, string> = {
  [NO_DELIMITER]: "line 1: DELIMITER names no delimiter",
}

const rank = (level: string) => ["read", "medium", "high", "critical"].indexOf(level)

/** Any other text — the advice asked for while the cursor moves — is read plainly. */
function readingOf(text: string): Answer[] {
  if (READINGS[text]) return READINGS[text]
  return text
    .split(";")
    .map((sql) => sql.trim())
    .filter(Boolean)
    .map((sql) => ({
      sql,
      line: text.slice(0, text.indexOf(sql)).split("\n").length,
      risk: /^(select|show)\b/i.test(sql) ? READ : CALL_RISK,
    }))
}

function resultOf(sql: string) {
  if (/^(select|call)\b/i.test(sql)) {
    const archived = /^call/i.test(sql)
    return {
      columns: [archived ? "archived" : "savings_accounts"],
      types: ["INT"],
      kinds: ["integer"],
      rows: [[archived ? "1284" : "312"]],
      rowCount: 1,
      rowsAffected: 0,
      duration: "3.2ms",
      truncated: false,
      statement: sql,
    }
  }
  return {
    columns: [],
    types: [],
    rows: [],
    rowCount: 0,
    rowsAffected: /^insert/i.test(sql) ? 1 : 0,
    duration: "4.1ms",
    truncated: false,
    statement: sql,
  }
}

/** The editor's routes for connections 1 and 2, over the section's fixture. Hands back what was sent. */
async function mockBodies(page: Page, options: DatabaseMock = {}) {
  await mockDatabases(page, options)
  const sent = {
    classify: [] as string[],
    query: [] as Record<string, unknown>[],
    script: [] as Record<string, unknown>[],
  }
  await page.route(/\/api\/v1\/databases\/(1|2)\/.+/, async (route) => {
    const request = route.request()
    const rest = new URL(request.url()).pathname.replace(/^\/api\/v1\/databases\/\d+/, "")
    const body = () => (request.postDataJSON() ?? {}) as Record<string, unknown>
    if (rest === "/catalog") {
      return json(route, {
        schema: "app",
        defaultSchema: "app",
        schemas: [{ name: "app", default: true, tables: 2 }],
        objects: {},
        truncated: [],
        limit: 5000,
      })
    }
    if (rest === "/outline") {
      return json(route, {
        schema: "app",
        tables: { "app.posts": ["id", "title", "published_at"], "app.accounts": ["id", "kind"] },
        entries: [
          { id: "app.posts", schema: "app", name: "posts", type: "table" },
          { id: "app.accounts", schema: "app", name: "accounts", type: "table" },
        ],
        truncated: false,
        total: 2,
        limit: 5000,
      })
    }
    if (rest === "/relations") return json(route, {})
    if (rest === "/history" || rest === "/queries") return json(route, [])
    if (rest === "/classify") {
      const text = String(body().query ?? "")
      sent.classify.push(text)
      if (UNREADABLE[text]) {
        return json(route, {
          destructive: true,
          level: "high",
          reasons: [UNREADABLE[text]],
          statements: [],
        })
      }
      const statements = readingOf(text)
      const worst = statements.reduce(
        (held, statement) =>
          rank(statement.risk.level) > rank(held.level) ? statement.risk : held,
        READ,
      )
      return json(route, {
        ...worst,
        reasons: [...new Set(statements.flatMap((statement) => statement.risk.reasons))],
        statements,
      })
    }
    if (rest === "/query") {
      const asked = body()
      sent.query.push(asked)
      const [statement] = readingOf(String(asked.query))
      return json(route, { result: resultOf(statement.sql), risk: statement.risk })
    }
    if (rest === "/script") {
      const asked = body()
      sent.script.push(asked)
      const statements = readingOf(String(asked.script)).map((statement, index) => ({
        index,
        ...statement,
        status: "ok",
        result: resultOf(statement.sql),
        durationMs: 4,
      }))
      return json(route, {
        statements,
        failed: -1,
        transaction: "none",
        durationMs: 14,
        risk: statements[0]?.risk ?? READ,
      })
    }
    return route.fallback()
  })
  return { sent }
}

const ON_SQLSERVER: DatabaseMock = {
  rows: {
    1: { name: "warehouse", driver: "sqlserver", port: "1433", database: "inventory", user: "sa" },
  },
  summaries: {
    1: {
      flavor: "sqlserver",
      flavorLabel: "SQL Server",
      versionNumber: "16.0.4135",
      version: "Microsoft SQL Server 2022 (RTM-CU14)",
    },
  },
}

const ON_ORACLE: DatabaseMock = {
  rows: { 1: { name: "ledger", driver: "oracle", port: "1521", database: "FREEPDB1" } },
  summaries: {
    1: {
      flavor: "oracle",
      flavorLabel: "Oracle",
      versionNumber: "23.5",
      version: "Oracle Database 23ai Free 23.5",
    },
  },
}

/* --------------------------------------------------------------- editor */

const editor = (page: Page) => page.locator("[data-slot=sql-editor] .monaco-editor").first()
const commands = (page: Page) => page.locator("[data-slot=query-commands]")
const results = (page: Page) => page.locator("[data-slot=query-results]")
const runButton = (page: Page) => commands(page).getByRole("button", { name: "Run", exact: true })
/** The lines the gutter marks as what Run sends. */
const marked = (page: Page) => page.locator("[data-slot=sql-editor] .margin .border-brand")
const editorText = (page: Page) =>
  page.locator("[data-slot=sql-editor] .view-lines").evaluate((el) =>
    [...el.querySelectorAll(".view-line")]
      .sort(
        (a, b) =>
          parseFloat((a as HTMLElement).style.top) - parseFloat((b as HTMLElement).style.top),
      )
      .map((line) => (line.textContent ?? "").replace(/\u00a0/g, " "))
      .join("\n"),
  )

async function openEditor(page: Page, id: number) {
  await page.goto(`/databases/${id}/query`)
  // Monaco is loaded on demand: on a busy runner its first paint is the slow part.
  await expect(editor(page)).toBeVisible({ timeout: 45_000 })
}

type Held = {
  getDomNode(): HTMLElement | null
  setValue(value: string): void
  setPosition(position: { lineNumber: number; column: number }): void
  focus(): void
}

/** Replaces the editor's text, set through the editor so the text sent is the text written. */
async function write(page: Page, sql: string) {
  await editor(page).click()
  await page.evaluate((text) => {
    const monaco = (window as unknown as { monaco: { editor: { getEditors(): Held[] } } }).monaco
    const held = monaco.editor
      .getEditors()
      .find((candidate) => candidate.getDomNode()?.closest("[data-slot=sql-editor]"))
    if (!held) throw new Error("no editor on the page")
    held.setValue(text)
    held.focus()
  }, sql)
}

/** Puts the cursor at a line and column, with the keyboard in the editor. */
async function cursorAt(page: Page, lineNumber: number, column: number) {
  await page.evaluate(
    ([line, col]) => {
      const monaco = (window as unknown as { monaco: { editor: { getEditors(): Held[] } } }).monaco
      const held = monaco.editor
        .getEditors()
        .find((candidate) => candidate.getDomNode()?.closest("[data-slot=sql-editor]"))
      if (!held) throw new Error("no editor on the page")
      held.setPosition({ lineNumber: line, column: col })
      held.focus()
    },
    [lineNumber, column] as const,
  )
}

const settled = (page: Page) =>
  page
    .getByRole("dialog")
    .evaluate((el) => Promise.all(el.getAnimations({ subtree: true }).map((a) => a.finished)))

const sideways = (page: Page) =>
  page.evaluate(() =>
    [document.documentElement, ...document.querySelectorAll("[data-slot=page]")]
      .map((el) => el.parentElement ?? el)
      .some((el) => el.scrollWidth > el.clientWidth + 1),
  )

/* ---------------------------------------------------------------- MySQL */

test("the cursor in a procedure's body runs the whole procedure, without its DELIMITER", async ({
  page,
}) => {
  const { sent } = await mockBodies(page)
  await openEditor(page, 2)
  await write(page, MYSQL)
  // On the DELETE, inside the body: the procedure is the statement, every line of it.
  await cursorAt(page, 5, 3)
  await expect(commands(page)).toContainText("Statement 1 of 2, lines 2–6")
  await expect(marked(page)).toHaveCount(5)
  await expect(commands(page).getByText("destroys", { exact: true })).toBeVisible()

  await page.keyboard.press("ControlOrMeta+Enter")
  const dialog = page.getByRole("dialog")
  await expect(dialog).toContainText("Run statement")
  await expect(dialog).toContainText(BODY)
  await expect(dialog).toContainText("DELETE FROM posts WHERE published_at")
  expect(sent.query).toHaveLength(0)
  await dialog.getByRole("button", { name: "Run statement" }).click()

  await expect.poll(() => sent.query.length).toBe(1)
  expect(sent.query[0].query).toBe(PROCEDURE)
  expect(sent.classify).toContain(PROCEDURE)
  expect(sent.script).toHaveLength(0)
  await expect(results(page)).toContainText("The statement returned no rows.")

  // On the CALL after it, the CALL alone.
  await cursorAt(page, 8, 3)
  await expect(commands(page)).toContainText("Statement 2 of 2")
  await expect(marked(page)).toHaveCount(1)
})

test("Run everything sends the DELIMITER lines with the statements, and each answers for itself", async ({
  page,
}) => {
  const { sent } = await mockBodies(page)
  await openEditor(page, 2)
  await write(page, MYSQL)
  await page.keyboard.press("ControlOrMeta+Shift+Enter")
  const dialog = page.getByRole("dialog")
  await expect(dialog).toContainText("Run 2 statements")
  await dialog.getByRole("button", { name: "Run 2 statements" }).click()

  await expect.poll(() => sent.script.length).toBe(1)
  expect(sent.script[0].script).toBe(MYSQL_ALL)
  const chips = results(page).getByRole("group", { name: "Statements of this run" })
  await expect(chips.getByRole("button")).toHaveCount(2)
  await expect(results(page).getByRole("grid", { name: "Result of statement 2" })).toBeVisible()

  await results(page)
    .getByRole("tab", { name: /Messages/ })
    .click()
  const messages = results(page).locator("[data-slot=query-messages]")
  await expect(messages).toContainText("Line 2")
  await expect(messages).toContainText("CREATE PROCEDURE archive_old_posts(IN days INT)")
  await expect(messages).toContainText("Line 8")
  await expect(messages).toContainText("2 statements ran")
})

test("a protected connection refuses a procedure before anything is sent, and says why", async ({
  page,
}) => {
  const { sent } = await mockBodies(page, { rows: { 2: { readOnly: true } } })
  await openEditor(page, 2)
  await write(page, MYSQL)
  await cursorAt(page, 4, 3)

  const why = commands(page).locator("[data-slot=run-refused]")
  await expect(why).toContainText(
    "This connection is protected, so only statements that read are run.",
  )
  await expect(why).toContainText(BODY)
  await expect(runButton(page)).toBeDisabled()
  await page.keyboard.press("ControlOrMeta+Enter")
  await expect(results(page).locator("[data-slot=query-refusal]")).toContainText("protected")
  expect(sent.query).toHaveLength(0)
  expect(sent.script).toHaveLength(0)
})

test("a role that only reads has no Run, and a procedure under the cursor is not sent", async ({
  page,
}) => {
  const { sent } = await mockBodies(page, { viewer: true })
  await openEditor(page, 2)
  await write(page, MYSQL)
  await cursorAt(page, 4, 3)
  await expect(runButton(page)).toHaveCount(0)
  await expect(commands(page)).toContainText(
    "Your role reads this database and may not run statements.",
  )
  await page.keyboard.press("ControlOrMeta+Enter")
  await expect(results(page).locator("[data-slot=query-refusal]")).toContainText(
    "may not run statements",
  )
  expect(sent.query).toHaveLength(0)
  expect(sent.script).toHaveLength(0)
})

test("a separator the server cannot read is a run that does not happen, in the server's words", async ({
  page,
}) => {
  const { sent } = await mockBodies(page)
  await openEditor(page, 2)
  await write(page, NO_DELIMITER)
  await page.keyboard.press("ControlOrMeta+Shift+Enter")
  const refusal = results(page).locator("[data-slot=query-refusal]")
  await expect(refusal).toContainText("Not run")
  await expect(refusal).toContainText("could not be read")
  await expect(refusal).toContainText("line 1: DELIMITER names no delimiter")
  expect(sent.classify).toContain(NO_DELIMITER)
  expect(sent.query).toHaveLength(0)
  expect(sent.script).toHaveLength(0)
})

test("a text of nothing but separators and comments holds nothing to run", async ({ page }) => {
  const { sent } = await mockBodies(page)
  await openEditor(page, 2)
  await write(page, "DELIMITER $$\n-- the procedures go here\nDELIMITER ;\n")
  await page.keyboard.press("ControlOrMeta+Enter")
  await expect(results(page)).toContainText("Nothing has been run in this tab")
  expect(sent.classify).toHaveLength(0)
  expect(sent.query).toHaveLength(0)
  expect(sent.script).toHaveLength(0)
})

/* ----------------------------------------------------------- SQL Server */

test("a SQL Server procedure that runs on past its END says so, and GO 3 runs its batch three times", async ({
  page,
}) => {
  const { sent } = await mockBodies(page, ON_SQLSERVER)
  await openEditor(page, 1)
  await write(page, MSSQL)
  await cursorAt(page, 4, 3)
  // The EXEC after END, with no ; between them, is part of the procedure's body.
  const hint = commands(page).locator("[data-slot=runs-on]")
  await expect(hint).toHaveText("Runs on past its END")
  await expect(hint).toHaveAttribute("title", /everything up to the next GO/)

  // A ; after END ends it, and the EXEC is a statement of its own.
  await cursorAt(page, 5, 4)
  await page.keyboard.type(";")
  await cursorAt(page, 4, 3)
  await expect(hint).toHaveCount(0)
  await expect(commands(page)).toContainText("Statement 1 of 2, lines 1–5")
  await expect(marked(page)).toHaveCount(5)

  // GO is sent with the statement it repeats, and every run of it answers.
  await write(page, REPEATED)
  await page.keyboard.press("ControlOrMeta+Enter")
  await expect.poll(() => sent.script.length).toBe(1)
  expect(sent.script[0].script).toBe(REPEATED)
  await expect(
    results(page).getByRole("group", { name: "Statements of this run" }).getByRole("button"),
  ).toHaveCount(3)

  // Format lays the statement out and leaves GO on its own line.
  await commands(page).getByRole("button", { name: "Format" }).click()
  await expect
    .poll(() => editorText(page))
    .toBe("INSERT INTO dbo.stock_log (note)\nVALUES ('counted')\nGO 3")
})

/* --------------------------------------------------------------- Oracle */

test("an Oracle block is sent with the semicolon after its END and without its / line", async ({
  page,
}) => {
  const { sent } = await mockBodies(page, ON_ORACLE)
  await openEditor(page, 1)
  await write(page, ORACLE)
  await cursorAt(page, 2, 3)
  await expect(commands(page)).toContainText("Statement 1 of 2, lines 1–4")
  await expect(marked(page)).toHaveCount(4)

  await page.keyboard.press("ControlOrMeta+Enter")
  const dialog = page.getByRole("dialog")
  await expect(dialog).toContainText(BODY)
  await dialog.getByRole("button", { name: "Run statement" }).click()
  await expect.poll(() => sent.query.length).toBe(1)
  expect(sent.query[0].query).toBe(BLOCK)

  // With no / after it, the block takes in the rest of the text, and the strip says so.
  await write(page, ORACLE_NO_SLASH)
  await cursorAt(page, 2, 3)
  const hint = commands(page).locator("[data-slot=runs-on]")
  await expect(hint).toHaveText("Runs to the end: no / after it")
  await expect(hint).toHaveAttribute("title", /a line holding only \//)
})

/* ---------------------------------------------------------------- phone */

test("at a phone's width the procedure's run fits without scrolling sideways", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 })
  await mockBodies(page)
  await openEditor(page, 2)
  await write(page, MYSQL)
  await cursorAt(page, 5, 3)
  await expect(commands(page).getByText("destroys", { exact: true })).toBeVisible()
  expect(await sideways(page)).toBe(false)
  await page.keyboard.press("ControlOrMeta+Enter")
  await page.getByRole("dialog").getByRole("button", { name: "Run statement" }).click()
  await expect(results(page)).toContainText("The statement returned no rows.")
  expect(await sideways(page)).toBe(false)
})

/* ---------------------------------------------------------- screenshots */

test("screenshots @shots", async ({ page }) => {
  test.skip(!process.env.JD_SHOTS, "set JD_SHOTS to a directory to take the screenshots")
  const dir = process.env.JD_SHOTS
  await page.setViewportSize({ width: 1440, height: 900 })

  // MySQL: the cursor in the body marks the whole procedure.
  await mockBodies(page)
  await openEditor(page, 2)
  await write(page, MYSQL)
  await cursorAt(page, 5, 3)
  await expect(commands(page)).toContainText("Statement 1 of 2, lines 2–6")
  await expect(commands(page).getByText("destroys", { exact: true })).toBeVisible()
  await page.screenshot({ path: `${dir}/mysql-procedure-1440.png` })

  await page.keyboard.press("ControlOrMeta+Enter")
  await expect(page.getByRole("dialog")).toContainText(BODY)
  await settled(page)
  await page.screenshot({ path: `${dir}/mysql-confirm-1440.png` })
  await page.getByRole("dialog").getByRole("button", { name: "Cancel" }).click()
  await expect(page.getByRole("dialog")).toBeHidden()

  await page.keyboard.press("ControlOrMeta+Shift+Enter")
  await page.getByRole("dialog").getByRole("button", { name: "Run 2 statements" }).click()
  await results(page)
    .getByRole("tab", { name: /Messages/ })
    .click()
  await expect(results(page).locator("[data-slot=query-messages]")).toContainText(
    "2 statements ran",
  )
  await page.screenshot({ path: `${dir}/mysql-run-everything-1440.png` })

  await page.setViewportSize({ width: 390, height: 844 })
  await page.reload()
  await expect(editor(page)).toBeVisible({ timeout: 45_000 })
  await write(page, MYSQL)
  await cursorAt(page, 5, 3)
  await expect(commands(page).getByText("destroys", { exact: true })).toBeVisible()
  await page.screenshot({ path: `${dir}/mysql-procedure-390.png` })

  // A protected connection says why before Run is pressed.
  await page.unrouteAll({ behavior: "ignoreErrors" })
  await page.setViewportSize({ width: 1440, height: 900 })
  await mockBodies(page, { rows: { 2: { readOnly: true } } })
  await openEditor(page, 2)
  await write(page, MYSQL)
  await cursorAt(page, 4, 3)
  await expect(commands(page).locator("[data-slot=run-refused]")).toContainText(BODY)
  await page.screenshot({ path: `${dir}/mysql-protected-1440.png` })

  // SQL Server: a procedure that took in the EXEC after its END.
  await page.unrouteAll({ behavior: "ignoreErrors" })
  await mockBodies(page, ON_SQLSERVER)
  await openEditor(page, 1)
  await write(page, MSSQL)
  await cursorAt(page, 4, 3)
  await expect(commands(page).locator("[data-slot=runs-on]")).toBeVisible()
  await page.screenshot({ path: `${dir}/sqlserver-runs-on-1440.png` })

  // Oracle: the block ran, and the query after its / line is a statement of its own.
  await page.unrouteAll({ behavior: "ignoreErrors" })
  await mockBodies(page, ON_ORACLE)
  await openEditor(page, 1)
  await write(page, ORACLE)
  await page.keyboard.press("ControlOrMeta+Shift+Enter")
  await page.getByRole("dialog").getByRole("button", { name: "Run 2 statements" }).click()
  await expect(results(page).getByRole("grid", { name: "Result of statement 2" })).toBeVisible()
  await cursorAt(page, 2, 3)
  await expect(commands(page)).toContainText("Statement 1 of 2, lines 1–4")
  await page.screenshot({ path: `${dir}/oracle-block-1440.png` })
})
