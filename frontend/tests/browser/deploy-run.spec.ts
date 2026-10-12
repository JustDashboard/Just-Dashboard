import { expect, test } from "@playwright/test"
import {
  deployment,
  json,
  mockProject,
  now,
  project,
  run as fixtureRun,
  steps as fixtureSteps,
} from "./deploy-fixture"
import type { DeploymentEngineRun, DeploymentStep } from "../../src/lib/types"

/**
 * The deployment (run) page: its own destination outside the project shell,
 * built around the event stream. Every scenario here mirrors a behaviour the
 * pre-rebuild `deploy-ui.spec.ts` protected on `deployment-run-workspace.tsx`
 * and `build-transcript.tsx` — the selectors changed with the redesign, the
 * behaviours did not.
 */

test.use({ timezoneId: "UTC" })

// The fixture's `run` and `steps` are plain object literals, typed loosely by
// inference (`state: string` rather than the narrower `DeploymentRunState`).
// Widening them once here, rather than at every call site, is what lets
// `snapshot()`'s overrides stay a real `Partial<DeploymentEngineRun>`.
const run = fixtureRun as DeploymentEngineRun
const steps = fixtureSteps as unknown as DeploymentStep[]

function snapshot(
  overrides: Partial<DeploymentEngineRun> = {},
  stepList: DeploymentStep[] = steps,
) {
  return { run: { ...run, ...overrides }, steps: stepList }
}

test("renders the run identity, facts and release path for an active run", async ({ page }) => {
  await mockProject(page)
  await page.routeWebSocket(/\/api\/v1\/deploy\/7\/runs\/84\/stream/, (socket) => {
    socket.send(JSON.stringify({ type: "snapshot", data: snapshot(), ts: Date.now() }))
  })
  await page.goto("/deploy/7/runs/84")

  await expect(page.getByRole("heading", { name: "Deployment #1" })).toBeVisible()
  const identity = page.locator('[data-slot="run-identity"]')
  await expect(identity.getByText("Verifying", { exact: true })).toBeVisible()
  // Nothing stands above the identity line: the run's verbs sit on it beside
  // the state, and the way back to the project is the rail's panel and the
  // menu's Open project rather than an eyebrow.
  await expect(page.locator("[data-slot=page-context]")).toHaveCount(0)
  await expect(
    identity.getByRole("button", { name: "More actions for Deployment #1", exact: true }),
  ).toBeVisible()

  // The identity line: the source as its forge, the repository as the title
  // (the run recorded no commit), and the run as a sentence of facts.
  await expect(identity.getByText("acme/api", { exact: true })).toBeVisible()
  await expect(page.getByText("Production", { exact: true })).toBeVisible()
  await expect(identity.getByText("Deploy", { exact: true })).toBeVisible()
  await expect(page.getByText("by operator", { exact: true })).toBeVisible()
  await expect(page.getByText(/Sep 0?3, 2026/)).toBeVisible()
  await expect(page.getByText("main", { exact: true })).toBeVisible()
  await expect(identity.getByText("Running for", { exact: true })).toBeVisible()

  const path = page.getByRole("list", { name: "Release path" })
  await expect(path).toBeVisible()
  // The fixture's run plans no certificate: that stage is not part of it,
  // rather than waiting on it for ever.
  await expect(path.getByText("Not part of this run", { exact: true })).toHaveCount(1)
  // The sequence on /deploy/new ended when the project was created: a first
  // run is read like any other, with no spine claiming the screens before it.
  await expect(page.getByRole("list", { name: "Progress" })).toHaveCount(0)
  // The path lights the stage at work; no second caption names it again.
  await expect(path.locator('[aria-current="step"]')).toContainText("Verify")
  await expect(page.getByRole("status")).toHaveCount(0)
})

test("the run's own frozen commit renders beside its branch, sha and subject", async ({ page }) => {
  await mockProject(page)
  const commit = {
    sourceRevision: "a12bc34d56ef7890a1",
    metadata: { commit: { sha: "a12bc34d56ef7890a1", subject: "Fix checkout" } },
  }
  await page.route("**/api/v1/deploy/7/runs/84", (route) => json(route, snapshot(commit)))
  await page.routeWebSocket(/\/api\/v1\/deploy\/7\/runs\/84\/stream/, (socket) => {
    socket.send(JSON.stringify({ type: "snapshot", data: snapshot(commit), ts: Date.now() }))
  })
  await page.goto("/deploy/7/runs/84")
  await expect(page.getByText("a12bc34", { exact: true })).toBeVisible()
  await expect(page.getByText("Fix checkout", { exact: true })).toBeVisible()
})

