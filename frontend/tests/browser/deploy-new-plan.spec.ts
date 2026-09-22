import { expect, test } from "@playwright/test"
import { gotoStep, json, mockNewProject, now } from "./deploy-fixture"

/**
 * `/deploy/new` — the plan a setup is building, and the four decisions that
 * used to be reachable only after the project existed: what an image is, what
 * else it answers to, whether pushes deploy themselves, and which unfinished
 * setups are still wanted.
 */

test("the plan drawing reads the setup back and opens the field that decides each step", async ({
  page,
}) => {
  await mockNewProject(page)
  await page.goto("/deploy/new")
  await page.getByRole("button", { name: "Import Wayy01/wesmokefish" }).click()
  // The drawing is beside every one of the four configure screens, so it reads
  // the setup back wherever the reader is standing in the sequence.
  await gotoStep(page, "project")
  await expect(page.getByRole("textbox", { name: "Project name" })).toBeVisible()

  const plan = page.getByRole("list", { name: "What this setup will create" })
  await expect(plan.getByText("Wayy01/wesmokefish", { exact: true })).toBeVisible()
  await expect(plan.getByText("Automatic recipe · Next.js")).toBeVisible()
  await expect(plan.getByText("Port 3000 · candidate first")).toBeVisible()

  // An unbounded container is the thing that takes the whole server down with
  // it, so the plan says so rather than leaving it inside Advanced to find.
  await expect(plan.getByText("No memory or CPU limit")).toBeVisible()

  // Pressing a step goes to the screen that decides it and opens the fold the
  // node was reading from — the limits are one press away, not a press, a
  // wall of twenty-five fields and a hunt.
  await expect(page.getByRole("spinbutton", { name: "Memory limit (MB)" })).toHaveCount(0)
  await plan.getByRole("button", { name: "Change the runtime settings" }).click()
  await expect(page.getByRole("spinbutton", { name: "Memory limit (MB)" })).toBeVisible()

  await page.getByRole("spinbutton", { name: "Memory limit (MB)" }).fill("512")
  await expect(plan.getByText("512 MB")).toBeVisible()
})

test("an image can be called a web application, which earns it a health gate and no downtime", async ({
  page,
}) => {
  const journey = await mockNewProject(page)
  await page.route("**/api/v1/docker/images", (route) => json(route, []))
  await page.goto("/deploy/new?source=image")
  await page
    .getByRole("textbox", { name: "Image reference", exact: true })
    .fill("ghcr.io/acme/app:1")
  await page.getByRole("button", { name: "Continue", exact: true }).click()

  // The port the image exposes answers the only question an image is asked, so
  // it opens on Review with the plan read back — the two-press import, for an
  // image. Calling it a web application is a correction, which is what the
  // earlier screens are for.
  await expect(page.getByRole("heading", { level: 1, name: "Ready to deploy?" })).toBeVisible()

  const plan = page.getByRole("list", { name: "What this setup will create" })
  await expect(plan.getByText("Port 8080 · stop first")).toBeVisible()

  await gotoStep(page, "project")
  await page.getByRole("combobox", { name: "Project type" }).click()
  await page.getByRole("option", { name: "Web application" }).click()

  // The port comes from the image's own configuration rather than zero, and it
  // is the runtime screen's first field rather than the last line of a fold.
  await expect(plan.getByText("Port 8080 · candidate first")).toBeVisible()
  await page.getByRole("button", { name: "Change the runtime settings" }).click()
  await expect(page.getByRole("spinbutton", { name: "Port the app listens on" })).toHaveValue(
    "8080",
  )

  // Preflight only allows candidate-first activation, and only requires a
  // readiness gate, for a web profile — so saying so has to change both.
  await expect(page.getByRole("combobox", { name: "Release strategy" })).toContainText(
    "Candidate first",
  )
  await page.getByRole("button", { name: /Health check/ }).click()
  await expect(page.locator("#readiness-name")).toBeVisible()

  await gotoStep(page, "review")
  await page.getByRole("button", { name: "Save only", exact: true }).click()
  await expect.poll(() => journey.commits()).toBe(1)
  const saved = journey.configuration() as {
    runtime: { strategy: string; internalPort: number }
    checks: { phase: string; kind: string }[]
  }
  expect(saved.runtime.strategy).toBe("blue_green")
  expect(saved.runtime.internalPort).toBe(8080)
  expect(saved.checks.some((check) => check.phase === "readiness" && check.kind === "http")).toBe(
    true,
  )
})

test("a second hostname can be added before the first deploy", async ({ page }) => {
  const journey = await mockNewProject(page)
  await page.goto("/deploy/new")
  await page.getByRole("button", { name: "Import Wayy01/wesmokefish" }).click()
  await gotoStep(page, "runtime")

  await page.getByRole("textbox", { name: "Hostname", exact: true }).fill("wesmokefish.test")
  await page.getByRole("button", { name: "Add another hostname" }).click()
  await page.getByRole("textbox", { name: "Also answers to 2" }).fill("www.wesmokefish.test")

  const plan = page.getByRole("list", { name: "What this setup will create" })
  await expect(plan.getByText("HTTPS · +1 more")).toBeVisible()

  await gotoStep(page, "review")
  await page.getByRole("button", { name: "Save only", exact: true }).click()
  await expect.poll(() => journey.commits()).toBe(1)
  const saved = journey.configuration() as { domains: { hostname: string }[] }
  expect(saved.domains.map((domain) => domain.hostname)).toEqual([
    "wesmokefish.test",
    "www.wesmokefish.test",
  ])
})

