import { expect, test, type Locator, type Page } from "@playwright/test"
import { expectSaved, json, mockProject, saveSettings, user } from "./deploy-fixture"

async function fitsViewport(page: Page, menu: Locator) {
  const bounds = await menu.boundingBox()
  const viewport = page.viewportSize()!
  expect(bounds).not.toBeNull()
  expect(bounds!.x).toBeGreaterThanOrEqual(0)
  expect(bounds!.y).toBeGreaterThanOrEqual(0)
  expect(bounds!.x + bounds!.width).toBeLessThanOrEqual(viewport.width)
  expect(bounds!.y + bounds!.height).toBeLessThanOrEqual(viewport.height)
}

for (const width of [1280, 1720, 390]) {
  test(`ownership opens as a plain list and saves the choice at ${width}px`, async ({ page }) => {
    await page.setViewportSize({ width, height: 900 })
    const fixture = await mockProject(page)
    await page.goto("/deploy/7/settings/domains")
    const trigger = page.getByRole("combobox", { name: "Ownership of api.example.test" })
    await expect(trigger).toHaveText("Managed")
    await expect(trigger).toHaveAttribute("data-variant", "outline")
    await expect(trigger).toHaveAccessibleDescription("removed with the project")
    await trigger.scrollIntoViewIfNeeded()
    const triggerBounds = (await trigger.boundingBox())!
    await trigger.click()
    const menu = page.getByRole("listbox")
    await expect(menu).toBeVisible()
    await expect(menu.getByRole("option")).toHaveText(["Managed", "Linked"])
    await expect(menu.locator("[data-slot=menu-item-hint]")).toHaveCount(0)
    await fitsViewport(page, menu)
    const menuBounds = (await menu.boundingBox())!
    expect(menuBounds.y).toBeGreaterThanOrEqual(triggerBounds.y + triggerBounds.height + 3)
    expect(menuBounds.width).toBeLessThan(180)
    if (width === 390) {
      await expect
        .poll(async () => (await menu.getByRole("option").first().boundingBox())?.height ?? 0)
        .toBeGreaterThanOrEqual(44)
    }
    await page.screenshot({ path: test.info().outputPath(`domains-${width}.png`), fullPage: true })
    if (width === 1280) {
      await page.screenshot({
        path: test.info().outputPath("ownership-detail.png"),
        clip: {
          x: Math.max(0, triggerBounds.x - 12),
          y: triggerBounds.y - 12,
          width: Math.min(420, width - triggerBounds.x),
          height: triggerBounds.height + menuBounds.height + 32,
        },
      })
    }
    await menu.getByRole("option", { name: "Linked", exact: true }).click()
    await expect(menu).toHaveCount(0)
    await expect(trigger).toBeFocused()
    await expect(trigger).toHaveText("Linked")
    await expect(trigger).toHaveAccessibleDescription("never removed")
    await saveSettings(page)
    await expectSaved(page)
    expect(fixture.configuration().domains[0].ownership).toBe("linked")
  })
}

test("ownership keeps keyboard selection, typeahead, Escape and outside dismissal", async ({
  page,
}) => {
  await mockProject(page)
  await page.goto("/deploy/7/settings/domains")
  const trigger = page.getByRole("combobox", { name: "Ownership of api.example.test" })
  const managed = page.getByRole("option", { name: "Managed", exact: true })
  const linked = page.getByRole("option", { name: "Linked", exact: true })
  await trigger.focus()
  await trigger.press("ArrowDown")
  await expect(managed).toBeFocused()
  await page.keyboard.press("End")
  await expect(linked).toBeFocused()
  await page.keyboard.press("Enter")
  await expect(trigger).toHaveText("Linked")
  await expect(trigger).toBeFocused()
  await trigger.press("Enter")
  await expect(linked).toBeFocused()
  await page.keyboard.press("m")
  await expect(managed).toBeFocused()
  await page.keyboard.press("Enter")
  await expect(trigger).toHaveText("Managed")
  await trigger.press("Enter")
  await expect(managed).toBeFocused()
  await page.keyboard.press("Escape")
  await expect(page.getByRole("listbox")).toHaveCount(0)
  await expect(trigger).toBeFocused()
  await trigger.click()
  await expect(managed).toBeFocused()
  await page.mouse.click(1200, 800)
  await expect(page.getByRole("listbox")).toHaveCount(0)
})

test("storage ownership shares the plain options including Observed", async ({ page }) => {
  await mockProject(page)
  await page.goto("/deploy/7/settings/storage")
  const trigger = page.getByRole("combobox", { name: "Ownership of /data" })
  await trigger.click()
  const menu = page.getByRole("listbox")
  await expect(menu.getByRole("option")).toHaveText(["Managed", "Linked", "Observed"])
  await menu.getByRole("option", { name: "Observed", exact: true }).click()
  await expect(trigger).toHaveText("Observed")
  await expect(trigger).toHaveAccessibleDescription("watched only; never removed")
})