test("build transcript formats chunks, dedupes by sequence, filters and copies", async ({
  page,
}) => {
  await mockProject(page)
  await page.routeWebSocket(/\/api\/v1\/deploy\/7\/runs\/84\/stream/, (socket) => {
    socket.send(JSON.stringify({ type: "snapshot", data: snapshot(), ts: Date.now() }))
    const event = {
      seq: 20,
      type: "step.log",
      runId: 84,
      stepId: 105,
      ts: now,
      data: {
        stream: "stdout",
        text: "Installing dependencies\r\n[33mWarning: optional cache unavailable[0m\nCache restore failed; continuing\nBuild complete\n",
        truncated: false,
      },
    }
    // The same event sent twice (identical seq) exercises dedupe by sequence.
    socket.send(
      JSON.stringify({
        type: "events",
        data: [
          event,
          event,
          { ...event, seq: 21, stepId: 110, data: { ...event.data, text: "GET /health 200\n" } },
        ],
        ts: Date.now(),
      }),
    )
  })
  await page.goto("/deploy/7/runs/84")

  const transcript = page.getByRole("group", { name: "Deployment transcript" })
  await expect(transcript.getByRole("listitem")).toHaveCount(5)
  // Each step's lines are a list of their own, under a rule naming the step
  // as the engine's read model names it.
  await expect(transcript.getByRole("list", { name: "Build", exact: true })).toBeVisible()
  await expect(
    transcript.getByRole("list", { name: "Readiness checks", exact: true }),
  ).toBeVisible()
  await expect(transcript).not.toContainText("")
  await expect(page.getByRole("combobox", { name: "Build log stage" })).toHaveText("All stages")

  await page.getByRole("textbox", { name: "Search build logs" }).fill("warning")
  await expect(transcript.getByRole("listitem")).toHaveCount(1)
  await page.getByRole("textbox", { name: "Search build logs" }).fill("")

  // The chip carries its count: "Errors 1".
  const errors = page.getByRole("button", { name: /^Errors/ })
  await expect(errors).toHaveText(/Errors\s*1/)
  await errors.click()
  await expect(transcript.getByRole("listitem")).toHaveCount(1)
  await expect(transcript).toContainText("Cache restore failed; continuing")
  await errors.click()

  await page.context().grantPermissions(["clipboard-read", "clipboard-write"])
  await page.getByRole("button", { name: "Copy build logs" }).click()
  const copied = await page.evaluate(() => navigator.clipboard.readText())
  expect(copied.split("\n")).toHaveLength(5)
})

test("the transcript resumes from the last sequence after a disconnect", async ({ page }) => {
  await mockProject(page)
  const connections: string[] = []
  await page.routeWebSocket(/\/api\/v1\/deploy\/7\/runs\/84\/stream/, (socket) => {
    connections.push(socket.url())
    const after = Number(new URL(socket.url()).searchParams.get("after") ?? 0)
    socket.send(JSON.stringify({ type: "snapshot", data: snapshot(), ts: Date.now() }))
    if (after < 16) {
      socket.send(
        JSON.stringify({
          type: "events",
          data: [
            {
              seq: 16,
              type: "step.log",
              runId: 84,
              stepId: 110,
              ts: now,
              data: { stream: "stdout", text: "readiness attempt one\n", truncated: false },
            },
          ],
          ts: Date.now(),
        }),
      )
      setTimeout(() => void socket.close({ code: 1012, reason: "test reconnect" }), 100)
      return
    }
    socket.send(
      JSON.stringify({
        type: "events",
        data: [
          {
            seq: 17,
            type: "step.log",
            runId: 84,
            stepId: 110,
            ts: now,
            data: { stream: "stdout", text: "readiness recovered\n", truncated: false },
          },
        ],
        ts: Date.now(),
      }),
    )
  })

  await page.goto("/deploy/7/runs/84")
  const transcript = page.getByRole("group", { name: "Deployment transcript" })
  await expect(transcript.getByText("readiness attempt one", { exact: true })).toBeVisible()
  await expect(transcript.getByText("readiness recovered", { exact: true })).toBeVisible()
  await expect.poll(() => connections.length).toBeGreaterThanOrEqual(2)
  expect(new URL(connections.at(-1)!).searchParams.get("after")).toBe("16")
})

test("a resync event replaces the snapshot instead of merging into it", async ({ page }) => {
  await mockProject(page)
  await page.routeWebSocket(/\/api\/v1\/deploy\/7\/runs\/84\/stream/, (socket) => {
    socket.send(
      JSON.stringify({
        type: "snapshot",
        data: snapshot({ state: "running" }),
        ts: Date.now(),
      }),
    )
    const resyncedSteps = steps.map((step) => ({ ...step, state: "passed" as const }))
    socket.send(
      JSON.stringify({
        type: "events",
        data: [
          {
            seq: 200,
            type: "resync",
            runId: 84,
            ts: now,
            data: {
              oldestSeq: 200,
              snapshot: snapshot(
                { state: "succeeded", releaseId: 20, endedAt: now },
                resyncedSteps,
              ),
            },
          },
        ],
        ts: Date.now(),
      }),
    )
  })
  await page.goto("/deploy/7/runs/84")
  // Only reachable by replacing state wholesale: applyEvent's incremental path
  // has no case for "resync" and would have left the run "running".
  await expect(page.getByText("Your release is ready", { exact: true })).toBeVisible()
  const identity = page.locator('[data-slot="run-identity"]')
  await expect(identity.getByText("Ready", { exact: true })).toBeVisible()
})

