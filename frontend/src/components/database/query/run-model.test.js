import { describe, expect, test } from "bun:test"
import {
  canFetchMore,
  definesSchema,
  elapsedText,
  goDurationMs,
  messagesOf,
  spanText,
  stepOutcome,
  stepToShow,
  runKey,
  stepLimit,
  stepsOfQuery,
  stepsOfScript,
  withRefetched,
} from "./run-model"

const READ = { destructive: false, level: "read", reasons: [] }
const WRITE = { destructive: false, level: "medium", reasons: ["inserts rows"] }
const rows = (count, over = {}) => ({
  columns: ["id"],
  types: ["INT8"],
  rows: Array.from({ length: count }, (_, i) => [String(i)]),
  rowCount: count,
  rowsAffected: 0,
  duration: "1.5ms",
  truncated: false,
  statement: "",
  ...over,
})
const written = (affected) => rows(0, { columns: [], types: [], rows: [], rowsAffected: affected })

const run = (over) => ({
  queryId: "q",
  phase: "done",
  startedAt: 1000,
  finishedAt: 1104,
  sql: "",
  scope: "all",
  offset: 0,
  line: 1,
  limit: 500,
  script: true,
  steps: [],
  failed: -1,
  ...over,
})

describe("a Go duration", () => {
  test("is read in milliseconds, whatever its unit", () => {
    expect(goDurationMs("1.5ms")).toBe(1.5)
    expect(goDurationMs("700µs")).toBeCloseTo(0.7, 6)
    expect(goDurationMs("700us")).toBeCloseTo(0.7, 6)
    expect(goDurationMs("2s")).toBe(2000)
    expect(goDurationMs("1m2.5s")).toBe(62_500)
    expect(goDurationMs("1h1m")).toBe(3_660_000)
    expect(goDurationMs("")).toBe(0)
    expect(goDurationMs("soon")).toBe(0)
  })
  test("is said as a reader says it", () => {
    expect([0.7, 2.26, 12.4, 104, 1240, 125_000, 120_000].map(spanText)).toEqual([
      "0.7 ms",
      "2.3 ms",
      "12 ms",
      "104 ms",
      "1.24 s",
      "2 min 5 s",
      "2 min",
    ])
    expect(spanText(-1)).toBe("")
  })
  test("a run still out shows a clock", () => {
    expect([0, 999, 7000, 61_000, 754_000].map(elapsedText)).toEqual([
      "0:00",
      "0:00",
      "0:07",
      "1:01",
      "12:34",
    ])
  })
})

describe("what the server answered, as steps", () => {
  test("one statement is a script of one, on its own line of the editor", () => {
    const [step] = stepsOfQuery({ result: rows(3), risk: READ }, "select 1", 7)
    expect(step).toMatchObject({
      index: 0,
      sql: "select 1",
      line: 7,
      status: "ok",
      durationMs: 1.5,
    })
    expect(step.risk).toBe(READ)
  })
  test("a script's statements are placed on their lines of the editor's text", () => {
    const steps = stepsOfScript(
      {
        statements: [
          {
            index: 0,
            sql: "select 1",
            line: 1,
            risk: READ,
            status: "ok",
            result: rows(1),
            durationMs: 0,
          },
          {
            index: 1,
            sql: "select x",
            line: 3,
            risk: READ,
            status: "error",
            error: "no x",
            durationMs: 4,
          },
          { index: 2, sql: "select 2", line: 4, risk: READ, status: "skipped", durationMs: 0 },
        ],
        failed: 1,
        transaction: "read_only",
        durationMs: 6,
        risk: READ,
      },
      10,
    )
    expect(steps.map((step) => step.line)).toEqual([10, 12, 13])
    // The result's own duration is finer than the step's whole milliseconds.
    expect(steps.map((step) => step.durationMs)).toEqual([1.5, 4, 0])
    expect(steps.map(stepOutcome)).toEqual(["1 row", "failed", "not run"])
  })
  test("what a step did, in a few words", () => {
    const of = (result) =>
      stepOutcome({ index: 0, sql: "", line: 1, status: "ok", result, durationMs: 0 })
    expect(of(rows(120))).toBe("120 rows")
    expect(of(rows(500, { truncated: true }))).toBe("500 rows, and more")
    expect(of(written(3))).toBe("3 rows changed")
    expect(of(written(1))).toBe("1 row changed")
    expect(of(written(0))).toBe("done")
    expect(of(undefined)).toBe("done")
  })
})

