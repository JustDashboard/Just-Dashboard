import { expect as baseExpect, test, type Page } from "@playwright/test"
import { SHOP, mockDatabases, pitrOff, type DatabaseMock } from "./database-fixture"

/**
 * Point-in-time recovery on a PostgreSQL server's Backups page.
 *
 * The claims are the ones the panel makes to its reader: a server that does
 * not archive says what archiving would give it and, to the role that may,
 * offers to set it up — showing the command line the server will restart
 * with before anything runs. A server that archives says which moments it
 * can go back to, how archiving is going (in WAL-G's own words when it
 * fails), and the base backups and restore points those moments rest on. A
 * recovery is a new server beside the old one, planned by the server before
 * it starts and followed to its end; a recovered copy is switched in only
 * after an ordinary confirmation. A role that may only read sees all of it
 * and none of the commands.
 */

const expect = baseExpect.configure({ timeout: 15_000 })
test.describe.configure({ timeout: 60_000 })

const NOW = "2026-10-17T09:00:00Z"
const ago = (minutes: number) => new Date(Date.now() - minutes * 60_000).toISOString()

type Reply = { status: number; body: unknown }
const isReply = (value: unknown): value is Reply =>
  typeof value === "object" && value !== null && "status" in value && "body" in value
type Answer = unknown | ((call: number, body: unknown) => unknown)

const backup = (name: string, minutesAgo: number, size: number) => ({
  name,
  startedAt: ago(minutesAgo + 2),
  finishedAt: ago(minutesAgo),
  walFile: name.replace("base_", ""),
  pgVersion: 170006,
  size,
  dataSize: size * 6,
})

const ARCHIVE = {
  id: 3,
  container: "shop-db",
  connectionId: 1,
  stream: "shop-db-20261010-030000",
  targetKind: "s3",
  target: {
    bucket: "acme-backups",
    endpoint: "https://s3.eu-central-1.amazonaws.com",
    prefix: "postgres",
  },
  hasCredentials: true,
  keyFingerprint: "3CCE 654E 2949 3AEB 611E  ED3C 4E40 4771 E1F2 E93C",
  schedule: "0 3 * * *",
  retentionDays: 7,
  image: "postgres:17-alpine",
  createdAt: "2026-10-10T03:00:00Z",
  createdBy: "operator",
  reading: {
    backups: [
      backup("base_000000010000000000000041", 60 * 24 * 6, 812_000_000),
      backup("base_000000010000000000000098", 60 * 24 * 3, 845_000_000),
      backup("base_0000000100000000000000F2", 360, 861_000_000),
    ],
    latest: "000000010000000000000107",
    latestAt: ago(1),
    window: { from: ago(60 * 24 * 6), to: ago(1) },
    size: 2_710_000_000,
    readAt: ago(1),
  },
  lastBackupAt: ago(360),
}

const ARCHIVER = {
  archiveMode: "on",
  walLevel: "replica",
  archivedCount: 1840,
  lastArchived: "000000010000000000000107",
  lastArchivedAt: ago(1),
  failedCount: 0,
  current: "000000010000000000000108",
  waiting: 0,
  version: 170006,
  inRecovery: false,
}

const archiving = (over: Record<string, unknown> = {}) =>
  pitrOff(SHOP, {
    container: "shop-db",
    archive: ARCHIVE,
    archiver: ARCHIVER,
    window: { from: ARCHIVE.reading.window.from, to: ago(1) },
    nextBackup: "2026-10-18T03:00:00Z",
    restorePoints: [
      {
        id: 1,
        name: "before-orders-migration",
        lsn: "0/F2000028",
        createdAt: ago(240),
        createdBy: "operator",
      },
    ],
    ...over,
  })

const COPY = {
  id: 7,
  archiveId: 3,
  source: "shop-db",
  container: "shop-db-restored-20261017-085500",
  connectionId: 12,
  port: 5434,
  target: { kind: "time", time: ago(90) },
  baseBackup: "base_0000000100000000000000F2",
  recoveredTo: ago(91),
  state: "ready",
  createdAt: ago(5),
  createdBy: "operator",
}

