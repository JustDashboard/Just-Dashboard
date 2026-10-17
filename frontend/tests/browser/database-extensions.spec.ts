import { expect as baseExpect, test, type Page, type Route } from "@playwright/test"
import { mockDatabases, type DatabaseMock } from "./database-fixture"

/**
 * A PostgreSQL database's Extensions page: the catalogue it reads, and the four
 * things done to an extension from it.
 *
 * The claims are the page's to its reader. What the database has is listed
 * first, and what could be enabled, what has a newer version and what the
 * server's image does not ship are each a chip away, grouped by what they are
 * for, searched and filtered. Enabling one offers the version, the schema and
 * CASCADE where the server leaves a choice, and shows the statement the server
 * planned for exactly what the form says; updating goes along a path the
 * server ships; disabling lists what depends on the extension and waits for
 * CASCADE by name. An extension whose library has to be loaded when the
 * server starts is enabled in three steps in one dialog — load it at start,
 * restart, create — each shown before it runs, and opens on the step the
 * server is at. A role that may only read is shown all of it and offered none
 * of it.
 */

const expect = baseExpect.configure({ timeout: 15_000 })
test.describe.configure({ timeout: 60_000 })

type Ext = Record<string, unknown> & { name: string; installed: boolean }

const EXTENSIONS: Ext[] = [
  {
    name: "hstore",
    version: "1.4",
    availableVersion: "1.8",
    installed: true,
    schema: "public",
    category: "Data types",
    about: "Sets of key–value pairs in one column.",
  },
  {
    name: "pg_stat_statements",
    version: "1.11",
    availableVersion: "1.11",
    installed: true,
    schema: "public",
    category: "Monitoring",
    library: "pg_stat_statements",
    about: "How often each statement runs and what it costs. The Performance page reads it.",
  },
  {
    name: "plpgsql",
    version: "1.0",
    availableVersion: "1.0",
    installed: true,
    schema: "pg_catalog",
    category: "Languages",
    about: "PostgreSQL's own procedural language. Installed in every database.",
  },
  {
    name: "auto_explain",
    installed: false,
    module: true,
    library: "auto_explain",
    category: "Monitoring",
    about: "Writes the plan of every statement slower than a threshold to the server log.",
  },
  {
    name: "citext",
    availableVersion: "1.6",
    installed: false,
    category: "Data types",
    about: "A text type that compares without regard to case, for email addresses and user names.",
  },
  {
    name: "cube",
    availableVersion: "1.5",
    installed: false,
    category: "Data types",
    about: "Points and boxes in many dimensions.",
  },
  {
    name: "earthdistance",
    availableVersion: "1.2",
    installed: false,
    category: "Geospatial",
    about: "Great-circle distances between points on the Earth, built on cube.",
  },
  {
    name: "pg_cron",
    availableVersion: "1.6",
    installed: false,
    category: "Scheduling",
    library: "pg_cron",
    about: "Runs SQL on a cron schedule, from inside the database.",
  },
  {
    name: "pg_trgm",
    availableVersion: "1.6",
    installed: false,
    category: "Search",
    about: "Trigram similarity between strings, and indexes that speed up LIKE and ILIKE.",
  },
  {
    name: "pgcrypto",
    availableVersion: "1.3",
    installed: false,
    category: "Security",
    about: "Hashing, encryption and random bytes in SQL.",
  },
  {
    name: "postgres_fdw",
    availableVersion: "1.1",
    installed: false,
    category: "Foreign data",
    about: "Queries the tables of another PostgreSQL server as if they were here.",
  },
  {
    name: "uuid-ossp",
    availableVersion: "1.1",
    installed: false,
    category: "Data types",
    about: "Functions that generate UUIDs; gen_random_uuid() is built in since 13.",
  },
  {
    name: "postgis",
    installed: false,
    unavailable: true,
    category: "Geospatial",
    about: "Geometry and geography types, spatial indexes and hundreds of spatial functions.",
  },
  {
    name: "vector",
    installed: false,
    unavailable: true,
    category: "Vectors",
    about: "A vector type with nearest-neighbour indexes (HNSW and IVFFlat), for embeddings.",
  },
]

type Preload = {
  loaded: string[]
  configured: string[]
  pendingRestart: boolean
  settable: boolean
  reason?: string
}

const version = (v: string, over: Record<string, unknown> = {}) => ({
  version: v,
  superuser: true,
  trusted: true,
  relocatable: true,
  requires: [],
  missing: [],
  ...over,
})

