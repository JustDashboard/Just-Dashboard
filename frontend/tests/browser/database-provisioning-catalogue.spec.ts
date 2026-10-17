import { expect, test, type Page } from "@playwright/test"
import { SHOP, mockDatabases } from "./database-fixture"
import { mockFleet, refusal } from "./database-fleet-fixture"

/**
 * The provisioning catalogue as it stands after its refresh: PostgreSQL 18 by
 * default, each release's support status said as a plain fact, the releases
 * past their end of life kept apart behind Older versions, and the full
 * PostgreSQL template whose image is built on the server, as a job, before
 * its first start. And the same status beside a database's version on its
 * Home and its General settings.
 *
 * The catalogue below is what `GET /databases/provision/options` answers on
 * an x86-64 server on 2026-10-17, dates and all.
 */

const eol = (product: string, release: string, date: string, past: boolean) => ({
  product,
  release,
  date: `${date}T00:00:00Z`,
  past,
  daysLeft: past ? -60 : 900,
})
const pg = (major: string, image: string, date: string, past = false) => ({
  version: major,
  image,
  endOfLife: eol("PostgreSQL", major, date, past),
})
const PG_DATES: Record<string, string> = {
  "18": "2030-11-14",
  "17": "2029-11-08",
  "16": "2028-11-09",
  "15": "2027-11-11",
  "14": "2026-11-12",
}
const template = (over: Record<string, unknown>) => ({
  driver: "postgres",
  flavor: "postgres",
  port: 5432,
  defaultUser: "jd",
  database: true,
  ...over,
})

function catalogue(build: { ready: boolean; job?: string } = { ready: false }) {
  return [
    template({
      engine: "postgres",
      label: "PostgreSQL",
      image: "postgres:18-alpine",
      versions: ["18", "17", "16", "15", "14"].map((major) =>
        pg(major, `postgres:${major}-alpine`, PG_DATES[major]),
      ),
      defaultVersion: "18",
    }),
    template({
      engine: "pgvector",
      label: "PostgreSQL + pgvector",
      image: "pgvector/pgvector:pg18",
      versions: ["18", "17", "16", "15"].map((major) =>
        pg(major, `pgvector/pgvector:pg${major}`, PG_DATES[major]),
      ),
      defaultVersion: "18",
    }),
    template({
      engine: "postgres-full",
      label: "PostgreSQL, full",
      image: "jd-postgres-full:18",
      versions: [
        { ...pg("18", "jd-postgres-full:18", PG_DATES["18"]), build },
        { ...pg("17", "jd-postgres-full:17", PG_DATES["17"]), build: { ready: false } },
      ],
      defaultVersion: "18",
      extensions: ["vector", "postgis", "pg_cron", "pg_partman", "pgaudit", "hypopg", "pg_repack"],
    }),
    template({
      engine: "timescaledb",
      label: "TimescaleDB",
      flavor: "timescaledb",
      image: "timescale/timescaledb:2.30.2-pg18",
      versions: ["18", "17", "16"].map((major) =>
        pg(major, `timescale/timescaledb:2.30.2-pg${major}`, PG_DATES[major]),
      ),
      defaultVersion: "18",
    }),
    template({
      engine: "mysql",
      label: "MySQL",
      image: "mysql:8.4",
      driver: "mysql",
      flavor: "mysql",
      port: 3306,
      versions: [
        { version: "9.7", image: "mysql:9.7", endOfLife: eol("MySQL", "9.7", "2034-04-30", false) },
        { version: "8.4", image: "mysql:8.4", endOfLife: eol("MySQL", "8.4", "2032-04-30", false) },
        { version: "8.0", image: "mysql:8.0", endOfLife: eol("MySQL", "8.0", "2026-04-30", true) },
      ],
      defaultVersion: "8.4",
    }),
    template({
      engine: "mariadb",
      label: "MariaDB",
      image: "mariadb:11.8",
      driver: "mysql",
      flavor: "mariadb",
      port: 3306,
      versions: [
        ["12.3", "2029-06-12", false],
        ["11.8", "2028-06-04", false],
        ["11.4", "2029-05-29", false],
        ["10.11", "2028-02-16", false],
        ["10.6", "2026-07-06", true],
      ].map(([version, date, past]) => ({
        version,
        image: `mariadb:${version}`,
        endOfLife: eol("MariaDB", String(version), String(date), Boolean(past)),
      })),
      defaultVersion: "11.8",
    }),
    template({
      engine: "valkey",
      label: "Valkey",
      image: "valkey/valkey:8-alpine",
      driver: "redis",
      flavor: "valkey",
      port: 6379,
      defaultUser: "",
      database: false,
      versions: ["9", "8", "7.2"].map((version) => ({
        version,
        image: `valkey/valkey:${version}-alpine`,
      })),
      defaultVersion: "8",
    }),
    template({
      engine: "mongodb",
      label: "MongoDB",
      image: "mongo:8.0",
      driver: "mongodb",
      flavor: "mongodb",
      port: 27017,
      versions: [
        {
          version: "8.0",
          image: "mongo:8.0",
          endOfLife: eol("MongoDB", "8.0", "2029-10-31", false),
        },
        {
          version: "7.0",
          image: "mongo:7.0",
          endOfLife: eol("MongoDB", "7.0", "2027-08-31", false),
        },
        {
          version: "6.0",
          image: "mongo:6.0",
          endOfLife: eol("MongoDB", "6.0", "2025-07-31", true),
        },
      ],
      defaultVersion: "8.0",
    }),
    template({
      engine: "clickhouse",
      label: "ClickHouse",
      image: "clickhouse/clickhouse-server:26.8",
      driver: "clickhouse",
      flavor: "clickhouse",
      port: 9000,
      versions: [
        ["26.8", "2027-08-27", false],
        ["26.3", "2027-03-26", false],
        ["25.8", "2026-08-29", true],
      ].map(([version, date, past]) => ({
        version,
        image: `clickhouse/clickhouse-server:${version}`,
        endOfLife: eol("ClickHouse", String(version), String(date), Boolean(past)),
      })),
      defaultVersion: "26.8",
    }),
  ]
}

