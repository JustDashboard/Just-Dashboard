import { expect, test } from "@playwright/test"
import type { DeploymentDraft } from "../../src/lib/types"
import { json, mockProject, now } from "./deploy-fixture"
import { betBot, mockWorkloadImport, recoveredWorkloadDraft } from "./workload-import-fixture"

test.use({ video: { mode: "on", size: { width: 1280, height: 900 } } })

function automatedDraft(): DeploymentDraft {
  const draft = recoveredWorkloadDraft()
  const inputs = [
    {
      storageKey: "JD_IMPORT_ENV_TELEGRAM_CHAT_ID_FIRST",
      name: "TELEGRAM_CHAT_ID",
      service: "doubles-games-tracker",
      category: "application" as const,
    },
    {
      storageKey: "JD_IMPORT_ENV_TELEGRAM_CHAT_ID_SECOND",
      name: "TELEGRAM_CHAT_ID",
      service: "high-market-tracker",
      category: "application" as const,
    },
    {
      storageKey: "JD_IMPORT_ENV_TELEGRAM_BOT_TOKEN_FIRST",
      name: "TELEGRAM_BOT_TOKEN",
      service: "doubles-games-tracker",
      category: "application" as const,
    },
    {
      storageKey: "JD_IMPORT_ENV_PATH_FIRST",
      name: "PATH",
      service: "doubles-games-tracker",
      category: "image_default" as const,
    },
  ].map((input) => ({
    ...input,
    kind: "environment" as const,
    origin: "container" as const,
    sensitivity: "secret" as const,
    retained: true,
    empty: false,
  }))
  draft.environmentKeys = inputs.map((input) => input.storageKey)
  draft.data.configuration!.variables = inputs.map((input) => ({
    name: input.storageKey,
    sensitivity: "secret",
    scopes: ["runtime"],
    required: false,
  }))
  draft.data.source!.mode = "recovered_snapshot"
  draft.data.source!.resourceId = "a".repeat(64)
  draft.data.adoption = {
    ...draft.data.adoption!,
    scope: "existing_services",
    excludedServices: [],
    inputs,
    buildSources: [
      {
        service: "api",
        framework: "nextjs",
        language: "javascript",
        role: "web",
        status: "snapshot",
        reason: "Verified Compose build context captured.",
      },
      {
        service: "high-market-tracker",
        language: "python",
        role: "worker",
        status: "image_only",
        reason: "Current immutable image remains reproducible; no verified source is attached.",
      },
    ],
    ingressBindings: [
      {
        id: "api-root",
        hostname: "app.example.test",
        path: "/",
        service: "api",
        proxyKind: "caddy",
        owner: "current Caddy",
        status: "linked",
        continuity: "host_port",
        https: true,
      },
      {
        id: "worker-api",
        hostname: "app.example.test",
        path: "/worker",
        service: "high-market-tracker",
        proxyKind: "nginx",
        owner: "current nginx",
        status: "linked",
        continuity: "retarget",
        plannedChange:
          "Deploy changes updates only the verified upstream address; existing TLS, auth and path handling stay in place.",
      },
    ],
  }
  return draft
}

test("captured settings show original names and require explicit intent to replace a value with empty", async ({
  page,
}) => {
  await mockProject(page)
  const writes: Record<string, unknown>[] = []
  await page.route("**/api/v1/deploy/7/environments/12/configuration", (route) =>
    json(route, {
      revision: 3,
      build: { method: "compose" },
      runtime: { strategy: "stop_first" },
      variables: [
        {
          name: "JD_IMPORT_ENV_TELEGRAM_CHAT_ID_FIRST",
          revision: 1,
          sensitivity: "secret",
          scopes: ["runtime"],
          masked: "••••••••",
          valueDigest: `sha256:${"1".repeat(64)}`,
          createdBy: "operator",
          createdAt: now,
          environmentId: 12,
          desiredRevision: 3,
          recoveredInput: automatedDraft().data.adoption!.inputs![0],
        },
      ],
      dependencies: [],
      checks: [],
      domains: [],
      pending: {
        pending: false,
        desiredRevision: 3,
        liveReleaseId: 20,
        livePlanRevision: 3,
        changes: [],
      },
    }),
  )
  await page.route("**/api/v1/deploy/7/environments/12/variables/*", (route) => {
    if (route.request().method() !== "PUT") return route.fallback()
    writes.push(route.request().postDataJSON())
    return json(route, { desiredRevision: 4 })
  })
  await page.goto("/deploy/7/settings/variables")
  const list = page.getByRole("list", { name: "Environment variables" })
  await expect(list).toContainText("doubles-games-tracker · TELEGRAM_CHAT_ID")
  await expect(list).not.toContainText("JD_IMPORT_ENV_")
  await list
    .getByRole("button", { name: "Actions for doubles-games-tracker · TELEGRAM_CHAT_ID" })
    .click()
  await expect(page.getByRole("menuitem", { name: "Remove" })).toHaveCount(0)
  await page.getByRole("menuitem", { name: "Edit" }).click()
  const editor = page.getByRole("dialog", { name: "Edit variable" })
  await editor.getByRole("button", { name: "Save variable" }).click()
  await expect(
    editor
      .getByText("Enter the value again — the dashboard does not read it back.", { exact: true })
      .last(),
  ).toBeVisible()
  expect(writes).toHaveLength(0)
  await editor.getByRole("switch", { name: "Set an empty value", exact: true }).click()
  await editor.getByRole("button", { name: "Save variable" }).click()
  await expect.poll(() => writes.length).toBe(1)
  expect(writes[0]).toEqual({ revision: 3, value: "", sensitivity: "secret", scopes: ["runtime"] })
  await expect(editor).toHaveCount(0)
})