const detailOf = (extension: Ext, over: Record<string, unknown> = {}) => ({
  ...extension,
  versions: [version(String(extension.availableVersion ?? "1.0"))],
  updates: [],
  schemas: ["extensions", "public"],
  defaultSchema: "public",
  members: [],
  dependents: [],
  dependentsTotal: 0,
  superuser: true,
  ...over,
})

const DETAILS: Record<string, unknown> = {
  earthdistance: detailOf(EXTENSIONS[6], {
    versions: [
      version("1.2", { trusted: false, requires: ["cube"], missing: ["cube"] }),
      version("1.1", { trusted: false, requires: ["cube"], missing: ["cube"] }),
    ],
  }),
  hstore: detailOf(EXTENSIONS[0], {
    versions: ["1.8", "1.7", "1.6", "1.5", "1.4"].map((v) => version(v)),
    updates: ["1.8", "1.7", "1.6", "1.5"],
    members: [
      { kind: "function", count: 57 },
      { kind: "operator", count: 22 },
      { kind: "operator class", count: 4 },
      { kind: "type", count: 2 },
    ],
    dependents: [
      {
        kind: "table column",
        object: "column attributes of table catalog.products",
        via: "type hstore",
      },
      {
        kind: "view",
        object: "view catalog.product_attributes",
        via: "column attributes of table catalog.products",
      },
    ],
    dependentsTotal: 2,
    drop: 'DROP EXTENSION "hstore"',
    dropCascade: 'DROP EXTENSION "hstore" CASCADE',
  }),
  pg_trgm: detailOf(EXTENSIONS[8]),
}

type Sent = { method: string; path: string; query: string; body: unknown }

/** What the create route writes for a body, as the server would: the preview's answer and the run's. */
function createStatement(body: Record<string, unknown>) {
  let stmt = `CREATE EXTENSION IF NOT EXISTS "${body.name}"`
  if (body.schema) stmt += ` WITH SCHEMA "${body.schema}"`
  if (body.version) stmt += ` VERSION '${body.version}'`
  if (body.cascade) stmt += " CASCADE"
  return stmt
}

const planned = (statements: string[], preview: boolean, extra: Record<string, unknown> = {}) => ({
  statement: statements.join(";\n"),
  statements,
  ...(preview ? { preview: true } : {}),
  ...extra,
})

/**
 * The extension routes of connection 1, over the shared fixture. The preload
 * state is the server's: a preload adds to what the files name, and a restart
 * makes that what is loaded.
 */
