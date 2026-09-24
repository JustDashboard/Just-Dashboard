import { expect, test } from "@playwright/test"
import { blueprintCatalogue, gotoStep, json, mockNewProject, now } from "./deploy-fixture"

/**
 * `/deploy/new` — the source strip and the four configure screens every source
 * lands on. `mockNewProject` wires the draft lifecycle, the GitHub repository
 * list, the blueprint catalogue and the hostname suggestion the way the old
 * `deploy-ui.spec.ts` did for quick deploy and the wizard; each test adds
 * only the endpoints its own source needs.
 */

test("the source strip switches the active source and is the only way in", async ({ page }) => {
  await mockNewProject(page)
  await page.goto("/deploy/new")
  await expect(page.getByRole("heading", { name: "Import Git repository" })).toBeVisible()
  // Five toggles for one answer, so a group of pressed buttons: a tablist
  // has to own tabs, and these were never tabs.
  const strip = page.getByRole("group", { name: "Project source" })
  await expect(strip.getByRole("button")).toHaveCount(5)
  await expect(page.getByRole("tablist")).toHaveCount(0)
  await expect(page.getByRole("button", { name: "Git repository", exact: true })).toHaveAttribute(
    "aria-pressed",
    "true",
  )

  // The Git tab used to repeat all five other sources as a grid of cards
  // directly under the strip that already listed them. One navigation.
  await expect(page.getByRole("button", { name: /ready-to-use defaults/ })).toHaveCount(0)

  await page.getByRole("button", { name: "Template", exact: true }).click()
  await expect(page.getByRole("heading", { name: "Application templates" })).toBeVisible()
  await expect(page.getByRole("button", { name: "Template", exact: true })).toHaveAttribute(
    "aria-pressed",
    "true",
  )

  await page.getByRole("button", { name: "Database", exact: true }).click()
  await expect(page.getByRole("heading", { name: "Start a database" })).toBeVisible()

  await page.getByRole("button", { name: "Docker image", exact: true }).click()
  await expect(page.getByRole("heading", { name: "Choose an image" })).toBeVisible()

  await page.getByRole("button", { name: "Compose", exact: true }).click()
  await expect(page.getByRole("heading", { name: "Compose stack" })).toBeVisible()

  await page.getByRole("button", { name: "Git repository", exact: true }).click()
  await expect(page.getByRole("heading", { name: "Import Git repository" })).toBeVisible()
})

test("every source and every configure step fits the window without the page scrolling", async ({
  page,
}) => {
  // The flow is decided in one view (§17 pass 8): what scrolls is the list or
  // form inside its own surface, never the page around the question.
  await page.setViewportSize({ width: 1280, height: 800 })
  await mockNewProject(page)
  await page.goto("/deploy/new")
  const overflow = () =>
    page.locator("[data-slot='page']").evaluate((element) => {
      const shell = element.parentElement!
      return {
        down: shell.scrollHeight - shell.clientHeight,
        across: shell.scrollWidth - shell.clientWidth,
      }
    })

  for (const source of ["Git repository", "Docker image", "Template", "Database", "Compose"]) {
    await page.getByRole("button", { name: source, exact: true }).click()
    await expect.poll(overflow, { message: source }).toEqual({ down: 0, across: 0 })
  }
  await page.getByRole("button", { name: "Template", exact: true }).click()
  await page.getByRole("button", { name: "Use Vaultwarden", exact: true }).click()
  await expect(page.getByRole("button", { name: "Use this template" })).toBeEnabled()
  await expect.poll(overflow, { message: "a chosen template" }).toEqual({ down: 0, across: 0 })

  await page.getByRole("button", { name: "Git repository", exact: true }).click()
  await page.getByRole("button", { name: "Import Wayy01/wesmokefish" }).click()
  for (const step of ["project", "runtime", "variables", "review"] as const) {
    await gotoStep(page, step)
    await expect.poll(overflow, { message: step }).toEqual({ down: 0, across: 0 })
  }
})

test("the repository list is drawn once, after both GitHub identities have answered", async ({
  page,
}) => {
  // The App answers from the dashboard's own token and the CLI through a `gh`
  // round trip, so the App's rows used to land first under placeholders for
  // the CLI's, and the list redrew itself when the second answer arrived.
  await mockNewProject(page)
  let answer!: () => void
  const cliAnswered = new Promise<void>((resolve) => (answer = resolve))
  await page.route("**/api/v1/deploy/github-app/", (route) =>
    json(route, {
      configured: true,
      installations: [
        { id: 1, account: "Wayy01", accountType: "User", htmlUrl: "", repositorySelection: "all" },
      ],
    }),
  )
  await page.route("**/api/v1/deploy/github-app/repositories", (route) =>
    json(route, [
      {
        installationId: 1,
        account: "Wayy01",
        nameWithOwner: "Wayy01/granted",
        name: "granted",
        private: false,
        defaultBranch: "main",
        cloneUrl: "https://github.com/Wayy01/granted.git",
        htmlUrl: "https://github.com/Wayy01/granted",
        pushedAt: now,
      },
    ]),
  )
  await page.route("**/api/v1/git/github/repos", async (route) => {
    await cliAnswered
    return route.fallback()
  })

  const appListed = page.waitForResponse("**/api/v1/deploy/github-app/repositories")
  await page.goto("/deploy/new")
  await appListed
  const skeletons = page.locator("[data-slot='flow-panel'] [data-slot='skeleton']")
  await expect(skeletons.first()).toBeVisible()
  await expect(page.getByRole("button", { name: "Import Wayy01/granted" })).toHaveCount(0)

  answer()
  await expect(page.getByRole("button", { name: "Import Wayy01/granted" })).toBeVisible()
  await expect(page.getByRole("button", { name: "Import Wayy01/wesmokefish" })).toBeVisible()
  await expect(skeletons).toHaveCount(0)
})

test("one GitHub account reached by both the App and the CLI is drawn once", async ({ page }) => {
  await mockNewProject(page)
  const installation = {
    id: 1,
    // GitHub logins are case-insensitive, and so is the comparison with the
    // CLI's "Wayy01".
    account: "wayy01",
    accountType: "User",
    htmlUrl: "https://github.com/settings/installations/1",
    repositorySelection: "all",
  }
  let installations = [installation]
  await page.route("**/api/v1/deploy/github-app/", (route) =>
    json(route, { configured: true, installations }),
  )
  await page.goto("/deploy/new")

  // The ordinary install: one operator, one account, reached two ways. Two
  // rows with the same face and the same name read as two accounts.
  const identities = page.getByRole("list", { name: "GitHub connections" })
  await expect(identities.getByRole("listitem")).toHaveCount(1)
  await expect(identities.getByText("GitHub App · GitHub CLI", { exact: true })).toBeVisible()
  await expect(identities.getByText("Connected", { exact: true })).toBeVisible()

  // An App on somebody else's account is a second identity, and keeps its row.
  installations = [{ ...installation, account: "acme" }]
  await page.reload()
  await expect(identities.getByRole("listitem")).toHaveCount(2)
  await expect(identities.getByText("acme", { exact: true })).toBeVisible()
  await expect(identities.getByText("Signed in", { exact: true })).toBeVisible()
})

test("with neither GitHub identity connected the panel says so and each identity is drawn as its product", async ({
  page,
}) => {
  await mockNewProject(page)
  await page.route("**/api/v1/git/github/", (route) =>
    json(route, { available: true, account: { loggedIn: false } }),
  )
  await page.goto("/deploy/new")

  // The surface the screen is built around no longer goes blank: it says why
  // there is nothing to pick. The verbs are the identity rows' own, beside the
  // state they change — one "Connect", not a second one a few pixels away.
  await expect(page.getByText("No GitHub account connected", { exact: true })).toBeVisible()
  await expect(page.getByRole("link", { name: "Connect", exact: true })).toHaveAttribute(
    "href",
    "/deploy/credentials",
  )
  await expect(page.getByRole("link", { name: "Connect GitHub" })).toHaveCount(0)

  // GitHub's own tile for the App, a terminal with GitHub in its corner for the
  // CLI — not two grey glyphs.
  const identities = page.getByRole("list", { name: "GitHub connections" })
  const [app, cli] = [
    identities.getByRole("listitem").nth(0),
    identities.getByRole("listitem").nth(1),
  ]
  await expect(app.getByText("Disconnected", { exact: true })).toBeVisible()
  await expect(app.locator("img").first()).toHaveAttribute("src", "/logos/github.svg")
  await expect(cli.getByText("Signed out", { exact: true })).toBeVisible()
  await expect(cli.locator("img").first()).toHaveAttribute("src", "/logos/terminal.svg")
  await expect(cli.locator("img").nth(1)).toHaveAttribute("src", "/logos/github.svg")
})

