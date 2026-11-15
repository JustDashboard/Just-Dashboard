import { describe, expect, test } from "bun:test"
import {
  formerSheetHref,
  parseTableSegment,
  segment,
  sessionHref,
  statementHref,
  tableHref,
  tableSegment,
  unsegment,
} from "./performance-address"

const query = (text) => new URLSearchParams(text)

describe("a path segment", () => {
  test("an ordinary name is itself", () => {
    expect(segment("orders")).toBe("orders")
    expect(segment("-8123456789012345678")).toBe("-8123456789012345678")
    expect(tableSegment("public", "orders")).toBe("public.orders")
  })

  test("holds no percent sign, no slash and no dot of the name's own", () => {
    for (const value of [
      "a/b",
      "a.b",
      "a%2Fb",
      "12,3301",
      "s1~1",
      "..",
      "naïve table",
      "a b?c#d",
    ]) {
      const written = segment(value)
      expect(written).not.toMatch(/[%/.?# ]/)
      expect(unsegment(written)).toBe(value)
    }
  })

  test("a statement with no id still has a segment", () => {
    expect(segment("")).toBe("~")
    expect(unsegment("~")).toBe("")
    expect(statementHref(1, "")).toBe("/databases/1/performance/statements/~")
    expect(statementHref(1, "~1")).toBe("/databases/1/performance/statements/~7E1")
    expect(unsegment("~7E1")).toBe("~1")
  })

  test("what it never wrote is not read as something", () => {
    expect(unsegment("a%2Fb")).toBeNull()
    expect(unsegment("a.b")).toBeNull()
    expect(unsegment("~ZZ")).toBeNull()
    expect(unsegment("")).toBeNull()
  })
})

describe("a table's segment", () => {
  test("the one dot is between the schema and the name", () => {
    expect(tableSegment("reports.2026", "q1/q2")).toBe("reports~2E2026.q1~2Fq2")
    expect(parseTableSegment("reports~2E2026.q1~2Fq2")).toEqual({
      schema: "reports.2026",
      name: "q1/q2",
    })
    expect(parseTableSegment("public.orders")).toEqual({ schema: "public", name: "orders" })
  })

  test("an engine with no schema has the name alone", () => {
    expect(tableSegment("", "notes")).toBe("notes")
    expect(tableSegment(undefined, "a.b")).toBe("a~2Eb")
    expect(parseTableSegment("a~2Eb")).toEqual({ schema: "", name: "a.b" })
  })

  test("a segment with a dot too many, or no name, is no table", () => {
    expect(parseTableSegment("a.b.c")).toBeNull()
    expect(parseTableSegment("public.")).toBeNull()
    expect(parseTableSegment("public.a%2Fb")).toBeNull()
  })
})

describe("the three addresses", () => {
  test("carry only what is set", () => {
    expect(statementHref(7, "s1", { tab: "plan", sort: null })).toBe(
      "/databases/7/performance/statements/s1?tab=plan",
    )
    expect(tableHref(7, "public", "orders", { scope: "" })).toBe(
      "/databases/7/performance/tables/public.orders",
    )
    expect(sessionHref(7, "12,3301", { state: "blocked" })).toBe(
      "/databases/7/performance/sessions/12~2C3301?state=blocked",
    )
  })
})

describe("an address that opened a sheet", () => {
  test("a statement, with the order its list was in", () => {
    expect(formerSheetHref(1, "statements", query("view=statements&statement=s1&sort=mean"))).toBe(
      "/databases/1/performance/statements/s1?sort=mean",
    )
  })

  test("a recorded statement opens on its history", () => {
    expect(formerSheetHref(1, "history", query("view=history&statement=7731904&sort=calls"))).toBe(
      "/databases/1/performance/statements/7731904?tab=history",
    )
  })

  test("a table, by the schema and name its old key held escaped", () => {
    expect(formerSheetHref(1, "tables", query("view=tables&object=public/orders"))).toBe(
      "/databases/1/performance/tables/public.orders",
    )
    expect(
      formerSheetHref(
        1,
        "tables",
        query(`view=tables&scope=a.b&object=${encodeURIComponent("a.b/c%2Fd")}`),
      ),
    ).toBe("/databases/1/performance/tables/a~2Eb.c~2Fd?scope=a.b")
  })

  test("a session, with the state its list was narrowed to", () => {
    expect(
      formerSheetHref(1, "sessions", query("view=sessions&session=12,3301&state=blocked")),
    ).toBe("/databases/1/performance/sessions/12~2C3301?state=blocked")
  })

  test("a key under another view opens nothing", () => {
    expect(formerSheetHref(1, "overview", query("statement=s1"))).toBeNull()
    expect(formerSheetHref(1, "tables", query("view=tables&object=orders"))).toBeNull()
    expect(formerSheetHref(1, "statements", query("view=statements&statement="))).toBeNull()
    expect(formerSheetHref(1, "sessions", query("view=sessions"))).toBeNull()
  })
})
