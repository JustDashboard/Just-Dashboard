import { expect, test } from "@playwright/test"
import {
  deployment,
  healthyOperations,
  json,
  mockProject,
  now,
  project,
  run,
  user,
} from "./deploy-fixture"
import type { DeploymentRuntimeServices } from "../../src/lib/types"

/**
 * The project's own pages: Overview, Deployments, Logs, Runtime, Console and
 * the game tabs. Every scenario mirrors a behaviour the pre-rebuild
 * `deploy-ui.spec.ts` protected on `deployment-overview.tsx`,
 * `deployment-workspace.tsx`, `deployment-runtime.tsx`, `deployment-logs.tsx`,
 * `deployment-console.tsx` and `deployment-game.tsx` — the selectors changed
 * with the redesign (real routes, `Row`/`Panel plain`, `VerbActions` menus),
 * the behaviours did not.
 */

test("a legacy ?tab= link lands on the new runtime route", async ({ page }) => {
  await mockProject(page)
  await page.goto("/deploy/7?tab=runtime")
  await expect(page).toHaveURL(/\/deploy\/7\/runtime$/)
})

test("a run deployed from a specific version names that version, not the branch", async ({
  page,
}) => {
  await mockProject(page)
  await page.route("**/api/v1/deploy/7/runs**", (route) => {
    const url = new URL(route.request().url())
    if (url.searchParams.get("view") !== "engine") return route.fallback()
    return json(route, {
      runs: [
        {
          ...run,
          state: "succeeded",
          sourceRevision: "a1b2c3d4e5f6",
          metadata: { requestedRef: "v1.4.2" },
        },
      ],
      running: false,
    })
  })
  await page.goto("/deploy/7/deployments")
  await expect(page.getByText(/v1\.4\.2 · a1b2c3d · by operator/)).toBeVisible()
})

test("the website preview is a desktop-width page shrunk into one tile that opens the site", async ({
  page,
}) => {
  await mockProject(page)
  let requests = 0
  await page.route("**/api/v1/deploy/7/preview-frame", (route) => {
    requests++
    return route.fulfill({
      contentType: "text/html",
      body: '<h1 id="app">My deployed website</h1>',
    })
  })
  await page.goto("/deploy/7")
  await expect.poll(() => requests).toBe(1)

  const tile = page.getByRole("link", { name: "Open api.example.test in a new tab" })
  await expect(tile).toHaveAttribute("href", "https://api.example.test/")
  await expect(tile).toHaveAttribute("target", "_blank")
  const preview = page.locator('iframe[title="Website preview for api-production"]')
  await expect(preview).toBeAttached()
  // Laid out at 1280 wide and scaled down: the frame's box on the page is no
  // wider than the tile it sits in, and its own layout width stays desktop.
  const [shown, tileWidth, laidOut] = await Promise.all([
    preview.evaluate((frame) => frame.getBoundingClientRect().width),
    tile.evaluate((element) => element.getBoundingClientRect().width),
    preview.evaluate((frame) => (frame as HTMLIFrameElement).offsetWidth),
  ])
  expect(laidOut).toBe(1280)
  expect(shown).toBeLessThanOrEqual(tileWidth)
  expect(shown).toBeGreaterThan(tileWidth * 0.9)
  await expect(page).toHaveURL(/\/deploy\/7$/)
})

