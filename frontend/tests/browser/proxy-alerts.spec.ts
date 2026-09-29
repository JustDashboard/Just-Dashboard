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
  await page.goto("/proxy")
  await page.getByRole("button", { name: "Alerts" }).click()
  const sheet = page.getByRole("dialog", { name: "Alerts" })
  await expect(sheet).toBeVisible()

  for (const [label, kind] of [
    ["Renewal failed", "renewal_failed"],
    ["Served certificate differs", "served_drift"],
  ]) {
    await sheet.getByRole("button", { name: "Add rule" }).click()
    const form = page.getByRole("dialog", { name: "Add alert" })
    await form.getByRole("button", { name: new RegExp(label) }).click()
    await form.getByRole("button", { name: "Add alert" }).click()
    await expect(sheet.getByText(label, { exact: true })).toBeVisible()
    expect(created.at(-1)).toMatchObject({ kind, params: {}, channels: [] })
  }

  await sheet.getByRole("button", { name: "Add rule" }).click()
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