async function mockExtensions(
  page: Page,
  options: DatabaseMock & {
    extensions?: Ext[]
    preload?: Partial<Preload>
    /** The list's answer instead, for a read that fails. */
    list?: (call: number) => { status: number; body: unknown } | undefined
  } = {},
) {
  await mockDatabases(page, {
    ...options,
    summaries: {
      1: {
        container: { id: "c1", name: "shop-db", image: "postgres:17", state: "running" },
        ...options.summaries?.[1],
      },
    },
  })
  const sent: Sent[] = []
  const state = {
    preload: {
      loaded: ["pg_stat_statements"],
      configured: ["pg_stat_statements"],
      pendingRestart: false,
      settable: true,
      ...options.preload,
    } as Preload,
    extensions: (options.extensions ?? EXTENSIONS).map((one) => ({ ...one })),
  }
  let listed = 0
  const json = (route: Route, body: unknown, status = 200) =>
    route.fulfill({ status, contentType: "application/json", body: JSON.stringify(body) })

  await page.route("**/api/v1/databases/1/**", async (route) => {
    const url = new URL(route.request().url())
    const path = url.pathname.replace(/^\/api\/v1\/databases\/1/, "")
    const method = route.request().method()
    const preview = url.searchParams.get("preview") === "1"
    let body: Record<string, unknown> = {}
    if (method !== "GET") {
      try {
        body = route.request().postDataJSON() ?? {}
      } catch {
        body = {}
      }
      sent.push({ method, path, query: url.search, body })
    }
    const named = /^\/server\/extensions\/([^/]+)$/.exec(path)
    const name = named ? decodeURIComponent(named[1]) : ""

    if (method === "GET" && path === "/server/extensions") {
      const failed = options.list?.(listed++)
      if (failed) return json(route, failed.body, failed.status)
      return json(route, {
        supported: true,
        editable: true,
        preload: {
          ...state.preload,
          pendingRestart:
            state.preload.configured.join() !== state.preload.loaded.join() ||
            state.preload.pendingRestart,
        },
        extensions: state.extensions.map((one) =>
          one.module
            ? { ...one, installed: state.preload.loaded.includes(String(one.library)) }
            : one,
        ),
      })
    }
    if (method === "GET" && named) {
      const detail = DETAILS[name] ?? detailOf(state.extensions.find((one) => one.name === name)!)
      return json(route, detail)
    }
    if (method === "POST" && path === "/server/extensions") {
      const stmt = createStatement(body)
      if (!preview) {
        const one = state.extensions.find((e) => e.name === body.name)
        if (one)
          Object.assign(one, { installed: true, version: one.availableVersion, schema: "public" })
      }
      return json(route, planned([stmt], preview))
    }
    if (method === "PATCH" && named) {
      const stmt = `ALTER EXTENSION "${name}" UPDATE TO '${body.version}'`
      return json(route, planned([stmt], preview))
    }
    if (method === "DELETE" && named) {
      const cascade = url.searchParams.get("cascade") === "1"
      return json(route, planned([`DROP EXTENSION "${name}"${cascade ? " CASCADE" : ""}`], false))
    }
    if (method === "POST" && path === "/server/preload") {
      const library = String(body.library)
      const libraries = [...state.preload.configured, library]
      const statements = [
        `ALTER SYSTEM SET shared_preload_libraries = ${libraries.map((one) => `'${one}'`).join(", ")}`,
        ...(library === "pg_cron" ? [`ALTER SYSTEM SET "cron"."database_name" = 'shop_main'`] : []),
        "SELECT pg_reload_conf()",
      ]
      if (!preview) state.preload.configured = libraries
      return json(
        route,
        planned(statements, preview, {
          library,
          libraries,
          restartRequired: true,
          notes:
            library === "pg_cron"
              ? [
                  "cron.database_name is set to shop_main: pg_cron keeps its jobs in that database and can only be created there.",
                ]
              : [],
        }),
      )
    }
    if (method === "POST" && path === "/power") {
      state.preload.loaded = [...state.preload.configured]
      state.preload.pendingRestart = false
      return json(route, { action: "restart", via: "docker", target: "shop-db", state: "running" })
    }
    return route.fallback()
  })
  return { sent, state }
}

/** Opens the page and waits for the list of connections to have answered. */
async function visit(page: Page, path = "/databases/1/settings/extensions") {
  const listed = page.waitForResponse(
    (response) => new URL(response.url()).pathname === "/api/v1/databases/",
    { timeout: 45_000 },
  )
  await page.goto(path)
  await listed
}

const section = (page: Page) => page.locator("section#extensions")
const dialog = (page: Page) => page.getByRole("dialog")
const row = (page: Page, name: string) => section(page).locator(`[data-extension="${name}"]`)
const chip = (page: Page, name: RegExp) => section(page).getByRole("button", { name })

async function noSidewaysScroll(page: Page) {
  const sideways = await page.evaluate(() =>
    [document.documentElement, ...document.querySelectorAll("[data-slot=page]")]
      .map((el) => el.parentElement ?? el)
      .some((el) => el.scrollWidth > el.clientWidth + 1),
  )
  expect(sideways, "the page scrolls sideways").toBe(false)
}

test("the catalogue is read by what each is for, searched, filtered and told apart from what the image lacks", async ({
  page,
}) => {
  await mockExtensions(page)
  await visit(page)
  await expect(section(page)).toContainText("3 enabled · 9 more available · 1 update available")
  // What the database has: a newer default is a fact on the row, and the library a
  // row needs is said beside it.
  await expect(row(page, "hstore")).toContainText("1.4")
  await expect(row(page, "hstore")).toContainText("1.8 available")
  await expect(row(page, "pg_stat_statements")).toContainText(
    "The server loads pg_stat_statements when it starts.",
  )
  // A row you read, not one you take; one that ships with PostgreSQL is drawn as PostgreSQL's.
  await expect(section(page).locator("[data-slot=choice-row]")).toHaveCount(0)
  await expect(row(page, "pg_stat_statements").locator("img")).toHaveAttribute(
    "src",
    "/logos/postgresql.svg",
  )

  await chip(page, /^Available/).click()
  // Grouped by what each is for, under the catalogue's headings.
  for (const heading of ["Data types", "Geospatial", "Monitoring", "Scheduling", "Search"]) {
    await expect(section(page).getByText(heading, { exact: true })).toBeVisible()
  }
  await expect(row(page, "pg_cron")).toContainText(
    "Needs pg_cron loaded when the server starts, and a restart.",
  )
  await section(page).getByLabel("Filter the extensions").fill("trigram")
  await expect(section(page).locator("[data-extension]")).toHaveCount(1)
  await expect(row(page, "pg_trgm")).toBeVisible()
  await section(page).getByLabel("Filter the extensions").fill("")
  await section(page).getByRole("combobox", { name: "Filter by category" }).click()
  await page.getByRole("option", { name: "Data types" }).click()
  await expect(section(page).locator("[data-extension]")).toHaveCount(3)
  await expect(row(page, "citext")).toBeVisible()

  await chip(page, /^Not in this image/).click()
  await expect(row(page, "vector")).toContainText("Not in this server's image.")
  await expect(row(page, "vector").getByRole("button")).toHaveCount(0)

  await chip(page, /^Update available/).click()
  await expect(section(page).locator("[data-extension]")).toHaveCount(1)
  await expect(row(page, "hstore").getByRole("button", { name: "Update hstore" })).toBeVisible()
})

