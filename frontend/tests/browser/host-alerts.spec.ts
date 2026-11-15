import { expect, test, type Page } from "@playwright/test"
import { iso, json, mockHost, now, user } from "./host-fixture"
import { risen } from "./risen"

/**
 * The host's Alerts page under Metrics: rules over the machine's own readings,
 * drawn as the proxy's Alerts page is, under a line that says what they are
 * doing now and carries the page's verbs. Served by the host fixture, with
 * the alert routes answered here.
 */

const rule = (id: number, kind: string, params: object, more: object = {}) => ({
  id,
  kind,
  params,
  channels: [],
  enabled: true,
  createdAt: iso(now),
  updatedAt: iso(now),
  ...more,
})

/**
 * Four rules in the four states a row has: firing, unjudged, watching, paused.
 * The last pass is ten minutes before the fixture's clock, well past the 45
 * seconds inside which the page says "just now".
 */
const everyState = {
  intervalSeconds: 60,
  metrics: true,
  lastPass: iso(now - 600_000),
  rules: [
    rule(1, "container_down", { minutes: 2 }),
    rule(
      2,
      "cpu_high",
      { threshold: 90, minutes: 10 },
      { channels: [1], unjudged: "Only 3 of the 10 minutes it averages are recorded so far." },
    ),
    rule(3, "disk_filling", { threshold: 85 }),
    rule(4, "backup_failed", {}, { enabled: false }),
  ],
  subjects: [
    {
      ruleId: 1,
      kind: "container_down",
      subject: "shop-api-1",
      label: "shop-api-1",
      detail: "It exited with code 1.",
      state: "firing",
      since: iso(now - 300_000),
      muted: false,
    },
  ],
  history: [],
}

const nothing = { rules: [], subjects: [], history: [], intervalSeconds: 60, metrics: true }

const identity = (page: Page) => page.locator("[data-slot=host-identity]")

const slack = {
  id: 1,
  name: "#ops",
  kind: "slack",
  url: "",
  target: "hooks.slack.com/services/T0",
  events: [],
  enabled: true,
  createdAt: iso(now),
  updatedAt: iso(now),
}

test("an administrator reaches Alerts from Metrics and adds a disk and a container rule", async ({
  page,
}) => {
  await mockHost(page)
  const rules: Array<Record<string, unknown>> = []
  const created: Array<Record<string, unknown>> = []
  await page.route("**/api/v1/system/alerts", (route) =>
    json(route, { rules, subjects: [], history: [], intervalSeconds: 60, metrics: true }),
  )
  await page.route("**/api/v1/deploy/notifications", (route) => json(route, []))
  await page.route("**/api/v1/system/alerts/rules", async (route) => {
    const body = route.request().postDataJSON() as Record<string, unknown>
    created.push(body)
    const rule = { id: rules.length + 1, createdAt: iso(now), updatedAt: iso(now), ...body }
    rules.push(rule)
    await route.fulfill({
      status: 201,
      contentType: "application/json",
      body: JSON.stringify(rule),
    })
  })

  await page.goto("/metrics")
  // Metrics' first render, its charts, holds the main thread for seconds on
  // a busy host, and a press made during it is handled only after it; its
  // first reading is waited for as metrics-ui.spec.ts waits for it, so the
  // waits after the press measure the Alerts page and not Metrics.
  await expect(page.getByText("CPU 97%", { exact: true })).toBeVisible({ timeout: 20_000 })
  await page.locator("[data-slot='host-identity']").getByRole("link", { name: "Alerts" }).click()
  await expect(page).toHaveURL(/\/metrics\/alerts$/)
  const sheet = page.getByRole("region", { name: "Rules" })
  await expect(identity(page)).toContainText("checked every minute")

  await page.getByRole("button", { name: "Add rule" }).click()
  let form = page.getByRole("dialog", { name: "Add alert" })
  await form.getByRole("button", { name: /Disk filling up/ }).click()
  // A disk has a line and no minutes.
  await expect(form.getByLabel("Averaged over")).toHaveCount(0)
  await form.getByLabel("At or above").fill("101")
  await form.getByRole("button", { name: "Add alert" }).click()
  await expect(form.getByText("The line is a percentage between 10 and 99.")).toBeVisible()
  expect(created).toHaveLength(0)
  await form.getByLabel("At or above").fill("85")
  await form.getByRole("button", { name: "Add alert" }).click()
  await expect(sheet.getByText("Disk filling up", { exact: true })).toBeVisible()
  expect(created.at(-1)).toMatchObject({
    kind: "disk_filling",
    params: { threshold: 85 },
    channels: [],
  })

  await page.getByRole("button", { name: "Add rule" }).click()
  form = page.getByRole("dialog", { name: "Add alert" })
  await form.getByRole("button", { name: /Container down/ }).click()
  await expect(form.getByLabel("At or above")).toHaveCount(0)
  await form.getByLabel("Down for").fill("5")
  await form.getByRole("button", { name: "Add alert" }).click()
  await expect(sheet.getByText("Container down", { exact: true })).toBeVisible()
  expect(created.at(-1)).toMatchObject({ kind: "container_down", params: { minutes: 5 } })
})

