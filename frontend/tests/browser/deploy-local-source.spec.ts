import { expect, test } from "@playwright/test"
import type { Page } from "@playwright/test"
import type { DeploymentDraftSource, DeploymentEnvironmentConfiguration } from "../../src/lib/types"
import { deployment, json, mockProject, project, saveSettings, user } from "./deploy-fixture"

async function mockLocalProject(page: Page, recovered = false) {
  await mockProject(page)
  let source: DeploymentDraftSource = {
    kind: "local",
    mode: "local_directory",
    localPath: "/srv/original-native-app",
    excludePaths: ["data", "uploads", "storage"],
    managedInPlace: false,
    platform: "linux/amd64",
  }
  if (recovered) {
    source.mode = "recovered_snapshot"
    source.resourceId = `sha256:${"a".repeat(64)}`
    source.localPath = undefined
  }
  let revision = 4
  let refused = false
  const writes: { method: string; path: string }[] = []
  const sources: Record<string, unknown>[] = []
  const configuration = (): DeploymentEnvironmentConfiguration => ({
    revision,
    source,
    build: { method: "recipe", recipe: "node", nodeVersion: "22" },
    runtime: {
      strategy: "stop_first",
      user: "1000:1000",
      workingDirectory: "/srv/app",
      mounts: [
        {
          source: "/srv/data",
          target: "/srv/app/data",
          ownership: "linked",
        },
      ],
    },
    variables: [],
    dependencies: [],
    checks: [],
    domains: [],
    pending: {
      pending: revision > 4,
      desiredRevision: revision,
      liveReleaseId: 20,
      livePlanRevision: 1,
      changes: revision > 4 ? [{ kind: "source", name: "Build source", change: "changed" }] : [],
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
          sourceKind: "local",
          buildMethod: "recipe",
          activeRun: undefined,
          desiredRevision: revision,
          livePlanRevision: 1,
          pendingChanges: revision > 4,
        },
      })
    if (path === "/deploy/7/environments/12/configuration") return json(route, configuration())
    if (path === "/deploy/7/environments/12/source") {
      const body = request.postDataJSON()
      sources.push(body)
      if (refused || body.subdirectory === "../../outside")
        return route.fulfill({
          status: refused ? 409 : 400,
          contentType: "application/json",
          body: JSON.stringify({
            error: {
              code: refused ? "revision_conflict" : "invalid_source",
              message: refused
                ? "Settings changed in another tab. Refresh before saving."
                : "The subdirectory must stay inside the build directory.",
            },
          }),
        })
      const next = { ...body }
      delete next.revision
      source = next
      revision += 1
      return json(route, configuration())
    }
    return route.fallback()
  })
  return {
    writes,
    sources,
    source: () => source,
    revision: () => revision,
    refuse: () => {
      refused = true
    },
  }
}

test("a recovered native source can attach future code without retaining the private snapshot handle", async ({
  page,
}) => {
  const fixture = await mockLocalProject(page, true)
  await page.goto("/deploy/7/settings/general")
  await expect(page.getByText(/verified source snapshot captured during import/)).toBeVisible()
  await page.getByRole("textbox", { name: "Subdirectory", exact: true }).fill("packages/server")
  await expect(page.getByRole("button", { name: "Save", exact: true })).toBeDisabled()
  await expect(
    page.getByText("Enter a build directory to attach new source.", { exact: true }),
  ).toBeVisible()
  expect(fixture.sources).toHaveLength(0)
  await page.getByRole("textbox", { name: "Build directory", exact: true }).fill("/srv/future-app")
  await saveSettings(page)
  await expect.poll(() => fixture.source().mode).toBe("local_directory")
  expect(fixture.source().resourceId).toBeUndefined()
  expect(fixture.source().localPath).toBe("/srv/future-app")
  expect(fixture.writes.filter((write) => !write.path.endsWith("/check"))).toEqual([
    { method: "PUT", path: "/deploy/7/environments/12/source" },
  ])
})

