import { describe, expect, test } from "bun:test"
import {
  EMPTY_QUERY,
  HISTORY_SIZE,
  addressOf,
  collectionKey,
  draftFromAddress,
  draftLabel,
  draftProblems,
  fieldProblem,
  findSpec,
  findStatement,
  isEmptyDraft,
  isFiltered,
  limitOf,
  optionCount,
  pageWindow,
  rangeWords,
  refusedField,
  sameDraft,
  withHistory,
} from "./query"

const draft = (over = {}) => ({ ...EMPTY_QUERY, ...over })

describe("the query and the address", () => {
  test("an address states a draft; a key it lacks is an empty field", () => {
    const address = { filter: "{ a: 1 }", sort: "{ a: -1 }", limit: "20" }
    expect(draftFromAddress((name) => address[name] ?? "")).toEqual(
      draft({ filter: "{ a: 1 }", sort: "{ a: -1 }", limit: "20" }),
    )
  })

  test("an empty field leaves the address rather than staying as an empty key", () => {
    expect(addressOf(draft({ filter: "{ a: 1 }", skip: " " }))).toEqual({
      filter: "{ a: 1 }",
      project: null,
      sort: null,
      collation: null,
      hint: null,
      skip: null,
      limit: null,
      maxTime: null,
    })
  })

  test("two drafts that differ only in space around them are the same query", () => {
    expect(sameDraft(draft({ filter: " {a:1} " }), draft({ filter: "{a:1}" }))).toBe(true)
    expect(sameDraft(draft({ filter: "{a:1}" }), draft({ filter: "{a:2}" }))).toBe(false)
    expect(isEmptyDraft(draft({ sort: "  " }))).toBe(true)
    expect(optionCount(draft({ filter: "{}", sort: "{a:1}", limit: "5" }))).toBe(2)
  })
})

describe("what can be told is wrong before the server reads it", () => {
  test("a document field takes a document in either spelling", () => {
    expect(fieldProblem("filter", '{ status: "paid" }')).toBeNull()
    expect(fieldProblem("filter", '{"age": {"$gt": 30}}')).toBeNull()
    expect(fieldProblem("sort", "[1]")).toBe("Write a document: { … }.")
    expect(fieldProblem("filter", "{ age: { $gt: 60 }")).toContain("never closed")
  })

  test("an index hint is a name or a key pattern", () => {
    expect(fieldProblem("hint", "email_1")).toBeNull()
    expect(fieldProblem("hint", "{ email: 1 }")).toBeNull()
    expect(fieldProblem("hint", "{ email: ")).not.toBeNull()
  })

  test("skip, limit and the time limit are whole numbers in range", () => {
    expect(fieldProblem("skip", "0")).toBeNull()
    expect(fieldProblem("skip", "-1")).not.toBeNull()
    expect(fieldProblem("skip", "1.5")).not.toBeNull()
    expect(fieldProblem("limit", "0")).not.toBeNull()
    expect(fieldProblem("limit", "5000")).toBeNull()
    expect(fieldProblem("maxTime", "300001")).not.toBeNull()
    expect(fieldProblem("maxTime", "abc")).not.toBeNull()
  })

  test("only the fields that are wrong are named", () => {
    expect(draftProblems(draft({ filter: "{", skip: "x", sort: "{ a: 1 }" }))).toEqual({
      filter: expect.stringContaining("never closed"),
      skip: "A whole number, 0 or more.",
    })
    expect(draftProblems(EMPTY_QUERY)).toEqual({})
  })
})

