import { expect, test } from "@playwright/test"
import { json, mockProject } from "./deploy-fixture"

/**
 * Project settings, part A: General, Build, Runtime, Environment variables,
 * Danger zone.
 *
 * Every test registers its own `page.route` after `mockProject` so the
 * newest handler wins; where the fixture already owns a stateful mock (the
 * environment configuration, the git policy) the override captures the
 * request and calls `route.fallback()` so the fixture's own bookkeeping still
 * runs. Renaming and the dotenv import have no fixture support at all, so
 * those routes are answered directly.
 */

function overflow(page: import("@playwright/test").Page) {
  return page.evaluate(() => document.documentElement.scrollWidth - window.innerWidth)
}

// The pending-changes banner also names a changed variable in one of its own
// `<li>`s, so a bare `page.locator("li", { hasText: name })` matches it too.
// Scoping to the named list is what actually picks out the variable row.
function variableRow(page: import("@playwright/test").Page, name: string) {
  return page.getByRole("list", { name: "Environment variables" }).locator("li", { hasText: name })
}

test.describe("General settings", () => {
  test("shows source facts and saves a new project name", async ({ page }) => {
    const renames: unknown[] = []
    await mockProject(page)
    await page.route("**/api/v1/deploy/7", async (route) => {
      if (route.request().method() !== "PUT") return route.fallback()
      const body = route.request().postDataJSON()
      renames.push(body)
      await json(route, { name: body.name })
    })

    await page.goto("/deploy/7/settings/general")
    await expect(page.getByRole("heading", { name: "Project", exact: true })).toBeVisible()
    await expect(page.getByText("/srv/api-production")).toBeVisible()
    await expect(page.getByText("Blue Green")).toBeVisible()

    await page.setViewportSize({ width: 1280, height: 900 })
    await page.screenshot({ path: test.info().outputPath("general-1280.png"), fullPage: true })

    const nameCard = page
      .locator("form")
      .filter({ has: page.getByRole("heading", { name: "Project name" }) })
    await nameCard.getByLabel("Project name").fill("api-production-2")
    await nameCard.getByRole("button", { name: "Save" }).click()
    await expect(page.getByText("Project renamed")).toBeVisible()
    expect(renames.at(-1)).toEqual({ name: "api-production-2" })

    await page.setViewportSize({ width: 390, height: 844 })
    expect(await overflow(page)).toBeLessThanOrEqual(1)
    await page.screenshot({ path: test.info().outputPath("general-390.png"), fullPage: true })
  })

  test("renaming to a name already in use shows a field error", async ({ page }) => {
    await mockProject(page)
    await page.route("**/api/v1/deploy/7", async (route) => {
      if (route.request().method() !== "PUT") return route.fallback()
      await route.fulfill({
        status: 409,
        contentType: "application/json",
        body: JSON.stringify({ error: { code: "name_taken", message: "That name is taken." } }),
      })
    })

    await page.goto("/deploy/7/settings/general")
    const nameCard = page
      .locator("form")
      .filter({ has: page.getByRole("heading", { name: "Project name" }) })
    await nameCard.getByLabel("Project name").fill("payments-api")
    await nameCard.getByRole("button", { name: "Save" }).click()
    await expect(page.getByText("That name is already used by another project")).toBeVisible()
  })

  test("saves a manual-only deployment policy with path filters and commit statuses off", async ({
    page,
  }) => {
    const fixture = await mockProject(page)
    await page.goto("/deploy/7/settings/general")

    const gitCard = page
      .locator("form")
      .filter({ has: page.getByRole("heading", { name: "Git", exact: true }) })
    await expect(gitCard.getByText(/New commits to main deploy automatically/)).toBeVisible()

    await gitCard.getByLabel("Include paths").fill("services/api/**")
    await gitCard.getByLabel("Exclude paths").fill("docs/**")
    await gitCard.getByRole("switch", { name: /Report deployment status/ }).click()
    await gitCard.getByRole("switch", { name: /^Deploy automatically/ }).click()
    await gitCard.getByRole("button", { name: "Save" }).click()

    await expect(page.getByText("Deployment policy saved")).toBeVisible()
    await expect(gitCard.getByText(/cannot queue new deployments/)).toBeVisible()

    const policy = fixture.gitPolicy()
    expect(policy.automatic).toBe(false)
    expect(policy.commitStatuses).toBe(false)
    expect(policy.watchInclude).toEqual(["services/api/**"])
    expect(policy.watchExclude).toEqual(["docs/**"])
  })

  test("a read-only role sees no save buttons", async ({ page }) => {
    await mockProject(page)
    await page.route("**/api/v1/auth/session", (route) =>
      json(route, {
        authenticated: true,
        needsTotp: false,
        needsEnrollment: false,
        require2fa: false,
        capabilities: ["read"],
      }),
    )
    await page.goto("/deploy/7/settings/general")
    await expect(page.getByRole("heading", { name: "Git", exact: true })).toBeVisible()
    await expect(page.getByRole("button", { name: "Save" })).toHaveCount(0)
  })
})