test("runtime services link to the exact Docker containers and stacks", async ({
  page,
}, testInfo) => {
  await page.emulateMedia({ reducedMotion: "reduce" })
  const runtime: DeploymentRuntimeServices = {
    status: "available",
    observedAt: now,
    services: [
      {
        containerId: "abc123",
        name: "web-live",
        releaseId: 20,
        liveRelease: true,
        state: "running",
        health: "healthy",
        imageId: "sha256:abc",
        stack: "jd-e12",
        service: "web",
      },
      {
        containerId: "def456",
        name: "web-candidate",
        releaseId: 21,
        liveRelease: false,
        state: "exited",
        health: "unavailable",
        imageId: "sha256:def",
      },
    ],
  }
  await mockProject(page, { normalized: true, runtime })
  await page.routeWebSocket(/\/api\/v1\/docker\/containers\/.*\/stats\/stream/, () => {})

  for (const width of [390, 1280]) {
    await page.setViewportSize({ width, height: 900 })
    await page.goto("/deploy/7/runtime")
    const list = page.getByRole("list", { name: "Runtime services" })
    await expect(list.getByText("Live release", { exact: true })).toBeVisible()
    await expect(list.getByText("Other release", { exact: true })).toBeVisible()
    await expect(list.getByText(/Health: Not observed/)).toBeVisible()
    await expect(list.getByRole("link", { name: "web-live", exact: true })).toHaveAttribute(
      "href",
      "/docker/containers?container=abc123",
    )
    expect(
      await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth),
    ).toBe(true)
    await list.screenshot({ path: testInfo.outputPath(`runtime-${width}.png`) })
  }

  const containerLink = page.getByRole("link", { name: "web-live", exact: true })
  await containerLink.focus()
  await expect(containerLink).toBeFocused()

  await page.getByRole("button", { name: "Actions for web-live" }).click()
  await page.getByRole("menuitem", { name: /Open stack/ }).click()
  await expect(page).toHaveURL(/\/docker\/stacks\?stack=jd-e12$/)
})

test("runtime evidence distinguishes unavailable Docker from an empty managed inventory", async ({
  page,
}) => {
  const unavailable: DeploymentRuntimeServices = {
    status: "unavailable",
    observedAt: now,
    reason: "Docker cannot be reached. Open Docker to check the connection.",
    services: [],
  }
  await mockProject(page, { normalized: true, runtime: unavailable })
  await page.goto("/deploy/7/runtime")
  await expect(page.getByText("Runtime unavailable", { exact: true })).toBeVisible()
  await expect(page.getByText(unavailable.reason!)).toBeVisible()
  await expect(page.getByText("No managed runtime services", { exact: true })).toHaveCount(0)

  await mockProject(page, {
    normalized: true,
    runtime: { status: "available", observedAt: now, services: [] },
  })
  await page.goto("/deploy/7/runtime")
  await expect(page.getByText("No managed runtime services", { exact: true })).toBeVisible()
  await expect(page.getByText("Runtime unavailable", { exact: true })).toHaveCount(0)
})

test("the deployments tab reports delivery figures with their basis and window", async ({
  page,
}, testInfo) => {
  await mockProject(page)
  await page.goto("/deploy/7/deployments")
  await expect(page.getByRole("heading", { name: "Delivery" })).toBeVisible()
  await expect(page.getByText("75%", { exact: true })).toBeVisible()
  await expect(page.getByText("9 of 12 decided releases", { exact: true })).toBeVisible()
  await expect(page.getByText("2.1", { exact: true })).toBeVisible()
  await expect(page.getByText("1.5h", { exact: true })).toBeVisible()
  await expect(page.getByText("mean over 2 recovered failures", { exact: true })).toBeVisible()
  await expect(page.getByText(/Health Gate Failed/)).toBeVisible()
  await expect(page.getByTestId("insights-daily").locator("li")).toHaveCount(31)

  await page.getByRole("combobox", { name: "Insights window" }).click()
  await page.getByRole("option", { name: "Last 7 days" }).click()
  await expect(page.getByText("67%", { exact: true })).toBeVisible()
  await expect(page.getByTestId("insights-daily").locator("li")).toHaveCount(8)

  for (const width of [390, 1280]) {
    await page.setViewportSize({ width, height: 900 })
    expect(
      await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth),
    ).toBe(true)
    await page.screenshot({ path: testInfo.outputPath(`deployments-${width}.png`), fullPage: true })
  }
})

