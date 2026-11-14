import { expect as baseExpect, test, type Locator, type Page } from "@playwright/test"
import { SHOP, mockDatabases, summaryOf, type DatabaseMock } from "./database-fixture"

/**
 * A database's Version page: which release its server runs, and moving it to
 * a newer one.
 *
 * The claims are the ones the page makes to its reader. The checks run first
 * and change nothing, and what they found is listed line by line; the upgrade
 * is offered only when nothing stops it, and is confirmed with what it does
 * to the server before it begins. Each of the three is a job the page follows
 * by its id, and one found running is shown on arrival. Afterwards the old
 * server is kept, and the two ways out are each asked for: finishing, which
 * removes it, and reverting, which takes a dump of the new server first
 * unless the reader says otherwise. The page is an administrator's, a
 * protected connection is checked and not upgraded, and a server the page
 * cannot move says why.
 */

const expect = baseExpect.configure({ timeout: 15_000 })
test.describe.configure({ timeout: 60_000 })

const minutesAgo = (minutes: number) => new Date(Date.now() - minutes * 60_000).toISOString()

type Reply = { status: number; body: unknown }
type Answer = unknown | ((call: number, body: unknown) => unknown)
const isReply = (value: unknown): value is Reply =>
  typeof value === "object" && value !== null && "status" in value && "body" in value

/** What `GET /databases/1` says the server is: the release it answers as. */
const serverAt = (version: string) =>
  summaryOf(SHOP, { versionNumber: version, version: `PostgreSQL ${version}` })

const OFFERED = {
  offered: true,
  engine: "postgres",
  label: "PostgreSQL",
  container: "shop-db",
  version: "16",
  image: "postgres:16-alpine",
  volume: "shop-db-data",
  targets: [{ version: "17", image: "postgres:17-alpine" }],
}

const check = (id: string, verdict: string, title: string, detail: string, items?: string[]) => ({
  id,
  verdict,
  title,
  detail,
  ...(items ? { items } : {}),
})

const CHECKS = [
  check("version", "ok", "Version", "PostgreSQL 16.4 to 17.6."),
  check(
    "extensions",
    "ok",
    "Extensions",
    "Each is created again in the new server at the version its image carries.",
    ["pg_stat_statements 1.10 → 1.11", "pg_trgm 1.6", "hstore 1.8"],
  ),
  check(
    "disk",
    "ok",
    "Disk space",
    "The databases take 46.0 MB. 38.2 GB is free for the new volume and 38.2 GB for the safety dump.",
  ),
  check(
    "names",
    "ok",
    "Containers and volume",
    "The new server takes the name shop-db and its port. The old one is kept, stopped, as shop-db-pg16 with its data until you finish or revert; the new data goes in shop-db-pg17-data.",
    ["shop-db-pg16", "shop-db-pg17-data"],
  ),
  check(
    "libraries",
    "ok",
    "Preloaded libraries",
    "The new image has every library in shared_preload_libraries.",
    ["pg_stat_statements"],
  ),
  check(
    "sessions",
    "warning",
    "Connected sessions",
    "3 sessions are connected now. They are disconnected when the copy starts, the server takes reads only until the new one takes its place, and applications have to reconnect after it.",
  ),
  check(
    "parameters",
    "ok",
    "Parameters",
    "2 parameters set from the Parameters page are carried to the new server.",
    ["work_mem", "log_min_duration_statement"],
  ),
]

const report = (over: Record<string, unknown> = {}) => ({
  from: "16",
  to: "17",
  server: "16.4",
  target: "17.6",
  image: "postgres:17-alpine",
  bytes: 48_234_496,
  databases: [
    { name: "postgres", bytes: 7_598_639, template: false },
    { name: "shop_main", bytes: 40_635_857, template: false },
  ],
  checks: CHECKS,
  ready: true,
  checkedAt: minutesAgo(1),
  ...over,
})

const BLOCKED = report({
  ready: false,
  checks: [
    CHECKS[0],
    check(
      "extensions",
      "blocker",
      "Extensions",
      "The new image does not carry these, so their databases cannot be restored on it. Drop them first if they are unused, or upgrade with an image that has them.",
      ["postgis in shop_main"],
    ),
    ...CHECKS.slice(2),
  ],
})