describe("the server's refusal lands on the field it is about", () => {
  test("a refusal that names its field", () => {
    expect(
      refusedField(
        "projection: line 1, column 7: unexpected '}' where a value was expected",
        draft({ project: "{ a: }" }),
      ),
    ).toEqual({
      field: "project",
      message: "Line 1, column 7: unexpected '}' where a value was expected",
    })
    expect(refusedField("sort: line 1, column 7: Foo(…) is not a type", draft()).field).toBe("sort")
  })

  test("an operator the server does not know belongs to the filter", () => {
    expect(
      refusedField("(BadValue) unknown operator: $gtx", draft({ filter: "{ a: { $gtx: 1 } }" })),
    ).toEqual({ field: "filter", message: "(BadValue) unknown operator: $gtx" })
  })

  test("a refusal about nothing in the bar is the query's, not a field's", () => {
    expect(refusedField("the server could not be reached", draft({ filter: "{a:1}" }))).toEqual({
      field: null,
      message: "the server could not be reached",
    })
    expect(refusedField("unknown operator: $x", draft()).field).toBeNull()
  })

  test("the server's words for a sort, a collation, a hint and a projection land on those fields", () => {
    const typed = draft({
      filter: "{ age: { $gt: 70 } }",
      sort: "{ age: 5 }",
      collation: '{ locale: "xx" }',
      hint: "nope_1",
      project: "{ a: 1, b: 0 }",
    })
    expect(
      refusedField(
        "(Location15975) $sort key ordering must be 1 (for ascending) or -1 (for descending)",
        typed,
      ).field,
    ).toBe("sort")
    expect(
      refusedField("(BadValue) Field 'locale' is invalid in: { locale: \"xx\" }", typed).field,
    ).toBe("collation")
    expect(
      refusedField(
        "(BadValue) error processing query: ns=app.users limit=51Tree: $and\nSort: {}\nProj: {}\n planner returned error :: caused by :: hint provided does not correspond to an existing index",
        typed,
      ).field,
    ).toBe("hint")
    expect(
      refusedField("(Location31254) Cannot do exclusion on field b in inclusion projection", typed)
        .field,
    ).toBe("project")
  })

  test("a refusal of the sort is never pinned on the filter", () => {
    const typed = draft({ filter: "{ age: { $gt: 70 } }", sort: "{ age: 5 }" })
    expect(refusedField("(Location15975) $sort key ordering must be 1 or -1", typed).field).toBe(
      "sort",
    )
    // A field that holds nothing is never blamed.
    expect(
      refusedField("(Location15975) $sort key ordering must be 1 or -1", draft({ filter: "{}" }))
        .field,
    ).toBeNull()
  })

  test("an operator only one field contains belongs to that field", () => {
    expect(
      refusedField(
        "(Location40324) Unrecognized expression '$bogus'",
        draft({ filter: "{ a: 1 }", project: "{ b: { $bogus: 1 } }" }),
      ).field,
    ).toBe("project")
    // Named by none of them, or by two: the query's.
    expect(
      refusedField(
        "(Location16410) FieldPath field names may not start with '$'. Consider using $getField or $setField.",
        draft({ filter: "{ a: 1 }", sort: "{ $x: 1 }" }),
      ).field,
    ).toBeNull()
  })

  test("a read that ran out of time is the time limit's, when one was typed", () => {
    expect(
      refusedField("operation exceeded time limit", draft({ maxTime: "5" }), "query_timeout").field,
    ).toBe("maxTime")
    expect(refusedField("the request timed out", draft(), "query_timeout").field).toBeNull()
  })
})

describe("a page inside the query", () => {
  test("with no limit, a page is the size asked for", () => {
    expect(pageWindow(draft(), 0, 50)).toEqual({
      page: 0,
      skip: 0,
      limit: 50,
      last: false,
      cap: null,
    })
    expect(pageWindow(draft(), 2, 50)).toMatchObject({ skip: 100, limit: 50 })
  })

  test("the reader's skip is where page one starts", () => {
    expect(pageWindow(draft({ skip: "1000" }), 1, 50)).toMatchObject({ skip: 1050, limit: 50 })
  })

  test("the reader's limit is where the last page ends", () => {
    expect(pageWindow(draft({ limit: "120" }), 0, 50)).toMatchObject({ limit: 50, last: false })
    expect(pageWindow(draft({ limit: "120" }), 2, 50)).toMatchObject({
      skip: 100,
      limit: 20,
      last: true,
    })
    // A page past the limit is the last page inside it, not a request for nothing.
    expect(pageWindow(draft({ limit: "120" }), 9, 50)).toMatchObject({
      page: 2,
      skip: 100,
      limit: 20,
    })
    expect(pageWindow(draft({ limit: "20" }), 0, 50)).toMatchObject({ limit: 20, last: true })
  })

  test("a limit that is not a number is no limit", () => {
    expect(limitOf(draft({ limit: "x" }))).toBeNull()
    expect(limitOf(draft({ limit: "0" }))).toBeNull()
    expect(limitOf(draft({ limit: " 7 " }))).toBe(7)
  })

  test("what is sent: empty fields left out, the page's own skip and limit", () => {
    expect(
      findSpec(draft({ filter: "{ a: 1 }", maxTime: "500" }), { skip: 50, limit: 50 }),
    ).toEqual({
      filter: "{ a: 1 }",
      projection: undefined,
      sort: undefined,
      collation: undefined,
      hint: undefined,
      skip: 50,
      limit: 50,
      maxTimeMS: 500,
    })
  })
})

