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

// The pending-changes strip also names a changed variable in one of its own
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
    // The facts the old Project card repeated sit beside what sets them: the
    // checkout and the repository on the Source head, which links to the
    // repository's own page.
    const source = page.getByRole("form", { name: "Source" })
    await expect(source.getByText("/srv/api-production")).toBeVisible()
    await expect(source.getByRole("link", { name: "acme/api" })).toHaveAttribute(
      "href",
      "https://github.com/acme/api",
    )
    await expect(page.getByRole("heading", { name: "Name", exact: true })).toBeVisible()

    await page.setViewportSize({ width: 1280, height: 900 })
    await page.screenshot({ path: test.info().outputPath("general-1280.png"), fullPage: true })

    const nameCard = page.getByRole("form", { name: "Project name" })
    await nameCard.getByLabel("Project name").fill("api-production-2")
    // The limit is counted inside the field, and the head says there is an
    // edit to save before Save is pressed.
    await expect(nameCard.getByText("16/64", { exact: true })).toBeVisible()
    await expect(nameCard.getByText("Unsaved changes", { exact: true })).toBeVisible()
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
    const nameCard = page.getByRole("form", { name: "Project name" })
    await nameCard.getByLabel("Project name").fill("payments-api")
    await nameCard.getByRole("button", { name: "Save" }).click()
    await expect(page.getByText("That name is already used by another project")).toBeVisible()
  })

  test("saves a manual-only deployment policy with path filters and commit statuses off", async ({
    page,
  }) => {
    const fixture = await mockProject(page)
    await page.goto("/deploy/7/settings/general")

    const gitCard = page.getByRole("form", { name: "Automatic deployment" })
    // The watch is drawn — the repository, the branch check, the project —
    // and the picture says what the policy does, so the switch needs no
    // sentence repeating it.
    const picture = gitCard.getByRole("list", { name: "How a push reaches a deployment" })
    await expect(picture).toBeVisible()
    await expect(picture.getByText("Every 5 s", { exact: true })).toBeVisible()
    await expect(picture.getByText("each matching commit", { exact: true })).toBeVisible()
    await expect(gitCard.getByText(/deploy automatically\./)).toHaveCount(0)

    await gitCard.getByLabel("Include paths").fill("services/api/**")
    await gitCard.getByLabel("Exclude paths").fill("docs/**")
    await gitCard.getByRole("switch", { name: /GitHub commit status/ }).click()
    await gitCard.getByRole("switch", { name: /^Deploy automatically/ }).click()
    // The path filters only govern automatic deployments, so they fold away
    // under the switch that turns those off — and are still saved.
    await expect(gitCard.getByLabel("Include paths")).toHaveCount(0)
    await gitCard.getByRole("button", { name: "Save" }).click()

    await expect(page.getByText("Deployment policy saved")).toBeVisible()
    await expect(picture.getByText("only when you press Deploy", { exact: true })).toBeVisible()
    await expect(gitCard.getByText("Manual", { exact: true })).toBeVisible()
    // What was saved is what the form holds: nothing left over to save.
    await expect(gitCard.getByText("Unsaved changes", { exact: true })).toHaveCount(0)

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
    await expect(
      page.getByRole("heading", { name: "Automatic deployment", exact: true }),
    ).toBeVisible()
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
    const buildCard = page.getByRole("form", { name: "Build" })

    // Every recipe the backend builds is offered — the select this replaced
    // had three of eight — and the saved one is the lit card.
    const builder = buildCard.getByRole("group", { name: "Builder" })
    await expect(builder.getByRole("button")).toHaveCount(10)
    await expect(builder.getByRole("button", { name: /^Node\.js/ })).toHaveAttribute(
      "aria-pressed",
      "true",
    )
    // The live release's own Build step: how long it took.
    await expect(page.getByText("1m 12s", { exact: true })).toBeVisible()

    await buildCard.getByLabel("Build command").fill("bun run build")
    await buildCard.getByLabel("Start command").fill("bun run start")
    await buildCard
      .getByRole("group", { name: "Package manager" })
      .getByRole("button", { name: /^npm/ })
      .click()
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
    const tasksCard = page.getByRole("form", { name: "Release tasks" })
    await expect(tasksCard.getByText("No release tasks configured.")).toBeVisible()
    // Where they run is drawn on the release path before any exists.
    await expect(
      tasksCard.getByRole("img", {
        name: /run in the Release stage, after Build and before Start/,
      }),
    ).toBeVisible()

    await tasksCard.getByRole("button", { name: "Add release task" }).click()
    // A new task takes the keyboard at its name, and every field is labelled.
    await expect(tasksCard.getByLabel("Release task 1 name")).toBeFocused()
    await expect(tasksCard.getByRole("group", { name: /^Task 1/ })).toContainText("seconds")
    await tasksCard.getByLabel("Release task 1 name").fill("Migrate database")
    await tasksCard.getByLabel("Release task 1 command").fill("./bin/migrate")
    await tasksCard.getByRole("button", { name: "Save" }).click()
    await expect(page.getByText("Release tasks saved")).toBeVisible()

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
    await expect(
      page.getByRole("group", { name: "Builder" }).getByRole("button").first(),
    ).toBeDisabled()
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
    const runtimeCard = page.getByRole("form", { name: "Runtime" })
    // The fixture's writable /data mount is what the executor refuses
    // blue/green over: the reading and the Releases head say so before the
    // next deployment finds out, and the choice cannot be taken again.
    await expect(
      page.getByText("/data is writable — two releases cannot share it", { exact: true }),
    ).toBeVisible()
    await expect(runtimeCard.getByText("Will fail on the next deployment")).toBeVisible()
    await expect(runtimeCard.getByRole("button", { name: "Release blue / green" })).toHaveCount(0)

    await runtimeCard.getByLabel("Memory limit").fill("512")
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
    const checksCard = page.getByRole("form", { name: "Health checks" })
    await expect(checksCard.getByText("No health checks configured.")).toBeVisible()
    // A web project with nothing checking it moves traffic unverified.
    const unverified = checksCard.getByText(/^No readiness check/)
    await expect(unverified).toBeVisible()

    await checksCard.getByRole("button", { name: "Add check" }).click()
    await checksCard.getByLabel("Health check 1 name").fill("HTTP readiness")
    await expect(checksCard.getByRole("radio", { name: "HTTP" })).toHaveAttribute(
      "aria-checked",
      "true",
    )
    await expect(checksCard.getByText("GET / → 2xx · 20 × 3 s").first()).toBeVisible()
    await expect(unverified).toHaveCount(0)
    await checksCard.getByRole("button", { name: "Save" }).click()
    await expect(page.getByText("Health checks saved")).toBeVisible()

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

test.describe("Runtime settings, drafts and refusals", () => {
  test("a switch turned on and off again leaves nothing to save, and a cleared host port keeps its field", async ({
    page,
  }) => {
    const writes: Array<Record<string, unknown>> = []
    await mockProject(page)
    await page.route("**/api/v1/deploy/7/environments/12/configuration", async (route) => {
      if (route.request().method() === "PUT")
        writes.push(route.request().postDataJSON() as Record<string, unknown>)
      await route.fallback()
    })
    await page.goto("/deploy/7/settings/runtime")
    const runtimeCard = page.getByRole("form", { name: "Runtime" })

    // Why blue/green fails is said beside the choice, at full ink.
    await expect(
      runtimeCard.getByText(
        "Blue / green is unavailable: /data is writable — two releases cannot share it.",
      ),
    ).toBeVisible()

    // The server leaves `false` out, so off again is what is saved.
    const privileged = runtimeCard.getByRole("switch", { name: "Privileged container" })
    await privileged.click()
    await expect(runtimeCard.getByText("Unsaved changes", { exact: true })).toBeVisible()
    await privileged.click()
    await expect(runtimeCard.getByText("Unsaved changes", { exact: true })).toHaveCount(0)

    // Clearing the port to type another keeps the switch on and the field
    // where it is, and a save with no port says so beside it.
    await runtimeCard.getByRole("switch", { name: /fixed host port/ }).click()
    const hostPort = runtimeCard.getByRole("spinbutton", { name: "Host port" })
    await expect(hostPort).toHaveValue("3000")
    await hostPort.fill("")
    await expect(hostPort).toBeVisible()
    await runtimeCard.getByRole("button", { name: "Save", exact: true }).click()
    await expect(runtimeCard.getByText("Use a port from 1 to 65535.")).toBeVisible()
    expect(writes).toHaveLength(0)
    await hostPort.fill("8080")
    await expect(hostPort).toHaveValue("8080")
    await runtimeCard.getByRole("switch", { name: /fixed host port/ }).click()
    await expect(hostPort).toHaveCount(0)
    await expect(runtimeCard.getByText("Unsaved changes", { exact: true })).toHaveCount(0)
  })

  test("a plan refusal that names no field is the form's own sentence, not a toast", async ({
    page,
  }) => {
    await mockProject(page)
    await page.route("**/api/v1/deploy/7/environments/12/configuration", async (route) => {
      if (route.request().method() !== "PUT") return route.fallback()
      await route.fulfill({
        status: 422,
        contentType: "application/json",
        body: JSON.stringify({
          error: {
            code: "invalid_plan",
            message: "runtime command passes credential material through argv",
          },
        }),
      })
    })
    await page.goto("/deploy/7/settings/runtime")
    const runtimeCard = page.getByRole("form", { name: "Runtime" })
    await runtimeCard.getByLabel("Memory limit").fill("512")
    await runtimeCard.getByRole("button", { name: "Save", exact: true }).click()

    await expect(runtimeCard.getByRole("alert")).toHaveText(
      "runtime command passes credential material through argv",
    )
    await expect(runtimeCard.getByText("Not saved", { exact: true })).toBeVisible()
    await expect(page.getByText("Could not save runtime settings")).toHaveCount(0)
  })

  test("a command check takes one argument per line as it is typed", async ({ page }) => {
    const writes: Array<Record<string, unknown>> = []
    await mockProject(page)
    await page.route("**/api/v1/deploy/7/environments/12/configuration", async (route) => {
      if (route.request().method() === "PUT")
        writes.push(route.request().postDataJSON() as Record<string, unknown>)
      await route.fallback()
    })
    await page.goto("/deploy/7/settings/runtime")
    const checksCard = page.getByRole("form", { name: "Health checks" })
    await checksCard.getByRole("button", { name: "Add check" }).click()
    await checksCard.getByRole("radio", { name: "Command" }).click()

    const argv = checksCard.getByLabel("Command argv")
    await argv.click()
    await page.keyboard.type("pg_isready")
    await page.keyboard.press("Enter")
    await page.keyboard.type("-U ")
    await page.keyboard.press("Enter")
    await page.keyboard.press("Enter")
    await page.keyboard.type("postgres")
    await expect(argv).toHaveValue("pg_isready\n-U \n\npostgres")
    await expect(checksCard.getByText("$ pg_isready -U postgres")).toBeVisible()

    await checksCard.getByRole("button", { name: "Save", exact: true }).click()
    await expect(page.getByText("Health checks saved")).toBeVisible()
    const saved = writes.at(-1) as { checks: Array<{ config: Record<string, unknown> }> }
    expect(saved.checks[0].config.command).toEqual(["pg_isready", "-U", "postgres"])
    await expect(argv).toHaveValue("pg_isready\n-U\npostgres")
    await expect(checksCard.getByText("Unsaved changes", { exact: true })).toHaveCount(0)
  })
})

test.describe("Build settings for a Compose stack", () => {
  test("a repository built as Compose keeps its root, platform and cache, and offers no builder", async ({
    page,
  }) => {
    await mockProject(page)
    await page.goto("/deploy/7/settings/build")
    // Saved through the fixture's own endpoint, so the page reads it back as
    // the server would hand it over.
    await page.evaluate(async () => {
      const url = "/api/v1/deploy/7/environments/12/configuration"
      const current = await (await fetch(url)).json()
      await fetch(url, {
        method: "PUT",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ ...current, build: { method: "compose", secrets: [] } }),
      })
    })
    await page.reload()

    const buildCard = page.getByRole("form", { name: "Build" })
    await expect(buildCard.getByText("Compose", { exact: true })).toBeVisible()
    await expect(buildCard.getByRole("group", { name: "Builder" })).toHaveCount(0)
    await expect(buildCard.getByRole("textbox", { name: "Root directory" })).toBeVisible()
    await expect(buildCard.getByRole("textbox", { name: "Target platform" })).toBeVisible()
    await expect(buildCard.getByRole("switch", { name: "Force a clean build" })).toBeVisible()
    await expect(buildCard.getByLabel("Build command")).toHaveCount(0)
  })
})