const KEPT = {
  ...OFFERED,
  offered: false,
  version: "17",
  image: "postgres:17-alpine",
  volume: "shop-db-pg17-data",
  targets: [],
  reason: "Finish or revert the upgrade from 16 first.",
  window: {
    id: 3,
    state: "upgraded",
    engine: "postgres",
    fromVersion: "16",
    toVersion: "17",
    fromImage: "postgres:16-alpine",
    toImage: "postgres:17-alpine",
    container: "shop-db",
    previous: "shop-db-pg16",
    previousState: "exited",
    previousVolume: "shop-db-data",
    volume: "shop-db-pg17-data",
    safetyDump: "shop_main-20261017-091204.dump",
    startedBy: "operator",
    startedAt: minutesAgo(47),
    upgradedAt: minutesAgo(41),
  },
}

const job = (id: string, kind: string, status: string, title: string) => ({
  id,
  kind: `database.transfer.1.${kind}`,
  title,
  target: "shop",
  status,
  exitCode: 0,
  startedAt: minutesAgo(1),
  endedAt: status === "running" ? undefined : minutesAgo(0),
  startedBy: "operator",
  lines: 4,
})

const line = (seq: number, stream: string, text: string) => ({
  seq,
  stream,
  text,
  at: minutesAgo(0),
})

const CHECK_LINES = [
  line(1, "status", "Starting PostgreSQL 17 beside it to see what it carries"),
  line(2, "status", "Reading shop-db and PostgreSQL 17"),
]

const UPGRADE_LINES = [
  line(1, "status", "Checking shop-db before anything changes"),
  line(2, "status", "Starting PostgreSQL 17 beside it as shop-db-pg17-upgrade"),
  line(3, "status", "Dumping shop_main as a safety copy"),
  line(4, "status", "shop-db takes reads only until the cutover; 3 sessions were disconnected"),
  line(5, "status", "Copying the roles"),
  line(6, "status", "Copying shop_main (38.8 MB)"),
  line(7, "stdout", "shop_main: 21.4 MB copied"),
]

/** A job read while it runs, as many times as `running` says, then as ended with its result line. */
const jobThatEnds =
  (
    id: string,
    kind: string,
    title: string,
    lines: unknown[],
    result: unknown,
    done = () => {},
    running = 1,
  ) =>
  (call: number) => {
    if (call < running) return { job: job(id, kind, "running", title), lines }
    done()
    return {
      job: job(id, kind, "succeeded", title),
      lines: [...lines, line(99, "result", JSON.stringify(result))],
    }
  }

/** The section's fixture, with the upgrade's routes and the jobs answered over it. */
async function mockUpgrade(
  page: Page,
  answers: Record<string, Answer>,
  options: DatabaseMock = {},
) {
  const mock = await mockDatabases(page, options)
  const calls = new Map<string, number>()
  const sent: { request: string; body: unknown }[] = []
  const seen: string[] = []
  await page.route("**/api/v1/**", async (route) => {
    const url = new URL(route.request().url())
    const path = url.pathname.replace(/^\/api\/v1/, "")
    const method = route.request().method()
    const key = `${method} ${path}`
    seen.push(key)
    let body: unknown
    if (method !== "GET") {
      body = route.request().postDataJSON()
      sent.push({ request: key, body })
    }
    if (!Object.hasOwn(answers, key)) return route.fallback()
    const call = calls.get(key) ?? 0
    calls.set(key, call + 1)
    const answer = answers[key]
    const value =
      typeof answer === "function"
        ? (answer as (call: number, body: unknown) => unknown)(call, body)
        : answer
    if (isReply(value)) return route.fulfill({ status: value.status, json: value.body })
    return route.fulfill({ json: value }).catch(() => undefined)
  })
  return { ...mock, sent, seen }
}

async function visit(page: Page, path: string) {
  const listed = page.waitForResponse(
    (response) => new URL(response.url()).pathname === "/api/v1/databases/",
    { timeout: 45_000 },
  )
  await page.goto(path)
  await listed
}

