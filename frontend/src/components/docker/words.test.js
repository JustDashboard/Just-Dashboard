import { describe, expect, test } from "bun:test"
import { changeWords, crashed, exitWords } from "./words"

describe("exitWords", () => {
  // The three signals a stop sends read as a stop, not as a failure.
  test("names the signals a stop sends", () => {
    expect(exitWords(0)).toBe("exited cleanly")
    expect(exitWords(130)).toBe("interrupted")
    expect(exitWords(137)).toBe("killed")
    expect(exitWords(143)).toBe("stopped")
    expect(exitWords(1)).toBe("exit 1")
  })

  test("says a kill for memory, and nothing for an unknown exit", () => {
    expect(exitWords(137, true)).toBe("killed for memory")
    expect(exitWords(undefined, true)).toBe("killed for memory")
    expect(exitWords(undefined)).toBeUndefined()
  })

  test("calls only the program's own failures a crash", () => {
    expect(crashed(1)).toBe(true)
    expect(crashed(2)).toBe(true)
    expect(crashed(0)).toBe(false)
    expect(crashed(137)).toBe(false)
    expect(crashed(143)).toBe(false)
  })
})

describe("changeWords", () => {
  const event = (action, over = {}) => ({ action, level: "info", ...over })

  test("says a health event the same way on every page", () => {
    expect(changeWords(event("health_status: unhealthy"))).toEqual({
      verb: "failing its check",
      tone: "danger",
    })
    expect(changeWords(event("health_status: healthy"))).toEqual({
      verb: "passing its check again",
      tone: "running",
    })
  })

  test("a die is a failure only when the program failed", () => {
    expect(changeWords(event("die", { level: "error", exitCode: "1" }))).toEqual({
      verb: "exited 1",
      tone: "danger",
    })
    expect(changeWords(event("die", { level: "error", exitCode: "137" }))).toEqual({
      verb: "killed",
      tone: "stopped",
    })
    expect(changeWords(event("die"))).toEqual({ verb: "exited", tone: "stopped" })
    expect(changeWords(event("die", { exitCode: "1" }), true)).toEqual({
      verb: "killed for memory",
      tone: "danger",
    })
  })

  test("leaves out what a reader does not need", () => {
    expect(changeWords(event("exec_start"))).toBeUndefined()
    expect(changeWords(event("kill"))).toBeUndefined()
    expect(changeWords(event("stop"))).toBeUndefined()
  })
})