test("Details shows the picked step's record, and only a step with output leads to the console", async ({
  page,
}) => {
  await mockProject(page)
  const withEvidence = steps.map((step) =>
    step.key === "backup_gate"
      ? { ...step, evidence: { reason: "no backup gate configured", routeRemoved: true } }
      : step,
  )
  await page.routeWebSocket(/\/api\/v1\/deploy\/7\/runs\/84\/stream/, (socket) => {
    socket.send(
      JSON.stringify({ type: "snapshot", data: snapshot({}, withEvidence), ts: Date.now() }),
    )
    socket.send(
      JSON.stringify({
        type: "events",
        data: [
          {
            seq: 30,
            type: "step.log",
            runId: 84,
            stepId: 111,
            ts: now,
            data: { stream: "stdout", text: "smoke: GET / 200\n", truncated: false },
          },
        ],
        ts: Date.now(),
      }),
    )
  })
  await page.goto("/deploy/7/runs/84")

  // No views to switch: the build logs and the details are both on the page.
  await expect(page.getByRole("group", { name: "Run views" })).toHaveCount(0)
  await expect(page.getByRole("heading", { name: "Build logs" })).toBeVisible()
  await expect(page.getByRole("heading", { name: "Details" })).toBeVisible()
  const list = page.getByRole("list", { name: "Deployment steps" })
  await expect(list).toBeVisible()
  await expect(page.getByText("15 steps · 8 passed · 1 skipped", { exact: true })).toBeVisible()
  // The rail is the release path read down: each stage the run includes.
  await expect(list.getByRole("list", { name: "Verify", exact: true })).toBeVisible()

  // It opens on the step at work.
  const inspector = page.getByRole("region", { name: "Readiness checks" })
  await expect(inspector.getByRole("heading", { name: "Readiness checks" })).toBeVisible()
  await expect(list.getByRole("button", { name: /Readiness checks/ })).toHaveAttribute(
    "aria-pressed",
    "true",
  )

  // The skipped gate's reason is its second line; picked, its evidence is facts.
  const gate = list.getByRole("button", { name: /Backup check/ })
  await expect(gate).toContainText("no backup gate configured")
  await gate.click()
  await expect(gate).toHaveAttribute("aria-pressed", "true")
  const record = page.getByRole("region", { name: "Backup check" })
  await expect(record.getByText("Route removed", { exact: true })).toBeVisible()
  await expect(record.getByText("Yes", { exact: true })).toBeVisible()
  // Nothing was written to the build log for it, so nothing offers the console.
  await expect(page.getByRole("button", { name: "Build output", exact: true })).toHaveCount(0)

  // A step that passed with no end recorded reads as done, not as a
  // duration measured to the present.
  await expect(list.getByRole("button", { name: /Resolve source/ })).toContainText("Done")

  const smoke = list.getByRole("button", { name: /Smoke checks/ })
  await smoke.focus()
  await page.keyboard.press("Enter")
  // The last lines it wrote stand in its record.
  await expect(
    page.getByRole("list", { name: "Smoke checks output" }).getByText("smoke: GET / 200"),
  ).toBeVisible()
  await page.getByRole("button", { name: "Build output", exact: true }).click()

  const stage = page.getByRole("combobox", { name: "Build log stage" })
  await expect(stage).toHaveText("Smoke checks")
  await expect(stage).toBeFocused()
  await expect(
    page.getByRole("group", { name: "Deployment transcript" }).getByText("smoke: GET / 200"),
  ).toBeVisible()
})

