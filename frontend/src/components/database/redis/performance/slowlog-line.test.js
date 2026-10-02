import { describe, expect, test } from "bun:test"
import { argsAfterCommand } from "./slowlog-line"

describe("a slow command's arguments", () => {
  test("the command's own word is not said twice", () => {
    expect(argsAfterCommand("CLIENT", ["client", "list"])).toEqual(["list"])
    expect(argsAfterCommand("KEYS", ["KEYS", "*"])).toEqual(["*"])
  })

  test("a command of two words loses both", () => {
    expect(argsAfterCommand("CONFIG GET", ["config", "get", "maxmemory"])).toEqual(["maxmemory"])
  })

  test("arguments that do not begin with the command are kept whole", () => {
    expect(argsAfterCommand("GET", ["session:1"])).toEqual(["session:1"])
    expect(argsAfterCommand("SET", ["set", "set", "value"])).toEqual(["set", "value"])
    expect(argsAfterCommand("PING", [])).toEqual([])
  })
})
