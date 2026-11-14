import { expect, test } from "@playwright/test"
import { json, mockProxy, user } from "./proxy-fixtures"
import { risen } from "./risen"

test("an administrator can add renewal failure and served certificate alerts", async ({ page }) => {
  await mockProxy(page, { included: true })
  const rules: Array<Record<string, unknown>> = []
  const created: Array<Record<string, unknown>> = []
  await page.route("**/api/v1/proxy/alerts", (route) =>
    json(route, { rules, subjects: [], history: [], intervalSeconds: 300 }),
  )
  await page.route("**/api/v1/deploy/notifications", (route) => json(route, []))
  await page.route("**/api/v1/proxy/alerts/rules", async (route) => {
    const body = route.request().postDataJSON() as Record<string, unknown>
    created.push(body)
    const rule = { id: rules.length + 1, ...body }
    rules.push(rule)
    await route.fulfill({
      status: 201,
      contentType: "application/json",
      body: JSON.stringify(rule),
    })
  })
  // Alerts is a page of its own, reached from the menu at the end of the overview's line.
  await page.goto("/proxy")
  await page
    .locator("[data-slot='host-identity']")
    .getByRole("button", { name: "More nginx actions" })
    .click()
  await page.getByRole("menuitem", { name: "Alerts" }).click()
  await expect(page).toHaveURL(/\/proxy\/alerts$/)
  const sheet = page.getByRole("region", { name: "Rules" })
  await expect(sheet).toBeVisible()

  for (const [label, kind] of [
    ["Renewal failed", "renewal_failed"],
    ["Served certificate differs", "served_drift"],
  ]) {
    await page.getByRole("button", { name: "Add rule" }).click()
    const form = page.getByRole("dialog", { name: "Add alert" })
    await form.getByRole("button", { name: new RegExp(label) }).click()
    await form.getByRole("button", { name: "Add alert" }).click()
    await expect(sheet.getByText(label, { exact: true })).toBeVisible()
    expect(created.at(-1)).toMatchObject({ kind, params: {}, channels: [] })
  }

  await page.getByRole("button", { name: "Add rule" }).click()
  const gradeForm = page.getByRole("dialog", { name: "Add alert" })
  await gradeForm.getByRole("button", { name: /Watched TLS grade below minimum/ }).click()
  await gradeForm
    .getByRole("radiogroup", { name: "Minimum TLS grade" })
    .getByRole("radio", { name: "B" })
    .click()
  await gradeForm.getByRole("button", { name: "Add alert" }).click()
  await expect(sheet.getByText("Watched TLS grade below minimum", { exact: true })).toBeVisible()
  expect(created.at(-1)).toMatchObject({ kind: "watch_grade_below", params: { grade: "B" } })
})

test("the alerts page wires what is watched to each channel and leads with what fires", async ({
  page,
}) => {
  await mockProxy(page, { included: true })
  const now = new Date().toISOString()
  await page.route("**/api/v1/proxy/alerts", (route) =>
    json(route, {
      intervalSeconds: 300,
      lastPass: now,
      rules: [
        {
          id: 1,
          kind: "site_errors",
          params: { threshold: 5, minutes: 15, minRequests: 20 },
          channels: [],
          enabled: true,
          createdAt: now,
          updatedAt: now,
        },
      ],
      subjects: [
        {
          ruleId: 1,
          kind: "site_errors",
          subject: "legacy.example.com",
          level: "danger",
          label: "legacy.example.com",
          detail: "12% answered 5xx",
          state: "firing",
          since: now,
          muted: false,
        },
      ],
      history: [],
    }),
  )
  await page.route("**/api/v1/deploy/notifications", (route) =>
    json(route, [
      {
        id: 1,
        name: "#ops",
        kind: "slack",
        url: "",
        target: "hooks.slack.com/services/T0",
        events: [],
        enabled: true,
        createdAt: now,
        updatedAt: now,
      },
    ]),
  )
  await page.goto("/proxy")
  // The overview's line says what fires, and the words lead to it.
  await expect(
    page.locator("[data-slot='host-identity']").getByRole("link", { name: "1 alert firing" }),
  ).toHaveAttribute("href", "/proxy/alerts")

  await page.goto("/proxy/alerts")
  const watched = page.getByRole("group", { name: "What the rules watch" })
  await expect(watched).toContainText("1 firing")
  // Certificates are drawn as Let's Encrypt, the channel as its own service.
  await expect(watched.locator("img[src='/logos/lets-encrypt.svg']")).toHaveCount(1)
  await expect(
    page.getByRole("group", { name: "Who is told" }).locator("img[src='/logos/slack.svg']"),
  ).toHaveCount(1)
  const firing = page.getByRole("region", { name: "Firing" })
  await expect(firing.getByText("legacy.example.com")).toBeVisible()
  await expect(firing.getByRole("button", { name: "Mute legacy.example.com" })).toBeVisible()
  // A rule is a card that opens its form.
  await page.getByRole("button", { name: "Edit Site failing" }).click()
  await expect(page.getByRole("dialog", { name: "Edit alert" })).toBeVisible()
})

