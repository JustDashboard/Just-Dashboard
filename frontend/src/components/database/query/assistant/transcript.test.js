import { describe, expect, test } from "bun:test"
import {
  ATTACH_CELL,
  ATTACH_COLUMNS,
  ATTACH_ROWS,
  EMPTY_TRANSCRIPT,
  answering,
  applyEvent,
  askedOf,
  attachable,
  beginTurn,
  decide,
  endTurn,
  isStatementFence,
  parseEvent,
  pendingCalls,
  planSource,
  readLines,
  statementTitle,
} from "./transcript"

const start = {
  type: "start",
  conversation: "c0ffee",
  provider: "anthropic",
  model: "claude-opus-5-5",
  modelLabel: "Claude Opus 5.5",
  context: { schema: "public", described: 4, named: 0, omitted: 0 },
}
const usage = { input: 1500, output: 64 }
const ask = { kind: "sql", prompt: "count the orders" }

/** A whole answer folded in, line by line. */
const fold = (t, id, events) => events.reduce((held, event) => applyEvent(held, id, event), t)

describe("a streamed answer", () => {
  test("arrives as lines, however the reads cut them", () => {
    let held = ""
    const seen = []
    for (const chunk of ['{"type":"te', 'xt","text":"SEL"}\n{"type":"text",', '"text":"ECT"}\n']) {
      const { lines, rest } = readLines(held, chunk)
      held = rest
      seen.push(...lines.map(parseEvent))
    }
    expect(held).toBe("")
    expect(seen.map((event) => event.text)).toEqual(["SEL", "ECT"])
    expect(parseEvent("not json")).toBeNull()
    expect(parseEvent('"just a string"')).toBeNull()
  })

  test("is drawn as it arrives and kept once it is whole", () => {
    let t = beginTurn(EMPTY_TRANSCRIPT, "t1", ask, "public")
    expect(answering(t.turns[0])).toBe(true)
    t = fold(t, "t1", [start, { type: "thinking" }])
    expect(t.turns[0].phase).toBe("thinking")
    // The conversation is the server's only once a turn of it is kept.
    expect(t.conversation).toBe("")
    t = fold(t, "t1", [
      { type: "text", text: "```sql\nSELECT " },
      { type: "text", text: "count(*) FROM orders;\n```" },
    ])
    expect(t.turns[0].phase).toBe("writing")
    expect(t.turns[0].parts).toEqual([
      { kind: "text", text: "```sql\nSELECT count(*) FROM orders;\n```" },
    ])
    t = applyEvent(t, "t1", {
      type: "done",
      stop: "end",
      usage,
      model: "claude-opus-5-5",
      kept: true,
    })
    expect(t.turns[0]).toMatchObject({ phase: "done", usage, model: "Claude Opus 5.5" })
    expect(t.conversation).toBe("c0ffee")
    expect(t.context.described).toBe(4)
    expect(answering(t.turns[0])).toBe(false)
  })

  test("a refusal, a cut-off answer and a failure each end the turn their own way", () => {
    const one = (done) => fold(beginTurn(EMPTY_TRANSCRIPT, "t", ask, "public"), "t", [start, done])
    const refused = one({ type: "done", stop: "refusal", usage, model: "", kept: false })
    expect(refused.turns[0].phase).toBe("refused")
    expect(refused.conversation).toBe("")
    expect(one({ type: "done", stop: "length", usage, model: "", kept: true }).turns[0].phase).toBe(
      "cut",
    )
    const failed = one({
      type: "error",
      error: { code: "assistant_failed", message: "Overloaded" },
    })
    expect(failed.turns[0]).toMatchObject({ phase: "failed", error: "Overloaded" })
    const stopped = endTurn(beginTurn(EMPTY_TRANSCRIPT, "t", ask, "public"), "t", "stopped")
    expect(stopped.turns[0].phase).toBe("stopped")
  })
})

describe("a call the assistant asks for", () => {
  const asking = () =>
    fold(beginTurn(EMPTY_TRANSCRIPT, "t1", ask, "public"), "t1", [
      start,
      { type: "text", text: "I need the columns of orders." },
      {
        type: "done",
        stop: "tool",
        usage,
        model: "claude-opus-5-5",
        kept: true,
        pending: [{ id: "toolu_1", tables: ["orders"] }],
      },
    ])

  test("waits for the person's answer", () => {
    const t = asking()
    expect(t.turns[0].phase).toBe("asking")
    expect(pendingCalls(t)).toEqual([{ id: "toolu_1", tables: ["orders"] }])
  })

  test("is answered in the turn that asked, which carries on", () => {
    let t = decide(asking(), true)
    expect(pendingCalls(t)).toEqual([])
    expect(t.turns[0].phase).toBe("waiting")
    expect(t.turns[0].parts[1]).toMatchObject({ kind: "ask", answer: "allowed" })
    t = fold(t, "t1", [
      start,
      { type: "described", calls: [{ tables: ["orders"], missing: null }] },
      { type: "text", text: "SELECT count(*) FROM orders;" },
      { type: "done", stop: "end", usage, model: "claude-opus-5-5", kept: true },
    ])
    expect(t.turns[0].parts.map((part) => part.kind)).toEqual(["text", "ask", "read", "text"])
    expect(t.turns[0].parts[2]).toEqual({ kind: "read", tables: ["orders"], missing: [] })
    expect(t.turns).toHaveLength(1)
  })

  test("declined is said as declined", () => {
    const t = decide(asking(), false)
    expect(t.turns[0].parts[1].answer).toBe("declined")
  })

  test("asking something else declines it, as the server does", () => {
    const t = beginTurn(asking(), "t2", ask, "public")
    expect(t.turns[0]).toMatchObject({ phase: "done" })
    expect(t.turns[0].parts[1].answer).toBe("declined")
    expect(pendingCalls(t)).toEqual([])
    expect(t.conversation).toBe("c0ffee")
  })
})

