import { describe, expect, test } from "bun:test"
import {
  captureFilter,
  captureLine,
  captureMatches,
  captureStateWord,
  captureTotals,
  neighbours,
  runChips,
  runCounts,
  runMatches,
  runRequestLine,
  runScopeOf,
  runTool,
  startedApart,
} from "./network-runs-view"

const run = (overrides = {}) => ({
  id: "a",
  name: "IPv6 DNS baseline",
  status: "completed",
  createdBy: "operator",
  request: { tool: "dns", target: "example.test", record: "AAAA" },
  ...overrides,
})

describe("a saved run in words", () => {
  test("names the tool as Tools does, and an investigation as a connection path", () => {
    expect(runTool(run())).toBe("DNS")
    expect(runTool(run({ request: { tool: "newtool", target: "" } }))).toBe("newtool")
    expect(runTool(run({ kind: "investigation", request: { tool: "", target: "" } }))).toBe(
      "Connection path",
    )
  })
  test("says the request on one line", () => {
    expect(runRequestLine(run())).toBe("example.test · AAAA")
    expect(
      runRequestLine(run({ request: { tool: "port", target: "2001:db8::9", port: 8443 } })),
    ).toBe("2001:db8::9:8443")
    expect(runRequestLine(run({ request: { tool: "listeners", target: "" } }))).toBe("this host")
    expect(
      runRequestLine(
        run({
          kind: "investigation",
          request: { tool: "", target: "" },
          investigationRequest: {
            target: "private.test",
            family: "inet6",
            protocol: "udp",
            port: 8443,
          },
        }),
      ),
    ).toBe("private.test · IPv6 · UDP 8443")
  })
})

describe("the runs list's chips", () => {
  const runs = [
    run({ id: "1", status: "queued" }),
    run({ id: "2", status: "cancelling" }),
    run({ id: "3", status: "failed" }),
    run({ id: "4" }),
  ]
  test("count work in flight as running, whatever its stage", () => {
    expect(runScopeOf(runs[0])).toBe("running")
    expect(runScopeOf(runs[1])).toBe("running")
    const counts = runCounts(runs)
    expect(counts).toEqual({
      all: 4,
      running: 2,
      completed: 1,
      failed: 1,
      interrupted: 0,
      cancelled: 0,
    })
    expect(runChips(counts, "all")).toEqual(["all", "running", "completed", "failed"])
    expect(runChips(counts, "interrupted")).toContain("interrupted")
  })
  test("narrow by scope and by every word of the search", () => {
    expect(runs.filter((r) => runMatches(r, "running", "")).map((r) => r.id)).toEqual(["1", "2"])
    expect(runMatches(runs[3], "all", "dns aaaa")).toBe(true)
    expect(runMatches(runs[3], "all", "dns ping")).toBe(false)
    expect(runMatches(runs[3], "failed", "dns")).toBe(false)
  })
})

describe("walking a list", () => {
  const rows = [{ id: "a" }, { id: "b" }, { id: "c" }]
  test("finds the neighbours, and starts at the top from an item the list does not hold", () => {
    expect(neighbours(rows, "b")).toEqual({ at: 1, previous: rows[0], next: rows[2] })
    expect(neighbours(rows, "a").previous).toBeUndefined()
    expect(neighbours(rows, "c").next).toBeUndefined()
    expect(neighbours(rows, "gone")).toEqual({ at: -1, previous: undefined, next: rows[0] })
  })
})

describe("a capture in words", () => {
  const request = {
    interface: "ens3",
    family: "inet6",
    protocol: "udp",
    source: "2001:db8::1",
    port: 8443,
  }
  test("says its filter", () => {
    expect(captureFilter(request)).toEqual({
      title: "UDP 8443",
      from: "2001:db8::1",
      to: "any address",
      narrowed: true,
    })
    expect(captureFilter({ ...request, protocol: "all", port: undefined, source: "" })).toEqual({
      title: "Every protocol",
      from: "any address",
      to: "any address",
      narrowed: false,
    })
    expect(captureLine({ request })).toBe("ens3 · IPv6 · UDP 8443")
  })
  test("says a row's state without repeating what the list is of", () => {
    expect(captureStateWord("Capture completed")).toBe("Completed")
    expect(captureStateWord("Capturing")).toBe("Capturing")
    expect(captureStateWord("Interrupted")).toBe("Interrupted")
  })
  test("adds the list up in bytes, not rounded to nothing", () => {
    expect(
      captureTotals({
        runs: [
          { status: "running", result: { bytes: 100 } },
          { status: "completed", result: { bytes: 412 } },
          { status: "failed" },
        ],
      }),
    ).toEqual({ capturing: 1, bytes: 512, retained: 3 })
  })
  test("matches a search against its name, interface and addresses", () => {
    expect(captureMatches({ name: "Incident", request }, "ens3 udp")).toBe(true)
    expect(captureMatches({ name: "Incident", request }, "2001:db8::1")).toBe(true)
    expect(captureMatches({ name: "Incident", request }, "eth0")).toBe(false)
  })
  test("says how far apart a related run and the capture started", () => {
    expect(startedApart("2026-10-08T11:59:00Z", "2026-10-08T12:00:01Z")).toBe(
      "started 1 min before the capture",
    )
    expect(startedApart("2026-10-08T12:00:31Z", "2026-10-08T12:00:01Z")).toBe(
      "started 30 s after the capture",
    )
    expect(startedApart(undefined, "2026-10-08T12:00:01Z")).toBeUndefined()
  })
})
