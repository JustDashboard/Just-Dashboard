import { expect, test } from "@playwright/test"
import type { DeploymentRuntimeServices } from "../../src/lib/types"
import { deployment, json, mockProject, now, project } from "./deploy-fixture"
import { betBot, mockWorkloadImport, recoveredWorkloadDraft } from "./workload-import-fixture"

test("existing-container scope includes stopped containers and requires review of server exclusions", async ({
  page,
}) => {
  const fixture = await mockWorkloadImport(page)
  await page.goto("/deploy/import")
  await page.getByRole("button", { name: "Review bet-bot" }).click()
  const all = page.getByRole("button", { name: "Every declared service", exact: true })
  const existing = page.getByRole("button", { name: "Existing containers only", exact: true })
  await expect(all).toHaveAttribute("aria-pressed", "true")
  await existing.focus()
  await page.keyboard.press("Enter")
  await expect(existing).toHaveAttribute("aria-pressed", "true")
  await expect(
    page.getByRole("list", { name: "Services to import" }).getByText("exited", { exact: true }),
  ).toHaveCount(2)
  await page.getByRole("button", { name: "Review migration" }).click()
  await expect(page).toHaveURL(/\/deploy\/new\?draft=recovered-workload-draft/)
  expect(fixture.recoveries).toEqual([
    { key: betBot.key, name: betBot.name, digest: betBot.digest, scope: "existing_services" },
  ])
  await expect(page.getByRole("list", { name: "Excluded Compose services" })).toContainText(
    "optional-worker",
  )
  await page.getByRole("button", { name: "Continue", exact: true }).click()
  await expect(
    page.getByText("Existing containers only · running and stopped", { exact: true }),
  ).toBeVisible()
  const excluded = page.getByRole("list", { name: "Excluded Compose services" })
  await expect(excluded.getByRole("listitem")).toHaveCount(2)
  await expect(excluded).toContainText("optional-worker")
  await expect(excluded).toContainText("unstarted-cache")
  await page.getByRole("checkbox", { name: /Review recovered runtime behavior/ }).check()
  await expect(page.getByRole("button", { name: "Adopt deployment", exact: true })).toHaveCount(0)
  expect(fixture.adoptions).toEqual([])
  await page.getByRole("checkbox", { name: /Review excluded Compose services/ }).check()
  await page.screenshot({
    path: test.info().outputPath("compose-existing-scope-review.png"),
    fullPage: true,
  })
  await page.getByRole("button", { name: "Adopt deployment", exact: true }).click()
  await expect(page).toHaveURL(/\/deploy\/77$/)
  expect(fixture.adoptions[0]).toMatchObject({
    acknowledgedWarnings: ["adoption_warning_1", "adoption_warning_2"],
  })
  expect(fixture.adoptions[0]).not.toHaveProperty("scope")
  expect(fixture.adoptions[0]).not.toHaveProperty("excludedServices")
})

test("choosing another stack resets scope to every declared service", async ({ page }) => {
  await mockWorkloadImport(page)
  await page.goto("/deploy/import")
  await page.getByRole("button", { name: "Review bet-bot" }).click()
  await page.getByRole("button", { name: "Existing containers only", exact: true }).click()
  await page.getByRole("button", { name: "Back", exact: true }).click()
  await page.getByRole("button", { name: "Review bet-bot" }).click()
  await expect(
    page.getByRole("button", { name: "Every declared service", exact: true }),
  ).toHaveAttribute("aria-pressed", "true")
})

test("recovered settings require server warning acknowledgement and adopt without a deploy run", async ({
  page,
}) => {
  const fixture = await mockWorkloadImport(page)
  await page.goto("/deploy/new?draft=recovered-workload-draft")
  await expect(page.getByRole("heading", { name: "What does it need to run?" })).toBeVisible()
  await page.getByRole("button", { name: "Continue", exact: true }).click()
  await expect(page.getByRole("heading", { name: "Ready to adopt this deployment?" })).toBeVisible()
  await expect(
    page.getByText("Adoption keeps this application running", { exact: true }),
  ).toBeVisible()
  await expect(page.getByText("2 of 4 running", { exact: true })).toBeVisible()
  await expect(page.getByText("1 saved on the server", { exact: true })).toBeVisible()
  await expect(page.getByText("bet-bot_db", { exact: true })).toBeVisible()
  await expect(page.getByText("When you deploy changes", { exact: true })).toBeVisible()
  await expect(page.getByRole("button", { name: "Acknowledge, then adopt" })).toBeEnabled()
  await page.getByRole("button", { name: "Acknowledge, then adopt" }).click()
  await expect.poll(() => fixture.configurationSaves.length).toBeGreaterThan(0)
  expect(fixture.adoptions).toEqual([])
  await page.getByRole("checkbox", { name: /Review recovered runtime behavior/ }).check()
  await page.getByRole("button", { name: "Adopt deployment", exact: true }).click()
  await expect(page).toHaveURL(/\/deploy\/77$/)
  expect(fixture.adoptions).toHaveLength(1)
  expect(fixture.adoptions[0]).toMatchObject({
    draftId: "recovered-workload-draft",
    acknowledgedWarnings: ["adoption_warning_1"],
  })
  expect(fixture.configurationSaves.at(-1)).toMatchObject({
    retainEnvironmentKeys: ["DATABASE_PASSWORD"],
    configuration: {
      runtime: {
        strategy: "stop_first",
        mounts: [{ source: "bet-bot_db", target: "/var/lib/postgresql/data", ownership: "linked" }],
      },
    },
  })
  expect(
    fixture.calls.some((call) => call.method === "POST" && /\/runs$|\/commit$/.test(call.path)),
  ).toBe(false)
})

