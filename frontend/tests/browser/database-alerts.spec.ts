import { expect, test, type Page } from "@playwright/test"
import { mockDatabases, type DatabaseMock } from "./database-fixture"

/**
 * A database's Alerts page: rules read as sentences with where each stands,
 * a new one is a form that speaks its own sentence back, a test says where it
 * went, and what was told lately is listed under them. Only an administrator
 * is offered anything to change.
 */

const ago = (minutes: number) => new Date(Date.now() - minutes * 60_000).toISOString()

const CHANNELS = [
  {
    id: 4,
    name: "on-call",
    kind: "discord",
    url: "https://discord.com/api/webhooks/1290/••••",
    target: "https://discord.com/api/webhooks/1290/••••",
    events: [],
    enabled: true,
    createdAt: ago(9000),
    updatedAt: ago(9000),
  },
  {
    id: 5,
    name: "ops mail",
    kind: "email",
    url: "",
    target: "ops@example.com",
    via: "smtp.example.com",
    events: [],
    enabled: true,
    createdAt: ago(9000),
    updatedAt: ago(9000),
  },
]

type Rule = Record<string, unknown> & { id: number }

const rule = (id: number, over: Record<string, unknown>): Rule => ({
  id,
  connectionId: 1,
  threshold: 0,
  minutes: 0,
  cooldownMinutes: 30,
  notifyRecovery: true,
  channels: [],
  enabled: true,
  createdAt: ago(6000),
  updatedAt: ago(6000),
  ...over,
})

function watching() {
  return {
    rules: [
      rule(11, { kind: "disk", threshold: 90, minutes: 5 }),
      rule(12, { kind: "connections", threshold: 80, minutes: 5 }),
      rule(13, { kind: "down", minutes: 2 }),
      rule(14, { kind: "backup_missed", connectionId: null, minutes: 60 }),
    ],
    subjects: [
      {
        ruleId: 11,
        subject: "connection:1",
        connectionId: 1,
        label: "shop",
        state: "firing",
        reading: 92.4,
        detail: "92% of the volume shop-pgdata is used (184.8 GB of 200.0 GB); the line is 90%.",
        since: ago(9),
        firedAt: ago(4),
        notifiedAt: ago(4),
        checkedAt: ago(0.2),
      },
      {
        ruleId: 12,
        subject: "connection:1",
        connectionId: 1,
        label: "shop",
        state: "quiet",
        reading: 32,
        detail: "31 of 97 connections in use (32%, 3 more kept for superusers); the line is 80%.",
        checkedAt: ago(0.2),
      },
      {
        ruleId: 13,
        subject: "connection:1",
        connectionId: 1,
        label: "shop",
        state: "quiet",
        reading: 0,
        detail: "shop answers.",
        checkedAt: ago(0.2),
      },
      {
        ruleId: 14,
        subject: "job:3",
        jobId: 3,
        label: "nightly-db",
        state: "quiet",
        reading: 7,
        detail: "The next run of nightly-db is due 2026-10-18 03:00 UTC.",
        checkedAt: ago(0.2),
      },
    ],
    events: [
      {
        id: 31,
        ruleId: 11,
        connectionId: 1,
        kind: "disk",
        subject: "connection:1",
        label: "shop",
        event: "fired",
        detail: "92% of the volume shop-pgdata is used (184.8 GB of 200.0 GB); the line is 90%.",
        delivered: 2,
        failed: 0,
        createdAt: ago(4),
      },
      {
        id: 30,
        ruleId: 13,
        connectionId: 1,
        kind: "down",
        subject: "connection:1",
        label: "shop",
        event: "recovered",
        detail: "shop answers.",
        delivered: 2,
        failed: 0,
        createdAt: ago(1300),
      },
      {
        id: 29,
        ruleId: 13,
        connectionId: 1,
        kind: "down",
        subject: "connection:1",
        label: "shop",
        event: "fired",
        detail: "shop does not answer: dial tcp 127.0.0.1:5432: connect: connection refused",
        delivered: 1,
        failed: 1,
        createdAt: ago(1312),
      },
      {
        id: 28,
        ruleId: 9,
        jobId: 3,
        kind: "backup_failed",
        subject: "job:3",
        label: "nightly-db",
        event: "fired",
        detail:
          "The run of nightly-db that started 2026-10-15 03:00 UTC failed: pg_dump: error: connection refused",
        delivered: 0,
        failed: 0,
        kept: "cooldown",
        createdAt: ago(2900),
      },
    ],
    lastPass: ago(0.2),
    everySeconds: 30,
  }
}