// The base fixture's single run is still mid-flight and carries no release
// yet; rollback and comparison read the run that *produced* each release, so
// this stands in two completed ones — the shape a real fleet has once more
// than one deployment has finished.
async function mockTwoCompletedReleases(page: import("@playwright/test").Page) {
  const runs = [
    { ...run, id: 84, runNumber: 2, state: "succeeded", releaseId: 20, operation: "deploy" },
    {
      ...run,
      id: 83,
      runNumber: 1,
      state: "succeeded",
      releaseId: 19,
      operation: "deploy",
      requestedAt: "2026-09-02T12:00:00Z",
      endedAt: "2026-09-02T12:02:00Z",
    },
  ]
  await page.route("**/api/v1/deploy/7/runs**", (route) => {
    const url = new URL(route.request().url())
    if (url.searchParams.get("view") !== "engine") return route.fallback()
    return json(route, { runs, running: false })
  })
  await page.route("**/api/v1/deploy/7", (route) =>
    json(route, { project, running: false, deployment: { ...deployment, activeRun: undefined } }),
  )
}

test("a retained release's row compares against the live one", async ({ page }) => {
  await mockProject(page)
  await mockTwoCompletedReleases(page)
  await page.goto("/deploy/7/deployments")

  const retainedRow = page.getByRole("listitem").filter({ hasText: "#1 Deploy" })
  const liveRow = page.getByRole("listitem").filter({ hasText: "#2 Deploy" })
  await expect(liveRow.getByText("Live", { exact: true })).toBeVisible()

  await retainedRow.getByRole("button", { name: "Actions for #1 Deploy", exact: true }).click()
  await page.getByRole("menuitem", { name: /Compare with live/ }).click()
  await expect(page.getByText("Release 19 compared with release 20")).toBeVisible()
  await expect(page.getByText("source revision")).toBeVisible()
  await expect(page.getByText("99887766554433221100 → a12bc34d56ef7890")).toBeVisible()
  const variables = page.getByRole("region", { name: "Variables" })
  await expect(variables.getByText("API_TOKEN")).toBeVisible()
  await expect(variables.getByText("digest only")).toBeVisible()
})

test("a retained release's row rolls back to live through the dialog", async ({ page }) => {
  const dashboard = await mockProject(page)
  await mockTwoCompletedReleases(page)
  await page.goto("/deploy/7/deployments")

  const retainedRow = page.getByRole("listitem").filter({ hasText: "#1 Deploy" })
  // Roll back to this release — straight to the review step, no typed phrase.
  await retainedRow.getByRole("button", { name: "Actions for #1 Deploy", exact: true }).click()
  await page.getByRole("menuitem", { name: /Roll back to this release/ }).click()
  const dialog = page.getByRole("dialog", { name: "Roll back to release #1" })
  await expect(dialog).toBeVisible()
  await expect(dialog.getByRole("textbox")).toHaveCount(0)
  await expect(dialog.getByText("api.example.test")).toBeVisible()
  await expect(dialog.getByText(/not rolled back/)).toBeVisible()
  await dialog.getByRole("button", { name: "Roll back", exact: true }).click()
  await expect(page).toHaveURL(/\/deploy\/7\/runs\/90$/)
  expect(dashboard.actions()).toEqual(["rollback"])
})