test("automatic deployment is a decision at creation, not a setting to find afterwards", async ({
  page,
}) => {
  const journey = await mockNewProject(page)
  await page.goto("/deploy/new")
  await page.getByRole("button", { name: "Import Wayy01/wesmokefish" }).click()
  // What happens on the *next* push is part of the last look before the
  // project is made, so it is on the Review screen the flow opens on.
  await expect(page.getByRole("heading", { level: 1, name: "Ready to deploy?" })).toBeVisible()

  const automatic = page.getByRole("switch", { name: "Deploy new commits automatically" })
  await expect(automatic).toBeChecked()

  // Typed rather than filled: a field that re-rendered itself from the parsed
  // lines swallowed the newline, so the second pattern could never be reached.
  const paths = page.getByRole("textbox", { name: "Only when these paths change" })
  await paths.click()
  await paths.pressSequentially("apps/web/**\npackages/ui/**")
  await expect(paths).toHaveValue("apps/web/**\npackages/ui/**")
  await automatic.click()

  await page.getByRole("button", { name: "Save only", exact: true }).click()
  await expect.poll(() => journey.commits()).toBe(1)
  expect(journey.commitBody()).toMatchObject({
    gitPolicy: {
      automatic: false,
      watchInclude: ["apps/web/**", "packages/ui/**"],
      commitStatuses: true,
    },
  })
})

test("an unfinished setup can be discarded, and changing source discards the one abandoned", async ({
  page,
}) => {
  const journey = await mockNewProject(page)
  let listed = [
    {
      id: "abandoned-1",
      name: "lampino",
      source: "git · https://github.com/Wayy01/lampino.git",
      currentStep: "detection",
      updatedAt: now,
      expiresAt: "2026-10-02T09:00:00Z",
    },
  ]
  await page.route("**/api/v1/deploy/drafts", (route) =>
    route.request().method() === "GET" ? json(route, listed) : route.fallback(),
  )
  await page.goto("/deploy/new")

  const list = page.getByRole("list", { name: "Unfinished setups" })
  await expect(list.getByRole("link", { name: /lampino/ })).toBeVisible()
  listed = []
  await list.getByRole("button", { name: "Discard lampino" }).click()
  await expect.poll(() => journey.discarded()).toContain("abandoned-1")
  await expect(page.getByRole("heading", { name: "Unfinished setups" })).toHaveCount(0)

  // Every press of Import creates a draft, so walking away from one and
  // picking a different source has to throw the first away rather than leave
  // it to expire — three tries at one repository were three rows here.
  await page.getByRole("button", { name: "Import Wayy01/wesmokefish" }).click()
  // The way out of the sequence is the way back from its first screen: there
  // is no step behind that one, so "Back" is "Change source".
  await gotoStep(page, "project")
  await page.getByRole("button", { name: "Change source" }).click()
  await expect(page.getByRole("heading", { name: "Import Git repository" })).toBeVisible()
  await expect.poll(() => journey.discarded()).toContain("journey-draft")
})

test("a finding whose remedy is inside Advanced opens it instead of pointing at nothing", async ({
  page,
}) => {
  await mockNewProject(page)
  await page.route("**/api/v1/deploy/drafts/journey-draft/preflight", (route) => {
    const findings = [
      {
        code: "package_manager_ambiguous",
        severity: "blocked",
        title: "More than one lockfile",
        means: "Two package managers claim this repository.",
        action: "Choose the package manager.",
        owner: "deploy",
        fieldId: "configuration.build.packageManager",
      },
      {
        code: "backup_policy_missing",
        severity: "warning",
        title: "Persistent storage has no linked backup policy",
        measured: "1 mount(s)",
        action: "Link a backup job or explicitly accept this risk.",
        owner: "backups",
        fieldId: "runtime.mounts",
        deepLink: "/backups",
      },
    ]
    return json(route, {
      draft: {
        id: "journey-draft",
        ownerUsername: "operator",
        currentStep: "preflight",
        revision: 9,
        data: {},
        findings,
        planPreview: "source -> build",
        updatedAt: now,
        expiresAt: "2026-10-02T09:00:00Z",
      },
      preflight: {
        revision: 9,
        findings,
        preview: "source -> build",
        digest: `sha256:${"b".repeat(64)}`,
        plan: { actions: [] },
      },
    })
  })
  await page.goto("/deploy/new")
  await page.getByRole("button", { name: "Import Wayy01/wesmokefish" }).click()
  // Preflight is the Review screen's own step now, so the findings arrive on
  // the screen that asked whether the plan was right.
  await expect(page.getByRole("heading", { level: 1, name: "Ready to deploy?" })).toBeVisible()
  await page.getByRole("button", { name: "Deploy", exact: true }).click()

  await expect(page.getByText("More than one lockfile")).toBeVisible()

  // A finding whose owner is another feature offers that page rather than a
  // control this screen does not have.
  const backup = page.getByText("Persistent storage has no linked backup policy")
  await expect(backup).toBeVisible()
  await expect(page.getByRole("link", { name: "Open owning page" })).toHaveAttribute(
    "href",
    "/backups",
  )

  // "Open it" is a step and a fold: the build settings are folded away on the
  // Project screen whenever detection answered them, and the link used to be
  // drawn for every finding and answered for none but a variable's.
  await expect(page.getByRole("combobox", { name: "Package manager" })).toHaveCount(0)
  await page.getByRole("button", { name: "Open it" }).click()
  await expect(
    page.getByRole("heading", { level: 1, name: "What are you building?" }),
  ).toBeVisible()
  await expect(page.getByRole("combobox", { name: "Package manager" })).toBeVisible()
})
