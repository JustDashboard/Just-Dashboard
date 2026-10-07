import { expect, test } from "@playwright/test"
import { mockNetwork } from "./network-fixture"

/**
 * The Network section against a mocked API (`network-fixture.ts`): every page
 * renders its readings from the shapes `netx` writes, the forms send what the
 * routes take, and the design system's structural rules hold. Nothing here
 * needs a network to change: the checks are about what the pages do with the
 * answers, which is the part a Go test cannot see.
 */

const PAGES = ["/network", "/network/interfaces", "/network/connections"]

/**
 * Review screenshots, for the eyes the checks do not have. Written only when
 * asked for, into the directory named — never into the tree.
 */
test.describe("screenshots", () => {
  const dir = process.env.JD_NETWORK_SHOTS
  test.skip(!dir, "set JD_NETWORK_SHOTS to a directory to capture them")

  for (const width of [390, 1440]) {
    test.describe(`${width} wide`, () => {
      test.use({ viewport: { width, height: 1000 } })
      for (const path of PAGES) {
        test(`${path} at ${width}`, async ({ page }) => {
          await mockNetwork(page)
          await page.goto(path)
          await page.waitForLoadState("networkidle")
          await page.waitForTimeout(1500)
          const name = path.replace(/^\/network\/?/, "") || "overview"
          await page.screenshot({ path: `${dir}/${name}-${width}.png`, fullPage: true })
          for (let part = 1; part <= 6; part++) {
            const moved = await page.locator("[data-slot=page]").evaluate((element) => {
              let parent = element.parentElement
              while (parent && !/auto|scroll/.test(getComputedStyle(parent).overflowY)) {
                parent = parent.parentElement
              }
              if (!parent || parent.scrollTop + parent.clientHeight >= parent.scrollHeight - 1)
                return false
              parent.scrollTop += parent.clientHeight - 80
              return true
            })
            if (!moved) break
            await page.waitForTimeout(300)
            await page.screenshot({ path: `${dir}/${name}-${width}-scroll-${part}.png` })
          }
        })
      }
    })
  }
})

test("the overview draws the topology, the readings and the attention list", async ({ page }) => {
  await mockNetwork(page)
  await page.goto("/network")
  await expect(page.getByText("The internet")).toBeVisible()
  await expect(page.getByText("Tailnet")).toBeVisible()
  await expect(page.getByRole("heading", { name: "Attention" })).toBeVisible()
})