test("a GitHub App that could not be read is not reported as no account connected", async ({
  page,
}) => {
  await mockNewProject(page)
  await page.route("**/api/v1/git/github/", (route) =>
    json(route, { available: true, account: { loggedIn: false } }),
  )
  await page.route("**/api/v1/deploy/github-app/", (route) =>
    route.fulfill({
      status: 503,
      contentType: "application/json",
      body: JSON.stringify({ error: { code: "unavailable", message: "GitHub is unreachable" } }),
    }),
  )
  await page.goto("/deploy/new")

  // The App may well be installed: the page does not know, so it does not say
  // "no account connected" — it says the list is waiting on the App.
  await expect(
    page.getByText("Nothing can be listed until the GitHub App answers.", { exact: false }),
  ).toBeVisible()
  await expect(page.getByText("No GitHub account connected", { exact: true })).toHaveCount(0)
  // Nor does the App's own row call it disconnected.
  const app = page.getByRole("list", { name: "GitHub connections" }).getByRole("listitem").first()
  await expect(app.getByText("Could not be read just now", { exact: true })).toBeVisible()
  await expect(app.getByText("Unknown", { exact: true })).toBeVisible()
  await expect(app.getByText("Disconnected", { exact: true })).toHaveCount(0)
})

test("a GitHub CLI that could not be read shows the error, not a missing account", async ({
  page,
}) => {
  await mockNewProject(page)
  await page.route("**/api/v1/git/github/", (route) =>
    route.fulfill({
      status: 503,
      contentType: "application/json",
      body: JSON.stringify({ error: { code: "unavailable", message: "gh did not answer" } }),
    }),
  )
  await page.goto("/deploy/new")

  await expect(page.getByRole("alert").filter({ hasText: "gh did not answer" })).toBeVisible()
  await expect(page.getByText("No GitHub account connected", { exact: true })).toHaveCount(0)
  const cli = page.getByRole("list", { name: "GitHub connections" }).getByRole("listitem").last()
  await expect(cli.getByText("Unknown", { exact: true })).toBeVisible()
  await expect(cli.getByText("Signed out", { exact: true })).toHaveCount(0)

  // With nothing to pick the pasted URL is the way forward, and its field
  // names the host as it is typed.
  const url = page.getByRole("textbox", { name: "Clone URL", exact: true })
  const host = page.getByRole("group").filter({ has: url }).locator("img")
  await expect(host).toHaveAttribute("src", "/logos/git.svg")
  await url.fill("https://gitlab.com/acme/infra.git")
  await expect(host).toHaveAttribute("src", "/logos/gitlab.svg")
})

test("unfinished drafts sit behind a counted button beside the question and link back to themselves", async ({
  page,
}) => {
  await mockNewProject(page)
  await page.route("**/api/v1/deploy/drafts", (route) =>
    route.request().method() === "GET"
      ? json(route, [
          {
            id: "abandoned-1",
            name: "storefront-redo",
            source: "Wayy01/storefront @ main",
            currentStep: "configuration",
            updatedAt: "2026-09-02T09:00:00Z",
            expiresAt: "2026-10-02T09:00:00Z",
          },
          {
            id: "abandoned-2",
            currentStep: "source",
            updatedAt: "2026-09-01T09:00:00Z",
            expiresAt: "2026-10-01T09:00:00Z",
          },
        ])
      : route.fallback(),
  )
  await page.goto("/deploy/new")

  // A way back, not a block pushing the sources down a screen that has to fit
  // the window: the button says how many, and the list opens from it.
  await page.getByRole("button", { name: /Unfinished setups\s*2/ }).click()
  await expect(page.getByRole("heading", { name: "Unfinished setups" })).toBeVisible()
  const list = page.getByRole("list", { name: "Unfinished setups" })
  // Opening the list is asking to resume one, so focus lands on the first
  // setup's own link — not on its Discard, with the tooltip open over it.
  await expect(list.getByRole("link", { name: /storefront-redo/ })).toBeFocused()
  await expect(page.getByRole("tooltip")).toHaveCount(0)
  await expect(list.getByRole("link", { name: /storefront-redo/ })).toHaveAttribute(
    "href",
    "/deploy/new?draft=abandoned-1",
  )
  await expect(list.getByText("Wayy01/storefront @ main", { exact: true })).toBeVisible()
  await expect(list.getByText("Configuration", { exact: true })).toBeVisible()
  await expect(list.getByRole("link", { name: "Untitled" })).toHaveAttribute(
    "href",
    "/deploy/new?draft=abandoned-2",
  )
  await expect(list.getByText("No source chosen yet", { exact: true })).toBeVisible()
  await expect(list.getByText("Source", { exact: true })).toBeVisible()

  // The chooser underneath still works — the draft list is a convenience,
  // not something the rest of the page depends on.
  await expect(page.getByRole("heading", { name: "Import Git repository" })).toBeVisible()
})

test("a GitHub repository reaches a running release with a 44px Deploy target and no overflow", async ({
  page,
}, testInfo) => {
  const quick = await mockNewProject(page)
  // On a phone, because the first deploy is as likely to be started from one
  // as from a desktop, and every primary action here is a 44px target.
  await page.setViewportSize({ width: 375, height: 850 })
  await page.goto("/deploy/new")

  await expect(page.getByRole("heading", { name: "Import Git repository" })).toBeVisible()
  await expect(page.getByRole("button", { name: "Import Wayy01/wesmokefish" })).toBeVisible()
  await testInfo.attach("source-git-375", {
    body: await page.screenshot({ fullPage: true }),
    contentType: "image/png",
  })
  await page.getByRole("button", { name: "Import Wayy01/wesmokefish" }).click()

  // Detection answered every question this repository asks, so the sequence
  // opens on its last screen with the plan read back and Deploy under it: the
  // two-press import survives being split into four steps.
  await expect(page.getByRole("heading", { level: 1, name: "Ready to deploy?" })).toBeVisible()

  // A node of the plan drawing is how the reader goes back to one answer —
  // it opens the step that owns it, and the fold inside it.
  await page.getByRole("button", { name: "Change the build settings" }).click()
  await expect(page.getByRole("combobox", { name: "Branch" })).toContainText("main")
  await expect(page.getByRole("textbox", { name: "Build command" })).toHaveValue("bun run build")
  await expect(page.getByRole("textbox", { name: "Start command" })).toHaveValue("bun start")

  await page.getByRole("button", { name: "Change the public address" }).click()
  await expect(page.getByRole("spinbutton", { name: /Port/ })).toHaveValue("3000")
  await expect(page.getByRole("textbox", { name: "Hostname" })).toHaveValue(
    "wesmokefish-a1b2c3.203-0-113-7.sslip.io",
  )
  // Nothing to press: the certificate is the run's own work, before it starts
  // anything, and the screen says so rather than offering a button.
  await expect(page.getByText(/orders the certificate .*before the release starts/)).toBeVisible()
  await expect(page.getByRole("button", { name: /certificate/i })).toHaveCount(0)

  await gotoStep(page, "variables")
  await page.getByText("Paste .env", { exact: true }).click()
  await page
    .getByRole("textbox", { name: "Environment variables" })
    .fill("NEXT_PUBLIC_SITE_URL=https://example.test")
  await gotoStep(page, "review")

  const deployButton = page.getByRole("button", { name: "Deploy", exact: true })
  expect((await deployButton.boundingBox())?.height).toBeGreaterThanOrEqual(44)
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(
    true,
  )
  await testInfo.attach("configure-375", {
    body: await page.screenshot({ fullPage: true }),
    contentType: "image/png",
  })
  await deployButton.click()

  // One press: plan saved, preflight run, environment applied, release started.
  await expect(page).toHaveURL(/\/deploy\/77\/runs\/84$/)
  const savedChecks = quick.configuration()?.checks as Array<{ config: Record<string, unknown> }>
  expect(savedChecks[0].config).not.toHaveProperty("port")
  expect(quick.commits()).toBe(1)
  expect(quick.runs()).toBe(1)
  expect(quick.staged()).toMatchObject({
    dotenv: "NEXT_PUBLIC_SITE_URL=https://example.test",
  })
  expect(quick.configuration()).toMatchObject({
    build: { buildCommand: "bun run build", startCommand: "bun start" },
    runtime: { internalPort: 3000 },
    domains: [
      { hostname: "wesmokefish-a1b2c3.203-0-113-7.sslip.io", https: true, ownership: "managed" },
    ],
    checks: [{ kind: "http", phase: "readiness", required: true }],
  })
})

