import { expect, test } from "@playwright/test"
import { mockProject } from "./deploy-fixture"
import { json } from "./host-fixture"
import { mockOverview } from "./overview-fixture"
import { widestDuringLap } from "./border-light"

/**
 * The light that goes round a frame while work happens in it is a square
 * centred on the frame's edge, so half of it is outside the frame. A mask
 * hides that half, but it still counted as content: on a phone, where a row
 * ends sixteen pixels from the window's edge, the page widened each time the
 * light went down the row's right side. A running database transfer was
 * clipped for it in its own file; a busy `ChoiceRow` and a working
 * `ProductCard` held theirs unclipped. The light's own box clips now
 * (`ui/border-beam.tsx`), and these are the two that were left.
 */
test.use({ viewport: { width: 390, height: 844 } })

test("a busy row's border light never reaches past the page on a phone", async ({ page }) => {
  await mockProject(page)
  await page.goto("/deploy/7/deployments")
  const row = page.locator("[data-slot=choice-row]").filter({
    has: page.locator("[data-slot=border-beam]"),
  })
  await expect(row).toHaveCount(1)
  // The row runs to the page's gutter, which is where the light would pass it.
  const box = (await row.boundingBox())!
  expect(390 - (box.x + box.width)).toBeLessThan(30)

  const clipped = await widestDuringLap(row)
  // The light's lap, and the row's own rise, which also moves by transform.
  expect(clipped.laps).toBeGreaterThanOrEqual(1)
  expect(clipped.most).toBeLessThanOrEqual(1)
  // The check sees what it is for: without the clip the light's box widens the page.
  expect((await widestDuringLap(row, true)).most).toBeGreaterThan(10)
})

test("a working card's border light never reaches past the page on a phone", async ({ page }) => {
  await mockOverview(page)
  let finish = () => {}
  const made = new Promise<void>((resolve) => (finish = resolve))
  await page.route("**/api/v1/boards/", async (route) => {
    if (route.request().method() !== "POST") return route.fallback()
    await made
    return json(route, { id: 9, name: "Untitled drawing", revision: 1, scene: {} })
  })
  await page.goto("/")
  const card = page.locator("[data-slot=shortcut]").filter({ hasText: "New drawing" })
  await card.click()
  const light = card.locator("[data-slot=border-beam]")
  await expect(light).toHaveCount(1)
  // The light's own box is what clips it, whatever holds the light.
  expect(await light.evaluate((el) => getComputedStyle(el).overflow)).toBe("hidden")

  const clipped = await widestDuringLap(card)
  expect(clipped.laps).toBeGreaterThanOrEqual(1)
  expect(clipped.most).toBeLessThanOrEqual(1)
  finish()
})