const NOTHING = () => ({
  rules: [],
  subjects: [],
  events: [],
  lastPass: ago(0.2),
  everySeconds: 30,
})

type Sent = { method: string; path: string; body: Record<string, unknown> | undefined }

/**
 * The section's fixture with the alert routes answered over it, from a reading
 * the routes below change as the page writes to them.
 */
async function mockAlerts(
  page: Page,
  options: DatabaseMock & { alerts?: ReturnType<typeof watching>; failed?: boolean } = {},
) {
  await mockDatabases(page, options)
  const state = options.alerts ?? watching()
  const sent: Sent[] = []
  let next = 100
  await page.route("**/api/v1/**", async (route) => {
    const url = new URL(route.request().url())
    const path = url.pathname.replace(/^\/api\/v1/, "")
    const method = route.request().method()
    if (path === "/deploy/notifications") return route.fulfill({ json: CHANNELS })
    if (!path.startsWith("/databases/alerts")) return route.fallback()
    if (method === "GET") {
      if (options.failed) {
        return route.fulfill({
          status: 500,
          json: { error: { code: "internal", message: "The alert rules could not be read." } },
        })
      }
      return route.fulfill({ json: state })
    }
    const body = method === "DELETE" ? undefined : route.request().postDataJSON()
    sent.push({ method, path, body })
    const id = Number(/\/rules\/(\d+)/.exec(path)?.[1])
    if (path.endsWith("/test")) {
      return route.fulfill({ json: { delivered: 2, failed: 0, paused: 0, channels: 2 } })
    }
    if (method === "POST") {
      const made = rule(next++, { ...body })
      state.rules.push(made)
      return route.fulfill({ status: 201, json: made })
    }
    const at = state.rules.findIndex((r) => r.id === id)
    if (method === "DELETE") {
      state.rules.splice(at, 1)
      return route.fulfill({ status: 204, body: "" })
    }
    state.rules[at] = { ...state.rules[at], ...body }
    return route.fulfill({ json: state.rules[at] })
  })
  return { sent, state }
}

async function visit(page: Page, path = "/databases/1/alerts") {
  const listed = page.waitForResponse(
    (response) => new URL(response.url()).pathname === "/api/v1/databases/",
    { timeout: 45_000 },
  )
  await page.goto(path)
  await listed
}

const rules = (page: Page) => page.getByRole("region", { name: "Alert rules" })
const recent = (page: Page) => page.getByRole("region", { name: "Recent alerts" })
const ruleRow = (page: Page, sentence: string) =>
  rules(page).getByRole("listitem").filter({ hasText: sentence })

