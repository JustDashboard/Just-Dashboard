import { expect, test } from "@playwright/test"
import { archivedProject, deployment, json, mockProject, now, run, user } from "./deploy-fixture"

/**
 * Projects, Archived and Notifications — the fleet-level screens.
 *
 * `mockProject` gives every test a working `/deploy/7` and `/deploy/notifications`
 * backend; each test overrides the fleet/archived list it actually exercises.
 */

test("fleet grid and list preserve filters and fit multiple projects", async ({
  page,
}, testInfo) => {
  await mockProject(page)
  const names = [
    "storefront",
    "payments-api",
    "background-worker",
    "documentation",
    "staging-site",
    "status-page",
  ]
  await page.route("**/api/v1/deploy/?view=fleet", (route) =>
    json(route, {
      deployments: names.map((name, index) => ({
        ...deployment,
        id: index + 1,
        name,
        endpoint: `${name}.example.test`,
        activeRun: undefined,
      })),
      activeWork: [],
      slots: { heavyUsed: 0, heavyCapacity: 2, lightUsed: 0, lightCapacity: 4 },
    }),
  )
  await page.setViewportSize({ width: 1280, height: 1000 })
  await page.goto("/deploy")
  const projects = page.getByRole("list", { name: "Deployment projects" })
  await expect(projects.locator(":scope > li")).toHaveCount(6)
  await expect(page.getByRole("button", { name: "Grid view" })).toHaveAttribute(
    "aria-pressed",
    "true",
  )
  // The fleet's four readings sit over the cards; the per-state counts are
  // the chips', and a state nothing is in draws no chip. Health nobody has
  // observed is not a reason for attention.
  for (const reading of ["Live", "Requests", "Failing requests", "Build slots"]) {
    await expect(page.getByText(reading, { exact: true })).toBeVisible()
  }
  await expect(page.getByRole("button", { name: /^Changes pending/ })).toContainText("6")
  await expect(page.getByRole("button", { name: /^Deploying/ })).toHaveCount(0)
  await expect(page.getByRole("button", { name: /^Attention/ })).toHaveCount(0)
  await testInfo.attach("fleet-grid-1280", {
    body: await page.screenshot({
      path: testInfo.outputPath("fleet-grid-1280.png"),
      fullPage: true,
    }),
    contentType: "image/png",
  })

  await page.getByRole("textbox", { name: "Search deployments" }).fill("payments")
  await expect(projects.locator(":scope > li")).toHaveCount(1)

  await page.getByRole("button", { name: "List view" }).click()
  await expect(page.getByRole("button", { name: "List view" })).toHaveAttribute(
    "aria-pressed",
    "true",
  )
  await expect(projects.locator(":scope > li")).toHaveCount(1)
  await expect(projects.getByText("payments-api", { exact: true })).toBeVisible()
  await expect(projects.getByText("storefront", { exact: true })).toHaveCount(0)

  await page.getByRole("textbox", { name: "Search deployments" }).fill("")
  await expect(projects.locator(":scope > li")).toHaveCount(6)

  await page.setViewportSize({ width: 390, height: 900 })
  await expect(projects).toBeVisible()
  await expect(projects.locator(":scope > li")).toHaveCount(6)
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(
    true,
  )
  await testInfo.attach("fleet-list-390", {
    body: await page.screenshot({
      path: testInfo.outputPath("fleet-list-390.png"),
      fullPage: true,
    }),
    contentType: "image/png",
  })
})

test("deployment fleet stays useful across the responsive contract", async ({ page }, testInfo) => {
  await mockProject(page)
  for (const width of [375, 768, 1024, 1440]) {
    await page.setViewportSize({ width, height: 900 })
    await page.goto("/deploy")
    await expect(page.getByRole("heading", { name: "Deployments", exact: true })).toBeVisible()
    await expect(page.getByRole("heading", { name: "In progress" })).toBeVisible()
    await expect(page.getByText("api-production", { exact: true }).first()).toBeVisible()
    expect(
      await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth),
      `horizontal viewport overflow at ${width}px`,
    ).toBe(true)
    await testInfo.attach(`fleet-${width}`, {
      body: await page.screenshot({
        path: testInfo.outputPath(`fleet-${width}.png`),
        fullPage: true,
      }),
      contentType: "image/png",
    })
  }
})

