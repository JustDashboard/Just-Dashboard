import { expect, test, type Locator, type Page } from "@playwright/test"
import {
  API_0,
  API_2,
  DAEMON,
  PM2,
  QUEUE,
  READ_ONLY,
  WORKER,
  mockPM2,
  type PM2Mocks,
} from "./pm2-fixture"
import { risen } from "./risen"

/**
 * A PM2 application's own page, and the list it is opened from, checked in a
 * browser against a mocked host.
 *
 * An application was a sheet over the list until 0.8.7. What these assert is
 * what the move to a page promised and a type check cannot see: that the old
 * address still lands, that the page opens on the application and draws
 * where it runs, that its views are in the address and its list stands
 * beside it, that a verb pressed on the page reaches every worker while a
 * worker's own reaches it alone, that nothing is offered to an account that
 * cannot use it, that rows hold one height and the opening holds its place
 * while the list is read, and that every state the page can be in is one it
 * was drawn for.
 */

const identity = (page: Page) => page.locator("[data-slot='pm2-app-identity']")
const rail = (page: Page) => page.getByRole("navigation", { name: "PM2 applications" })
const calls = (mocks: PM2Mocks) => mocks.writes.map((write) => write.call)

async function menu(page: Page, name: string): Promise<string[]> {
  await page.getByRole("button", { name, exact: true }).click()
  const items = await page.getByRole("menuitem").allInnerTexts()
  await page.keyboard.press("Escape")
  return items
}

async function choose(page: Page, name: string, item: string) {
  await page.getByRole("button", { name, exact: true }).click()
  await page.getByRole("menuitem", { name: item, exact: true }).click()
}

const box = async (locator: Locator) => (await locator.boundingBox())!

test("an application's page opens on what it is and draws where it runs", async ({ page }) => {
  await mockPM2(page)
  await page.goto("/processes/pm2/api")

  // The line it opens on: what runs it, its name and state, then the facts
  // an application is opened to check, and one command.
  const line = identity(page)
  await expect(line.locator("img[src='/logos/nodejs.svg']")).toBeVisible()
  await expect(line).toContainText("api")
  await expect(line.getByText("online", { exact: true })).toBeVisible()
  await expect(line).toContainText("/srv/api/server.js")
  await expect(line).toContainText("cluster of 2")
  await expect(line).toContainText("6 restarts")
  await expect(line.getByRole("button", { name: "Reload", exact: true })).toBeVisible()
  await expect(line.getByRole("button")).toHaveCount(2)

  // No tiles: the sheet opened on four.
  await expect(page.locator("[data-slot='stat-grid']")).toHaveCount(0)
  await expect(page.locator("[data-slot='stat-tile']")).toHaveCount(0)

  // Where it runs: the daemon and the hook above, its workers, the port and
  // its files below, each opening what it is.
  const picture = page.getByRole("group", { name: "Where it runs" })
  await expect(picture.getByRole("link", { name: "Open the PM2 daemon, PID 900" })).toHaveAttribute(
    "href",
    "/processes/900",
  )
  await expect(picture.getByRole("link", { name: "Open pm2-deploy.service" })).toHaveAttribute(
    "href",
    "/processes/services/pm2-deploy.service",
  )
  await expect(picture.getByRole("link", { name: "Open process 4021" })).toHaveAttribute(
    "href",
    "/processes/4021",
  )
  await expect(picture.getByRole("link", { name: "Open process 4022" })).toBeVisible()
  await expect(picture.getByText(":3000")).toBeVisible()
  await expect(picture.getByText(/every interface · 12 open connections/)).toBeVisible()
  await expect(picture.getByRole("link", { name: "Open /srv/api in Files" })).toHaveAttribute(
    "href",
    "/files?path=%2Fsrv%2Fapi",
  )

  // What it takes, as sentences: the two workers summed, memory against the
  // limit PM2 restarts a worker at, each over the minutes the sampler kept.
  const readings = page.getByTestId("pm2-readings").locator("[data-slot='reading-line']")
  await expect(readings).toHaveCount(4)
  await expect(readings.nth(0)).toContainText("16.0% of one core")
  await expect(readings.nth(0)).toContainText("2 workers · 22 threads")
  await expect(readings.nth(0).locator("svg").last()).toBeVisible()
  // A figure counts up once it is on screen.
  await readings.nth(3).scrollIntoViewIfNeeded()
  await expect(readings.nth(1)).toContainText("448.0 MB in use")
  await expect(readings.nth(1)).toContainText("fullest worker at 85% of the 300 MB")
  await expect(readings.nth(2)).toContainText("36.0 KB/s")
  await expect(readings.nth(3)).toContainText("Restarted 6 times")

  // How PM2 runs it and whether it comes back, which the sheet also said.
  await expect(page.getByText("Cluster of 2 workers")).toBeVisible()
  await expect(page.getByText("Restarts a worker above 300 MB")).toBeVisible()
  await expect(page.getByText("Comes back", { exact: true })).toBeVisible()
  await expect(page.getByText("/home/deploy/.pm2/logs/api-err.log")).toBeVisible()

  // The picture's log opens the log.
  await picture.getByRole("button", { name: "Read its log" }).click()
  await expect(page).toHaveURL(/\/processes\/pm2\/api\?tab=logs$/)
})