/** The build job as `GET /jobs/{id}` reads it: running with its output so far, or ended. */
function buildReading(status: "running" | "succeeded" | "failed", lines: string[], error?: string) {
  return {
    job: {
      id: "build-18",
      kind: "database.image.build.postgres-full.18",
      title: "Building jd-postgres-full:18",
      target: "jd-postgres-full:18",
      status,
      exitCode: status === "failed" ? 100 : 0,
      error,
      startedAt: "2026-10-17T09:00:00Z",
      endedAt: status === "running" ? undefined : "2026-10-17T09:03:12Z",
      startedBy: "operator",
      lines: lines.length,
    },
    lines: lines.map((text, index) => ({
      seq: index + 1,
      stream: index === 0 ? "status" : "stderr",
      text,
      at: "2026-10-17T09:00:00Z",
    })),
  }
}

const BUILD_OUTPUT = [
  "Building jd-postgres-full:18 from the dashboard's recipe 643388e87b5c",
  "#1 [internal] load build definition from Dockerfile",
  "#4 [1/2] FROM docker.io/library/postgres:18-trixie",
  "#5 [2/2] RUN set -eux; apt-get update; apt-get install -y --no-install-recommends",
  "#5 12.41 Setting up postgresql-18-pgvector (0.8.7-1.pgdg13+1) ...",
  "#5 18.07 Setting up postgresql-18-postgis-3 (3.6.4+dfsg-2.pgdg13+1) ...",
]

/**
 * A database's home with its own reads answered, so a screenshot of the line
 * the support status is on shows the home as it is on a healthy server.
 */