describe("a conversation", () => {
  test("carries on in the same schema, and begins again in another", () => {
    let t = fold(beginTurn(EMPTY_TRANSCRIPT, "t1", ask, "public"), "t1", [
      start,
      { type: "done", stop: "end", usage, model: "m", kept: true },
    ])
    t = beginTurn(t, "t2", ask, "public")
    expect(t.conversation).toBe("c0ffee")
    expect(t.turns).toHaveLength(2)
    const other = beginTurn(t, "t3", ask, "analytics")
    expect(other.conversation).toBe("")
    expect(other.schema).toBe("analytics")
    expect(other.turns.map((turn) => turn.id)).toEqual(["t3"])
  })

  test("a first turn that failed leaves nothing to carry on", () => {
    let t = fold(beginTurn(EMPTY_TRANSCRIPT, "t1", ask, "public"), "t1", [
      start,
      { type: "error", error: { code: "assistant_failed", message: "down" } },
    ])
    t = beginTurn(t, "t2", ask, "public")
    expect(t.conversation).toBe("")
  })
})

describe("what a question carries", () => {
  test("a result is attached as its first rows and columns, and counted", () => {
    const columns = Array.from({ length: 40 }, (_, i) => `c${i}`)
    const rows = Array.from({ length: 80 }, (_, r) => columns.map((_, c) => r * 100 + c))
    const attached = attachable({ columns, rows, rowCount: 900 })
    expect(attached.columns).toHaveLength(ATTACH_COLUMNS)
    expect(attached.rows).toHaveLength(ATTACH_ROWS)
    expect(attached.rows[0]).toHaveLength(ATTACH_COLUMNS)
    expect(attached.total).toBe(900)
  })

  test("cuts a long value as the server does, so a wide result still fits the request", () => {
    const body = "é".repeat(100_000)
    const document = { body: "x".repeat(300) }
    const attached = attachable({
      columns: ["body", "doc", "short", "n"],
      rows: [[body, document, "kept", 7]],
      rowCount: 1,
    })
    const [cut, json, short, n] = attached.rows[0]
    expect([...cut]).toHaveLength(ATTACH_CELL + 1)
    expect(cut.endsWith("…")).toBe(true)
    expect(json).toBe(JSON.stringify(document).slice(0, ATTACH_CELL) + "…")
    expect(short).toBe("kept")
    expect(n).toBe(7)
  })

  test("is listed by kind and count, never by what is in it", () => {
    const asked = askedOf({
      kind: "sql",
      prompt: "why",
      statement: "SELECT secret FROM t",
      result: { columns: ["a"], rows: [["ann@example.com"]], total: 1 },
    })
    expect(asked.attached).toEqual(["statement", "result · 1 row"])
    expect(JSON.stringify(asked)).not.toContain("ann@example.com")
  })

  test("a plan is given as the engine wrote it", () => {
    expect(planSource({ plan: [{ Plan: { "Node Type": "Seq Scan" } }], result: {} })).toContain(
      '"Node Type": "Seq Scan"',
    )
    expect(
      planSource({ result: { columns: ["QUERY PLAN"], rows: [["Seq Scan on orders"], [null]] } }),
    ).toBe("Seq Scan on orders\n")
    expect(
      planSource({
        result: {
          columns: ["id", "parent", "detail"],
          rows: [[2, 0, "SCAN orders"]],
        },
      }),
    ).toBe("id\tparent\tdetail\n2\t0\tSCAN orders")
  })
})

describe("a statement in an answer", () => {
  test("is a statement when it is fenced as SQL or as nothing", () => {
    expect(isStatementFence("")).toBe(true)
    expect(isStatementFence("SQL")).toBe(true)
    expect(isStatementFence("postgresql")).toBe(true)
    expect(isStatementFence("json")).toBe(false)
    expect(isStatementFence("bash")).toBe(false)
  })

  test("opens in a tab named for the question", () => {
    expect(statementTitle({ kind: "sql", prompt: "count  the\norders", attached: [] })).toBe(
      "count the orders",
    )
    expect(statementTitle({ kind: "sql", prompt: "x".repeat(60), attached: [] })).toHaveLength(40)
    expect(statementTitle({ kind: "error", prompt: "", attached: [] })).toBe("Corrected statement")
    expect(
      statementTitle({ kind: "policy", prompt: "own rows", table: "orders", attached: [] }),
    ).toBe("Policy on orders")
  })
})