test("Details draws what a step recorded as what each thing is", async ({ page }) => {
  await mockProject(page)
  const prepared = steps.map((step) =>
    step.key === "prepare_context"
      ? {
          ...step,
          evidence: {
            buildRoot: "/var/lib/just-dashboard/deployment-workspaces/run-20/source",
            prepared: {
              method: "recipe",
              recipe: "node",
              nodeVersion: "22 (recipe default)",
              toolchain: "bun 1.3.11 (packageManager)",
              dockerfileDigest: `sha256:${"307f9ed5".repeat(8)}`,
              dockerfilePreview: "FROM node:22-alpine AS build\nWORKDIR /app\nRUN bun install\n",
              buildArgv: ["docker", "buildx", "build", "--progress=plain", "."],
              baseImages: [
                { reference: "oven/bun:1.3.11-alpine", digest: `sha256:${"7e".repeat(32)}` },
              ],
            },
          },
        }
      : step,
  )
  await page.routeWebSocket(/\/api\/v1\/deploy\/7\/runs\/84\/stream/, (socket) => {
    socket.send(JSON.stringify({ type: "snapshot", data: snapshot({}, prepared), ts: Date.now() }))
  })
  await page.goto("/deploy/7/runs/84")
  const list = page.getByRole("list", { name: "Deployment steps" })
  // The row says what the step concluded, and is drawn as the toolchain it chose.
  const row = list.getByRole("button", { name: /Prepare build context/ })
  await expect(row).toContainText("Node 22 · bun 1.3.11")
  await expect(row.locator('img[src="/logos/bun.svg"]')).toBeVisible()
  await row.click()

  const record = page.getByRole("region", { name: "Prepare build context" })
  // A version is drawn beside the product it is a version of.
  await expect(record.getByText("22 (recipe default)", { exact: true })).toBeVisible()
  // A digest is cut to what tells two apart, the whole of it a copy away.
  await expect(record.getByText("307f9ed5307f", { exact: true })).toBeVisible()
  await expect(record.getByRole("button", { name: "Copy dockerfile digest" })).toBeVisible()
  // The Dockerfile and the command are code, each with its own name.
  const dockerfile = record.locator("figure", { hasText: "Dockerfile" })
  await expect(dockerfile.getByText("WORKDIR", { exact: true })).toBeVisible()
  await expect(dockerfile.getByText("3 lines", { exact: true })).toBeVisible()
  const command = record.locator("figure", { hasText: "Command" })
  await expect(command.getByText("--progress=plain", { exact: true })).toBeVisible()
  // An image is its product.
  const images = record.getByRole("region", { name: "Base images" })
  await expect(images.getByText("oven/bun:1.3.11-alpine", { exact: true })).toBeVisible()

  // The record as it was kept stays one fold away.
  await record.getByText("Recorded evidence", { exact: true }).click()
  await expect(record.getByText('"buildRoot"', { exact: true })).toBeVisible()
})

test("a stage with no output says so instead of reporting a failed search", async ({ page }) => {
  await mockProject(page)
  await page.routeWebSocket(/\/api\/v1\/deploy\/7\/runs\/84\/stream/, (socket) => {
    socket.send(JSON.stringify({ type: "snapshot", data: snapshot(), ts: Date.now() }))
    socket.send(
      JSON.stringify({
        type: "events",
        data: [
          {
            seq: 31,
            type: "step.log",
            runId: 84,
            stepId: 105,
            ts: now,
            data: { stream: "stdout", text: "built\n", truncated: false },
          },
        ],
        ts: Date.now(),
      }),
    )
  })
  await page.goto("/deploy/7/runs/84")
  await page.getByRole("combobox", { name: "Build log stage" }).click()
  await page.getByRole("option", { name: "Check plan" }).click()
  await expect(page.getByText("Check plan wrote nothing to the build log.")).toBeVisible()
})

test("cancel requests cancellation and shows the toast and notice", async ({ page }) => {
  await mockProject(page)
  await page.route("**/api/v1/deploy/7/runs/84/cancel", (route) =>
    json(route, { ...run, state: "cancelling", cancelRequested: true }),
  )
  await page.routeWebSocket(/\/api\/v1\/deploy\/7\/runs\/84\/stream/, (socket) => {
    socket.send(JSON.stringify({ type: "snapshot", data: snapshot(), ts: Date.now() }))
  })
  await page.goto("/deploy/7/runs/84")

  await page.getByRole("button", { name: "Cancel", exact: true }).click()
  await expect(page.getByText("Cleanup progress remains visible on this page.")).toBeVisible()
  await expect(
    page.getByText("The current operation will stop safely and run its cleanup."),
  ).toBeVisible()
  const identity = page.locator('[data-slot="run-identity"]')
  await expect(identity.getByText("Cancelling", { exact: true })).toBeVisible()
})

test("retry navigates to the run it created", async ({ page }) => {
  await mockProject(page)
  await page.route("**/api/v1/deploy/7/runs/84", (route) =>
    json(route, snapshot({ state: "failed", terminalCode: "build_failed" })),
  )
  await page.routeWebSocket(/\/api\/v1\/deploy\/7\/runs\/84\/stream/, (socket) => {
    socket.send(
      JSON.stringify({
        type: "snapshot",
        data: snapshot({ state: "failed", terminalCode: "build_failed" }),
        ts: Date.now(),
      }),
    )
  })
  await page.goto("/deploy/7/runs/84")
  await page.getByRole("button", { name: "Retry", exact: true }).click()
  await expect(page).toHaveURL(/\/deploy\/7\/runs\/85$/)
})

