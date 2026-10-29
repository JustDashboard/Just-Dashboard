import { expect, test, type Page, type Route } from "@playwright/test"
import {
  deployment,
  json,
  mockProject,
  now,
  project,
  releaseComparison,
  run as fixtureRun,
} from "./deploy-fixture"
import type {
  DataCopy,
  DataCopyComparison,
  DeploymentEngineRun,
  DeploymentRelease,
  ReleaseDataPoint,
} from "../../src/lib/types"

/**
 * Data-aware rollback (`deploy/data_rollback.go`): the point each linked
 * database was at when a release went live, on the release's row and in what
 * changed; restoring a release's data into a side copy and working with the
 * copy — its connection URL, its tables beside the live ones, keeping it
 * longer, removing it, and the two ways on from it, one of which is not
 * offered and says why; the rollback dialog's side copy; and the run page's
 * link to the copy its rollback asked for.
 */

test.use({ timezoneId: "UTC" })

const run = fixtureRun as DeploymentEngineRun

const point19: ReleaseDataPoint = {
  connectionId: 5,
  name: "orders-db",
  engine: "postgres",
  kind: "pitr",
  at: "2026-09-02T12:01:30Z",
  archiveId: 3,
  recordedAt: "2026-09-02T12:01:31Z",
}
const point20: ReleaseDataPoint = {
  ...point19,
  at: "2026-09-03T11:58:00Z",
  recordedAt: "2026-09-03T11:58:01Z",
}

function releases(eligible = true): DeploymentRelease[] {
  const base = {
    projectId: 7,
    environmentId: 12,
    strategy: "blue_green" as const,
    expectedDowntime: false,
    pinned: false,
  }
  return [
    {
      ...base,
      id: 20,
      number: 2,
      runId: 84,
      predecessorReleaseId: 19,
      state: "live",
      planRevision: 2,
      sourceRevision: "a12bc34d56ef7890",
      imageDigest: `sha256:${"a".repeat(64)}`,
      configDigest: `sha256:${"b".repeat(64)}`,
      variablesDigest: `sha256:${"c".repeat(64)}`,
      createdAt: now,
      activatedAt: "2026-09-03T11:58:00Z",
      dataPoints: [point20],
      dataCopy: { eligible: true },
    },
    {
      ...base,
      id: 19,
      number: 1,
      runId: 83,
      state: "retained",
      planRevision: 1,
      sourceRevision: "99887766554433221100",
      imageDigest: `sha256:${"d".repeat(64)}`,
      configDigest: `sha256:${"e".repeat(64)}`,
      variablesDigest: `sha256:${"f".repeat(64)}`,
      createdAt: "2026-09-02T12:00:00Z",
      activatedAt: "2026-09-02T12:01:30Z",
      dataPoints: [point19],
      dataCopy: eligible
        ? { eligible: true }
        : { eligible: false, reason: "the point-in-time archive no longer reaches that moment" },
    },
  ]
}

const readyCopy: DataCopy = {
  id: 3,
  projectId: 7,
  environmentId: 12,
  releaseId: 19,
  releaseNumber: 1,
  connectionId: 5,
  name: "orders-db",
  engine: "postgres",
  database: "app",
  state: "ready",
  source: { kind: "pitr", at: point19.at, archiveId: 3 },
  recoveredTo: "2026-09-02T12:01:29Z",
  host: "db-5-copy-3.jd.internal",
  port: 5432,
  network: "jd-e12-db-7f3k2m9q1x0a",
  // Far enough ahead that the line reads in hours whenever the spec runs.
  expiresAt: new Date(Date.now() + 2 * 3_600_000 - 60_000).toISOString(),
  createdBy: "operator",
  createdAt: now,
  readyAt: now,
  restoreMs: 8_200,
}

const comparison: DataCopyComparison = {
  copyId: 3,
  database: "app",
  tables: [
    { schema: "public", name: "items", copyRows: 3, liveRows: 3 },
    { schema: "public", name: "orders", copyRows: 120, liveRows: 245 },
  ],
  truncated: false,
  readAt: now,
}