test("pin and unpin a release from its row menu", async ({ page }) => {
  await mockProject(page)
  await mockTwoCompletedReleases(page)
  let releases = [
    {
      id: 20,
      projectId: 7,
      environmentId: 12,
      number: 2,
      runId: 84,
      predecessorReleaseId: 19,
      state: "live",
      planRevision: 2,
      sourceRevision: "a12bc34d56ef7890",
      configDigest: `sha256:${"b".repeat(64)}`,
      variablesDigest: `sha256:${"c".repeat(64)}`,
      strategy: "blue_green",
      expectedDowntime: false,
      createdAt: now,
      activatedAt: now,
      pinned: false,
    },
    {
      id: 19,
      projectId: 7,
      environmentId: 12,
      number: 1,
      runId: 83,
      state: "retained",
      planRevision: 1,
      sourceRevision: "99887766554433221100",
      configDigest: `sha256:${"e".repeat(64)}`,
      variablesDigest: `sha256:${"f".repeat(64)}`,
      strategy: "blue_green",
      expectedDowntime: false,
      createdAt: "2026-09-02T12:00:00Z",
      retiredAt: now,
      pinned: true,
    },
  ]
  const pinCalls: { id: number; pinned: boolean }[] = []
  await page.route("**/api/v1/deploy/7/environments/12/releases**", (route) => {
    const request = route.request()
    const path = new URL(request.url()).pathname.replace(/^\/api\/v1/, "")
    if (path === "/deploy/7/environments/12/releases" && request.method() === "GET") {
      return json(route, releases)
    }
    const match = path.match(/^\/deploy\/7\/environments\/12\/releases\/(\d+)\/pin$/)
    if (match && request.method() === "PUT") {
      const id = Number(match[1])
      const { pinned } = request.postDataJSON() as { pinned: boolean }
      pinCalls.push({ id, pinned })
      releases = releases.map((release) => (release.id === id ? { ...release, pinned } : release))
      return json(
        route,
        releases.find((release) => release.id === id),
      )
    }
    return route.fallback()
  })
  await page.goto("/deploy/7/deployments")

  const retainedRow = page.getByRole("listitem").filter({ hasText: "#1 Deploy" })
  const liveRow = page.getByRole("listitem").filter({ hasText: "#2 Deploy" })
  await expect(retainedRow.getByText("Pinned", { exact: true })).toBeVisible()
  await expect(liveRow.getByText("Pinned", { exact: true })).toHaveCount(0)

  await retainedRow.getByRole("button", { name: "Actions for #1 Deploy", exact: true }).click()
  await page.getByRole("menuitem", { name: "Unpin release" }).click()
  await expect(page.getByText("Release unpinned", { exact: true })).toBeVisible()
  await expect(retainedRow.getByText("Pinned", { exact: true })).toHaveCount(0)

  await liveRow.getByRole("button", { name: "Actions for #2 Deploy", exact: true }).click()
  await page.getByRole("menuitem", { name: "Pin release" }).click()
  await expect(page.getByText("Release pinned", { exact: true })).toBeVisible()
  await expect(liveRow.getByText("Pinned", { exact: true })).toBeVisible()

  expect(pinCalls).toEqual([
    { id: 19, pinned: false },
    { id: 20, pinned: true },
  ])
})

test("Load older deployments appends a page using the environment and cursor", async ({ page }) => {
  await mockProject(page)
  const requests: URLSearchParams[] = []
  await page.route("**/api/v1/deploy/7/runs**", (route) => {
    const url = new URL(route.request().url())
    if (url.searchParams.get("view") !== "engine") return route.fallback()
    // The newest page carries a cursor, which is what makes the engine's
    // "there is more" visible as the button in the first place.
    if (!url.searchParams.has("before")) {
      return json(route, { runs: [run], running: true, nextBefore: run.id })
    }
    requests.push(url.searchParams)
    return json(route, {
      runs: [
        {
          ...run,
          id: 50,
          runNumber: 0,
          state: "succeeded",
          requestedAt: "2026-09-01T12:00:00Z",
          endedAt: "2026-09-01T12:01:00Z",
        },
      ],
      running: false,
    })
  })
  await page.goto("/deploy/7/deployments")
  await expect(page.getByText("#1 Deploy", { exact: true })).toBeVisible()

  await page.getByRole("button", { name: "Load older deployments", exact: true }).click()
  await expect(page.getByText("#0 Deploy", { exact: true })).toBeVisible()
  expect(requests).toHaveLength(1)
  expect(requests[0].get("environment")).toBe("12")
  expect(requests[0].get("before")).toBe("84")
  expect(requests[0].get("limit")).toBe("30")
  expect(requests[0].get("state")).toBeNull()

  // The mocked page carries no `nextBefore`, so the engine is out of older
  // runs and the button that asked for more of them goes away.
  await expect(page.getByRole("button", { name: "Load older deployments" })).toHaveCount(0)
})