test.describe("Build settings", () => {
  test("rewrites build and start commands for the chosen package manager, keeping dependencies", async ({
    page,
  }) => {
    const writes: Array<Record<string, unknown>> = []
    await mockProject(page)
    await page.route("**/api/v1/deploy/7/environments/12/configuration", async (route) => {
      if (route.request().method() === "PUT")
        writes.push(route.request().postDataJSON() as Record<string, unknown>)
      await route.fallback()
    })

    await page.goto("/deploy/7/settings/build")
    const buildCard = page
      .locator("form")
      .filter({ has: page.getByRole("heading", { name: "Build", exact: true }) })

    await buildCard.getByLabel("Build command").fill("bun run build")
    await buildCard.getByLabel("Start command").fill("bun run start")
    await buildCard.getByLabel("Package manager").click()
    await page.getByRole("option", { name: "npm", exact: true }).click()
    await expect(buildCard.getByLabel("Build command")).toHaveValue("npm run build")
    await expect(buildCard.getByLabel("Start command")).toHaveValue("npm run start")

    await page.setViewportSize({ width: 1280, height: 900 })
    await page.screenshot({ path: test.info().outputPath("build-1280.png"), fullPage: true })

    await buildCard.getByRole("button", { name: "Save" }).click()
    await expect(page.getByText("Build settings saved")).toBeVisible()

    const saved = writes.at(-1) as { build: Record<string, unknown>; dependencies: unknown }
    expect(saved.build.buildCommand).toBe("npm run build")
    expect(saved.build.startCommand).toBe("npm run start")
    expect(saved.build.packageManager).toBe("npm")
    expect(saved.dependencies).toEqual([
      {
        kind: "backup",
        ownership: "linked",
        resourceKind: "backup_job",
        resourceId: "4",
        config: { requiredBeforeDeploy: true, maxAgeSeconds: 86400, requireRestoreTest: false },
      },
    ])

    await page.setViewportSize({ width: 390, height: 844 })
    expect(await overflow(page)).toBeLessThanOrEqual(1)
    await page.screenshot({ path: test.info().outputPath("build-390.png"), fullPage: true })
  })

  test("adds and saves a release task", async ({ page }) => {
    const writes: Array<Record<string, unknown>> = []
    await mockProject(page)
    await page.route("**/api/v1/deploy/7/environments/12/configuration", async (route) => {
      if (route.request().method() === "PUT")
        writes.push(route.request().postDataJSON() as Record<string, unknown>)
      await route.fallback()
    })

    await page.goto("/deploy/7/settings/build")
    const tasksCard = page
      .locator("form")
      .filter({ has: page.getByRole("heading", { name: "Release tasks" }) })
    await expect(tasksCard.getByText("No release tasks configured.")).toBeVisible()

    await tasksCard.getByRole("button", { name: "Add release task" }).click()
    await tasksCard.getByLabel("Release task 1 name").fill("Migrate database")
    await tasksCard.getByLabel("Release task 1 command").fill("./bin/migrate")
    await tasksCard.getByRole("button", { name: "Save" }).click()
    await expect(page.getByText("Build settings saved")).toBeVisible()

    const saved = writes.at(-1) as { build: { releaseTasks: unknown } }
    expect(saved.build.releaseTasks).toEqual([
      {
        name: "Migrate database",
        command: "./bin/migrate",
        workingDirectory: "",
        timeoutSeconds: 300,
        env: [],
      },
    ])
  })

  test("a read-only role sees no save button", async ({ page }) => {
    await mockProject(page)
    await page.route("**/api/v1/auth/session", (route) =>
      json(route, {
        authenticated: true,
        needsTotp: false,
        needsEnrollment: false,
        require2fa: false,
        capabilities: ["read"],
      }),
    )
    await page.goto("/deploy/7/settings/build")
    await expect(page.getByRole("heading", { name: "Build", exact: true })).toBeVisible()
    await expect(page.getByRole("button", { name: "Save" })).toHaveCount(0)
  })
})