test("redeploy starts a new run from a succeeded live release", async ({ page }) => {
  const dashboard = await mockProject(page)
  const succeeded = { state: "succeeded" as const, releaseId: 20, endedAt: now }
  await page.route("**/api/v1/deploy/7/runs/84", (route) => json(route, snapshot(succeeded)))
  await page.routeWebSocket(/\/api\/v1\/deploy\/7\/runs\/84\/stream/, (socket) => {
    socket.send(JSON.stringify({ type: "snapshot", data: snapshot(succeeded), ts: Date.now() }))
  })
  await page.goto("/deploy/7/runs/84")
  await page.getByRole("button", { name: "Redeploy", exact: true }).click()
  await expect(page).toHaveURL(/\/deploy\/7\/runs\/88$/)
  expect(dashboard.actions()).toEqual(["redeploy"])
})

test("a terminal reason renders as a danger notice", async ({ page }) => {
  await mockProject(page)
  const failed = {
    state: "failed" as const,
    terminalCode: "health_gate_failed",
    terminalReason: "could not connect after 20 attempts",
    endedAt: now,
  }
  await page.route("**/api/v1/deploy/7/runs/84", (route) => json(route, snapshot(failed)))
  await page.routeWebSocket(/\/api\/v1\/deploy\/7\/runs\/84\/stream/, (socket) => {
    socket.send(JSON.stringify({ type: "snapshot", data: snapshot(failed), ts: Date.now() }))
  })
  await page.goto("/deploy/7/runs/84")
  // The reader's words first, the engine's code beside them as a literal.
  await expect(page.getByText("Health gate failed", { exact: true })).toBeVisible()
  await expect(page.getByText("health_gate_failed", { exact: true })).toBeVisible()
  await expect(page.getByText("could not connect after 20 attempts", { exact: true })).toBeVisible()
})

test("a failure leads to the step that failed", async ({ page }) => {
  await mockProject(page)
  const failed = {
    state: "failed" as const,
    terminalCode: "build_failed",
    terminalReason: "bun run build exited with status 1",
    endedAt: now,
  }
  const failedSteps = steps.map((step) =>
    step.key === "build_artifact" ? { ...step, state: "failed" as const } : step,
  )
  await page.route("**/api/v1/deploy/7/runs/84", (route) =>
    json(route, snapshot(failed, failedSteps)),
  )
  await page.routeWebSocket(/\/api\/v1\/deploy\/7\/runs\/84\/stream/, (socket) => {
    socket.send(
      JSON.stringify({ type: "snapshot", data: snapshot(failed, failedSteps), ts: Date.now() }),
    )
    socket.send(
      JSON.stringify({
        type: "events",
        data: [
          {
            seq: 40,
            type: "step.log",
            runId: 84,
            stepId: 105,
            ts: now,
            data: {
              stream: "stderr",
              text: 'error: script "build" exited with code 1\n',
              truncated: false,
            },
          },
        ],
        ts: Date.now(),
      }),
    )
  })
  await page.goto("/deploy/7/runs/84")

  // What broke, where in the run, and the last lines the step wrote.
  const failure = page.getByRole("region", { name: "Why this deployment failed" })
  await expect(failure.getByText("Failed at", { exact: false })).toContainText("Build")
  await expect(failure.getByText("step 5 of 15", { exact: false })).toBeVisible()
  await expect(
    failure
      .getByRole("list", { name: "Last lines from Build" })
      .getByText('error: script "build" exited with code 1'),
  ).toBeVisible()

  // Details opens on it.
  await expect(page.getByRole("region", { name: "Build", exact: true })).toBeVisible()
  await expect(
    page.getByRole("list", { name: "Deployment steps" }).getByRole("button", { name: /^Build/ }),
  ).toHaveAttribute("aria-pressed", "true")

  await failure.getByRole("button", { name: "Show in build logs", exact: true }).click()
  await expect(page.getByRole("combobox", { name: "Build log stage" })).toHaveText("Build")
})

test("a rolled-back run names the release that stayed live then, not the one live now", async ({
  page,
}) => {
  await mockProject(page)
  // Its candidate was release #2, which replaced #1; #2 has gone live since
  // by another run, and the sentence is about this run's moment.
  const rolledBack = {
    state: "rolled_back" as const,
    candidateReleaseId: 20,
    terminalCode: "activation_failed",
    terminalReason: "The new release stopped answering during the switch.",
    endedAt: now,
  }
  await page.route("**/api/v1/deploy/7/runs/84", (route) => json(route, snapshot(rolledBack)))
  await page.routeWebSocket(/\/api\/v1\/deploy\/7\/runs\/84\/stream/, (socket) => {
    socket.send(JSON.stringify({ type: "snapshot", data: snapshot(rolledBack), ts: Date.now() }))
  })
  await page.goto("/deploy/7/runs/84")
  await expect(
    page.getByText("Rolled back — release #1 stayed live", { exact: true }),
  ).toBeVisible()
})