/** The two finished runs that made releases #1 and #2, as deploy-project.spec has them. */
async function mockRuns(page: Page, extra: Partial<DeploymentEngineRun> = {}) {
  const runs = [
    { ...run, id: 84, runNumber: 2, state: "succeeded", releaseId: 20, operation: "deploy" },
    {
      ...run,
      id: 83,
      runNumber: 1,
      state: "succeeded",
      releaseId: 19,
      operation: "deploy",
      requestedAt: "2026-09-02T12:00:00Z",
      endedAt: "2026-09-02T12:02:00Z",
      ...extra,
    },
  ]
  await page.route("**/api/v1/deploy/7/runs**", (route) => {
    const url = new URL(route.request().url())
    if (url.searchParams.get("view") !== "engine") return route.fallback()
    return json(route, { runs, running: false })
  })
  await page.route("**/api/v1/deploy/7", (route) =>
    json(route, { project, running: false, deployment: { ...deployment, activeRun: undefined } }),
  )
}

/**
 * The data-copy routes over one list the spec can watch: what each verb
 * sent, in order, and the copies as the server would hold them after it.
 */
async function mockCopies(page: Page, initial: DataCopy[] = [], eligible = true) {
  let copies = [...initial]
  const sent: { verb: string; body?: unknown }[] = []
  await page.route("**/api/v1/deploy/7/environments/12/releases?*", (route) =>
    json(route, releases(eligible)),
  )
  await page.route("**/api/v1/deploy/7/environments/12/releases", (route) =>
    json(route, releases(eligible)),
  )
  await page.route("**/api/v1/deploy/7/environments/12/releases/*/data-copies", async (route) => {
    const body = route.request().postDataJSON() as { afterRunId?: number }
    sent.push({ verb: "restore", body })
    const created: DataCopy = {
      ...readyCopy,
      id: 4,
      state: body.afterRunId ? "waiting" : "restoring",
      afterRunId: body.afterRunId,
      host: undefined,
      expiresAt: undefined,
      readyAt: undefined,
    }
    copies = [created, ...copies]
    await route.fulfill({
      status: 202,
      contentType: "application/json",
      body: JSON.stringify([created]),
    })
  })
  await page.route("**/api/v1/deploy/7/environments/12/data-copies**", async (route: Route) => {
    const request = route.request()
    const url = new URL(request.url())
    const path = url.pathname.replace("/api/v1/deploy/7/environments/12/data-copies", "")
    if (path === "" && request.method() === "GET") {
      const runId = Number(url.searchParams.get("run") ?? 0)
      return json(route, runId ? copies.filter((copy) => copy.afterRunId === runId) : copies)
    }
    if (path === "/3/url") {
      sent.push({ verb: "url" })
      return json(route, {
        url: "postgres://app:Gen3rated-Secret@db-5-copy-3.jd.internal:5432/app",
        host: "db-5-copy-3.jd.internal",
        port: 5432,
        user: "app",
        database: "app",
      })
    }
    if (path === "/3/extend") {
      sent.push({ verb: "extend", body: request.postDataJSON() })
      copies = copies.map((copy) =>
        copy.id === 3
          ? {
              ...copy,
              expiresAt: new Date(Date.parse(copy.expiresAt ?? now) + 3_600_000).toISOString(),
            }
          : copy,
      )
      return json(
        route,
        copies.find((copy) => copy.id === 3),
      )
    }
    if (path === "/3/comparison") {
      sent.push({ verb: "compare" })
      return json(route, comparison)
    }
    if (path === "/3" && request.method() === "DELETE") {
      sent.push({ verb: "remove" })
      copies = copies.map((copy) =>
        copy.id === 3 ? { ...copy, state: "removed", reason: "removed by operator" } : copy,
      )
      return json(
        route,
        copies.find((copy) => copy.id === 3),
      )
    }
    return route.fallback()
  })
  return {
    sent: () => sent.map((entry) => entry.verb),
    body: (verb: string) => sent.find((e) => e.verb === verb)?.body,
  }
}

const retainedRow = (page: Page) =>
  page.getByRole("listitem").filter({ hasText: "#1 Deploy" }).first()

