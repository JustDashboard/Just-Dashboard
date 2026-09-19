import { expect, test } from "@playwright/test"
import { json, mockProject, now, run as fixtureRun, steps as fixtureSteps } from "./deploy-fixture"
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

test("renders the header, facts and release path for an active run", async ({ page }) => {
  await mockProject(page)
  await page.routeWebSocket(/\/api\/v1\/deploy\/7\/runs\/84\/stream/, (socket) => {
    socket.send(JSON.stringify({ type: "snapshot", data: snapshot(), ts: Date.now() }))
  })
  await page.goto("/deploy/7/runs/84")

  await expect(page.getByRole("heading", { name: "Deployment #1" })).toBeVisible()
  const header = page.locator('[data-slot="page-header"]')
  await expect(header.getByText("Verifying", { exact: true })).toBeVisible()
  await expect(page.getByRole("link", { name: /api-production/ })).toHaveAttribute(
    "href",
    "/deploy/7",
  )

  await expect(page.getByText("Production", { exact: true })).toBeVisible()
  await expect(page.getByText("Deploy", { exact: true })).toBeVisible()
  await expect(page.getByText("by operator", { exact: true })).toBeVisible()
  await expect(page.getByText(/Sep 0?3, 2026/)).toBeVisible()
  await expect(page.getByText("main", { exact: true })).toBeVisible()

  await expect(page.getByRole("list", { name: "Release path" })).toBeVisible()
  await expect(page.getByRole("status")).toHaveText("Verify Readiness…")
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

  const transcript = page.getByRole("list", { name: "Deployment transcript" })
  await expect(transcript.getByRole("listitem")).toHaveCount(5)
  await expect(transcript).not.toContainText("")
  await expect(page.getByRole("combobox", { name: "Build log stage" })).toHaveText("All stages")

  await page.getByRole("textbox", { name: "Search build logs" }).fill("warning")
  await expect(transcript.getByRole("listitem")).toHaveCount(1)
  await page.getByRole("textbox", { name: "Search build logs" }).fill("")

  await page.getByRole("button", { name: "Errors", exact: true }).click()
  await expect(transcript.getByRole("listitem")).toHaveCount(1)
  await expect(transcript).toContainText("Cache restore failed; continuing")
  await page.getByRole("button", { name: "Errors", exact: true }).click()

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
  await expect(page.getByText("readiness attempt one", { exact: true })).toBeVisible()
  await expect(page.getByText("readiness recovered", { exact: true })).toBeVisible()
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
  const header = page.locator('[data-slot="page-header"]')
  await expect(header.getByText("Ready", { exact: true })).toBeVisible()
})

