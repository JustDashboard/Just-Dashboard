import { expect, test, type Page } from "@playwright/test"
import { hold } from "./database-fixture"
import { backupJob, mockFleet, refusal } from "./database-fleet-fixture"
import { risen } from "./risen"

/**
 * The two lines under the control center's identity line: whether every
 * database is backed up on a schedule, and whether anything tells you when
 * one goes down. They took the place of the band of storage, sessions and
 * backups, which split totals the identity line already gave and showed
 * databases never backed up as an amber span with nothing to press.
 *
 * Each claim here is a way the lines could mislead or the presses could do the
 * wrong thing: a paused job counted as protection, the rest of the databases
 * added to a job that also archives files, a rule added beside one somebody
 * paused, alerts called on when nothing would deliver them, a line that moves
 * the cards under it as its reads land.
 */

const lines = (page: Page) => page.locator("[data-slot=fleet-protection]")
const line = (page: Page, name: "Backups" | "Alerts") =>
  lines(page).locator(`[data-slot=protection-line][data-name=${name}]`)
const dialog = (page: Page) => page.getByRole("alertdialog")
const scroller = (page: Page) => page.locator("[data-slot=page]").first().locator("xpath=..")
/** Every fixture database: shop, blog, cache, app and notes. */
const EVERY = [1, 2, 4, 5, 7]