test("an invalid project name is flagged beside the field and clears once fixed", async ({
  page,
}) => {
  await mockNewProject(page)
  await page.goto("/deploy/new")
  await page.getByRole("button", { name: "Import Wayy01/wesmokefish" }).click()
  await gotoStep(page, "project")
  const name = page.getByRole("textbox", { name: "Project name" })
  await name.fill("invalid name")
  await name.blur()
  await expect(name).toHaveAttribute("aria-invalid", "true")
  await expect(page.getByText("Start with a letter or number.", { exact: false })).toBeVisible()
  await name.fill("valid-name")
  await expect(name).toHaveAttribute("aria-invalid", "false")

  // And it is refused where it was typed rather than four screens later under
  // the button: Continue does not leave a screen whose answer cannot be saved.
  await name.fill("invalid name")
  await page.getByRole("button", { name: "Continue", exact: true }).click()
  await expect(
    page.getByRole("heading", { level: 1, name: "What are you building?" }),
  ).toBeVisible()
  await expect(page.getByText("Use 1–64 letters", { exact: false })).toBeVisible()
})

test("editing the name and pasting environment text keeps the text out of the URL and browser storage", async ({
  page,
}) => {
  const quick = await mockNewProject(page)
  await page.goto("/deploy/new")
  await page.getByRole("button", { name: "Import Wayy01/wesmokefish" }).click()
  await gotoStep(page, "project")
  await page.getByRole("textbox", { name: "Project name" }).fill("edited-site")
  await gotoStep(page, "variables")
  await page.getByText("Paste .env", { exact: true }).click()
  await page
    .getByRole("textbox", { name: "Environment variables", exact: true })
    .fill("API_TOKEN=handoff-secret")
  const references = page.getByRole("button", { name: /Variable references & scopes/ })
  await references.click()
  await expect(references).toHaveAttribute("aria-expanded", "true")
  expect(page.url()).not.toContain("handoff-secret")
  expect(await page.evaluate(() => JSON.stringify([localStorage, sessionStorage]))).not.toContain(
    "handoff-secret",
  )
  await gotoStep(page, "review")
  await page.getByRole("button", { name: "Deploy", exact: true }).click()
  await expect(page).toHaveURL(/\/deploy\/77\/runs\/84$/)
  expect(quick.staged()).toMatchObject({
    dotenv: "API_TOKEN=handoff-secret",
  })
  expect(quick.commits()).toBe(1)
})

for (const stage of ["runs"]) {
  test(`quick creation recovers an existing project after ${stage} fails`, async ({ page }) => {
    const quick = await mockNewProject(page)
    await page.route(`**/api/v1/deploy/77/environments/78/${stage}`, (route) =>
      route.fulfill({
        status: 503,
        contentType: "application/json",
        body: JSON.stringify({
          error: { code: "unavailable", message: "Setup service unavailable" },
        }),
      }),
    )
    await page.goto("/deploy/new")
    await page.getByRole("button", { name: "Import Wayy01/wesmokefish" }).click()
    await gotoStep(page, "variables")
    await page.getByText("Paste .env", { exact: true }).click()
    await page
      .getByRole("textbox", { name: "Environment variables", exact: true })
      .fill("API_TOKEN=keep-this-value")
    await gotoStep(page, "review")
    await page.getByRole("button", { name: "Deploy", exact: true }).click()
    await expect(page.getByRole("heading", { name: "Deployment created" })).toBeVisible()
    await expect(page.getByText("Setup service unavailable", { exact: true })).toBeVisible()
    expect(quick.commits()).toBe(1)
    await expect(page.getByRole("button", { name: "Deploy", exact: true })).toHaveCount(0)
    await expect(page.getByRole("link", { name: "Open deployment", exact: true })).toHaveAttribute(
      "href",
      "/deploy/77/deployments",
    )
  })
}

test("a pasted Git URL with expert settings reaches a reviewed plan", async ({ page }) => {
  const journey = await mockNewProject(page)
  await page.route("**/api/v1/deploy/credentials", async (route) => {
    if (route.request().method() !== "GET") return route.fallback()
    await json(route, [
      {
        id: 12,
        name: "Deploy token",
        kind: "git_bearer",
        target: "git.example.test",
        createdAt: now,
        updatedAt: now,
        usedBy: 1,
      },
    ])
  })
  let selectedSource: unknown
  page.on("request", (request) => {
    if (request.method() !== "PUT" || !request.url().endsWith("/deploy/drafts/journey-draft"))
      return
    const body = request.postDataJSON()
    if (body.step === "source") selectedSource = body.source
  })
  await page.goto("/deploy/new")
  await page
    .getByRole("textbox", { name: "Clone URL", exact: true })
    .fill("https://git.example.test/team/service.git")
  await page.getByText("Branch & authentication", { exact: true }).click()
  await page.getByRole("textbox", { name: "Branch or tag", exact: true }).fill("release/next")
  // The numeric "Saved credential ID" input is gone — a Select fed by
  // GET /deploy/credentials sends the same field under the hood.
  await page.getByRole("combobox", { name: "Credential" }).click()
  await page.getByRole("option", { name: "Deploy token" }).click()
  await page.getByRole("button", { name: "Import", exact: true }).click()
  await expect(page.getByRole("heading", { level: 1, name: "Ready to deploy?" })).toBeVisible()

  // The variable the plan declares comes first: a build secret and a release
  // task may only name one that already carries the matching scope, and the
  // step that refuses them is the step they are typed on.
  await gotoStep(page, "variables")
  const references = page.getByRole("button", { name: /Variable references & scopes/ })
  await references.click()
  await expect(references).toHaveAttribute("aria-expanded", "true")
  await page.getByRole("button", { name: "Add variable reference" }).click()
  await page.getByRole("textbox", { name: "Variable 1 name" }).fill("NPM_TOKEN")
  await page.getByRole("checkbox", { name: "Build", exact: true }).check()
  await page.getByRole("checkbox", { name: "Release Task", exact: true }).check()

  await gotoStep(page, "project")
  const extras = page.getByRole("button", { name: /Build secrets & release tasks/ })
  await extras.click()
  await expect(extras).toHaveAttribute("aria-expanded", "true")
  await page.getByRole("textbox", { name: "Target platform" }).fill("linux/amd64")

  await page.getByRole("button", { name: "Add build secret" }).click()
  await page.getByRole("combobox", { name: "Build secret 1 variable" }).fill("NPM_TOKEN")

  await page.getByRole("button", { name: "Add release task" }).click()
  await page.getByRole("textbox", { name: "Release task 1 name" }).fill("Database migration")
  await page.getByRole("textbox", { name: "Release task 1 working directory" }).fill("app")
  await page.getByRole("textbox", { name: "Release task 1 command" }).fill("./bin/migrate")
  await page.getByRole("checkbox", { name: "NPM_TOKEN", exact: true }).check()

  await gotoStep(page, "review")
  await page.getByRole("button", { name: "Save only", exact: true }).click()
  await expect(page).toHaveURL(/\/deploy\/77$/)
  expect(selectedSource).toMatchObject({
    kind: "git",
    mode: "git_url",
    url: "https://git.example.test/team/service.git",
    ref: "release/next",
    credentialId: 12,
  })
  expect(journey.commits()).toBe(1)
  expect(journey.configuration()).toMatchObject({
    build: {
      targetPlatform: "linux/amd64",
      secrets: [{ variable: "NPM_TOKEN", step: "install" }],
      releaseTasks: [
        {
          name: "Database migration",
          command: "./bin/migrate",
          workingDirectory: "app",
          timeoutSeconds: 300,
          env: ["NPM_TOKEN"],
        },
      ],
    },
  })
})

test("a private image reference sends the chosen registry credential", async ({ page }) => {
  await mockNewProject(page)
  await page.route("**/api/v1/docker/images", (route) => json(route, []))
  await page.route("**/api/v1/deploy/credentials", async (route) => {
    if (route.request().method() !== "GET") return route.fallback()
    await json(route, [
      {
        id: 9,
        name: "Registry login",
        kind: "registry",
        target: "ghcr.io",
        username: "deploy",
        createdAt: now,
        updatedAt: now,
        usedBy: 0,
      },
    ])
  })
  let selectedSource: unknown
  page.on("request", (request) => {
    if (request.method() !== "PUT" || !request.url().endsWith("/deploy/drafts/journey-draft"))
      return
    const body = request.postDataJSON()
    if (body.step === "source") selectedSource = body.source
  })

  await page.goto("/deploy/new?source=image")
  const reference = page.getByRole("textbox", { name: "Image reference", exact: true })
  // The registry is read from the reference as it is typed: Docker Hub's
  // whale for a bare name, GitHub's mark for ghcr.io.
  const registry = page.getByRole("group").filter({ has: reference }).locator("img")
  await expect(registry).toHaveAttribute("src", "/logos/docker.svg")
  await reference.fill("ghcr.io/acme/private:1.0")
  await expect(registry).toHaveAttribute("src", "/logos/github.svg")
  await page.getByRole("combobox", { name: "Credential" }).click()
  await page.getByRole("option", { name: "Registry login" }).click()
  await page.getByRole("button", { name: "Continue", exact: true }).click()
  await expect(page.getByRole("heading", { level: 1, name: "Ready to deploy?" })).toBeVisible()

  expect(selectedSource).toMatchObject({
    kind: "image",
    mode: "image_reference",
    image: "ghcr.io/acme/private:1.0",
    credentialId: 9,
  })
})