const PAGE = "/databases/1/settings/version"
const section = (page: Page, id: string) => page.locator(`section#${id}`)
const report_ = (page: Page) => page.getByRole("list", { name: "What the checks found" })
const progress = (page: Page) => page.locator("[data-slot=transfer-progress]")
const dialog = (page: Page) => page.getByRole("dialog")
/**
 * Waits for what is arriving on a surface to have arrived. A running job's
 * beam and the wire of a server being copied loop for as long as the work
 * runs, and are the state being shown rather than a frame caught mid-way.
 */
const settled = (surface: Locator) =>
  surface.evaluate((el) =>
    Promise.all(
      el
        .getAnimations({ subtree: true })
        .filter((animation) => animation.effect?.getComputedTiming().iterations !== Infinity)
        .map((animation) => animation.finished),
    ),
  )
/** Scrolls whatever holds a surface so its top is at the top of the view. */
const atTop = (surface: Locator) =>
  surface.evaluate((el) => el.scrollIntoView({ block: "start", behavior: "instant" }))
const noSideways = (page: Page) =>
  page.evaluate(() =>
    [document.documentElement, ...document.querySelectorAll("[data-slot=page]")]
      .map((el) => el.parentElement ?? el)
      .some((el) => el.scrollWidth > el.clientWidth + 1),
  )

/** Checks 17 from the page, and answers with the report given. */
function checking(found: unknown) {
  return {
    "POST /databases/1/upgrade/check": job(
      "c1",
      "major-upgrade-check",
      "running",
      "Check shop for PostgreSQL 17",
    ),
    "GET /jobs/c1": jobThatEnds(
      "c1",
      "major-upgrade-check",
      "Check shop for PostgreSQL 17",
      CHECK_LINES,
      found,
    ),
  }
}

test("the page reads the release, checks the newer one and lists what the checks found", async ({
  page,
}) => {
  const server = await mockUpgrade(page, {
    "GET /databases/1/upgrade": OFFERED,
    ...checking(report()),
  })
  await visit(page, PAGE)

  await expect(section(page, "version")).toContainText("PostgreSQL 16.4")
  await expect(section(page, "version")).toContainText("postgres:16-alpine")
  await expect(section(page, "version").getByRole("link", { name: "shop-db" })).toHaveAttribute(
    "href",
    "/docker/containers/shop-db",
  )
  // The server and the release it would become, drawn as the way it goes.
  const picture = section(page, "version").getByRole("list", {
    name: "The server and its newer release",
  })
  await expect(picture).toContainText("PostgreSQL 16.4")
  await expect(picture).toContainText("PostgreSQL 17")
  await expect(picture).toContainText("postgres:17-alpine")
  // What it runs from and where its data is are the captions of the server that runs.
  await expect(picture).toContainText("image postgres:16-alpine")
  await expect(picture).toContainText("data shop-db-data")
  // Between them, the four things an upgrade does, each a mark with a few words, none taken yet.
  const steps = picture.locator("[data-step]")
  await expect(steps).toHaveCount(4)
  await expect(steps).toContainText(["Dump", "New server", "Copy", "Switch"])
  await expect(picture.locator("[data-step][data-state=ahead]")).toHaveCount(4)
  await expect(picture.locator("[data-step=start]")).toContainText("PostgreSQL 17 starts beside it")
  const upgrade = section(page, "upgrade")
  await expect(upgrade).toContainText("Not checked yet")
  // The sentences about what it does are the confirmation's, not the page's.
  await expect(page.locator("[data-slot=page]")).not.toContainText("takes reads only")
  await expect(upgrade.getByRole("listitem")).toHaveCount(0)
  // Nothing is offered to run before the checks have said it can.
  await expect(upgrade.getByRole("button", { name: /^Upgrade to/ })).toHaveCount(0)

  await upgrade.getByRole("button", { name: "Check PostgreSQL 17" }).click()
  await expect(report_(page)).toBeVisible()
  await expect(report_(page).getByRole("listitem")).toHaveCount(CHECKS.length)
  await expect(report_(page).locator("[data-check=sessions]")).toHaveAttribute(
    "data-verdict",
    "warning",
  )
  await expect(report_(page).locator("[data-check=extensions]")).toContainText(
    "pg_stat_statements 1.10 → 1.11",
  )
  await expect(upgrade).toContainText("ready, 1 thing to know")
  // The copy's size is known once it has been checked.
  await expect(picture.locator("[data-step=copy]")).toContainText(
    /[\d.]+ [kKMG]?B, reads only meanwhile/,
  )
  // The console has said its lines; the report says them as checks.
  await expect(progress(page)).toHaveCount(0)
  await expect(upgrade.getByRole("button", { name: "Upgrade to PostgreSQL 17" })).toBeEnabled()
  expect(server.sent).toEqual([
    { request: "POST /databases/1/upgrade/check", body: { version: "17" } },
  ])
})