test("blocked recovery remains on discovery and an immutable baseline is excluded from remembered setup", async ({
  page,
}) => {
  const fixture = await mockWorkloadImport(page)
  await page.route("**/api/v1/deploy/import/recover", (route) =>
    route.fulfill({
      status: 409,
      contentType: "application/json",
      body: JSON.stringify({
        error: {
          code: "migration_blocked",
          message: "This process has no safe restart authority. Open its manager to supply one.",
        },
      }),
    }),
  )
  await page.goto("/deploy/import")
  await page.getByRole("button", { name: "Review bet-bot" }).click()
  await page.getByRole("button", { name: "Review migration" }).click()
  await expect(page.getByRole("alert").getByText(/no safe restart authority/)).toBeVisible()
  await expect(page).toHaveURL(/\/deploy\/import$/)
  expect(fixture.adoptions).toEqual([])

  const draft = recoveredWorkloadDraft()
  await page.route("**/api/v1/deploy/drafts/recovered-workload-draft", (route) =>
    route.request().method() === "GET"
      ? json(route, {
          ...draft,
          data: {
            ...draft.data,
            adoption: {
              ...draft.data.adoption,
              baseline: { environment: { TOKEN: "baseline-private-token" } },
              unexpectedEnvironment: { TOKEN: "unexpected-private-token" },
            },
          },
        })
      : route.fallback(),
  )
  await page.goto("/deploy/new?draft=recovered-workload-draft")
  await expect(page.getByRole("heading", { name: "What does it need to run?" })).toBeVisible()
  await expect(page.getByText(/baseline-private-token|unexpected-private-token/)).toHaveCount(0)
  const stored = await page.evaluate(() => JSON.stringify({ ...localStorage, ...sessionStorage }))
  expect(stored).not.toContain("baseline-private-token")
  expect(stored).not.toContain("unexpected-private-token")
})

test("changed origin at adoption gives a reinspect path and never starts a run", async ({
  page,
}) => {
  const fixture = await mockWorkloadImport(page)
  await page.route("**/api/v1/deploy/import/adopt", (route) =>
    route.fulfill({
      status: 409,
      contentType: "application/json",
      body: JSON.stringify({
        error: {
          code: "workload_changed",
          message: "The original runtime changed. Inspect it again before adopting.",
        },
      }),
    }),
  )
  await page.goto("/deploy/new?draft=recovered-workload-draft")
  await expect(page.getByRole("heading", { name: "What does it need to run?" })).toBeVisible()
  await page.getByRole("button", { name: "Continue", exact: true }).click()
  await page.getByRole("checkbox", { name: /Review recovered runtime behavior/ }).check()
  await page.getByRole("button", { name: "Adopt deployment", exact: true }).click()
  await expect(page.getByRole("link", { name: /Reinspect workload/ })).toHaveAttribute(
    "href",
    "/deploy/import",
  )
  await expect(page).toHaveURL(/\/deploy\/new\?draft=/)
  expect(fixture.calls.some((call) => call.method === "POST" && /\/runs$/.test(call.path))).toBe(
    false,
  )
})