test("a Compose stack in a Git repository signs in with a saved credential picked by name", async ({
  page,
}) => {
  await mockNewProject(page)
  await page.route("**/api/v1/deploy/credentials", async (route) => {
    if (route.request().method() !== "GET") return route.fallback()
    await json(route, [
      {
        id: 4,
        name: "GitHub PAT",
        kind: "git_bearer",
        target: "github.com",
        createdAt: now,
        updatedAt: now,
        usedBy: 1,
      },
    ])
  })
  let selectedSource: unknown
  page.on("request", (request) => {
    if (request.method() !== "PUT" || !request.url().endsWith("/deploy/drafts/journey-draft"))
      return
    const body = request.postDataJSON()
    if (body.step === "source") selectedSource = body.source
  })

  await page.goto("/deploy/new?source=compose")
  await page
    .getByRole("group", { name: "Where the files are" })
    .getByRole("button", { name: /In a Git repository/ })
    .click()
  const url = page.getByRole("textbox", { name: "Git URL", exact: true })
  await url.fill("https://github.com/acme/stack.git")
  await expect(page.getByRole("group").filter({ has: url }).locator("img")).toHaveAttribute(
    "src",
    "/logos/github.svg",
  )
  // A credential is chosen by its name, as on the Git tab — this used to be a
  // number field asking for an id nothing on the page showed.
  await page.getByRole("combobox", { name: "Credential" }).click()
  await page.getByRole("option", { name: "GitHub PAT" }).click()
  await page.getByRole("button", { name: "Inspect", exact: true }).click()
  await expect
    .poll(() => selectedSource)
    .toMatchObject({
      kind: "compose",
      mode: "compose_git",
      url: "https://github.com/acme/stack.git",
      credentialId: 4,
    })
})

test("resuming a duplicated draft shows its copied variable needing a value, and Save works", async ({
  page,
}) => {
  // `POST /deploy/{id}/duplicate` hands back a draft already on the
  // configuration step, with no detection yet — this stands in for it rather
  // than driving `mockNewProject`'s own "journey-draft" through the picker.
  await mockNewProject(page)
  let revision = 5
  let configuration: Record<string, unknown> = {
    build: { method: "recipe", recipe: "node" },
    runtime: { internalPort: 3000, hostPort: 0, bindAddress: "127.0.0.1", strategy: "blue_green" },
    variables: [
      { name: "DATABASE_URL", sensitivity: "secret", scopes: ["runtime"], required: true },
    ],
    dependencies: [],
    checks: [],
    domains: [],
  }
  const intent = { name: "api-copy", profile: "web" }
  const source = {
    kind: "git",
    mode: "git_url",
    url: "https://github.com/acme/api.git",
    ref: "main",
  }
  let committed = 0
  const currentDraft = () => ({
    id: "duplicate-draft-1",
    ownerUsername: "operator",
    currentStep: "configuration",
    revision,
    data: { intent, source, configuration },
    findings: [],
    planPreview: "",
    updatedAt: now,
    expiresAt: "2026-10-04T12:00:00Z",
  })

  await page.route("**/api/v1/deploy/drafts/duplicate-draft-1", async (route) => {
    const method = route.request().method()
    if (method === "GET") return json(route, currentDraft())
    if (method === "PUT") {
      const body = route.request().postDataJSON() as { configuration?: Record<string, unknown> }
      revision += 1
      if (body.configuration) configuration = body.configuration
      return json(route, currentDraft())
    }
    return route.fallback()
  })
  await page.route("**/api/v1/deploy/drafts/duplicate-draft-1/preflight", async (route) => {
    if (route.request().method() !== "POST") return route.fallback()
    revision += 1
    const findings = [
      { code: "source.ok", severity: "pass", title: "Source identity resolved", owner: "source" },
    ]
    await json(route, {
      draft: { ...currentDraft(), findings },
      preflight: {
        revision,
        findings,
        preview: "",
        digest: `sha256:${"a".repeat(64)}`,
        plan: { actions: [] },
      },
    })
  })
  await page.route("**/api/v1/deploy/drafts/duplicate-draft-1/commit", async (route) => {
    if (route.request().method() !== "POST") return route.fallback()
    committed += 1
    await json(route, { projectId: 77, environmentId: 78, planRevision: revision, created: true })
  })

  await page.goto("/deploy/new?draft=duplicate-draft-1")
  await gotoStep(page, "project")
  await expect(page.getByRole("textbox", { name: "Project name" })).toHaveValue("api-copy")

  // The copied variable — a name and a scope, no value — is a row in the
  // "Variable references & scopes" fold a blueprint's declared variables use,
  // on the screen that asks what the project needs to run. It is also why the
  // sequence landed on that screen, so the fold holding the answer arrives
  // open rather than as one more press.
  await gotoStep(page, "variables")
  const references = page.getByRole("button", { name: /Variable references & scopes/ })
  await expect(references).toHaveAttribute("aria-expanded", "true")
  const reference = page.getByRole("textbox", { name: "Variable DATABASE_URL reference" })
  await expect(reference).toHaveValue("")
  await reference.fill("${{credential.db}}")

  await gotoStep(page, "review")
  await page.getByRole("button", { name: "Save only", exact: true }).click()
  await expect(page).toHaveURL(/\/deploy\/77$/)
  expect(committed).toBe(1)
})

test("Add database from Configure creates a database, retries after a failed connection, and links DATABASE_URL", async ({
  page,
}) => {
  const quick = await mockNewProject(page)
  let provisioned = 0
  let failAddress = true
  await page.route("**/api/v1/databases/**", (route) => {
    const path = new URL(route.request().url()).pathname.replace("/api/v1", "")
    if (path === "/databases/provision/options")
      return json(route, [
        { engine: "postgres", label: "PostgreSQL", image: "postgres:17", driver: "postgres" },
      ])
    if (path === "/databases/provision") {
      provisioned++
      return json(route, { container: "started-db" })
    }
    if (path === "/databases/adopt")
      return json(route, { id: 42, name: "started-db", driver: "postgres" })
    if (path.endsWith("/ping")) return json(route, { ok: true })
    if (path.endsWith("/url"))
      return failAddress
        ? route.fulfill({
            status: 400,
            contentType: "application/json",
            body: JSON.stringify({
              error: { code: "bad_request", message: "Database needs a shared network" },
            }),
          })
        : json(route, {
            url: "postgres://app:secret@172.17.0.4:5432/app",
            reference: "${{database.42}}",
          })
    return json(route, [])
  })
  await page.goto("/deploy/new")
  await page.getByRole("button", { name: "Import Wayy01/wesmokefish" }).click()
  await gotoStep(page, "variables")
  await page.locator("#env-key-0").fill("API_KEY")
  await page.locator("#env-value-0").fill("another-secret")

  await page.getByRole("button", { name: "Add database" }).click()
  await page.getByText("PostgreSQL", { exact: true }).click()
  await page.getByRole("button", { name: "Create database", exact: true }).click()
  await expect(page.getByText("Database needs a shared network", { exact: true })).toBeVisible()
  await page.keyboard.press("Escape")
  await expect(page.getByText(/Database started-db has started/)).toBeVisible()

  failAddress = false
  await page.getByRole("button", { name: "Add database" }).click()
  await page.getByRole("button", { name: "Retry connection setup", exact: true }).click()
  await page.getByRole("button", { name: "Use this database", exact: true }).click()
  await expect(page.getByRole("dialog")).toHaveCount(0)
  await expect(page.locator("#env-key-1")).toHaveValue("DATABASE_URL")
  await expect(page.locator("#env-value-1")).toHaveValue("${{database.42}}")
  expect(provisioned).toBe(1)

  expect(await page.evaluate(() => JSON.stringify([localStorage, sessionStorage]))).not.toContain(
    "172.17.0.4",
  )
  await gotoStep(page, "review")
  await page.getByRole("button", { name: "Deploy", exact: true }).click()
  await expect(page).toHaveURL(/\/deploy\/77\/runs\/84$/)
  expect(quick.configuration()).toMatchObject({
    dependencies: [
      {
        kind: "database",
        ownership: "linked",
        resourceKind: "database_connection",
        resourceId: "42",
      },
    ],
  })
  expect(quick.staged()?.dotenv).toContain("${{database.42}}")
  expect(quick.staged()?.dotenv).toContain('API_KEY="another-secret"')
})

