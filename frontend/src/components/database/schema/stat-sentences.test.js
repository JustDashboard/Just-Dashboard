import { expect, test } from "bun:test"
import { statSentences } from "./stat-sentences"

const said = (stat) =>
  statSentences(stat).map((sentence) =>
    sentence.words.map((word) => (typeof word === "string" ? word : word.figure)).join(""),
  )

test("a table's rows and its dead ones are one sentence, with the share that makes it mean something", () => {
  expect(said({ rows: 9000, deadRows: 412 })).toEqual([
    "About 9,000 rows, and 412 dead rows, 4% of the table",
  ])
  expect(said({ rows: 1, deadRows: 1 })).toEqual(["About 1 row, and 1 dead row, 50% of the table"])
})

test("no dead rows is said without a share, and a sliver is never rounded to nothing", () => {
  expect(said({ rows: 9000, deadRows: 0 })).toEqual(["About 9,000 rows, and 0 dead rows"])
  expect(said({ rows: 1_000_000, deadRows: 3 })).toEqual([
    "About 1,000,000 rows, and 3 dead rows, under 1% of the table",
  ])
})

test("a count the engine does not keep is left out, never said as zero", () => {
  expect(said({})).toEqual(["Rows not counted"])
  expect(said({ rows: -1, deadRows: 7 })).toEqual(["Rows not counted; 7 dead rows"])
  expect(said({ rows: 12 })).toEqual(["About 12 rows"])
})

test("writes are three counts where the engine keeps three, and one where it keeps one", () => {
  expect(said({ rows: 5, inserts: 1204, updates: 310, deletes: 12 })[1]).toBe(
    "1,204 inserted, 310 updated and 12 deleted since the counters were reset",
  )
  expect(said({ rows: 5, updates: 1 })[1]).toBe("1 write")
  expect(said({ rows: 5, inserts: 4, updates: -1, deletes: 2 })).toEqual(["About 5 rows"])
})

test("what changed since the last analyze, and how the table is stored", () => {
  expect(said({ rows: 5, modsSinceAnalyze: 88 })[1]).toBe(
    "88 rows changed since it was last analyzed",
  )
  expect(said({ rows: 5, engine: "MergeTree", parts: 4 })[1]).toBe("Stored by MergeTree in 4 parts")
  expect(said({ rows: 5, engine: "InnoDB" })[1]).toBe("Stored by InnoDB")
  expect(said({ rows: 5, parts: 1 })[1]).toBe("Stored in 1 part")
})