test.describe("Runtime settings", () => {
  test("saves memory, CPU, process limits and the restart policy", async ({ page }) => {
    const writes: Array<Record<string, unknown>> = []
    await mockProject(page)
    await page.route("**/api/v1/deploy/7/environments/12/configuration", async (route) => {
      if (route.request().method() === "PUT")
        writes.push(route.request().postDataJSON() as Record<string, unknown>)
      await route.fallback()
    })

    await page.goto("/deploy/7/settings/runtime")
    const runtimeCard = page
      .locator("form")
      .filter({ has: page.getByRole("heading", { name: "Runtime", exact: true }) })

    await runtimeCard.getByLabel("Memory limit (MiB)").fill("512")
    await runtimeCard.getByLabel("CPU limit").fill("1.5")
    await runtimeCard.getByLabel("Process limit").fill("256")
    await runtimeCard.getByLabel("Restart policy").click()
    await page.getByRole("option", { name: "On failure" }).click()

    await page.setViewportSize({ width: 1280, height: 900 })
    await page.screenshot({ path: test.info().outputPath("runtime-1280.png"), fullPage: true })

    await runtimeCard.getByRole("button", { name: "Save" }).click()
    await expect(page.getByText("Runtime settings saved")).toBeVisible()

    const saved = writes.at(-1) as { runtime: Record<string, unknown> }
    expect(saved.runtime.memoryMb).toBe(512)
    expect(saved.runtime.cpus).toBe(1.5)
    expect(saved.runtime.pidsLimit).toBe(256)
    expect(saved.runtime.restartPolicy).toBe("on-failure")

    await page.setViewportSize({ width: 390, height: 844 })
    expect(await overflow(page)).toBeLessThanOrEqual(1)
    await page.screenshot({ path: test.info().outputPath("runtime-390.png"), fullPage: true })
  })

  test("adds and saves a health check", async ({ page }) => {
    const writes: Array<Record<string, unknown>> = []
    await mockProject(page)
    await page.route("**/api/v1/deploy/7/environments/12/configuration", async (route) => {
      if (route.request().method() === "PUT")
        writes.push(route.request().postDataJSON() as Record<string, unknown>)
      await route.fallback()
    })

    await page.goto("/deploy/7/settings/runtime")
    const checksCard = page
      .locator("form")
      .filter({ has: page.getByRole("heading", { name: "Health checks" }) })
    await expect(checksCard.getByText("No health checks configured.")).toBeVisible()

    await checksCard.getByRole("button", { name: "Add check" }).click()
    await checksCard.getByLabel("Health check 1 name").fill("HTTP readiness")
    await checksCard.getByRole("button", { name: "Save" }).click()
    await expect(page.getByText("Runtime settings saved")).toBeVisible()

    const saved = writes.at(-1) as { checks: Array<Record<string, unknown>> }
    expect(saved.checks).toHaveLength(1)
    expect(saved.checks[0]).toMatchObject({
      name: "HTTP readiness",
      kind: "http",
      phase: "readiness",
      required: true,
    })
    expect(saved.checks[0].config).toMatchObject({
      path: "/",
      attempts: 20,
      timeoutSeconds: 5,
      intervalSeconds: 3,
    })
  })

  test("a read-only role sees no save buttons", async ({ page }) => {
    await mockProject(page)
    await page.route("**/api/v1/auth/session", (route) =>
      json(route, {
        authenticated: true,
        needsTotp: false,
        needsEnrollment: false,
        require2fa: false,
        capabilities: ["read"],
      }),
    )
    await page.goto("/deploy/7/settings/runtime")
    await expect(page.getByRole("heading", { name: "Health checks" })).toBeVisible()
    await expect(page.getByRole("button", { name: "Save" })).toHaveCount(0)
  })
})

