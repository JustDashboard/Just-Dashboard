import { describe, expect, test } from "bun:test"
import {
  EMPTY_TRANSCRIPT,
  appendTranscript,
  clearTranscript,
  consoleRows,
} from "./transcript-buffer"

const event = (seq, text, fields = {}) => ({
  seq,
  stepId: 105,
  ts: "2026-09-03T12:00:00Z",
  stream: "stdout",
  text,
  truncated: false,
  ...fields,
})

describe("appendTranscript", () => {
  test("lines that follow the last are read alone and added, the rows already held kept", () => {
    const first = appendTranscript(EMPTY_TRANSCRIPT, [event(1, "one\n"), event(2, "two\nthree\n")])
    expect(first.rows.map((row) => [row.id, row.line.number, row.line.text])).toEqual([
      ["1:0", 1, "one"],
      ["2:0", 2, "two"],
      ["2:1", 3, "three"],
    ])
    const next = appendTranscript(first, [event(3, "#14 ERROR: failed to build\n")])
    expect(next.epoch).toBe(first.epoch)
    // The same objects: a memoised line is not drawn again.
    expect(next.rows[0]).toBe(first.rows[0])
    expect(next.rows.at(-1)).toMatchObject({ id: "3:0", seq: 3, line: { number: 4 } })
    expect(next).toMatchObject({ lines: 4, errors: 1, next: 5 })
    expect(next.perStep.get(105)).toBe(4)
    // The counts are a new map, so a reader sees them change.
    expect(next.perStep).not.toBe(first.perStep)
  })

  test("a sequence already held, or one out of order, rebuilds the whole and says so", () => {
    const held = appendTranscript(EMPTY_TRANSCRIPT, [event(1, "a\n"), event(3, "c\n")])
    const next = appendTranscript(held, [event(2, "b\n"), event(3, "c again\n")])
    expect(next.epoch).toBe(held.epoch + 1)
    expect(next.rows.map((row) => row.line.text)).toEqual(["a", "b", "c again"])
    expect(next.rows.map((row) => row.line.number)).toEqual([1, 2, 3])
    expect(next.lines).toBe(3)
  })

  test("a batch out of order within itself is sorted and each sequence kept once", () => {
    const next = appendTranscript(EMPTY_TRANSCRIPT, [
      event(2, "b\n"),
      event(1, "a\n"),
      event(2, "b2\n"),
    ])
    expect(next.rows.map((row) => row.line.text)).toEqual(["a", "b2"])
    expect(next.epoch).toBe(0)
  })

  test("past the cap the oldest go, and the lines after them keep their numbers", () => {
    let buffer = EMPTY_TRANSCRIPT
    for (let seq = 1; seq <= 6; seq++) {
      buffer = appendTranscript(
        buffer,
        [
          event(seq, seq === 2 ? "fatal: broke\n" : `line ${seq}\n`, {
            stepId: seq < 3 ? 104 : 105,
          }),
        ],
        4,
      )
    }
    expect(buffer.events.map((entry) => entry.seq)).toEqual([3, 4, 5, 6])
    expect(buffer.rows.map((row) => row.line.number)).toEqual([3, 4, 5, 6])
    expect(buffer).toMatchObject({ lines: 4, errors: 0, next: 7, epoch: 0 })
    expect(buffer.perStep.has(104)).toBe(false)
    expect(buffer.perStep.get(105)).toBe(4)
  })

  test("the engine's status stream is its own voice, escapes are dropped and truncation noted once", () => {
    const rows = consoleRows([
      event(1, "Resolved main to a12bc34\n", { stream: "status" }),
      event(2, "\x1b[32mgreen\x1b[0m\r\nsecond\n", { truncated: true }),
    ])
    expect(rows[0].line.kind).toBe("note")
    expect(rows.map((row) => row.line.text)).toEqual([
      "Resolved main to a12bc34",
      "green",
      "second",
    ])
    expect(rows.map((row) => row.truncated)).toEqual([false, false, true])
  })

  test("a resync starts a new transcript", () => {
    const held = appendTranscript(EMPTY_TRANSCRIPT, [event(9, "old\n")])
    const cleared = clearTranscript(held)
    expect(cleared).toMatchObject({
      rows: [],
      events: [],
      lines: 0,
      next: 1,
      epoch: held.epoch + 1,
    })
    const next = appendTranscript(cleared, [event(4, "new\n")])
    expect(next.rows[0].line).toMatchObject({ number: 1, text: "new" })
  })
})