test("filter chips narrow the grid", async ({ page }) => {
  await mockProject(page)
  await page.route("**/api/v1/deploy/?view=fleet", (route) =>
    json(route, {
      deployments: [
        {
          ...deployment,
          id: 1,
          name: "deploying-app",
          activeRun: run,
          lastRun: run,
          pendingChanges: false,
          health: "healthy",
        },
        {
          ...deployment,
          id: 2,
          name: "failed-app",
          activeRun: undefined,
          lastRun: { ...run, id: 90, state: "failed", releaseId: 5 },
          pendingChanges: false,
          health: "healthy",
        },
        {
          ...deployment,
          id: 3,
          name: "pending-app",
          activeRun: undefined,
          lastRun: { ...run, id: 91, state: "succeeded", releaseId: 20 },
          pendingChanges: true,
          health: "healthy",
        },
      ],
      activeWork: [],
      slots: { heavyUsed: 0, heavyCapacity: 2, lightUsed: 0, lightCapacity: 4 },
    }),
  )
  await page.goto("/deploy")
  const projects = page.getByRole("list", { name: "Deployment projects" })
  await expect(projects.locator(":scope > li")).toHaveCount(3)

  // Each chip carries its count, so its name is "Deploying 1" and the like.
  await expect(page.getByRole("button", { name: /^Failed/ })).toContainText("1")
  await page.getByRole("button", { name: /^Deploying/ }).click()
  await expect(projects.locator(":scope > li")).toHaveCount(1)
  await expect(projects.getByText("deploying-app", { exact: true })).toBeVisible()

  await page.getByRole("button", { name: /^Failed/ }).click()
  await expect(projects.locator(":scope > li")).toHaveCount(1)
  await expect(projects.getByText("failed-app", { exact: true })).toBeVisible()

  await page.getByRole("button", { name: /^Changes pending/ }).click()
  await expect(projects.locator(":scope > li")).toHaveCount(1)
  await expect(projects.getByText("pending-app", { exact: true })).toBeVisible()

  await page.getByRole("button", { name: /^All/ }).click()
  await expect(projects.locator(":scope > li")).toHaveCount(3)

  // A failed deploy is said once, in the engine's words, above the fleet.
  await expect(page.getByRole("heading", { name: "Attention", exact: true })).toBeVisible()
  await expect(page.getByText(/^failed-app: #1 Deploy failed/)).toBeVisible()
})

test("the empty state offers to start a project or clear the filters", async ({ page }) => {
  await mockProject(page)
  await page.route("**/api/v1/deploy/?view=fleet", (route) =>
    json(route, {
      deployments: [],
      activeWork: [],
      slots: { heavyUsed: 0, heavyCapacity: 2, lightUsed: 0, lightCapacity: 4 },
    }),
  )
  await page.goto("/deploy")
  await expect(page.getByText("Deploy your first project", { exact: true })).toBeVisible()
  // Two "New project" links exist at once here — the header's and the empty
  // state's own — so the assertion scopes to the empty state's block.
  const empty = page.locator('[data-slot="empty-state"]')
  await expect(empty.getByRole("link", { name: "New project", exact: true })).toBeVisible()
  await expect(
    empty.getByRole("link", { name: "See what is already running", exact: true }),
  ).toHaveAttribute("href", "/docker/stacks")

  await page.route("**/api/v1/deploy/?view=fleet", (route) =>
    json(route, {
      deployments: [{ ...deployment, id: 1, name: "storefront", health: "healthy" }],
      activeWork: [],
      slots: { heavyUsed: 0, heavyCapacity: 2, lightUsed: 0, lightCapacity: 4 },
    }),
  )
  await page.goto("/deploy")
  await page.getByRole("textbox", { name: "Search deployments" }).fill("nothing-like-this")
  await expect(page.getByText("No deployments match", { exact: true })).toBeVisible()
  await page.getByRole("button", { name: "Clear filters", exact: true }).click()
  await expect(
    page.getByRole("list", { name: "Deployment projects" }).locator(":scope > li"),
  ).toHaveCount(1)
})

test("New project is hidden for a read-only role", async ({ page }) => {
  await mockProject(page)
  await page.route("**/api/v1/auth/session", (route) =>
    json(route, { ...user, capabilities: ["read"] }),
  )
  await page.goto("/deploy")
  await expect(page.getByRole("heading", { name: "Deployments", exact: true })).toBeVisible()
  await expect(page.getByRole("link", { name: "New project", exact: true })).toHaveCount(0)
})

test("Credentials link sits beside Notifications and opens the credentials page", async ({
  page,
}) => {
  await mockProject(page)
  await page.route("**/api/v1/deploy/credentials", async (route) => {
    if (route.request().method() !== "GET") return route.fallback()
    await json(route, [])
  })
  await page.goto("/deploy")
  // Scoped to the page: the sidebar's Deployments panel names Credentials too,
  // and this test is about the link in the page header.
  await page.getByRole("main").getByRole("link", { name: "Credentials", exact: true }).click()
  await expect(page).toHaveURL(/\/deploy\/credentials$/)
  await expect(page.getByRole("heading", { name: "Credentials", exact: true })).toBeVisible()
})

test("Credentials link is hidden for a read-only role, unlike Notifications", async ({ page }) => {
  await mockProject(page)
  await page.route("**/api/v1/auth/session", (route) =>
    json(route, { ...user, capabilities: ["read"] }),
  )
  await page.goto("/deploy")
  await expect(page.getByRole("heading", { name: "Deployments", exact: true })).toBeVisible()
  const main = page.getByRole("main")
  await expect(main.getByRole("link", { name: "Notifications", exact: true })).toBeVisible()
  await expect(main.getByRole("link", { name: "Credentials", exact: true })).toHaveCount(0)
  // The rail seals it the same way, from the same capability the backend does.
  const rail = page.getByRole("navigation", { name: "Sidebar" })
  await expect(rail.getByRole("link", { name: "Notifications", exact: true })).toBeVisible()
  await expect(rail.getByRole("link", { name: "Credentials", exact: true })).toHaveCount(0)
})

test("on a phone the header's pages sit behind one menu beside New project", async ({ page }) => {
  await mockProject(page)
  await page.setViewportSize({ width: 390, height: 844 })
  await page.goto("/deploy")
  const main = page.getByRole("main")
  await expect(main.getByRole("link", { name: "New project", exact: true })).toBeVisible()
  // One render per shape: the links are not also drawn and hidden.
  await expect(main.getByRole("link", { name: "Notifications", exact: true })).toHaveCount(0)
  await main.getByRole("button", { name: "Pages", exact: true }).click()
  await expect(page.getByRole("menuitem", { name: /^Credentials/ })).toBeVisible()
  await page.getByRole("menuitem", { name: /^Archived/ }).click()
  await expect(page).toHaveURL(/\/deploy\?view=archived$/)
})

test("a stopped project reads Stopped", async ({ page }) => {
  await mockProject(page)
  await page.route("**/api/v1/deploy/?view=fleet", (route) =>
    json(route, {
      deployments: [
        {
          ...deployment,
          id: 1,
          name: "paused-app",
          activeRun: undefined,
          stopped: true,
          health: "healthy",
        },
      ],
      activeWork: [],
      slots: { heavyUsed: 0, heavyCapacity: 2, lightUsed: 0, lightCapacity: 4 },
    }),
  )
  await page.goto("/deploy")
  await expect(page.getByText("Stopped", { exact: true })).toBeVisible()
})

test("in-progress Cancel is hidden once the engine would refuse it", async ({ page }) => {
  await mockProject(page)
  await page.route("**/api/v1/deploy/?view=fleet", (route) =>
    json(route, {
      deployments: [{ ...deployment, activeRun: run, lastRun: run }],
      activeWork: [
        {
          run: { ...run, state: "activating" },
          projectName: "api-production",
          environment: "Production",
          currentStep: "activate",
        },
      ],
      slots: { heavyUsed: 1, heavyCapacity: 2, lightUsed: 0, lightCapacity: 4 },
    }),
  )
  await page.goto("/deploy")
  const row = page
    .getByRole("region", { name: "In progress" })
    .getByRole("listitem")
    .filter({ hasText: "api-production" })
  await expect(row).toBeVisible()
  await expect(row.getByRole("button", { name: "Cancel", exact: true })).toHaveCount(0)
  // The row is the destination: its title opens the run, named for it.
  await expect(row.getByRole("link", { name: "View api-production", exact: true })).toHaveAttribute(
    "href",
    "/deploy/7/runs/84",
  )
  await expect(row.getByRole("img", { name: /^Stage \d of 7/ })).toBeVisible()
})

test("in-progress Cancel disables itself after the first click", async ({ page }) => {
  await mockProject(page)
  let cancelCalls = 0
  let releaseCancel = () => {}
  const cancelGate = new Promise<void>((resolve) => {
    releaseCancel = resolve
  })
  await page.route("**/api/v1/deploy/7/runs/84/cancel", async (route) => {
    cancelCalls++
    await cancelGate
    await json(route, { ...run, state: "cancelling" })
  })
  await page.route("**/api/v1/deploy/?view=fleet", (route) =>
    json(route, {
      deployments: [{ ...deployment, activeRun: run, lastRun: run }],
      activeWork: [
        {
          run: { ...run, state: "running" },
          projectName: "api-production",
          environment: "Production",
          currentStep: "build_artifact",
        },
      ],
      slots: { heavyUsed: 1, heavyCapacity: 2, lightUsed: 0, lightCapacity: 4 },
    }),
  )
  await page.goto("/deploy")
  const cancelButton = page
    .getByRole("region", { name: "In progress" })
    .getByRole("listitem")
    .filter({ hasText: "api-production" })
    .getByRole("button", { name: "Cancel", exact: true })
  await expect(cancelButton).toBeEnabled()
  await cancelButton.click()
  await expect(cancelButton).toBeDisabled()
  await cancelButton.click({ force: true })
  releaseCancel()
  await expect(page.getByText("Cancelling api-production", { exact: true })).toBeVisible()
  expect(cancelCalls).toBe(1)
})

test("the in-progress live region announces the count, not every second's elapsed time", async ({
  page,
}) => {
  await mockProject(page)
  await page.route("**/api/v1/deploy/?view=fleet", (route) =>
    json(route, {
      deployments: [{ ...deployment, activeRun: run, lastRun: run }],
      activeWork: [
        {
          run: { ...run, state: "running" },
          projectName: "api-production",
          environment: "Production",
          currentStep: "build_artifact",
        },
      ],
      slots: { heavyUsed: 1, heavyCapacity: 2, lightUsed: 0, lightCapacity: 4 },
    }),
  )
  await page.goto("/deploy")
  const region = page.getByRole("region", { name: "In progress" })
  await expect(region.getByText("1 deployment in progress", { exact: true })).toHaveCount(1)
  // Exactly one live region — the summary line — and the row list underneath
  // carries neither `aria-live` nor `aria-atomic` of its own.
  await expect(region.locator('[aria-live="polite"]')).toHaveCount(1)
  await expect(region.getByRole("list")).not.toHaveAttribute("aria-live")
  await expect(region.getByRole("list")).not.toHaveAttribute("aria-atomic")
})

test("a Compose deployment with more than one service reports how many", async ({ page }) => {
  await mockProject(page)
  await page.route("**/api/v1/deploy/?view=fleet", (route) =>
    json(route, {
      deployments: [
        {
          ...deployment,
          id: 1,
          name: "stack-app",
          profile: "compose",
          sourceKind: "compose",
          sourceRef: "docker-compose.yml",
          serviceCount: 3,
          activeRun: undefined,
        },
      ],
      activeWork: [],
      slots: { heavyUsed: 0, heavyCapacity: 2, lightUsed: 0, lightCapacity: 4 },
    }),
  )
  await page.goto("/deploy")
  await expect(page.getByText(/3 services/)).toBeVisible()
  await expect(page.getByText("docker-compose.yml", { exact: true })).toHaveCount(0)
})

test("a card says how many pull requests are open, and the chip narrows the fleet to them", async ({
  page,
}) => {
  await mockProject(page)
  await page.route("**/api/v1/deploy/?view=fleet", (route) =>
    json(route, {
      deployments: [
        { ...deployment, id: 1, name: "storefront", activeRun: undefined },
        { ...deployment, id: 2, name: "worker", activeRun: undefined },
      ],
      activeWork: [],
      slots: { heavyUsed: 0, heavyCapacity: 2, lightUsed: 0, lightCapacity: 4 },
    }),
  )
  await page.route("**/api/v1/deploy/pull-requests", (route) =>
    json(route, { projects: { "1": { open: 3, previews: 1 } } }),
  )
  await page.setViewportSize({ width: 1280, height: 1000 })
  await page.goto("/deploy")
  const projects = page.getByRole("list", { name: "Deployment projects" })
  await expect(projects.locator(":scope > li")).toHaveCount(2)
  // A count in plain words on the source line, only where there is one.
  const storefront = projects.locator(":scope > li").filter({ hasText: "storefront" })
  await expect(storefront.getByText("3 pull requests · 1 preview", { exact: true })).toBeVisible()
  await expect(
    projects
      .locator(":scope > li")
      .filter({ hasText: "worker" })
      .getByText(/pull request/),
  ).toHaveCount(0)

  const chip = page.getByRole("button", { name: /^Pull requests/ })
  await expect(chip).toContainText("1")
  await chip.click()
  await expect(projects.locator(":scope > li")).toHaveCount(1)
  await expect(projects.getByText("storefront", { exact: true })).toBeVisible()

  // The list row's second line carries the same words.
  await page.getByRole("button", { name: "List view" }).click()
  await expect(projects.getByText("3 pull requests · 1 preview", { exact: true })).toBeVisible()
})

test("archived list can be permanently deleted with confirmation and errors remain reviewable", async ({
  page,
}, testInfo) => {
  await mockProject(page)
  let deleted = false
  let refuse = true
  let calls = 0
  await page.route("**/api/v1/deploy/?view=archived", (route) =>
    json(route, deleted ? [] : [{ ...archivedProject, archivedAt: now }]),
  )
  await page.route("**/api/v1/deploy/9/permanent", (route) => {
    calls++
    if (refuse) {
      return route.fulfill({
        status: 409,
        contentType: "application/json",
        body: JSON.stringify({
          error: { code: "already_running", message: "A deployment run is still active" },
        }),
      })
    }
    deleted = true
    return route.fulfill({ status: 204 })
  })
  await page.goto("/deploy")
  await page.getByRole("link", { name: "Archived", exact: true }).click()
  await expect(page).toHaveURL(/\/deploy\?view=archived$/)
  await expect(page.getByRole("heading", { name: "Archived", exact: true })).toBeVisible()
  await expect(page.getByRole("link", { name: "retired-api" })).toBeVisible()
  // What deleting it would take with it is on the row.
  const archived = page.getByRole("list", { name: "Archived deployments" })
  await expect(archived.getByText("4 variables", { exact: true })).toBeVisible()
  await expect(archived.getByText("acme/retired-api", { exact: true })).toBeVisible()
  await testInfo.attach("archived-1280", {
    body: await page.screenshot({ path: testInfo.outputPath("archived-1280.png"), fullPage: true }),
    contentType: "image/png",
  })
  await page.setViewportSize({ width: 390, height: 900 })
  await testInfo.attach("archived-390", {
    body: await page.screenshot({ path: testInfo.outputPath("archived-390.png"), fullPage: true }),
    contentType: "image/png",
  })
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(
    true,
  )
  await page.setViewportSize({ width: 1280, height: 720 })

  // Restore is the row's named act; deleting for good is in its menu.
  const purge = async () => {
    await archived.getByRole("button", { name: "Actions for retired-api" }).click()
    await page.getByRole("menuitem", { name: /Delete permanently/ }).click()
  }
  await purge()
  const dialog = page.getByRole("dialog", { name: "Delete retired-api permanently" })
  await expect(dialog).toContainText("persistent data remain on the server")
  await expect(dialog.getByText("Deleted for good", { exact: true })).toBeVisible()
  await expect(dialog.getByText("Stays on the server", { exact: true })).toBeVisible()
  await expect(dialog.getByText("4 variables", { exact: true })).toBeVisible()
  await dialog.getByRole("button", { name: "Cancel" }).click()
  expect(calls).toBe(0)

  await purge()
  // Rare and unrecoverable, so the name is typed first, as on the Danger zone.
  await expect(dialog.getByRole("button", { name: "Delete permanently" })).toBeDisabled()
  await dialog.getByRole("textbox").fill("retired-api")
  await dialog.getByRole("button", { name: "Delete permanently" }).click()
  await expect(page.getByText("A deployment run is still active", { exact: true })).toBeVisible()
  await expect(dialog).toBeVisible()

  refuse = false
  await dialog.getByRole("button", { name: "Delete permanently" }).click()
  await expect(page.getByText("No archived deployments", { exact: true })).toBeVisible()
  expect(calls).toBe(2)
})

test("a restored project clears a name conflict, then returns to the fleet", async ({ page }) => {
  await mockProject(page)
  let restored = false
  let refused = true
  const unarchiveCalls: string[] = []
  await page.route("**/api/v1/deploy/?view=archived", (route) =>
    json(route, restored ? [] : [{ ...archivedProject, archivedAt: now }]),
  )
  await page.route("**/api/v1/deploy/9/unarchive", (route) => {
    unarchiveCalls.push(route.request().method())
    if (refused) {
      return route.fulfill({
        status: 409,
        contentType: "application/json",
        body: JSON.stringify({
          error: { code: "name_taken", message: "retired-api is already used by another project" },
        }),
      })
    }
    restored = true
    return json(route, { ...deployment, id: 9, name: "retired-api" })
  })
  await page.goto("/deploy?view=archived")
  await expect(page.getByRole("link", { name: "retired-api" })).toBeVisible()

  await page.getByRole("button", { name: "Restore", exact: true }).click()
  await expect(page.getByText("Could not restore the project", { exact: true })).toBeVisible()
  await expect(
    page.getByText("retired-api is already used by another project", { exact: true }),
  ).toBeVisible()
  await expect(page).toHaveURL(/\/deploy\?view=archived$/)

  refused = false
  await page.getByRole("button", { name: "Restore", exact: true }).click()
  await expect(page.getByText("Restored retired-api", { exact: true })).toBeVisible()
  await expect(page).toHaveURL(/\/deploy\/9$/)
  expect(unarchiveCalls).toEqual(["POST", "POST"])
})

test("Discord channel creation, pause, test delivery, history and removal", async ({
  page,
}, testInfo) => {
  const dashboard = await mockProject(page)
  await page.goto("/deploy/notifications")
  await expect(page.getByRole("heading", { name: "Notifications", exact: true })).toBeVisible()

  await page.getByRole("button", { name: "Add channel", exact: true }).click()
  // The kinds are cards named by their service; Discord is the one chosen.
  await expect(
    page.getByRole("group", { name: "Deliver to" }).getByRole("button", { name: /^Discord/ }),
  ).toHaveAttribute("aria-pressed", "true")
  await page.getByLabel("Name").fill("Ops room")
  await page
    .getByLabel("Discord webhook URL")
    .fill("https://discord.com/api/webhooks/123456/secret-token-value")
  // Screenshot before toggling the switch: it shows the defaults (Succeeded
  // and Failed pre-checked) settled, rather than a still-animating thumb.
  await testInfo.attach("notifications-form-1280", {
    body: await page.screenshot({
      path: testInfo.outputPath("notifications-form-1280.png"),
      fullPage: true,
    }),
    contentType: "image/png",
  })
  await page.getByRole("switch", { name: "Started" }).click()
  await page.getByRole("dialog").getByRole("button", { name: "Add channel", exact: true }).click()

  const row = page.getByRole("listitem").filter({ hasText: "Ops room" })
  await expect(row).toBeVisible()
  await testInfo.attach("notifications-list-1280", {
    body: await page.screenshot({
      path: testInfo.outputPath("notifications-list-1280.png"),
      fullPage: true,
    }),
    contentType: "image/png",
  })
  await page.setViewportSize({ width: 390, height: 900 })
  await testInfo.attach("notifications-list-390", {
    body: await page.screenshot({
      path: testInfo.outputPath("notifications-list-390.png"),
      fullPage: true,
    }),
    contentType: "image/png",
  })
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(
    true,
  )
  await page.setViewportSize({ width: 1280, height: 720 })
  await expect(row).toContainText("https://discord.com/api/webhooks/123456/••••")
  await expect(row).not.toContainText("secret-token-value")
  await expect(row).toContainText("Started, Succeeded, Failed")
  // The card says how the last message went, not "Enabled": nothing yet.
  await expect(row.getByText("never sent", { exact: true })).toBeVisible()
  await expect(page.getByText("one-time-notification-secret")).toHaveCount(0)

  await row.getByRole("button", { name: "Send test", exact: true }).click()
  await expect(page.getByText("Test message sent to Ops room")).toBeVisible()
  expect(dashboard.notificationTests()).toBe(1)

  await row.getByRole("button", { name: "Actions for Ops room" }).click()
  await page.getByRole("menuitem", { name: "Pause channel" }).click()
  await expect(row.getByText("Paused", { exact: true })).toBeVisible()

  await row.getByRole("button", { name: "Actions for Ops room" }).click()
  await page.getByRole("menuitem", { name: "Delivery history" }).click()
  await expect(page.getByRole("heading", { name: "Deliveries · Ops room" })).toBeVisible()
  await expect(page.getByText(/run\.failed/)).toBeVisible()
  await expect(page.getByText("Delivered", { exact: true })).toBeVisible()
  await page.keyboard.press("Escape")

  await row.getByRole("button", { name: "Actions for Ops room" }).click()
  await page.getByRole("menuitem", { name: "Remove channel" }).click()
  await page.getByRole("button", { name: "Remove channel", exact: true }).click()
  await expect(page.getByText("No notification channels", { exact: true })).toBeVisible()
})