test.describe("Environment variables", () => {
  test("adds a variable and shows it pending beside the existing secret", async ({ page }) => {
    await mockProject(page)
    await page.goto("/deploy/7/settings/variables")

    await expect(page.getByRole("heading", { name: "Environment variables" })).toBeVisible()
    await expect(variableRow(page, "API_TOKEN")).toBeVisible()
    await expect(variableRow(page, "API_TOKEN").getByText("Pending")).toBeVisible()
    // Who can read it is said by name, whatever the three slots look like.
    await expect(variableRow(page, "API_TOKEN").getByText("Reaches runtime")).toBeAttached()
    // Every count is a chip over the list, and the chip narrows the list to it.
    await expect(page.getByRole("button", { name: /^Pending/ })).toBeVisible()

    // The list comes first; adding one is a sheet from the section's head, and
    // while it is open the sheet's own command is the only "Add variable".
    await page.getByRole("button", { name: "Add variable" }).click()
    const editor = page.getByRole("dialog", { name: "Add variable" })
    await editor.getByLabel("Variable name").fill("database_url")
    await editor.getByLabel("Value", { exact: true }).fill("postgres://example")
    await page.getByRole("button", { name: "Add variable" }).click()
    await expect(page.getByText("Variable saved")).toBeVisible()
    await expect(editor).toHaveCount(0)
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

    await page.getByRole("button", { name: "Add variable" }).click()
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
    await page.getByRole("button", { name: "Import .env" }).click()
    await page.getByLabel("Dotenv values").fill("FOO=bar\nBAZ=qux")
    // Every name in the paste is listed with what importing it does before
    // anything is written.
    const preview = page.getByRole("list", { name: "What this imports" })
    await expect(preview.getByRole("listitem")).toHaveCount(2)
    await expect(preview.getByRole("listitem").first()).toContainText("FOO")
    await expect(preview.getByText("new", { exact: true })).toHaveCount(2)
    await page.getByRole("button", { name: "Import variables" }).click()
    await expect(page.getByText("Variables imported")).toBeVisible()

    expect(imports.at(-1)).toMatchObject({
      revision: 3,
      dotenv: "FOO=bar\nBAZ=qux",
      sensitivity: "secret",
      scopes: ["runtime"],
    })
  })

  test("a paste the server's dry run refuses as a whole says why, and cannot be imported", async ({
    page,
  }) => {
    await mockProject(page)
    await page.route("**/api/v1/deploy/7/environments/12/variables/import**", (route) =>
      route.fulfill({
        status: 422,
        contentType: "application/json",
        body: JSON.stringify({
          error: { code: "variable_cycle", message: "A references B, which references A" },
        }),
      }),
    )

    await page.goto("/deploy/7/settings/variables")
    await page.getByRole("button", { name: "Import .env" }).click()
    await page.getByLabel("Dotenv values").fill("A=${{variable.B}}\nB=${{variable.A}}")
    await expect(page.getByText("A references B, which references A")).toBeVisible()
    await expect(page.getByRole("button", { name: "Import variables" })).toBeDisabled()
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
    await expect(page.getByRole("button", { name: "Import .env" })).toHaveCount(0)
    // A row a reader cannot edit is still drawn, but offers no editor.
    await expect(page.getByRole("button", { name: "Edit API_TOKEN" })).toHaveCount(0)
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

  test("an unrecoverable row action error shows as a toast, never inside the editor", async ({
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
    // The editor never opened for it, and the row carries no inline refusal.
    await expect(page.locator("form")).toHaveCount(0)
    await expect(
      page
        .getByRole("list", { name: "Environment variables" })
        .getByText("rotation service is down"),
    ).toHaveCount(0)
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
    // The header menu's own verb, so it asks first, as the menu does.
    await page.getByRole("button", { name: "Stop", exact: true }).click()
    await page.getByRole("dialog").getByRole("button", { name: "Stop", exact: true }).click()

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
    // Acts the role cannot take are not drawn, and the empty page says why.
    await expect(
      page.getByText("Your role cannot stop, archive or delete this deployment."),
    ).toBeVisible()

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
    const buildCard = page.getByRole("form", { name: "Build" })
    const builder = buildCard.getByRole("group", { name: "Builder" })

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

    await builder.getByRole("button", { name: /^Python/ }).click()
    await expect(buildCard.getByLabel("Package manager")).toHaveCount(0)
    await buildCard.getByRole("radio", { name: "3.12" }).click()
    await buildCard.getByRole("button", { name: "Save" }).click()
    await expect(page.getByText("Build settings saved").last()).toBeVisible()
    saved = writes.at(-1) as { build: Record<string, unknown> }
    expect(saved.build.recipe).toBe("python")
    expect(saved.build.pythonVersion).toBe("3.12")
    expect(saved.build.packageManager).toBeUndefined()

    // Leaving the recipes for a Dockerfile takes the recipe with it: the
    // server refuses a recipe on any other builder.
    await builder.getByRole("button", { name: /^Dockerfile/ }).click()
    await buildCard.getByLabel("Dockerfile path").fill("deploy/Dockerfile")
    await buildCard.getByRole("button", { name: "Save" }).click()
    await expect.poll(() => writes.length).toBe(3)
    saved = writes.at(-1) as { build: Record<string, unknown> }
    expect(saved.build.method).toBe("dockerfile")
    expect(saved.build.dockerfile).toBe("deploy/Dockerfile")
    expect(saved.build.recipe).toBeUndefined()
    expect(saved.build.pythonVersion).toBeUndefined()
  })
})