test("a template that needs its own public URL arrives with one this server can deliver", async ({
  page,
}) => {
  const journey = await mockNewProject(page)
  await page.goto("/deploy/new")
  await page.getByRole("button", { name: "Template", exact: true }).click()
  await page.getByRole("button", { name: "Use Vaultwarden", exact: true }).click()

  // Eight reviewed definitions need their own public URL — n8n writes it into
  // every webhook, Vaultwarden into its WebAuthn origin — and the panel used to
  // send nothing, so choosing one ended in the server's own `"domain" is
  // required` after a draft had already been created. `/deploy/hostname`
  // always knew a name that resolves here with no DNS record to create.
  const domain = page.getByRole("textbox", { name: "Public domain" })
  await expect(domain).toHaveValue("wesmokefish-a1b2c3.203-0-113-7.sslip.io")
  await expect(page.getByText(/Replace it with your own domain if you have one/)).toBeVisible()

  await page.getByRole("button", { name: "Use this template", exact: true }).click()
  // A blueprint's declared variables are review material, so the sequence
  // opens there; the plan is what the last screen reads back.
  await expect(
    page.getByRole("heading", { level: 1, name: "What does it need to run?" }),
  ).toBeVisible()
  await gotoStep(page, "review")
  await expect(page.getByRole("heading", { level: 1, name: "Ready to deploy?" })).toBeVisible()
  expect(journey.source()).toMatchObject({
    blueprintId: "vaultwarden",
    blueprintInputs: { domain: "wesmokefish-a1b2c3.203-0-113-7.sslip.io" },
  })

  // Review used to be one section of four facts under the irreversible
  // button: no finding is drawn until preflight has run, and preflight ran
  // inside the press. It runs on arrival now, and the plan is read back.
  await expect(page.getByRole("heading", { name: "Checked against this server" })).toBeVisible()
  await expect(page.getByText("Runtime plan is valid")).toBeVisible()
  await expect(page.getByRole("heading", { name: "Data it keeps" })).toBeVisible()
  await expect(page.getByText("/data", { exact: true })).toBeVisible()
  await expect(page.getByText(/Everything this vault holds/)).toBeVisible()
  await expect(page.getByRole("heading", { name: "Secrets made on this server" })).toBeVisible()
  await expect(page.getByText(/48 characters, made when this plan is saved/)).toBeVisible()
  await expect(page.getByRole("heading", { name: "Before it takes traffic" })).toBeVisible()
  await expect(page.getByText(/GET \/alive · up to 60s to answer/)).toBeVisible()
  await expect(page.getByRole("heading", { name: "At the cutover" })).toBeVisible()
  await expect(page.getByText(/unreachable for a few seconds/)).toBeVisible()
  // The count is what reaches the container, not the rows somebody typed.
  await expect(page.getByText("3 declared", { exact: true })).toBeVisible()
  // The name the application was told is the route the release publishes —
  // both come from this one input, so the two leave this screen agreeing.
  expect(journey.configuration()).toMatchObject({
    domains: [{ hostname: "wesmokefish-a1b2c3.203-0-113-7.sslip.io", https: true }],
  })
})

test("a template input the page cannot answer is named before the press, not after it", async ({
  page,
}) => {
  const journey = await mockNewProject(page)
  await page.goto("/deploy/new")
  await page.getByRole("button", { name: "Template", exact: true }).click()
  await page.getByRole("button", { name: "Use Vaultwarden", exact: true }).click()
  // Cleared once the suggestion has landed in it. Clearing a field that is
  // still empty changes nothing, and the suggestion then fills it under the
  // test — which is the page working, on a server slow enough to lose the race.
  const domain = page.getByRole("textbox", { name: "Public domain" })
  await expect(domain).toHaveValue(/sslip\.io$/)
  await domain.fill("")

  await page.getByRole("button", { name: "Use this template", exact: true }).click()
  await expect(page.getByText("Public domain: Required.", { exact: true })).toBeVisible()
  await expect(page.getByRole("alert").filter({ hasText: /^Required\.$/ })).toBeVisible()
  // Refused here, so the server was never asked to start a setup that cannot
  // finish: every earlier attempt left a draft in the unfinished-setups list.
  expect(journey.started()).toBe(0)
  await expect(page.getByRole("heading", { name: "Application templates" })).toBeVisible()
})

test("a supported blueprint reaches a reviewed plan with the server's rendered configuration", async ({
  page,
}) => {
  const journey = await mockNewProject(page)
  await page.goto("/deploy/new")
  await page.getByRole("button", { name: "Template", exact: true }).click()
  await page.getByRole("button", { name: "Use PostgreSQL", exact: true }).click()
  await expect(page.getByText(/Reviewed 2026-09-11 by Just Dashboard/)).toBeVisible()
  await page.getByRole("textbox", { name: "Database name" }).fill("shop")
  await page.getByRole("button", { name: "Use this template", exact: true }).click()

  // A blueprint opens on the screen its declared variables are on, and the
  // fold holding them is open: a generated password and a rendered database
  // name are review material, not a power-user setting.
  await expect(
    page.getByRole("heading", { level: 1, name: "What does it need to run?" }),
  ).toBeVisible()
  await expect(page.getByRole("textbox", { name: "Variable POSTGRES_DB value" })).toHaveValue(
    "shop",
  )
  await expect(page.getByRole("textbox", { name: "Variable POSTGRES_PASSWORD value" })).toHaveValue(
    "Generated on save (40 characters)",
  )

  await gotoStep(page, "review")
  await page.getByRole("button", { name: "Save only", exact: true }).click()
  await expect(page).toHaveURL(/\/deploy\/77$/)
  expect(journey.commits()).toBe(1)
  expect(journey.configuration()).toMatchObject({
    runtime: {
      image: "postgres:16-alpine",
      strategy: "stop_first",
      mounts: [{ target: "/var/lib/postgresql/data", ownership: "managed" }],
    },
    variables: expect.arrayContaining([
      expect.objectContaining({ name: "POSTGRES_PASSWORD", generate: 40 }),
      expect.objectContaining({ name: "POSTGRES_DB", value: "shop" }),
    ]),
  })
  expect(JSON.stringify(journey.configuration())).not.toContain("hunter")
})

test("the catalogue says how each template is signed into before it is deployed", async ({
  page,
}) => {
  // The defect this covers: an operator deployed a template, opened its
  // address, and met a login form for an account nobody had created and a
  // password nobody had given them. The reviewed definition now declares how
  // its first sign-in works, and the picker has to spend that on the card —
  // where the template is chosen — and not only after the deploy.
  await mockNewProject(page)
  await page.goto("/deploy/new")
  await page.getByRole("button", { name: "Template", exact: true }).click()

  await expect(
    page.getByRole("group", { name: "Monitoring" }).getByText("you create the first account"),
  ).toBeVisible()
  await expect(
    page.getByRole("group", { name: "Productivity" }).getByText("token generated here"),
  ).toBeVisible()
  await expect(
    page.getByRole("group", { name: "Databases" }).getByText("no sign-in page"),
  ).toBeVisible()

  await page.getByRole("button", { name: "Use Vaultwarden", exact: true }).click()
  await expect(page.getByText("Sign in with the token generated here")).toBeVisible()
  await expect(page.getByText(/invite your own account/)).toBeVisible()
})

test("a topic narrows the catalogue to its shelf and every card carries its product's logo", async ({
  page,
}) => {
  await mockNewProject(page)
  await page.goto("/deploy/new?source=template")
  const topics = page.getByRole("group", { name: "Template topics" })
  await expect(topics.getByRole("button", { name: "All 4" })).toHaveAttribute(
    "aria-pressed",
    "true",
  )

  await topics.getByRole("button", { name: "Game servers 1" }).click()
  await expect(page.getByRole("group", { name: "Game servers" })).toBeVisible()
  await expect(page.getByRole("group", { name: "Monitoring" })).toHaveCount(0)

  // Pressing the chosen shelf again puts every shelf back.
  await topics.getByRole("button", { name: "Game servers 1" }).click()
  await expect(page.getByRole("group", { name: "Monitoring" })).toBeVisible()

  // The logo is the product's own file, served from this origin.
  const kuma = page.getByRole("group", { name: "Monitoring" }).locator("img")
  await expect(kuma).toHaveAttribute("src", "/logos/uptime-kuma.svg")
  await expect
    .poll(() => kuma.evaluate((img: HTMLImageElement) => img.naturalWidth))
    .toBeGreaterThan(0)
})