test("a project whose newest page has no cursor never offers older deployments", async ({
  page,
}) => {
  await mockProject(page)
  await page.goto("/deploy/7/deployments")
  await expect(page.getByText("#1 Deploy", { exact: true })).toBeVisible()
  await expect(page.getByRole("button", { name: "Load older deployments" })).toHaveCount(0)
})

test("a rolled-back run offers no Retry", async ({ page }) => {
  await mockProject(page)
  await page.route("**/api/v1/deploy/7/runs**", (route) => {
    const url = new URL(route.request().url())
    if (url.searchParams.get("view") !== "engine") return route.fallback()
    return json(route, {
      runs: [{ ...run, state: "rolled_back", releaseId: undefined }],
      running: false,
    })
  })
  await page.goto("/deploy/7/deployments")
  await page.getByRole("button", { name: "Actions for #1 Deploy", exact: true }).click()
  await expect(page.getByRole("menuitem", { name: "Open deployment" })).toBeVisible()
  await expect(page.getByRole("menuitem", { name: "Retry" })).toHaveCount(0)
})

test("a legacy project's commit rollback asks for the short sha before it runs", async ({
  page,
}) => {
  await mockProject(page, { normalized: false })
  await page.goto("/deploy/7/deployments")
  const list = page.getByRole("list", { name: "Recoverable commits" })
  await expect(list.getByText("Current release")).toBeVisible()
  const row = list.getByRole("listitem").filter({ hasText: "Known-good release" })
  await row.getByRole("button", { name: "Roll back" }).click()

  const dialog = page.getByRole("dialog", { name: "Roll back" })
  await expect(dialog).toContainText("api-production")
  const confirmButton = dialog.getByRole("button", { name: "Roll back", exact: true })
  await expect(confirmButton).toBeDisabled()
  await dialog.getByRole("textbox").fill("9988776")
  await expect(confirmButton).toBeEnabled()
  await confirmButton.click()
  await expect(page).toHaveURL(/\/deploy\/7\/runs\/86$/)
})

test("the console tab opens a shell inside the live release container", async ({ page }) => {
  const runtime: DeploymentRuntimeServices = {
    status: "available",
    observedAt: now,
    services: [
      {
        containerId: "abc123",
        name: "jd-e12-r20",
        releaseId: 20,
        liveRelease: true,
        state: "running",
        health: "healthy",
        imageId: "sha256:abc",
      },
      {
        containerId: "def456",
        name: "jd-e12-r19",
        releaseId: 19,
        liveRelease: false,
        state: "running",
        health: "healthy",
        imageId: "sha256:def",
      },
    ],
  }
  await mockProject(page, { normalized: true, runtime })
  await page.routeWebSocket(/\/api\/v1\/docker\/containers\/.*\/exec/, () => {})
  await page.goto("/deploy/7/console")
  await expect(page.getByText("jd-e12-r20 · deployment shell")).toBeVisible()
  await page.getByRole("combobox", { name: "Console container" }).click()
  await page.getByRole("option", { name: "jd-e12-r19" }).click()
  await expect(page.getByText("jd-e12-r19 · deployment shell")).toBeVisible()

  await mockProject(page, {
    normalized: true,
    runtime: { status: "available", observedAt: now, services: [] },
  })
  await page.goto("/deploy/7/console")
  await expect(page.getByText("No running container")).toBeVisible()
})

test("Runtime hands a service straight to its own console preselected", async ({ page }) => {
  const runtime: DeploymentRuntimeServices = {
    status: "available",
    observedAt: now,
    services: [
      {
        containerId: "abc123",
        name: "jd-e12-r20",
        releaseId: 20,
        liveRelease: true,
        state: "running",
        health: "healthy",
        imageId: "sha256:abc",
      },
      {
        containerId: "def456",
        name: "jd-e12-r19",
        releaseId: 19,
        liveRelease: false,
        state: "running",
        health: "healthy",
        imageId: "sha256:def",
      },
    ],
  }
  await mockProject(page, { normalized: true, runtime })
  await page.routeWebSocket(/\/api\/v1\/docker\/containers\/.*\/(exec|stats\/stream)/, () => {})
  await page.goto("/deploy/7/runtime")
  const list = page.getByRole("list", { name: "Runtime services" })
  await list.getByRole("button", { name: "Actions for jd-e12-r19" }).click()
  await page.getByRole("menuitem", { name: "Console" }).click()
  await expect(page).toHaveURL(/\/deploy\/7\/console\?service=def456$/)
  await expect(page.getByText("jd-e12-r19 · deployment shell")).toBeVisible()
})