test("an extension is enabled with a version, a schema and CASCADE, and runs the statement it showed", async ({
  page,
}) => {
  const { sent } = await mockExtensions(page)
  await visit(page)
  await chip(page, /^Available/).click()
  await row(page, "earthdistance").getByRole("button", { name: "Enable earthdistance" }).click()
  const form = dialog(page)
  await expect(form).toContainText("Great-circle distances")
  // What it needs and the database lacks is created with it unless the reader says otherwise.
  const cascade = form.getByRole("switch", { name: "Also enable what it needs: cube" })
  await expect(cascade).toBeChecked()
  await expect(form).toContainText('CREATE EXTENSION IF NOT EXISTS "earthdistance" CASCADE')

  await form.getByRole("combobox", { name: "Schema" }).click()
  await page.getByRole("option", { name: "extensions", exact: true }).click()
  await form.getByRole("combobox", { name: "Version" }).click()
  await page.getByRole("option", { name: "1.1", exact: true }).click()
  const statement = `CREATE EXTENSION IF NOT EXISTS "earthdistance" WITH SCHEMA "extensions" VERSION '1.1' CASCADE`
  await expect(form).toContainText(statement)

  await cascade.click()
  await expect(form).toContainText(
    "Without it the server refuses: cube is not enabled in shop_main.",
  )
  await cascade.click()
  await form.getByRole("button", { name: "Enable", exact: true }).click()
  await expect(form).toHaveCount(0)
  const run = sent.filter(
    (one) => one.path === "/server/extensions" && !one.query.includes("preview"),
  )
  expect(run).toHaveLength(1)
  expect(run[0].body).toEqual({
    name: "earthdistance",
    version: "1.1",
    schema: "extensions",
    cascade: true,
  })
})

test("an update goes along a path the server ships, to the version chosen", async ({ page }) => {
  const { sent } = await mockExtensions(page)
  await visit(page)
  await row(page, "hstore").getByRole("button", { name: "Update hstore" }).click()
  const form = dialog(page)
  await expect(form.getByRole("radio", { name: "1.8" })).toHaveAttribute("data-state", "on")
  await expect(form).toContainText(`ALTER EXTENSION "hstore" UPDATE TO '1.8'`)
  await form.getByRole("radio", { name: "1.6" }).click()
  await expect(form).toContainText(`ALTER EXTENSION "hstore" UPDATE TO '1.6'`)
  await form.getByRole("button", { name: "Update", exact: true }).click()
  await expect(form).toHaveCount(0)
  expect(
    sent.find((one) => one.method === "PATCH" && !one.query.includes("preview"))?.body,
  ).toEqual({
    version: "1.6",
  })
})

test("a drop lists what depends on the extension first, and waits for CASCADE by name", async ({
  page,
}) => {
  const { sent } = await mockExtensions(page)
  await visit(page)
  await row(page, "hstore").getByRole("button", { name: "Disable hstore" }).click()
  const form = dialog(page)
  await expect(form).toContainText("57 functions, 22 operators, 4 operator classes and 2 types")
  const dependents = form.getByRole("list", { name: "What depends on hstore" })
  await expect(dependents.getByRole("listitem")).toHaveCount(2)
  await expect(dependents).toContainText("column attributes of table catalog.products")
  await expect(dependents).toContainText("uses type hstore")
  await expect(form).toContainText('DROP EXTENSION "hstore"')
  const disable = form.getByRole("button", { name: "Disable", exact: true })
  await expect(disable).toBeDisabled()

  await form.getByRole("switch", { name: "Drop these 2 with the extension" }).click()
  await expect(form).toContainText('DROP EXTENSION "hstore" CASCADE')
  await disable.click()
  await expect(form).toHaveCount(0)
  const drop = sent.find((one) => one.method === "DELETE")
  expect(drop?.path).toBe("/server/extensions/hstore")
  expect(drop?.query).toBe("?cascade=1")
})

