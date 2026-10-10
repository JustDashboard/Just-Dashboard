import { describe, expect, test } from "bun:test"
import { commandLine, quoteWord } from "./command-line"

describe("an argument written for the console's reader", () => {
  test("plain text is quoted as it is", () => {
    expect(quoteWord("user:7:profile")).toBe('"user:7:profile"')
    expect(quoteWord("two words")).toBe('"two words"')
    expect(quoteWord("")).toBe('""')
  })

  test("a quote and a backslash are escaped, so neither ends the word", () => {
    expect(quoteWord('say "hi" \\ bye')).toBe('"say \\"hi\\" \\\\ bye"')
  })

  test("what is not printable ASCII is written as its bytes", () => {
    expect(quoteWord("a\nb\t\u0000")).toBe('"a\\x0ab\\x09\\x00"')
    expect(quoteWord("é")).toBe('"\\xc3\\xa9"')
    // 0xff 0x00 0x41: not text at all.
    expect(quoteWord({ base64: "/wBB" })).toBe('"\\xff\\x00A"')
  })

  test("a number is written bare", () => {
    expect(quoteWord(60)).toBe("60")
  })

  test("a command keeps its own words and quotes every argument", () => {
    expect(commandLine("HEXPIRE", ["user:7", 60, "FIELDS", 1, "FIELDS"])).toBe(
      'HEXPIRE "user:7" 60 "FIELDS" 1 "FIELDS"',
    )
  })
})
