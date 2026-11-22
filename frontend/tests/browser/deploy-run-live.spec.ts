import { expect, test } from "@playwright/test"
import { mockProject, run as fixtureRun, steps as fixtureSteps } from "./deploy-fixture"
import type { DeploymentEngineRun, DeploymentStep } from "../../src/lib/types"

/**
 * The run page while a run moves under it: what the stream says, as it says
 * it. The snapshot is read once, so every time the page shows for a step or
 * the run after that comes from the events alone.
 */

test.use({ timezoneId: "UTC" })

const run = fixtureRun as DeploymentEngineRun
const steps = fixtureSteps as unknown as DeploymentStep[]
const STREAM = /\/api\/v1\/deploy\/7\/runs\/84\/stream/

/** The run as a page opened before it reached a slot: queued, every step waiting. */
function queuedSnapshot(): { run: DeploymentEngineRun; steps: DeploymentStep[] } {
  return {
    run: { ...run, state: "queued", claimedAt: undefined, endedAt: undefined },
    steps: steps.map((step) => ({
      ...step,
      state: "pending" as const,
      startedAt: undefined,
      endedAt: undefined,
      evidence: {},
    })),
  }
}

test("a step that starts after the page connected shows its time, and the header follows the run", async ({
  page,
}) => {
  await mockProject(page)
  let send: (events: unknown[]) => void = () => {}
  await page.routeWebSocket(STREAM, (socket) => {
    socket.onClose(() => {})
    socket.send(JSON.stringify({ type: "snapshot", data: queuedSnapshot(), ts: Date.now() }))
    send = (events) => socket.send(JSON.stringify({ type: "events", data: events, ts: Date.now() }))
  })
  await page.clock.install({ time: new Date("2026-09-03T12:00:00Z") })
  await page.goto("/deploy/7/runs/84")

  const identity = page.locator('[data-slot="run-identity"]')
  await expect(identity.getByText("Queued for", { exact: true })).toBeVisible()

  // A slot claims the run, then its build starts: the events the engine
  // writes, and nothing else, since the snapshot is not read again.
  const claimed = new Date("2026-09-03T12:00:00Z")
  let seq = 200
  send([
    {
      seq: ++seq,
      type: "run.state",
      runId: 84,
      ts: claimed.toISOString(),
      data: { state: "preparing", claimedAt: claimed.toISOString() },
    },
    {
      seq: ++seq,
      type: "step.state",
      runId: 84,
      stepId: 105,
      ts: claimed.toISOString(),
      data: {
        key: "build_artifact",
        state: "running",
        attempt: 1,
        startedAt: claimed.toISOString(),
        evidence: {},
      },
    },
  ])
  await expect(identity.getByText("Running for", { exact: true })).toBeVisible()

  // Forty seconds into the build (and the seconds the page took to load),
  // its row in the steps reads its time.
  await page.clock.runFor(40_000)
  const buildRow = page
    .getByRole("list", { name: "Deployment steps" })
    .getByRole("button", { name: /^Build\b/ })
  await expect(buildRow).toContainText(/(?<!\d)[4-9]\ds/)
  await expect(page.getByRole("list", { name: "Release path" })).toContainText(/(?<!\d)[4-9]\ds/)

  // It ends, and the run with it: the times stop where the engine says.
  const ended = new Date("2026-09-03T12:01:05Z")
  send([
    {
      seq: ++seq,
      type: "step.state",
      runId: 84,
      stepId: 105,
      ts: ended.toISOString(),
      data: {
        key: "build_artifact",
        state: "passed",
        attempt: 1,
        startedAt: claimed.toISOString(),
        endedAt: ended.toISOString(),
        evidence: {},
      },
    },
    {
      seq: ++seq,
      type: "run.state",
      runId: 84,
      ts: ended.toISOString(),
      data: { state: "failed", code: "readiness", endedAt: ended.toISOString() },
    },
  ])
  await expect(identity.getByText("Took", { exact: true })).toBeVisible()
  await expect(identity).toContainText("1m 5s")
  await expect(buildRow).toContainText("1m 5s")
  // Later, it still says what it took: nothing is measured to the present.
  await page.clock.runFor(240_000)
  await expect(identity).toContainText("1m 5s")
  await expect(buildRow).toContainText("1m 5s")
})

test("a step the engine wrote no times for takes them from its events", async ({ page }) => {
  await mockProject(page)
  let send: (events: unknown[]) => void = () => {}
  await page.routeWebSocket(STREAM, (socket) => {
    socket.onClose(() => {})
    socket.send(JSON.stringify({ type: "snapshot", data: queuedSnapshot(), ts: Date.now() }))
    send = (events) => socket.send(JSON.stringify({ type: "events", data: events, ts: Date.now() }))
  })
  await page.clock.install({ time: new Date("2026-09-03T12:00:00Z") })
  await page.goto("/deploy/7/runs/84")
  await expect(page.getByRole("list", { name: "Deployment steps" })).toBeVisible()

  // An engine from before the times were sent: the event's own instant is
  // when the step started.
  send([
    {
      seq: 300,
      type: "step.state",
      runId: 84,
      stepId: 105,
      ts: "2026-09-03T12:00:00Z",
      data: { key: "build_artifact", state: "running", attempt: 1, evidence: {} },
    },
  ])
  await page.clock.runFor(12_000)
  await expect(
    page.getByRole("list", { name: "Deployment steps" }).getByRole("button", { name: /^Build\b/ }),
  ).toContainText(/(?<!\d)[1-5]\ds/)
})
