import { expect, test } from "@playwright/test"
import type { Page } from "@playwright/test"
import type { DeploymentEnvironmentConfiguration, DeploymentDraftSource } from "../../src/lib/types"
import {
  deployment,
  healthyOperations,
  json,
  mockProject,
  project,
  saveSettings,
  user,
} from "./deploy-fixture"
import { mockWorkloadImport, recoveredWorkloadDraft } from "./workload-import-fixture"

const yaml = `services:
  app:
    image: n8nio/n8n:1.100.0
    environment:
      N8N_ENCRYPTION_KEY: \${N8N_ENCRYPTION_KEY}
    mem_limit: 512m
    cpus: 0.5
    volumes:
      - n8n-data:/home/node/.n8n
volumes:
  n8n-data:
    external: true
`

async function mockComposeProject(page: Page, recovered = false) {
  await mockProject(page, {
    operations: {
      ...healthyOperations,
      storage: { ...healthyOperations.storage, status: "available", mounts: [] },
    },
  })
  let source: DeploymentDraftSource = {
    kind: "compose",
    mode: "compose_paste",
    composeFiles: [{ path: "compose.yml", order: 0, content: yaml }],
  }
  if (recovered) {
    source.mode = "recovered_snapshot"
    source.resourceId = `sha256:${"a".repeat(64)}`
  }
  let revision = 4
  const writes: { method: string; path: string }[] = []
  const sources: Record<string, unknown>[] = []
  const configuration = (): DeploymentEnvironmentConfiguration => ({
    revision,
    source,
    build: { method: "compose", primaryService: "app" },
    runtime: { strategy: "stop_first", memoryMb: 0, cpus: 0, pidsLimit: 0, mounts: [] },
    variables: [],
    dependencies: [],
    checks: [],
    domains: [],
    pending: {
      pending: revision > 4,
      desiredRevision: revision,
      liveReleaseId: 20,
      livePlanRevision: 4,
      changes: revision > 4 ? [{ kind: "source", name: "Compose source", change: "changed" }] : [],
    },
  })
  await page.route("**/api/v1/**", async (route) => {
    const request = route.request()
    const path = new URL(request.url()).pathname.replace(/^\/api\/v1/, "")
    if (request.method() !== "GET") writes.push({ method: request.method(), path })
    if (path === "/deploy/7")
      return json(route, {
        project,
        running: true,
        deployment: {
          ...deployment,
          profile: "compose",
          sourceKind: source.kind,
          buildMethod: "compose",
          activeRun: undefined,
          desiredRevision: revision,
          livePlanRevision: 4,
          pendingChanges: revision > 4,
        },
      })
    if (path === "/deploy/7/environments/12/configuration") return json(route, configuration())
    if (path === "/deploy/7/environments/12/source") {
      const body = request.postDataJSON()
      sources.push(body)
      if (body.composeFiles[0].content === "services: [")
        return route.fulfill({
          status: 400,
          contentType: "application/json",
          body: JSON.stringify({
            error: { code: "invalid_compose", message: "compose.yml has invalid YAML." },
          }),
        })
      source = { ...source, composeFiles: body.composeFiles }
      revision += 1
      return json(route, { ...configuration(), source })
    }
    return route.fallback()
  })
  return { writes, sources, source: () => source, revision: () => revision }
}

test("editing recovered Compose documents retains their verified build context handle", async ({
  page,
}) => {
  const fixture = await mockComposeProject(page, true)
  await page.goto("/deploy/7/settings/general")
  await page
    .getByRole("textbox", { name: "compose.yml content", exact: true })
    .fill(yaml.replace("512m", "768m"))
  await saveSettings(page)
  await expect.poll(() => fixture.sources.length).toBe(1)
  expect(fixture.sources[0].mode).toBe("recovered_snapshot")
  expect(fixture.sources[0].resourceId).toBe(`sha256:${"a".repeat(64)}`)
  expect(fixture.writes.filter((write) => !write.path.endsWith("/check"))).toEqual([
    { method: "PUT", path: "/deploy/7/environments/12/source" },
  ])
})