test("a library loaded at start is added, the server restarted, then the extension created, each shown first", async ({
  page,
}) => {
  const { sent } = await mockExtensions(page)
  await visit(page)
  await chip(page, /^Available/).click()
  await row(page, "pg_cron").getByRole("button", { name: "Enable pg_cron" }).click()
  const flow = dialog(page)
  const steps = flow.getByRole("list", { name: "The 3 steps to enable pg_cron" })
  await expect(steps.getByRole("listitem")).toHaveCount(3)
  // The list keeps what is loaded already, and says what it does with the parameter pg_cron reads.
  await expect(flow).toContainText(
    "ALTER SYSTEM SET shared_preload_libraries = 'pg_stat_statements', 'pg_cron'",
  )
  await expect(flow).toContainText("Kept")
  await expect(flow).toContainText("cron.database_name is set to shop_main")
  await flow.getByRole("button", { name: "Load at start" }).click()

  // Read back from the server: the files name it, the running server has not loaded it.
  const restart = flow.getByRole("button", { name: "Restart shop" })
  await expect(restart).toBeVisible()
  await expect(steps.getByRole("listitem").nth(0)).toContainText("Done")
  await expect(flow).toContainText("Every session on it is disconnected")
  await expect(section(page)).toContainText("pg_cron is loaded once shop restarts")
  await restart.click()

  const create = flow.getByRole("button", { name: "Enable pg_cron" })
  await expect(create).toBeEnabled()
  await expect(flow).toContainText('CREATE EXTENSION IF NOT EXISTS "pg_cron"')
  await create.click()
  await expect(flow).toHaveCount(0)
  expect(
    sent.filter((one) => !one.query.includes("preview")).map((one) => `${one.method} ${one.path}`),
  ).toEqual(["POST /server/preload", "POST /power", "POST /server/extensions"])
  expect(sent.find((one) => one.path === "/power")?.body).toEqual({ action: "restart" })
})

test("a restart that is still owed is said at the top, and taken from there", async ({ page }) => {
  const { sent } = await mockExtensions(page, {
    preload: { configured: ["pg_stat_statements", "pg_cron"] },
  })
  await visit(page)
  await expect(section(page)).toContainText("Waiting for a restart")
  await expect(section(page)).toContainText("pg_cron is loaded once shop restarts")
  await chip(page, /^Available/).click()
  await expect(row(page, "pg_cron")).toContainText("pg_cron is loaded once the server restarts.")
  // The dialog opens on the step the server is at.
  await row(page, "pg_cron").getByRole("button", { name: "Enable pg_cron" }).click()
  await expect(dialog(page).getByRole("button", { name: "Restart shop" })).toBeVisible()
  await page.keyboard.press("Escape")
  await expect(dialog(page)).toHaveCount(0)

  await section(page).getByRole("button", { name: "Restart shop" }).click()
  await dialog(page).getByRole("button", { name: "Restart", exact: true }).click()
  await expect(section(page)).not.toContainText("Waiting for a restart")
  expect(sent.map((one) => `${one.method} ${one.path}`)).toContain("POST /power")
})

test("a list the dashboard cannot change says why, and offers nothing that would be refused", async ({
  page,
}) => {
  await mockExtensions(page, {
    preload: {
      settable: false,
      reason:
        "shared_preload_libraries is given on the server's command line, which outranks ALTER SYSTEM: a library is added there, where the server is started.",
    },
  })
  await visit(page)
  await chip(page, /^Available/).click()
  await row(page, "pg_cron").getByRole("button", { name: "Enable pg_cron" }).click()
  const flow = dialog(page)
  await expect(flow).toContainText("pg_cron cannot be added from here")
  await expect(flow).toContainText("given on the server's command line")
  await expect(flow.getByRole("button", { name: "Load at start" })).toHaveCount(0)
  await flow.getByRole("button", { name: "Close" }).click()
  await expect(flow).toHaveCount(0)
})