test("the game workspace sends commands, moderates players and edits only declared settings", async ({
  page,
}) => {
  const sent: string[] = []
  let written: Record<string, string> | null = null
  let players = {
    supported: true,
    status: "available",
    online: 1,
    maximum: 20,
    names: ["Notch"],
    observedAt: now,
  }
  await mockProject(page)
  await page.route("**/api/v1/deploy/7", (route) =>
    json(route, {
      project,
      running: false,
      deployment: { ...deployment, profile: "game", buildMethod: "image", activeRun: undefined },
    }),
  )
  await page.route("**/api/v1/deploy/7/game**", async (route) => {
    const request = route.request()
    const path = new URL(request.url()).pathname.replace(/^\/api\/v1/, "")
    if (path === "/deploy/7/game") {
      return json(route, {
        status: "available",
        blueprintId: "minecraft-java",
        edition: "java",
        containerId: "mc123",
        address: "play.example.test:25565",
        console: true,
        players,
        files: [],
      })
    }
    if (path === "/deploy/7/game/players" && request.method() === "GET") {
      return json(route, players)
    }
    if (path === "/deploy/7/game/console") {
      const body = request.postDataJSON() as { command: string }
      sent.push(body.command)
      return json(route, {
        command: body.command,
        output: "There are 1 of a max of 20 players online: Notch",
        exitCode: 0,
        executedAt: now,
      })
    }
    if (path.startsWith("/deploy/7/game/players/")) {
      const action = path.split("/").at(-1)
      sent.push(`${action}:${(request.postDataJSON() as { name: string }).name}`)
      players = { ...players, online: 0, names: [] }
      return json(route, { command: action, output: "", exitCode: 0, executedAt: now })
    }
    if (path === "/deploy/7/game/properties" && request.method() === "GET") {
      return json(route, {
        status: "available",
        path: "/data/server.properties",
        raw: "#Minecraft server properties\nmotd=Old name\nmax-players=20\nexperimental=keep-me\n",
        values: { motd: "Old name", "max-players": "20", experimental: "keep-me" },
        restartRequired: true,
        known: [
          { key: "motd", kind: "text", label: "Server list description" },
          {
            key: "max-players",
            kind: "number",
            label: "Maximum players",
            minimum: 1,
            maximum: 1000,
          },
          {
            key: "difficulty",
            kind: "choice",
            label: "Difficulty",
            choices: [
              { value: "easy", label: "Easy" },
              { value: "normal", label: "Normal" },
            ],
          },
        ],
      })
    }
    if (path === "/deploy/7/game/properties" && request.method() === "PUT") {
      written = (request.postDataJSON() as { changes: Record<string, string> }).changes
      return json(route, { applied: Object.keys(written), restartRequired: true })
    }
    return route.fulfill({
      status: 503,
      contentType: "application/json",
      body: JSON.stringify({ error: { code: "not_available", message: "Not mocked" } }),
    })
  })

  await page.goto("/deploy/7/console")
  await expect(page.getByRole("log", { name: "Console transcript" })).toBeVisible()
  const field = page.getByRole("textbox", { name: "Command" })
  await field.fill("list")
  await page.getByRole("button", { name: "Send" }).click()
  await expect(page.getByText("There are 1 of a max of 20 players online: Notch")).toBeVisible()
  expect(sent).toContain("list")
  // The previous command comes back with the up arrow rather than being retyped.
  await field.focus()
  await page.keyboard.press("ArrowUp")
  await expect(field).toHaveValue("list")
  await expect(page.getByText("Only plain game commands are accepted.")).toBeVisible()

  await page.goto("/deploy/7/players")
  const list = page.getByRole("list", { name: "Online players" })
  await expect(list.getByText("Notch")).toBeVisible()
  await list.getByRole("button", { name: "Kick" }).click()
  expect(sent).toContain("kick:Notch")

  await page.goto("/deploy/7/game-settings")
  await expect(page.getByLabel("Server list description")).toHaveValue("Old name")
  // A key the blueprint does not declare stays in the raw preview and gets no
  // control of its own.
  await page.getByText("View the file on disk", { exact: true }).click()
  await expect(page.getByText("experimental=keep-me")).toBeVisible()
  await expect(page.getByLabel("experimental")).toHaveCount(0)
  await page.getByLabel("Server list description").fill("New name")
  await expect(page.getByText("The server reads this file on start.")).toBeVisible()
  await page.getByRole("button", { name: /^Save/ }).click()
  await expect.poll(() => written).toEqual({ motd: "New name" })
})