describe("the run told in order", () => {
  const ok = { index: 0, sql: "select 1", line: 1, status: "ok", result: rows(1), durationMs: 2 }
  const failed = {
    index: 1,
    sql: "select x",
    line: 2,
    status: "error",
    error: "no x",
    durationMs: 1,
  }
  const skipped = { index: 2, sql: "select 2", line: 3, status: "skipped", durationMs: 0 }

  test("each statement, then how the whole ended", () => {
    const lines = messagesOf(
      run({ steps: [ok, failed, skipped], failed: 1, transaction: "read_only" }),
    )
    expect(lines.map((line) => [line.tone, line.text])).toEqual([
      ["success", "1 row"],
      ["danger", "failed"],
      ["default", "not run"],
      ["default", "Run in one read-only scope: nothing could be written."],
      ["danger", "Stopped at statement 2 of 3: 1 statement ran, 1 did not."],
    ])
    expect(lines[1].detail).toBe("no x")
  })
  test("a script that ran to its end says how long it took", () => {
    const lines = messagesOf(run({ steps: [ok, { ...ok, index: 1 }], transaction: "committed" }))
    expect(lines.at(-2)?.text).toBe("One transaction, committed: every statement took effect.")
    expect(lines.at(-1)?.text).toBe("2 statements ran in 104 ms.")
  })
  test("a rolled-back transaction is said as that", () => {
    const lines = messagesOf(run({ steps: [ok, failed], failed: 1, transaction: "rolled_back" }))
    expect(lines.find((line) => line.key === "transaction")).toMatchObject({
      tone: "warning",
      text: "One transaction, rolled back: nothing took effect.",
    })
  })
  test("a run that was not sent says so and nothing else", () => {
    expect(messagesOf(run({ refused: "Your role may not run this." }))).toEqual([
      {
        key: "refused",
        tone: "warning",
        text: "Nothing was run.",
        detail: "Your role may not run this.",
      },
    ])
  })
  test("a cancelled run, a refused request and no run at all", () => {
    expect(messagesOf(run({ cancelled: true })).map((line) => line.key)).toEqual(["cancelled"])
    expect(messagesOf(run({ error: "too many statements" }))[0]).toMatchObject({
      tone: "danger",
      detail: "too many statements",
    })
    expect(messagesOf(undefined)).toEqual([])
    // A run still out has no ending to tell.
    expect(
      messagesOf(run({ phase: "running", steps: [ok, ok], finishedAt: undefined })),
    ).toHaveLength(2)
  })
})

describe("which result is shown when a run lands", () => {
  const ok = (index, result) => ({ index, sql: "", line: 1, status: "ok", result, durationMs: 0 })
  test("the statement that failed", () => {
    expect(
      stepToShow(run({ steps: [ok(0, rows(1)), { ...ok(1), status: "error" }], failed: 1 })),
    ).toBe(1)
  })
  test("else the last that returned rows, else the last", () => {
    expect(stepToShow(run({ steps: [ok(0, written(1)), ok(1, rows(2)), ok(2, written(1))] }))).toBe(
      1,
    )
    expect(stepToShow(run({ steps: [ok(0, written(1)), ok(1, written(2))] }))).toBe(1)
    expect(stepToShow(run({ steps: [] }))).toBe(0)
  })
})