test("an empty database and a list that cannot be read each say so", async ({ page }) => {
  let failing = true
  await mockExtensions(page, {
    extensions: EXTENSIONS.filter((one) => !one.installed),
    list: () =>
      failing
        ? {
            status: 502,
            body: {
              error: { code: "query_failed", message: "permission denied for view pg_settings" },
            },
          }
        : undefined,
  })
  await visit(page)
  await expect(section(page)).toContainText("Could not read the extensions")
  await expect(section(page)).toContainText("permission denied for view pg_settings")
  failing = false
  await section(page).getByRole("button", { name: "Try again" }).click()
  await expect(section(page)).toContainText("No extension is enabled in this database.")
  await expect(section(page)).toContainText("0 enabled")
})

test("a role that may only read is shown the catalogue and offered none of its commands", async ({
  page,
}) => {
  await mockExtensions(page, {
    viewer: true,
    preload: { configured: ["pg_stat_statements", "pg_cron"] },
  })
  await visit(page)
  await expect(row(page, "hstore")).toContainText("1.8 available")
  await expect(section(page)).toContainText("pg_cron is loaded once shop restarts")
  await expect(
    section(page).getByRole("button", { name: /^(Enable|Update|Disable|Restart) / }),
  ).toHaveCount(0)
  await chip(page, /^Available/).click()
  await expect(row(page, "pg_cron")).toBeVisible()
  await expect(
    section(page).getByRole("button", { name: /^(Enable|Update|Disable|Restart) / }),
  ).toHaveCount(0)
})

test("at a phone's width the list and the dialogs keep to the screen", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 })
  await mockExtensions(page)
  await visit(page)
  await expect(row(page, "hstore")).toBeVisible()
  await noSidewaysScroll(page)
  await chip(page, /^Available/).click()
  await expect(row(page, "pg_cron")).toBeVisible()
  await noSidewaysScroll(page)
  await row(page, "pg_cron").getByRole("button", { name: "Enable pg_cron" }).click()
  await expect(dialog(page).getByRole("button", { name: "Load at start" })).toBeVisible()
  const box = await dialog(page).boundingBox()
  expect(box && box.x >= 0 && box.x + box.width <= 390).toBe(true)
})

test("extension states @shots", async ({ page }) => {
  test.skip(!process.env.JD_SHOTS, "JD_SHOTS names the directory the screenshots go in")
  test.setTimeout(180_000)
  const shot = (name: string) => page.screenshot({ path: `${process.env.JD_SHOTS}/${name}.png` })
  await page.setViewportSize({ width: 1440, height: 900 })
  await mockExtensions(page)
  await visit(page)
  await expect(row(page, "hstore")).toBeVisible()
  await shot("extensions-enabled-1440")

  await chip(page, /^Available/).click()
  await expect(row(page, "pg_cron")).toBeVisible()
  await shot("extensions-catalogue-1440")

  await row(page, "earthdistance").getByRole("button", { name: "Enable earthdistance" }).click()
  await expect(dialog(page)).toContainText('CREATE EXTENSION IF NOT EXISTS "earthdistance" CASCADE')
  await shot("extensions-enable-1440")
  await page.keyboard.press("Escape")

  await row(page, "pg_cron").getByRole("button", { name: "Enable pg_cron" }).click()
  await expect(dialog(page)).toContainText("ALTER SYSTEM SET shared_preload_libraries")
  await shot("extensions-preload-1440")
  await dialog(page).getByRole("button", { name: "Load at start" }).click()
  await expect(dialog(page).getByRole("button", { name: "Restart shop" })).toBeVisible()
  await shot("extensions-restart-1440")
  await page.keyboard.press("Escape")

  await chip(page, /^Enabled/).click()
  await row(page, "hstore").getByRole("button", { name: "Update hstore" }).click()
  await expect(dialog(page)).toContainText(`ALTER EXTENSION "hstore" UPDATE TO '1.8'`)
  await shot("extensions-update-1440")
  await page.keyboard.press("Escape")

  await row(page, "hstore").getByRole("button", { name: "Disable hstore" }).click()
  await dialog(page).getByRole("switch", { name: "Drop these 2 with the extension" }).click()
  await expect(dialog(page)).toContainText('DROP EXTENSION "hstore" CASCADE')
  await shot("extensions-disable-1440")
  // A choice was made, so a slip would ask; Cancel is the reader saying so.
  await dialog(page).getByRole("button", { name: "Cancel" }).click()
  await expect(dialog(page)).toHaveCount(0)

  await page.setViewportSize({ width: 390, height: 844 })
  await expect(row(page, "hstore")).toBeVisible()
  await shot("extensions-enabled-390")
})