test("a release's row says its data point was recorded, and the verb restores it beside production", async ({
  page,
}) => {
  await mockProject(page)
  await mockRuns(page)
  const copies = await mockCopies(page)
  await page.goto("/deploy/7/deployments")

  const row = retainedRow(page)
  const tag = row.getByText("Data point", { exact: true })
  await expect(tag).toBeVisible()
  await expect(tag).toHaveAttribute("title", "orders-db: point in time · 2026-09-02 12:01:30 UTC")
  // No copies yet: no panel standing empty above the list.
  await expect(page.getByRole("heading", { name: "Data copies" })).toHaveCount(0)

  await row.getByRole("button", { name: "Actions for #1 Deploy", exact: true }).click()
  await page.getByRole("menuitem", { name: /Restore data as of this release/ }).click()
  const dialog = page.getByRole("dialog", { name: "Restore data as of release #1" })
  await expect(dialog.getByText("orders-db", { exact: true })).toBeVisible()
  await expect(dialog.getByText("point in time · 2026-09-02 12:01:30 UTC")).toBeVisible()
  await expect(dialog.getByText("Production's data is not touched")).toBeVisible()
  await expect(dialog.getByText(/is not offered: production must never run against/)).toBeVisible()
  await dialog.getByRole("button", { name: "Restore copy", exact: true }).click()
  await expect(dialog).toBeHidden()
  expect(copies.sent()).toEqual(["restore"])
  expect(copies.body("restore")).toEqual({})

  // The copy appears as it restores.
  const panel = page.locator("#data-copies")
  await expect(panel.getByRole("heading", { name: "Data copies" })).toBeVisible()
  await expect(panel.getByText("Restoring", { exact: true })).toBeVisible()
})

test("the restore verb is not offered for a release the server says cannot be restored", async ({
  page,
}) => {
  await mockProject(page)
  await mockRuns(page)
  await mockCopies(page, [], false)
  await page.goto("/deploy/7/deployments")
  const row = retainedRow(page)
  await expect(row.getByText("Data point", { exact: true })).toBeVisible()
  await row.getByRole("button", { name: "Actions for #1 Deploy", exact: true }).click()
  await expect(page.getByRole("menuitem", { name: /Roll back to this release/ })).toBeVisible()
  await expect(page.getByRole("menuitem", { name: /Restore data as of this release/ })).toHaveCount(
    0,
  )
})

