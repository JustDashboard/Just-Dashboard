import { describe, expect, test } from "bun:test"
import { readFileSync } from "node:fs"
import { join } from "node:path"
import {
  EMPTY_FILTER,
  describePredicate,
  fieldsFromParams,
  fieldsOf,
  filterEquals,
  filterQuery,
  hideField,
  isFilterActive,
  levelsFromParam,
  matchFields,
  onlyField,
  parseFieldTokens,
  toggleField,
  withFieldTokens,
} from "./log-filter"

// The server and this file answer "does this line pass `f=`" for the same
// lines — the server for the lines it sends, this for the counts the live
// pane shows over them — so both suites run one set of vectors, and a rule
// that drifts on either side fails here or in Go.
const vectors = JSON.parse(
  readFileSync(
    join(import.meta.dir, "../../../backend/internal/logsx/testdata/predicates.json"),
    "utf8",
  ),
)

describe("the shared predicate vectors", () => {
  test("there are enough of them to mean something", () => {
    expect(vectors.length).toBeGreaterThan(50)
  })

  for (const vector of vectors) {
    test(vector.name ?? JSON.stringify(vector.f), () => {
      const fields = fieldsFromParams(vector.f)
      // Every vector is a well-formed question; one the link reader drops
      // would be testing a different question than the server's.
      expect(Object.values(fields).flat()).toHaveLength(vector.f.length)
      expect(matchFields(vector.line, fields)).toBe(vector.match)
    })
  }
})

describe("a filter stored before fields existed", () => {
  const old = { q: "", exclude: "", regex: false, ignoreCase: true, levels: [] }

  test("reads as no fields, and nothing throws", () => {
    expect(fieldsOf(old)).toEqual({})
    expect(isFilterActive(old)).toBe(false)
    expect(filterQuery(old).f).toBeUndefined()
    expect(filterEquals(old, EMPTY_FILTER)).toBe(true)
  })

  test("a field map of the wrong shape is dropped, not thrown on", () => {
    for (const fields of [null, "user:root", 3, ["user:root"], { user: "root" }, { user: [1] }]) {
      const f = { ...old, fields }
      expect(fieldsOf(f)).toEqual({})
      expect(isFilterActive(f)).toBe(false)
      expect(filterQuery(f).f).toBeUndefined()
    }
    expect(fieldsOf(null)).toEqual({})
    expect(fieldsOf(undefined)).toEqual({})
  })

  test("the well-formed part of a half-broken map survives", () => {
    const f = { ...old, fields: { user: ["root", 7, "*x"], "bad key": ["x"], level: ["error"] } }
    expect(fieldsOf(f)).toEqual({ user: ["root"] })
  })
})

describe("the query the routes share", () => {
  test("carries predicates as repeated f, keys in order", () => {
    const f = { ...EMPTY_FILTER, fields: { user: ["root", "!admin"], event: ["auth_failed"] } }
    expect(filterQuery(f).f).toEqual(["event:auth_failed", "user:root", "user:!admin"])
    expect(isFilterActive(f)).toBe(true)
  })

  test("a well-formed map is returned as itself, so a memo on it holds", () => {
    const fields = { user: ["root"] }
    expect(fieldsOf({ fields })).toBe(fields)
  })

  test("equality ignores the order levels and values were chosen in", () => {
    const a = { ...EMPTY_FILTER, levels: ["error", "warn"], fields: { event: ["a", "b"] } }
    const b = { ...EMPTY_FILTER, levels: ["warn", "error"], fields: { event: ["b", "a"] } }
    expect(filterEquals(a, b)).toBe(true)
    expect(filterEquals(a, { ...b, fields: { event: ["a"] } })).toBe(false)
    expect(filterEquals(a, { ...b, fields: { ...b.fields, user: ["x"] } })).toBe(false)
  })
})