test("native build source saves a separate inspected directory as pending and preserves exclusions", async ({
  page,
}) => {
  const fixture = await mockLocalProject(page)
  await page.goto("/deploy/7/settings/general")
  await expect(page.getByText(/original source is retained for baseline rollback/)).toBeVisible()
  await expect(
    page.getByRole("list", { name: "Retained data excluded from builds" }),
  ).toContainText("uploads")
  await page
    .getByRole("textbox", { name: "Build directory", exact: true })
    .fill(" /srv/next-native-app ")
  await page.getByRole("textbox", { name: "Subdirectory", exact: true }).fill(" packages/server ")
  await saveSettings(page)
  await expect(page.getByText("1 saved change not live", { exact: true })).toBeVisible()
  await expect(page.getByRole("textbox", { name: "Build directory", exact: true })).toHaveValue(
    "/srv/next-native-app",
  )
  expect(fixture.sources).toEqual([
    {
      revision: 4,
      kind: "local",
      mode: "local_directory",
      localPath: "/srv/next-native-app",
      subdirectory: "packages/server",
      excludePaths: ["data", "uploads", "storage"],
      managedInPlace: false,
      platform: "linux/amd64",
    },
  ])
  expect(fixture.writes.filter((write) => !write.path.endsWith("/check"))).toEqual([
    { method: "PUT", path: "/deploy/7/environments/12/source" },
  ])
  expect(await page.evaluate(() => JSON.stringify(sessionStorage))).not.toContain(
    "/srv/next-native-app",
  )
  expect(await page.evaluate(() => JSON.stringify(sessionStorage))).not.toContain(
    "PRIVATE_API_TOKEN",
  )
  await page.screenshot({
    path: test.info().outputPath("native-source-pending.png"),
    fullPage: true,
  })
})

test("invalid native source keeps the current revision and unsaved input without changing runtime", async ({
  page,
}) => {
  const fixture = await mockLocalProject(page)
  await page.goto("/deploy/7/settings/general")
  await page
    .getByRole("textbox", { name: "Build directory", exact: true })
    .fill("/srv/next-native-app")
  const subdirectory = page.getByRole("textbox", { name: "Subdirectory", exact: true })
  await subdirectory.fill("../../outside")
  await saveSettings(page)
  await expect(
    page.getByRole("alert").getByText("The subdirectory must stay inside the build directory."),
  ).toBeVisible()
  await expect(subdirectory).toHaveValue("../../outside")
  expect(fixture.revision()).toBe(4)
  expect(fixture.source().localPath).toBe("/srv/original-native-app")
  await page.reload()
  await expect(subdirectory).toHaveValue("")
  await expect(page.getByRole("textbox", { name: "Build directory", exact: true })).toHaveValue(
    "/srv/original-native-app",
  )
  expect(fixture.writes.filter((write) => !write.path.endsWith("/check"))).toEqual([
    { method: "PUT", path: "/deploy/7/environments/12/source" },
  ])
})

test("a native source revision conflict retains the attempted directory and explains refresh", async ({
  page,
}) => {
  const fixture = await mockLocalProject(page)
  fixture.refuse()
  await page.goto("/deploy/7/settings/general")
  const directory = page.getByRole("textbox", { name: "Build directory", exact: true })
  await directory.fill("/srv/next-native-app")
  await saveSettings(page)
  await expect(
    page.getByRole("alert").getByText("Settings changed in another tab. Refresh before saving."),
  ).toBeVisible()
  await expect(directory).toHaveValue("/srv/next-native-app")
  expect(fixture.source().localPath).toBe("/srv/original-native-app")
  expect(fixture.revision()).toBe(4)
})

test("a reader sees native source and retained-data exclusions without source editing", async ({
  page,
}) => {
  const fixture = await mockLocalProject(page)
  await page.route("**/api/v1/auth/session", (route) =>
    json(route, { ...user, capabilities: ["read"] }),
  )
  await page.goto("/deploy/7/settings/general")
  await expect(page.getByRole("textbox", { name: "Build directory", exact: true })).toHaveAttribute(
    "readonly",
  )
  await expect(page.getByRole("textbox", { name: "Subdirectory", exact: true })).toHaveAttribute(
    "readonly",
  )
  await expect(
    page.getByRole("list", { name: "Retained data excluded from builds" }),
  ).toContainText("storage")
  await expect(page.getByRole("button", { name: "Save", exact: true })).toHaveCount(0)
  expect(fixture.writes).toEqual([])
})