test("an upgrade is confirmed with what it does, followed to its end, and leaves the old server kept", async ({
  page,
}) => {
  let state: unknown = OFFERED
  const server = await mockUpgrade(page, {
    "GET /databases/1": () => serverAt(state === KEPT ? "17.6" : "16.4"),
    "GET /databases/1/upgrade": () => state,
    ...checking(report()),
    "POST /databases/1/upgrade": () => {
      state = {
        ...OFFERED,
        offered: false,
        reason: "An upgrade is running.",
        job: job("u1", "major-upgrade", "running", "Upgrade shop to PostgreSQL 17"),
      }
      return job("u1", "major-upgrade", "running", "Upgrade shop to PostgreSQL 17")
    },
    "GET /jobs/u1": jobThatEnds(
      "u1",
      "major-upgrade",
      "Upgrade shop to PostgreSQL 17",
      UPGRADE_LINES,
      {
        from: "16",
        to: "17",
        server: "17.6",
        container: "shop-db",
        previous: "shop-db-pg16",
        volume: "shop-db-pg17-data",
        safetyDump: "shop_main-20261017-091204.dump",
        databases: 2,
        tables: 14,
        rows: 182_311,
        summary: "2 databases, 14 tables and 182311 rows match",
      },
      () => {
        state = KEPT
      },
      3,
    ),
  })
  await visit(page, PAGE)
  await section(page, "upgrade").getByRole("button", { name: "Check PostgreSQL 17" }).click()
  await section(page, "upgrade").getByRole("button", { name: "Upgrade to PostgreSQL 17" }).click()

  await expect(dialog(page)).toContainText("Upgrade to PostgreSQL 17")
  await expect(dialog(page)).toContainText("A dump of shop_main is taken first")
  await expect(dialog(page)).toContainText("takes reads only")
  await expect(dialog(page)).toContainText("PostgreSQL 16.4 is kept, stopped, with its data")
  await dialog(page).getByRole("button", { name: "Upgrade", exact: true }).click()

  await expect(progress(page)).toHaveAttribute(
    "aria-label",
    "Upgrade shop to PostgreSQL 17, running",
  )
  await expect(progress(page)).toContainText("shop_main: 21.4 MB copied")
  await expect(progress(page)).toHaveAttribute(
    "aria-label",
    "Upgrade shop to PostgreSQL 17, succeeded",
  )
  await expect(progress(page)).toContainText("2 databases, 14 tables and 182311 rows match")

  const pair = section(page, "version").getByRole("list", { name: "The server and the one kept" })
  await expect(pair).toContainText("shop-db-pg16")
  // Each server's data volume is its own caption.
  await expect(pair.locator("[data-slot=kept-server]")).toContainText("data shop-db-data")
  await expect(pair).toContainText("data shop-db-pg17-data")
  await expect(pair.getByRole("link", { name: "shop-db-pg16" })).toHaveAttribute(
    "href",
    "/docker/containers/shop-db-pg16",
  )
  const kept = section(page, "kept")
  await expect(kept).toContainText("PostgreSQL 16 is kept")
  await expect(kept).toContainText("Upgraded 41m ago by operator")
  await expect(kept.getByRole("link", { name: "shop_main-20261017-091204.dump" })).toHaveAttribute(
    "href",
    "/databases/1/backups",
  )
  await expect(kept.getByRole("button", { name: "Finish the upgrade" })).toBeVisible()
  await expect(kept.getByRole("button", { name: "Revert to PostgreSQL 16…" })).toBeVisible()
  expect(server.sent.map((one) => one.request)).toEqual([
    "POST /databases/1/upgrade/check",
    "POST /databases/1/upgrade",
  ])
  expect(server.sent[1].body).toEqual({ version: "17" })
})

