import { describe, expect, test } from "bun:test"
import { housekeeping } from "./housekeeping"

describe("the commands that ask about a server rather than use it", () => {
  test("are told by their first word, however the server spells them", () => {
    for (const command of [
      "INFO",
      "info",
      "COMMAND INFO",
      "CONFIG|GET",
      "client|list",
      "HELLO",
      "PING",
    ]) {
      expect(housekeeping(command)).toBe(true)
    }
  })

  test("a command on keys is not one, nor a walk of them", () => {
    for (const command of ["SET", "HSET", "GET", "SCAN", "TYPE", "FLUSHDB", "XADD", "INFORM"]) {
      expect(housekeeping(command)).toBe(false)
    }
  })
})