test("a ready copy reveals its URL, compares with live, is kept longer and is removed", async ({
  page,
}) => {
  await mockProject(page)
  await mockRuns(page)
  const copies = await mockCopies(page, [readyCopy])
  await page.goto("/deploy/7/deployments")

  const panel = page.locator("#data-copies")
  const copyRow = panel.getByRole("listitem").filter({ hasText: "orders-db" })
  await expect(copyRow.getByText("Ready", { exact: true })).toBeVisible()
  // What it is a copy of, the moment it reached, and how long it has.
  await expect(
    copyRow.getByText(
      /as of release #1 · point in time · 2026-09-02 12:01:29 UTC · removed in 1h 5\dm/,
    ),
  ).toBeVisible()
  await expect(
    panel.getByText(/Pointing the rolled-back release at the copy is not offered/),
  ).toBeVisible()

  const actions = () =>
    copyRow.getByRole("button", { name: "Actions for the copy of orders-db", exact: true }).click()

  // The URL is read on request, never drawn until revealed.
  await actions()
  await page.getByRole("menuitem", { name: "Connection URL…" }).click()
  const connection = page.getByRole("dialog", { name: "Connect to the copy of orders-db" })
  await expect(connection.getByText("db-5-copy-3.jd.internal")).toBeVisible()
  await expect(connection.getByText(/Gen3rated-Secret/)).toHaveCount(0)
  expect(copies.sent()).toEqual([])
  await connection.getByRole("button", { name: "Reveal" }).click()
  await expect(
    connection.getByText("postgres://app:Gen3rated-Secret@db-5-copy-3.jd.internal:5432/app"),
  ).toBeVisible()
  await connection.getByRole("button", { name: "Hide" }).click()
  await expect(connection.getByText(/Gen3rated-Secret/)).toHaveCount(0)
  await connection.getByRole("button", { name: "Close" }).first().click()

  // Tables and their counts, copy beside live; a differing count is marked.
  await actions()
  await page.getByRole("menuitem", { name: "Compare with live" }).click()
  const sheet = page.getByRole("dialog", { name: "orders-db: copy and live" })
  await expect(sheet.getByText("1 of 2 tables differ")).toBeVisible()
  const orders = sheet.getByRole("row").filter({ hasText: "public.orders" })
  await expect(orders.getByRole("cell", { name: "120", exact: true })).toBeVisible()
  await expect(orders.getByRole("cell", { name: "245", exact: true })).toBeVisible()
  await expect(orders.getByText("+125 live")).toBeVisible()
  await expect(
    sheet.getByRole("row").filter({ hasText: "public.items" }).getByText("same"),
  ).toBeVisible()
  await page.keyboard.press("Escape")
  await expect(sheet).toBeHidden()

  await actions()
  await page.getByRole("menuitem", { name: "Keep 1 more hour" }).click()
  await expect(copyRow.getByText(/removed in 2h 5\dm/)).toBeVisible()
  expect(copies.body("extend")).toEqual({ hours: 1 })

  // Restoring in place is the existing route's, with the point chosen.
  await actions()
  await page.getByRole("menuitem", { name: "Restore in place to this point…" }).click()
  const inPlace = page.getByRole("dialog", { name: "Restore orders-db in place" })
  await expect(inPlace.getByText("This replaces production's data")).toBeVisible()
  await expect(inPlace.getByRole("link", { name: /Open its Backups page/ })).toHaveAttribute(
    "href",
    "/databases/5/backups?recover=2026-09-02T12%3A01%3A30Z",
  )
  await inPlace.getByRole("button", { name: "Cancel" }).click()

  await actions()
  await page.getByRole("menuitem", { name: "Remove now" }).click()
  const confirm = page.getByRole("dialog", { name: "Remove copy" })
  await confirm.getByRole("button", { name: "Remove copy" }).click()
  await expect(copyRow.getByText("Removed", { exact: true })).toBeVisible()
  expect(copies.sent()).toEqual(["url", "compare", "extend", "remove"])
})

