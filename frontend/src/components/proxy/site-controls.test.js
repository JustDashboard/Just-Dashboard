import { describe, expect, test } from "bun:test"
import { checkState, controlSetting, supportWord, validPath } from "./site-controls"

describe("site controls", () => {
  test("a control reads its setting and what the build lacks", () => {
    expect(controlSetting({ configured: false })).toBe("not set")
    expect(controlSetting({ configured: true, setting: "10r/s, burst 20" })).toBe("10r/s, burst 20")
    expect(supportWord({ support: "missing" })).toBe("this nginx lacks its module")
    expect(supportWord({ support: "built-in" })).toBeUndefined()
  })

  test("only what nginx answered verifies a control", () => {
    expect(checkState({ state: "verified" })).toEqual({ label: "Verified", tone: "running" })
    expect(checkState({ state: "not-effective" }).tone).toBe("danger")
    expect(checkState({ state: "not-measured" }).tone).toBe("unknown")
  })

  test("a path is one the server takes", () => {
    for (const ok of ["", "/", "/assets/app.css"]) expect(validPath(ok)).toBe(true)
    for (const bad of ["app.css", "/a b", "/x#y"]) expect(validPath(bad)).toBe(false)
  })
})