async function mockHealthyHome(page: Page) {
  const at = (step: number) =>
    new Date(Date.parse("2026-10-17T09:00:00Z") + step * 30_000).toISOString()
  const stats = (step: number) => ({
    supported: true,
    at: at(step),
    driver: "postgres",
    version: "PostgreSQL 14.11",
    uptimeSeconds: 186_400,
    role: "standalone",
    database: "shop_main",
    databaseBytes: 24_517_655,
    connections: { total: 12, active: 2, idle: 10, idleInTransaction: 0, waiting: 0, max: 100 },
    counters: {
      transactionsCommitted: 1_000 + step * 50,
      transactionsRolledBack: 0,
      blocksHit: 900_000 + step * 990,
      blocksRead: 2_700 + step * 10,
      rowsRead: 400_000 + step * 5_000,
      rowsWritten: 90_000 + step * 50,
    },
    gauges: { replicas: 0 },
    pool: null,
  })
  const dump = {
    file: "shop-20261017-060000.dump",
    size: 420_000,
    takenAt: "2026-10-17T06:00:00Z",
    format: "pg_dump archive",
    durationMs: 900,
    tool: "pg_dump",
    summary: "written by pg_dump",
    origin: "dump",
  }
  const answers: Record<string, unknown> = {
    "/stats": stats(3),
    "/stats/history": {
      samples: [0, 1, 2].map((step) => ({
        at: Date.parse(at(step)),
        stats: stats(step),
        gap: false,
      })),
      everySeconds: 30,
      retentionHours: 168,
    },
    "/tablestats": {
      supported: true,
      schema: "",
      truncated: false,
      tables: [
        { schema: "public", table: "orders", rows: 9_000, totalBytes: 3_399_680 },
        { schema: "public", table: "customers", rows: 2_400, totalBytes: 1_064_960 },
      ],
    },
    "/consumers": {
      checkedAt: "2026-10-17T09:00:00Z",
      nodes: [
        { id: "db:1", kind: "database", name: "shop", product: "postgres", connId: 1 },
        {
          id: "deploy:7",
          kind: "deployment",
          name: "shop-api",
          product: "nodejs",
          detail: "production",
          href: "/deploy/7",
        },
      ],
      edges: [
        {
          from: "db:1",
          to: "deploy:7",
          via: ["binding", "session"],
          sessions: 8,
          status: "connected",
        },
      ],
    },
    "/backups": {
      dir: "/var/lib/just-dashboard/backups/databases/shop",
      files: [dump],
      options: {},
    },
    "/schemas": [{ name: "shop_main", size: 24_517_655, owner: "app" }],
    "/statements": {
      supported: true,
      sort: "total",
      resettable: true,
      totalMs: 100,
      since: "2026-10-17T08:00:00Z",
      statements: [
        {
          id: "1",
          query: "SELECT *\n  FROM orders\n WHERE customer_id = $1",
          calls: 1_200,
          totalMs: 60,
          meanMs: 0.05,
          rows: 1_200,
          hitRatio: 1,
          share: 0.6,
        },
      ],
    },
  }
  await page.route("**/api/v1/databases/1/**", (route) => {
    const rest = new URL(route.request().url()).pathname.replace(/^\/api\/v1\/databases\/1/, "")
    return Object.hasOwn(answers, rest) ? route.fulfill({ json: answers[rest] }) : route.fallback()
  })
}

const panel = (page: Page) => page.locator("[data-slot=flow-panel]")
const engine = (page: Page, image: RegExp) => page.getByRole("button", { name: image })
const noSideScroll = (page: Page) =>
  page.evaluate(() => document.documentElement.scrollWidth > window.innerWidth)

/** Add a database with the refreshed catalogue; the build answers as `reading` says. */
async function mockCatalogue(
  page: Page,
  options: {
    build?: { ready: boolean; job?: string }
    readings?: () => ReturnType<typeof buildReading>
  } = {},
) {
  const server = await mockFleet(page)
  server.answer("GET /databases/provision/options", () => ({ body: catalogue(options.build) }))
  server.answer("POST /databases/provision/build", () => ({
    status: 202,
    body: buildReading("running", BUILD_OUTPUT.slice(0, 1)).job,
  }))
  server.answer("GET /jobs/build-18", () => ({
    body: (options.readings ?? (() => buildReading("running", BUILD_OUTPUT)))(),
  }))
  return server
}

test("PostgreSQL 18 is the default, and each release says how long it is maintained", async ({
  page,
}) => {
  const server = await mockCatalogue(page)
  await page.goto("/databases/new")
  await engine(page, /postgres:18-alpine/).click()
  const settings = panel(page)
  await expect(settings.getByRole("radio", { name: "18", exact: true })).toHaveAttribute(
    "data-state",
    "on",
  )
  await expect(settings).toContainText("Supported until Nov 2030")
  // PostgreSQL 14 is still maintained, so it stands with the rest.
  await settings.getByRole("radio", { name: "14", exact: true }).click()
  await expect(settings).toContainText("Supported until Nov 2026")
  await expect(settings.getByText("Older versions")).toHaveCount(0)
  await settings.getByRole("radio", { name: "18", exact: true }).click()
  await settings.getByRole("button", { name: "Create", exact: true }).click()
  await expect(page).toHaveURL(/\/databases\/20$/)
  expect(server.bodies("POST /databases/provision")).toEqual([
    expect.objectContaining({ engine: "postgres", version: "18", exposure: "local" }),
  ])
})