test("the page wires what is watched to each channel, leads with what fires and says what it cannot judge", async ({
  page,
}) => {
  await mockHost(page)
  const tested: string[] = []
  const rules = [
    {
      id: 1,
      kind: "container_down",
      params: { minutes: 2 },
      channels: [],
      enabled: true,
      createdAt: iso(now),
      updatedAt: iso(now),
    },
    {
      id: 2,
      kind: "cpu_high",
      params: { threshold: 90, minutes: 10 },
      channels: [1],
      enabled: true,
      createdAt: iso(now),
      updatedAt: iso(now),
      unjudged: "Only 3 of the 10 minutes it averages are recorded so far.",
    },
  ]
  await page.route("**/api/v1/system/alerts", (route) =>
    json(route, {
      intervalSeconds: 60,
      metrics: true,
      lastPass: iso(now),
      rules,
      subjects: [
        {
          ruleId: 1,
          kind: "container_down",
          subject: "shop-api-1",
          label: "shop-api-1",
          detail: "It exited with code 1.",
          state: "firing",
          since: iso(now - 300_000),
          muted: false,
        },
      ],
      history: [
        {
          id: 1,
          ruleId: 1,
          kind: "container_down",
          subject: "shop-api-1",
          label: "shop-api-1",
          event: "host.alert.firing",
          detail: "It exited with code 1.",
          delivered: 1,
          failed: 0,
          muted: false,
          test: false,
          createdAt: iso(now - 240_000),
        },
      ],
    }),
  )
  await page.route("**/api/v1/deploy/notifications", (route) => json(route, [slack]))
  await page.route("**/api/v1/system/alerts/rules/*/test", async (route) => {
    tested.push(new URL(route.request().url()).pathname)
    await json(route, { delivered: 1, failed: 0 })
  })

  // Metrics counts what fires beside the way to it. The count is drawn in
  // the page's first render, after its charts: the alerts answer arrives in
  // about a second, and on a loaded host the main thread is then busy for
  // seconds more (3.6 s of long tasks between the answer and the count here,
  // 20 s with the processor throttled sixfold), so it is waited for as the
  // page's first reading is in metrics-ui.spec.ts.
  await page.goto("/metrics")
  await expect(page.getByRole("link", { name: "Alerts, 1 firing" })).toBeVisible({
    timeout: 20_000,
  })

  await page.goto("/metrics/alerts")
  const watched = page.getByRole("group", { name: "What the rules watch" })
  await expect(watched).toContainText("1 firing")
  // Containers are drawn as Docker, the channel as its own service.
  await expect(watched.locator("img[src='/logos/docker.svg']")).toHaveCount(1)
  await expect(
    page.getByRole("group", { name: "Who is told" }).locator("img[src='/logos/slack.svg']"),
  ).toHaveCount(1)
  const firing = page.getByRole("region", { name: "Firing" })
  await expect(firing.getByText("It exited with code 1.")).toBeVisible()
  await expect(firing.getByRole("button", { name: "Mute shop-api-1" })).toBeVisible()
  await expect(page.getByRole("list", { name: "Alert history" })).toContainText("told 1 channel")

  // A rule that cannot be judged says so, rather than looking quiet.
  const rulesList = page.getByRole("list", { name: "Alert rules" })
  await expect(rulesList.getByText("Cannot judge")).toBeVisible()
  await expect(rulesList.getByText(/Only 3 of the 10 minutes/)).toBeVisible()

  // A rule's test goes to its channels from its menu.
  await page.getByRole("button", { name: /Actions for CPU high/ }).click()
  await page.getByRole("menuitem", { name: "Send test" }).click()
  await expect.poll(() => tested).toEqual(["/api/v1/system/alerts/rules/2/test"])

  // A rule is a row that opens its form.
  await page.getByRole("button", { name: "Edit Container down" }).click()
  const form = page.getByRole("dialog", { name: "Edit alert" })
  await expect(form.getByLabel("Down for")).toHaveValue("2")
})

