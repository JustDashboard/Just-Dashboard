import { describe, expect, test } from "bun:test"
import { restartAnswer, setupPhases, setupStart } from "./statements-setup-model"

const PRELOAD = `ALTER SYSTEM SET "shared_preload_libraries" = 'timescaledb', 'pg_stat_statements'`
const enable = (extra = {}) => ({
  extension: "pg_stat_statements",
  available: true,
  preloaded: false,
  configured: false,
  preload: PRELOAD,
  ...extra,
})

describe("where a server's setup starts", () => {
  test("at the library list, where the module was never loaded", () => {
    expect(setupStart(enable())).toBe("preload")
  })

  test("at the restart, where the list names it and the server has not restarted since", () => {
    expect(setupStart(enable({ configured: true, preload: undefined }))).toBe("restart")
  })

  test("nowhere, where one statement is enough and the page offers that alone", () => {
    expect(
      setupStart(enable({ preloaded: true, preload: undefined, sql: "CREATE EXTENSION" })),
    ).toBeUndefined()
  })

  test("nowhere, where the module is not installed: a server told to load it would not start", () => {
    expect(setupStart(enable({ available: false }))).toBeUndefined()
  })

  test("nowhere, where the list cannot be changed from here", () => {
    const blocked = enable({ preload: undefined, blocked: "set on the command line" })
    expect(setupStart(blocked)).toBeUndefined()
    expect(setupStart(undefined)).toBeUndefined()
  })
})

describe("whether the dashboard can restart it", () => {
  const container = { via: "docker", start: false, stop: true, restart: true }

  test("where the summary offers a restart and the role may interrupt", () => {
    expect(restartAnswer(container, true)).toEqual({ can: true })
  })

  test("not for a role that may not, though the server could be", () => {
    expect(restartAnswer(container, false)).toEqual({
      can: false,
      reason: "Your role may not restart a database.",
    })
  })

  test("not for a server on another machine, in the server's own words", () => {
    const remote = {
      via: "",
      start: false,
      stop: false,
      restart: false,
      reason: "The server is on another machine (db.internal) and is started and stopped there.",
    }
    expect(restartAnswer(remote, true)).toEqual({ can: false, reason: remote.reason })
    expect(restartAnswer({ ...remote, reason: "No unit found" }, true).reason).toBe(
      "No unit found.",
    )
  })

  test("not for a server that is down, nor one whose summary has not answered", () => {
    expect(restartAnswer({ ...container, restart: false }, true).can).toBe(false)
    expect(restartAnswer(undefined, true).can).toBe(false)
  })
})

describe("how far along it is", () => {
  const states = (progress) => setupPhases(progress).map((phase) => phase.state)

  test("waiting at a step is everything before it done and nothing running", () => {
    expect(states({ step: "preload", state: "waiting" })).toEqual(["pending", "pending", "pending"])
    expect(states({ step: "restart", state: "waiting" })).toEqual(["passed", "pending", "pending"])
  })

  test("the step at work runs, and a failure stays where it happened", () => {
    expect(states({ step: "restart", state: "running" })).toEqual(["passed", "running", "pending"])
    expect(states({ step: "extension", state: "failed" })).toEqual(["passed", "passed", "failed"])
  })

  test("done is every step passed, under the steps' own names", () => {
    expect(states({ step: "extension", state: "done" })).toEqual(["passed", "passed", "passed"])
    expect(setupPhases({ step: "preload", state: "waiting" }).map((phase) => phase.label)).toEqual([
      "Load the module at start",
      "Restart the server",
      "Create the extension",
    ])
  })
})
