import { describe, expect, test } from "bun:test"
import {
  applyLines,
  applyToRuntime,
  cores,
  cpuReadings,
  fleetSummary,
  historyLine,
  isOff,
  memoryReadings,
  mib,
  span,
  verdictTone,
} from "./sizing-model"

const sizing = (overrides) => ({
  projectId: 7,
  projectName: "shop",
  environmentId: 12,
  environmentName: "production",
  environmentKind: "production",
  status: "available",
  verdict: "too_tight",
  offBy: 1.25,
  window: {
    from: "2026-10-27T06:00:00Z",
    to: "2026-10-29T06:00:00Z",
    samples: 576,
    containers: 1,
    intervalSeconds: 300,
    coveredSeconds: 172800,
    neededSeconds: 86400,
  },
  memory: {
    verdict: "too_tight",
    limitMb: 512,
    liveLimitMb: 512,
    p95Mb: 491,
    peakMb: 500,
    suggestedMb: 640,
    oomKills: 0,
    offBy: 1.25,
    reasons: ["It used 491 MiB or more 5% of the time, 96% of the limit."],
  },
  cpu: {
    verdict: "oversized",
    limit: 4,
    liveLimit: 4,
    p95: 0.21,
    peak: 0.4,
    suggested: 0.35,
    throttledShare: null,
    offBy: 11.43,
    reasons: ["The limit is 11.4× what it needs."],
  },
  apply: { memoryMb: 640, cpus: 0.35 },
  revision: 3,
  pending: false,
  oom: { watching: true, since: "2026-10-29T00:00:00Z", kills: 0 },
  ...overrides,
})

describe("right-sizing words", () => {
  test("figures read as a person would type them", () => {
    expect(mib(640)).toBe("640 MiB")
    expect(mib(1536)).toBe("1.5 GiB")
    expect(mib(2048)).toBe("2 GiB")
    expect(cores(1)).toBe("1 core")
    expect(cores(0.35)).toBe("0.35 cores")
    expect(cores(12)).toBe("12 cores")
  })

  test("the history a suggestion rests on, or how much more it needs", () => {
    expect(historyLine(sizing().window)).toBe("576 samples over 2 days")
    expect(historyLine({ ...sizing().window, samples: 72, coveredSeconds: 6 * 3600 })).toBe(
      "6 h of the 24 h of history a suggestion needs",
    )
    expect(span(45 * 60)).toBe("45 min")
    expect(span(30 * 3600)).toBe("30 h")
  })

  test("each limit's readings sit after its suggestion", () => {
    const value = sizing()
    expect(memoryReadings(value.memory)).toBe("limit 512 MiB · p95 491 MiB · peak 500 MiB")
    expect(cpuReadings(value.cpu)).toBe("limit 4 cores · p95 0.21 cores · peak 0.4 cores")
    expect(cpuReadings({ ...value.cpu, limit: 0, throttledShare: 0.12 })).toBe(
      "no limit · p95 0.21 cores · peak 0.4 cores · throttled 12%",
    )
    expect(applyLines(value)).toEqual(["memory 512 MiB → 640 MiB", "CPU 4 cores → 0.35 cores"])
    expect(applyLines(sizing({ apply: { cpus: 0.35 } }))).toEqual(["CPU 4 cores → 0.35 cores"])
  })

  test("the tone is the verdict's, and only the actionable ones are listed", () => {
    expect(verdictTone("too_tight")).toBe("danger")
    expect(verdictTone("oversized")).toBe("warning")
    expect(verdictTone("right")).toBe("running")
    expect(verdictTone("unlimited")).toBe("unknown")
    expect(isOff(sizing())).toBe(true)
    expect(isOff(sizing({ verdict: "unlimited" }))).toBe(true)
    expect(isOff(sizing({ verdict: "right", apply: null }))).toBe(false)
    expect(isOff(sizing({ verdict: "insufficient_history", apply: null }))).toBe(false)
    expect(isOff(sizing({ status: "compose" }))).toBe(false)
  })

  test("an apply writes only the suggested limits into the runtime", () => {
    const runtime = {
      strategy: "blue_green",
      internalPort: 3000,
      memoryMb: 512,
      cpus: 4,
      pidsLimit: 256,
    }
    expect(applyToRuntime(runtime, { memoryMb: 640, cpus: 0.35 })).toEqual({
      ...runtime,
      memoryMb: 640,
      cpus: 0.35,
    })
    expect(applyToRuntime(runtime, { cpus: 1 })).toEqual({ ...runtime, cpus: 1 })
    expect(runtime.memoryMb).toBe(512)
  })

  test("the fleet says what it does not list", () => {
    expect(
      fleetSummary([
        sizing({ verdict: "right" }),
        sizing({ verdict: "right" }),
        sizing({ verdict: "insufficient_history" }),
        sizing({ verdict: "insufficient_history", status: "unavailable" }),
        sizing(),
      ]),
    ).toBe("2 sized right · 1 still gathering history")
    expect(fleetSummary([])).toBe("")
  })
})