test.describe("Environment variables", () => {
  test("adds a variable and shows it pending beside the existing secret", async ({ page }) => {
    await mockProject(page)
    await page.goto("/deploy/7/settings/variables")

    await expect(page.getByRole("heading", { name: "Environment variables" })).toBeVisible()
    await expect(variableRow(page, "API_TOKEN")).toBeVisible()
    await expect(variableRow(page, "API_TOKEN").getByText("Pending")).toBeVisible()

    await page.getByLabel("Variable name").fill("database_url")
    await page.getByLabel("Value", { exact: true }).fill("postgres://example")
    await page.getByRole("button", { name: "Add variable" }).click()
    await expect(page.getByText("Variable saved")).toBeVisible()
    await expect(variableRow(page, "DATABASE_URL")).toBeVisible()

    await page.setViewportSize({ width: 1280, height: 900 })
    await page.screenshot({ path: test.info().outputPath("variables-1280.png"), fullPage: true })
    await page.setViewportSize({ width: 390, height: 844 })
    expect(await overflow(page)).toBeLessThanOrEqual(1)
    await page.screenshot({ path: test.info().outputPath("variables-390.png"), fullPage: true })
  })

  test("reveals and hides a variable's stored value", async ({ page }) => {
    await mockProject(page)
    await page.goto("/deploy/7/settings/variables")

    const row = variableRow(page, "API_TOKEN")
    await row.getByRole("button", { name: "Reveal" }).click()
    await expect(row.getByText("revealed-browser-secret")).toBeVisible()
    await row.getByRole("button", { name: "Hide" }).click()
    await expect(row.getByText("revealed-browser-secret")).toHaveCount(0)
  })

  test("rotating or generating a secret shows the value once", async ({ page }) => {
    await mockProject(page)
    await page.goto("/deploy/7/settings/variables")

    const row = variableRow(page, "API_TOKEN")
    await row.getByRole("button", { name: "Actions for API_TOKEN" }).click()
    await page.getByRole("menuitem", { name: "Rotate" }).click()
    await expect(page.getByText("API_TOKEN was generated")).toBeVisible()
    await expect(page.getByText("rotated-browser-secret")).toBeVisible()
    await page.getByRole("button", { name: "Dismiss" }).click()
    await expect(page.getByText("rotated-browser-secret")).toHaveCount(0)

    await page.getByLabel("Variable name").fill("new_secret")
    await page.getByRole("button", { name: "Generate secret" }).click()
    await expect(page.getByText("NEW_SECRET was generated")).toBeVisible()
    await expect(page.getByText("generated-browser-secret")).toBeVisible()
  })

  test("removing a variable asks for confirmation before deleting it", async ({ page }) => {
    const deletes: unknown[] = []
    await mockProject(page)
    await page.route("**/api/v1/deploy/7/environments/12/variables/API_TOKEN", async (route) => {
      if (route.request().method() !== "DELETE") return route.fallback()
      deletes.push(route.request().postDataJSON())
      await json(route, { desiredRevision: 4 })
    })

    await page.goto("/deploy/7/settings/variables")
    const row = variableRow(page, "API_TOKEN")
    await row.getByRole("button", { name: "Actions for API_TOKEN" }).click()
    await page.getByRole("menuitem", { name: "Remove" }).click()
    await expect(page.getByRole("heading", { name: "Remove API_TOKEN" })).toBeVisible()
    await page.getByRole("button", { name: "Remove variable" }).click()

    await expect(page.getByText("Remove API_TOKEN completed")).toBeVisible()
    expect(deletes.at(-1)).toEqual({ revision: 3 })
  })

  test("imports a dotenv paste with the chosen scopes and sensitivity", async ({ page }) => {
    const imports: unknown[] = []
    await mockProject(page)
    await page.route("**/api/v1/deploy/7/environments/12/variables/import", async (route) => {
      imports.push(route.request().postDataJSON())
      await json(route, { desiredRevision: 4, variable: {}, variables: [] })
    })

    await page.goto("/deploy/7/settings/variables")
    await page.getByRole("button", { name: "Paste a .env instead" }).click()
    await page.getByLabel("Dotenv values").fill("FOO=bar\nBAZ=qux")
    await page.getByRole("button", { name: "Import variables" }).click()
    await expect(page.getByText("Variables imported")).toBeVisible()

    expect(imports.at(-1)).toMatchObject({
      revision: 3,
      dotenv: "FOO=bar\nBAZ=qux",
      sensitivity: "secret",
      scopes: ["runtime"],
    })
  })

  test("a read-only role sees no add, generate or save controls", async ({ page }) => {
    await mockProject(page)
    await page.route("**/api/v1/auth/session", (route) =>
      json(route, {
        authenticated: true,
        needsTotp: false,
        needsEnrollment: false,
        require2fa: false,
        capabilities: ["read"],
      }),
    )
    await page.goto("/deploy/7/settings/variables")
    await expect(variableRow(page, "API_TOKEN")).toBeVisible()
    await expect(page.getByRole("button", { name: "Add variable" })).toHaveCount(0)
    await expect(page.getByRole("button", { name: "Generate secret" })).toHaveCount(0)
    await expect(page.getByRole("button", { name: "Paste a .env instead" })).toHaveCount(0)
  })

  test("editing a variable requires a real value again, and saves it non-empty", async ({
    page,
  }) => {
    const writes: unknown[] = []
    await mockProject(page)
    await page.route("**/api/v1/deploy/7/environments/12/variables/API_TOKEN", async (route) => {
      if (route.request().method() !== "PUT") return route.fallback()
      writes.push(route.request().postDataJSON())
      await route.fallback()
    })

    await page.goto("/deploy/7/settings/variables")
    const row = variableRow(page, "API_TOKEN")
    await row.getByRole("button", { name: "Actions for API_TOKEN" }).click()
    await page.getByRole("menuitem", { name: "Edit" }).click()

    // Scoped to the add/edit form (the page's only <form>): the dev
    // server's own Next.js dev-tools overlay carries an unrelated `alert`
    // role on every page.
    const form = page.locator("form")
    await expect(page.getByRole("button", { name: "Save variable" })).toBeVisible()
    await expect(page.getByLabel("Value", { exact: true })).toHaveValue("")
    await expect(form.getByRole("alert")).toHaveCount(0)

    // A scope is changed, but the value is left blank — loadIntoForm never
    // reads the stored secret back, so this must be refused, not sent empty.
    await page.getByRole("switch", { name: /^Build/ }).click()
    await page.getByRole("button", { name: "Save variable" }).click()
    await expect(form.getByRole("alert")).toHaveText(
      "Enter the value again — the dashboard does not read it back.",
    )
    expect(writes).toHaveLength(0)

    await page.getByLabel("Value", { exact: true }).fill("sk_live_regenerated")
    await page.getByRole("button", { name: "Save variable" }).click()
    await expect(page.getByText("Variable saved")).toBeVisible()
    expect(writes.at(-1)).toMatchObject({
      revision: 3,
      value: "sk_live_regenerated",
      scopes: ["runtime", "build"],
    })
  })

  test("rotating a secret disables the row's other actions while pending, and retries a stale revision once", async ({
    page,
  }) => {
    await mockProject(page)
    let releaseRotate: () => void = () => {}
    const gate = new Promise<void>((resolve) => {
      releaseRotate = resolve
    })
    let attempts = 0
    await page.route(
      "**/api/v1/deploy/7/environments/12/variables/API_TOKEN/rotate",
      async (route) => {
        attempts += 1
        if (attempts === 1) {
          await gate
          await route.fulfill({
            status: 409,
            contentType: "application/json",
            body: JSON.stringify({ error: { code: "revision_conflict", message: "stale" } }),
          })
          return
        }
        await route.fallback()
      },
    )

    await page.goto("/deploy/7/settings/variables")
    const row = variableRow(page, "API_TOKEN")
    await row.getByRole("button", { name: "Actions for API_TOKEN" }).click()
    await page.getByRole("menuitem", { name: "Rotate" }).click()

    await expect(row.getByRole("button", { name: "Reveal" })).toBeDisabled()
    releaseRotate()

    await expect(page.getByText("API_TOKEN was generated")).toBeVisible()
    expect(attempts).toBe(2)
    await expect(row.getByRole("button", { name: "Reveal" })).toBeEnabled()
  })

  test("an unrecoverable row action error shows as a toast, never inside the add-variable card", async ({
    page,
  }) => {
    await mockProject(page)
    await page.route(
      "**/api/v1/deploy/7/environments/12/variables/API_TOKEN/rotate",
      async (route) => {
        await route.fulfill({
          status: 500,
          contentType: "application/json",
          body: JSON.stringify({
            error: { code: "internal", message: "rotation service is down" },
          }),
        })
      },
    )
    await page.goto("/deploy/7/settings/variables")
    const row = variableRow(page, "API_TOKEN")
    await row.getByRole("button", { name: "Actions for API_TOKEN" }).click()
    await page.getByRole("menuitem", { name: "Rotate" }).click()

    await expect(page.getByText("Could not rotate API_TOKEN")).toBeVisible()
    const addVariableCard = page
      .locator("form")
      .filter({ has: page.getByRole("heading", { name: "Add a variable" }) })
    await expect(addVariableCard.getByText("rotation service is down")).toHaveCount(0)
  })
})

