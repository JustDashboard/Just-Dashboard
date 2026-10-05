import { expect, test } from "@playwright/test"
import type { Page } from "@playwright/test"
import type {
  DeploymentConfiguration,
  DeploymentDraft,
  DeploymentSourceMode,
} from "../../src/lib/types"
import { gotoStep, json, mockNewProject, now } from "./deploy-fixture"

const configuration: DeploymentConfiguration = {
  build: { method: "none" },
  runtime: { internalPort: 0, hostPort: 0, bindAddress: "127.0.0.1", strategy: "blue_green" },
  variables: [],
  dependencies: [],
  checks: [],
  domains: [],
}

function legacyDraft(mode: DeploymentSourceMode, configured = true): DeploymentDraft {
  return {
    id: "legacy-import-draft",
    ownerUsername: "operator",
    currentStep: configured ? "configuration" : "source",
    revision: 5,
    data: {
      intent: { name: "legacy-workload", profile: "imported" },
      source: {
        kind: "import",
        mode,
        resourceId: mode === "existing_checkout" ? "" : "legacy-workload",
        ...(mode === "existing_checkout" ? { localPath: "/srv/legacy-workload" } : {}),
      },
      ...(configured ? { configuration } : {}),
    },
    findings: [],
    planPreview: "",
    updatedAt: now,
    expiresAt: "2027-01-01T12:00:00Z",
  }
}

async function mockLegacyDraft(page: Page, initial: DeploymentDraft) {
  await mockNewProject(page)
  let draft = initial
  const mutations: string[] = []
  page.on("request", (request) => {
    if (request.url().includes("/api/v1/deploy/") && request.method() !== "GET")
      mutations.push(new URL(request.url()).pathname)
  })
  await page.route("**/api/v1/deploy/drafts", (route) =>
    json(route, [
      {
        id: draft.id,
        name: draft.data.intent?.name,
        source: draft.data.source?.resourceId || draft.data.source?.localPath,
        currentStep: draft.currentStep,
        updatedAt: draft.updatedAt,
        expiresAt: draft.expiresAt,
      },
    ]),
  )
  await page.route(`**/api/v1/deploy/drafts/${draft.id}`, (route) => {
    if (route.request().method() === "PUT") {
      const body = route.request().postDataJSON() as { configuration?: DeploymentConfiguration }
      draft = {
        ...draft,
        revision: draft.revision + 1,
        data: {
          ...draft.data,
          ...(body.configuration ? { configuration: body.configuration } : {}),
        },
      }
    }
    return json(route, draft)
  })
  await page.route(`**/api/v1/deploy/drafts/${draft.id}/preflight`, (route) => {
    draft = { ...draft, revision: draft.revision + 1 }
    return json(route, {
      draft,
      preflight: {
        revision: draft.revision,
        findings: [],
        preview: "",
        digest: `sha256:${"a".repeat(64)}`,
        plan: { actions: [] },
      },
    })
  })
  await page.route("**/api/v1/deploy/import/adopt", (route) =>
    json(route, { projectId: 77, environmentId: 78, planRevision: draft.revision, created: true }),
  )
  await page.route("**/api/v1/deploy/import/discovery", (route) =>
    json(route, { checkedAt: now, items: [], silences: [] }),
  )
  return { mutations }
}

for (const mode of ["existing_container", "existing_stack"] as const) {
  test(`a saved ${mode} draft opens discovery without changing or redetecting it`, async ({
    page,
  }) => {
    // The stack never reached detection: recovery must work without building
    // a Configure flow from configuration that the old setup never saved.
    const draft = legacyDraft(mode, mode === "existing_container")
    const api = await mockLegacyDraft(page, draft)
    await page.goto("/deploy/new")
    await page.getByRole("button", { name: /Unfinished setups/ }).click()
    await page.getByRole("link", { name: "Resume legacy-workload" }).click()

    await expect(
      page.getByRole("heading", { name: "Import this existing workload?" }),
    ).toBeVisible()
    await expect(page.getByRole("heading", { name: "legacy-workload", exact: true })).toBeVisible()
    await expect(page.getByText(/Your saved setup remains available/)).toBeVisible()
    await expect(page.getByRole("button", { name: "Adopt workload" })).toHaveCount(0)
    expect(api.mutations).toEqual([])

    const discover = page.getByRole("link", { name: "Discover existing workloads" })
    await expect(discover).toHaveAttribute("href", "/deploy/import")
    await discover.click()
    await expect(page).toHaveURL(/\/deploy\/import$/)
    expect(api.mutations).toEqual([])
  })
}

test("a remembered external flow offers discovery and preserves its saved draft", async ({
  page,
}) => {
  const draft = legacyDraft("existing_stack")
  const api = await mockLegacyDraft(page, draft)
  const flow = {
    name: "legacy-workload",
    profile: "imported",
    source: draft.data.source,
    draft,
    configuration,
    sourceLabel: "legacy-workload",
  }
  await page.addInitScript((remembered) => {
    sessionStorage.setItem(
      "jd.session.state.entry.deploy.new.configure.flow",
      JSON.stringify(remembered),
    )
    sessionStorage.setItem("jd.session.state.entry.deploy.new.configure.step", '"review"')
  }, flow)
  await page.goto("/deploy/new")

  await expect(page.getByRole("heading", { name: "Import this existing workload?" })).toBeVisible()
  await expect(page.getByRole("link", { name: "Discover existing workloads" })).toBeVisible()
  await expect(page.getByRole("button", { name: "Adopt workload" })).toHaveCount(0)
  expect(api.mutations).toEqual([])
  expect(
    await page.evaluate(
      () =>
        JSON.parse(sessionStorage.getItem("jd.session.state.entry.deploy.new.configure.flow")!)
          .draft.id,
    ),
  ).toBe(draft.id)
})

test("a legacy checkout draft keeps Configure and its adoption endpoint", async ({ page }) => {
  const draft = legacyDraft("existing_checkout")
  const api = await mockLegacyDraft(page, draft)
  // The draft takes precedence over a legacy profile link, so checkout
  // imports are not accidentally sent to the external-workload importer.
  await page.goto(`/deploy/new?profile=imported&draft=${draft.id}`)
  await gotoStep(page, "project")
  await expect(page.getByRole("textbox", { name: "Project name" })).toHaveValue("legacy-workload")
  await expect(page.getByRole("link", { name: "Discover existing workloads" })).toHaveCount(0)
  await gotoStep(page, "review")
  await page.getByRole("button", { name: "Adopt workload" }).click()
  await expect(page).toHaveURL(/\/deploy\/77$/)
  expect(api.mutations.filter((path) => path.endsWith("/deploy/import/adopt"))).toHaveLength(1)
  expect(api.mutations.some((path) => path.endsWith("/runs"))).toBe(false)
})

for (const query of ["source=import", "profile=imported"]) {
  test(`a legacy ${query} link without a draft opens the importer`, async ({ page }) => {
    const api = await mockLegacyDraft(page, legacyDraft("existing_container"))
    await page.goto(`/deploy/new?${query}`)
    await expect(page).toHaveURL(/\/deploy\/import$/)
    await expect(page.getByRole("group", { name: "Project source" })).toHaveCount(0)
    expect(api.mutations).toEqual([])
  })
}
