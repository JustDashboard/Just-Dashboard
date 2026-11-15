import { describe, expect, test } from "bun:test"
import { folderSentence } from "./counts"

describe("what a folder holds, in words", () => {
  test("folders, files and the measured size read as one sentence", () => {
    expect(folderSentence(12, 40)).toBe("12 folders, 40 files")
    expect(folderSentence(12, 40, 1.2 * 1024 ** 3)).toBe("12 folders, 40 files, 1.2 GB")
  })

  test("one of a kind is singular and thousands are grouped", () => {
    expect(folderSentence(1, 1)).toBe("1 folder, 1 file")
    expect(folderSentence(0, 1200)).toBe("1,200 files")
  })

  test("a kind there is none of is left out", () => {
    expect(folderSentence(3, 0)).toBe("3 folders")
    expect(folderSentence(0, 2, 2048)).toBe("2 files, 2.0 KB")
  })

  test("an empty folder has no sentence, measured or not", () => {
    expect(folderSentence(0, 0)).toBeUndefined()
    expect(folderSentence(0, 0, 0)).toBeUndefined()
  })

  test("a size that was not measured is not a size of nothing", () => {
    expect(folderSentence(2, 5, null)).toBe("2 folders, 5 files")
    expect(folderSentence(2, 5, 0)).toBe("2 folders, 5 files, 0 B")
  })
})
