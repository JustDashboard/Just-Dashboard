import { describe, expect, test } from "bun:test"
import { workingTreeCounts, workingTreeSquares } from "./git-status"

// The bar on a repository row is a reading, so it has to be true at the edges
// as well as in the middle: a clean tree draws nothing, a single conflict is
// never rounded away, and five squares always means five squares.

const repo = (over = {}) => ({ changes: 0, staged: 0, untracked: 0, conflicts: 0, ...over })

describe("what git's summary implies about a working tree", () => {
  test("what is not staged, new or conflicted is an ordinary edit", () => {
    expect(workingTreeCounts(repo({ changes: 28, staged: 4, untracked: 2, conflicts: 1 }))).toEqual(
      {
        conflict: 1,
        staged: 4,
        modified: 21,
        untracked: 2,
      },
    )
  })

  test("a clean tree is four zeroes", () => {
    expect(workingTreeCounts(repo())).toEqual({
      conflict: 0,
      staged: 0,
      modified: 0,
      untracked: 0,
    })
  })

  // git counts a file staged *and* edited again on both sides, so the named
  // categories can add up to more than the total. The row still has to draw.
  test("a total smaller than its parts never goes negative", () => {
    expect(workingTreeCounts(repo({ changes: 2, staged: 2, untracked: 2 })).modified).toBe(0)
  })
})

describe("the five squares", () => {
  const squares = (over) => workingTreeSquares(workingTreeCounts(repo(over)))

  test("a clean tree draws none", () => {
    expect(squares()).toEqual([])
  })

  test("one category takes the whole bar", () => {
    expect(squares({ changes: 9 })).toEqual([
      "modified",
      "modified",
      "modified",
      "modified",
      "modified",
    ])
  })

  test("the split is proportional", () => {
    // 6 staged, 4 new: three squares and two.
    expect(squares({ changes: 10, staged: 6, untracked: 4 })).toEqual([
      "staged",
      "staged",
      "staged",
      "untracked",
      "untracked",
    ])
  })

  test("a lone conflict among twenty-seven edits still gets a square, and gets it first", () => {
    const bar = squares({ changes: 28, conflicts: 1 })
    expect(bar).toHaveLength(5)
    expect(bar[0]).toBe("conflict")
    expect(bar.filter((part) => part === "conflict")).toHaveLength(1)
  })

  // Four equal categories cannot share five squares evenly, and the spare one
  // goes to the first in severity order rather than to whichever the sort
  // happened to leave on top.
  test("every category present is drawn, in severity order", () => {
    expect(squares({ changes: 8, staged: 2, untracked: 2, conflicts: 2 })).toEqual([
      "conflict",
      "conflict",
      "staged",
      "modified",
      "untracked",
    ])
  })

  test("the bar is always five squares once anything is in it", () => {
    for (const over of [
      { changes: 1 },
      { changes: 1, untracked: 1 },
      { changes: 3, staged: 1, untracked: 1, conflicts: 1 },
      { changes: 400, staged: 399, untracked: 1 },
    ]) {
      expect(squares(over)).toHaveLength(5)
    }
  })
})