const PLAN = {
  container: "shop-db",
  stream: "shop-db-20261017-090000",
  location: "s3://acme-backups/postgres/shop-db-20261017-090000",
  settings: {
    archive_command: "/jd-pitr/wal-g wal-push %p",
    archive_mode: "on",
    archive_timeout: "60",
  },
  command: [
    "postgres",
    "-c",
    "archive_command=/jd-pitr/wal-g wal-push %p",
    "-c",
    "archive_mode=on",
    "-c",
    "archive_timeout=60",
  ],
  mounts: ["shop-db-20261017-090000-pitr:/jd-pitr:ro"],
}

const TITLES: Record<string, string> = {
  enable: "Set up point-in-time recovery for shop-db",
  restore: "Recover shop-db into shop-db-restored-20261017-090500",
  switch: "Switch shop-db to shop-db-restored-20261017-085500",
  disable: "Turn point-in-time recovery off for shop-db",
}

const job = (id: string, what: string, status: string, over: Record<string, unknown> = {}) => ({
  id,
  kind: `database.pitr.shop-db.${what}`,
  title: TITLES[what],
  target: "shop-db",
  status,
  exitCode: 0,
  startedAt: NOW,
  startedBy: "operator",
  lines: 3,
  ...over,
})
const line = (seq: number, stream: string, text: string) => ({ seq, stream, text, at: NOW })

/** A job running the first time it is read and finished the second, with what it produced. */
const jobThatEnds =
  (id: string, what: string, result: Record<string, unknown>) => (call: number) => ({
    job: job(id, what, call === 0 ? "running" : "succeeded", {
      endedAt: call === 0 ? undefined : NOW,
    }),
    lines: [
      line(
        1,
        "status",
        what === "restore"
          ? "Fetching the base backup"
          : "Making the key the archive is encrypted with",
      ),
      line(2, "stdout", "INFO: Backup extraction complete."),
      ...(call === 0 ? [] : [line(3, "result", JSON.stringify(result))]),
    ],
  })

/**
 * The section's fixture with the point-in-time recovery routes answered over
 * it. `sent` is every request that was not a read, with what it carried.
 */
async function mockPitr(page: Page, answers: Record<string, Answer>, options: DatabaseMock = {}) {
  await mockDatabases(page, options)
  const calls = new Map<string, number>()
  const sent: { request: string; body: unknown }[] = []
  const table: Record<string, Answer> = { "GET /jobs/": [], "GET /backups/": [], ...answers }
  await page.route("**/api/v1/**", async (route) => {
    const url = new URL(route.request().url())
    const path = url.pathname.replace(/^\/api\/v1/, "")
    const method = route.request().method()
    const key = `${method} ${path}`
    let body: unknown
    if (method !== "GET") {
      body = route.request().postDataJSON()
      sent.push({ request: key, body })
    }
    if (!Object.hasOwn(table, key)) return route.fallback()
    const call = calls.get(key) ?? 0
    calls.set(key, call + 1)
    const answer = table[key]
    const value =
      typeof answer === "function"
        ? (answer as (c: number, b: unknown) => unknown)(call, body)
        : answer
    if (isReply(value)) return route.fulfill({ status: value.status, json: value.body })
    return route.fulfill({ json: value }).catch(() => undefined)
  })
  return { sent }
}

async function visit(page: Page) {
  const listed = page.waitForResponse(
    (response) => new URL(response.url()).pathname === "/api/v1/databases/",
    { timeout: 45_000 },
  )
  await page.goto("/databases/1/backups")
  await listed
}

/**
 * A moment some minutes ago as a `datetime-local` field takes it, and as the
 * instant it names: both in the browser's own time zone, which is the one the
 * field is read in.
 */
async function momentAgo(page: Page, minutes: number) {
  return page.evaluate((back) => {
    const d = new Date(Date.now() - back * 60_000)
    d.setMilliseconds(0)
    const pad = (n: number) => String(n).padStart(2, "0")
    return {
      local: `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}T${pad(d.getHours())}:${pad(d.getMinutes())}:${pad(d.getSeconds())}`,
      iso: d.toISOString(),
    }
  }, minutes)
}

// Exact: a job's console is a region too, and its name begins with the job's title.
const panel = (page: Page) =>
  page.getByRole("region", { name: "Point-in-time recovery", exact: true })
const dialog = (page: Page) => page.getByRole("dialog")
const progress = (page: Page) => panel(page).locator("[data-slot=transfer-progress]")

