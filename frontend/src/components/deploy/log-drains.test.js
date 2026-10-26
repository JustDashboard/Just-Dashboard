import { describe, expect, test } from "bun:test"
import { drainEndpointProblem, drainHealth, drainReach } from "./log-drains"

describe("drainEndpointProblem", () => {
  test("a header never goes over plain http to another machine", () => {
    expect(drainEndpointProblem("http://loki.lan:3100", true)).toBe("credential-over-http")
    expect(drainEndpointProblem("http://127.0.0.1:3100", true)).toBeUndefined()
    expect(drainEndpointProblem("http://[::1]:4318", true)).toBeUndefined()
    expect(drainEndpointProblem("https://logs.example.com", true)).toBeUndefined()
  })
  test("plain http without a header is allowed and said", () => {
    expect(drainEndpointProblem("http://10.0.0.5:4318", false)).toBe("cleartext")
    expect(drainEndpointProblem("http://127.example.com", false)).toBe("cleartext")
    expect(drainEndpointProblem("not a url", false)).toBeUndefined()
  })
})

describe("drainReach", () => {
  const projects = [
    { id: 1, name: "shop" },
    { id: 2, name: "blog" },
    { id: 3, name: "api" },
  ]
  test("names the streams and the projects", () => {
    expect(drainReach({ streams: ["output", "requests"], projects: [] }, projects)).toBe(
      "output and requests of every project",
    )
    expect(drainReach({ streams: ["requests"], projects: [1, 2] }, projects)).toBe(
      "requests of shop and blog",
    )
    expect(drainReach({ streams: ["output"], projects: [1, 2, 3] }, projects)).toBe(
      "container output of 3 projects",
    )
  })
})

describe("drainHealth", () => {
  const base = { shipped: 0, dropped: 0, batches: 0, queued: 0 }
  test("a failure after the last success is a warning in the destination's words", () => {
    const health = drainHealth({
      enabled: true,
      status: {
        ...base,
        shipped: 12,
        queued: 40,
        lastSuccessAt: "2026-10-26T10:00:00Z",
        lastErrorAt: "2026-10-26T10:05:00Z",
        lastError: "the destination refused the credentials (401): invalid token",
      },
    })
    expect(health.tone).toBe("warning")
    expect(health.line).toContain("refused the credentials (401)")
    expect(health.line).toContain("40 records waiting")
  })
  test("a refusal that a later delivery recovered from is history", () => {
    const health = drainHealth({
      enabled: true,
      status: {
        ...base,
        shipped: 2,
        lastSuccessAt: "2026-10-26T10:05:00Z",
        lastErrorAt: "2026-10-26T10:00:00Z",
        lastError: "the destination answered 503",
      },
    })
    expect(health.tone).toBe("muted")
    expect(health.line).toStartWith("2 records shipped · last delivery")
  })
  test("paused and never-sent drains say so", () => {
    expect(drainHealth({ enabled: false, status: { ...base, shipped: 3 } }).line).toStartWith(
      "Paused",
    )
    expect(drainHealth({ enabled: true, status: base }).line).toStartWith("Nothing sent yet")
    expect(
      drainHealth({
        enabled: true,
        status: { ...base, shipped: 9, dropped: 4, lastSuccessAt: "2026-10-26T10:00:00Z" },
      }).tone,
    ).toBe("warning")
  })
})
