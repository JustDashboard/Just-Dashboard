import { describe, expect, test } from "bun:test"
import {
  boolWords,
  changeWords,
  filterParameters,
  fromRedisConfig,
  fromSettings,
  groupParameters,
  isOn,
  parameterProblem,
  readableValue,
} from "./settings-parameters-model"

const setting = (name, over = {}) => ({ name, value: "1", editable: true, ...over })

describe("one list from either route", () => {
  test("a SQL engine's category is a heading and what is under it", () => {
    const [work] = fromSettings([
      setting("work_mem", {
        value: "4096",
        unit: "kB",
        category: "Resource Usage / Memory",
        type: "integer",
        min: "64",
        max: "2147483647",
        default: "4096",
        context: "user",
        restartRequired: false,
      }),
    ])
    expect(work).toMatchObject({
      name: "work_mem",
      group: "Resource Usage",
      subgroup: "Memory",
      restart: false,
      pending: false,
      changed: false,
      editable: true,
      redacted: false,
    })
  })

  test("a parameter with no category is listed under Other, which comes last", () => {
    const groups = groupParameters(
      fromSettings([
        setting("zeta"),
        setting("alpha", { category: "InnoDB" }),
        setting("beta", { category: "General" }),
      ]),
    )
    expect(groups.map((group) => group.name)).toEqual(["General", "InnoDB", "Other"])
  })

  test("a key–value server's groups are its own, every parameter settable, a secret write-only", () => {
    const list = fromRedisConfig({
      supported: true,
      rewritable: false,
      groups: [
        { name: "memory", params: [{ name: "maxmemory", value: "0" }] },
        { name: "security", params: [{ name: "requirepass", value: "", secret: true, set: true }] },
      ],
    })
    expect(list.map((one) => [one.name, one.group, one.editable])).toEqual([
      ["maxmemory", "Memory", true],
      ["requirepass", "Security", true],
    ])
    expect(list[1].secret).toEqual({ set: true })
    expect(list[0].secret).toBeUndefined()
  })

  test("a group counts what differs from the default and what waits for a restart", () => {
    const [group] = groupParameters(
      fromSettings([
        setting("b", { category: "X", changed: true }),
        setting("a", { category: "X", pendingRestart: true, changed: true }),
        setting("c", { category: "X" }),
      ]),
    )
    expect(group.parameters.map((one) => one.name)).toEqual(["a", "b", "c"])
    expect(group.changed).toBe(2)
    expect(group.pending).toBe(1)
  })
})

describe("finding a parameter", () => {
  const list = fromSettings([
    setting("work_mem", { category: "Resource Usage / Memory", description: "query workspaces" }),
    setting("shared_buffers", { category: "Resource Usage / Memory", changed: true }),
    setting("wal_level", { category: "Write-Ahead Log", pendingRestart: true, changed: true }),
    setting("pgrst.jwt_secret", { value: "", redacted: true }),
  ])

  test("every word typed is somewhere in its name, its meaning, its heading or its value", () => {
    expect(filterParameters(list, "mem", "").map((one) => one.name)).toEqual([
      "work_mem",
      "shared_buffers",
    ])
    expect(filterParameters(list, "memory work", "").map((one) => one.name)).toEqual(["work_mem"])
    expect(filterParameters(list, "WORKSPACES", "").map((one) => one.name)).toEqual(["work_mem"])
    expect(filterParameters(list, "nothing-like-it", "")).toEqual([])
  })

  test("the chips narrow to what changed or what waits", () => {
    expect(filterParameters(list, "", "changed").map((one) => one.name)).toEqual([
      "shared_buffers",
      "wal_level",
    ])
    expect(filterParameters(list, "", "pending").map((one) => one.name)).toEqual(["wal_level"])
    expect(filterParameters(list, "wal", "changed")).toHaveLength(1)
  })

  test("a withheld value is never searched", () => {
    const hidden = fromSettings([setting("x.token", { value: "hunter2", redacted: true })])
    expect(filterParameters(hidden, "hunter2", "")).toEqual([])
  })
})

describe("a value in the unit a person thinks in", () => {
  test("pages and kilobytes become a size", () => {
    expect(readableValue({ value: "16384", unit: "8kB" })).toBe("128.0 MB")
    expect(readableValue({ value: "4096", unit: "kB" })).toBe("4.0 MB")
    expect(readableValue({ value: "2048", unit: "MB" })).toBe("2.0 GB")
  })

  test("long times become a duration", () => {
    expect(readableValue({ value: "300000", unit: "ms" })).toBe("5m")
    expect(readableValue({ value: "3600", unit: "s" })).toBe("1h")
    expect(readableValue({ value: "90", unit: "min" })).toBe("1h 30m")
  })

  test("a value that already reads well, an off number and a word are left alone", () => {
    expect(readableValue({ value: "200", unit: "ms" })).toBeUndefined()
    expect(readableValue({ value: "64", unit: "MB" })).toBeUndefined()
    expect(readableValue({ value: "-1", unit: "kB" })).toBeUndefined()
    expect(readableValue({ value: "0", unit: "ms" })).toBeUndefined()
    expect(readableValue({ value: "on" })).toBeUndefined()
    expect(readableValue({ value: "128" })).toBeUndefined()
  })
})

