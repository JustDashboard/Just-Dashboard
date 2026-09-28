import { describe, expect, test } from "bun:test"
import {
  engineRun,
  hear,
  markUnloaded,
  reloadOutcome,
  stillUnloaded,
  unitRunning,
  unitState,
} from "./site-serving"

const unit = (activeState, extra = {}) => ({
  name: "nginx.service",
  description: "A high performance web server",
  loadState: "loaded",
  activeState,
  subState: activeState === "active" ? "running" : "dead",
  unitFileState: "enabled",
  enabled: true,
  ...extra,
})
const reading = (at, activeState, extra) => ({ at, unit: unit(activeState, extra) })

const valid = { valid: true, command: "nginx -t", output: "", diagnostics: [] }
const noPidFile = 'nginx: [error] open() "/run/nginx.pid" failed (2: No such file or directory)'
const reloaded = { reloaded: true, reload: { validation: valid, reloaded: true, output: "" } }
const notRunning = {
  reloaded: false,
  reloadError: `reload failed: ${noPidFile}`,
  reload: { validation: valid, reloaded: false, output: noPidFile },
}
const failed = {
  reloaded: false,
  reloadError: "reload failed: signal process started",
  reload: {
    validation: valid,
    reloaded: false,
    output: "nginx: [alert] kill(812, 1) failed (1: Operation not permitted)",
  },
}

describe("unitRunning", () => {
  test("is up while active or reloading, and nothing else", () => {
    expect(unitRunning(unit("active"))).toBe(true)
    expect(unitRunning(unit("reloading"))).toBe(true)
    for (const state of ["inactive", "failed", "activating", "deactivating"]) {
      expect(unitRunning(unit(state))).toBe(false)
    }
  })
})

describe("unitState", () => {
  test("says systemd's sub-state where it adds something", () => {
    expect(unitState(unit("inactive"))).toBe("inactive (dead)")
    expect(unitState({ activeState: "failed", subState: "failed" })).toBe("failed")
  })
})

describe("reloadOutcome", () => {
  test("reads a reload that went through", () => {
    expect(reloadOutcome(reloaded, reading(1, "active"))).toBe("reloaded")
    // A delete answers with the reload and no top-level flag.
    expect(reloadOutcome({ reload: reloaded.reload }, undefined)).toBe("reloaded")
  })

  test("reads no reload at all as nothing", () => {
    expect(reloadOutcome({ reloaded: false }, undefined)).toBeUndefined()
  })

  test("reads no nginx to signal as not running, unless systemd has it up", () => {
    expect(reloadOutcome(notRunning, undefined)).toBe("notRunning")
    expect(reloadOutcome(notRunning, reading(1, "inactive"))).toBe("notRunning")
    // Up with its pid file gone: running what it had, out of the reload's reach.
    expect(reloadOutcome(notRunning, reading(1, "active"))).toBe("failed")
  })

  test("reads any other failure as failed", () => {
    expect(reloadOutcome(failed, undefined)).toBe("failed")
    expect(reloadOutcome(failed, reading(1, "inactive"))).toBe("failed")
  })
})

describe("hear", () => {
  test("keeps the newest reload, and when one last went through", () => {
    let heard = hear({}, 10, "reloaded")
    expect(heard).toEqual({ last: { at: 10, outcome: "reloaded" }, loadedAt: 10 })
    heard = hear(heard, 20, "failed")
    expect(heard).toEqual({ last: { at: 20, outcome: "failed" }, loadedAt: 10 })
    heard = hear(heard, 30, "notRunning")
    expect(heard).toEqual({ last: { at: 30, outcome: "notRunning" }, loadedAt: 10 })
  })

  test("ignores a verb that asked for no reload", () => {
    const heard = { last: { at: 10, outcome: "reloaded" }, loadedAt: 10 }
    expect(hear(heard, 20, undefined)).toBe(heard)
  })
})

describe("engineRun", () => {
  test("knows nothing without a reading", () => {
    expect(engineRun(undefined, undefined)).toBeUndefined()
    // A failed reload says nothing about whether nginx runs.
    expect(engineRun(undefined, { at: 5, outcome: "failed" })).toBeUndefined()
  })

  test("reads systemd's unit", () => {
    expect(engineRun(reading(5, "active"), undefined)).toEqual({ running: true, from: "unit" })
    expect(engineRun(reading(5, "failed"), undefined)).toEqual({ running: false, from: "unit" })
  })

  test("reads the verbs' reloads on a host whose nginx has no unit", () => {
    expect(engineRun(undefined, { at: 5, outcome: "notRunning" })).toEqual({
      running: false,
      from: "reload",
    })
    expect(engineRun(undefined, { at: 5, outcome: "reloaded" })).toEqual({
      running: true,
      from: "reload",
    })
  })

  test("goes by the newer of the two", () => {
    // Stopped since systemd was last read: the reload is newer.
    expect(engineRun(reading(5, "active"), { at: 9, outcome: "notRunning" })).toEqual({
      running: false,
      from: "reload",
    })
    // Started since the reload found none: systemd is newer.
    expect(engineRun(reading(12, "active"), { at: 9, outcome: "notRunning" })).toEqual({
      running: true,
      from: "unit",
    })
    expect(engineRun(reading(12, "inactive"), { at: 9, outcome: "reloaded" })).toEqual({
      running: false,
      from: "unit",
    })
    // A failed reload is no reading: systemd's older one stands.
    expect(engineRun(reading(5, "inactive"), { at: 9, outcome: "failed" })).toEqual({
      running: false,
      from: "unit",
    })
  })
})

describe("stillUnloaded", () => {
  const up = reading(5, "active", { mainPid: 812 })

  test("marks the nginx process the reload could not reach", () => {
    expect(markUnloaded(10, up)).toEqual({ at: 10, pid: 812 })
    expect(markUnloaded(10, reading(5, "inactive", { mainPid: 812 }))).toEqual({ at: 10 })
    expect(markUnloaded(10, undefined)).toEqual({ at: 10 })
  })

  test("holds until a verb reloads nginx after the change", () => {
    const mark = markUnloaded(10, up)
    expect(stillUnloaded(undefined, undefined, up)).toBe(false)
    expect(stillUnloaded(mark, undefined, up)).toBe(true)
    // A reload from before the change loaded what came before it.
    expect(stillUnloaded(mark, 8, up)).toBe(true)
    expect(stillUnloaded(mark, 11, up)).toBe(false)
  })

  test("holds across systemd's readings of the same nginx, and a reload keeps its process", () => {
    const mark = markUnloaded(10, up)
    expect(stillUnloaded(mark, undefined, reading(20, "active", { mainPid: 812 }))).toBe(true)
    expect(stillUnloaded(mark, undefined, reading(20, "reloading", { mainPid: 812 }))).toBe(true)
  })

  test("ends when nginx has started again since, and so read the change", () => {
    expect(
      stillUnloaded(markUnloaded(10, up), undefined, reading(20, "active", { mainPid: 944 })),
    ).toBe(false)
    // Marked with nginx down: any nginx up now started after.
    const down = markUnloaded(10, reading(5, "inactive"))
    expect(stillUnloaded(down, undefined, reading(20, "inactive"))).toBe(true)
    expect(stillUnloaded(down, undefined, reading(20, "active", { mainPid: 944 }))).toBe(false)
  })

  test("holds on a host with no unit until a verb reloads", () => {
    const mark = markUnloaded(10, undefined)
    expect(stillUnloaded(mark, undefined, undefined)).toBe(true)
    expect(stillUnloaded(mark, 12, undefined)).toBe(false)
  })
})