test("an unavailable blueprint explains its status and offers no way to use it", async ({
  page,
}) => {
  const reason = "Blueprint deployment is unavailable until runtime support is complete."
  await mockNewProject(page)
  await page.route("**/api/v1/deploy/blueprints/", (route) =>
    json(
      route,
      blueprintCatalogue.map((entry) => ({
        ...entry,
        deploymentSupported: false,
        unavailableReason: reason,
      })),
    ),
  )
  await page.goto("/deploy/new")
  await page.getByRole("button", { name: "Template", exact: true }).click()
  await expect(page.getByText(reason).first()).toBeVisible()
  await expect(page.getByText("Preview only").first()).toBeVisible()
  await expect(page.getByRole("button", { name: /^Use /i })).toHaveCount(0)
})

test("a Minecraft blueprint accepts the EULA in the open, offers versions, and previews an existing server", async ({
  page,
}) => {
  const journey = await mockNewProject(page)
  await page.route("**/api/v1/deploy/game/import/preview", async (route) => {
    const path = (route.request().postDataJSON() as { path: string }).path
    if (path !== "/srv/minecraft") {
      return route.fulfill({
        status: 400,
        contentType: "application/json",
        body: JSON.stringify({
          error: {
            code: "invalid_import",
            message: "no Minecraft server was found in that directory",
          },
        }),
      })
    }
    return json(route, {
      root: "/srv/minecraft",
      edition: "java",
      software: "paper",
      version: "1.21.1",
      evidence: [{ path: "paper-1.21.1-42.jar", reason: "the jar name identifies a paper server" }],
      worldPaths: ["world", "world_nether"],
      modPaths: ["plugins"],
      configPaths: ["server.properties", "eula.txt"],
      logPaths: ["logs"],
      ignoredPaths: ["logs — regenerated by the server on every start"],
      properties: { motd: "Imported server", "max-players": "40", difficulty: "hard" },
      port: 25570,
      totalBytes: 1048576,
      eulaAccepted: true,
      warnings: ["More than one world directory was found."],
    })
  })

  await page.goto("/deploy/new")
  await page.getByRole("button", { name: "Template", exact: true }).click()
  // The reviewed catalogue is shelved by what a template is for, unfiltered: a
  // game blueprint sits in "Game servers" beside "Monitoring" and
  // "Databases", not behind a prior "what are you deploying" choice.
  await expect(page.getByRole("group", { name: "Monitoring" })).toBeVisible()
  await expect(page.getByRole("group", { name: "Game servers" })).toBeVisible()

  await page.getByRole("button", { name: "Use Minecraft (Java Edition)", exact: true }).click()
  await expect(page.getByText("itzg/minecraft-server:2026.9.1-java21")).toBeVisible()
  await expect(page.getByText(/Reviewed 2026-09-11 by Just Dashboard/)).toBeVisible()
  await expect(page.getByText("2 releases read from the upstream manifest.")).toBeVisible()

  // The EULA is accepted against its own linked agreement, in the open.
  const eula = page.getByRole("checkbox", { name: /I accept the Minecraft EULA/ })
  await expect(eula).not.toBeChecked()
  await expect(page.getByRole("link", { name: /Read the agreement/ })).toHaveAttribute(
    "href",
    "https://aka.ms/MinecraftEULA",
  )
  await eula.check()

  // Advanced blueprint settings stay folded away until asked for.
  const advancedToggle = page.getByRole("button", { name: /advanced blueprint settings/i })
  await expect(advancedToggle).toHaveAttribute("aria-expanded", "false")
  await expect(page.getByText("Verify accounts with Mojang")).toHaveCount(0)
  await advancedToggle.click()
  // A yes-or-no input is an option whose label is the sentence, not a switch
  // with "On" written beside it.
  await expect(page.getByRole("switch", { name: "Verify accounts with Mojang" })).toBeChecked()
  await advancedToggle.click()

  await page.getByRole("button", { name: "I already have a server on this machine" }).click()
  const pathField = page.getByRole("textbox", { name: "Server directory" })
  await pathField.fill("/srv/not-a-server")
  await page.getByRole("button", { name: "Inspect", exact: true }).click()
  await expect(page.getByText("That directory was not usable")).toBeVisible()

  await pathField.fill("/srv/minecraft")
  await page.getByRole("button", { name: "Inspect", exact: true }).click()
  await expect(page.getByText("paper", { exact: true })).toBeVisible()
  await expect(page.getByText("port 25570", { exact: true })).toBeVisible()
  await expect(page.getByText("EULA accepted", { exact: true })).toBeVisible()
  await expect(page.getByRole("combobox", { name: "Minecraft version" })).toContainText("1.21.1")
  await expect(page.getByRole("spinbutton", { name: "Maximum players" })).toHaveValue("40")

  await page.getByRole("button", { name: "Use this template", exact: true }).click()
  // The same rule that opened Postgres's variables opens these; the game
  // default port is what the operator reviews, unedited, one screen back.
  await expect(
    page.getByRole("heading", { level: 1, name: "What does it need to run?" }),
  ).toBeVisible()
  await gotoStep(page, "runtime")
  await expect(page.getByRole("spinbutton", { name: "Port the app listens on" })).toHaveValue(
    "25565",
  )

  await gotoStep(page, "review")
  await page.getByRole("button", { name: "Save only", exact: true }).click()
  await expect(page).toHaveURL(/\/deploy\/77$/)
  expect(journey.commits()).toBe(1)
})

test("pasting a Compose file surfaces its services, unsupported items and the effective plan", async ({
  page,
}) => {
  await mockNewProject(page)
  await page.route("**/api/v1/deploy/drafts/journey-draft/detect", (route) =>
    json(route, {
      id: "journey-draft",
      ownerUsername: "operator",
      currentStep: "detection",
      revision: 4,
      data: {
        intent: { name: "compose", profile: "compose" },
        source: {
          kind: "compose",
          mode: "compose_paste",
          composeFiles: [
            {
              path: "compose.yml",
              content: "services:\n  web:\n    image: nginx:alpine\n",
              order: 0,
            },
          ],
        },
        detection: {
          source: { kind: "compose", composeFiles: ["compose.yml"], services: ["web", "worker"] },
          candidates: [
            {
              id: "compose-candidate",
              name: "Compose stack (2 services)",
              root: "",
              profile: "compose",
              buildMethod: "compose",
              confidence: "high",
              evidence: [],
              needsDecision: [],
            },
          ],
          compose: {
            digest: "sha256:aa",
            files: ["compose.yml"],
            services: [
              { name: "web", image: "nginx:alpine", ports: [], mounts: [], advanced: [] },
              { name: "worker", buildContext: ".", ports: [], mounts: [], advanced: [] },
            ],
            variables: ["API_KEY"],
            warnings: [
              "worker builds from a local Dockerfile; rebuilds are not tracked automatically.",
            ],
            unsupported: ["network mode host is not supported"],
            preview: "services:\n  web:\n    image: nginx:alpine\n  worker:\n    build: .\n",
          },
          selectedId: "compose-candidate",
          scannedFiles: 1,
          scannedBytes: 64,
          truncated: false,
          gitRequirements: { submodules: false, lfs: false },
        },
      },
      findings: [],
      planPreview: "",
      updatedAt: now,
      expiresAt: "2026-09-04T12:00:00Z",
    }),
  )
  await page.goto("/deploy/new")
  await page.getByRole("button", { name: "Compose", exact: true }).click()
  await page.getByLabel("compose.yml content").fill("services:\n  web:\n    image: nginx:alpine\n")
  await page.getByRole("button", { name: "Inspect", exact: true }).click()

  await gotoStep(page, "project")
  await expect(page.getByRole("textbox", { name: "Project name" })).toBeVisible()
  const services = page.locator('[data-slot="tag"]')
  await expect(services.filter({ hasText: /^web$/ })).toBeVisible()
  await expect(services.filter({ hasText: /^worker$/ })).toBeVisible()
  // With more than one service the operator picks the one readiness follows.
  await expect(page.getByRole("combobox", { name: "Primary service" })).toHaveText("web")
  await expect(page.getByText("network mode host is not supported")).toBeVisible()
  await page.getByText("Effective Compose plan", { exact: true }).click()
  await expect(page.getByText("build: .")).toBeVisible()
  // The variable the Compose file references becomes a required runtime
  // variable — reviewable where the project's variables are, without retyping.
  // Preflight refuses a required variable with nothing in it, so it is also
  // what this setup is asked about: the screen is where the sequence lands and
  // the fold holding the row arrives open.
  await gotoStep(page, "variables")
  await expect(page.getByRole("button", { name: /Variable references & scopes/ })).toHaveAttribute(
    "aria-expanded",
    "true",
  )
  const apiKey = page.getByRole("textbox", { name: "Variable API_KEY reference" })
  await expect(apiKey).toBeVisible()
  // Typing the value answers the reason the fold was opened, which must not be
  // the reason it shuts: `open` on a `<details>` is written again every time
  // the prop changes, so a fold computed on each render closes mid-word.
  await apiKey.fill("${{credential.api}}")
  await expect(page.getByRole("button", { name: /Variable references & scopes/ })).toHaveAttribute(
    "aria-expanded",
    "true",
  )
  await expect(apiKey).toBeVisible()
})

