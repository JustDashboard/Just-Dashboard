import { describe, expect, test } from "bun:test"
import { fieldValue, parseStructured } from "./log-format"

describe("structured log lines", () => {
  test("a Go slog line becomes a message, a level and its remaining fields", () => {
    const line = parseStructured(
      '{"time":"2026-09-11T10:04:02.114Z","level":"INFO","msg":"listening","addr":":8080"}',
    )
    expect(line).not.toBeNull()
    expect(line.message).toBe("listening")
    expect(line.level).toBe("info")
    // The timestamp is dropped: the viewer already renders one in its own column.
    expect(line.fields).toEqual([["addr", ":8080"]])
  })

  test("the other common field names are recognised", () => {
    expect(parseStructured('{"ts":1,"severity":"ERROR","message":"boom"}')).toEqual({
      message: "boom",
      level: "error",
      fields: [],
    })
    expect(parseStructured('{"@timestamp":"x","@level":"warn","@message":"careful"}')).toEqual({
      message: "careful",
      level: "warn",
      fields: [],
    })
  })

  test("a level is optional", () => {
    expect(parseStructured('{"msg":"no level here","count":3}')).toEqual({
      message: "no level here",
      level: undefined,
      fields: [["count", 3]],
    })
  })

  test("plain container output is left completely alone", () => {
    expect(parseStructured("172.18.0.1 - GET / 200")).toBeNull()
    expect(parseStructured("")).toBeNull()
    // Looks like JSON, is not.
    expect(parseStructured('{"msg": broken}')).toBeNull()
    // JSON, but not an object.
    expect(parseStructured("[1,2,3]")).toBeNull()
  })

  test("a JSON object with no message field is not rearranged", () => {
    expect(parseStructured('{"level":"info","status":200}')).toBeNull()
  })

  test("a message that is not a string does not count as one", () => {
    expect(parseStructured('{"msg":{"nested":true},"level":"info"}')).toBeNull()
  })

  test("field values survive being something other than a string", () => {
    expect(fieldValue("plain")).toBe("plain")
    expect(fieldValue(42)).toBe("42")
    expect(fieldValue({ a: 1 })).toBe('{"a":1}')
    expect(fieldValue(null)).toBe("null")
  })
})
