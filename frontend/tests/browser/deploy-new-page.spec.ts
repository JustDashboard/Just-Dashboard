import { expect, test } from "@playwright/test"
import { risen } from "./risen"
import { blueprintCatalogue, json, mockNewProject } from "./deploy-fixture"

/**
 * The new project page's source step after its pass to the deploy standard:
 * the GitHub App that is not connected is drawn, the column ends on its
 * controls rather than on a paragraph, and a shelf of one template does not
 * take a row to itself.
 */

for (const width of [1280, 390]) {
  test(`the GitHub App that is not connected is a picture, at ${width}`, async ({ page }) => {
    await page.setViewportSize({ width, height: 900 })
    await mockNewProject(page)
    await page.goto("/deploy/new")

    const picture = page.getByRole("group", { name: "GitHub App", exact: true })
    await expect(picture.getByText("Disconnected", { exact: true })).toBeVisible()
    await expect(picture.getByText("Not set up on this server", { exact: true })).toBeVisible()
    await expect(picture.getByText("This server", { exact: true })).toBeVisible()
    // The two marks and the line between them, which crosses neither's words.
    await expect(picture.locator("svg path").first()).toBeAttached()
    // Its one verb is on the section's title line, and goes where the App is set up.
    const github = page
      .locator("section")
      .filter({ has: page.getByRole("heading", { name: "GitHub", exact: true }) })
    await expect(github.getByRole("link", { name: "Connect", exact: true })).toHaveAttribute(
      "href",
      "/deploy/credentials",
    )
    // The CLI that is signed in keeps its row, and the App has none beside it.
    const identities = page.getByRole("list", { name: "GitHub connections" })
    await expect(identities.getByRole("listitem")).toHaveCount(1)
    await expect(identities.getByText("Signed in", { exact: true })).toBeVisible()

    // The column ends on Import: the Review step says what a push does.
    await expect(page.getByText(/New commits to the selected branch deploy/)).toHaveCount(0)
    await expect(page.getByRole("button", { name: "Import", exact: true })).toBeVisible()
    expect(
      await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth),
    ).toBe(true)

    // Nothing about the flow changed: a repository still imports on a press.
    await page.getByRole("button", { name: "Import Wayy01/wesmokefish", exact: true }).click()
    await expect(page.getByRole("heading", { level: 1 })).not.toHaveText("What are you deploying?")
  })
}

test("an App that exists and nobody installed is drawn with its line, and says so", async ({
  page,
}) => {
  await mockNewProject(page)
  await page.route("**/api/v1/deploy/github-app/", (route) =>
    json(route, {
      configured: true,
      installations: [],
      installUrl: "https://github.com/apps/just-dashboard-test/installations/new",
    }),
  )
  await page.goto("/deploy/new")
  const picture = page.getByRole("group", { name: "GitHub App", exact: true })
  await expect(picture.getByText("Uninstalled", { exact: true })).toBeVisible()
  await expect(
    picture.getByText("Waiting for an account to install it", { exact: true }),
  ).toBeVisible()
  // Its verb is GitHub's own page for it, not this server's set-up.
  await expect(page.getByRole("link", { name: "Adjust access" })).toHaveAttribute(
    "href",
    "https://github.com/apps/just-dashboard-test/installations/new",
  )
  await expect(page.getByRole("link", { name: "Connect", exact: true })).toHaveCount(0)
})

test("a connected App is its account's row again, with no picture", async ({ page }) => {
  await mockNewProject(page)
  await page.route("**/api/v1/deploy/github-app/", (route) =>
    json(route, {
      configured: true,
      installations: [
        {
          id: 1,
          account: "acme",
          accountType: "Organization",
          htmlUrl: "https://github.com/organizations/acme/settings/installations/1",
          repositorySelection: "all",
        },
      ],
    }),
  )
  await page.goto("/deploy/new")
  const identities = page.getByRole("list", { name: "GitHub connections" })
  await expect(identities.getByRole("listitem")).toHaveCount(2)
  await expect(identities.getByText("Installed", { exact: true })).toBeVisible()
  await expect(page.locator('[data-slot="github-app-picture"]')).toHaveCount(0)
})

test("shelves of one template stand side by side, and a fuller shelf takes the row", async ({
  page,
}) => {
  await page.setViewportSize({ width: 1280, height: 900 })
  await mockNewProject(page)
  const kuma = blueprintCatalogue.find((entry) => entry.id === "uptime-kuma")!
  await page.route("**/api/v1/deploy/blueprints/", (route) =>
    json(route, [
      ...blueprintCatalogue,
      { ...kuma, id: "grafana", name: "Grafana", description: "Dashboards over your metrics." },
    ]),
  )
  await page.goto("/deploy/new?source=template")

  const shelf = (name: string) => page.getByRole("group", { name, exact: true })
  await expect(shelf("Monitoring").getByRole("button")).toHaveCount(2)
  await risen(page)
  const box = async (name: string) => (await shelf(name).boundingBox())!
  const [productivity, monitoring, databases, games] = await Promise.all(
    ["Productivity", "Monitoring", "Databases", "Game servers"].map(box),
  )
  // Two templates: the shelf takes the row and lays them across it.
  expect(monitoring.width).toBeGreaterThan(productivity.width * 1.8)
  // One template each: Databases and Game servers share a row, one card high
  // each, rather than stacking as a narrow column.
  expect(Math.round(databases.y)).toBe(Math.round(games.y))
  expect(games.x).toBeGreaterThan(databases.x + databases.width)
  expect(Math.round(databases.height)).toBe(Math.round(games.height))
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(
    true,
  )

  // On a phone there is one column, and nothing is cut off.
  await page.setViewportSize({ width: 390, height: 844 })
  await risen(page)
  const [narrowDatabases, narrowGames] = await Promise.all(["Databases", "Game servers"].map(box))
  expect(narrowGames.y).toBeGreaterThan(narrowDatabases.y + narrowDatabases.height - 1)
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(
    true,
  )

  // Picking one still opens its settings: the flow is as it was.
  await page.setViewportSize({ width: 1280, height: 900 })
  await page.getByRole("button", { name: "Use Vaultwarden", exact: true }).click()
  await expect(page.getByRole("region", { name: "Selected template settings" })).toBeVisible()
})