test("a check that finds what stops the upgrade offers no upgrade, and says to fix it first", async ({
  page,
}) => {
  await mockUpgrade(page, { "GET /databases/1/upgrade": OFFERED, ...checking(BLOCKED) })
  await visit(page, PAGE)
  const upgrade = section(page, "upgrade")
  await upgrade.getByRole("button", { name: "Check PostgreSQL 17" }).click()
  await expect(report_(page).locator("[data-check=extensions]")).toHaveAttribute(
    "data-verdict",
    "blocker",
  )
  await expect(report_(page).locator("[data-check=extensions]")).toContainText(
    "postgis in shop_main",
  )
  await expect(upgrade).toContainText("1 thing to fix first")
  await expect(upgrade).toContainText("One check stops the upgrade.")
  await expect(upgrade.getByRole("button", { name: /^Upgrade to/ })).toHaveCount(0)
  await expect(upgrade.getByRole("button", { name: "Check again" })).toBeEnabled()
})

test("the kept server is finished after being asked, and reverted with a dump of the new one first", async ({
  page,
}) => {
  const server = await mockUpgrade(page, {
    "GET /databases/1": serverAt("17.6"),
    "GET /databases/1/upgrade": KEPT,
    "POST /databases/1/upgrade/complete": {
      previous: "shop-db-pg16",
      volumes: ["shop-db-data"],
      warnings: [],
      detail: "PostgreSQL 17 is kept; shop-db-pg16 was removed with its data",
    },
    "POST /databases/1/upgrade/revert": job(
      "r1",
      "major-upgrade-revert",
      "running",
      "Revert shop to PostgreSQL 16",
    ),
    "GET /jobs/r1": {
      job: job("r1", "major-upgrade-revert", "running", "Revert shop to PostgreSQL 16"),
      lines: [line(1, "status", "Dumping shop_main on PostgreSQL 17 first")],
    },
  })
  await visit(page, PAGE)
  const kept = section(page, "kept")
  await kept.getByRole("button", { name: "Finish the upgrade" }).click()
  await expect(dialog(page)).toContainText("Removes shop-db-pg16 and its data volume shop-db-data")
  await expect(dialog(page)).toContainText("After this the upgrade cannot be reverted.")
  await dialog(page).getByRole("button", { name: "Cancel" }).click()
  await expect(dialog(page)).toHaveCount(0)
  expect(server.sent).toEqual([])

  await kept.getByRole("button", { name: "Revert to PostgreSQL 16…" }).click()
  await expect(dialog(page)).toContainText("What was written to PostgreSQL 17 is discarded")
  await expect(
    dialog(page).getByRole("switch", { name: /Take a dump of shop_main on PostgreSQL 17 first/ }),
  ).toBeChecked()
  await dialog(page).getByRole("button", { name: "Revert", exact: true }).click()
  await expect(progress(page)).toHaveAttribute(
    "aria-label",
    "Revert shop to PostgreSQL 16, running",
  )
  expect(server.sent).toEqual([
    { request: "POST /databases/1/upgrade/revert", body: { dumpFirst: true } },
  ])

  // One operation at a time: finishing waits for the revert.
  await expect(kept.getByRole("button", { name: "Finish the upgrade" })).toBeDisabled()
})

test("finishing removes the old server and says what stays", async ({ page }) => {
  let state: unknown = KEPT
  const server = await mockUpgrade(page, {
    "GET /databases/1": serverAt("17.6"),
    "GET /databases/1/upgrade": () => state,
    "POST /databases/1/upgrade/complete": () => {
      state = {
        ...KEPT,
        window: undefined,
        reason: "PostgreSQL 17 is the newest release Databases offers.",
        last: { ...KEPT.window, state: "finished", endedAt: minutesAgo(0) },
      }
      return {
        previous: "shop-db-pg16",
        volumes: ["shop-db-data"],
        warnings: [],
        detail: "PostgreSQL 17 is kept; shop-db-pg16 was removed with its data",
      }
    },
  })
  await visit(page, PAGE)
  await section(page, "kept").getByRole("button", { name: "Finish the upgrade" }).click()
  await dialog(page).getByRole("button", { name: "Remove PostgreSQL 16" }).click()
  await expect(section(page, "kept")).toHaveCount(0)
  await expect(section(page, "version")).toContainText(
    "PostgreSQL 17 is the newest release Databases offers.",
  )
  expect(server.sent).toEqual([{ request: "POST /databases/1/upgrade/complete", body: {} }])
})

