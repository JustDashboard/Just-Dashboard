import { describe, expect, test } from "bun:test"
import { byKeyType, keyType, nameHue, statementVerb } from "./kinds"

describe("the hues a home tells kinds apart with", () => {
  test("a key type keeps the hue the Keys page gives it", () => {
    expect(keyType("string")).toEqual({ label: "String", color: "var(--tag-blue)" })
    expect(keyType("zset")).toEqual({ label: "Sorted set", color: "var(--tag-pink)" })
    expect(keyType("ReJSON-RL").label).toBe("JSON")
  })

  test("a type nobody knows keeps its own word and the quiet ink", () => {
    expect(keyType("TSDB-TYPE")).toEqual({ label: "TSDB-TYPE", color: "var(--muted-foreground)" })
    // A name every object carries is not a key type.
    expect(keyType("constructor").color).toBe("var(--muted-foreground)")
  })

  test("types are listed in the legend's order, strangers last", () => {
    expect(["zset", "TSDB-TYPE", "hash", "string"].sort(byKeyType)).toEqual([
      "string",
      "hash",
      "zset",
      "TSDB-TYPE",
    ])
  })

  test("a statement's first word says what it does", () => {
    expect(statementVerb("SELECT d.datname FROM pg_database d")).toEqual({
      word: "SELECT",
      rest: " d.datname FROM pg_database d",
      color: "var(--tag-blue)",
    })
    expect(statementVerb("  insert into t values ($1)")?.color).toBe("var(--tag-green)")
    expect(statementVerb("UPDATE t SET a = $1")?.color).toBe("var(--tag-violet)")
    expect(statementVerb("DELETE FROM t")?.color).toBe("var(--tag-pink)")
    expect(statementVerb("ALTER TABLE t ADD c int")?.color).toBe("var(--tag-cyan)")
  })

  test("a statement that is none of those is left in its own ink", () => {
    expect(statementVerb("BEGIN")).toBeNull()
    expect(statementVerb("/* comment */ SELECT 1")).toBeNull()
    expect(statementVerb("")).toBeNull()
    expect(statementVerb("constructor")).toBeNull()
  })

  test("a name has one hue however it is cased, and never a state's", () => {
    expect(nameHue("Public")).toBe(nameHue("public"))
    for (const name of ["public", "analytics", "jdtest", "postgres", "root", "app"]) {
      expect(nameHue(name)).not.toMatch(/red|amber/)
    }
  })
})