test("a server that does not archive offers it, and the setup shows what the server will restart with", async ({
  page,
}) => {
  let on = false
  const server = await mockPitr(page, {
    "GET /databases/1/pitr": () => (on ? archiving() : pitrOff(SHOP, { container: "shop-db" })),
    "POST /databases/1/pitr": (_call: number, body: unknown) => {
      if ((body as { preview?: boolean }).preview) return PLAN
      on = true
      return job("p1", "enable", "running")
    },
    "GET /jobs/p1": jobThatEnds("p1", "enable", {
      archive: 3,
      backup: "base_000000010000000000000002",
    }),
  })
  await visit(page)
  await expect(panel(page)).toContainText("The log of shop-db is not archived")
  await panel(page).getByRole("button", { name: "Set up point-in-time recovery" }).first().click()

  await expect(dialog(page)).toContainText("shop-db restarts once")
  await dialog(page).getByRole("radio", { name: "S3" }).click()
  // No bucket yet: nothing can be asked of the server.
  await expect(dialog(page).getByRole("button", { name: "Set up and restart" })).toBeDisabled()
  await dialog(page).getByLabel("Bucket").fill("acme-backups")
  await dialog(page).getByLabel("Prefix").fill("postgres")
  await dialog(page).getByLabel("Access key ID").fill("AKIAEXAMPLE")
  await dialog(page).getByLabel("Secret access key").fill("wJalrXUtnFEMI")

  // The command line is the server's answer, quoted the way a shell needs it.
  await expect(dialog(page)).toContainText(
    "postgres -c 'archive_command=/jd-pitr/wal-g wal-push %p' -c archive_mode=on",
  )
  await expect(dialog(page)).toContainText("s3://acme-backups/postgres/shop-db-20261017-090000")
  await expect
    .poll(() => server.sent.findLast((one) => one.request === "POST /databases/1/pitr")?.body)
    .toMatchObject({
      preview: true,
      targetKind: "s3",
      target: { bucket: "acme-backups", prefix: "postgres" },
    })

  await dialog(page).getByRole("button", { name: "Set up and restart" }).click()
  await expect(dialog(page)).toHaveCount(0)
  await expect(progress(page)).toHaveAttribute(
    "aria-label",
    "Set up point-in-time recovery for shop-db, succeeded",
  )
  const sent = server.sent.filter((one) => one.request === "POST /databases/1/pitr").at(-1)
  expect(sent?.body).toEqual({
    targetKind: "s3",
    target: { bucket: "acme-backups", region: "", endpoint: "", prefix: "postgres" },
    secrets: { accessKeyId: "AKIAEXAMPLE", secretAccessKey: "wJalrXUtnFEMI" },
    schedule: "0 3 * * *",
    retentionDays: 7,
  })
  // Once it ends the panel reads the server again, and it archives.
  await expect(panel(page)).toContainText("Any moment from")
  await expect(panel(page).getByText("Archiving", { exact: true }).first()).toBeVisible()
})

test("an archiving server says what it can go back to and what that rests on", async ({ page }) => {
  await mockPitr(page, { "GET /databases/1/pitr": archiving() })
  await visit(page)
  await expect(panel(page).locator("[data-slot=pitr-window]")).toContainText("Any moment from")
  await expect(panel(page).locator("dl")).toContainText("000000010000000000000107")
  await expect(panel(page)).toContainText("s3://acme-backups/postgres/shop-db-20261010-030000")
  await expect(panel(page)).toContainText("the last 7 days")
  await expect(panel(page)).toContainText("3 base backups")
  const backups = panel(page).getByRole("table", { name: "Base backups" })
  await expect(backups.getByRole("row")).toHaveCount(4)
  // Newest first.
  await expect(backups.getByRole("row").nth(1)).toContainText("base_0000000100000000000000F2")
  await expect(panel(page).getByRole("group", { name: "Restore points" })).toContainText(
    "before-orders-migration",
  )
  // No headline tiles: the page states its figures as facts.
  await expect(page.locator("[data-slot=stat-tile]")).toHaveCount(0)
})

test("archiving that fails says why, in WAL-G's words", async ({ page }) => {
  await mockPitr(page, {
    "GET /databases/1/pitr": archiving({
      archiver: {
        ...ARCHIVER,
        failedCount: 14,
        lastFailed: "000000010000000000000108",
        lastFailedAt: ago(0.5),
      },
      archiveError:
        "failed to upload 'pg_wal/000000010000000000000108' to bucket 'acme-backups': AccessDenied: Access Denied",
    }),
  })
  await visit(page)
  const notice = panel(page).getByText("Archiving is failing")
  await expect(notice).toBeVisible()
  await expect(panel(page)).toContainText("AccessDenied: Access Denied")
  await expect(panel(page)).toContainText("14 failures")
})

