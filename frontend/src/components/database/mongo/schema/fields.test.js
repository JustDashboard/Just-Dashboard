import { describe, expect, test } from "bun:test"
import {
  allUnique,
  distinctWords,
  filterPath,
  matchingFields,
  missingClause,
  rangeWords,
  shareWords,
  typeClause,
  valueClause,
} from "./fields"

describe("the filter a press asks Documents for", () => {
  test("an array's elements are matched through the array itself", () => {
    expect(filterPath("tags[]")).toBe("tags")
    expect(filterPath("items[].sku")).toBe("items.sku")
    expect(filterPath("address.city")).toBe("address.city")
  })

  test("a value is written with the type the sample found it as", () => {
    expect(valueClause("address.city", "string", "Berlin")).toBe('{ "address.city": "Berlin" }')
    expect(valueClause("age", "int", "20")).toBe('{ "age": 20 }')
    expect(valueClause("verified", "bool", "true")).toBe('{ "verified": true }')
    expect(valueClause("n", "long", "9007199254740993")).toBe(
      '{ "n": { "$numberLong": "9007199254740993" } }',
    )
    expect(valueClause("roles[]", "string", "admin")).toBe('{ "roles": "admin" }')
  })

  test("a string with a quote or a backslash is still one string", () => {
    expect(valueClause("name", "string", 'a "b" \\ c')).toBe('{ "name": "a \\"b\\" \\\\ c" }')
  })

  test("a value that cannot be stated exactly gives no clause", () => {
    // The server cuts a long string at 160 bytes and marks the cut.
    expect(valueClause("bio", "string", `${"x".repeat(160)}…`)).toBeNull()
    // A short string that happens to end in an ellipsis is whole.
    expect(valueClause("bio", "string", "and so on…")).toBe('{ "bio": "and so on…" }')
    expect(valueClause("price", "decimal", "1.5")).toBeNull()
    expect(valueClause("age", "int", "20; drop")).toBeNull()
    expect(valueClause("verified", "bool", "yes")).toBeNull()
  })

  test("a type, and a field that is not there", () => {
    expect(typeClause("legacyId", "int")).toBe('{ "legacyId": { "$type": "int" } }')
    expect(missingClause("items[].note")).toBe('{ "items.note": { "$exists": false } }')
  })
})

describe("a type's spread in words", () => {
  test("numbers: the least, the most and the mean", () => {
    expect(rangeWords({ type: "int", count: 1, share: 1, min: "18", max: "77", avg: 46.625 })).toBe(
      "18 to 77 · mean 46.63",
    )
    expect(rangeWords({ type: "int", count: 1, share: 1, min: "5", max: "5", avg: 5 })).toBe("5")
  })

  test("moments by the day, and an ObjectId by when it was made", () => {
    expect(
      rangeWords({
        type: "date",
        count: 1,
        share: 1,
        min: "2026-07-31T04:17:43.349Z",
        max: "2026-10-01T02:17:43.207Z",
        avg: 1,
      }),
    ).toBe("2026-07-31 to 2026-10-01")
    expect(
      rangeWords({
        type: "objectId",
        count: 1,
        share: 1,
        min: "2026-10-01T15:17:43Z",
        max: "2026-10-01T15:17:43Z",
      }),
    ).toBe("made 2026-10-01")
  })

  test("strings and lists by their length", () => {
    expect(
      rangeWords({ type: "string", count: 1, share: 1, minLength: 6, maxLength: 9, avgLength: 7 }),
    ).toBe("6 to 9 bytes")
    expect(
      rangeWords({ type: "array", count: 1, share: 1, minLength: 1, maxLength: 1, avgLength: 1 }),
    ).toBe("1 elements")
    expect(rangeWords({ type: "bool", count: 1, share: 1 })).toBeNull()
  })

  test("how many values differ, and when every one does", () => {
    expect(distinctWords({ type: "int", count: 1, share: 1, distinct: 58 })).toBe("58 distinct")
    expect(
      distinctWords({ type: "string", count: 1, share: 1, distinct: 1000, distinctCapped: true }),
    ).toBe("over 1,000 distinct")
    expect(distinctWords({ type: "date", count: 1, share: 1 })).toBeNull()
    const top = (counts) => counts.map((count, index) => ({ value: String(index), count }))
    expect(allUnique({ type: "string", count: 3, share: 1, top: top([1, 1, 1]) })).toBe(true)
    expect(allUnique({ type: "string", count: 3, share: 1, top: top([2, 1]) })).toBe(false)
    expect(allUnique({ type: "string", count: 1, share: 1, top: top([1]) })).toBe(false)
  })

  test("a share of the sample as a percentage a reader compares", () => {
    expect(shareWords(1)).toBe("100%")
    expect(shareWords(0.105)).toBe("11%")
    expect(shareWords(0.014)).toBe("1.4%")
    expect(shareWords(0.0004)).toBe("<0.1%")
    expect(shareWords(0)).toBe("0%")
    expect(shareWords(0.9996)).toBe("100%")
  })
})

describe("finding a field", () => {
  const field = (path, depth) => ({ path, name: path.split(".").pop(), depth })
  const fields = [
    field("_id", 0),
    field("address", 0),
    field("address.city", 1),
    field("address.zip", 1),
    field("items", 0),
    field("items[]", 1),
    field("items[].sku", 2),
  ]

  test("a match is listed with the fields above it", () => {
    expect(matchingFields(fields, "city").map((entry) => entry.path)).toEqual([
      "address",
      "address.city",
    ])
    expect(matchingFields(fields, "SKU").map((entry) => entry.path)).toEqual([
      "items",
      "items[]",
      "items[].sku",
    ])
  })

  test("no search is every field; a search that finds nothing is none", () => {
    expect(matchingFields(fields, "  ")).toHaveLength(fields.length)
    expect(matchingFields(fields, "nope")).toEqual([])
  })
})
