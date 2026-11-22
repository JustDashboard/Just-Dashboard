import { expect, test } from "@playwright/test"
import { mockProject, run as fixtureRun, steps as fixtureSteps } from "./deploy-fixture"
import type { DeploymentEngineRun, DeploymentStep } from "../../src/lib/types"

test.use({ timezoneId: "UTC" })

const run = fixtureRun as DeploymentEngineRun
const steps = fixtureSteps as unknown as DeploymentStep[]
const STREAM = /\/api\/v1\/deploy\/7\/runs\/84\/stream/

/** The run at its build: the steps before it passed, the build running, the rest waiting. */
function building(): { run: DeploymentEngineRun; steps: DeploymentStep[] } {
  const at = "2026-09-03T12:00:00Z"
  return {
    run: { ...run, state: "running", claimedAt: at, endedAt: undefined },
    steps: steps.map((step): DeploymentStep => {
      const base = { ...step, evidence: {}, startedAt: undefined, endedAt: undefined }
      if (step.key === "build_artifact") return { ...base, state: "running", startedAt: at }
      if (step.ordinal < 5) return { ...base, state: "passed", startedAt: at, endedAt: at }
      return { ...base, state: "pending" }
    }),
  }
}

/**
 * A chatty build through the console: 3,000 BuildKit lines in the engine's
 * batches of 64, ten a second, which is the rate a `next build` or a
 * `cargo build` writes at. Measured in the page: from the batch's message
 * landing to its last line being on screen, and the frames drawn meanwhile.
 * The figures are attached to the result (`console-figures`).
 *
 * Before 0.9.1 the console never finished such a build: past 2,600 lines the
 * drip held its last 400 for as long as the reader followed, and a batch took
 * four to five seconds to reach the screen. The bounds are loose, because a
 * shared machine's frames are not a laptop's: every batch is drawn, and drawn
 * on the build's heels rather than seconds behind it.
 */
