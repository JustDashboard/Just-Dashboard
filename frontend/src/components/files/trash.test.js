import { describe, expect, test } from "bun:test"
import {
  ageWords,
  cameFrom,
  deletedWhen,
  indexAge,
  itemSize,
  keepSentence,
  keptFor,
  keptShort,
  trashSentence,
} from "./trash"
import {
  carryIdentity,
  copyOperation,
  deleteOperation,
  moveOperation,
  pushOperation,
  runUndo,
  UNDO_DEPTH,
} from "./undo"

const NOW = Date.parse("2026-11-20T12:00:00Z")
const DAY = 24 * 60 * 60 * 1000
const at = (ms) => new Date(NOW - ms).toISOString()

describe("the Trash in words", () => {
  test("how long something is still kept", () => {
    expect(keptFor({ expiresAt: new Date(NOW + 29.5 * DAY).toISOString() }, NOW)).toBe(
      "kept 29 more days",
    )
    expect(keptFor({ expiresAt: new Date(NOW + 1.2 * DAY).toISOString() }, NOW)).toBe(
      "kept 1 more day",
    )
    expect(keptFor({ expiresAt: new Date(NOW + 3600_000).toISOString() }, NOW)).toBe("goes today")
    expect(keptFor({}, NOW)).toBe("kept until emptied")
    expect(keptShort({ expiresAt: new Date(NOW + 3 * DAY).toISOString() }, NOW)).toBe("3 days")
    expect(keptShort({}, NOW)).toBe("until emptied")
  })

  test("ages are words, not unit letters", () => {
    expect(ageWords(at(10_000), NOW)).toBe("just now")
    expect(ageWords(at(5 * 60_000), NOW)).toBe("5 minutes ago")
    expect(ageWords(at(2 * 3600_000), NOW)).toBe("2 hours ago")
    expect(ageWords(at(3 * DAY), NOW)).toBe("3 days ago")
    expect(deletedWhen({ deletedAt: at(3600_000), deletedBy: "ada" }, NOW)).toBe(
      "1 hour ago by ada",
    )
  })

  test("what the Trash holds, as one sentence", () => {
    expect(trashSentence({ items: [], bytes: 0, partial: false })).toBe("Nothing in the Trash")
    expect(trashSentence({ items: [{}, {}, {}], bytes: 1.2 * 1024 ** 3, partial: false })).toBe(
      "3 items, 1.2 GB",
    )
    expect(trashSentence({ items: [{}], bytes: 2048, partial: true })).toBe(
      "1 item, at least 2.0 KB",
    )
    expect(keepSentence(30)).toBe("kept 30 days")
    expect(keepSentence(0)).toBe("kept until the Trash is emptied")
    expect(itemSize({ size: 0, sizeKnown: false })).toBe("measuring")
  })

  test("where it came from is its folder", () => {
    expect(cameFrom({ originalPath: "/etc/nginx/nginx.conf" })).toBe("/etc/nginx")
    expect(cameFrom({ originalPath: "/srv" })).toBe("/")
    expect(cameFrom({ originalPath: "" })).toBe("an unrecorded place")
  })
})

describe("Find's index in words", () => {
  test("an answer from the index says how old it is, and a walk says nothing", () => {
    const index = { builtAt: "", ageSeconds: 7200, entries: 1, complete: true, building: false }
    expect(indexAge({ source: "index", index })).toBe("index from 2 hours ago")
    expect(indexAge({ source: "index", index: { ...index, ageSeconds: 5 } })).toBe(
      "index from just now",
    )
    expect(indexAge({ source: "walk" })).toBe("")
    expect(indexAge(undefined)).toBe("")
  })
})

