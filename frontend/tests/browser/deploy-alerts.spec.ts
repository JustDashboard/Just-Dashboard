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

    await card.getByRole("button", { name: "Add an alert" }).click()
    await page.getByRole("combobox", { name: "Alert kind" }).click()
    await page.getByRole("option", { name: "Slow responses" }).click()
    await page.getByLabel("Alert limit").fill("800")
    await page.getByLabel("Alert window in minutes").fill("10")
    // The form speaks the rule back before it is saved.
    await expect(page.getByText("Slow responses: p95 above 800ms over 10 min.")).toBeVisible()
    await page.getByRole("button", { name: "Add alert" }).click()
    await expect(card.getByText("p95 above 800ms over 10 min")).toBeVisible()
  })
})
