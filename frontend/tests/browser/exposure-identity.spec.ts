import { expect, test, type Page } from "@playwright/test"
import { json, user } from "./host-fixture"
import { exposure, mockOverview } from "./overview-fixture"
import { risen } from "./risen"

/**
 * How the dashboard is reached, on the two pages that say it: the Security
 * overview's opening line (`ExposureIdentity`) and the Security row of the
 * host Overview, which reads the same grade. Neither moves what is under it
 * when the exposure lands.
 */

const reached = {
  ...exposure,
  grade: "tailscale",
  allowlist: ["100.64.0.0/10"],
  interfaces: ["tailscale0"],
  tailscaleIp: "100.110.34.31",
  client: "100.110.34.9",
}

/** `/exposure`, answered only once the returned function is called. */
async function holdExposure(page: Page) {
  let release = () => {}
  const held = new Promise<void>((resolve) => (release = resolve))
  await page.route("**/api/v1/exposure", async (route) => {
    await held
    await json(route, reached)
  })
  return release
}

for (const width of [390, 1280]) {
  test(`the Security overview's line waits at the height it will have, at ${width}`, async ({
    page,
  }) => {
    await page.setViewportSize({ width, height: 900 })
    // Every other read of the page is refused at once, so the only thing
    // that lands during the test is the exposure.
    await page.route("**/api/v1/**", (route) =>
      route.fulfill({
        status: 503,
        contentType: "application/json",
        body: JSON.stringify({ error: { code: "unavailable", message: "not read in this test" } }),
      }),
    )
    await page.route("**/api/v1/auth/session", (route) => json(route, user))
    const release = await holdExposure(page)
    await page.goto("/security")
    const line = page.locator("[data-slot=page] > [data-slot=host-identity]")
    await expect(line.locator("[data-slot=skeleton]").first()).toBeVisible()
    const under = page.locator("[data-slot=page] > [data-slot=host-identity] + *")
    await expect(under).toBeVisible()
    // The other reads have been refused, and drawn as that, by now.
    await page.waitForTimeout(1500)
    await risen(page)
    const measure = async () => {
      const [a, b] = await Promise.all([line.boundingBox(), under.boundingBox()])
      return { line: Math.round(a!.height), under: Math.round(b!.y) }
    }
    const before = await measure()
    release()
    await expect(line.getByText("Tailscale only", { exact: true })).toBeVisible()
    await expect(line.getByText("100.110.34.31", { exact: true })).toBeVisible()
    await risen(page)
    expect(await measure()).toEqual(before)
  })

  test(`the Overview's Security row waits at the height it will have, at ${width}`, async ({
    page,
  }) => {
    await page.setViewportSize({ width, height: 900 })
    await mockOverview(page)
    const release = await holdExposure(page)
    await page.goto("/")
    const rows = page.getByRole("list", { name: "Services" }).locator(":scope > li")
    const row = rows.filter({ has: page.getByRole("link", { name: "Open Security" }) })
    await expect(row.locator("[data-slot=skeleton]")).toBeVisible()
    await expect(rows.filter({ hasText: "Git" })).toContainText("4 repositories")
    await risen(page)
    const measure = () =>
      rows.evaluateAll((all) =>
        all.map((item) => {
          const box = item.getBoundingClientRect()
          return { y: Math.round(box.y), height: box.height }
        }),
      )
    const before = await measure()
    release()
    await expect(row).toContainText("Tailscale only, 1 allowed range on 1 interface")
    await risen(page)
    expect(await measure()).toEqual(before)
  })
}