describe("fetching more of a cut result", () => {
  const step = (risk, truncated) => ({
    index: 0,
    sql: "",
    line: 1,
    status: "ok",
    risk,
    result: rows(500, { truncated }),
    durationMs: 0,
  })
  test("only a statement that reads is run again, and only below the most a run shows", () => {
    expect(canFetchMore(step(READ, true), 500, 5000)).toBe(true)
    expect(canFetchMore(step(READ, true), 5000, 5000)).toBe(false)
    expect(canFetchMore(step(READ, false), 500, 5000)).toBe(false)
    expect(canFetchMore(step(WRITE, true), 500, 5000)).toBe(false)
    expect(canFetchMore(step(undefined, true), 500, 5000)).toBe(false)
  })
})

describe("one statement of a run asked again for more rows", () => {
  const steps = [
    {
      index: 0,
      sql: "select 1",
      line: 1,
      risk: READ,
      status: "ok",
      result: rows(1),
      durationMs: 1,
    },
    {
      index: 1,
      sql: "select * from customers",
      line: 1,
      risk: READ,
      status: "ok",
      result: rows(500, { truncated: true }),
      durationMs: 9,
    },
    {
      index: 2,
      sql: "select nope",
      line: 2,
      risk: READ,
      status: "error",
      error: "no",
      durationMs: 0,
    },
  ]
  const ran = run({ steps, failed: 2, limit: 500, script: true })
  test("takes the new rows and leaves every other statement what it had", () => {
    const next = withRefetched(
      ran,
      1,
      { result: rows(1000, { truncated: true }), risk: READ },
      1000,
    )
    expect(next).toHaveLength(3)
    expect(next[0]).toBe(steps[0])
    expect(next[2]).toBe(steps[2])
    expect(next[1].result.rowCount).toBe(1000)
    expect(next[1].limit).toBe(1000)
    expect(next[1].sql).toBe("select * from customers")
  })
  test("is then asked for more from where it stands, not from the run's first limit", () => {
    const [, again] = withRefetched(
      ran,
      1,
      { result: rows(1000, { truncated: true }), risk: READ },
      1000,
    )
    expect(stepLimit(ran, steps[1])).toBe(500)
    expect(stepLimit(ran, again)).toBe(1000)
    expect(canFetchMore(again, stepLimit(ran, again), 5000)).toBe(true)
    expect(canFetchMore(again, 5000, 5000)).toBe(false)
  })
  test("the run is the same run whatever request of it is out", () => {
    expect(runKey({ ...ran, key: "first", queryId: "second" })).toBe("first")
    expect(runKey({ ...ran, queryId: "only" })).toBe("only")
  })
})

describe("whether a run may have changed what the connection holds", () => {
  const made = (sql, risk = WRITE) =>
    run({ sql, risk, steps: [{ index: 0, sql, line: 1, risk, status: "ok", durationMs: 1 }] })
  test("a table made, altered, dropped or renamed is worth reading the schema again for", () => {
    expect(definesSchema(made("create table t(id int)"))).toBe(true)
    expect(definesSchema(made("-- new\n  ALTER TABLE t add c int"))).toBe(true)
    expect(definesSchema(made("/* gone */ drop view v"))).toBe(true)
    expect(definesSchema(made("rename table a to b"))).toBe(true)
  })
  test("rows written or read are not", () => {
    expect(definesSchema(made("insert into t values (1)"))).toBe(false)
    expect(definesSchema(made("select 'create table'", READ))).toBe(false)
  })
  test("any statement of a script counts, and a run that never left does not", () => {
    const script = run({
      sql: "select 1; create index i on t(c)",
      risk: WRITE,
      steps: [
        { index: 0, sql: "select 1", line: 1, risk: READ, status: "ok", durationMs: 1 },
        {
          index: 1,
          sql: "create index i on t(c)",
          line: 1,
          risk: WRITE,
          status: "ok",
          durationMs: 1,
        },
      ],
    })
    expect(definesSchema(script)).toBe(true)
    expect(definesSchema(run({ sql: "drop table t", risk: WRITE, refused: "not allowed" }))).toBe(
      false,
    )
  })
})
