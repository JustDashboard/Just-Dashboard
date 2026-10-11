import { describe, expect, test } from "bun:test"
import { parseMarkdown } from "./markdown"
import {
  arrowhead,
  borderPoint,
  boundsOf,
  carriedBy,
  clipboardText,
  copyNotes,
  createLink,
  createNote,
  linkEnds,
  linkGeometry,
  noteAt,
  noteTitle,
  readClipboard,
  removeNotes,
  reorder,
  searchNotes,
  taskProgress,
  TEMPLATES,
  toggleTask,
} from "./stickyboard"

/** The task items the note's renderer draws, in the order it draws them. */
function drawnTasks(text) {
  const tasks = []
  const walk = (blocks) => {
    for (const block of blocks) {
      if (block.t === "list") {
        for (const item of block.items) {
          if (item.task) tasks.push(item.done)
          walk(item.c)
        }
      } else if (block.t === "quote") walk(block.c)
    }
  }
  walk(parseMarkdown(text))
  return tasks
}

const at = (x, y, w = 100, h = 100, extra = {}) => ({
  ...createNote("sticky", { x: 0, y: 0 }),
  x,
  y,
  w,
  h,
  ...extra,
})

describe("ticking a task in a note", () => {
  const text = [
    "# Release",
    "- [ ] changelog",
    "- [x] tag",
    "  - [ ] nested",
    "",
    "```",
    "- [ ] not a box",
    "```",
    "> - [ ] quoted",
    "1. [ ] numbered",
    "- [ ]",
  ].join("\n")

  test("counts tasks exactly as the note draws them", () => {
    expect(drawnTasks(text)).toEqual([false, true, false, false, false])
    expect(taskProgress(text)).toEqual({ done: 1, total: 5 })
  })

  test("each index flips the box the reader pressed and nothing else", () => {
    for (let i = 0; i < 5; i++) {
      const before = drawnTasks(text)
      const after = drawnTasks(toggleTask(text, i))
      expect(after).toEqual(before.map((done, j) => (j === i ? !done : done)))
    }
    expect(toggleTask(text, 1).split("\n")[2]).toBe("- [ ] tag")
    expect(toggleTask(text, 0).split("\n")[1]).toBe("- [x] changelog")
  })

  test("a box past the last one changes nothing", () => {
    expect(toggleTask(text, 9)).toBe(text)
  })
})

describe("where a link meets a note", () => {
  const box = { x: 0, y: 0, w: 200, h: 100 }

  test("on the border facing the point, naming the side", () => {
    expect(borderPoint(box, { x: 500, y: 50 })).toEqual({ x: 200, y: 50, side: "right" })
    expect(borderPoint(box, { x: -500, y: 50 })).toEqual({ x: 0, y: 50, side: "left" })
    expect(borderPoint(box, { x: 100, y: 900 })).toEqual({ x: 100, y: 100, side: "bottom" })
    // Steeper than the box's own diagonal, a line leaves by the bottom.
    expect(borderPoint(box, { x: 400, y: 100 }).side).toBe("right")
    expect(borderPoint(box, { x: 400, y: 250 }).side).toBe("bottom")
  })

  test("both ends of a link face each other", () => {
    const { source, target } = linkEnds(box, { x: 400, y: 0, w: 200, h: 100 })
    expect(source).toEqual({ x: 200, y: 50, side: "right" })
    expect(target).toEqual({ x: 400, y: 50, side: "left" })
  })

  test("a point at the centre does not divide by zero", () => {
    expect(borderPoint(box, { x: 100, y: 50 })).toEqual({ x: 100, y: 0, side: "top" })
  })
})

describe("the path a link draws", () => {
  const left = { x: 0, y: 0, w: 200, h: 100 }
  const right = { x: 500, y: 200, w: 200, h: 100 }
  const below = { x: 0, y: 400, w: 200, h: 100 }

  test("a straight link runs border to border and points at its target", () => {
    const g = linkGeometry(left, { x: 500, y: 0, w: 200, h: 100 }, "straight")
    expect(g.d).toBe("M200,50 L500,50")
    expect(g.end).toMatchObject({ x: 500, y: 50, angle: 0 })
    expect(g.start.angle).toBeCloseTo(Math.PI)
  })

  test("a curve leaves each note square to its side", () => {
    const g = linkGeometry(left, right, "curved")
    expect(g.d.startsWith("M200,")).toBe(true)
    expect(g.d).toContain(" C")
    // Arriving through the target's left side, the head points right.
    expect(Math.abs(g.end.angle)).toBeLessThan(Math.PI / 2)
  })

  test("an elbow runs through the widest gap with square corners rounded", () => {
    const across = linkGeometry(left, right, "elbow")
    expect(across.start).toMatchObject({ x: 200, y: 50 })
    expect(across.end).toMatchObject({ x: 500, y: 250, angle: 0 })
    expect(across.d).toContain(" Q")
    const down = linkGeometry(left, below, "elbow")
    expect(down.start).toMatchObject({ x: 100, y: 100 })
    expect(down.end).toMatchObject({ x: 100, y: 400 })
    expect(down.end.angle).toBeCloseTo(Math.PI / 2)
    // In line, the route is one straight run with nothing to round.
    expect(down.d).not.toContain(" Q")
  })

  test("an arrowhead's tip is the link's end", () => {
    expect(arrowhead({ x: 10, y: 0, angle: 0 }, 10).startsWith("M10,0 L")).toBe(true)
  })
})

