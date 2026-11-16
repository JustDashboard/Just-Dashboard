import { expect, test } from "@playwright/test"
import {
  deployment,
  healthyOperations,
  json,
  mockProject,
  now,
  project,
  run,
} from "./deploy-fixture"

test("the project header is one compact row that keeps its readings apart across screen sizes", async ({
  page,
}, testInfo) => {
  test.setTimeout(90_000)
  await page.emulateMedia({ reducedMotion: "reduce" })
  await mockProject(page, { operations: healthyOperations })
  const completed = {
    ...run,
    state: "succeeded",
    releaseId: 20,
    endedAt: now,
    metadata: {
      commit: {
        sha: deployment.sourceRevision,
        author: "Header Author",
        subject: "Keep the detailed commit message in the deployment history",
      },
    },
  }
  await page.route("**/api/v1/deploy/7", (route) =>
    json(route, {
      project,
      running: false,
      deployment: { ...deployment, activeRun: undefined, lastRun: completed },
    }),
  )
  await page.route("**/api/v1/deploy/7/runs**", (route) => {
    if (new URL(route.request().url()).searchParams.get("view") !== "engine")
      return route.fallback()
    return json(route, { runs: [completed], running: false })
  })

  for (const width of [390, 768, 1280, 1720]) {
    await page.setViewportSize({ width, height: 900 })
    await page.goto("/deploy/7")
    const identity = page.getByRole("region", { name: "About api-production" })
    await expect(identity.getByText("acme/api", { exact: true })).toBeVisible()
    await expect(identity.getByText("Release #2", { exact: true })).toBeVisible()
    await expect(identity.getByText("Ready", { exact: true })).toBeVisible()
    await expect(identity.getByText("No priority issues found", { exact: true })).toHaveCount(0)
    await expect(identity.locator('a[href="#attention"]')).toHaveCount(0)
    // Auto-deploy on is the expected answer: a phone's one line of facts leaves it to the desktop.
    const automatic = identity.getByRole("link", { name: "Automatic deployment settings" })
    if (width === 390) await expect(automatic).toHaveCount(0)
    else
      await expect(automatic).toHaveAttribute(
        "href",
        "/deploy/7/settings/general#automatic-deployment",
      )
    // The rail leads back and the address leads to the site: neither is repeated here.
    await expect(page.locator("[data-slot=page-context]")).toHaveCount(0)
    await expect(page.getByRole("link", { name: "Visit", exact: true })).toHaveCount(0)
    // It repeats above every project page, so on a desktop it is the height of
    // its tile and command rather than a block the page's content waits under.
    if (width >= 1280) expect((await identity.boundingBox())!.height).toBeLessThanOrEqual(64)
    await expect(identity.getByText(completed.metadata.commit.subject)).toHaveCount(0)
    expect(
      await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth),
    ).toBe(true)
    await identity.screenshot({ path: testInfo.outputPath(`header-${width}.png`) })
  }

  await page.goto("/deploy/7/deployments")
  await expect(page.getByText(completed.metadata.commit.subject, { exact: true })).toBeVisible()
  await expect(page.getByText(completed.metadata.commit.author, { exact: true })).toBeVisible()

  // User-controlled repository and branch names must not widen the shared header.
  const longName = "a-very-long-repository-name-".repeat(8)
  await page.route("**/api/v1/deploy/7", (route) =>
    json(route, {
      project,
      running: false,
      deployment: {
        ...deployment,
        activeRun: undefined,
        sourceRepository: `acme/${longName}`,
        sourceRef: `feature/${longName}`,
      },
    }),
  )
  for (const width of [390, 1280]) {
    await page.setViewportSize({ width, height: 900 })
    await page.goto("/deploy/7/runtime")
    await expect(page.getByRole("region", { name: "About api-production" })).toBeVisible()
    expect(
      await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth),
    ).toBe(true)
  }
})

test("on a phone the project header is one row over one line of facts, and holds still while it loads", async ({
  page,
}) => {
  await page.emulateMedia({ reducedMotion: "reduce" })
  await mockProject(page, { operations: healthyOperations })
  let release: () => void = () => {}
  const held = new Promise<void>((resolve) => (release = resolve))
  await page.route("**/api/v1/deploy/7", async (route) => {
    await held
    return route.fallback()
  })
  await page.setViewportSize({ width: 390, height: 900 })
  await page.goto("/deploy/7")
  const header = page.locator("[data-slot=project-header]").first()
  await expect(header).toHaveAttribute("aria-busy", "true")
  const loading = (await header.boundingBox())!
  release()

  const identity = page.getByRole("region", { name: "About api-production" })
  await expect(identity.getByText("acme/api", { exact: true })).toBeVisible()
  await expect(identity).not.toHaveAttribute("aria-busy", "true")
  const loaded = (await identity.boundingBox())!
  // Nothing under the line moves when the project lands.
  expect(Math.abs(loaded.y - loading.y)).toBeLessThanOrEqual(0.5)
  expect(Math.abs(loaded.height - loading.height)).toBeLessThanOrEqual(0.5)
  // One row and one line of facts: wave 0's line, with the command beside the
  // name and the state and facts on rows of their own, was 129px tall here,
  // and the verbs' own row made it 44px taller still.
  expect(loaded.height).toBeLessThanOrEqual(80)

  const parts = await identity.evaluate((line) => {
    const box = (element: Element | null) => {
      const rect = element!.getBoundingClientRect()
      return { top: rect.top, bottom: rect.bottom, left: rect.left, right: rect.right }
    }
    const title = line.querySelector("p")!
    const buttons = Array.from(line.querySelectorAll("[data-size]")).filter(
      (button) => button.getClientRects().length > 0,
    )
    return {
      tile: box(line.firstElementChild),
      title: box(title),
      // The name is read whole: nothing drawn in it is cut (a screen
      // reader's words, one pixel wide by design, are not drawn).
      cut: [title, ...Array.from(title.querySelectorAll("*"))].some(
        (element) => element.clientWidth > 1 && element.scrollWidth > element.clientWidth + 0.5,
      ),
      verbs: buttons.map((button) => ({
        ...box(button),
        name: button.getAttribute("aria-label") ?? button.textContent?.trim(),
      })),
      facts: box(line.lastElementChild),
    }
  })
  const centre = (part: { top: number; bottom: number }) => (part.top + part.bottom) / 2
  // The tile, the name and every verb share one row, the verbs at its far end.
  expect(parts.verbs.length).toBeGreaterThanOrEqual(2)
  for (const part of [parts.title, ...parts.verbs])
    expect(Math.abs(centre(part) - centre(parts.tile))).toBeLessThanOrEqual(1)
  for (const verb of parts.verbs) {
    expect(verb.left).toBeGreaterThanOrEqual(parts.title.right)
    // An icon button, as wide as it is tall, that keeps its words as its name.
    expect(Math.abs(verb.right - verb.left - (verb.bottom - verb.top))).toBeLessThanOrEqual(0.5)
    expect(verb.name).toBeTruthy()
  }
  expect(parts.cut).toBe(false)
  // The tile is a step smaller on a phone.
  expect(parts.tile.bottom - parts.tile.top).toBeLessThanOrEqual(32.5)
  // One line of facts, under the row.
  expect(parts.facts.top).toBeGreaterThanOrEqual(parts.tile.bottom)
  expect(parts.facts.bottom - parts.facts.top).toBeLessThanOrEqual(16.5)
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(
    true,
  )
})