test("a recovery is planned by the server, runs beside it, and ends with a way into the copy", async ({
  page,
}) => {
  const restorePlan = {
    container: "shop-db-restored-20261017-090500",
    port: 5434,
    backup: "base_0000000100000000000000F2",
    backupAt: ago(360),
    settings: {
      archive_mode: "off",
      restore_command: "/jd-pitr/wal-g wal-fetch %f %p",
      recovery_target_time: "2026-10-17 07:30:00+00",
      recovery_target_action: "promote",
    },
  }
  const server = await mockPitr(page, {
    "GET /databases/1/pitr": archiving(),
    "POST /databases/1/pitr/restore": (_call: number, body: unknown) =>
      (body as { preview?: boolean }).preview ? restorePlan : job("r1", "restore", "running"),
    "GET /jobs/r1": jobThatEnds("r1", "restore", {
      container: "shop-db-restored-20261017-090500",
      connectionId: 12,
      port: 5434,
      recoveredTo: ago(90),
      summary: "Recovered to 2026-10-17T07:30:00Z",
    }),
  })
  await visit(page)
  const moment = await momentAgo(page, 90)
  await panel(page).getByRole("button", { name: "Recover to a moment" }).click()
  await expect(dialog(page)).toContainText("shop-db is not touched")
  await dialog(page)
    .getByRole("button", { name: /^A moment/ })
    .click()
  await dialog(page).getByLabel("Moment, in this browser's time").fill(moment.local)
  await expect(dialog(page)).toContainText("recovery_target_time = '2026-10-17 07:30:00+00'")
  await expect(dialog(page)).toContainText("shop-db-restored-20261017-090500 · 127.0.0.1:5434")
  await expect(dialog(page)).toContainText("base_0000000100000000000000F2")

  await dialog(page).getByRole("button", { name: "Recover into a new server" }).click()
  await expect(progress(page)).toHaveAttribute("aria-label", /succeeded$/)
  await expect(progress(page)).toContainText("Recovered to 2026-10-17T07:30:00Z")
  await expect(
    panel(page).getByRole("button", { name: "Open shop-db-restored-20261017-090500" }),
  ).toBeVisible()
  const sent = server.sent.filter((one) => one.request === "POST /databases/1/pitr/restore").at(-1)
  expect(sent?.body).toEqual({ archiveId: 3, target: { kind: "time", time: moment.iso } })
})

test("a moment before the archive is refused before it is asked for", async ({ page }) => {
  const server = await mockPitr(page, {
    "GET /databases/1/pitr": archiving(),
    "POST /databases/1/pitr/restore": {
      container: "shop-db-restored-20261017-090500",
      port: 5434,
      backup: "base_0000000100000000000000F2",
      backupAt: ago(360),
      settings: { archive_mode: "off", restore_command: "/jd-pitr/wal-g wal-fetch %f %p" },
    },
  })
  await visit(page)
  await panel(page).getByRole("button", { name: "Recover to a moment" }).click()
  await dialog(page)
    .getByRole("button", { name: /^A moment/ })
    .click()
  await dialog(page).getByLabel("Moment, in this browser's time").fill("2020-01-01T00:00:00")
  await expect(dialog(page)).toContainText("no earlier")
  await expect(
    dialog(page).getByRole("button", { name: "Recover into a new server" }),
  ).toBeDisabled()
  // The plans read while the form was filled ran nothing; no recovery was asked for.
  const asked = server.sent.filter(
    (one) =>
      one.request === "POST /databases/1/pitr/restore" &&
      !(one.body as { preview?: boolean }).preview,
  )
  expect(asked).toHaveLength(0)
})

