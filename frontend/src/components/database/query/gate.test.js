import { describe, expect, test } from "bun:test"
import { gate, reasonsText, riskWord } from "./gate"

const read = { destructive: false, level: "read", reasons: [] }
const write = { destructive: false, level: "medium", reasons: ["inserts rows"] }
const drop = { destructive: true, level: "critical", reasons: ["drops a table", "deletes rows"] }
const admin = { canRun: true, canDestroy: true, readOnly: false }

describe("whether a statement may be sent", () => {
  test("a read runs without a question", () => {
    expect(gate(read, admin)).toEqual({ run: true, confirm: false })
    expect(gate(write, admin)).toEqual({ run: true, confirm: false })
  })
  test("a statement that destroys is asked about first", () => {
    expect(gate(drop, admin)).toEqual({ run: true, confirm: true })
  })
  test("a role that may not run statements is told so, whatever the statement", () => {
    expect(gate(read, { ...admin, canRun: false })).toEqual({
      run: false,
      why: "Your role reads this database and may not run statements.",
    })
  })
  test("a role without the destructive capability is refused what destroys, with what it does", () => {
    expect(gate(drop, { ...admin, canDestroy: false })).toEqual({
      run: false,
      why: "Your role may not run a statement that destroys. This one drops a table and deletes rows.",
    })
    expect(gate(write, { ...admin, canDestroy: false })).toEqual({ run: true, confirm: false })
  })
  test("a protected connection runs only what reads", () => {
    expect(gate(read, { ...admin, readOnly: true })).toEqual({ run: true, confirm: false })
    expect(gate(write, { ...admin, readOnly: true })).toEqual({
      run: false,
      why: "This connection is protected, so only statements that read are run. This one inserts rows.",
    })
    expect(gate(drop, { ...admin, readOnly: true }).run).toBe(false)
  })
  test("the server's reasons are one sentence", () => {
    expect(reasonsText(read)).toBe("")
    expect(reasonsText({ ...drop, reasons: ["a", "b", "c"] })).toBe("a, b and c")
  })
  test("one word for what a statement would do", () => {
    expect([read, write, drop].map(riskWord)).toEqual(["reads", "writes", "destroys"])
    expect(riskWord({ destructive: true, level: "high", reasons: [] })).toBe("destroys")
  })
})