const noRules = { rules: [], subjects: [], history: [], intervalSeconds: 300 }

for (const width of [1280, 390]) {
  test(`the alerts page opens on what it is, with Add rule as its command, at ${width}`, async ({
    page,
  }) => {
    await page.setViewportSize({ width, height: 900 })
    await mockProxy(page, { included: true })
    let release = () => {}
    const held = new Promise<void>((resolve) => (release = resolve))
    await page.route("**/api/v1/proxy/alerts", async (route) => {
      await held
      return json(route, noRules)
    })
    await page.route("**/api/v1/deploy/notifications", (route) => json(route, []))
    await page.goto("/proxy/alerts")

    // The line is there while the rules are read, where it will stand.
    const identity = page.locator("[data-slot='host-identity']")
    await expect(identity).toHaveAttribute("data-layout", "project")
    await expect(identity.getByText("Reading the rules…")).toBeVisible()
    await risen(page)
    const loading = await identity.boundingBox()

    release()
    await expect(identity.getByText("No rules on", { exact: true })).toBeVisible()
    await expect(identity.getByText("telling nobody yet", { exact: true })).toBeVisible()
    await risen(page)
    const loaded = await identity.boundingBox()
    expect(loaded?.y).toBe(loading?.y)
    expect(loaded?.height).toBe(loading?.height)

    // No way back above it and no row of figures: the rail names the section.
    await expect(page.locator("[data-slot='page-context']")).toHaveCount(0)
    await expect(page.locator("[data-slot='stat-grid']")).toHaveCount(0)
    // Add rule is the line's one command, and nowhere else on the page.
    const add = page.getByRole("button", { name: "Add rule" })
    await expect(add).toHaveCount(1)
    await expect(identity.getByRole("button", { name: "Add rule" })).toHaveAttribute(
      "data-variant",
      "default",
    )
    // The server in the picture says what it does with a rule, which a
    // paragraph under Rules used to.
    await expect(page.getByText("checks every 5 min")).toBeVisible()
    await expect(page.getByText(/Nobody is told when a certificate runs low/)).toHaveCount(0)

    // The catalogue: every kind a card of one height, a few words on it and
    // the sentence behind them.
    const rules = page.getByRole("region", { name: "Rules" })
    const cards = rules.locator("[data-slot='choice-card']")
    await expect(cards).toHaveCount(10)
    const heights = await cards.evaluateAll((all) =>
      all.map((card) => Math.round(card.getBoundingClientRect().height)),
    )
    expect(new Set(heights).size).toBe(1)
    await expect(cards.filter({ hasText: "Certificate expiring" })).toContainText(
      "21, 7, 3 and 1 days left",
    )
    await expect(
      rules.getByTitle("Told once at each threshold a certificate crosses."),
    ).toHaveCount(1)
    // A card still opens its own rule.
    await cards.filter({ hasText: "Upstream down" }).click()
    const form = page.getByRole("dialog", { name: "Add alert" })
    await expect(form.getByLabel("Down for")).toHaveValue("5")
    // The field's help is behind its label, not under it.
    await expect(form.getByText("From 5 minutes to a day; checked every 5 minutes.")).toHaveCount(0)
    await form.getByRole("button", { name: "What this setting does" }).hover()
    await expect(page.getByRole("tooltip")).toContainText("From 5 minutes to a day")
    await page.keyboard.press("Escape")

    const overflow = await page
      .locator("[data-slot='page']")
      .evaluate((element) => element.scrollWidth > element.clientWidth + 1)
    expect(overflow).toBe(false)
  })
}