test("Compose overrides never imply absent service limits or storage and link to editable source", async ({
  page,
}) => {
  await mockComposeProject(page)
  await page.goto("/deploy/7/settings/runtime")
  await expect(page.getByText("No limits", { exact: true })).toHaveCount(0)
  await expect(page.getByText("no limit", { exact: true })).toHaveCount(0)
  await expect(page.getByText("keeps Compose limits", { exact: true })).toHaveCount(3)
  await page.getByRole("link", { name: "Compose source", exact: true }).click()
  await expect(page).toHaveURL(/\/settings\/general#compose-source$/)
  await expect(page.getByRole("textbox", { name: "compose.yml content" })).toHaveValue(yaml)

  await page.goto("/deploy/7/settings/storage")
  await expect(page.getByText("No persistent mounts", { exact: true })).toHaveCount(0)
  await expect(page.getByText("This release declares no persistent storage.")).toHaveCount(0)
  await expect(
    page.getByText(/Each service keeps its Compose volumes and bind mounts/),
  ).toBeVisible()
  await expect(page.getByRole("link", { name: "Compose source", exact: true })).toHaveAttribute(
    "href",
    "/deploy/7/settings/general#compose-source",
  )
})

test("invalid Compose edits preserve the source and valid version edits stay pending without runtime mutations", async ({
  page,
}) => {
  const fixture = await mockComposeProject(page)
  await page.goto("/deploy/7/settings/general")
  const editor = page.getByRole("textbox", { name: "compose.yml content" })
  await editor.fill("services: [")
  await saveSettings(page)
  await expect(page.getByRole("alert").getByText("compose.yml has invalid YAML.")).toBeVisible()
  await expect(editor).toHaveValue("services: [")
  expect(fixture.revision()).toBe(4)
  expect(fixture.source().composeFiles?.[0].content).toBe(yaml)

  await page.reload()
  await expect(editor).toHaveValue(yaml)
  const edited = yaml.replace("1.100.0", "1.101.0")
  await editor.fill(edited)
  await saveSettings(page)
  await expect(page.getByText("1 saved change not live", { exact: true })).toBeVisible()
  await expect(editor).toHaveValue(edited)
  expect(fixture.sources.at(-1)).toMatchObject({
    revision: 4,
    kind: "compose",
    mode: "compose_paste",
    composeFiles: [{ path: "compose.yml", order: 0, content: edited }],
  })
  expect(fixture.writes.filter((write) => !write.path.endsWith("/check"))).toEqual([
    { method: "PUT", path: "/deploy/7/environments/12/source" },
    { method: "PUT", path: "/deploy/7/environments/12/source" },
  ])
  await page.screenshot({
    path: test.info().outputPath("compose-source-pending.png"),
    fullPage: true,
  })
})

test("a reader can inspect captured Compose configuration without editing it", async ({ page }) => {
  await mockComposeProject(page)
  await page.route("**/api/v1/auth/session", (route) =>
    json(route, { ...user, capabilities: ["read"] }),
  )
  await page.goto("/deploy/7/settings/general")
  await expect(page.getByRole("textbox", { name: "compose.yml content" })).toHaveAttribute(
    "readonly",
  )
  await expect(page.getByRole("button", { name: "Add file" })).toBeDisabled()
  await expect(page.getByRole("button", { name: "Remove compose.yml" })).toBeDisabled()
})

test("a Git-backed Compose stack links to its repository source settings", async ({ page }) => {
  const fixture = await mockComposeProject(page)
  Object.assign(fixture.source(), {
    kind: "git",
    mode: "git_url",
    url: "https://github.com/acme/stack",
    ref: "main",
  })
  await page.goto("/deploy/7/settings/runtime")
  const source = page.getByRole("link", { name: "Compose source", exact: true })
  await expect(source).toHaveAttribute("href", "/deploy/7/settings/general#source")
  await source.click()
  await expect(page.getByRole("form", { name: "Source", exact: true })).toBeVisible()
  await expect(page.getByRole("textbox", { name: "Repository URL" })).toHaveValue(
    "https://github.com/acme/stack",
  )
})

test("recovered Compose review shows service mounts and captured limits without empty-storage claims", async ({
  page,
}) => {
  const draft = recoveredWorkloadDraft()
  draft.data.source = {
    kind: "compose",
    mode: "compose_paste",
    composeFiles: [{ path: "compose.yml", order: 0, content: yaml }],
  }
  draft.data.configuration!.runtime.mounts = []
  draft.data.detection = {
    source: { kind: "compose" },
    candidates: [],
    scannedFiles: 1,
    scannedBytes: yaml.length,
    truncated: false,
    gitRequirements: { submodules: false, lfs: false },
    compose: {
      files: ["compose.yml"],
      services: [
        {
          name: "app",
          image: "n8nio/n8n:1.100.0",
          ports: [],
          mounts: ["n8n-data:/home/node/.n8n"],
          advanced: [],
        },
      ],
      variables: ["N8N_ENCRYPTION_KEY"],
      warnings: [],
      unsupported: [],
      preview: yaml,
      digest: "checked-compose",
    },
  }
  await mockWorkloadImport(page, undefined, draft)
  await page.goto(`/deploy/new?draft=${draft.id}`)
  await page.getByRole("button", { name: "Continue", exact: true }).click()
  await expect(page.getByText(/Nothing survives a rebuild/)).toHaveCount(0)
  const plan = page.getByRole("list", { name: "What this setup will create" })
  await expect(
    plan.getByText("Service limits are in Compose source", { exact: true }),
  ).toBeVisible()
  await expect(plan.getByText("No memory or CPU limit", { exact: true })).toHaveCount(0)
  await expect(page.getByRole("list", { name: "Compose service mounts" })).toContainText(
    "n8n-data:/home/node/.n8n",
  )
  await page.getByText("Compose service configuration", { exact: true }).click()
  await expect(page.getByRole("textbox", { name: "Reviewed compose.yml" })).toHaveValue(yaml)
  await expect(page.getByRole("textbox", { name: "Reviewed compose.yml" })).toHaveAttribute(
    "readonly",
  )
  await page.screenshot({ path: test.info().outputPath("compose-review.png"), fullPage: true })
})