test("a step in Details opens to its evidence, and only a step with output leads to the console", async ({
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

  const views = page.getByRole("group", { name: "Run views" })
  await views.getByRole("button", { name: "Details", exact: true }).click()
  const list = page.getByRole("list", { name: "Deployment steps" })
  await expect(list).toBeVisible()
  await expect(page.getByText("15 steps · 8 passed · 1 skipped", { exact: true })).toBeVisible()

  // The skipped gate's reason is its second line; open, its evidence is rows.
  const gate = page.getByRole("button", { name: /Backup Gate/ })
  await expect(gate).toContainText("no backup gate configured")
  await gate.click()
  await expect(page.getByText("Route removed", { exact: true })).toBeVisible()
  await expect(page.getByText("yes", { exact: true })).toBeVisible()
  // Nothing was written to the build log for it, so nothing offers the console.
  await expect(page.getByRole("button", { name: "Build output", exact: true })).toHaveCount(0)

  const smoke = page.getByRole("button", { name: /Verify Smoke/ })
  await smoke.focus()
  await page.keyboard.press("Enter")
  await page.getByRole("button", { name: "Build output", exact: true }).click()

  await expect(views.getByRole("button", { name: "Build logs", exact: true })).toHaveAttribute(
    "aria-pressed",
    "true",
  )
  const stage = page.getByRole("combobox", { name: "Build log stage" })
  await expect(stage).toHaveText("Verify Smoke")
  await expect(stage).toBeFocused()
  await expect(page.getByText("smoke: GET / 200", { exact: true })).toBeVisible()
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
  await page.getByRole("option", { name: "Analyze Plan" }).click()
  await expect(page.getByText("Analyze Plan wrote nothing to the build log.")).toBeVisible()
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
  const header = page.locator('[data-slot="page-header"]')
  await expect(header.getByText("Cancelling", { exact: true })).toBeVisible()
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

test("run runtime logs open the server-provided activation window and withhold unproven windows", async ({
  page,
}) => {
  await mockProject(page)
  await page.routeWebSocket(/\/api\/v1\/deploy\/7\/runs\/84\/stream/, (socket) => {
    socket.send(JSON.stringify({ type: "snapshot", data: snapshot(), ts: Date.now() }))
  })
  const since = "2026-11-01T06:25:30.123Z"
  const until = "2026-11-01T06:35:30.123Z"
  const query = new URLSearchParams({ source: "docker:preview", mode: "search", since, until })
  let activated = true
  await page.route("**/api/v1/deploy/7/runs/84/logs", (route) =>
    json(route, {
      status: "available",
      windowReason: activated ? "" : "This run has no completed activation evidence.",
      sources: [
        {
          containerId: "preview",
          name: "preview-web",
          liveUrl: "/logs?source=docker%3Apreview",
          activationUrl: activated ? `/logs?${query}` : undefined,
        },
      ],
    }),
  )
  const streams: string[] = []
  await page.routeWebSocket(/\/api\/v1\/logs\/stream/, (socket) => {
    streams.push(new URL(socket.url()).searchParams.get("source") || "")
    socket.send(
      JSON.stringify({
        type: "logs",
        data: [{ text: "GET /api/health 200", level: "info" }],
        ts: Date.now(),
      }),
    )
  })
  const searches: URL[] = []
  await page.route("**/api/v1/logs/**", (route) => {
    const url = new URL(route.request().url())
    if (url.pathname.endsWith("/sources"))
      return json(route, {
        sources: [{ id: "docker:preview", label: "preview-web", kind: "docker", rotated: false }],
        units: [],
        roots: [],
        missing: {},
      })
    if (url.pathname.endsWith("/search")) {
      searches.push(url)
      return json(route, {
        lines: [],
        scanned: 0,
        matched: 0,
        truncated: false,
        complete: true,
        files: [],
        histogram: [],
        tookMillis: 1,
      })
    }
    return json(route, {})
  })

  await page.goto("/deploy/7/runs/84")
  await page
    .getByRole("group", { name: "Run views" })
    .getByRole("button", { name: "Runtime logs", exact: true })
    .click()
  await expect(page.getByText("GET /api/health 200", { exact: true })).toBeVisible()
  expect(streams).toContain("docker:preview")

  const toggle = page.getByRole("button", { name: "Around activation", exact: true })
  await expect(toggle).toBeVisible()
  await toggle.focus()
  await page.keyboard.press("Enter")
  await expect.poll(() => searches.length).toBe(1)
  await expect(page).toHaveURL(/\/deploy\/7\/runs\/84$/)
  expect(searches[0].searchParams.get("source")).toBe("docker:preview")
  expect(searches[0].searchParams.get("since")).toBe(since)
  expect(searches[0].searchParams.get("until")).toBe(until)

  activated = false
  await page.goto("/deploy/7/runs/84")
  await page
    .getByRole("group", { name: "Run views" })
    .getByRole("button", { name: "Runtime logs", exact: true })
    .click()
  await expect(
    page.getByText("This run has no completed activation evidence.", { exact: true }),
  ).toBeVisible()
  await expect(page.getByRole("button", { name: "Around activation", exact: true })).toHaveCount(0)
  await expect(page.getByRole("combobox", { name: "Runtime log source" })).toHaveText("preview-web")
})

test("metrics compares the release before and after activation", async ({ page }) => {
  await mockProject(page)
  await page.routeWebSocket(/\/api\/v1\/deploy\/7\/runs\/84\/stream/, (socket) => {
    socket.send(JSON.stringify({ type: "snapshot", data: snapshot(), ts: Date.now() }))
  })
  await page.route("**/api/v1/deploy/7/runs/84/metrics", (route) =>
    json(route, {
      status: "available",
      before: {
        releaseId: 19,
        status: "available",
        history: {
          series: [
            {
              containerId: "abc123def456",
              points: [
                { samples: 4, cpu: 12, cpuPeak: 20, mem: 30, memPeak: 40, memBytesPeak: 2048 },
              ],
            },
          ],
        },
      },
      after: {
        releaseId: 20,
        status: "partial",
        reason: "Only two samples were retained before this window closed.",
        history: {
          series: [
            {
              containerId: "def456abc123",
              points: [
                { samples: 2, cpu: 18, cpuPeak: 25, mem: 35, memPeak: 45, memBytesPeak: 4096 },
              ],
            },
          ],
        },
      },
      hostBefore: { points: [{ samples: 4, cpu: 10, cpuPeak: 15, mem: 50, memPeak: 55 }] },
      hostAfter: { points: [{ samples: 2, cpu: 11, cpuPeak: 16, mem: 52, memPeak: 57 }] },
    }),
  )
  await page.goto("/deploy/7/runs/84")
  await page
    .getByRole("group", { name: "Run views" })
    .getByRole("button", { name: "Metrics", exact: true })
    .click()
  await expect(page.getByRole("heading", { name: "Metrics around activation" })).toBeVisible()
  await expect(page.getByText("Before activation", { exact: true })).toBeVisible()
  await expect(page.getByText("After activation", { exact: true })).toBeVisible()
  await expect(page.getByText("Partial history", { exact: true })).toBeVisible()
  await expect(page.getByText("12%", { exact: true })).toBeVisible()
  await expect(page.getByText(/Host CPU mean 10%/)).toBeVisible()
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
  await expect(page.getByText("health_gate_failed", { exact: true })).toBeVisible()
  await expect(page.getByText("could not connect after 20 attempts", { exact: true })).toBeVisible()
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

  await page
    .getByRole("group", { name: "Run views" })
    .getByRole("button", { name: "Details", exact: true })
    .click()
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
})