test("a release past its end of life is kept behind Older versions, which says so", async ({
  page,
}) => {
  const server = await mockCatalogue(page)
  await page.goto("/databases/new")
  await engine(page, /^MySQL/).click()
  const settings = panel(page)
  await expect(settings.getByRole("radio", { name: "8.4", exact: true })).toHaveAttribute(
    "data-state",
    "on",
  )
  await expect(settings).toContainText("Supported until Apr 2032")
  // Folded away, and not one of the versions offered first.
  await expect(settings.getByRole("radio", { name: "8.0", exact: true })).toBeHidden()
  const older = settings.getByRole("button", { name: /Older versions/ })
  await expect(older).toHaveAttribute("aria-expanded", "false")
  await older.click()
  await expect(older).toHaveAttribute("aria-expanded", "true")
  await expect(settings).toContainText("8.0 — End of life since Apr 30, 2026")
  await expect(settings).toContainText("no more fixes, security fixes included")
  await settings.getByRole("radio", { name: "8.0", exact: true }).click()
  await expect(settings.getByRole("radio", { name: "8.4", exact: true })).toHaveAttribute(
    "data-state",
    "off",
  )
  // Said once, in the fold beside 8.0: under 9.7 and 8.4 it would read as theirs.
  await expect(settings.getByText("End of life since Apr 30, 2026")).toHaveCount(1)
  await expect(settings.getByText("Supported until Apr 2032")).toHaveCount(0)
  await expect(settings.getByText("mysql:8.0", { exact: true })).toBeVisible()
  await settings.getByRole("button", { name: "Create", exact: true }).click()
  await expect(page).toHaveURL(/\/databases\/20$/)
  expect(server.bodies("POST /databases/provision")).toEqual([
    expect.objectContaining({ engine: "mysql", version: "8.0" }),
  ])
})

test("the full template says what it carries and builds its image as a job before it starts", async ({
  page,
}) => {
  // The build runs for as long as the test reads it, and ends when it says.
  let finished = false
  const server = await mockCatalogue(page, {
    readings: () =>
      finished
        ? buildReading("succeeded", [...BUILD_OUTPUT, "Built jd-postgres-full:18"])
        : buildReading("running", BUILD_OUTPUT),
  })
  let provisioned!: () => void
  const provision = new Promise<void>((resolve) => (provisioned = resolve))
  server.answer("POST /databases/provision", async () => {
    await provision
    return { status: 202, body: { container: "jd-postgres-full" } }
  })
  await page.goto("/databases/new")
  await engine(page, /jd-postgres-full:18/).click()
  const settings = panel(page)
  const notice = settings.getByText("Its image is built here first")
  await expect(notice).toBeVisible()
  for (const name of [
    "vector",
    "postgis",
    "pg_cron",
    "pg_partman",
    "pgaudit",
    "hypopg",
    "pg_repack",
  ]) {
    await expect(settings.getByText(name, { exact: true })).toBeVisible()
  }
  await expect(settings).toContainText("Settings → Extensions")
  await settings.getByLabel("Database", { exact: true }).fill("orders")
  await settings.getByRole("button", { name: "Create", exact: true }).click()

  const progress = settings.locator("[data-slot=database-progress]")
  await expect(progress.getByRole("listitem")).toHaveText([
    /Build the image/,
    /Start the container/,
    /Wait for the engine/,
    /Connect it/,
  ])
  await expect(progress.locator('[aria-current="step"]')).toContainText("Build the image")
  await expect(progress.getByRole("status")).toContainText("Building the image on this server")
  // The build's own output, where the job console draws every job.
  await expect(settings.getByText("Building jd-postgres-full:18").first()).toBeVisible()
  await expect(settings.getByText(/Setting up postgresql-18-pgvector/)).toBeVisible()
  await expect(settings).toContainText("This keeps running if you close the page")
  expect(server.bodies("POST /databases/provision")).toEqual([])

  finished = true
  await expect(progress.locator('[aria-current="step"]')).toContainText("Start the container")
  // Built here, so never pulled.
  await expect(progress.getByRole("status")).toContainText("Starting the container")
  await expect(progress.getByRole("status")).not.toContainText("Pulling")
  provisioned()
  await expect(page).toHaveURL(/\/databases\/20$/)
  expect(server.bodies("POST /databases/provision/build")).toEqual([
    { engine: "postgres-full", version: "18" },
  ])
  expect(server.bodies("POST /databases/provision")).toEqual([
    expect.objectContaining({ engine: "postgres-full", version: "18", database: "orders" }),
  ])
})