describe("undo, as data", () => {
  const item = (id, name) => ({ id, name })

  test("a move goes back last first, then what it replaced returns", () => {
    const op = moveOperation("moving 2 items", [
      { from: "/a/x", to: "/b/x" },
      { from: "/a/y", to: "/b/y", replaced: item("r1", "y") },
    ])
    expect(op.steps).toEqual([
      { kind: "move", from: "/b/y", to: "/a/y" },
      { kind: "restore", id: "r1", name: "y" },
      { kind: "move", from: "/b/x", to: "/a/x" },
    ])
  })

  test("a copy is undone by trashing the copy, and only by an account that may delete", () => {
    const op = copyOperation("copying x", [{ to: "/b/x", replaced: item("r2", "x") }], true)
    expect(op.steps).toEqual([
      { kind: "trash", path: "/b/x" },
      { kind: "restore", id: "r2", name: "x" },
    ])
    const refused = copyOperation("copying x", [{ to: "/b/x" }], false)
    expect(refused.steps).toEqual([])
    expect(refused.why).toContain("cannot")
  })

  test("a delete restores what went to the Trash and says what cannot come back", () => {
    expect(deleteOperation("deleting 2 items", [item("a", "x"), item("b", "y")], 0).steps).toEqual([
      { kind: "restore", id: "b", name: "y" },
      { kind: "restore", id: "a", name: "x" },
    ])
    const forGood = deleteOperation("deleting x", [], 1)
    expect(forGood.steps).toEqual([])
    expect(forGood.why).toBe("It cannot be undone.")
  })

  test("running stops at the first step that fails", async () => {
    const calls = []
    const result = await runUndo(
      moveOperation("moving", [
        { from: "/a/x", to: "/b/x" },
        { from: "/a/y", to: "/b/y", replaced: item("r", "y") },
      ]),
      {
        move: async (from, to) => {
          calls.push(`move ${from} ${to}`)
          if (from === "/b/y") throw new Error("taken")
        },
        restore: async (id) => calls.push(`restore ${id}`),
        trash: async (path) => calls.push(`trash ${path}`),
      },
    )
    expect(calls).toEqual(["move /b/y /a/y"])
    expect(result.done).toBe(0)
    expect(result.failed.step.kind).toBe("move")
  })

  test("a step names the entry its operation left, and the runner is given it", async () => {
    const op = moveOperation("moving x", [{ from: "/a/x", to: "/b/x", identity: "9:12:1.5" }])
    const copy = copyOperation("copying y", [{ to: "/b/y", identity: "9:13:2.5" }], true)
    const calls = []
    const runner = {
      move: async (from, to, identity) => calls.push(`move ${from} ${to} ${identity}`),
      restore: async (id) => calls.push(`restore ${id}`),
      trash: async (path, identity) => calls.push(`trash ${path} ${identity}`),
    }
    await runUndo(op, runner)
    await runUndo(copy, runner)
    expect(calls).toEqual(["move /b/x /a/x 9:12:1.5", "trash /b/y 9:13:2.5"])
  })

  test("a save in the editor carries a waiting step to the file it left, and nothing else", () => {
    const op = moveOperation("renaming x", [{ from: "/a/x", to: "/a/y", identity: "9:12:1.5" }])
    const copy = copyOperation("duplicating z", [{ to: "/a/z copy", identity: "9:14:3.5" }], true)
    // The editor saved /a/y, which was the renamed file: the undo follows it.
    carryIdentity(op, "/a/y", "9:12:1.5", "9:20:7.5")
    expect(op.steps).toEqual([{ kind: "move", from: "/a/y", to: "/a/x", identity: "9:20:7.5" }])
    carryIdentity(copy, "/a/z copy", "9:14:3.5", "9:21:8.5")
    expect(copy.steps).toEqual([{ kind: "trash", path: "/a/z copy", identity: "9:21:8.5" }])
    // What the editor saved over had another identity (something else took
    // the name), or it saved another path: the step keeps its own.
    carryIdentity(op, "/a/y", "9:30:1.5", "9:31:2.5")
    carryIdentity(op, "/a/x", "9:20:7.5", "9:32:2.5")
    carryIdentity(op, "/a/y", "", "9:33:2.5")
    expect(op.steps).toEqual([{ kind: "move", from: "/a/y", to: "/a/x", identity: "9:20:7.5" }])
  })

  test("the stack keeps the newest operations", () => {
    let stack = []
    for (let i = 0; i < UNDO_DEPTH + 5; i++)
      stack = pushOperation(stack, { label: `${i}`, steps: [] })
    expect(stack).toHaveLength(UNDO_DEPTH)
    expect(stack.at(-1).label).toBe(`${UNDO_DEPTH + 4}`)
  })
})