test("the rollback dialog's side copy is off by default and, ticked, waits for the rollback", async ({
  page,
}) => {
  const dashboard = await mockProject(page)
  await mockRuns(page)
  const copies = await mockCopies(page)
  await page.goto("/deploy/7/deployments")

  const open = async () => {
    await retainedRow(page)
      .getByRole("button", { name: "Actions for #1 Deploy", exact: true })
      .click()
    await page.getByRole("menuitem", { name: /Roll back to this release/ }).click()
    return page.getByRole("dialog", { name: "Roll back to release #1" })
  }
  let dialog = await open()
  const box = dialog.getByRole("checkbox", {
    name: /Also restore the database as of this release into a side copy/,
  })
  await expect(box).not.toBeChecked()
  await box.check()
  await expect(dialog.getByText(/also restored as it was when release #1 went live/)).toBeVisible()
  // Closed and opened again, it is off again.
  await dialog.getByRole("button", { name: "Cancel" }).click()
  dialog = await open()
  await expect(
    dialog.getByRole("checkbox", { name: /Also restore the database as of this release/ }),
  ).not.toBeChecked()
  await dialog
    .getByRole("checkbox", { name: /Also restore the database as of this release/ })
    .check()
  await dialog.getByRole("button", { name: "Roll back", exact: true }).click()
  await expect(page).toHaveURL(/\/deploy\/7\/runs\/90$/)
  expect(dashboard.actions()).toEqual(["rollback"])
  expect(copies.sent()).toEqual(["restore"])
  expect(copies.body("restore")).toEqual({ afterRunId: 90 })
})

test("the rollback dialog leaves the database alone when the box is not ticked", async ({
  page,
}) => {
  const dashboard = await mockProject(page)
  await mockRuns(page)
  const copies = await mockCopies(page)
  await page.goto("/deploy/7/deployments")
  await retainedRow(page)
    .getByRole("button", { name: "Actions for #1 Deploy", exact: true })
    .click()
  await page.getByRole("menuitem", { name: /Roll back to this release/ }).click()
  const dialog = page.getByRole("dialog", { name: "Roll back to release #1" })
  await dialog.getByRole("button", { name: "Roll back", exact: true }).click()
  await expect(page).toHaveURL(/\/deploy\/7\/runs\/90$/)
  expect(dashboard.actions()).toEqual(["rollback"])
  expect(copies.sent()).toEqual([])
})

test("a rollback's run page links the side copy it asked for", async ({ page }) => {
  await mockProject(page)
  const rollback = {
    ...run,
    state: "succeeded" as const,
    operation: "rollback" as const,
    releaseId: 21,
    endedAt: now,
    metadata: { targetReleaseId: 19 },
  }
  const snapshot = { run: rollback, steps: [] }
  await page.route("**/api/v1/deploy/7/runs/84", (route) => json(route, snapshot))
  await page.routeWebSocket(/\/api\/v1\/deploy\/7\/runs\/84\/stream/, (socket) => {
    socket.send(JSON.stringify({ type: "snapshot", data: snapshot, ts: Date.now() }))
  })
  await mockCopies(page, [{ ...readyCopy, afterRunId: 84 }])
  await page.goto("/deploy/7/runs/84")
  const copies = page.getByRole("list", { name: "Side copies of this rollback" })
  const link = copies.getByRole("link").filter({ hasText: "orders-db" })
  await expect(link).toHaveAttribute("href", "/deploy/7/deployments#data-copies")
  await expect(link.getByText("Ready", { exact: true })).toBeVisible()
})

test("what changed names each release's data point", async ({ page }) => {
  await mockProject(page, {
    comparison: {
      ...releaseComparison,
      comparison: releaseComparison.comparison && {
        ...releaseComparison.comparison,
        changes: { ...releaseComparison.comparison.changes, dataPoint: true },
        dataPoints: { from: [point19], to: [point20] },
      },
    },
  })
  await mockRuns(page)
  await mockCopies(page)
  await page.goto("/deploy/7/deployments")
  const liveRow = page.getByRole("listitem").filter({ hasText: "#2 Deploy" }).first()
  await liveRow.getByRole("button", { name: "Actions for #2 Deploy", exact: true }).click()
  await page.getByRole("menuitem", { name: /What changed in this release/ }).click()
  const section = page.getByRole("dialog", { name: "What changed" }).getByRole("region", {
    name: "Data point",
  })
  await expect(section.getByText("orders-db")).toBeVisible()
  await expect(
    section.getByText(
      "point in time · 2026-09-02 12:01:30 UTC → point in time · 2026-09-03 11:58:00 UTC",
    ),
  ).toBeVisible()
})

test("on a phone the copies panel fits the width", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 })
  await mockProject(page)
  await mockRuns(page)
  await mockCopies(page, [readyCopy])
  await page.goto("/deploy/7/deployments")
  await expect(page.locator("#data-copies").getByText("orders-db", { exact: true })).toBeVisible()
  const overflow = await page.evaluate(
    () => document.documentElement.scrollWidth - document.documentElement.clientWidth,
  )
  expect(overflow).toBeLessThanOrEqual(0)
})