test("a recovered Git source starts manual even when an earlier setup remembered automatic deployment", async ({
  page,
}) => {
  await page.addInitScript(() => {
    sessionStorage.setItem(
      "jd.session.state",
      JSON.stringify({
        "deploy.new.configure.gitPolicy": {
          automatic: true,
          watchInclude: ["old-app/**"],
          watchExclude: [],
          commitStatuses: true,
        },
      }),
    )
  })
  const draft = recoveredWorkloadDraft()
  draft.data.source = { kind: "local", mode: "local_checkout", localPath: "/srv/bot", ref: "main" }
  draft.data.intent = { name: "bot", profile: "web" }
  draft.data.configuration!.runtime = {
    ...draft.data.configuration!.runtime,
    user: "1000:1000",
    workingDirectory: "/opt/app",
    hostNetwork: true,
  }
  draft.data.configuration!.build = {
    method: "recipe",
    recipe: "node",
    startCommand: "node app.js",
  }
  const fixture = await mockWorkloadImport(page, [betBot], draft)
  await page.goto("/deploy/new?draft=recovered-workload-draft")
  await expect(page.getByRole("heading", { name: "What does it need to run?" })).toBeVisible()
  await page.getByRole("button", { name: "Back", exact: true }).click()
  await page.getByRole("spinbutton", { name: "Port the app listens on" }).fill("3001")
  await page.getByRole("button", { name: "Continue", exact: true }).click()
  await page.getByRole("button", { name: "Continue", exact: true }).click()
  await expect(
    page.getByRole("switch", { name: "Deploy new commits automatically" }),
  ).not.toBeChecked()
  await page.getByRole("checkbox", { name: /Review recovered runtime behavior/ }).check()
  await page.getByRole("button", { name: "Adopt deployment", exact: true }).click()
  await expect(page).toHaveURL(/\/deploy\/77$/)
  expect(fixture.adoptions[0]).toMatchObject({
    gitPolicy: { automatic: false, watchInclude: [], watchExclude: [], commitStatuses: true },
  })
  expect(fixture.configurationSaves.at(-1)).toMatchObject({
    configuration: {
      runtime: {
        internalPort: 3001,
        user: "1000:1000",
        workingDirectory: "/opt/app",
        hostNetwork: true,
      },
    },
  })
})

for (const manager of ["pm2", "systemd"] as const) {
  test(`${manager} adopted baselines expose full project controls and read the original log source`, async ({
    page,
  }, testInfo) => {
    const resourceId = manager === "pm2" ? "app-0" : "bot.service"
    const source = manager === "pm2" ? "pm2:daemon/0/bot" : "journal:bot.service"
    const runtime: DeploymentRuntimeServices = {
      status: "available",
      observedAt: now,
      services: [
        {
          manager,
          containerId: "",
          resourceId,
          logSource: source,
          name: "bot",
          releaseId: 20,
          liveRelease: true,
          state: "running",
          health: "",
          imageId: "",
        },
      ],
    }
    await mockProject(page, { runtime })
    const logReads: string[] = []
    const dockerReads: string[] = []
    page.on("request", (request) => {
      const url = new URL(request.url())
      if (url.pathname === "/api/v1/logs/source")
        logReads.push(url.searchParams.get("source") ?? "")
      if (url.pathname.startsWith("/api/v1/docker/containers/")) dockerReads.push(url.pathname)
    })
    await page.route("**/api/v1/logs/source?*", (route) =>
      json(route, {
        id: source,
        label: "bot",
        kind: manager === "pm2" ? "pm2" : "journal",
        lens: "plain",
      }),
    )
    await page.route("**/api/v1/deploy/7", (route) =>
      json(route, {
        project: { ...project, name: "bot", profile: "worker" },
        running: true,
        runtime,
        deployment: {
          ...deployment,
          name: "bot",
          profile: "worker",
          sourceKind: "local",
          strategy: "stop_first",
          expectedDowntime: true,
          activeRun: undefined,
          lastRun: undefined,
          pendingChanges: false,
          endpoint: "",
          importedWorkload: { ...betBot, kind: manager },
          health: "healthy",
        },
      }),
    )
    await page.goto("/deploy/7/runtime")
    await expect(
      page.getByRole("link", { name: `Open bot in ${manager === "pm2" ? "PM2" : "systemd"}` }),
    ).toHaveAttribute("href", manager === "pm2" ? "/processes/pm2" : "/processes/services")
    await expect(page.getByRole("button", { name: "Redeploy", exact: true })).toBeVisible()
    await page.getByRole("button", { name: "Deployment actions" }).click()
    await expect(page.getByRole("menuitem", { name: "Open in Docker", exact: true })).toHaveCount(0)
    await page.keyboard.press("Escape")
    await page.screenshot({ path: testInfo.outputPath(`${manager}-runtime.png`), fullPage: true })
    await expect(page.getByRole("heading", { name: "Original workload" })).toHaveCount(0)
    await page.goto("/deploy/7/console")
    await expect(
      page.getByText("The current runtime uses a host manager", { exact: true }),
    ).toBeVisible()
    await page.goto(`/deploy/7/logs?view=output&service=${encodeURIComponent(resourceId)}`)
    await expect.poll(() => logReads).toContain(source)
    expect(logReads).not.toContain("docker:")
    expect(dockerReads).toEqual([])
    await page.goto("/deploy/7/settings/runtime")
    await expect(page.getByRole("heading", { name: "Runtime", exact: true })).toBeVisible()
  })
}