test("quick deploy shows the actual HTTPS blocker instead of assuming Certbot is missing", async ({
  page,
}) => {
  await mockNewProject(page)
  const reason = "Port 80 is already used by caddy; configure challenge routing in that web server."
  await page.route("**/api/v1/deploy/hostname**", (route) =>
    json(route, {
      hostname: "wesmokefish-a1b2c3.203-0-113-7.sslip.io",
      covered: false,
      certificateIssue: reason,
      method: "sslip",
      detail: reason,
    }),
  )
  await page.goto("/deploy/new")
  await page.getByRole("button", { name: "Import Wayy01/wesmokefish" }).click()
  await gotoStep(page, "runtime")
  await expect(page.getByText("Automatic HTTPS needs attention")).toBeVisible()
  await expect(page.getByText(reason, { exact: false }).last()).toBeVisible()
  await expect(page.getByText("Install certbot", { exact: false })).toHaveCount(0)
  await expect(page.getByRole("link", { name: "Certificates page" })).toBeVisible()
})

test("quick deploy keeps HTTPS automatic when Docker Caddy owns the public ports", async ({
  page,
}) => {
  await mockNewProject(page)
  await page.route("**/api/v1/deploy/hostname**", (route) =>
    json(route, {
      hostname: "wesmokefish-a1b2c3.203-0-113-7.sslip.io",
      covered: false,
      certificateMethod: "caddy",
      method: "sslip",
      detail: "The public Caddy ingress manages HTTPS automatically.",
    }),
  )
  await page.goto("/deploy/new")
  await page.getByRole("button", { name: "Import Wayy01/wesmokefish" }).click()
  await gotoStep(page, "runtime")
  await expect(
    page.getByText("The managed Caddy ingress orders the certificate", { exact: false }),
  ).toBeVisible()
  await expect(page.getByText("Automatic HTTPS needs attention")).toHaveCount(0)
  await expect(page.getByText("Install certbot", { exact: false })).toHaveCount(0)
})

test("a detected site opens with its variables, its database and its single-page fallback ready", async ({
  page,
}) => {
  const quick = await mockNewProject(page)
  // What the backend's catalogue says about a Vite site whose code reads a
  // PostgreSQL connection: the form should arrive already filled in.
  const candidate = {
    id: "site-candidate",
    name: "Vite site",
    root: "",
    profile: "static",
    buildMethod: "recipe",
    recipe: "node",
    confidence: "high",
    framework: "vite",
    packageManager: "bun",
    buildCommand: "bun run build",
    outputDirectory: "dist",
    port: 80,
    spaFallback: true,
    variables: [
      { name: "DATABASE_URL", sources: [".env.example", "src/db.ts"] },
      { name: "SESSION_SECRET", example: "change me", sources: [".env.example"] },
      { name: "RESEND_API_KEY", sources: ["src/lib/mail.ts"] },
    ],
    databases: [{ engine: "postgres", variable: "DATABASE_URL", evidence: "pg in package.json" }],
    evidence: [{ path: "package.json", reason: "Vite dependency ^6.0.0" }],
    needsDecision: [],
  }
  await page.route("**/api/v1/deploy/drafts/journey-draft/detect", (route) =>
    json(route, {
      id: "journey-draft",
      ownerUsername: "operator",
      currentStep: "detection",
      revision: 4,
      data: {
        intent: { name: "wesmokefish", profile: "web" },
        source: {
          kind: "git",
          mode: "connected_repository",
          provider: "github",
          repository: "Wayy01/wesmokefish",
          ref: "main",
        },
        detection: {
          source: {
            kind: "git",
            remote: "https://github.com/Wayy01/wesmokefish.git",
            ref: "main",
            revision: "a12bc34d56ef7890a12bc34d56ef7890a12bc34d",
          },
          candidates: [candidate],
          selectedId: candidate.id,
          scannedFiles: 12,
          scannedBytes: 4096,
          truncated: false,
          gitRequirements: { submodules: false, lfs: false },
        },
      },
      findings: [],
      planPreview: "",
      updatedAt: now,
      expiresAt: "2026-10-04T12:00:00Z",
    }),
  )
  await page.route("**/api/v1/databases/provision/options", (route) =>
    json(route, [
      {
        engine: "postgres",
        label: "PostgreSQL 16",
        image: "postgres:16-alpine",
        driver: "postgres",
      },
      { engine: "redis", label: "Redis 7", image: "redis:7-alpine", driver: "redis" },
    ]),
  )
  await page.goto("/deploy/new")
  await page.getByRole("button", { name: "Import Wayy01/wesmokefish" }).click()

  // Three detected variables with nothing in them is a question only the
  // operator can answer, so that is the screen the sequence opens on.
  await expect(
    page.getByRole("heading", { level: 1, name: "What does it need to run?" }),
  ).toBeVisible()

  // The framework is named the way its documentation spells it, and the
  // build settings carry the catalogue's serving defaults.
  await gotoStep(page, "project")
  await expect(page.getByText("Vite", { exact: true })).toBeVisible()
  await page.locator("summary").filter({ hasText: "Build & output settings" }).click()
  await expect(page.getByRole("textbox", { name: "Output directory" })).toHaveValue("dist")
  await expect(page.getByRole("switch", { name: "Single-page application" })).toBeChecked()
  await gotoStep(page, "runtime")
  await expect(page.getByRole("spinbutton", { name: /Port/ })).toHaveValue("80")

  // Every variable the source reads is a row already, with the example as
  // its placeholder and where it was read beside the key.
  await gotoStep(page, "variables")
  await expect(page.locator("#env-key-0")).toHaveValue("DATABASE_URL")
  await expect(page.locator("#env-key-1")).toHaveValue("SESSION_SECRET")
  await expect(page.locator("#env-key-2")).toHaveValue("RESEND_API_KEY")
  await expect(page.locator("#env-value-1")).toHaveAttribute("placeholder", "change me")
  await expect(page.getByText("from .env.example").first()).toBeVisible()
  await expect(page.getByText("from src/lib/mail.ts")).toBeVisible()
  await expect(
    page.getByText("3 detected variables have no value yet and will not be set."),
  ).toBeVisible()

  // The database the code connects to is one button, opening the sheet on
  // the right engine and the right variable.
  const databases = page.getByRole("list", { name: "Detected databases" })
  await expect(databases.getByText("PostgreSQL", { exact: true })).toBeVisible()
  await expect(databases.getByText("pg in package.json")).toBeVisible()
  await databases.getByRole("button", { name: "Add PostgreSQL" }).click()
  await expect(page.getByRole("textbox", { name: "Environment variable" })).toHaveValue(
    "DATABASE_URL",
  )
  await expect(page.getByRole("button", { name: /PostgreSQL 16/ })).toHaveAttribute(
    "aria-pressed",
    "true",
  )
  await page.keyboard.press("Escape")
  await expect(page.getByRole("textbox", { name: "Environment variable" })).toHaveCount(0)

  // A detected row left empty is skipped rather than set to nothing; the
  // one that was filled in is what the project receives.
  await page.locator("#env-value-1").fill("s3cret")
  await expect(
    page.getByText("2 detected variables have no value yet and will not be set."),
  ).toBeVisible()
  await gotoStep(page, "review")
  await page.getByRole("button", { name: "Deploy", exact: true }).click()
  await page.waitForURL(/\/deploy\/77\/runs\/84$/)
  expect(quick.staged()?.dotenv).toBe('SESSION_SECRET="s3cret"')
  const saved = quick.configuration() as { build: Record<string, unknown> }
  expect(saved.build.spaFallback).toBe(true)
  expect(saved.build.outputDirectory).toBe("dist")
})