test("a stopped project reads Stopped and starts from the primary button", async ({ page }) => {
  const dashboard = await mockProject(page)
  await page.route("**/api/v1/deploy/7", (route) =>
    json(route, {
      project,
      running: false,
      deployment: { ...deployment, stopped: true, activeRun: undefined },
    }),
  )
  await page.goto("/deploy/7")
  await expect(page.getByText("Stopped", { exact: true })).toBeVisible()
  await page.getByRole("button", { name: "Start", exact: true }).click()
  await expect(page).toHaveURL(/\/deploy\/7\/runs\/88$/)
  expect(dashboard.actions()).toEqual(["start"])
})

for (const normalized of [false, true]) {
  test(`delete project is accessible and confirmed for ${normalized ? "normalized" : "legacy"} deployments`, async ({
    page,
  }) => {
    await mockProject(page, { normalized })
    let deleted = 0
    await page.route("**/api/v1/deploy/7", async (route) => {
      if (route.request().method() !== "DELETE") return route.fallback()
      deleted++
      await route.fulfill({ status: 204 })
    })
    await page.goto("/deploy/7")
    await page.getByRole("button", { name: "Deployment actions", exact: true }).click()
    await page.getByRole("menuitem", { name: /Archive deployment/ }).click()
    const dialog = page.getByRole("dialog")
    await expect(dialog).toContainText("Running containers, routes, and persistent data remain")
    await dialog.getByRole("button", { name: "Cancel", exact: true }).click()
    expect(deleted).toBe(0)

    await page.getByRole("button", { name: "Deployment actions", exact: true }).click()
    await page.getByRole("menuitem", { name: /Archive deployment/ }).click()
    await page
      .getByRole("dialog")
      .getByRole("button", { name: "Archive deployment", exact: true })
      .click()
    await expect(page).toHaveURL(/\/deploy$/)
    expect(deleted).toBe(1)
  })
}

test("delete project keeps the project open when the server refuses", async ({ page }) => {
  await mockProject(page)
  await page.route("**/api/v1/deploy/7", async (route) => {
    if (route.request().method() !== "DELETE") return route.fallback()
    await route.fulfill({
      status: 409,
      contentType: "application/json",
      body: JSON.stringify({ error: { code: "conflict", message: "Project is busy" } }),
    })
  })
  await page.goto("/deploy/7")
  await page.getByRole("button", { name: "Deployment actions", exact: true }).click()
  await page.getByRole("menuitem", { name: /Archive deployment/ }).click()
  await page
    .getByRole("dialog")
    .getByRole("button", { name: "Archive deployment", exact: true })
    .click()
  await expect(page.getByText("Project is busy", { exact: true })).toBeVisible()
  await expect(page.getByRole("dialog")).toBeVisible()
  await expect(page).toHaveURL(/\/deploy\/7$/)
})

test("delete project is hidden without destructive capability", async ({ page }) => {
  await mockProject(page)
  await page.route("**/api/v1/auth/session", (route) =>
    json(route, { ...user, capabilities: ["read"] }),
  )
  await page.goto("/deploy/7")
  await expect(page.getByRole("heading", { name: /api-production/ })).toBeVisible()
  await expect(page.getByText("Archive deployment")).toHaveCount(0)
})