test("a cancelled run says why it stopped, and one never claimed was only queued", async ({
  page,
}) => {
  await mockProject(page)
  const cancelled = {
    state: "cancelled" as const,
    claimedAt: undefined,
    terminalReason: "Cancelled by operator",
    endedAt: "2026-09-03T12:00:40Z",
  }
  await page.route("**/api/v1/deploy/7/runs/84", (route) => json(route, snapshot(cancelled, [])))
  await page.routeWebSocket(/\/api\/v1\/deploy\/7\/runs\/84\/stream/, (socket) => {
    socket.send(JSON.stringify({ type: "snapshot", data: snapshot(cancelled, []), ts: Date.now() }))
  })
  await page.goto("/deploy/7/runs/84")
  await expect(page.getByText("Cancelled by operator", { exact: true })).toBeVisible()
  const identity = page.locator('[data-slot="run-identity"]')
  await expect(identity.getByText("Queued for", { exact: true })).toBeVisible()
  await expect(identity.getByText("never reached a build slot", { exact: true })).toBeVisible()
  await expect(identity.getByText("Took", { exact: true })).toHaveCount(0)
})

test("a finished run's release can be rolled back to from its own page", async ({ page }) => {
  await mockProject(page)
  const retained = { state: "succeeded" as const, releaseId: 19, endedAt: now }
  await page.route("**/api/v1/deploy/7/runs/84", (route) => json(route, snapshot(retained)))
  await page.routeWebSocket(/\/api\/v1\/deploy\/7\/runs\/84\/stream/, (socket) => {
    socket.send(JSON.stringify({ type: "snapshot", data: snapshot(retained), ts: Date.now() }))
  })
  await page.goto("/deploy/7/runs/84")
  await expect(page.getByText("Superseded — release #2 is live now", { exact: true })).toBeVisible()
  await page.getByRole("button", { name: "More actions for Deployment #1", exact: true }).click()
  await expect(page.getByRole("menuitem", { name: /Compare with live/ })).toBeVisible()
  await page.getByRole("menuitem", { name: /Roll back to this release/ }).click()
  const dialog = page.getByRole("dialog", { name: "Roll back to release #1" })
  await expect(dialog).toBeVisible()
  await expect(dialog.getByRole("button", { name: "Roll back", exact: true })).toBeEnabled()
})

test("visit and the ready block appear only when this run's release is the live one", async ({
  page,
}) => {
  await mockProject(page)
  const live = { state: "succeeded" as const, releaseId: 20, endedAt: now }
  await page.route("**/api/v1/deploy/7/runs/84", (route) => json(route, snapshot(live)))
  await page.routeWebSocket(/\/api\/v1\/deploy\/7\/runs\/84\/stream/, (socket) => {
    socket.send(JSON.stringify({ type: "snapshot", data: snapshot(live), ts: Date.now() }))
  })
  await page.goto("/deploy/7/runs/84")
  await expect(page.getByText("Your release is ready", { exact: true })).toBeVisible()
  await expect(page.getByRole("link", { name: "Visit", exact: true })).toHaveAttribute(
    "href",
    "https://api.example.test/",
  )

  // A run whose release has since been superseded by a newer one gets a
  // different story entirely, and no Visit anywhere on the page.
  const superseded = { state: "succeeded" as const, releaseId: 19, endedAt: now }
  await page.route("**/api/v1/deploy/7/runs/84", (route) => json(route, snapshot(superseded)))
  await page.routeWebSocket(/\/api\/v1\/deploy\/7\/runs\/84\/stream/, (socket) => {
    socket.send(JSON.stringify({ type: "snapshot", data: snapshot(superseded), ts: Date.now() }))
  })
  await page.reload()
  await expect(page.getByText("Superseded — release #2 is live now", { exact: true })).toBeVisible()
  await expect(page.getByRole("link", { name: "Open project", exact: true })).toHaveAttribute(
    "href",
    "/deploy/7",
  )
  await expect(page.getByRole("link", { name: "Visit", exact: true })).toHaveCount(0)
})