test("the views are in the address and the list beside it carries the view along", async ({
  page,
}) => {
  await mockPM2(page)
  await page.goto("/processes/pm2/api")
  const tabs = page.getByRole("tablist", { name: "Views of api" })
  await expect(tabs.getByRole("tab")).toHaveText([
    /Overview/,
    /Instances\s*2/,
    /Logs/,
    /Environment/,
    /Control/,
  ])

  // The list's own order, crashing first, a cluster as one row; the one on
  // screen is filled.
  const rows = rail(page).getByRole("button")
  await expect(rows.filter({ hasText: /worker|api|queue/ })).toHaveText([
    /worker[\s\S]*errored · 15 unstable/,
    /api[\s\S]*cluster of 2/,
    /queue[\s\S]*fork/,
  ])
  await expect(rail(page).getByRole("button", { name: "api", exact: true })).toHaveAttribute(
    "aria-current",
    "page",
  )

  await tabs.getByRole("tab", { name: "Environment" }).click()
  await expect(page).toHaveURL(/\/processes\/pm2\/api\?tab=environment$/)
  await expect(page.getByText("As worker #0 has it.")).toBeVisible()
  await expect(page.getByText("NODE_ENV").first()).toBeVisible()

  // Alt+↓ walks the list with the view; a fork has no Instances.
  await page.keyboard.press("Alt+ArrowDown")
  await expect(page).toHaveURL(/\/processes\/pm2\/queue\?tab=environment$/)
  await expect(page.getByRole("tablist", { name: "Views of queue" }).getByRole("tab")).toHaveText([
    /Overview/,
    /Logs/,
    /Environment/,
    /Control/,
  ])
  await expect(page.getByRole("tab", { name: "Environment" })).toHaveAttribute(
    "aria-selected",
    "true",
  )
  await expect(page.getByRole("button", { name: "Next application" })).toBeDisabled()

  // An application that is not running has no environment to read, and says so.
  await rail(page).getByRole("button", { name: "worker", exact: true }).click()
  await expect(page).toHaveURL(/\/processes\/pm2\/worker\?tab=environment$/)
  await expect(page.getByText("It is not running")).toBeVisible()

  // The list's search narrows the column.
  await rail(page).getByLabel("Search the applications").fill("queue")
  await expect(rail(page).locator("[data-workspace-item]")).toHaveCount(1)
  await expect(rail(page)).toContainText("1 of 3")
})