test("an upgrade already running is shown on arrival, and nothing else begins beside it", async ({
  page,
}) => {
  const running = job("u9", "major-upgrade", "running", "Upgrade shop to PostgreSQL 17")
  let phase = "prepare"
  await mockUpgrade(page, {
    "GET /databases/1/upgrade": () => ({
      ...OFFERED,
      offered: false,
      reason: "An upgrade is running.",
      window: { ...KEPT.window, state: "running", phase, upgradedAt: undefined },
      job: running,
    }),
    "GET /jobs/u9": { job: running, lines: UPGRADE_LINES },
  })
  await visit(page, PAGE)
  await expect(progress(page)).toHaveAttribute(
    "aria-label",
    "Upgrade shop to PostgreSQL 17, running",
  )
  await expect(
    section(page, "upgrade").getByRole("button", { name: "Check PostgreSQL 17" }),
  ).toBeDisabled()

  // The picture says where it has got to: the dump and the new server first.
  const picture = section(page, "version").getByRole("list", {
    name: "The server and its newer release",
  })
  await expect(picture).toContainText("Being copied into")
  await expect(picture.locator("[data-step=dump]")).toHaveAttribute("data-state", "running")
  await expect(picture.locator("[data-step=copy]")).toHaveAttribute("data-state", "ahead")
  // The record is read again while the job runs, and the steps follow it.
  phase = "copy"
  await expect(picture.locator("[data-step=dump]")).toHaveAttribute("data-state", "done")
  await expect(picture.locator("[data-step=copy]")).toHaveAttribute("data-state", "running")
  await expect(picture.locator("[data-step=dump]")).toContainText("Dump, done")
  phase = "cutover"
  await expect(picture.locator("[data-step=switch]")).toHaveAttribute("data-state", "running")
  await expect(picture.locator("[data-step=copy]")).toHaveAttribute("data-state", "done")
})

test("the picture holds its place while the release is read, wide and on a phone", async ({
  page,
}) => {
  for (const width of [1280, 390]) {
    await page.setViewportSize({ width, height: 1000 })
    await page.unrouteAll({ behavior: "ignoreErrors" })
    await mockUpgrade(page, { "GET /databases/1/upgrade": OFFERED })
    await visit(page, PAGE)
    const picture = section(page, "version").getByRole("list", {
      name: "The server and its newer release",
    })
    await expect(picture.locator("[data-step]")).toHaveCount(4)
    // Every step is on the screen, with its words.
    for (const step of await picture.locator("[data-step]").all()) {
      const box = await step.boundingBox()
      expect(box && box.x >= 0 && box.x + box.width <= width).toBe(true)
    }
    if (width === 1280) {
      // Wide, the six marks stand on one line.
      const tops = await picture.locator("[data-step] > div:first-child").evaluateAll((all) =>
        all.map((one) => {
          const box = one.getBoundingClientRect()
          return Math.round(box.top + box.height / 2)
        }),
      )
      expect(new Set(tops).size).toBe(1)
    }
    expect(await noSideways(page)).toBe(false)
  }
})

test("a server the page cannot move says why, and offers nothing to run", async ({ page }) => {
  await mockUpgrade(page, {
    "GET /databases/1/upgrade": {
      offered: false,
      targets: [],
      reason:
        "shop-db belongs to the compose project shop; change its image in the compose file instead.",
    },
  })
  await visit(page, PAGE)
  await expect(section(page, "version")).toContainText("compose project shop")
  await expect(section(page, "upgrade")).toHaveCount(0)
  await expect(page.getByRole("button", { name: /^Check/ })).toHaveCount(0)
})