test("Duplicate project names a copy and navigates to its resumed draft", async ({ page }) => {
  await mockProject(page)
  let posted: Record<string, unknown> | undefined
  await page.route("**/api/v1/deploy/7/duplicate", async (route) => {
    if (route.request().method() !== "POST") return route.fallback()
    posted = route.request().postDataJSON() as Record<string, unknown>
    await json(route, { id: "new-draft-9", currentStep: "configuration" })
  })

  await page.goto("/deploy/7")
  await page.getByRole("button", { name: "Deployment actions", exact: true }).click()
  await page.getByRole("menuitem", { name: "Duplicate project…" }).click()

  const dialog = page.getByRole("dialog", { name: "Duplicate api-production" })
  await expect(dialog.getByLabel("Name")).toHaveValue("api-production-copy")
  await dialog.getByRole("button", { name: "Duplicate project" }).click()

  await expect(page).toHaveURL(/\/deploy\/new\?draft=new-draft-9$/)
  expect(posted).toEqual({ name: "api-production-copy" })
})

test("Duplicate project refuses a name already in use", async ({ page }) => {
  await mockProject(page)
  await page.route("**/api/v1/deploy/7/duplicate", async (route) => {
    if (route.request().method() !== "POST") return route.fallback()
    await route.fulfill({
      status: 409,
      contentType: "application/json",
      body: JSON.stringify({ error: { code: "name_taken", message: "That name is taken." } }),
    })
  })

  await page.goto("/deploy/7")
  await page.getByRole("button", { name: "Deployment actions", exact: true }).click()
  await page.getByRole("menuitem", { name: "Duplicate project…" }).click()
  await page
    .getByRole("dialog", { name: "Duplicate api-production" })
    .getByRole("button", { name: "Duplicate project" })
    .click()
  await expect(page.getByText("That name is already used.")).toBeVisible()
  await expect(page).toHaveURL(/\/deploy\/7$/)
})

test("Duplicate project is hidden for an archived project", async ({ page }) => {
  await mockProject(page)
  await page.route("**/api/v1/deploy/7", async (route) => {
    if (route.request().method() !== "GET") return route.fallback()
    await json(route, {
      project: { ...project, archivedAt: now },
      running: false,
      deployment: { ...deployment, activeRun: undefined },
    })
  })
  await page.goto("/deploy/7")
  await page.getByRole("button", { name: "Deployment actions", exact: true }).click()
  await expect(page.getByRole("menuitem", { name: "Duplicate project…" })).toHaveCount(0)
})

test("overview, logs and runtime read cleanly at phone and desktop widths", async ({
  page,
}, testInfo) => {
  const runtime: DeploymentRuntimeServices = {
    status: "available",
    observedAt: now,
    services: [
      {
        containerId: "abc123",
        name: "web-live",
        releaseId: 20,
        liveRelease: true,
        state: "running",
        health: "healthy",
        imageId: "sha256:abc",
        startedAt: now,
      },
    ],
  }
  await mockProject(page, { normalized: true, runtime, operations: healthyOperations })
  await page.routeWebSocket(/\/api\/v1\/docker\/containers\/.*\/stats\/stream/, () => {})
  await page.route("**/api/v1/deploy/7/preview-frame", (route) =>
    route.fulfill({ contentType: "text/html", body: "<h1>site</h1>" }),
  )

  for (const width of [390, 1280]) {
    await page.setViewportSize({ width, height: 900 })

    await page.goto("/deploy/7")
    await expect(page.getByRole("heading", { name: "Production", exact: true })).toBeVisible()
    expect(
      await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth),
    ).toBe(true)
    await page.screenshot({ path: testInfo.outputPath(`overview-${width}.png`), fullPage: true })

    await page.goto("/deploy/7/logs")
    expect(
      await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth),
    ).toBe(true)
    await page.screenshot({ path: testInfo.outputPath(`logs-${width}.png`), fullPage: true })
  }
})