test.describe("database alerts", () => {
  test("rules read as sentences with where each stands, over what was told lately", async ({
    page,
  }) => {
    await mockAlerts(page)
    await visit(page)
    // The rail lists the page among the database's operations.
    await expect(page.getByRole("link", { name: "Alerts", exact: true }).first()).toBeVisible()
    await expect(page.getByRole("list", { name: "Who is told about shop" })).toBeVisible()

    const disk = ruleRow(page, "Disk filling up")
    await expect(disk.getByText("above 90% for 5 min")).toBeVisible()
    await expect(disk.getByText(/Firing · 92% used since/)).toBeVisible()
    // The reading is drawn against the rule's line, and said in the server's words.
    await expect(disk.getByRole("meter", { name: "92% used against a 90% line" })).toBeVisible()
    await expect(disk.getByText(/of the volume shop-pgdata is used/)).toBeVisible()
    await expect(rules(page).getByText("1 firing")).toBeVisible()

    await expect(
      ruleRow(page, "Connections near the limit").getByText(/Quiet · 32% of the limit/),
    ).toBeVisible()
    await expect(ruleRow(page, "Server down").getByText("no answer for 2 min")).toBeVisible()
    const missed = ruleRow(page, "Backup missed")
    await expect(missed.getByText("Every database")).toBeVisible()
    await expect(missed.getByText("follows nightly-db")).toBeVisible()
    // Firing first: the rule telling somebody is the one a reader came for.
    await expect(rules(page).getByRole("listitem").first()).toContainText("Disk filling up")

    await expect(recent(page).getByRole("listitem")).toHaveCount(4)
    await expect(recent(page).getByRole("listitem").first()).toContainText("told 2 channels")
    await expect(recent(page).getByText(/told 1 channel, 1 failed/)).toBeVisible()
    await expect(recent(page).getByText(/kept back by the cooldown/)).toBeVisible()
    await expect(recent(page).getByText(/rule removed/)).toBeVisible()
  })

  test("a new rule is added from a form that speaks it back", async ({ page }) => {
    const { sent } = await mockAlerts(page)
    await visit(page)
    await rules(page).getByRole("button", { name: "Add alert" }).click()
    const sheet = page.getByRole("dialog", { name: "Add alert" })
    await expect(sheet).toBeVisible()
    await sheet
      .getByRole("group", { name: "Alert kind" })
      .getByRole("button", { name: /^Replication behind/ })
      .click()
    // The unit sits inside the line's own edge.
    await expect(sheet.getByText("s", { exact: true })).toBeVisible()
    await sheet.getByLabel("Alert line").fill("30")
    await sheet.getByRole("switch", { name: /on-call/ }).click()
    await expect(
      sheet.getByText("Replication behind: more than 30 s behind for 2 min, or stopped."),
    ).toBeVisible()
    await sheet.getByRole("button", { name: "Add alert" }).click()
    await expect(sheet).toHaveCount(0)
    expect(sent.at(-1)).toMatchObject({
      method: "POST",
      path: "/databases/alerts/rules",
      body: {
        connectionId: 1,
        kind: "replication",
        threshold: 30,
        minutes: 2,
        cooldownMinutes: 30,
        notifyRecovery: true,
        channels: [4],
      },
    })
    await expect(
      ruleRow(page, "Replication behind").getByText("more than 30 s behind for 2 min, or stopped"),
    ).toBeVisible()

    // A rule for every database is offered every kind, and saved for none in particular.
    await rules(page).getByRole("button", { name: "Add alert" }).click()
    const again = page.getByRole("dialog", { name: "Add alert" })
    await again
      .getByRole("radiogroup", { name: "Which databases the alert watches" })
      .getByRole("radio", { name: "Every database" })
      .click()
    await again.getByRole("button", { name: /^Backup failed/ }).click()
    await again.getByLabel("Cooldown in minutes").fill("abc")
    await again.getByRole("button", { name: "Add alert" }).click()
    // Refused before it is sent, beside the field that caused it.
    await expect(again.getByText("Between 0 and 10080 minutes.")).toBeVisible()
    await again.getByLabel("Cooldown in minutes").fill("0")
    await again.getByRole("button", { name: "Add alert" }).click()
    await expect(again).toHaveCount(0)
    expect(sent.at(-1)?.body).toMatchObject({
      connectionId: null,
      kind: "backup_failed",
      cooldownMinutes: 0,
    })
  })

  test("a test says where it went, and a rule is paused only once that is confirmed", async ({
    page,
  }) => {
    const { sent } = await mockAlerts(page)
    await visit(page)
    const menu = () =>
      page.getByRole("button", { name: "Actions for Disk filling up: above 90% for 5 min" })
    await menu().click()
    await page.getByRole("menuitem", { name: "Send test" }).click()
    await expect(page.getByText("Test sent to 2 channels")).toBeVisible()
    expect(sent.at(-1)).toMatchObject({ method: "POST", path: "/databases/alerts/rules/11/test" })

    await menu().click()
    await page.getByRole("menuitem", { name: "Pause" }).click()
    const confirm = page
      .getByRole("alertdialog")
      .or(page.getByRole("dialog", { name: "Pause alert" }))
    await expect(confirm.getByText("Disk filling up: above 90% for 5 min")).toBeVisible()
    await confirm.getByRole("button", { name: "Pause alert" }).click()
    await expect(ruleRow(page, "Disk filling up").getByText("Paused")).toBeVisible()
    expect(sent.at(-1)).toMatchObject({
      method: "PUT",
      path: "/databases/alerts/rules/11",
      body: { kind: "disk", threshold: 90, minutes: 5, connectionId: 1, enabled: false },
    })
  })

  test("with no rule, the first starts from a card of what the engine can be watched for", async ({
    page,
  }) => {
    await mockAlerts(page, { alerts: NOTHING() })
    await visit(page)
    await expect(
      page.getByText(
        "Nobody is told when shop stops answering, fills its disk or misses a backup.",
      ),
    ).toBeVisible()
    await expect(recent(page).getByText("Nothing has been told about shop yet.")).toBeVisible()
    await rules(page)
      .getByRole("button", { name: /^Disk filling up/ })
      .click()
    const sheet = page.getByRole("dialog", { name: "Add alert" })
    await expect(sheet.getByLabel("Alert line")).toHaveValue("90")
    await sheet.getByRole("button", { name: "Cancel" }).click()

    // A file has no server: only its backups can be watched.
    await visit(page, "/databases/7/alerts")
    const cards = rules(page).getByRole("button")
    await expect(cards.filter({ hasText: "Backup failed" })).toBeVisible()
    await expect(cards.filter({ hasText: "Server down" })).toHaveCount(0)
  })

  test("a read that failed says so in the page's place", async ({ page }) => {
    await mockAlerts(page, { failed: true })
    await visit(page)
    await expect(rules(page).getByRole("alert")).toContainText("The alert rules could not be read.")
    await expect(recent(page)).toHaveCount(0)
  })

  test("a viewer reads the rules and is offered nothing to change", async ({ page }) => {
    await mockAlerts(page, { viewer: true })
    await visit(page)
    await expect(ruleRow(page, "Disk filling up").getByText(/Firing · 92% used/)).toBeVisible()
    await expect(page.getByRole("button", { name: "Add alert" })).toHaveCount(0)
    await expect(page.getByRole("button", { name: /^Actions for/ })).toHaveCount(0)

    await page.unrouteAll({ behavior: "ignoreErrors" })
    await mockAlerts(page, { viewer: true, alerts: NOTHING() })
    await visit(page)
    await expect(page.getByText("No alert watches shop")).toBeVisible()
    await expect(rules(page).getByRole("button")).toHaveCount(0)
  })

  test("at 390 px the page reads down without a sideways scroll", async ({ page }) => {
    await page.setViewportSize({ width: 390, height: 844 })
    await mockAlerts(page)
    await visit(page)
    await expect(ruleRow(page, "Disk filling up").getByText(/Firing · 92% used/)).toBeVisible()
    expect(
      await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth),
    ).toBe(true)
    expect(
      await page.locator("[data-slot=page]").evaluate((el) => el.scrollWidth <= el.clientWidth + 1),
    ).toBe(true)
  })

  test("screenshots @shots", async ({ page }) => {
    test.skip(!process.env.JD_SHOTS, "set JD_SHOTS to a directory to take the screenshots")
    const dir = process.env.JD_SHOTS
    await page.setViewportSize({ width: 1440, height: 900 })
    await mockAlerts(page)
    await visit(page)
    await expect(ruleRow(page, "Disk filling up").getByText(/Firing · 92% used/)).toBeVisible()
    await page.screenshot({ path: `${dir}/alerts-firing-1440.png` })

    await rules(page).getByRole("button", { name: "Add alert" }).click()
    const sheet = page.getByRole("dialog", { name: "Add alert" })
    await sheet.getByRole("button", { name: /^Replication behind/ }).click()
    await sheet.getByLabel("Alert line").fill("30")
    await sheet.getByRole("switch", { name: /on-call/ }).click()
    await expect(sheet.getByText(/more than 30 s behind/)).toBeVisible()
    await page.screenshot({ path: `${dir}/alerts-add-1440.png` })
    await sheet.getByRole("button", { name: "Cancel" }).click()

    await page
      .getByRole("button", { name: "Actions for Disk filling up: above 90% for 5 min" })
      .click()
    await page.screenshot({ path: `${dir}/alerts-menu-1440.png` })
    await page.keyboard.press("Escape")

    await page.setViewportSize({ width: 390, height: 844 })
    await page.reload()
    await expect(ruleRow(page, "Disk filling up").getByText(/Firing · 92% used/)).toBeVisible()
    await page.screenshot({ path: `${dir}/alerts-firing-390.png` })

    await page.unrouteAll({ behavior: "ignoreErrors" })
    await page.setViewportSize({ width: 1440, height: 900 })
    await mockAlerts(page, { alerts: NOTHING() })
    await visit(page)
    await expect(page.getByText(/Nobody is told when shop stops answering/)).toBeVisible()
    await page.screenshot({ path: `${dir}/alerts-empty-1440.png` })

    await page.unrouteAll({ behavior: "ignoreErrors" })
    await mockAlerts(page, { viewer: true })
    await visit(page)
    await expect(ruleRow(page, "Disk filling up").getByText(/Firing · 92% used/)).toBeVisible()
    await page.screenshot({ path: `${dir}/alerts-viewer-1440.png` })
  })
})
