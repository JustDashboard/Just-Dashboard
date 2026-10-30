import { expect, test, type Page } from "@playwright/test"
import { mockOverview } from "./overview-fixture"
import { json, user } from "./host-fixture"

/**
 * The daily count's one question, on the Overview. What is being checked is the promise it makes:
 * nothing is answered for anybody, the report is printed in the question, and once somebody has
 * answered nobody is asked again.
 */

const BODY = '{"version":"0.8.2","usedToday":true,"usedThisWeek":true}'
const undecided = {
  enabled: true,
  choice: "undecided",
  siteUrl: "https://just-dashboard.com",
  nextBody: BODY,
}

async function mock(page: Page, initial: Record<string, unknown>) {
  const world = { status: initial, puts: [] as unknown[] }
  await mockOverview(page)
  await page.route("**/api/v1/dashboard/usage-ping", async (route) => {
    if (route.request().method() === "PUT") {
      const body = route.request().postDataJSON() as { on: boolean }
      world.puts.push(body)
      world.status = { ...world.status, choice: body.on ? "on" : "off" }
    }
    return json(route, world.status)
  })
  return world
}

const question = (page: Page) => page.getByText("Count this server in the public install numbers?")
const identity = (page: Page) => page.locator('[data-slot="host-identity"]')

test("the question prints the report, and a yes ends it", async ({ page }) => {
  const world = await mock(page, undecided)
  await page.goto("/")

  await expect(question(page)).toBeVisible()
  await expect(page.getByText(BODY)).toBeVisible()
  expect(world.puts).toEqual([])

  await page.getByRole("button", { name: "Count this server" }).click()
  await expect(question(page)).toBeHidden()
  expect(world.puts).toEqual([{ on: true }])
})

test("no thanks is an answer, and it does not come back", async ({ page }) => {
  const world = await mock(page, undecided)
  await page.goto("/")
  await page.getByRole("button", { name: "No thanks" }).click()
  await expect(question(page)).toBeHidden()
  expect(world.puts).toEqual([{ on: false }])

  await page.reload()
  await expect(identity(page)).toBeVisible()
  await expect(question(page)).toBeHidden()
})

for (const [name, status] of [
  ["already answered", { ...undecided, choice: "on" }],
  ["switched off in .env", { ...undecided, enabled: false, offBy: "JD_USAGE_PING" }],
] as const) {
  test(`nobody is asked when it is ${name}`, async ({ page }) => {
    await mock(page, status)
    await page.goto("/")
    await expect(identity(page)).toBeVisible()
    await expect(question(page)).toBeHidden()
  })
}

test("an account that cannot answer is not asked, and does not read it", async ({ page }) => {
  await mock(page, undecided)
  await page.route("**/api/v1/auth/session", (route) =>
    json(route, { ...user, capabilities: ["read", "service.control"] }),
  )
  let reads = 0
  page.on("request", (request) => {
    if (request.url().includes("/dashboard/usage-ping")) reads += 1
  })
  await page.goto("/")
  await expect(identity(page)).toBeVisible()
  await expect(question(page)).toBeHidden()
  expect(reads).toBe(0)
})