test("the line counts the rules that are on, what fires and who is told", async ({ page }) => {
  await mockProxy(page, { included: true })
  const now = new Date().toISOString()
  const rule = (id: number, kind: string, enabled: boolean) => ({
    id,
    kind,
    params: {},
    channels: [],
    enabled,
    createdAt: now,
    updatedAt: now,
  })
  await page.route("**/api/v1/proxy/alerts", (route) =>
    json(route, {
      intervalSeconds: 300,
      lastPass: now,
      rules: [
        rule(1, "cert_expired", true),
        rule(2, "engine_down", true),
        rule(3, "renewal_failed", false),
      ],
      subjects: [
        {
          ruleId: 1,
          kind: "cert_expired",
          subject: "old.example.com",
          level: "danger",
          label: "old.example.com",
          detail: "expired yesterday",
          state: "firing",
          since: now,
          muted: false,
        },
      ],
      history: [],
    }),
  )
  await page.route("**/api/v1/deploy/notifications", (route) =>
    json(route, [
      {
        id: 1,
        name: "#ops",
        kind: "slack",
        url: "",
        target: "hooks.slack.com/services/T0",
        events: [],
        enabled: true,
        createdAt: now,
        updatedAt: now,
      },
    ]),
  )
  await page.goto("/proxy/alerts")
  const identity = page.locator("[data-slot='host-identity']")
  await expect(identity.getByText("2 rules on", { exact: true })).toBeVisible()
  await expect(identity.getByText("1 firing", { exact: true })).toBeVisible()
  await expect(identity.getByText("telling 1 channel", { exact: true })).toBeVisible()
  await expect(identity.getByText("1 paused", { exact: true })).toBeVisible()
  // The list's count stays the list's.
  await expect(page.getByRole("region", { name: "Rules" })).toContainText("3 rules · 1 paused")
  // Check now is Firing's own verb, and still asks.
  let asked = 0
  await page.route("**/api/v1/proxy/alerts/evaluate", (route) => {
    asked += 1
    return json(route, {})
  })
  await page
    .getByRole("region", { name: "Firing" })
    .getByRole("button", { name: "Check now" })
    .click()
  await expect.poll(() => asked).toBe(1)
})

test("an account that only reads is told the rules are an administrator's, and the server is not asked", async ({
  page,
}) => {
  await mockProxy(page, { included: true })
  await page.route("**/api/v1/auth/session", (route) =>
    json(route, { ...user, capabilities: ["read"], user: { ...user.user, role: "viewer" } }),
  )
  const asked: string[] = []
  for (const path of ["proxy/alerts", "deploy/notifications"]) {
    await page.route(`**/api/v1/${path}`, (route) => {
      asked.push(path)
      return route.fulfill({
        status: 403,
        contentType: "application/json",
        body: JSON.stringify({ error: { code: "forbidden", message: "Not allowed" } }),
      })
    })
  }
  await page.goto("/proxy/alerts")
  const identity = page.locator("[data-slot='host-identity']")
  await expect(identity.getByText("Administrators only")).toBeVisible()
  await expect(page.getByText("Only an administrator reads and sets the alert rules")).toBeVisible()
  // It read the server's refusal as a failure to retry.
  await expect(page.getByText("Not allowed")).toHaveCount(0)
  await expect(page.getByRole("button", { name: /Add rule|Check now|Try again/ })).toHaveCount(0)
  expect(asked).toEqual([])
})