test("a failed upgrade is said where the next one is begun, with what was kept", async ({
  page,
}) => {
  await mockUpgrade(page, {
    "GET /databases/1/upgrade": {
      ...OFFERED,
      last: {
        ...KEPT.window,
        state: "failed",
        endedAt: minutesAgo(12),
        error:
          "the copy does not match the old server: shop_main: public.orders has 9120 rows, not 9121",
      },
    },
  })
  await visit(page, PAGE)
  const upgrade = section(page, "upgrade")
  await expect(upgrade).toContainText("The upgrade to PostgreSQL 17 failed 12m ago and was undone")
  await expect(upgrade).toContainText("public.orders has 9120 rows, not 9121")
  await expect(upgrade).toContainText("The safety dump shop_main-20261017-091204.dump is kept")
})

test("a reading that fails says so and is tried again", async ({ page }) => {
  await mockUpgrade(page, {
    "GET /databases/1/upgrade": (call: number) =>
      call === 0
        ? {
            status: 502,
            body: { error: { code: "docker_failed", message: "Docker did not answer" } },
          }
        : OFFERED,
  })
  await visit(page, PAGE)
  const version = section(page, "version")
  await expect(version).toContainText("Could not read which release it runs")
  await version.getByRole("button", { name: /Try again|Retry/ }).click()
  await expect(section(page, "upgrade")).toContainText("Not checked yet")
})

test("a protected connection is checked and not upgraded", async ({ page }) => {
  await mockUpgrade(
    page,
    { "GET /databases/1/upgrade": OFFERED, ...checking(report()) },
    {
      rows: { 1: { readOnly: true } },
    },
  )
  await visit(page, PAGE)
  const upgrade = section(page, "upgrade")
  await upgrade.getByRole("button", { name: "Check PostgreSQL 17" }).click()
  await expect(report_(page)).toBeVisible()
  await expect(upgrade.getByRole("button", { name: /^Upgrade to/ })).toHaveCount(0)
  await expect(upgrade).toContainText("This connection is protected")
})

test("a role that may only read is not offered the page, and its address says whose it is", async ({
  page,
}) => {
  const server = await mockUpgrade(page, { "GET /databases/1/upgrade": OFFERED }, { viewer: true })
  await visit(page, "/databases/1/settings")
  const rail = page.getByRole("navigation", { name: "Sidebar" })
  await expect(rail.getByRole("link", { name: "General", exact: true })).toBeVisible()
  await expect(rail.getByRole("link", { name: "Version", exact: true })).toHaveCount(0)
  await visit(page, PAGE)
  await expect(page.getByText("Upgrades are an administrator's")).toBeVisible()
  expect(server.seen).not.toContain("GET /databases/1/upgrade")
})

test("an engine the page cannot upgrade has no Version page", async ({ page }) => {
  await mockUpgrade(page, {})
  await visit(page, "/databases/4/settings")
  const rail = page.getByRole("navigation", { name: "Sidebar" })
  await expect(rail.getByRole("link", { name: "General", exact: true })).toBeVisible()
  await expect(rail.getByRole("link", { name: "Version", exact: true })).toHaveCount(0)
  await visit(page, "/databases/4/settings/version")
  await expect(page.getByText(/Redis has no release to upgrade from here/)).toBeVisible()
})

test("at a phone's width nothing scrolls sideways, with the report and with the kept server", async ({
  page,
}) => {
  await page.setViewportSize({ width: 390, height: 844 })
  let state: unknown = OFFERED
  await mockUpgrade(page, {
    "GET /databases/1": () => serverAt(state === KEPT ? "17.6" : "16.4"),
    "GET /databases/1/upgrade": () => state,
    ...checking(BLOCKED),
  })
  await visit(page, PAGE)
  await section(page, "upgrade").getByRole("button", { name: "Check PostgreSQL 17" }).click()
  await expect(report_(page)).toBeVisible()
  expect(await noSideways(page)).toBe(false)
  state = KEPT
  await visit(page, PAGE)
  await expect(section(page, "kept")).toBeVisible()
  expect(await noSideways(page)).toBe(false)
})