test("volume ownership uses the same control and read-only accounts cannot change it", async ({
  page,
}) => {
  await mockProject(page, { showcase: true })
  await page.goto("/deploy/7/settings/databases")
  const trigger = page.getByRole("combobox", { name: "Ownership of api-data" })
  await trigger.click()
  await expect(page.getByRole("listbox").getByRole("option")).toHaveText([
    "Managed",
    "Linked",
    "Observed",
  ])
  await page.keyboard.press("Escape")
  await page.route("**/api/v1/auth/session", (route) =>
    json(route, { ...user, capabilities: ["read"], user: { ...user.user, role: "viewer" } }),
  )
  await page.reload()
  await expect(trigger).toBeDisabled()
})

test("a long option list stays within the window and scrolls to keyboard selection", async ({
  page,
}) => {
  await page.setViewportSize({ width: 390, height: 600 })
  await mockProject(page)
  await page.route("**/api/v1/deploy/credentials", (route) =>
    json(
      route,
      Array.from({ length: 40 }, (_, index) => ({
        id: index + 1,
        name: `Git account ${index + 1}`,
        kind: "git_ssh",
        target: "git.example.test",
        hasSecret: true,
      })),
    ),
  )
  await page.goto("/deploy/7/settings/general")
  const trigger = page.getByRole("combobox", { name: "Credential", exact: true })
  await trigger.scrollIntoViewIfNeeded()
  await trigger.click()
  const menu = page.getByRole("listbox")
  await fitsViewport(page, menu)
  await page.keyboard.press("End")
  const last = menu.getByRole("option", { name: "Git account 40", exact: true })
  await expect(last).toBeFocused()
  const menuBounds = (await menu.boundingBox())!
  const itemBounds = (await last.boundingBox())!
  expect(itemBounds.y).toBeGreaterThanOrEqual(menuBounds.y)
  expect(itemBounds.y + itemBounds.height).toBeLessThanOrEqual(menuBounds.y + menuBounds.height)
  await page.keyboard.press("Enter")
  await expect(trigger).toHaveText("Git account 40")
  await expect(trigger).toBeFocused()
})

test("credential metadata is below its name and never copied into the trigger", async ({
  page,
}) => {
  await mockProject(page)
  await page.route("**/api/v1/deploy/credentials", (route) =>
    json(route, [
      { id: 5, name: "Production Git", kind: "git_ssh", target: "github.com", hasSecret: true },
      {
        id: 6,
        name: "Archive Git",
        kind: "git_ssh",
        target: "git.example.test/teams/platform/archived/repositories",
        hasSecret: true,
      },
    ]),
  )
  await page.goto("/deploy/7/settings/general")
  const trigger = page.getByRole("combobox", { name: "Credential", exact: true })
  await trigger.click()
  const menu = page.getByRole("listbox")
  const option = menu.getByRole("option", { name: "Production Git", exact: true })
  const name = (await option.locator("[data-slot=select-item-text]").boundingBox())!
  const hint = (await option.locator("[data-slot=menu-item-hint]").boundingBox())!
  expect(name.height).toBeLessThanOrEqual(24)
  expect(hint.y).toBeGreaterThanOrEqual(name.y + name.height)
  expect(Math.abs(hint.x - name.x)).toBeLessThan(1)
  await page.screenshot({ path: test.info().outputPath("credentials-desktop.png"), fullPage: true })
  await option.click()
  await expect(trigger).toHaveText("Production Git")
  await trigger.click()
  await page.keyboard.press("Escape")
  await expect(trigger).toBeFocused()
  await page.setViewportSize({ width: 390, height: 844 })
  await trigger.click()
  await fitsViewport(page, menu)
  expect(
    await page.evaluate(() => document.documentElement.scrollWidth - innerWidth),
  ).toBeLessThanOrEqual(1)
  await page.screenshot({ path: test.info().outputPath("credentials-mobile.png"), fullPage: true })
})

test("action menus use the same surface and respect reduced motion", async ({ page }) => {
  await page.emulateMedia({ reducedMotion: "reduce" })
  await mockProject(page)
  await page.goto("/deploy/7/settings/domains")
  await page.getByRole("button", { name: "Actions for api.example.test" }).click()
  const menu = page.getByRole("menu")
  await expect(menu).toBeVisible()
  await fitsViewport(page, menu)
  expect(await menu.evaluate((node) => getComputedStyle(node).animationName)).toBe("none")
  await page.screenshot({ path: test.info().outputPath("actions-desktop.png"), fullPage: true })
  await page.keyboard.press("Escape")
  await expect(page.getByRole("button", { name: "Actions for api.example.test" })).toBeFocused()
})
