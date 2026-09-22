import { expect, test } from "@playwright/test"
import { json, mockDraftJourney, now } from "./deploy-fixture"

/**
 * Regressions found in review, on the parts of the new-project flow that
 * `?draft=` resumes straight into Configure: a stage that fails `/preflight`
 * used to strand the draft on its old revision, and an ambiguous detection
 * had no control to resolve it from. `deploy-new.spec.ts` (not owned here)
 * keeps the rest of the source-picking and configure behaviour; these two
 * scenarios are deliberately built from a resumed draft rather than a picked
 * repository so each is a minimal, self-contained fixture.
 */

const configuration = {
  build: {
    method: "recipe",
    recipe: "node",
    buildCommand: "bun run build",
    startCommand: "bun start",
    secrets: [],
    releaseTasks: [],
  },
  runtime: {
    image: "",
    command: [],
    internalPort: 3000,
    hostPort: 0,
    bindAddress: "127.0.0.1",
    strategy: "blue_green",
    privileged: false,
    hostNetwork: false,
    capabilities: [],
    devices: [],
    mounts: [],
  },
  variables: [],
  dependencies: [],
  checks: [],
  domains: [],
}

test.describe("A stage that fails /preflight", () => {
  test("a second Deploy press does not 409 on a stale revision", async ({ page }) => {
    await mockDraftJourney(page)
    let revision = 5
    let saveCount = 0
    let preflightCount = 0
    const draftPayload = () => ({
      id: "fail-preflight-draft",
      ownerUsername: "operator",
      currentStep: "configuration",
      revision,
      data: {
        intent: { name: "preflight-test", profile: "web" },
        source: {
          kind: "git",
          mode: "git_url",
          url: "https://github.com/Wayy01/preflight-test.git",
          ref: "main",
        },
        configuration,
        detection: {
          source: {
            kind: "git",
            remote: "https://github.com/Wayy01/preflight-test.git",
            ref: "main",
          },
          candidates: [
            {
              id: "web-candidate",
              name: "Next.js web application",
              root: "",
              profile: "web",
              buildMethod: "recipe",
              recipe: "node",
              confidence: "high",
              framework: "Next.js",
            },
          ],
          selectedId: "web-candidate",
          scannedFiles: 5,
          scannedBytes: 512,
          truncated: false,
          gitRequirements: { submodules: false, lfs: false },
        },
      },
      findings: [],
      planPreview: "",
      updatedAt: now,
      expiresAt: "2026-09-04T12:00:00Z",
    })

    await page.route("**/api/v1/deploy/drafts/fail-preflight-draft", async (route) => {
      const method = route.request().method()
      if (method === "GET") return json(route, draftPayload())
      if (method === "PUT") {
        const body = route.request().postDataJSON() as { revision: number }
        if (body.revision !== revision) {
          await route.fulfill({
            status: 409,
            contentType: "application/json",
            body: JSON.stringify({
              error: { code: "draft_revision_conflict", message: "This draft changed elsewhere." },
            }),
          })
          return
        }
        saveCount += 1
        revision += 1
        return json(route, draftPayload())
      }
      return route.fallback()
    })
    await page.route("**/api/v1/deploy/drafts/fail-preflight-draft/preflight", async (route) => {
      preflightCount += 1
      if (preflightCount === 1) {
        await route.fulfill({
          status: 500,
          contentType: "application/json",
          body: JSON.stringify({ error: { code: "internal", message: "preflight exploded" } }),
        })
        return
      }
      return json(route, {
        draft: draftPayload(),
        preflight: {
          revision,
          findings: [],
          expectedDowntime: false,
          preview: "source -> build -> verify -> route",
          digest: `sha256:${"a".repeat(64)}`,
        },
      })
    })
    await page.route("**/api/v1/deploy/drafts/fail-preflight-draft/commit", (route) =>
      json(route, { projectId: 501, environmentId: 502, planRevision: revision, created: true }),
    )
    await page.route("**/api/v1/deploy/501/environments/502/runs", (route) =>
      json(route, { id: 999, state: "queued" }),
    )

    await page.goto("/deploy/new?draft=fail-preflight-draft")
    // Everything this draft asks was already answered, so it resumes on the
    // last of the four configure screens with Deploy under the plan.
    await expect(page.getByRole("heading", { name: "Ready to deploy?" })).toBeVisible()
    const deploy = page.getByRole("button", { name: "Deploy", exact: true })

    // Arriving checks the plan: the save succeeds (revision 5 -> 6) but
    // preflight 500s — and a failed check is not asked again on its own.
    await expect(page.getByText("preflight exploded")).toBeVisible()
    await page.waitForTimeout(500)
    expect(saveCount).toBe(1)
    expect(preflightCount).toBe(1)

    // Deploy must save with the revision the failed check already produced,
    // not the one the draft opened with — otherwise this 409s.
    await deploy.click()
    await page.waitForURL(/\/deploy\/501\/runs\/999$/)
    expect(saveCount).toBe(2)
  })
})