describe("where the page sits, said honestly", () => {
  const exact = (value) => ({ value, exact: true, scope: "filter" })

  test("an exact count", () => {
    expect(rangeWords(0, 50, exact(6000), false, null)).toBe("1–50 of 6,000")
    expect(rangeWords(50, 50, exact(6000), false, null)).toBe("51–100 of 6,000")
  })

  test("an estimate is said as one", () => {
    expect(rangeWords(0, 50, { value: 250000, exact: false, scope: "filter" }, false, null)).toBe(
      "1–50 of about 250,000",
    )
  })

  // The server could not count the matches in time and sent the collection's
  // size instead: that is not how many match, and is not printed as if it were.
  test("the collection's size under a filter is not the number of matches", () => {
    expect(rangeWords(0, 50, { value: 6000, exact: false, scope: "collection" }, true, null)).toBe(
      "1–50 · 6,000 in the collection",
    )
  })

  test("the reader's own limit bounds the total", () => {
    expect(rangeWords(0, 50, exact(6000), false, 120)).toBe("1–50 of 120")
  })

  test("no count is just the range; nothing found is said in words", () => {
    expect(rangeWords(0, 50, null, false, null)).toBe("1–50")
    expect(rangeWords(0, 0, exact(0), true, null)).toBe("No documents")
    expect(rangeWords(100, 0, exact(60), false, null)).toBe("Past the last of 60")
  })

  test("a filter is anything but an empty document", () => {
    expect(isFiltered(draft())).toBe(false)
    expect(isFiltered(draft({ filter: "{}" }))).toBe(false)
    expect(isFiltered(draft({ filter: "{ a: 1 }" }))).toBe(true)
  })
})

describe("history", () => {
  const entry = (filter, at, collection = "users") => ({
    database: "app",
    collection,
    draft: draft({ filter }),
    at,
  })

  test("a query run again moves to the front rather than being listed twice", () => {
    let history = withHistory([], entry("{ a: 1 }", 1))
    history = withHistory(history, entry("{ b: 2 }", 2))
    history = withHistory(history, entry(" { a: 1 } ", 3))
    expect(history.map((held) => held.at)).toEqual([3, 2])
  })

  test("the same query on another collection is another entry", () => {
    const history = withHistory([entry("{ a: 1 }", 1)], entry("{ a: 1 }", 2, "orders"))
    expect(history).toHaveLength(2)
  })

  test("the whole collection is not history, and the list is bounded", () => {
    expect(withHistory([], entry("", 1))).toEqual([])
    let history = []
    for (let n = 0; n < HISTORY_SIZE + 10; n++)
      history = withHistory(history, entry(`{ n: ${n} }`, n))
    expect(history).toHaveLength(HISTORY_SIZE)
    expect(history[0].at).toBe(HISTORY_SIZE + 9)
  })

  test("a query in one line", () => {
    expect(draftLabel(draft({ filter: "{\n  a: 1\n}", sort: "{ a: -1 }", limit: "5" }))).toBe(
      "{ a: 1 } · sort { a: -1 } · limit 5",
    )
    expect(draftLabel(draft())).toBe("{}")
  })
})

describe("the query as the shell writes it", () => {
  test("a plain name and one that needs getCollection", () => {
    expect(findStatement("users", draft())).toBe("db.users.find({})")
    expect(findStatement("my-coll", draft({ filter: "{ a: 1 }" }))).toBe(
      'db.getCollection("my-coll").find({ a: 1 })',
    )
  })

  test("every field that is set", () => {
    expect(
      findStatement(
        "orders",
        draft({
          filter: '{ status: "paid" }',
          project: "{ total: 1 }",
          sort: "{ total: -1 }",
          skip: "10",
          limit: "5",
          maxTime: "500",
        }),
      ),
    ).toBe(
      'db.orders.find({ status: "paid" }, { total: 1 }).sort({ total: -1 }).skip(10).limit(5).maxTimeMS(500)',
    )
  })
})

test("a collection among a server's is its database and its name", () => {
  expect(collectionKey("a", "b.c")).not.toBe(collectionKey("a.b", "c"))
})
