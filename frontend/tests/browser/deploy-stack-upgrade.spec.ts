import { expect, test } from "@playwright/test"
import { json, user } from "./deploy-fixture"
import { FLARESOLVERR_UPGRADE, mockStackProject, stackState } from "./deploy-stack-fixture"

/**
 * "Add FlareSolverr" on the Stack page in the states the happy path in
 * `deploy-stack-page.spec.ts` does not reach: nothing left to add, a refusal
 * and a failure from the route, someone who is not an administrator, and a
 * phone's width.
 */

test("Add FlareSolverr with nothing left to add closes and reads the stack again", async ({
  page,
}) => {
  const stack = await mockStackProject(page, {
    state: { ...stackState("ready"), upgrade: FLARESOLVERR_UPGRADE },
    upgrade: "stack_upgrade_none",
  })
  await page.goto("/deploy/7/stack")
  const step = page.locator("[data-slot='stack-upgrade']")
  await step.getByRole("button", { name: "Add FlareSolverr" }).click()
  const dialog = page.getByRole("dialog", { name: "Add FlareSolverr" })
  const before = stack.reads()
  await dialog.getByRole("button", { name: "Add and deploy" }).click()
  await expect(dialog).toBeHidden()
  await expect(page.locator("[data-sonner-toast]")).toContainText(
    "The stack already runs every app.",
  )
  await expect.poll(() => stack.reads()).toBeGreaterThan(before)
  await expect(step).toHaveCount(0)
  await expect(page.getByText("stack_upgrade_none")).toHaveCount(0)
})

test("Add FlareSolverr refused or failed is answered in the dialog, which stays open", async ({
  page,
}) => {
  await mockStackProject(page, {
    state: { ...stackState("ready"), upgrade: FLARESOLVERR_UPGRADE },
  })
  let answer: "stack_not_preset" | "broken" = "stack_not_preset"
  await page.route("**/api/v1/deploy/7/stack/upgrade", (route) =>
    route.fulfill({
      status: answer === "broken" ? 500 : 409,
      contentType: "application/json",
      body: JSON.stringify({
        error:
          answer === "broken"
            ? { code: "internal", message: "the source could not be saved" }
            : { code: "stack_not_preset", message: "not the stack's template" },
      }),
    }),
  )
  await page.goto("/deploy/7/stack")
  await page
    .locator("[data-slot='stack-upgrade']")
    .getByRole("button", { name: "Add FlareSolverr" })
    .click()
  const dialog = page.getByRole("dialog", { name: "Add FlareSolverr" })
  await dialog.getByRole("button", { name: "Add and deploy" }).click()
  await expect(dialog).toContainText("Nothing was added")
  await expect(dialog).toContainText(
    "The deployment's source is no longer the stack's template, so there is nothing of the stack's to add.",
  )
  await expect(dialog.getByText("stack_not_preset")).toHaveCount(0)
  answer = "broken"
  await dialog.getByRole("button", { name: "Add and deploy" }).click()
  await expect(dialog).toContainText("The apps were not added")
  await expect(dialog).toContainText("the source could not be saved")
  await expect(dialog).not.toContainText("Nothing was added")
  await expect(dialog).toBeVisible()
})

test("someone who is not an administrator reads what is new and who can add it, with no button", async ({
  page,
}) => {
  const stack = await mockStackProject(page, {
    state: { ...stackState("ready"), upgrade: FLARESOLVERR_UPGRADE },
  })
  await page.route("**/api/v1/auth/session", (route) =>
    json(route, {
      ...user,
      capabilities: ["read", "service.control"],
      user: { ...user.user, role: "operator" },
    }),
  )
  await page.goto("/deploy/7/stack")
  const step = page.locator("[data-slot='stack-upgrade']")
  await expect(step).toContainText("New in the stack: FlareSolverr")
  await expect(step).toContainText("An administrator can add it.")
  await expect(step.getByRole("button")).toHaveCount(0)
  expect(stack.upgradeRequests()).toEqual([])
})

test("on a phone the new app's step and its dialog fit the width", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 })
  await mockStackProject(page, {
    state: { ...stackState("ready"), upgrade: FLARESOLVERR_UPGRADE },
  })
  await page.goto("/deploy/7/stack")
  const step = page.locator("[data-slot='stack-upgrade']")
  const add = step.getByRole("button", { name: "Add FlareSolverr" })
  await expect(add).toBeInViewport()
  const sideways = await page.evaluate(() =>
    [document.documentElement, ...document.querySelectorAll("[data-slot=page]")]
      .map((el) => el.parentElement ?? el)
      .some((el) => el.scrollWidth > el.clientWidth + 1),
  )
  expect(sideways).toBe(false)
  await add.click()
  const dialog = page.getByRole("dialog", { name: "Add FlareSolverr" })
  const box = await dialog.boundingBox()
  expect(box!.x).toBeGreaterThanOrEqual(0)
  expect(box!.x + box!.width).toBeLessThanOrEqual(390)
  const row = dialog.getByRole("list", { name: "The apps added" }).getByRole("listitem")
  await expect(row).toHaveCount(1)
  expect(Math.round((await row.boundingBox())!.height)).toBe(36)
  await expect(dialog.getByRole("button", { name: "Add and deploy" })).toBeInViewport()
})

test("with changes saved but not deployed, Add FlareSolverr says they go out with it", async ({
  page,
}) => {
  const note = "Every change saved to the deployment but not deployed yet goes out with it."
  await mockStackProject(page, {
    state: { ...stackState("ready"), upgrade: FLARESOLVERR_UPGRADE },
    pendingChanges: true,
  })
  await page.goto("/deploy/7/stack")
  const step = page.locator("[data-slot='stack-upgrade']")
  await expect(step).toContainText(`your library, settings, domains and login stay`)
  await expect(step).toContainText(note)
  await step.getByRole("button", { name: "Add FlareSolverr" }).click()
  const dialog = page.getByRole("dialog", { name: "Add FlareSolverr" })
  await expect(dialog).toContainText("version 1.2.0")
  await expect(dialog).toContainText(note)

  // With nothing saved and not deployed, neither says it.
  await mockStackProject(page, { state: { ...stackState("ready"), upgrade: FLARESOLVERR_UPGRADE } })
  await page.goto("/deploy/7/stack")
  await expect(step).toContainText("New in the stack: FlareSolverr")
  await expect(step).not.toContainText(note)
  await step.getByRole("button", { name: "Add FlareSolverr" }).click()
  await expect(dialog).toContainText("version 1.2.0")
  await expect(dialog).not.toContainText(note)
})