test("an image already built starts like any other, and a build in progress is waited for", async ({
  page,
}) => {
  const server = await mockCatalogue(page, { build: { ready: true } })
  await page.goto("/databases/new?engine=postgres-full")
  const settings = panel(page)
  await expect(settings.getByText("Its image is built", { exact: true })).toBeVisible()
  await expect(settings).toContainText("It starts like any other.")
  await settings.getByRole("button", { name: "Create", exact: true }).click()
  await expect(page).toHaveURL(/\/databases\/20$/)
  expect(server.bodies("POST /databases/provision/build")).toEqual([])

  await mockCatalogue(page, { build: { ready: false, job: "build-18" } })
  await page.goto("/databases/new?engine=postgres-full")
  await expect(panel(page).getByText("Its image is being built now")).toBeVisible()
  await expect(panel(page)).toContainText("Create waits for that build, then starts it.")
})

test("a build that fails stops before anything is started and shows why", async ({ page }) => {
  const server = await mockCatalogue(page, {
    readings: () =>
      buildReading(
        "failed",
        [...BUILD_OUTPUT, "E: Failed to fetch http://apt.postgresql.org/pub/repos/apt/ 503"],
        "the build of jd-postgres-full:18 exited with status 100; its output above says where it stopped",
      ),
  })
  await page.goto("/databases/new?engine=postgres-full")
  const settings = panel(page)
  await settings.getByRole("button", { name: "Create", exact: true }).click()
  await expect(settings.getByText("It was not started")).toBeVisible()
  await expect(settings).toContainText("exited with status 100")
  const failed = settings.getByRole("listitem").filter({ hasText: "Build the image" })
  await expect(failed).toContainText("Failed")
  await expect(settings.getByText(/Failed to fetch/)).toBeVisible()
  await expect(settings.getByRole("button", { name: "Create", exact: true })).toBeEnabled()
  expect(server.bodies("POST /databases/provision")).toEqual([])
})

test("the catalogue's empty and failed answers are said, and Retry asks again", async ({
  page,
}) => {
  const server = await mockFleet(page)
  let failing = true
  server.answer("GET /databases/provision/options", () =>
    failing ? refusal(503, "docker_unavailable", "Docker did not answer", true) : { body: [] },
  )
  await page.goto("/databases/new")
  await expect(page.getByText("Not available on this host")).toBeVisible()
  // A catalogue that could not be read is not one that offers nothing.
  await expect(page.getByText("This server offers no engine to start from here.")).toHaveCount(0)
  failing = false
  await page.getByRole("button", { name: "Retry loading database engines" }).click()
  await expect(page.getByText("Not available on this host")).toHaveCount(0)
  await expect(page.getByText("This server offers no engine to start from here.")).toBeVisible()
})

test("a role that may only read is not offered the catalogue, and reads the support status", async ({
  page,
}) => {
  await mockFleet(page, {
    viewer: true,
    summaries: { 1: { endOfLife: eol("PostgreSQL", "16", "2028-11-09", false) } },
  })
  await page.goto("/databases/new")
  await expect(page.getByText("Adding a database needs an administrator")).toBeVisible()
  await expect(page.locator("[data-slot=flow-panel]")).toHaveCount(0)

  await page.goto("/databases/1")
  // Beside the version it is about, before where the server runs.
  await expect(page.locator("[data-slot=host-identity]")).toContainText(
    /PostgreSQL 16\.4\s*·\s*supported until Nov 2028/,
  )
  await page.goto("/databases/1/settings")
  await expect(page.locator("section#connection")).toContainText("supported until Nov 2028")
})

test("Home and General say a release's end of life beside its version, and nothing where it is unknown", async ({
  page,
}) => {
  await mockDatabases(page, {
    summaries: {
      1: {
        versionNumber: "13.14",
        version: "PostgreSQL 13.14",
        endOfLife: eol("PostgreSQL", "13", "2025-11-13", true),
      },
    },
  })
  await page.goto("/databases/1")
  const identity = page.locator("[data-slot=host-identity]")
  await expect(identity).toContainText("PostgreSQL 13.14")
  await expect(identity).toContainText("end of life since Nov 13, 2025")
  await page.goto("/databases/1/settings")
  await expect(page.locator("section#connection")).toContainText("end of life since Nov 13, 2025")

  // Redis has no dates in the table: its version stands alone.
  await page.goto("/databases/4")
  await expect(page.locator("[data-slot=host-identity]")).toContainText("Redis 7.4.1")
  await expect(page.locator("[data-slot=host-identity]")).not.toContainText(/supported|end of life/)
})