test.describe("Danger zone", () => {
  test("a stop refusal shows the server's message as a toast and re-reads the state", async ({
    page,
  }) => {
    await mockProject(page)
    await page.route("**/api/v1/deploy/7/environments/12/runs", async (route) => {
      if (route.request().method() !== "POST") return route.fallback()
      const body = route.request().postDataJSON() as { operation: string }
      if (body.operation !== "stop") return route.fallback()
      await route.fulfill({
        status: 409,
        contentType: "application/json",
        body: JSON.stringify({
          error: { code: "already_stopped", message: "The application is already stopped." },
        }),
      })
    })
    let reads = 0
    await page.route("**/api/v1/deploy/7", async (route) => {
      if (route.request().method() === "GET") reads += 1
      await route.fallback()
    })

    await page.goto("/deploy/7/settings/danger")
    await expect(page.getByRole("heading", { name: "Stop the application" })).toBeVisible()
    const readsBeforeClick = reads
    await page.getByRole("button", { name: "Stop", exact: true }).click()

    await expect(page.getByText("Could not stop the application")).toBeVisible()
    await expect(page.getByText("The application is already stopped.")).toBeVisible()
    // Re-reads immediately rather than leaving the card's own "stopped"
    // disagreeing with the server for up to the next 5s poll.
    await expect.poll(() => reads).toBeGreaterThan(readsBeforeClick)
  })

  test("without the destructive capability there is no Stop or Restart, only Rebuild", async ({
    page,
  }) => {
    await mockProject(page)
    await page.route("**/api/v1/auth/session", (route) =>
      json(route, {
        authenticated: true,
        needsTotp: false,
        needsEnrollment: false,
        require2fa: false,
        capabilities: ["read", "service.control", "system.admin"],
      }),
    )
    await page.goto("/deploy/7/settings/danger")
    await expect(page.getByRole("button", { name: "Deployment actions" })).toBeVisible()
    await expect(page.getByRole("heading", { name: "Stop the application" })).toHaveCount(0)

    // The header's menu takes the service down through the same operations,
    // so it agrees with the card: stopping and restarting need `destructive`,
    // a rebuild does not.
    await page.getByRole("button", { name: "Deployment actions" }).click()
    await expect(page.getByRole("menuitem", { name: "Rebuild without cache" })).toBeVisible()
    await expect(page.getByRole("menuitem", { name: "Stop", exact: true })).toHaveCount(0)
    await expect(page.getByRole("menuitem", { name: "Restart" })).toHaveCount(0)
  })
})