test("every surface of data-aware rollback @shots", async ({ page }) => {
  test.skip(!process.env.JD_SHOTS, "set JD_SHOTS to a directory to write the screenshots to")
  const out = process.env.JD_SHOTS as string
  // A dialog or a sheet is captured once it has finished arriving.
  const settled = () =>
    page.evaluate(() =>
      Promise.all(document.getAnimations().map((animation) => animation.finished)),
    )
  await page.setViewportSize({ width: 1280, height: 900 })
  await mockProject(page, {
    comparison: {
      ...releaseComparison,
      comparison: releaseComparison.comparison && {
        ...releaseComparison.comparison,
        changes: { ...releaseComparison.comparison.changes, dataPoint: true },
        dataPoints: { from: [point19], to: [point20] },
      },
    },
  })
  await mockRuns(page)
  await mockCopies(page, [readyCopy])
  await page.goto("/deploy/7/deployments")
  await expect(page.locator("#data-copies").getByText("Ready", { exact: true })).toBeVisible()

  await retainedRow(page)
    .getByRole("button", { name: "Actions for #1 Deploy", exact: true })
    .click()
  await expect(
    page.getByRole("menuitem", { name: /Restore data as of this release/ }),
  ).toBeVisible()
  await settled()
  await page.screenshot({ path: `${out}/01-release-row-and-menu.png` })
  await page.getByRole("menuitem", { name: /Restore data as of this release/ }).click()
  const restore = page.getByRole("dialog", { name: "Restore data as of release #1" })
  await expect(restore.getByText("Production's data is not touched")).toBeVisible()
  await settled()
  await page.screenshot({ path: `${out}/02-restore-confirmation.png` })
  await restore.getByRole("button", { name: "Cancel" }).click()

  await page.locator("#data-copies").scrollIntoViewIfNeeded()
  await settled()
  await page.screenshot({ path: `${out}/03-data-copies-panel.png` })

  await page
    .locator("#data-copies")
    .getByRole("button", { name: "Actions for the copy of orders-db", exact: true })
    .click()
  await page.getByRole("menuitem", { name: "Connection URL…" }).click()
  const connection = page.getByRole("dialog", { name: "Connect to the copy of orders-db" })
  await connection.getByRole("button", { name: "Reveal" }).click()
  await expect(connection.getByText(/^postgres:\/\//)).toBeVisible()
  await settled()
  await page.screenshot({ path: `${out}/03b-copy-connection-url.png` })
  await connection.getByRole("button", { name: "Close" }).first().click()

  await page
    .locator("#data-copies")
    .getByRole("button", { name: "Actions for the copy of orders-db", exact: true })
    .click()
  await page.getByRole("menuitem", { name: "Compare with live" }).click()
  const sheet = page.getByRole("dialog", { name: "orders-db: copy and live" })
  await expect(sheet.getByText("1 of 2 tables differ")).toBeVisible()
  await settled()
  await page.screenshot({ path: `${out}/04-compare-with-live.png` })
  await page.keyboard.press("Escape")
  await expect(sheet).toBeHidden()

  await page
    .getByRole("listitem")
    .filter({ hasText: "#2 Deploy" })
    .first()
    .getByRole("button", { name: "Actions for #2 Deploy", exact: true })
    .click()
  await page.getByRole("menuitem", { name: /What changed in this release/ }).click()
  const changed = page.getByRole("dialog", { name: "What changed" })
  await expect(changed.getByRole("region", { name: "Data point" })).toBeVisible()
  await settled()
  await page.screenshot({ path: `${out}/05-what-changed-data-point.png` })
  await page.keyboard.press("Escape")

  await retainedRow(page)
    .getByRole("button", { name: "Actions for #1 Deploy", exact: true })
    .click()
  await page.getByRole("menuitem", { name: /Roll back to this release/ }).click()
  const rollback = page.getByRole("dialog", { name: "Roll back to release #1" })
  await rollback.getByRole("checkbox", { name: /Also restore the database/ }).check()
  await settled()
  await page.screenshot({ path: `${out}/06-rollback-dialog-side-copy.png` })
  await rollback.getByRole("button", { name: "Cancel" }).click()

  const rollbackRun = {
    ...run,
    state: "succeeded" as const,
    operation: "rollback" as const,
    releaseId: 21,
    endedAt: now,
    metadata: { targetReleaseId: 19 },
  }
  const snapshot = { run: rollbackRun, steps: [] }
  await page.route("**/api/v1/deploy/7/runs/84", (route) => json(route, snapshot))
  await page.routeWebSocket(/\/api\/v1\/deploy\/7\/runs\/84\/stream/, (socket) => {
    socket.send(JSON.stringify({ type: "snapshot", data: snapshot, ts: Date.now() }))
  })
  await mockCopies(page, [{ ...readyCopy, afterRunId: 84 }])
  await page.goto("/deploy/7/runs/84")
  await expect(page.getByRole("list", { name: "Side copies of this rollback" })).toBeVisible()
  await page.screenshot({ path: `${out}/07-run-page-side-copy.png` })
})
