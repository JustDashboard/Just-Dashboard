import { expect, test } from "@playwright/test"
import { mockProject } from "./deploy-fixture"

/**
 * Traffic alerts on Automation settings: rules read as sentences with their
 * state, a new one is a form that speaks its own sentence back, and a test
 * delivery says where it went.
 */
test.describe("traffic alerts", () => {
  test("rules read as sentences, and a new one is added from a form", async ({ page }) => {
    await mockProject(page)
    await page.goto("/deploy/7/settings/automation")
    const card = page.getByRole("region", { name: "Traffic alerts" }).or(page.locator("#alerts"))
    await expect(card.getByText("Failing requests")).toBeVisible()
    await expect(card.getByText("more than 1% of requests fail over 5 min")).toBeVisible()
    await expect(card.getByText(/Firing · 4\.2% failing/)).toBeVisible()
    // The last reading is drawn against the rule's line, not only spelled out.
    await expect(card.getByRole("meter", { name: "4.2% failing against a 1% line" })).toBeVisible()
    // Rules exist, so the starter cards for an empty section are not drawn.
    await expect(card.getByRole("button", { name: /^No traffic/ })).toHaveCount(0)

    await card.getByRole("button", { name: "Add alert" }).click()
    // The kind is picked by its card, and the unit sits inside the limit's edge.
    await page
      .getByRole("group", { name: "Alert kind" })
      .getByRole("button", { name: /^Slow responses/ })
      .click()
    await expect(page.getByRole("dialog").getByText("ms", { exact: true })).toBeVisible()
    await page.getByLabel("Alert limit").fill("800")
    await page.getByLabel("Alert window in minutes").fill("10")
    // The form speaks the rule back before it is saved.
    await expect(page.getByText("Slow responses: p95 above 800ms over 10 min.")).toBeVisible()
    await page.getByRole("button", { name: "Add alert" }).click()
    await expect(card.getByText("p95 above 800ms over 10 min")).toBeVisible()
  })

  // The Logs page is where a reader finds out that nobody would have been told,
  // so the first rule is added there, in a sheet over the readings, with the
  // same form Automation settings uses.
  test("the logs page adds the first rule from a sheet over its readings", async ({ page }) => {
    await mockProject(page)
    await page.goto("/deploy/7/logs")
    await expect(page.getByText("1 firing")).toBeVisible()
    // Start from a deployment with no rule at all.
    await page.evaluate(() => fetch("/api/v1/deploy/7/alerts/91", { method: "DELETE" }))
    await page.reload()

    await expect(page.getByText("No alerts", { exact: true })).toBeVisible()
    await page.getByRole("button", { name: "Add alert" }).click()
    // The same title Automation's sheet carries: one form, one name.
    const sheet = page.getByRole("dialog", { name: "Add alert" })
    await expect(sheet).toBeVisible()
    // It opens on what the rule would watch today.
    await expect(sheet.getByText("1.0% failing")).toBeVisible()
    await sheet.getByLabel("Alert limit").fill("2")
    await sheet.getByRole("button", { name: "Add alert" }).click()

    await expect(sheet).toHaveCount(0)
    await expect(page.getByText("All quiet", { exact: true })).toBeVisible()
    await expect(
      page.getByText(/1 of 1 alert watching — more than 2% of requests fail over 5 min/),
    ).toBeVisible()
  })
})