test.describe("Build settings for static output and Python", () => {
  test("static output offers the single-page fallback and Python shows its version", async ({
    page,
  }) => {
    const writes: Array<Record<string, unknown>> = []
    await mockProject(page)
    await page.route("**/api/v1/deploy/7/environments/12/configuration", async (route) => {
      if (route.request().method() === "PUT")
        writes.push(route.request().postDataJSON() as Record<string, unknown>)
      await route.fallback()
    })

    await page.goto("/deploy/7/settings/build")
    const buildCard = page
      .locator("form")
      .filter({ has: page.getByRole("heading", { name: "Build", exact: true }) })

    // A server has nothing for nginx to fall back to, so the switch only
    // appears once there is static output.
    await expect(buildCard.getByRole("switch", { name: "Single-page application" })).toHaveCount(0)
    await buildCard.getByLabel("Output directory").fill("dist")
    const fallback = buildCard.getByRole("switch", { name: "Single-page application" })
    await expect(fallback).toBeVisible()
    await fallback.click()
    await buildCard.getByRole("button", { name: "Save" }).click()
    await expect(page.getByText("Build settings saved").last()).toBeVisible()
    let saved = writes.at(-1) as { build: Record<string, unknown> }
    expect(saved.build.spaFallback).toBe(true)
    expect(saved.build.outputDirectory).toBe("dist")

    await buildCard.getByLabel("Language").click()
    await page.getByRole("option", { name: "Python", exact: true }).click()
    await expect(buildCard.getByLabel("Package manager")).toHaveCount(0)
    await buildCard.getByLabel("Python version (optional)").fill("3.12")
    await buildCard.getByRole("button", { name: "Save" }).click()
    await expect(page.getByText("Build settings saved").last()).toBeVisible()
    saved = writes.at(-1) as { build: Record<string, unknown> }
    expect(saved.build.recipe).toBe("python")
    expect(saved.build.pythonVersion).toBe("3.12")
    expect(saved.build.packageManager).toBeUndefined()
  })
})