test("a preview's run is live by its own environment and visited at the preview's address", async ({
  page,
}) => {
  await mockProject(page)
  const address = "https://host.tailnet.ts.net:21000"
  const previewRun = { state: "succeeded" as const, environmentId: 40, releaseId: 61, endedAt: now }
  await page.route("**/api/v1/deploy/7/runs/84", (route) => json(route, snapshot(previewRun)))
  await page.routeWebSocket(/\/api\/v1\/deploy\/7\/runs\/84\/stream/, (socket) => {
    socket.send(JSON.stringify({ type: "snapshot", data: snapshot(previewRun), ts: Date.now() }))
  })
  await page.route("**/api/v1/deploy/7/environments/40/releases*", (route) =>
    json(route, [
      {
        id: 61,
        projectId: 7,
        environmentId: 40,
        number: 1,
        runId: 84,
        state: "live",
        planRevision: 2,
        sourceRevision: "b1f8547",
        strategy: "blue_green",
        expectedDowntime: false,
        createdAt: now,
        activatedAt: now,
        pinned: false,
      },
    ]),
  )
  await page.route("**/api/v1/deploy/7/previews", (route) =>
    json(route, [
      {
        id: 3,
        triggerId: 5,
        providerRef: "4",
        environmentId: 40,
        environmentSlug: "pr-4",
        state: "open",
        updatedAt: now,
        number: 4,
        liveReleaseId: 61,
        address: { kind: "tailnet", url: address, port: 21000, published: true },
      },
    ]),
  )
  await page.goto("/deploy/7/runs/84")
  await expect(page.getByText("Your release is ready", { exact: true })).toBeVisible()
  await expect(page.getByText(/Superseded/)).toHaveCount(0)
  await expect(page.getByRole("link", { name: "Visit", exact: true })).toHaveAttribute(
    "href",
    address,
  )
  await expect(page.getByRole("link", { name: address })).toBeVisible()
  // Redeploy would enqueue on production's terms; a preview is tested again from its pull request.
  await expect(page.getByRole("button", { name: "Redeploy" })).toHaveCount(0)
  await expect(page.locator('[data-slot="run-identity"]').getByText("PR 4")).toBeVisible()
})

test("a Stop that succeeded reads as stopped and offers Start, not as a deploy that went live", async ({
  page,
}, testInfo) => {
  await mockProject(page)
  const stop: Partial<DeploymentEngineRun> = {
    operation: "stop",
    state: "succeeded",
    releaseId: 20,
    slotClass: "light",
    endedAt: now,
    metadata: { targetReleaseId: 20 },
  }
  // A Stop plans three steps, the first of them start_candidate against the live release.
  const stopSteps = steps
    .filter((step) => ["start_candidate", "record_release", "notify"].includes(step.key))
    .map((step) => ({ ...step, state: "passed" as const, endedAt: now }))
  let readProject = { stopped: true, lastRun: { ...run, ...stop } }
  await page.route("**/api/v1/deploy/7", (route) =>
    route.request().method() === "GET"
      ? json(route, {
          project,
          running: false,
          deployment: { ...deployment, activeRun: undefined, ...readProject },
        })
      : route.fallback(),
  )
  await page.route("**/api/v1/deploy/7/runs/84", (route) => json(route, snapshot(stop, stopSteps)))
  await page.routeWebSocket(/\/api\/v1\/deploy\/7\/runs\/84\/stream/, (socket) => {
    socket.send(
      JSON.stringify({ type: "snapshot", data: snapshot(stop, stopSteps), ts: Date.now() }),
    )
  })
  await page.setViewportSize({ width: 1280, height: 900 })
  await page.goto("/deploy/7/runs/84")

  const identity = page.locator('[data-slot="run-identity"]')
  await expect(page.getByText("Your release is stopped", { exact: true })).toBeVisible()
  await expect(page.getByText("Your release is ready", { exact: true })).toHaveCount(0)
  await expect(identity.getByText("Stopped", { exact: true })).toBeVisible()
  await expect(identity.getByText("Ready", { exact: true })).toHaveCount(0)
  // The Stop's target is the live release it stopped, not one it rolls back to.
  await expect(page.getByText(/rolls back to/)).toHaveCount(0)
  const path = page.getByRole("list", { name: "Release path" })
  await expect(path.getByText("Stop", { exact: true })).toBeVisible()
  await expect(path.getByText("Start", { exact: true })).toHaveCount(0)
  await expect(page.getByRole("button", { name: "Redeploy", exact: true })).toHaveCount(0)
  await expect(page.getByRole("link", { name: "Visit", exact: true })).toHaveCount(0)
  await page.screenshot({ path: testInfo.outputPath("run-stopped.png") })

  const details = page.getByRole("list", { name: "Deployment steps" })
  await expect(details.getByText("Stop live release", { exact: true })).toBeVisible()
  await expect(page.getByText("Start new release", { exact: true })).toHaveCount(0)

  // Start is the answer to Stopped, and it asks for a start run.
  const started = page.waitForRequest(
    (request) =>
      request.method() === "POST" &&
      request.url().endsWith("/api/v1/deploy/7/environments/12/runs"),
  )
  await page.getByRole("button", { name: "Start", exact: true }).click()
  expect((await started).postDataJSON()).toEqual({ operation: "start" })
  await expect(page).toHaveURL(/\/deploy\/7\/runs\/88$/)

  // Once a newer run has started it again, the Stop says so rather than
  // "stopped" for ever, and offers no Start.
  readProject = { stopped: false, lastRun: { ...run, ...stop, id: 88, operation: "start" } }
  await page.goto("/deploy/7/runs/84")
  await expect(
    page.getByText("Started again since — release #2 is running", { exact: true }),
  ).toBeVisible()
  await expect(page.getByRole("button", { name: "Start", exact: true })).toHaveCount(0)
})