test.describe("An ambiguous detection", () => {
  test("picking a candidate re-detects with its id", async ({ page }) => {
    await mockDraftJourney(page)
    const candidates = [
      {
        id: "web-candidate",
        name: "Next.js web application",
        root: "",
        profile: "web",
        buildMethod: "recipe",
        recipe: "node",
        confidence: "high",
        framework: "Next.js",
      },
      {
        id: "worker-candidate",
        name: "Background worker",
        root: "worker",
        profile: "worker",
        buildMethod: "recipe",
        recipe: "node",
        confidence: "medium",
      },
    ]
    const draftPayload = (selectedId: string, revision: number) => ({
      id: "candidate-draft",
      ownerUsername: "operator",
      currentStep: "configuration",
      revision,
      data: {
        intent: { name: "ambiguous-app", profile: "web" },
        source: {
          kind: "git",
          mode: "git_url",
          url: "https://github.com/Wayy01/ambiguous.git",
          ref: "main",
        },
        configuration,
        detection: {
          source: { kind: "git", remote: "https://github.com/Wayy01/ambiguous.git", ref: "main" },
          candidates,
          selectedId,
          scannedFiles: 6,
          scannedBytes: 512,
          truncated: false,
          gitRequirements: { submodules: false, lfs: false },
        },
      },
      findings: [],
      planPreview: "",
      updatedAt: now,
      expiresAt: "2026-09-04T12:00:00Z",
    })
    const detectBodies: { selectedId?: string }[] = []

    await page.route("**/api/v1/deploy/drafts/candidate-draft", async (route) => {
      if (route.request().method() !== "GET") return route.fallback()
      return json(route, draftPayload("", 4))
    })
    await page.route("**/api/v1/deploy/drafts/candidate-draft/detect", async (route) => {
      const body = route.request().postDataJSON() as { selectedId?: string }
      detectBodies.push(body)
      return json(route, draftPayload(body.selectedId ?? "", 5))
    })

    await page.goto("/deploy/new?draft=candidate-draft")
    // Two equally strong candidates is a question only the operator can
    // answer, so the sequence opens on the screen that asks it.
    await expect(page.getByRole("heading", { name: "What are you building?" })).toBeVisible()
    const picker = page.getByRole("group", { name: "Detected candidates" })
    await expect(picker.getByText("Next.js web application")).toBeVisible()
    await expect(picker.getByText("Background worker")).toBeVisible()
    await expect(picker.getByRole("switch", { name: /Background worker/ })).not.toBeChecked()

    await picker.getByRole("switch", { name: /Background worker/ }).click()

    await expect.poll(() => detectBodies.at(-1)?.selectedId).toBe("worker-candidate")
    await expect(picker.getByRole("switch", { name: /Background worker/ })).toBeChecked()
  })
})