test("a recovered copy is switched in only after it is confirmed", async ({ page }) => {
  const server = await mockPitr(page, {
    "GET /databases/1/pitr": archiving({ restores: [COPY] }),
    "POST /databases/1/pitr/restores/7/switch": job("s1", "switch", "running"),
    "GET /jobs/s1": {
      job: job("s1", "switch", "running"),
      lines: [line(1, "status", "Stopping shop-db")],
    },
  })
  await visit(page)
  const copies = panel(page).getByRole("group", { name: "Recovered copies" })
  await expect(copies).toContainText("shop-db-restored-20261017-085500")
  await expect(
    copies.getByRole("link", { name: "Open shop-db-restored-20261017-085500" }),
  ).toHaveAttribute("href", "/databases/12")
  await copies.getByRole("button", { name: "Switch in" }).click()
  const confirm = page.getByRole("dialog", { name: "Switch to shop-db-restored-20261017-085500" })
  await expect(confirm).toContainText("takes its name, its port and its networks")
  await expect(confirm.getByRole("textbox")).toHaveCount(0)
  expect(server.sent.filter((one) => one.request.endsWith("/switch"))).toHaveLength(0)
  await confirm.getByRole("button", { name: "Switch", exact: true }).click()
  await expect
    .poll(() => server.sent.filter((one) => one.request.endsWith("/switch")).length)
    .toBe(1)
})

test("turning it off keeps the archive unless asked, and says which it will do", async ({
  page,
}) => {
  const server = await mockPitr(page, {
    "GET /databases/1/pitr": archiving(),
    "DELETE /databases/1/pitr": job("o1", "disable", "running"),
    "GET /jobs/o1": { job: job("o1", "disable", "running"), lines: [] },
  })
  await visit(page)
  await panel(page).getByRole("button", { name: "More point-in-time recovery actions" }).click()
  await page.getByRole("menuitem", { name: "Turn off" }).click()
  await expect(dialog(page)).toContainText("The archive is kept under Earlier archives")
  await dialog(page).getByRole("switch", { name: "Also delete the archive" }).click()
  await dialog(page).getByRole("button", { name: "Turn off and delete" }).click()
  await expect
    .poll(() => server.sent.find((one) => one.request === "DELETE /databases/1/pitr")?.body)
    .toEqual({
      deleteArchive: true,
    })
})

test("a restore point is named, and a name the server would refuse is not sent", async ({
  page,
}) => {
  const server = await mockPitr(page, {
    "GET /databases/1/pitr": archiving(),
    "POST /databases/1/pitr/restore-points": {
      id: 2,
      name: "before-deploy",
      lsn: "0/108000028",
      createdAt: NOW,
    },
  })
  await visit(page)
  await panel(page).getByRole("button", { name: "Mark a restore point" }).click()
  await dialog(page).getByLabel("Name").fill("before deploy")
  await expect(dialog(page).getByRole("button", { name: "Mark" })).toBeDisabled()
  await dialog(page).getByLabel("Name").fill("before-deploy")
  await dialog(page).getByRole("button", { name: "Mark" }).click()
  await expect
    .poll(() => server.sent.find((one) => one.request.endsWith("/restore-points"))?.body)
    .toEqual({
      name: "before-deploy",
    })
})

test("a role that may only read sees the archive and none of the commands", async ({ page }) => {
  await mockPitr(
    page,
    { "GET /databases/1/pitr": archiving({ restores: [COPY] }) },
    { viewer: true },
  )
  await visit(page)
  await expect(panel(page)).toContainText("Any moment from")
  for (const name of [
    "Recover to a moment",
    "Take a base backup",
    "Mark a restore point",
    "Set up point-in-time recovery",
    "Switch in",
    "More point-in-time recovery actions",
  ]) {
    await expect(panel(page).getByRole("button", { name })).toHaveCount(0)
  }
})

test("a server this dashboard cannot archive says why, and offers nothing", async ({ page }) => {
  await mockPitr(page, {
    "GET /databases/1/pitr": pitrOff(SHOP, {
      available: false,
      reason:
        "shop-db belongs to the compose project shop, so its settings and mounts are its compose file's; archive it from there.",
    }),
  })
  await visit(page)
  await expect(panel(page)).toContainText("belongs to the compose project shop")
  await expect(
    panel(page).getByRole("button", { name: "Set up point-in-time recovery" }),
  ).toHaveCount(0)
})

test("a reading that fails is said, and tried again on request", async ({ page }) => {
  await mockPitr(page, {
    "GET /databases/1/pitr": (call: number) =>
      call === 0
        ? {
            status: 502,
            body: { error: { code: "docker_failed", message: "Docker did not answer" } },
          }
        : archiving(),
  })
  await visit(page)
  await expect(panel(page)).toContainText("Could not read point-in-time recovery")
  await expect(panel(page)).toContainText("Docker did not answer")
  await panel(page).getByRole("button", { name: "Try again" }).click()
  await expect(panel(page)).toContainText("Any moment from")
})