test("the console keeps up with a 3,000-line build", async ({ page }, testInfo) => {
  test.setTimeout(120_000)
  await mockProject(page)
  const TOTAL = 3000
  const BATCH = 64
  let seq = 1000
  const line = (index: number) => {
    const n = index % 40
    if (n === 0) return `#${14 + (index % 7)} [build ${1 + (index % 4)}/4] RUN bun run build`
    if (n === 39) return `#${14 + (index % 7)} DONE ${(index / 100).toFixed(1)}s`
    return `#${14 + (index % 7)} ${(index / 10).toFixed(3)} compiled module ${index} in ${index % 97}ms`
  }
  const batch = (from: number, count: number) =>
    Array.from({ length: count }, (_, offset) => ({
      seq: ++seq,
      type: "step.log",
      runId: 84,
      stepId: 105,
      ts: new Date(Date.parse("2026-09-03T12:00:00Z") + (from + offset) * 10).toISOString(),
      data: { stream: "stdout", text: `${line(from + offset)}\n`, truncated: false },
    }))
  let socketSend: (payload: string) => void = () => {}
  await page.routeWebSocket(STREAM, (socket) => {
    socket.onClose(() => {})
    socket.send(JSON.stringify({ type: "snapshot", data: building(), ts: Date.now() }))
    socketSend = (payload) => socket.send(payload)
  })
  // After the route, whose mock replaces the page's WebSocket: the probe
  // wraps that.
  await page.addInitScript(() => {
    type Probe = {
      arrivals: Map<number, number>
      lags: number[]
      frames: number[]
      longTasks: number[]
      pending: number[]
      running: boolean
    }
    const probe: Probe = {
      arrivals: new Map(),
      lags: [],
      frames: [],
      longTasks: [],
      pending: [],
      running: false,
    }
    ;(window as unknown as { __probe: Probe }).__probe = probe
    const Native = window.WebSocket
    class Probed extends Native {
      constructor(url: string | URL, protocols?: string | string[]) {
        super(url, protocols)
        // Registered before the page's own handler, so the instant is when
        // the message reached the page, not when the page was done with it.
        this.addEventListener("message", (event) => {
          try {
            const envelope = JSON.parse(String(event.data))
            if (envelope.type !== "events" || !Array.isArray(envelope.data)) return
            const last = envelope.data.at(-1)
            if (last?.type !== "step.log") return
            probe.arrivals.set(last.seq, performance.now())
            probe.pending.push(last.seq)
          } catch {}
        })
      }
    }
    window.WebSocket = Probed as typeof WebSocket
    try {
      new PerformanceObserver((list) => {
        for (const entry of list.getEntries()) probe.longTasks.push(entry.duration)
      }).observe({ type: "longtask", buffered: true })
    } catch {}
    let last = 0
    const tick = (at: number) => {
      if (probe.running && last) probe.frames.push(at - last)
      last = at
      // A batch's last line on screen is its lag; batches land in order.
      while (probe.pending.length) {
        const seq = probe.pending[0]
        // The console's own copy: the step's record quotes its last lines too.
        const pane = document.querySelector('[aria-label="Deployment transcript"]')
        if (!pane?.querySelector(`#build-line-${seq}-0`)) break
        probe.pending.shift()
        probe.lags.push(performance.now() - probe.arrivals.get(seq)!)
      }
      requestAnimationFrame(tick)
    }
    requestAnimationFrame(tick)
  })

  await page.setViewportSize({ width: 1440, height: 1000 })
  await page.goto("/deploy/7/runs/84")
  await expect(page.locator('[data-slot="console-waiting"]')).toBeVisible()

  await page.evaluate(() => {
    ;(window as unknown as { __probe: { running: boolean } }).__probe.running = true
  })
  const started = Date.now()
  for (let from = 0; from < TOTAL; from += BATCH) {
    socketSend(
      JSON.stringify({
        type: "events",
        data: batch(from, Math.min(BATCH, TOTAL - from)),
        ts: Date.now(),
      }),
    )
    // The engine's clock, not the page's: a page that falls behind must
    // not slow the build down with it.
    await new Promise((resolve) => setTimeout(resolve, 100))
  }
  const batches = Math.ceil(TOTAL / BATCH)
  await page.waitForFunction(
    (count) => (window as unknown as { __probe: { lags: number[] } }).__probe.lags.length >= count,
    batches,
    { timeout: 60_000, polling: 250 },
  )
  const drained = Date.now() - started
  const result = await page.evaluate(() => {
    const probe = (
      window as unknown as {
        __probe: { lags: number[]; frames: number[]; longTasks: number[]; running: boolean }
      }
    ).__probe
    probe.running = false
    return { lags: probe.lags, frames: probe.frames, longTasks: probe.longTasks }
  })
  const sorted = (values: number[]) => [...values].sort((a, b) => a - b)
  const at = (values: number[], q: number) => sorted(values)[Math.floor((values.length - 1) * q)]
  const figures = {
    lines: TOTAL,
    batches: result.lags.length,
    lagMedianMs: Math.round(at(result.lags, 0.5)),
    lagWorstMs: Math.round(Math.max(...result.lags)),
    frames: result.frames.length,
    frameMedianMs: Math.round(at(result.frames, 0.5) * 10) / 10,
    frameWorstMs: Math.round(Math.max(...result.frames)),
    framesOver50ms: result.frames.filter((frame) => frame > 50).length,
    longTasks: result.longTasks.length,
    longTaskMs: Math.round(result.longTasks.reduce((sum, value) => sum + value, 0)),
    drainedMs: drained,
  }
  testInfo.annotations.push({ type: "console-figures", description: JSON.stringify(figures) })
  console.log(`console figures: ${JSON.stringify(figures)}`)

  // Every line is there, the last numbered as the last, and find still finds
  // one among them.
  const transcript = page.getByRole("group", { name: "Deployment transcript" })
  await expect(page.getByText(`${TOTAL.toLocaleString()} lines`).first()).toBeVisible()
  await expect(transcript.locator(`#build-line-${seq}-0`)).toContainText(String(TOTAL))
  await page.getByRole("textbox", { name: "Search build logs" }).fill("compiled module 2998 ")
  await expect(transcript.getByRole("listitem")).toHaveCount(1)
  expect(figures.batches).toBe(batches)
  expect(figures.lagMedianMs).toBeLessThan(1500)
})
