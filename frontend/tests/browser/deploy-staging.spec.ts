import { expect, test, type Page, type Route } from "@playwright/test"
import { deployment, mockProject, now, project } from "./deploy-fixture"

/**
 * Staging and promotion over the mocked project: adding staging from the
 * header, the switch that every page then reads through, the Promote
 * confirmation with the image's own id and production's configuration
 * difference, a production that takes only promotions, and the release a
 * promotion made being marked as one.
 */

const imageId = `sha256:${"ab".repeat(32)}`
const production = {
  id: 12,
  name: "Production",
  slug: "production",
  kind: "production",
  desiredRevision: 3,
  liveReleaseId: 20,
  createdAt: now,
}
const staging = {
  id: 13,
  name: "Staging",
  slug: "staging",
  kind: "staging",
  desiredRevision: 1,
  liveReleaseId: 31,
  createdAt: now,
}
const stagingRun = {
  id: 96,
  runNumber: 6,
  projectId: 7,
  environmentId: 13,
  state: "succeeded",
  operation: "deploy",
  trigger: "git_push",
  actor: "git-monitor",
  requestedAt: now,
  endedAt: now,
  cancelRequested: false,
  planRevision: 1,
  priority: 500,
  slotClass: "heavy",
  releaseId: 31,
  sourceRevision: "f00dbabe1234567890abcdef1234567890abcdef",
  metadata: {
    commit: { sha: "f00dbabe1234567890abcdef1234567890abcdef", subject: "Faster checkout" },
  },
}
const stagingRelease = {
  id: 31,
  projectId: 7,
  environmentId: 13,
  number: 2,
  runId: 96,
  state: "live",
  planRevision: 1,
  sourceRevision: stagingRun.sourceRevision,
  imageDigest: imageId,
  configDigest: `sha256:${"c".repeat(64)}`,
  variablesDigest: `sha256:${"d".repeat(64)}`,
  strategy: "blue_green",
  expectedDowntime: false,
  createdAt: now,
  activatedAt: now,
  pinned: false,
}

const promotion = {
  environmentId: 13,
  environmentName: "Staging",
  releaseId: 31,
  releaseNumber: 2,
  runId: 96,
  imageId,
  imageDigest: imageId,
}
const promotionRun = {
  ...stagingRun,
  id: 97,
  runNumber: 7,
  environmentId: 12,
  operation: "promote",
  trigger: "manual",
  actor: "operator",
  releaseId: 20,
  metadata: { promotion },
}

function json(route: Route, body: unknown, status = 200) {
  return route.fulfill({ status, contentType: "application/json", body: JSON.stringify(body) })
}

/** The staging half of the project, layered over the fixture's production. */
async function mockStaging(
  page: Page,
  options: {
    exists?: boolean
    promotionOnly?: boolean
    promoted?: boolean
    idle?: boolean
    refused?: boolean
  } = {},
) {
  let exists = options.exists ?? true
  const requests: { method: string; path: string; body?: unknown }[] = []
  await page.route("**/api/v1/deploy/7**", async (route) => {
    const request = route.request()
    const url = new URL(request.url())
    const path = url.pathname.replace(/^\/api\/v1/, "")
    const method = request.method()
    if (method !== "GET") requests.push({ method, path, body: request.postDataJSON() })
    if (path === "/deploy/7/environments" && method === "GET") {
      return json(route, [
        { ...production, promotionOnly: options.promotionOnly },
        ...(exists ? [staging] : []),
      ])
    }
    if (path === "/deploy/7/environments" && method === "POST") {
      exists = true
      return json(
        route,
        {
          environment: staging,
          copied: ["NODE_ENV"],
          skipped: [
            {
              name: "API_TOKEN",
              kind: "variable",
              reason: "A secret: production's value stays in production.",
            },
            {
              name: "database_connection 4",
              kind: "database",
              reason: "Production's database: staging's release tasks would migrate it.",
            },
          ],
          adjusted: ["/data mounts a new, empty volume jd-staging-e13-1a2b instead of api-data."],
        },
        201,
      )
    }
    if (path === "/deploy/7" && url.searchParams.get("environment") === "13") {
      return json(route, {
        project,
        running: false,
        deployment: {
          ...deployment,
          environmentId: 13,
          environmentName: "Staging",
          environmentKind: "staging",
          desiredRevision: 1,
          liveReleaseId: 31,
          livePlanRevision: 1,
          pendingChanges: false,
          sourceRef: "staging",
          endpoint: "https://staging.api.example.test",
          activeRun: undefined,
          lastRun: stagingRun,
        },
      })
    }
    if (path === "/deploy/7" && !url.searchParams.has("environment") && options.idle) {
      return json(route, {
        project,
        running: false,
        deployment: { ...deployment, activeRun: undefined, pendingChanges: false },
      })
    }
    if (path === "/deploy/7/runs" && !url.searchParams.has("environment") && options.promoted) {
      return json(route, { runs: [promotionRun], running: false })
    }
    if (path === "/deploy/7/runs" && url.searchParams.get("environment") === "13") {
      return json(route, { runs: [stagingRun], running: false })
    }
    if (path === "/deploy/7/environments/13/releases") return json(route, [stagingRelease])
    if (path === "/deploy/7/environments/12/releases" && options.promoted) {
      return json(route, [
        {
          ...stagingRelease,
          id: 20,
          environmentId: 12,
          number: 20,
          runId: 97,
          promotion,
        },
      ])
    }
    if (path === "/deploy/7/environments/12/promotion" && options.refused) {
      return json(
        route,
        {
          error: {
            code: "promotion_source",
            message: "only a successful, retained staging release can be promoted",
          },
        },
        409,
      )
    }
    if (path === "/deploy/7/environments/12/promotion") {
      return json(route, {
        source: {
          environmentId: 13,
          environmentName: "Staging",
          release: stagingRelease,
          imageId,
          imageDigest: imageId,
          imageSizeBytes: 187_000_000,
          imagePresent: true,
        },
        target: {
          environmentId: 12,
          environmentName: "Production",
          liveRelease: { ...stagingRelease, id: 20, environmentId: 12, number: 20 },
          desiredRevision: 3,
          activeRuns: 0,
          promotionOnly: false,
          pendingChanges: [],
          releaseTasks: ["migrate"],
        },
        detail: {
          status: "available",
          fields: [
            { field: "image", from: "same", to: "same", changed: false },
            { field: "memory limit", from: "256 MiB", to: "1024 MiB", changed: true },
          ],
          variables: [
            { name: "API_TOKEN", change: "added", secret: true },
            { name: "DATABASE_URL", change: "changed", secret: true },
            { name: "NODE_ENV", change: "unchanged", secret: true },
          ],
          dependencies: [],
          checks: [],
          domains: [{ name: "api.example.test", change: "added" }],
        },
        blockers: [],
      })
    }
    if (path === "/deploy/7/environments/12/promote" && method === "POST") {
      return json(route, { ...stagingRun, id: 99, environmentId: 12, operation: "promote" }, 202)
    }
    return route.fallback()
  })
  return requests
}

