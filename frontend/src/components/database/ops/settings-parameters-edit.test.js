import { describe, expect, test } from "bun:test"
import {
  configBody,
  parameterChanges,
  rangeWords,
  settingBody,
  shownValue,
  startValue,
  valueFacts,
} from "./settings-parameters-edit"

const parameter = (name, over = {}) => ({
  name,
  value: "1",
  group: "Other",
  restart: false,
  pending: false,
  changed: false,
  editable: true,
  redacted: false,
  ...over,
})
const DELAY = parameter("bgwriter_delay", {
  value: "210",
  unit: "ms",
  type: "integer",
  default: "200",
  min: "10",
  max: "10000",
  changed: true,
})
const FSYNC = parameter("fsync", { value: "on", type: "bool", default: "on" })
const SECRET = parameter("requirepass", { value: "", secret: { set: true } })

describe("a parameter's control", () => {
  test("starts from the engine's own word, and from nothing for a secret", () => {
    expect(startValue(DELAY)).toBe("210")
    expect(startValue(FSYNC)).toBe("on")
    expect(startValue(parameter("x", { value: "ON", type: "bool" }))).toBe("ON")
    expect(startValue(SECRET)).toBe("")
  })

  test("shows the draft, the default while a reset is staged, or what was just saved", () => {
    expect(shownValue(DELAY, undefined)).toBe("210")
    expect(shownValue(DELAY, { value: "250" })).toBe("250")
    expect(shownValue(DELAY, { reset: true })).toBe("200")
    expect(shownValue(DELAY, undefined, "250")).toBe("250")
  })
})

describe("the rows a save sends", () => {
  const list = [DELAY, FSYNC, SECRET]

  test("only the ones that differ, in the list's order", () => {
    const changes = parameterChanges(list, {
      fsync: { value: "off" },
      bgwriter_delay: { value: "250" },
      requirepass: { value: "" },
    })
    expect(changes.map((change) => change.parameter.name)).toEqual(["bgwriter_delay", "fsync"])
  })

  test("a value typed back to what it was is not a change", () => {
    expect(parameterChanges(list, { bgwriter_delay: { value: "210" } })).toEqual([])
    // Nor one typed back to what was saved a moment ago and not yet read back.
    expect(
      parameterChanges(list, { bgwriter_delay: { value: "250" } }, { bgwriter_delay: "250" }),
    ).toEqual([])
  })

  test("a value the server would refuse is said with it", () => {
    const [change] = parameterChanges(list, { bgwriter_delay: { value: "5" } })
    expect(change.problem).toBe("Between 10 and 10000 ms.")
  })

  test("a reset is a change only of a parameter that is not at its default", () => {
    expect(parameterChanges(list, { bgwriter_delay: { reset: true } })).toHaveLength(1)
    expect(parameterChanges(list, { fsync: { reset: true } })).toEqual([])
  })

  test("each sends what the dialog sent: the name with a trimmed value, or with reset", () => {
    expect(settingBody({ parameter: DELAY, edit: { value: " 250 " } })).toEqual({
      name: "bgwriter_delay",
      value: "250",
    })
    expect(settingBody({ parameter: DELAY, edit: { reset: true } })).toEqual({
      name: "bgwriter_delay",
      reset: true,
    })
    expect(configBody({ parameter: SECRET, edit: { value: "s3cret" } }, true)).toEqual({
      name: "requirepass",
      value: "s3cret",
      rewrite: true,
    })
    expect(configBody({ parameter: SECRET, edit: { value: "s3cret" } }, false)).toEqual({
      name: "requirepass",
      value: "s3cret",
    })
  })
})

describe("the line under a value", () => {
  test("says the value in a unit a person thinks in, and the default when it is not at it", () => {
    const buffers = parameter("shared_buffers", {
      value: "32768",
      unit: "8kB",
      default: "16384",
      changed: true,
    })
    expect(valueFacts(buffers, "32768")).toBe("256.0 MB · default 16384")
    expect(valueFacts(DELAY, "210")).toBe("default 200")
    // At its default there is nothing to add.
    expect(valueFacts(FSYNC, "on")).toBe("")
    // Edited away from the default, the default is what it would go back to.
    expect(valueFacts(FSYNC, "off")).toBe("default on")
    expect(valueFacts(DELAY, "200")).toBe("")
  })

  test("a range is said whole, or not at all", () => {
    expect(rangeWords(DELAY)).toBe("10 to 10000")
    expect(rangeWords({ min: "0" })).toBe("0 to …")
    expect(rangeWords({})).toBeUndefined()
  })
})