test("automation retains service-specific inputs and reviews grouped evidence and existing domains", async ({
  page,
}, testInfo) => {
  await page.setViewportSize({ width: 1280, height: 900 })
  const draft = automatedDraft()
  const fixture = await mockWorkloadImport(page, [betBot], draft)
  await page.route("**/api/v1/deploy/drafts/recovered-workload-draft/preflight", (route) =>
    json(route, {
      draft: { ...draft, revision: route.request().postDataJSON().revision + 1 },
      preflight: {
        revision: route.request().postDataJSON().revision + 1,
        findings: ["doubles-games-tracker", "high-market-tracker"].map((service, index) => ({
          code: `adoption_data_${index}`,
          issueCode: "persistent_data_reused",
          service,
          severity: "warning",
          title: "Existing data needs a recovery plan",
          measured: `${service}: Existing storage is retained. Verify its backup before deploying changes.`,
          means: "Image rollback does not restore persistent data.",
          action: "Verify a backup before deploying changes.",
        })),
        expectedDowntime: true,
        preview: "",
        digest: "automated-plan",
        plan: { actions: [] },
      },
    }),
  )
  await page.goto(`/deploy/new?draft=${draft.id}`)
  await expect(page.getByRole("heading", { name: "Ready to adopt this deployment?" })).toBeVisible()
  await expect(page.getByRole("list", { name: "Existing domain routes" })).toContainText(
    "app.example.test/worker",
  )
  await expect(page.getByRole("list", { name: "Recovered build sources" })).toContainText("nextjs")
  const warnings = page.locator("#deployment-warning-acknowledgements")
  await expect(warnings.getByRole("checkbox")).toHaveCount(1)
  await warnings.getByRole("checkbox").check()
  await expect(page.getByRole("button", { name: "Adopt deployment", exact: true })).toBeEnabled()
  await page.screenshot({ path: testInfo.outputPath("automated-review-1280.png"), fullPage: true })
  await page.setViewportSize({ width: 1720, height: 1000 })
  await page.screenshot({ path: testInfo.outputPath("automated-review-1720.png"), fullPage: true })
  await page.setViewportSize({ width: 1280, height: 900 })
  await page.getByRole("button", { name: "Back", exact: true }).click()
  await expect(page.getByRole("heading", { name: "What does it need to run?" })).toBeVisible()
  const panel = page.locator("[data-slot=flow-panel]")
  await expect(panel).toContainText("TELEGRAM_CHAT_ID")
  await expect(panel).not.toContainText("JD_IMPORT_ENV_")
  await expect(
    page.getByRole("button", {
      name: "Replace doubles-games-tracker · TELEGRAM_CHAT_ID",
      exact: true,
    }),
  ).toBeVisible()
  await page.screenshot({
    path: testInfo.outputPath("original-environment-1280.png"),
    fullPage: true,
  })
  await page
    .getByRole("button", { name: "Replace doubles-games-tracker · TELEGRAM_CHAT_ID", exact: true })
    .click()
  await page
    .getByLabel("New value for doubles-games-tracker · TELEGRAM_CHAT_ID", { exact: true })
    .fill("first-$literal\nsecond-line")
  await page
    .getByRole("button", { name: "Replace high-market-tracker · TELEGRAM_CHAT_ID", exact: true })
    .click()
  await page.getByRole("button", { name: "Continue", exact: true }).click()
  await expect
    .poll(() => fixture.configurationSaves.at(-1)?.dotenv)
    .toContain('JD_IMPORT_ENV_TELEGRAM_CHAT_ID_FIRST="first-$literal\\nsecond-line"')
  const save = fixture.configurationSaves.at(-1) as {
    dotenv: string
    retainEnvironmentKeys: string[]
  }
  expect(save.dotenv).toContain(
    'JD_IMPORT_ENV_TELEGRAM_CHAT_ID_FIRST="first-$literal\\nsecond-line"',
  )
  expect(save.dotenv).toContain('JD_IMPORT_ENV_TELEGRAM_CHAT_ID_SECOND=""')
  expect(save.retainEnvironmentKeys).toEqual(draft.environmentKeys)
  await warnings.getByRole("checkbox").check()
  await page.getByRole("button", { name: "Adopt deployment", exact: true }).click()
  await expect(page).toHaveURL(/\/deploy\/77$/)
  expect(fixture.adoptions[0].acknowledgedWarnings).toEqual(["adoption_data_0", "adoption_data_1"])
  expect(fixture.calls.some((call) => call.method === "POST" && /\/runs$/.test(call.path))).toBe(
    false,
  )
})