test("at 390 pixels the panel fits the width and keeps its commands", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 })
  await mockPitr(page, { "GET /databases/1/pitr": archiving({ restores: [COPY] }) })
  await visit(page)
  await expect(panel(page)).toContainText("Any moment from")
  await expect(panel(page).getByRole("button", { name: "Recover to a moment" })).toBeVisible()
  // Too narrow for the table: each base backup is a block of lines.
  await expect(panel(page).getByRole("list", { name: "Base backups" })).toBeVisible()
  expect(await page.evaluate(() => document.documentElement.scrollWidth > window.innerWidth)).toBe(
    false,
  )
})

test("point-in-time recovery screenshots @shots", async ({ page }) => {
  test.skip(!process.env.JD_SHOTS, "set JD_SHOTS to a directory to write the screenshots")
  const dir = process.env.JD_SHOTS
  let state: "off" | "on" | "failing" | "copy" = "off"
  await mockPitr(page, {
    "GET /databases/1/pitr": () =>
      state === "off"
        ? pitrOff(SHOP, { container: "shop-db" })
        : state === "failing"
          ? archiving({
              archiver: {
                ...ARCHIVER,
                failedCount: 14,
                lastFailed: "000000010000000000000108",
                lastFailedAt: ago(0.5),
              },
              archiveError:
                "failed to upload 'pg_wal/000000010000000000000108' to bucket 'acme-backups': AccessDenied: Access Denied",
            })
          : state === "copy"
            ? archiving({ restores: [COPY] })
            : archiving(),
    "POST /databases/1/pitr": PLAN,
    "POST /databases/1/pitr/restore": {
      container: "shop-db-restored-20261017-090500",
      port: 5434,
      backup: "base_0000000100000000000000F2",
      backupAt: ago(360),
      settings: {
        archive_mode: "off",
        restore_command: "/jd-pitr/wal-g wal-fetch %f %p",
        recovery_target_time: "2026-10-17 07:30:00+00",
        recovery_target_action: "promote",
      },
    },
  })
  await page.setViewportSize({ width: 1440, height: 900 })
  const shoot = async (name: string) => {
    await panel(page).scrollIntoViewIfNeeded()
    await page.screenshot({ path: `${dir}/${name}.png` })
  }

  await visit(page)
  await expect(panel(page)).toContainText("The log of shop-db is not archived")
  await shoot("pitr-off-1440")
  await panel(page).getByRole("button", { name: "Set up point-in-time recovery" }).first().click()
  await dialog(page).getByRole("radio", { name: "S3" }).click()
  await dialog(page).getByLabel("Bucket").fill("acme-backups")
  await dialog(page).getByLabel("Prefix").fill("postgres")
  await expect(dialog(page)).toContainText("archive_mode=on")
  await page.screenshot({ path: `${dir}/pitr-setup-1440.png` })
  await page.keyboard.press("Escape")
  await page.keyboard.press("Escape")

  state = "on"
  await page.reload()
  await expect(panel(page)).toContainText("Any moment from")
  await shoot("pitr-archiving-1440")
  await panel(page).getByRole("button", { name: "Recover to a moment" }).click()
  await dialog(page)
    .getByRole("button", { name: /^A moment/ })
    .click()
  await dialog(page)
    .getByLabel("Moment, in this browser's time")
    .fill((await momentAgo(page, 90)).local)
  await expect(dialog(page)).toContainText("recovery_target_time")
  await page.screenshot({ path: `${dir}/pitr-recover-1440.png` })
  await page.keyboard.press("Escape")
  await page.keyboard.press("Escape")

  state = "failing"
  await page.reload()
  await expect(panel(page)).toContainText("Archiving is failing")
  await shoot("pitr-failing-1440")

  state = "copy"
  await page.reload()
  await panel(page)
    .getByRole("group", { name: "Recovered copies" })
    .getByRole("button", { name: "Switch in" })
    .click()
  await expect(page.getByRole("dialog", { name: /^Switch to/ })).toBeVisible()
  await page.screenshot({ path: `${dir}/pitr-switch-1440.png` })
  await page.keyboard.press("Escape")

  state = "on"
  await page.setViewportSize({ width: 390, height: 844 })
  await page.reload()
  await expect(panel(page)).toContainText("Any moment from")
  await shoot("pitr-archiving-390")
})