test("the page opens on what the rules are doing, with its verbs on that line", async ({
  page,
}) => {
  await mockHost(page)
  let checks = 0
  await page.route("**/api/v1/system/alerts", (route) => json(route, everyState))
  await page.route("**/api/v1/deploy/notifications", (route) =>
    json(route, [slack, { ...slack, id: 2, name: "On-call mail", kind: "email", enabled: false }]),
  )
  await page.route("**/api/v1/system/alerts/evaluate", async (route) => {
    checks++
    await json(route, everyState)
  })
  // The line says how long ago the rules were checked, and `now` is the
  // moment this file was loaded: a fresh worker read "just now" and one a
  // few minutes old "3m 20s ago". The page reads the fixture's own clock.
  await page.clock.setFixedTime(new Date(now))
  await page.goto("/metrics/alerts")
  const line = identity(page)
  await expect(line).toHaveAttribute("data-layout", "project")
  await expect(line.getByText("Host alerts", { exact: true })).toBeVisible()
  await expect(line.getByText("1 firing", { exact: true })).toHaveClass(/text-destructive/)
  await expect(line).toContainText("3 rules on, 1 paused")
  await expect(line).toContainText("telling 1 channel of 2")
  await expect(line).toContainText("checked 10m ago, every minute")
  // One command on the line; Check now is behind the menu, and neither
  // stands on a section's line any more.
  await expect(line.getByRole("button", { name: "Add rule" })).toBeVisible()
  await expect(page.getByRole("button", { name: "Add rule" })).toHaveCount(1)
  await expect(page.getByRole("button", { name: "Check now" })).toHaveCount(0)
  await line.getByRole("button", { name: "Alert actions" }).click()
  await page.getByRole("menuitem", { name: "Check now" }).click()
  await expect.poll(() => checks).toBe(1)
  // The line is the page's first block, over the picture, and no tile row.
  const first = (await line.boundingBox())!
  const picture = (await page.getByRole("group", { name: "What the rules watch" }).boundingBox())!
  expect(first.y).toBeLessThan(picture.y)
  await expect(page.locator("[data-slot=stat-grid]")).toHaveCount(0)
})

for (const width of [1280, 390]) {
  test(`a rule's row is one height in every state at ${width}`, async ({ page }) => {
    await page.setViewportSize({ width, height: 900 })
    await mockHost(page)
    await page.route("**/api/v1/system/alerts", (route) => json(route, everyState))
    await page.route("**/api/v1/deploy/notifications", (route) => json(route, [slack]))
    await page.goto("/metrics/alerts")
    const rows = page.getByRole("list", { name: "Alert rules" }).locator(":scope > li")
    await expect(rows).toHaveCount(4)
    // Why a rule cannot be judged is on its line, not under it.
    await expect(rows.nth(1)).toContainText("Only 3 of the 10 minutes")
    await expect(rows.nth(1)).toContainText("Cannot judge")
    await expect(rows.nth(3)).toContainText("Paused")
    await risen(page)
    const boxes = await rows.evaluateAll((all) => all.map((row) => row.getBoundingClientRect()))
    expect(new Set(boxes.map((box) => box.height)).size).toBe(1)
    // Each row starts one row and one gap under the row before it.
    const steps = boxes.slice(1).map((box, index) => box.y - boxes[index].y)
    expect(new Set(steps).size).toBe(1)
    expect(
      await page.evaluate(
        () => document.documentElement.scrollWidth > document.documentElement.clientWidth,
      ),
    ).toBe(false)
  })

  test(`the page holds its shape while the rules are read at ${width}`, async ({ page }) => {
    await page.setViewportSize({ width, height: 900 })
    await mockHost(page)
    let release = () => {}
    const held = new Promise<void>((resolve) => (release = resolve))
    await page.route("**/api/v1/system/alerts", async (route) => {
      await held
      await json(route, nothing)
    })
    await page.route("**/api/v1/deploy/notifications", (route) => json(route, []))
    await page.goto("/metrics/alerts")
    const line = identity(page)
    const picture = page.getByRole("group", { name: "What the rules watch" })
    const blocks = [
      line,
      picture,
      page.getByRole("region", { name: "Firing" }),
      page.getByRole("region", { name: "Rules" }),
    ]
    await expect(line.locator("[data-slot=skeleton]").first()).toBeVisible()
    await expect(picture).toBeVisible()
    await risen(page)
    const measure = () =>
      Promise.all(
        blocks.map(async (block) => {
          const box = (await block.boundingBox())!
          return { y: Math.round(box.y), height: Math.round(box.height) }
        }),
      )
    const before = await measure()
    release()
    await expect(line.getByText("No rules", { exact: true })).toBeVisible()
    await risen(page)
    const after = await measure()
    // The line, the picture and Firing stand where they stood; Rules starts
    // where it started (its templates are taller than the rows it waited as).
    expect(after.slice(0, 3)).toEqual(before.slice(0, 3))
    expect(after[3].y).toBe(before[3].y)
  })
}