test.describe("what protects the databases", () => {
  test("nothing scheduled and nothing watched: each line says so and offers the one press", async ({
    page,
  }) => {
    await mockFleet(page)
    await page.goto("/databases")

    const backups = line(page, "Backups")
    await expect(backups).toContainText("None of the 5 is backed up on a schedule")
    await expect(backups).toContainText(/and 2 more/)
    // The press says what it does, straight after the words it answers.
    await expect(backups.getByRole("button", { name: "Schedule backups" })).toBeVisible()

    const alerts = line(page, "Alerts")
    await expect(alerts).toContainText("Nothing tells you when one goes down")
    await expect(alerts.getByRole("button", { name: "Turn on alerts" })).toBeVisible()
    const after = async (name: "Backups" | "Alerts") => {
      const words = (await line(page, name).locator("[data-slot=check-words]").boundingBox())!
      const press = (await line(page, name).getByRole("button").boundingBox())!
      return press.x - (words.x + words.width)
    }
    expect(await after("Backups")).toBeLessThan(24)
    expect(await after("Alerts")).toBeLessThan(24)
    // Words, not a tinted archive and bell.
    await expect(lines(page).locator("svg")).toHaveCount(0)

    // The band is gone, and nothing of it is drawn.
    await expect(page.locator("[data-slot=fleet-band]")).toHaveCount(0)
    await expect(page.locator("[data-slot=bar-reading]")).toHaveCount(0)
  })

  test("Schedule makes a nightly job of every database and starts its first run", async ({
    page,
  }) => {
    const server = await mockFleet(page)
    await page.goto("/databases")
    await line(page, "Backups").getByRole("button", { name: "Schedule" }).click()

    await expect(dialog(page)).toContainText("Back up 5 databases every night")
    await expect(dialog(page)).toContainText("/var/backups/just-dashboard")
    await expect(dialog(page)).toContainText("keeps the last 7")
    await dialog(page).getByRole("button", { name: "Schedule" }).click()
    await expect(dialog(page)).toHaveCount(0)

    const [made] = server.bodies("POST /backups/")
    expect(made).toMatchObject({
      name: "Databases",
      targetKind: "local",
      target: { path: "/var/backups/just-dashboard" },
      schedule: "0 3 * * *",
      retention: 7,
      enabled: true,
      sources: [],
    })
    expect([...(made.databaseDumps as number[])].sort((a, b) => a - b)).toEqual(EVERY)
    expect(server.sent.map((one) => one.call)).toContain("POST /backups/100/run")

    await expect(line(page, "Backups")).toContainText("All 5 backed up every day at 03:00")
    await expect(line(page, "Backups")).toContainText("Databases is running now")
    await expect(line(page, "Backups").getByRole("button")).toHaveCount(0)
    await expect(lines(page).getByRole("link", { name: "Backup job Databases" })).toHaveAttribute(
      "href",
      "/backups/100",
    )
  })

  test("the rest join the database-only job already there, one at a time, never a job with files", async ({
    page,
  }) => {
    const server = await mockFleet(page, {
      jobs: [
        backupJob(7, { name: "Nightly dumps", databaseDumps: [1, 2] }),
        backupJob(8, { name: "Shop files", sources: ["/srv/shop"], databaseDumps: [1] }),
      ],
    })
    await page.goto("/databases")
    const backups = line(page, "Backups")
    await expect(backups).toContainText("3 of 5 not backed up on a schedule")
    await backups.getByRole("button", { name: "Schedule" }).click()
    await expect(dialog(page)).toContainText("Add 3 databases to Nightly dumps")
    await expect(dialog(page)).toContainText("every day at 03:00")
    await dialog(page).getByRole("button", { name: "Schedule" }).click()
    await expect(dialog(page)).toHaveCount(0)

    expect(server.bodies("POST /backups/")).toEqual([])
    expect(server.bodies("POST /backups/8/database-dumps")).toEqual([])
    const added = server.bodies("POST /backups/7/database-dumps").map((one) => one.connectionId)
    expect([...(added as number[])].sort((a, b) => a - b)).toEqual([4, 5, 7])
    // Both jobs run at three, so the line can say when.
    await expect(backups).toContainText("All 5 backed up every day at 03:00")
  })

  test("a paused job, one run by hand and one taken from another server protect nothing", async ({
    page,
  }) => {
    await mockFleet(page, {
      jobs: [
        backupJob(1, { databaseDumps: [1], enabled: false }),
        backupJob(2, { databaseDumps: [2], schedule: "" }),
        backupJob(3, { databaseDumps: [4], imported: true }),
        backupJob(4, { databaseDumps: [5, 7] }),
      ],
    })
    await page.goto("/databases")
    await expect(line(page, "Backups")).toContainText("3 of 5 not backed up on a schedule")
    await expect(line(page, "Backups")).toContainText(/shop|blog|cache/)
  })

  test("every database on a schedule: the line says when, and a failed run is the reading's worry", async ({
    page,
  }) => {
    const server = await mockFleet(page, {
      jobs: [
        backupJob(3, {
          name: "Databases",
          databaseDumps: EVERY,
          lastRun: {
            id: 4,
            jobId: 3,
            status: "success",
            startedAt: new Date(Date.now() - 2 * 3_600_000).toISOString(),
            endedAt: new Date(Date.now() - 2 * 3_600_000 + 60_000).toISOString(),
            sizeBytes: 43_201_331,
          },
        }),
      ],
    })
    await page.goto("/databases")
    const backups = line(page, "Backups")
    await expect(backups).toContainText("All 5 backed up every day at 03:00")
    await expect(backups).toContainText(/Last run 1h(?: \d+m)? ago, 41\.2 MB · next in 8h/)
    await expect(lines(page).getByRole("link", { name: "Backup job Databases" })).toBeVisible()

    server.answer("GET /backups/", () => ({
      body: [
        backupJob(3, {
          name: "Databases",
          databaseDumps: EVERY,
          lastRun: { id: 5, jobId: 3, status: "failed", startedAt: new Date().toISOString() },
        }),
      ],
    }))
    await page.reload()
    await expect(backups).toContainText("The last run of Databases failed just now")
    // The line takes the warning's tone: its dot and its words.
    await expect(backups).toHaveAttribute("data-tone", "warning")
    await expect(backups.getByText("All 5 backed up every day at 03:00")).toHaveClass(
      /text-warning/,
    )
  })

  test("Turn on adds the usual rules over every database, and none beside one somebody paused", async ({
    page,
  }) => {
    const server = await mockFleet(page, {
      alerts: {
        rules: [
          {
            id: 1,
            connectionId: null,
            kind: "disk",
            threshold: 90,
            minutes: 5,
            cooldownMinutes: 30,
            notifyRecovery: true,
            channels: [],
            enabled: false,
            createdAt: new Date().toISOString(),
            updatedAt: new Date().toISOString(),
          },
        ],
      },
    })
    await page.goto("/databases")
    await line(page, "Alerts").getByRole("button", { name: "Turn on" }).click()
    await expect(dialog(page)).toContainText("stops answering for 2 minutes")
    await expect(dialog(page)).not.toContainText("fills its disk")
    // Nothing would deliver them yet, and the question says so.
    await expect(dialog(page)).toContainText("No notification channel is switched on yet")
    await dialog(page).getByRole("button", { name: "Turn on" }).click()
    await expect(dialog(page)).toHaveCount(0)

    const made = server.bodies("POST /databases/alerts/rules")
    expect(made.map((one) => one.kind)).toEqual([
      "down",
      "connections",
      "backup_failed",
      "backup_missed",
    ])
    for (const rule of made) expect(rule).toMatchObject({ connectionId: null, channels: [] })

    // Watched now, but nothing is there to hear it.
    const alerts = line(page, "Alerts")
    await expect(alerts).toContainText("Alerts have nowhere to go")
    await expect(lines(page).getByRole("link", { name: "Notification channels" })).toHaveAttribute(
      "href",
      "/deploy/notifications",
    )
  })

  test("a Turn on that stops after its first rule keeps the press, and the next makes only the rest", async ({
    page,
  }) => {
    const server = await mockFleet(page)
    let tried = 0
    server.answer("POST /databases/alerts/rules", () => {
      tried += 1
      if (tried === 2) return refusal(500, "internal", "the store could not be written")
    })
    await page.goto("/databases")
    await line(page, "Alerts").getByRole("button", { name: "Turn on" }).click()
    await dialog(page).getByRole("button", { name: "Turn on" }).click()
    await expect(page.getByText("Turn on alerts for every database failed")).toBeVisible()
    await dialog(page).getByRole("button", { name: "Cancel" }).click()

    // The one rule made watches every database; the line does not call that done.
    const alerts = line(page, "Alerts")
    await expect(alerts).toContainText("1 of the 5 usual alerts on")
    await expect(alerts).toContainText(
      "Not on: disk filling up, connections near the limit and 2 more",
    )
    await expect(alerts).toHaveAttribute("data-tone", "warning")
    await alerts.getByRole("button", { name: "Turn on alerts" }).click()
    await expect(dialog(page)).not.toContainText("stops answering")
    await expect(dialog(page)).toContainText("fills its disk past 90%")
    await dialog(page).getByRole("button", { name: "Turn on" }).click()
    await expect(dialog(page)).toHaveCount(0)

    // Read again before it made anything: the rule that was there is not made twice.
    expect(server.bodies("POST /databases/alerts/rules").map((one) => one.kind)).toEqual([
      "down",
      "disk",
      "disk",
      "connections",
      "backup_failed",
      "backup_missed",
    ])
    await expect(alerts).toContainText("Alerts have nowhere to go")
    await expect(alerts.getByRole("button")).toHaveCount(0)
  })

  test("watched and told: the line names the channels, and what is firing comes first", async ({
    page,
  }) => {
    const rule = (id: number, kind: string, connectionId: number | null = null) => ({
      id,
      connectionId,
      kind,
      threshold: kind === "disk" ? 90 : 0,
      minutes: 2,
      cooldownMinutes: 30,
      notifyRecovery: true,
      channels: [],
      enabled: true,
      createdAt: new Date().toISOString(),
      updatedAt: new Date().toISOString(),
    })
    const server = await mockFleet(page, {
      alerts: { rules: [rule(1, "down"), rule(2, "disk")] },
      channels: [
        { id: 1, name: "Ops Slack", kind: "slack", enabled: true },
        { id: 2, name: "Old pager", kind: "webhook", enabled: false },
      ],
    })
    await page.goto("/databases")
    const alerts = line(page, "Alerts")
    await expect(alerts).toContainText("All 5 watched")
    await expect(alerts).toContainText("2 rules · told to Ops Slack")
    await expect(alerts.getByRole("button")).toHaveCount(0)

    server.answer("GET /databases/alerts", () => ({
      body: {
        rules: [rule(1, "down"), rule(2, "disk")],
        subjects: [
          {
            ruleId: 2,
            subject: "db:2",
            connectionId: 2,
            label: "blog",
            state: "firing",
            reading: 93,
            detail: "93% used, over 90%",
          },
        ],
        events: [],
        everySeconds: 30,
      },
    }))
    await page.reload()
    await expect(alerts).toContainText("blog: disk filling up")
    await expect(alerts).toContainText("93% used, over 90%")
    await expect(lines(page).getByRole("link", { name: "Alerts of blog" })).toHaveAttribute(
      "href",
      "/databases/2/alerts",
    )
  })

  test("a role that only reads is told the same, as ways to the pages, with no press", async ({
    page,
  }) => {
    await mockFleet(page, { viewer: true })
    await page.goto("/databases")
    await expect(line(page, "Backups")).toContainText("None of the 5 is backed up on a schedule")
    await expect(line(page, "Alerts")).toContainText("Nothing tells you when one goes down")
    await expect(lines(page).getByRole("button")).toHaveCount(0)
    await expect(lines(page).getByRole("link", { name: "Backup jobs" })).toHaveAttribute(
      "href",
      "/backups",
    )
  })

  test("the lines are one height and the cards stay where they are as the reads land", async ({
    page,
  }) => {
    await page.setViewportSize({ width: 1440, height: 900 })
    const server = await mockFleet(page)
    const held = hold()
    server.answer("GET /backups/", async () => {
      await held.until
      return { body: [backupJob(3, { databaseDumps: EVERY })] }
    })
    await page.goto("/databases")
    await expect(line(page, "Backups")).toContainText("Reading the backup jobs…")
    await expect(line(page, "Alerts").getByRole("button", { name: "Turn on" })).toBeVisible()
    await risen(page)
    const heading = page.getByRole("heading", { name: "All databases" })
    const before = (await heading.boundingBox())!.y
    const heights = () =>
      lines(page)
        .locator("[data-slot=protection-line]")
        .evaluateAll((all) => all.map((one) => one.getBoundingClientRect().height))

    // One line with its verb and one waiting: the same height.
    expect(new Set(await heights()).size).toBe(1)
    held.release()
    await expect(line(page, "Backups")).toContainText("All 5 backed up every day at 03:00")
    expect((await heading.boundingBox())!.y).toBe(before)
    expect(new Set(await heights()).size).toBe(1)
  })

  test("at a phone's width both lines keep their press and nothing scrolls sideways", async ({
    page,
  }) => {
    await page.setViewportSize({ width: 390, height: 844 })
    await mockFleet(page)
    await page.goto("/databases")
    await expect(line(page, "Backups").getByRole("button", { name: "Schedule" })).toBeVisible()
    await expect(line(page, "Alerts").getByRole("button", { name: "Turn on" })).toBeVisible()
    expect(await scroller(page).evaluate((el) => el.scrollWidth > el.clientWidth + 1)).toBe(false)
    expect(
      await page.evaluate(() => document.documentElement.scrollWidth > window.innerWidth),
    ).toBe(false)
  })

  test("the two lines, asking and answered @shots", async ({ page }) => {
    test.skip(!process.env.JD_SHOTS, "JD_SHOTS names the directory the screenshots go to")
    const shot = (name: string) => page.screenshot({ path: `${process.env.JD_SHOTS}/${name}.png` })
    await page.setViewportSize({ width: 1440, height: 900 })
    await mockFleet(page)
    await page.goto("/databases")
    await expect(line(page, "Backups").getByRole("button", { name: "Schedule" })).toBeVisible()
    await risen(page)
    await shot("fleet-protection-asking-1440")
    await line(page, "Backups").getByRole("button", { name: "Schedule" }).click()
    await expect(dialog(page)).toBeVisible()
    await shot("fleet-protection-schedule-1440")
    await dialog(page).getByRole("button", { name: "Schedule" }).click()
    await line(page, "Alerts").getByRole("button", { name: "Turn on" }).click()
    await dialog(page).getByRole("button", { name: "Turn on" }).click()
    await expect(line(page, "Backups")).toContainText("All 5 backed up")
    await expect(line(page, "Alerts")).toContainText("Alerts have nowhere to go")
    await page.addStyleTag({ content: "[data-sonner-toaster]{display:none!important}" })
    await shot("fleet-protection-answered-1440")
    await page.setViewportSize({ width: 390, height: 844 })
    await page.goto("/databases")
    await expect(line(page, "Backups")).toContainText("All 5 backed up")
    await shot("fleet-protection-390")
  })
})