test("container access options say what they allow, and they and the mounts reach the plan", async ({
  page,
}) => {
  const journey = await mockNewProject(page)
  await page.goto("/deploy/new?mode=advanced")
  await page.getByRole("button", { name: "Import Wayy01/wesmokefish" }).click()
  await gotoStep(page, "runtime")

  // The consequence is the option's title (§7), not a "?" beside a 12px word.
  const privileged = page.getByRole("switch", {
    name: "Run privileged — every host device and kernel capability",
  })
  await expect(privileged).not.toBeChecked()
  await expect(
    page.getByRole("switch", {
      name: "Share the host's network — every port it opens is open on the host",
    }),
  ).not.toBeChecked()
  await privileged.click()
  await expect(privileged).toBeChecked()

  // The mount rows are the Storage settings' own, and a mount named here is
  // one the project makes.
  await page.getByRole("button", { name: "Add mount" }).click()
  await page.locator("#adv-mount-source-0").fill("pgdata")
  await page.locator("#adv-mount-target-0").fill("/var/lib/data")

  await gotoStep(page, "review")
  await page.getByRole("button", { name: "Save only", exact: true }).click()
  await expect.poll(() => journey.commits()).toBe(1)
  expect(journey.configuration()).toMatchObject({
    runtime: {
      privileged: true,
      mounts: [{ source: "pgdata", target: "/var/lib/data", ownership: "managed" }],
    },
  })
})

test("a deploy link arrives on the Git tab with its clone URL and branch filled in", async ({
  page,
}) => {
  await mockNewProject(page)
  await page.goto("/deploy/new?repo=https://github.com/Wayy01/other.git&ref=release")
  await expect(page.getByRole("textbox", { name: "Clone URL" })).toHaveValue(
    "https://github.com/Wayy01/other.git",
  )
  await page.locator("summary").filter({ hasText: "Branch & authentication" }).click()
  await expect(page.getByRole("textbox", { name: "Branch or tag" })).toHaveValue("release")

  // Importing from the prefilled field inspects that URL at that branch.
  await page.getByRole("button", { name: "Import", exact: true }).click()
  await expect(page.getByText("https://github.com/Wayy01/other.git", { exact: true })).toBeVisible()
  await gotoStep(page, "project")
  await expect(page.getByRole("textbox", { name: "Branch" })).toHaveValue("release")

  // Anything that is not a clone URL is left out of the field.
  await page.goto("/deploy/new?repo=javascript:alert(1)")
  await expect(page.getByRole("textbox", { name: "Clone URL" })).toHaveValue("")
})

test("a Laravel import arrives with its application key and address set up, and mints other secrets on request", async ({
  page,
}) => {
  const quick = await mockNewProject(page)
  const candidate = {
    id: "laravel-candidate",
    name: "Laravel application",
    root: "",
    profile: "web",
    buildMethod: "recipe",
    recipe: "php",
    confidence: "high",
    framework: "laravel",
    startCommand:
      "php artisan migrate --force && frankenphp php-server --listen :80 --root /app/public",
    port: 80,
    // Detection says how each is supplied: the server mints APP_KEY in
    // Laravel's own shape at commit, and APP_URL follows the domain.
    variables: [
      {
        name: "APP_KEY",
        sources: [".env.example"],
        setup: "generate",
        setupReason: "Laravel encrypts with it (laravel/framework)",
        generateLength: 32,
        generateFormat: "laravel",
      },
      {
        name: "APP_URL",
        example: "http://localhost",
        sources: [".env.example"],
        setup: "domain",
        setupReason: "Laravel generates absolute URLs from it",
        domainTemplate: "{{scheme}}://{{hostname}}",
      },
      { name: "SESSION_SECRET", sources: ["config/session.php"] },
      { name: "STRIPE_KEY", sources: ["config/services.php"] },
    ],
    evidence: [{ path: "composer.json", reason: "Laravel dependency ^12.0" }],
    needsDecision: [],
  }
  await page.route("**/api/v1/deploy/drafts/journey-draft/detect", (route) =>
    json(route, {
      id: "journey-draft",
      ownerUsername: "operator",
      currentStep: "detection",
      revision: 4,
      data: {
        intent: { name: "wesmokefish", profile: "web" },
        source: {
          kind: "git",
          mode: "git_url",
          url: "https://github.com/Wayy01/shop.git",
          ref: "main",
        },
        detection: {
          source: { kind: "git", remote: "https://github.com/Wayy01/shop.git", ref: "main" },
          candidates: [candidate],
          selectedId: candidate.id,
          scannedFiles: 40,
          scannedBytes: 4096,
          truncated: false,
          gitRequirements: { submodules: false, lfs: false },
        },
      },
      findings: [],
      planPreview: "",
      updatedAt: now,
      expiresAt: "2026-10-04T12:00:00Z",
    }),
  )
  await page.goto("/deploy/new")
  await page.getByRole("button", { name: "Import Wayy01/wesmokefish" }).click()
  await gotoStep(page, "project")
  await expect(page.getByText("Laravel", { exact: true })).toBeVisible()
  await gotoStep(page, "variables")

  // APP_KEY is minted on the server when the project is created, so the
  // browser never holds it; APP_URL follows the suggested address.
  await expect(page.locator("#env-key-0")).toHaveValue("APP_KEY")
  await expect(page.locator("#env-value-0")).toHaveValue("")
  await expect(page.locator("#env-value-0")).toHaveAttribute(
    "placeholder",
    "Generated when the project is created",
  )
  await expect(page.locator("#env-value-1")).toHaveValue("")
  await expect(page.locator("#env-value-1")).toHaveAttribute(
    "placeholder",
    "https://wesmokefish-a1b2c3.203-0-113-7.sslip.io",
  )

  // A self-issued secret typed by hand offers to be minted; one the server
  // mints, and a provider's key, do not.
  const generate = page.getByRole("button", { name: "Generate", exact: true })
  await expect(generate).toHaveCount(1)
  await generate.click()
  await expect(page.locator("#env-value-2")).toHaveValue(/^[0-9a-f]{64}$/)
  await expect(generate).toHaveCount(0)
  // STRIPE_KEY (a provider's key nothing here can mint) is still empty, and
  // the note says so; the set-up rows are not counted.
  await expect(
    page.getByText("1 detected variable has no value yet and will not be set."),
  ).toBeVisible()
  await gotoStep(page, "review")
  await expect(
    page.getByText("a Laravel base64: key over 32 random bytes", { exact: false }),
  ).toBeVisible()
  await page.getByRole("button", { name: "Deploy", exact: true }).click()
  await page.waitForURL(/\/deploy\/77\/runs\/84$/)
  const saved = quick.configuration() as { variables: Record<string, unknown>[] }
  expect(saved.variables).toEqual([
    {
      name: "APP_KEY",
      sensitivity: "secret",
      scopes: ["runtime", "build"],
      generate: 32,
      generateFormat: "laravel",
    },
    {
      name: "APP_URL",
      sensitivity: "plain",
      scopes: ["runtime", "build"],
      domainTemplate: "{{scheme}}://{{hostname}}",
      value: "https://wesmokefish-a1b2c3.203-0-113-7.sslip.io",
    },
    // The secret minted here was staged with the environment; the server
    // records its declaration, never its value.
    { name: "SESSION_SECRET", sensitivity: "secret", scopes: ["runtime", "build"] },
  ])
})

test("the public address can ask visitors for a password before the first deploy", async ({
  page,
}) => {
  const quick = await mockNewProject(page)
  await page.goto("/deploy/new")
  await page.getByRole("button", { name: "Import Wayy01/wesmokefish" }).click()
  await gotoStep(page, "runtime")
  await expect(page.getByRole("textbox", { name: "Hostname" })).toHaveValue(
    "wesmokefish-a1b2c3.203-0-113-7.sslip.io",
  )
  await page.getByRole("switch", { name: "Ask visitors for a password" }).click()
  await page.getByRole("textbox", { name: "User name" }).fill("client")
  await page.locator("#public-protection-password").fill("show and tell")
  expect(await page.evaluate(() => JSON.stringify([localStorage, sessionStorage]))).not.toContain(
    "show and tell",
  )
  await gotoStep(page, "review")
  await page.getByRole("button", { name: "Deploy", exact: true }).click()
  await page.waitForURL(/\/deploy\/77\/runs\/84$/)
  const saved = quick.configuration() as { domains: Record<string, unknown>[] }
  expect(saved.domains).toEqual([
    {
      hostname: "wesmokefish-a1b2c3.203-0-113-7.sslip.io",
      https: true,
      ownership: "managed",
      protection: { username: "client", hash: "fixture-sealed-password" },
    },
  ])
})
