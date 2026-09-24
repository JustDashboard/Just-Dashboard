import { describe, expect, test } from "bun:test"
import { readDotenv } from "./dotenv"

// Each case is one the server's parseDotenvEntries answers the same way, so a
// preview never lists what the import would refuse, or cut, or join.
describe("readDotenv", () => {
  test("reads assignments in order, skipping blanks, comments and a leading export", () => {
    const { entries, error } = readDotenv("# header\n\nexport FOO=bar\n  BAZ = qux  \n")
    expect(error).toBeUndefined()
    expect(entries.map(({ name, value, line }) => [name, value, line])).toEqual([
      ["FOO", "bar", 3],
      ["BAZ", "qux", 4],
    ])
  })

  test("a quoted value may span lines and unescapes inside double quotes only", () => {
    const { entries } = readDotenv("KEY=\"one\\ntwo\nthree\"\nRAW='a\\nb'\nNEXT=1")
    expect(entries[0]).toMatchObject({ name: "KEY", value: "one\ntwo\nthree", line: 1 })
    expect(entries[1]).toMatchObject({ name: "RAW", value: "a\\nb", line: 3 })
    expect(entries[2]).toMatchObject({ name: "NEXT", line: 4 })
  })

  test("an unknown escape keeps its backslash, as the server does", () => {
    expect(readDotenv('A="x\\qy"').entries[0].value).toBe("x\\qy")
  })

  test("a bad name and a repeated one are refused by name, not as a whole", () => {
    const { entries, error } = readDotenv("1BAD=x\nOK=1\nOK=2")
    expect(error).toBeUndefined()
    expect(entries.map((entry) => entry.refused)).toEqual(["invalid_name", undefined, "duplicate"])
  })

  test("a name longer than the server's 128 characters is refused, not previewed as new", () => {
    const long = `A${"B".repeat(127)}`
    const { entries } = readDotenv(`${long}=1\n${long}C=2`)
    expect(entries.map((entry) => entry.refused)).toEqual([undefined, "invalid_name"])
  })

  test("a line without an equals sign, an open quote or trailing text refuse the paste", () => {
    expect(readDotenv("FOO=1\nnonsense").error).toBe("Line 2 needs NAME=value.")
    expect(readDotenv('A="open\nB=2').error).toBe(
      "The quoted value starting on line 1 is not closed.",
    )
    expect(readDotenv('A="x" y').error).toBe("Line 1 has text after its quoted value.")
  })

  test("a comment after a quoted value is allowed", () => {
    expect(readDotenv('A="x" # note\nB=2').entries.map((entry) => entry.value)).toEqual(["x", "2"])
  })
})
