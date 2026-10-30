import { expect, test } from "@playwright/test"
import { json, mockProxy } from "./proxy-fixtures"

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
  // Alerts is a page of its own now, reached from the overview's bar.
  await page.goto("/proxy")
  await page.locator("[data-slot='host-identity']").getByRole("link", { name: "Alerts" }).click()
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
  // The overview's bar counts what fires beside the way to it.
  await expect(page.getByRole("link", { name: "Alerts, 1 firing" })).toBeVisible()

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