test("staging is added from the header, read through the switch and promoted with its image id", async ({
  page,
}) => {
  await mockProject(page)
  const requests = await mockStaging(page, { exists: false })
  await page.goto("/deploy/7")

  await page.getByRole("button", { name: "Deployment actions" }).click()
  await page.getByRole("menuitem", { name: "Add staging environment…" }).click()
  const add = page.getByRole("dialog", { name: "Add a staging environment" })
  await expect(add.getByText("Database links: link a database of staging's own")).toBeVisible()
  await add.getByLabel("Domain").fill("staging.api.example.test")
  await add.getByRole("button", { name: "Add staging" }).click()
  await expect(page.getByRole("dialog", { name: "Staging is ready" })).toBeVisible()
  expect(requests.find((entry) => entry.path === "/deploy/7/environments")?.body).toEqual({
    kind: "staging",
    domain: "staging.api.example.test",
    copySecrets: false,
  })
  await expect(page.getByText("API_TOKEN")).toBeVisible()
  await page.getByRole("button", { name: "Open staging" }).click()

  const environment = page.getByRole("radiogroup", { name: "Environment" })
  await expect(environment.getByRole("radio", { name: "Staging" })).toBeChecked()
  await expect(page.getByText("staging.api.example.test").first()).toBeVisible()

  await page.getByRole("button", { name: "Promote", exact: true }).click()
  const confirm = page.getByRole("dialog", { name: "Promote staging release #2" })
  await expect(confirm.getByText(imageId)).toBeVisible()
  await expect(confirm.getByRole("listitem").filter({ hasText: "API_TOKEN" })).toContainText(
    "only in production",
  )
  await expect(confirm.getByText("a different value")).toBeVisible()
  await expect(confirm.getByText("Production's release tasks run first")).toBeVisible()
  await expect(confirm.getByText("NODE_ENV")).toHaveCount(0)
  await confirm.getByRole("button", { name: "Promote to production" }).click()
  await expect(page).toHaveURL(/\/deploy\/7\/runs\/99$/)
  expect(requests.find((entry) => entry.path.endsWith("/promote"))?.body).toEqual({ releaseId: 31 })
})

test("a production that takes only promotions offers Promote where it offered Deploy", async ({
  page,
}) => {
  await mockProject(page)
  await mockStaging(page, { promotionOnly: true, idle: true })
  await page.goto("/deploy/7")
  const header = page.locator('[data-slot="project-header"]')
  await expect(header.getByRole("button", { name: "Promote from staging…" })).toBeVisible()
  await expect(header.getByRole("button", { name: /^Deploy( changes)?$/ })).toHaveCount(0)
})

test("a release production took from staging is marked as promoted", async ({ page }) => {
  await mockProject(page)
  await mockStaging(page, { promoted: true, idle: true })
  await page.goto("/deploy/7/deployments")
  await expect(page.getByText("#7 Promote")).toBeVisible()
  await expect(page.getByText("From staging #2").first()).toBeVisible()
})

test("a staging release production cannot take says why instead of waiting", async ({ page }) => {
  await mockProject(page)
  await mockStaging(page, { refused: true })
  await page.goto("/deploy/7?env=staging")
  await page.getByRole("button", { name: "Promote", exact: true }).click()
  const confirm = page.getByRole("dialog", { name: /^Promote/ })
  await expect(confirm.getByText("only a successful, retained staging release")).toBeVisible()
  await expect(confirm.getByRole("button", { name: "Promote to production" })).toBeDisabled()
})