describe("a link's f values", () => {
  test("split at the first colon, so an IPv6 address survives", () => {
    expect(fieldsFromParams(["client:2001:db8::1", "client:!::1"])).toEqual({
      client: ["2001:db8::1", "!::1"],
    })
  })

  test("what the server would refuse is dropped rather than sent", () => {
    expect(
      fieldsFromParams([
        "no-colon",
        ":value",
        "bad key:x",
        "level:error",
        `user:${"x".repeat(257)}`,
        "status:>abc",
        "status:>=",
        "status:>inf",
        "status:<=-Infinity",
        "status:>NaN",
        "user:*x",
        "user:!",
        "user:~",
        "user:=",
        "user:",
        "x".repeat(65) + ":y",
      ]),
    ).toEqual({})
  })

  test("a value at the byte limit is kept, and multi-byte characters count as bytes", () => {
    expect(fieldsFromParams([`user:${"x".repeat(256)}`]).user).toHaveLength(1)
    expect(fieldsFromParams([`user:${"é".repeat(129)}`])).toEqual({})
  })

  test("at most thirty-two predicates", () => {
    const many = Array.from({ length: 40 }, (_, i) => `k${i}:v`)
    expect(Object.keys(fieldsFromParams(many))).toHaveLength(32)
  })

  test("keys may carry the characters structured logs use", () => {
    expect(fieldsFromParams(["log.level:warn", "@version:1", "request-id:a"])).toEqual({
      "log.level": ["warn"],
      "@version": ["1"],
      "request-id": ["a"],
    })
  })

  test("levels keep only the ones there are chips for", () => {
    expect(levelsFromParam("error, WARN,bogus,error")).toEqual(["error", "warn"])
    expect(levelsFromParam(null)).toEqual([])
  })
})

describe("key:value words in the search box", () => {
  const known = ["user", "status", "client", "path"]

  test("become fields when the key is one the lens reads", () => {
    expect(parseFieldTokens("user:postgres slow status:>=500", known)).toEqual({
      q: "slow",
      fields: { user: ["postgres"], status: [">=500"] },
    })
  })

  test("a time, a URL and an unknown key stay words", () => {
    const q = "at 12:30 http://host/x shard:3"
    expect(parseFieldTokens(q, known)).toEqual({ q, fields: {} })
  })

  test("a quoted value keeps its spaces, and an address keeps its colons", () => {
    expect(parseFieldTokens('path:"/a b" client:2001:db8::1', known)).toEqual({
      q: "",
      fields: { path: ["/a b"], client: ["2001:db8::1"] },
    })
  })

  test("a malformed value is left in the search", () => {
    expect(parseFieldTokens("status:>abc", known)).toEqual({ q: "status:>abc", fields: {} })
  })

  test("the filter is returned as itself when there is nothing to move", () => {
    const f = { ...EMPTY_FILTER, q: "just words" }
    expect(withFieldTokens(f, known)).toBe(f)
    const regex = { ...EMPTY_FILTER, q: "user:x", regex: true }
    expect(withFieldTokens(regex, known)).toBe(regex)
  })

  test("moved words join the fields already there", () => {
    const f = { ...EMPTY_FILTER, q: "user:root oops", fields: { user: ["admin"] } }
    expect(withFieldTokens(f, known)).toEqual({
      ...f,
      q: "oops",
      fields: { user: ["admin", "root"] },
    })
  })
})

describe("narrowing from a value", () => {
  test("only replaces the key's predicates with the value, escaped when it must be", () => {
    expect(onlyField({ user: ["!x"], db: ["a"] }, "user", "root")).toEqual({
      user: ["root"],
      db: ["a"],
    })
    expect(onlyField({}, "path", "~admin")).toEqual({ path: ["=~admin"] })
  })

  test("hide adds the exclusion and drops an inclusion that could no longer hold", () => {
    expect(hideField({ user: ["root", "admin"] }, "user", "root")).toEqual({
      user: ["admin", "!root"],
    })
    expect(hideField({ user: ["!root"] }, "user", "root")).toEqual({ user: ["!root"] })
  })

  test("toggle includes, then lets go, and removes the key when nothing is left", () => {
    const on = toggleField({ user: ["!root"] }, "user", "root")
    expect(on).toEqual({ user: ["root"] })
    expect(toggleField(on, "user", "root")).toEqual({})
  })

  test("a predicate reads as words", () => {
    expect(describePredicate("root")).toBe("root")
    expect(describePredicate("!root")).toBe("not root")
    expect(describePredicate(">=500")).toBe("≥ 500")
    expect(describePredicate("~api")).toBe("contains api")
    expect(describePredicate("!*")).toBe("no value")
  })
})