test("the address that opened the sheet lands on the application's page", async ({ page }) => {
  await mockPM2(page)
  // A bare name is what a process's owner link and a port's carried.
  await page.goto("/processes/pm2?app=api")
  await expect(page).toHaveURL(/\/processes\/pm2\/api$/)
  await expect(identity(page)).toContainText("cluster of 2")
  await expect(page.getByRole("dialog")).toHaveCount(0)
  // An exact identity is what the list, the server map and the palette carried.
  await page.goto("/processes/pm2?app=deploy:1")
  await expect(page).toHaveURL(/\/processes\/pm2\/worker$/)
  await expect(identity(page).getByText("errored", { exact: true })).toBeVisible()
  // What is gone says so, rather than a list with nothing open.
  await page.goto("/processes/pm2?app=retired")
  await expect(page).toHaveURL(/\/processes\/pm2\/retired$/)
  await expect(page.getByText("No application named retired under PM2")).toBeVisible()
  await page.getByRole("link", { name: "Back to PM2" }).click()
  await expect(page).toHaveURL(/\/processes\/pm2$/)

  // A row of the list opens the page; its Logs verb opens the log of the
  // worker whose row it was.
  await page.getByRole("button", { name: "queue", exact: true }).click()
  await expect(page).toHaveURL(/\/processes\/pm2\/queue$/)
  await page.goto("/processes/pm2")
  const second = page.getByRole("row", { name: /api #2/ })
  await second.hover()
  await second.getByRole("button", { name: "More actions" }).click()
  await page.getByRole("menuitem", { name: "Logs", exact: true }).click()
  await expect(page).toHaveURL(/\/processes\/pm2\/api\?tab=logs&worker=2$/)
})

test("a verb on the page reaches every worker, and a worker's own reaches it alone", async ({
  page,
}) => {
  const mocks = await mockPM2(page)
  await page.goto("/processes/pm2/api")

  // Every verb the sheet had is here: one as the command, the rest as words.
  expect(await menu(page, "More for api")).toEqual([
    "Restart",
    "Stop",
    "Scale…",
    "Reset restart counter",
    "Flush logs",
    "Open working directory",
    "Copy script path",
    "Delete from PM2",
  ])

  // The command, to each worker in turn: a rolling reload.
  await identity(page).getByRole("button", { name: "Reload", exact: true }).click()
  await expect
    .poll(() => calls(mocks))
    .toEqual(["POST /pm2/api/reload?user=deploy&id=0", "POST /pm2/api/reload?user=deploy&id=2"])

  // Stop says it means both workers before it stops them.
  mocks.writes.length = 0
  await choose(page, "More for api", "Stop")
  const confirm = page.getByRole("alertdialog").or(page.getByRole("dialog"))
  await expect(confirm).toContainText("All 2 workers of api stop serving")
  await confirm.getByRole("button", { name: "Stop", exact: true }).click()
  await expect
    .poll(() => calls(mocks))
    .toEqual(["POST /pm2/api/stop?user=deploy&id=0", "POST /pm2/api/stop?user=deploy&id=2"])

  // PM2 flushes by name, so once.
  mocks.writes.length = 0
  await choose(page, "More for api", "Flush logs")
  await confirm.getByRole("button", { name: "Flush", exact: true }).click()
  await expect.poll(() => calls(mocks)).toEqual(["POST /pm2/api/flush?user=deploy&id=0"])

  // Under Instances a worker's verbs are its own.
  mocks.writes.length = 0
  await page.getByRole("tab", { name: /Instances/ }).click()
  const workers = page.getByRole("list", { name: "Workers" }).locator("[data-slot='choice-row']")
  await expect(workers).toHaveCount(2)
  await expect(workers.nth(0)).toContainText("PID 4021")
  await expect(
    workers.nth(1).getByRole("link", { name: "Open worker 2, PID 4022" }),
  ).toHaveAttribute("href", "/processes/4022")
  expect(await menu(page, "More for worker 2")).toEqual(
    expect.arrayContaining(["Reload", "Restart", "Stop", "Logs", "Scale…", "Delete from PM2"]),
  )
  await choose(page, "More for worker 2", "Restart")
  await confirm.getByRole("button", { name: "Restart", exact: true }).click()
  await expect.poll(() => calls(mocks)).toEqual(["POST /pm2/api/restart?user=deploy&id=2"])
  // Its Logs verb opens its own stream.
  await choose(page, "More for worker 2", "Logs")
  await expect.poll(() => mocks.sockets.at(-1)?.get("source")).toBe("pm2:deploy/2/api")
  await expect(page.getByRole("tab", { name: "Logs" })).toHaveAttribute("aria-selected", "true")

  // Scaling stays a dialog: a stepper over the workers, said before it is done.
  mocks.writes.length = 0
  await page.getByRole("tab", { name: /Instances/ }).click()
  await page.getByRole("button", { name: "Scale…", exact: true }).click()
  const scale = page.getByRole("dialog", { name: /Scale api/ })
  await expect(scale.getByRole("button", { name: "Scale to 2" })).toBeDisabled()
  await scale.getByRole("button", { name: "One worker more" }).click()
  await expect(scale).toContainText("Starts 1 worker")
  await scale.getByRole("button", { name: "Scale to 3" }).click()
  await expect
    .poll(() => mocks.writes)
    .toEqual([{ call: "POST /pm2/api/scale?user=deploy&id=0", body: { instances: 3 } }])

  // Control reads each verb before it is pressed, and saving the startup
  // list is there with whether a reboot brings the application back.
  mocks.writes.length = 0
  await page.getByRole("tab", { name: "Control" }).click()
  await expect(page.getByRole("list", { name: "Run it" }).getByRole("listitem")).toHaveText([
    /Reload[\s\S]*Replaces its workers one at a time\./,
    /Restart[\s\S]*Stops it and starts it again\./,
    /Stop[\s\S]*Stops it until it is started again\./,
  ])
  const startup = page.getByRole("list", { name: "Startup" })
  await expect(startup).toContainText("api comes back after a reboot")
  await startup.getByRole("button", { name: "Save" }).click()
  await expect.poll(() => calls(mocks)).toEqual(["POST /pm2/save"])

  // Deleting it removes both workers and leaves the page, which has nothing left to show.
  mocks.writes.length = 0
  await page
    .getByRole("list", { name: "Housekeeping" })
    .getByRole("button", { name: "Delete", exact: true })
    .click()
  await expect(confirm).toContainText("and its 2 workers")
  await confirm.getByRole("button", { name: "Delete", exact: true }).click()
  await expect
    .poll(() => calls(mocks))
    .toEqual(["DELETE /pm2/api?user=deploy&id=0", "DELETE /pm2/api?user=deploy&id=2"])
  await expect(page).toHaveURL(/\/processes\/pm2$/)
})

test("an application PM2 gave up on says so, and Start is its command", async ({ page }) => {
  const mocks = await mockPM2(page)
  await page.goto("/processes/pm2/worker")
  await expect(identity(page)).toContainText("16 restarts (15 unstable)")
  await expect(page.getByText("PM2 stopped restarting it")).toBeVisible()
  // The readings hold their place and say why they are empty.
  const readings = page.getByTestId("pm2-readings").locator("[data-slot='reading-line']")
  await expect(readings).toHaveCount(4)
  await expect(readings.nth(0)).toContainText("Not running")
  await expect(readings.nth(3)).toContainText("Restarted 16 times")
  await expect(readings.nth(3)).toContainText("15 unstable")
  await identity(page).getByRole("button", { name: "Start", exact: true }).click()
  await expect.poll(() => calls(mocks)).toEqual(["POST /pm2/worker/start?user=deploy&id=1"])
  // The notice's press reads the log, on the application's own stream.
  await page.getByRole("button", { name: "Read the log" }).click()
  await expect.poll(() => mocks.sockets.at(-1)?.get("source")).toBe("pm2:deploy/1/worker")
  await expect(page.getByText("Restarted 16 times")).toBeVisible()
  await expect(page.getByRole("button", { name: "Around the last start" })).toHaveCount(0)
})

test("an application started since the list was saved says a reboot loses it", async ({ page }) => {
  const mocks = await mockPM2(page, {
    inventory: () => ({ ...PM2, daemons: [{ ...DAEMON, savedApps: ["api", "worker"] }] }),
  })
  await page.goto("/processes/pm2/queue")
  await expect(identity(page).getByText("not in the saved list")).toBeVisible()
  const notice = page.getByText("A reboot would not bring it back")
  await expect(notice).toBeVisible()
  await expect(page.getByText("Not in the saved list", { exact: true })).toBeVisible()
  await page.getByRole("button", { name: "Save list" }).first().click()
  await expect.poll(() => calls(mocks)).toEqual(["POST /pm2/save"])
})

test("a read-only account sees no control it cannot use", async ({ page }) => {
  const mocks = await mockPM2(page, { session: READ_ONLY })
  await page.goto("/processes/pm2/api")
  await expect(identity(page)).toContainText("cluster of 2")
  await expect(identity(page).getByRole("button", { name: /Reload|Start/ })).toHaveCount(0)
  expect(await menu(page, "More for api")).toEqual(["Open working directory", "Copy script path"])
  await expect(page.getByRole("tab", { name: "Control" })).toHaveCount(0)
  // A link to the view it does not have opens the Overview.
  await page.goto("/processes/pm2/api?tab=control")
  await expect(page.getByRole("tab", { name: "Overview" })).toHaveAttribute("aria-selected", "true")
  await page.getByRole("tab", { name: /Instances/ }).click()
  await expect(page.getByRole("button", { name: "Scale…" })).toHaveCount(0)
  expect(await menu(page, "More for worker 1")).toEqual([
    "Logs",
    "Open working directory",
    "Copy script path",
  ])
  // A credential in its environment stays masked for it.
  await page.getByRole("tab", { name: "Environment" }).click()
  await expect(page.getByText("NODE_ENV").first()).toBeVisible()
  await expect(page.getByText("fixture-secret")).toHaveCount(0)

  // And on the list: no command, no daemon menu, no verbs but the three that read.
  await page.goto("/processes/pm2")
  await expect(page.getByRole("row", { name: /worker/ })).toBeVisible()
  await expect(page.getByRole("button", { name: "Start application" })).toHaveCount(0)
  await expect(page.getByRole("button", { name: "Startup and bulk" })).toHaveCount(0)
  expect(mocks.writes).toEqual([])
})

test("the list opens on PM2 with one command and the daemon's verbs in a menu", async ({
  page,
}) => {
  await mockPM2(page)
  await page.goto("/processes/pm2")
  const line = page.locator("[data-slot='host-identity']")
  await expect(line).toHaveAttribute("data-layout", "project")
  await expect(line.getByText("Resurrects on boot")).toBeVisible()
  await expect(line.getByRole("button", { name: "Start application" })).toBeVisible()
  await expect(page.locator("[data-slot='stat-grid']")).toHaveCount(0)
  // "Startup and bulk" was a second grey button on the table's title line.
  await expect(
    page.locator("[data-slot='panel-header']").getByRole("button", { name: /Startup|Start/ }),
  ).toHaveCount(0)
  expect(await menu(page, "Startup and bulk")).toEqual([
    "Save startup list",
    "Reload all",
    "Restart all",
    "Stop all",
  ])
  // Scale opens where it was asked for.
  const first = page.getByRole("row", { name: /api #0/ })
  await first.hover()
  await first.getByRole("button", { name: "More actions" }).click()
  await page.getByRole("menuitem", { name: "Scale…" }).click()
  await expect(page.getByRole("dialog", { name: /Scale api/ })).toBeVisible()
  await expect(page).toHaveURL(/\/processes\/pm2$/)
})

for (const width of [1280, 390]) {
  test.describe(`at ${width}`, () => {
    test.use({ viewport: { width, height: width === 390 ? 844 : 900 } })

    test("rows hold one height, and a worker that dies moves nothing under it", async ({
      page,
    }) => {
      let died = false
      await mockPM2(page, {
        inventory: () =>
          died
            ? {
                ...PM2,
                processes: [
                  { ...API_0, status: "errored", pid: 0, cpu: 0, memory: 0, unstableRestarts: 4 },
                  API_2,
                  WORKER,
                  QUEUE,
                ],
              }
            : PM2,
      })

      // The list: a row with a limit, an uptime and unstable restarts and a
      // row with none of them.
      await page.goto("/processes/pm2")
      const rows = page.locator("[data-workspace-item]:visible")
      await expect(rows).toHaveCount(4)
      await risen(page)
      const heights = await rows.evaluateAll((all) =>
        all.map((row) => Math.round(row.getBoundingClientRect().height)),
      )
      // A table's rows are one height. Drawn down on a phone they are the
      // list's cards, which wrap what a row has to say, as they did.
      if (width === 1280) expect(new Set(heights).size).toBe(1)

      // An application's workers: one height whatever each is doing, and the
      // one under a worker that dies stays where it was.
      await page.goto("/processes/pm2/api?tab=instances")
      const workers = page
        .getByRole("list", { name: "Workers" })
        .locator("[data-slot='choice-row']")
      await expect(workers).toHaveCount(2)
      await risen(page)
      const before = [await box(workers.nth(0)), await box(workers.nth(1))]
      expect(Math.round(before[0].height)).toBe(Math.round(before[1].height))
      died = true
      await expect(workers.nth(0)).toContainText("PM2 stopped retrying", { timeout: 15_000 })
      await risen(page)
      const after = [await box(workers.nth(0)), await box(workers.nth(1))]
      expect(Math.round(after[0].height)).toBe(Math.round(before[0].height))
      expect(Math.round(after[1].height)).toBe(Math.round(before[1].height))
      // Measured from the row above: on a phone the line the page opens on
      // wraps a fact more once a worker is down, and the list goes with it.
      expect(Math.round(after[1].y - after[0].y)).toBe(Math.round(before[1].y - before[0].y))
      if (width === 1280) expect(Math.round(after[1].y)).toBe(Math.round(before[1].y))

      // The readings are four lines of one height, running or not.
      await page.goto("/processes/pm2/worker")
      const lines = page.getByTestId("pm2-readings").locator("[data-slot='reading-line']")
      await expect(lines).toHaveCount(4)
      const tall = await lines.evaluateAll((all) =>
        all.map((line) => Math.round(line.getBoundingClientRect().height)),
      )
      expect(new Set(tall).size).toBe(1)
      await page.goto("/processes/pm2/queue")
      await expect(lines.nth(1)).toContainText("in use")
      expect(
        await lines.evaluateAll((all) =>
          all.map((line) => Math.round(line.getBoundingClientRect().height)),
        ),
      ).toEqual(tall)
    })

    test("the opening is in the same place while the list is read and once it has been", async ({
      page,
    }) => {
      let release = () => {}
      const hold = new Promise<void>((resolve) => (release = resolve))
      await mockPM2(page, { hold })

      await page.goto("/processes/pm2")
      const line = page.locator("[data-slot='host-identity']")
      const band = page.locator("[data-slot='pm2-band']")
      await expect(band).toContainText("Reading PM2…")
      await risen(page)
      const loading = { line: await box(line), band: await box(band) }
      release()
      await expect(page.locator("[data-workspace-item]:visible")).toHaveCount(4)
      await risen(page)
      const loaded = { line: await box(line), band: await box(band) }
      expect(Math.round(loaded.line.y)).toBe(Math.round(loading.line.y))
      // The line and the band keep their height when the applications land
      // under them. On a phone the line's facts wrap, and the band follows.
      if (width === 1280) {
        expect(Math.round(loaded.line.height)).toBe(Math.round(loading.line.height))
        expect(Math.round(loaded.band.y)).toBe(Math.round(loading.band.y))
        expect(Math.round(loaded.band.height)).toBe(Math.round(loading.band.height))
      }
    })

    test("an application's line is in the same place before its list is read", async ({ page }) => {
      let release = () => {}
      const hold = new Promise<void>((resolve) => (release = resolve))
      await mockPM2(page, { hold })
      await page.goto("/processes/pm2/queue")
      await expect(identity(page)).toContainText("Reading queue…")
      await risen(page)
      const loading = await box(identity(page))
      release()
      await expect(identity(page)).toContainText("fork")
      await risen(page)
      const loaded = await box(identity(page))
      expect(Math.round(loaded.y)).toBe(Math.round(loading.y))
      expect(Math.round(loaded.x)).toBe(Math.round(loading.x))
    })

    test("nothing is cut off or scrolls sideways", async ({ page }) => {
      await mockPM2(page)
      for (const path of [
        "/processes/pm2",
        "/processes/pm2/api",
        "/processes/pm2/api?tab=instances",
        "/processes/pm2/api?tab=logs",
        "/processes/pm2/api?tab=environment",
        "/processes/pm2/api?tab=control",
        "/processes/pm2/worker",
      ]) {
        await page.goto(path)
        await page.waitForLoadState("networkidle")
        const overflow = await page.evaluate(
          () => document.documentElement.scrollWidth > document.documentElement.clientWidth,
        )
        expect(overflow, `${path} scrolls sideways at ${width}`).toBe(false)
        const unnamed = await page.evaluate(() => {
          const bad: string[] = []
          for (const el of document.querySelectorAll<HTMLElement>("button")) {
            if (el.offsetParent === null) continue
            if ((el.textContent ?? "").trim().length > 0) continue
            if (!el.getAttribute("aria-label") && !el.querySelector(".sr-only")) {
              bad.push(el.outerHTML.slice(0, 120))
            }
          }
          return bad
        })
        expect(unnamed, `unlabelled icon-only controls on ${path}`).toEqual([])
      }
    })
  })
}

test.describe("an application on a phone", () => {
  test.use({ viewport: { width: 390, height: 844 } })

  test("is one pane with the way back and its command in reach", async ({ page }) => {
    await mockPM2(page)
    await page.goto("/processes/pm2/api")
    await expect(rail(page)).toBeHidden()
    await expect(page.getByRole("link", { name: "PM2", exact: true })).toBeInViewport()
    await expect(page.getByRole("button", { name: "Next application" })).toBeInViewport()
    await expect(
      identity(page).getByRole("button", { name: "Reload", exact: true }),
    ).toBeInViewport()
    // The page scrolls as a whole: its last section is reached, and the
    // line it opens on has gone with the rest.
    const last = page.getByRole("heading", { name: "After a reboot" })
    await last.scrollIntoViewIfNeeded()
    await expect(last).toBeInViewport()
    await expect(identity(page)).not.toBeInViewport()
    // The picture's nodes say in words what the lines say when there is room.
    await expect(page.getByText("Worker 1 of 2", { exact: true })).toBeVisible()
  })
})

test("every state the pages can be in is one they were drawn for", async ({ page }) => {
  // PM2 not installed: its logo, one sentence, the next step.
  await mockPM2(page, { inventory: () => ({ available: false, processes: null }) })
  for (const path of ["/processes/pm2", "/processes/pm2/api"]) {
    await page.goto(path)
    const state = page.locator("[data-slot='empty-state']")
    await expect(state).toContainText("PM2 is not installed")
    await expect(state.locator("img[src='/logos/pm2.svg']")).toBeVisible()
    await expect(state).toContainText("npm install -g pm2")
    await expect(state.getByRole("link", { name: "Open a terminal" })).toBeVisible()
    await expect(rail(page)).toHaveCount(0)
  }
  await page.unrouteAll({ behavior: "ignoreErrors" })

  // Installed, and no account's daemon is running: not "PM2 is running".
  await mockPM2(page, { inventory: () => ({ available: true, daemons: [], processes: [] }) })
  await page.goto("/processes/pm2")
  const idle = page.locator("[data-slot='empty-state']")
  await expect(idle).toContainText("No PM2 daemon is running")
  await expect(idle).toContainText("pm2 start app.js")
  await expect(idle.locator("img[src='/logos/pm2.svg']")).toBeVisible()
  await expect(page.locator("[data-slot='pm2-band']")).toHaveCount(0)
  await page.unrouteAll({ behavior: "ignoreErrors" })

  // A daemon that runs nothing keeps the page's shape and offers the start.
  await mockPM2(page, { inventory: () => ({ ...PM2, processes: [] }) })
  await page.goto("/processes/pm2")
  await expect(page.locator("[data-slot='host-identity']")).toContainText("0 applications")
  const empty = page.locator("[data-slot='empty-state']")
  await expect(empty).toContainText("PM2 runs nothing yet")
  await expect(empty.getByRole("button", { name: "Start application" })).toBeVisible()
  await page.unrouteAll({ behavior: "ignoreErrors" })

  // PM2 not answering: what happened in the product's words, PM2's own under
  // them, and the read again.
  let failing = true
  await mockPM2(page)
  // Registered after the mock, so it is asked first.
  await page.route("**/api/v1/pm2/", async (route) => {
    if (!failing) return route.fallback()
    await route.fulfill({
      status: 502,
      contentType: "application/json",
      body: JSON.stringify({ code: "command_failed", message: "pm2 jlist: exit status 1" }),
    })
  })
  for (const path of ["/processes/pm2", "/processes/pm2/api"]) {
    failing = true
    await page.goto(path)
    // Next announces a route change through an alert of its own.
    const failed = page.locator("[role='alert']").filter({ hasText: "PM2 did not answer" })
    await expect(failed).toBeVisible()
    await expect(failed).toContainText("pm2 jlist: exit status 1")
    await expect(failed.locator("img[src='/logos/pm2.svg']")).toBeVisible()
    failing = false
    await failed.getByRole("button", { name: "Try again" }).click()
    await expect(failed).toHaveCount(0)
  }
  await expect(identity(page)).toContainText("cluster of 2")
  await page.unrouteAll({ behavior: "ignoreErrors" })

  // Two accounts each running an `api`: the page asks which, and the account
  // in the address answers.
  await mockPM2(page, {
    inventory: () => ({
      available: true,
      daemons: [DAEMON, { ...DAEMON, account: "staging", home: "/home/staging" }],
      processes: [
        API_0,
        API_2,
        { ...API_0, daemonId: "staging", pid: 5021, user: "staging", execMode: "fork_mode" },
      ],
    }),
  })
  await page.goto("/processes/pm2/api")
  await expect(page.getByText("Two accounts run an application named api")).toBeVisible()
  await page.getByRole("link", { name: /api\s*staging/ }).click()
  await expect(page).toHaveURL(/\/processes\/pm2\/api\?account=staging$/)
  await expect(identity(page)).toContainText("fork")
  await expect(identity(page)).toContainText("staging")
  // The list beside it says whose each is, and walking it keeps the account.
  await expect(rail(page).getByRole("button", { name: "api, deploy" })).toBeVisible()
  await page.keyboard.press("Alt+ArrowUp")
  await expect(page).toHaveURL(/\/processes\/pm2\/api\?account=deploy$/)
  await expect(identity(page)).toContainText("cluster of 2")
})