test("the catalogue and a build in progress fit a phone without sideways scrolling", async ({
  page,
}) => {
  await page.setViewportSize({ width: 390, height: 844 })
  await mockCatalogue(page)
  await page.goto("/databases/new?engine=mysql")
  await panel(page)
    .getByRole("button", { name: /Older versions/ })
    .click()
  await expect(panel(page)).toContainText("End of life since Apr 30, 2026")
  expect(await noSideScroll(page)).toBe(false)

  await page.goto("/databases/new?engine=postgres-full")
  await panel(page).getByRole("button", { name: "Create", exact: true }).click()
  await expect(panel(page).getByText(/Setting up postgresql-18-postgis-3/)).toBeVisible()
  expect(await noSideScroll(page)).toBe(false)

  await mockDatabases(page, {
    summaries: { 1: { endOfLife: eol("PostgreSQL", "16", "2028-11-09", false) } },
  })
  await page.goto(`/databases/${SHOP.id}`)
  await expect(page.locator("[data-slot=host-identity]")).toContainText("supported until Nov 2028")
  expect(await noSideScroll(page)).toBe(false)
})

test("screenshots of the refreshed catalogue @shots", async ({ page }) => {
  test.skip(!process.env.JD_SHOTS, "JD_SHOTS names the folder the screenshots are saved in")
  const shots = process.env.JD_SHOTS
  await page.emulateMedia({ reducedMotion: "reduce" })
  await page.setViewportSize({ width: 1440, height: 900 })

  await mockCatalogue(page)
  await page.goto("/databases/new?engine=postgres")
  await expect(panel(page)).toContainText("Supported until Nov 2030")
  await page.screenshot({ path: `${shots}/catalogue-postgres-18-1440.png` })

  await page.goto("/databases/new?engine=mysql")
  await panel(page)
    .getByRole("button", { name: /Older versions/ })
    .click()
  await panel(page).getByRole("radio", { name: "8.0", exact: true }).click()
  await expect(panel(page)).toContainText("End of life since Apr 30, 2026")
  await page.screenshot({ path: `${shots}/catalogue-older-versions-1440.png` })

  await page.goto("/databases/new?engine=postgres-full")
  await expect(panel(page).getByText("Its image is built here first")).toBeVisible()
  await page.screenshot({ path: `${shots}/catalogue-full-template-1440.png` })

  await panel(page).getByRole("button", { name: "Create", exact: true }).click()
  await expect(panel(page).getByText(/Setting up postgresql-18-postgis-3/)).toBeVisible()
  await page.screenshot({ path: `${shots}/catalogue-full-building-1440.png` })

  await mockDatabases(page, {
    summaries: {
      1: {
        versionNumber: "14.11",
        version: "PostgreSQL 14.11",
        endOfLife: eol("PostgreSQL", "14", "2026-11-12", false),
      },
    },
  })
  await mockHealthyHome(page)
  await page.goto("/databases/1")
  await expect(page.locator("[data-slot=host-identity]")).toContainText("supported until Nov 2026")
  await expect(page.getByText("This server's statistics are not available")).toHaveCount(0)
  await expect(page.getByText(/Could not read/)).toHaveCount(0)
  await page.screenshot({ path: `${shots}/home-support-status-1440.png` })
  await page.goto("/databases/1/settings")
  await expect(page.locator("section#connection")).toContainText("supported until Nov 2026")
  await page.screenshot({ path: `${shots}/general-support-status-1440.png` })

  await page.setViewportSize({ width: 390, height: 844 })
  await mockCatalogue(page)
  await page.goto("/databases/new?engine=mysql")
  await panel(page)
    .getByRole("button", { name: /Older versions/ })
    .click()
  await expect(panel(page)).toContainText("End of life since Apr 30, 2026")
  await panel(page).scrollIntoViewIfNeeded()
  await page.screenshot({ path: `${shots}/catalogue-older-versions-390.png` })
})