test("the Version page, in each of its states @shots", async ({ page }) => {
  test.skip(!process.env.JD_SHOTS, "JD_SHOTS is unset")
  test.setTimeout(300_000)
  const shot = (name: string, width: number) =>
    `${process.env.JD_SHOTS}/after-version-${name}-${width}.png`

  for (const width of [1280, 390]) {
    await page.setViewportSize({ width, height: width === 390 ? 844 : 900 })
    await page.unrouteAll({ behavior: "ignoreErrors" })
    let state: unknown = OFFERED
    let found: unknown = report()
    await mockUpgrade(page, {
      "GET /databases/1": () => serverAt(state === KEPT ? "17.6" : "16.4"),
      "GET /databases/1/upgrade": () => state,
      "POST /databases/1/upgrade/check": job(
        "c1",
        "major-upgrade-check",
        "running",
        "Check shop for PostgreSQL 17",
      ),
      "GET /jobs/c1": (call: number) =>
        jobThatEnds(
          "c1",
          "major-upgrade-check",
          "Check shop for PostgreSQL 17",
          CHECK_LINES,
          found,
        )(call),
      "POST /databases/1/upgrade": () => {
        state = {
          ...OFFERED,
          offered: false,
          reason: "An upgrade is running.",
          window: { ...KEPT.window, state: "running", phase: "copy", upgradedAt: undefined },
          job: job("u1", "major-upgrade", "running", "Upgrade shop to PostgreSQL 17"),
        }
        return job("u1", "major-upgrade", "running", "Upgrade shop to PostgreSQL 17")
      },
      "GET /jobs/u1": {
        job: job("u1", "major-upgrade", "running", "Upgrade shop to PostgreSQL 17"),
        lines: UPGRADE_LINES,
      },
    })

    // Not checked yet: the release, the four steps, and Check.
    await visit(page, PAGE)
    const upgrade = section(page, "upgrade")
    await expect(upgrade).toContainText("Not checked yet")
    await settled(page.locator("[data-slot=page]").first())
    await page.screenshot({ path: shot("full", width), fullPage: true })

    // Checked, ready, with one thing to know.
    await upgrade.getByRole("button", { name: "Check PostgreSQL 17" }).click()
    await expect(report_(page)).toBeVisible()
    await settled(page.locator("[data-slot=page]").first())
    await page.screenshot({ path: shot("report", width), fullPage: true })

    // The upgrade asked for, over the page from its top.
    await atTop(section(page, "version"))
    await upgrade.getByRole("button", { name: "Upgrade to PostgreSQL 17" }).click()
    await expect(dialog(page)).toBeVisible()
    await settled(dialog(page))
    await page.screenshot({ path: shot("confirm", width) })

    // The upgrade under way, at its copy.
    await dialog(page).getByRole("button", { name: "Upgrade", exact: true }).click()
    const underWay = page.getByRole("region", { name: "Upgrade shop to PostgreSQL 17, running" })
    await expect(underWay).toContainText("shop_main: 21.4 MB copied")
    await expect(dialog(page)).toHaveCount(0)
    await expect(
      section(page, "version").locator("[data-step=copy][data-state=running]"),
    ).toBeVisible()
    await atTop(section(page, "version"))
    await settled(underWay)
    await page.screenshot({ path: shot("upgrading", width), fullPage: true })

    // A check that stops it.
    state = OFFERED
    found = BLOCKED
    await visit(page, PAGE)
    await section(page, "upgrade").getByRole("button", { name: "Check PostgreSQL 17" }).click()
    await expect(report_(page).locator("[data-check=extensions]")).toHaveAttribute(
      "data-verdict",
      "blocker",
    )
    await settled(page.locator("[data-slot=page]").first())
    await page.screenshot({ path: shot("blocked", width), fullPage: true })

    // Upgraded, with the old server kept.
    state = KEPT
    await visit(page, PAGE)
    await expect(section(page, "kept")).toBeVisible()
    await settled(page.locator("[data-slot=page]").first())
    await page.screenshot({ path: shot("kept", width), fullPage: true })

    // A server with nowhere to go, and one the page cannot move.
    state = { ...KEPT, window: undefined, reason: "PostgreSQL 17 is the newest release offered." }
    await visit(page, PAGE)
    await expect(section(page, "version")).toContainText("newest release")
    await settled(page.locator("[data-slot=page]").first())
    await page.screenshot({ path: shot("newest", width), fullPage: true })
  }
})