test("with no rule the page offers what can be watched, a name and one fact each", async ({
  page,
}) => {
  await mockHost(page)
  await page.route("**/api/v1/system/alerts", (route) => json(route, nothing))
  await page.route("**/api/v1/deploy/notifications", (route) => json(route, []))
  await page.goto("/metrics/alerts")
  const rules = page.getByRole("region", { name: "Rules" })
  const cards = rules.getByRole("group", { name: "Rules to add" }).getByRole("button")
  await expect(cards).toHaveCount(8)
  await expect(cards.first()).toHaveText(/CPU high\s*90% or more over 10 min/)
  await expect(cards.nth(2)).toHaveText(/Disk filling up\s*90% of space or inodes/)
  // The paragraph over them, and the two sentences on each card, are gone.
  await expect(rules.locator("p")).toHaveCount(0)
  await expect(page.getByText(/Nobody is told when the CPU/)).toHaveCount(0)
  await expect(page.getByText(/so one spike is not an alert/)).toHaveCount(0)
  const heights = await cards.evaluateAll((all) =>
    all.map((card) => card.getBoundingClientRect().height),
  )
  expect(new Set(heights).size).toBe(1)
  // A card opens the form on its kind.
  await cards.nth(2).click()
  const form = page.getByRole("dialog", { name: "Add alert" })
  await expect(form.getByRole("button", { name: /Disk filling up/ })).toHaveAttribute(
    "aria-pressed",
    "true",
  )
  await form.getByRole("button", { name: "Cancel" }).click()

  // Nothing told and nobody to tell are each said with a mark and a way on.
  const channels = page.getByRole("region", { name: "Channels" })
  for (const logo of ["slack", "discord", "telegram"]) {
    await expect(channels.locator(`img[src="/logos/${logo}.svg"]`)).toBeVisible()
  }
  await expect(channels).toContainText("No channels yet")
  await expect(channels.getByRole("link", { name: "Add a channel" })).toHaveAttribute(
    "href",
    "/deploy/notifications",
  )
  await expect(
    page.getByRole("region", { name: "Recently told" }).locator("[data-slot=empty-state]"),
  ).toContainText("Nothing told yet")
})

test("an account that is not an administrator is told so, and the server is not asked", async ({
  page,
}) => {
  await mockHost(page)
  const asked: string[] = []
  page.on("request", (request) => asked.push(new URL(request.url()).pathname))
  await page.route("**/api/v1/auth/session", (route) =>
    json(route, { ...user, capabilities: ["read"], user: { ...user.user, role: "viewer" } }),
  )
  await page.goto("/metrics/alerts")
  const state = page.locator("[data-slot=empty-state]")
  await expect(state).toContainText("Alerts are an administrator's")
  await expect(state.getByRole("link", { name: "Back to Metrics" })).toHaveAttribute(
    "href",
    "/metrics",
  )
  await expect(page.getByRole("button", { name: "Add rule" })).toHaveCount(0)
  await expect(page.locator("[data-slot=error-state]")).toHaveCount(0)
  expect(asked).not.toContain("/api/v1/system/alerts")
  expect(asked).not.toContain("/api/v1/deploy/notifications")
})
