import { describe, expect, test } from "bun:test"
import { shaped } from "./api"

describe("an answer held to the field the page needs", () => {
  test("is handed back when it has that field", () => {
    const answer = { documents: [], returned: 0 }
    expect(shaped(answer, "documents", "the documents")).toBe(answer)
  })

  test("is a failed read with a sentence when it has not", () => {
    for (const answer of [null, "<html>", 3, { rows: [] }]) {
      expect(() => shaped(answer, "documents", "the documents")).toThrow(
        "The server's answer for the documents could not be read.",
      )
    }
  })
})