describe("what a point and a section hold", () => {
  const section = at(0, 0, 1000, 1000, { kind: "section", id: "s" })
  const under = at(100, 100, 200, 200, { id: "under" })
  const over = at(150, 150, 200, 200, { id: "over" })
  const outside = at(2000, 0, 200, 200, { id: "outside" })
  const pinned = at(500, 500, 100, 100, { id: "pinned", locked: true })
  const notes = [section, under, over, outside, pinned]

  test("the last-drawn note wins, and a section only where no note is", () => {
    expect(noteAt(notes, { x: 200, y: 200 })?.id).toBe("over")
    expect(noteAt(notes, { x: 120, y: 120 })?.id).toBe("under")
    expect(noteAt(notes, { x: 900, y: 900 })?.id).toBe("s")
    expect(noteAt(notes, { x: 200, y: 200 }, 0, "over")?.id).toBe("under")
    expect(noteAt(notes, { x: 5000, y: 5000 })).toBeNull()
  })

  test("a section carries what lies wholly inside it and is not locked", () => {
    expect(carriedBy(notes, "s").sort()).toEqual(["over", "under"])
    expect(carriedBy(notes, "under")).toEqual([])
  })

  test("bounds cover every rect", () => {
    expect(boundsOf([under, outside])).toEqual({ x: 100, y: 0, w: 2100, h: 300 })
    expect(boundsOf([])).toBeNull()
  })
})

describe("changing a board", () => {
  const a = at(0, 0, 100, 100, { id: "a", text: "Deploy **API**" })
  const b = at(200, 0, 100, 100, { id: "b", text: "Rotate keys" })
  const c = at(400, 0, 100, 100, { id: "c", kind: "section", text: "Ops" })
  const ab = createLink("a", "b")
  const bc = createLink("b", "c")

  test("removing a note takes its links with it", () => {
    const doc = removeNotes(
      { version: 1, background: "dots", notes: [a, b, c], links: [ab, bc] },
      new Set(["b"]),
    )
    expect(doc.notes.map((n) => n.id)).toEqual(["a", "c"])
    expect(doc.links).toEqual([])
  })

  test("a copy keeps the links inside the group, with new ids, and unlocks", () => {
    const copied = copyNotes([{ ...a, locked: true }, b], [ab, bc], { x: 24, y: 24 })
    expect(copied.notes).toHaveLength(2)
    expect(copied.notes[0]).toMatchObject({ x: 24, y: 24, text: a.text, locked: false })
    expect(copied.notes[0].id).not.toBe("a")
    expect(copied.links).toHaveLength(1)
    expect(copied.links[0].from).toBe(copied.notes[0].id)
    expect(copied.links[0].to).toBe(copied.notes[1].id)
  })

  test("reordering keeps sections under every note", () => {
    const front = reorder([c, a, b], new Set(["a", "c"]), "front")
    expect(front.map((n) => n.id)).toEqual(["c", "b", "a"])
    const back = reorder([c, a, b], new Set(["b"]), "back")
    expect(back.map((n) => n.id)).toEqual(["c", "b", "a"])
  })

  test("search folds case and accents and needs every word", () => {
    const cafe = at(0, 0, 100, 100, { text: "Café menu for Friday" })
    expect(searchNotes([a, b, cafe], "cafe fri")).toEqual([cafe])
    expect(searchNotes([a, b, cafe], "  ")).toEqual([])
    expect(searchNotes([a, b, cafe], "api")).toEqual([a])
  })

  test("a note's title is its first line without its marks", () => {
    expect(noteTitle({ text: "\n## Plan **it**\nmore" })).toBe("Plan it")
    expect(noteTitle({ text: "- [x] shipped" })).toBe("shipped")
    expect(noteTitle({ text: "   " })).toBe("Empty note")
  })
})

describe("the clipboard", () => {
  test("round-trips notes and links, and ignores plain text", () => {
    const a = at(0, 0)
    const b = at(200, 0)
    const link = createLink(a.id, b.id)
    expect(readClipboard(clipboardText([a, b], [link]))).toEqual({ notes: [a, b], links: [link] })
    expect(readClipboard("just some words")).toBeNull()
    expect(readClipboard('{"notes":[],"links":[]}')).toBeNull()
  })
})

describe("templates", () => {
  test("every template is a board the server accepts", () => {
    const kinds = new Set(["sticky", "text", "section"])
    for (const template of TEMPLATES) {
      const doc = template.build()
      const ids = new Set(doc.notes.map((n) => n.id))
      expect(ids.size).toBe(doc.notes.length)
      for (const note of doc.notes) {
        expect(kinds.has(note.kind)).toBe(true)
        expect(note.w).toBeGreaterThanOrEqual(24)
        expect(note.h).toBeGreaterThanOrEqual(24)
      }
      for (const link of doc.links) {
        expect(ids.has(link.from) && ids.has(link.to) && link.from !== link.to).toBe(true)
      }
      // Sections are the ground the notes stand on, so they are drawn first.
      const firstNote = doc.notes.findIndex((n) => n.kind !== "section")
      const lastSection = doc.notes.findLastIndex((n) => n.kind === "section")
      expect(lastSection < firstNote || firstNote === -1 || lastSection === -1).toBe(true)
    }
  })

  test("building a template twice gives fresh ids", () => {
    const kanban = TEMPLATES.find((t) => t.id === "kanban")
    expect(kanban.build().notes[0].id).not.toBe(kanban.build().notes[0].id)
  })
})