describe("a yes/no parameter keeps the engine's own words", () => {
  test("in their own case", () => {
    expect(boolWords("on")).toEqual({ on: "on", off: "off" })
    expect(boolWords("OFF")).toEqual({ on: "ON", off: "OFF" })
    expect(boolWords("1")).toEqual({ on: "1", off: "0" })
    expect(boolWords("yes")).toEqual({ on: "yes", off: "no" })
    expect(boolWords("something")).toEqual({ on: "on", off: "off" })
  })

  test("and is read whichever pair it uses", () => {
    expect(isOn("ON")).toBe(true)
    expect(isOn("true")).toBe(true)
    expect(isOn("off")).toBe(false)
    expect(isOn("0")).toBe(false)
  })
})

describe("a value the server would refuse is said before it is sent", () => {
  const [integer] = fromSettings([
    setting("bgwriter_delay", { type: "integer", min: "10", max: "10000", unit: "ms" }),
  ])
  const [huge] = fromSettings([
    setting("max_connect_errors", { type: "integer", min: "1", max: "18446744073709551615" }),
  ])
  const [level] = fromSettings([
    setting("wal_level", { type: "enum", enum: ["minimal", "replica", "logical"] }),
  ])

  test("a number outside its range, with the range named", () => {
    expect(parameterProblem(integer, "210")).toBeUndefined()
    expect(parameterProblem(integer, "5")).toBe("Between 10 and 10000 ms.")
    expect(parameterProblem(integer, "20000")).toBe("Between 10 and 10000 ms.")
    expect(parameterProblem(integer, "")).toBe("A number.")
    expect(parameterProblem(integer, "soon")).toBe("A whole number.")
  })

  test("a 64-bit bound is compared digit for digit", () => {
    expect(parameterProblem(huge, "18446744073709551615")).toBeUndefined()
    expect(parameterProblem(huge, "18446744073709551616")).toBeDefined()
    expect(parameterProblem(huge, "0")).toBeDefined()
  })

  test("a unit, an octal or a hexadecimal number is the engine's to judge", () => {
    expect(parameterProblem(integer, "2s")).toBeUndefined()
    expect(parameterProblem(integer, "64MB")).toBeUndefined()
    expect(parameterProblem(integer, "0600")).toBeUndefined()
    expect(parameterProblem(integer, "0x1F")).toBeUndefined()
  })

  test("a closed set, a yes/no and a secret", () => {
    expect(parameterProblem(level, "logical")).toBeUndefined()
    expect(parameterProblem(level, "LOGICAL")).toBeUndefined()
    expect(parameterProblem(level, "archive")).toBe("One of minimal, replica, logical.")
    const [flag] = fromSettings([setting("fsync", { type: "bool", value: "on" })])
    expect(parameterProblem(flag, "off")).toBeUndefined()
    expect(parameterProblem(flag, "maybe")).toBe("On or off.")
    const [secret] = fromRedisConfig({
      supported: true,
      rewritable: false,
      groups: [{ name: "security", params: [{ name: "requirepass", value: "", secret: true }] }],
    })
    expect(parameterProblem(secret, "")).toBeDefined()
    expect(parameterProblem(secret, "s3cret")).toBeUndefined()
  })

  test("a parameter the engine publishes no type for takes what is typed", () => {
    const [plain] = fromRedisConfig({
      supported: true,
      rewritable: false,
      groups: [{ name: "memory", params: [{ name: "maxmemory-policy", value: "noeviction" }] }],
    })
    expect(parameterProblem(plain, "allkeys-lru")).toBeUndefined()
    expect(parameterProblem(plain, "")).toBeUndefined()
  })
})

describe("what a change did", () => {
  const change = (over) => ({
    name: "x",
    value: "1",
    statements: [],
    persisted: true,
    restartRequired: false,
    ...over,
  })

  test("in effect, stored for a restart, or kept only until one", () => {
    expect(changeWords(change({}))).toBe("In effect now.")
    expect(changeWords(change({ restartRequired: true }))).toContain("restarted")
    expect(changeWords(change({ persisted: false }))).toContain("not kept across a restart")
    expect(
      changeWords(change({ note: "MariaDB keeps this until the server restarts." })),
    ).toContain("MariaDB keeps this")
  })
})