test("cancel disappears once activation begins, and a rolled-back run offers no retry", async ({
  page,
}) => {
  await mockProject(page)
  await page.route("**/api/v1/deploy/7/runs/84", (route) =>
    json(route, snapshot({ state: "activating" })),
  )
  await page.routeWebSocket(/\/api\/v1\/deploy\/7\/runs\/84\/stream/, (socket) => {
    socket.send(
      JSON.stringify({ type: "snapshot", data: snapshot({ state: "activating" }), ts: Date.now() }),
    )
  })
  await page.goto("/deploy/7/runs/84")
  await expect(page.getByRole("heading", { name: "Deployment #1" })).toBeVisible()
  // The engine answers 409 run_not_cancellable once activation starts;
  // isCancellable withholds the button rather than offer a press that fails.
  await expect(page.getByRole("button", { name: "Cancel", exact: true })).toHaveCount(0)

  await page.route("**/api/v1/deploy/7/runs/84", (route) =>
    json(route, snapshot({ state: "rolled_back" })),
  )
  await page.routeWebSocket(/\/api\/v1\/deploy\/7\/runs\/84\/stream/, (socket) => {
    socket.send(
      JSON.stringify({
        type: "snapshot",
        data: snapshot({ state: "rolled_back" }),
        ts: Date.now(),
      }),
    )
  })
  await page.reload()
  await expect(page.getByRole("heading", { name: "Deployment #1" })).toBeVisible()
  await expect(page.getByRole("button", { name: "Cancel", exact: true })).toHaveCount(0)
  await expect(page.getByRole("button", { name: "Retry", exact: true })).toHaveCount(0)
})

test("the success block waits for the project read, so it never flashes Superseded first", async ({
  page,
}) => {
  await mockProject(page)
  const live = { state: "succeeded" as const, releaseId: 20, endedAt: now }
  await page.route("**/api/v1/deploy/7/runs/84", (route) => json(route, snapshot(live)))
  await page.routeWebSocket(/\/api\/v1\/deploy\/7\/runs\/84\/stream/, (socket) => {
    socket.send(JSON.stringify({ type: "snapshot", data: snapshot(live), ts: Date.now() }))
  })
  // A deterministic gate rather than a timer: held open until the assertions
  // below have already checked the page while `project.data` is undefined.
  let releaseProject: () => void = () => {}
  const gate = new Promise<void>((resolve) => {
    releaseProject = resolve
  })
  await page.route("**/api/v1/deploy/7", async (route) => {
    if (route.request().method() !== "GET") return route.fallback()
    await gate
    await route.fallback()
  })

  await page.goto("/deploy/7/runs/84")
  await expect(page.getByRole("heading", { name: "Deployment #1" })).toBeVisible()
  await expect(page.getByText("Superseded", { exact: false })).toHaveCount(0)
  await expect(page.getByText("Your release is ready", { exact: true })).toHaveCount(0)

  releaseProject()
  await expect(page.getByText("Your release is ready", { exact: true })).toBeVisible()
})

test("is responsive from 390 to 1280 wide with no sideways scroll", async ({ page }, testInfo) => {
  await mockProject(page)
  await page.routeWebSocket(/\/api\/v1\/deploy\/7\/runs\/84\/stream/, (socket) => {
    socket.send(JSON.stringify({ type: "snapshot", data: snapshot(), ts: Date.now() }))
  })
  await page.setViewportSize({ width: 1280, height: 1000 })
  await page.goto("/deploy/7/runs/84")
  await expect(page.getByRole("combobox", { name: "Build log stage" })).toBeVisible()
  await page.screenshot({ path: testInfo.outputPath("run-desktop.png"), fullPage: true })

  await page.setViewportSize({ width: 390, height: 900 })
  await expect(page.getByRole("heading", { name: "Deployment #1" })).toBeVisible()
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
  await page.screenshot({ path: testInfo.outputPath("run-mobile.png"), fullPage: true })

  await page.getByRole("list", { name: "Deployment steps" }).getByRole("button").first().click()
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
})
