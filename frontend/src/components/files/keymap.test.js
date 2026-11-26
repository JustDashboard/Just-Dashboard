import { describe, expect, test } from "bun:test"
import { filesKey, moveIndex, TYPING_PAUSE_MS } from "./keymap"

const photos = { name: "photos", path: "/srv/photos", isDir: true }
const notes = { name: "notes.txt", path: "/srv/notes.txt", isDir: false }
const pictures = { name: "pictures", path: "/srv/pictures", isDir: true }
const readme = { name: "README.md", path: "/srv/README.md", isDir: false }
const ENTRIES = [photos, notes, pictures, readme]
const NOW = 10_000

const CODES = { " ": "Space", ".": "Period", ">": "Period" }

/** A key Files owns, landing on the listing itself, with no modifier unless one is given. */
function press(key, extra = {}) {
  return {
    key,
    code: CODES[key] ?? "",
    metaKey: false,
    ctrlKey: false,
    shiftKey: false,
    altKey: false,
    owned: true,
    typing: false,
    onControl: false,
    ...extra,
  }
}
const ctrl = (key, extra) => press(key, { ctrlKey: true, ...extra })
const cmd = (key, extra) => press(key, { metaKey: true, ...extra })
const alt = (key, extra) => press(key, { altKey: true, ...extra })
const shift = (key, extra) => press(key, { shiftKey: true, ...extra })

/** A writable folder with somewhere to go back, forward and up to, nothing active or selected. */
function state(extra = {}) {
  return {
    path: "/srv",
    entries: ENTRIES,
    active: null,
    selected: new Set(),
    clipHeld: false,
    canWrite: true,
    canDestruct: true,
    canBack: true,
    canForward: true,
    up: "/",
    view: "list",
    typed: { query: "", time: 0, dir: "/srv" },
    now: NOW,
    ...extra,
  }
}

const NOTHING = { prevent: false, action: null }
const taken = (action) => ({ prevent: true, action })
const decide = (keyPress, extra) => filesKey(keyPress, state(extra))

describe("where the key lands", () => {
  const every = [
    ctrl("f"),
    ctrl("a"),
    press("F5"),
    alt("ArrowLeft"),
    press("Escape"),
    press("Delete"),
    press("Backspace"),
    press("Enter"),
    press("ArrowDown"),
    press("n"),
  ]

  test("nothing is taken while a dialog, menu or listbox is open, or outside the workspace", () => {
    for (const keyPress of every) {
      expect(decide({ ...keyPress, owned: false }, { active: notes })).toEqual(NOTHING)
    }
  })

  test("nothing is taken from a field being typed in", () => {
    for (const keyPress of every) {
      expect(decide({ ...keyPress, typing: true }, { active: notes })).toEqual(NOTHING)
    }
    expect(decide(ctrl("c", { typing: true }), { active: notes })).toEqual(NOTHING)
    expect(decide(ctrl("v", { typing: true }))).toEqual(NOTHING)
  })

  test("a control keeps its own Enter, Space, arrows and letters", () => {
    const control = { onControl: true }
    expect(decide(press("Enter", control), { active: notes })).toEqual(NOTHING)
    expect(decide(press(" ", control), { active: notes })).toEqual(NOTHING)
    for (const key of ["ArrowDown", "ArrowUp", "ArrowLeft", "ArrowRight", "Home", "End"]) {
      expect(decide(press(key, control), { active: photos })).toEqual(NOTHING)
    }
    expect(decide(shift("?", control))).toEqual(NOTHING)
    expect(decide(press("r", control))).toEqual(NOTHING)
  })

  test("a control does not keep Escape, F2, Delete or Backspace", () => {
    const control = { onControl: true }
    expect(decide(press("Escape", control), { active: notes }).action.kind).toBe("escape")
    expect(decide(press("F2", control), { active: notes }).action.kind).toBe("rename")
    expect(decide(press("Delete", control), { active: notes }).action.kind).toBe("delete")
    expect(decide(press("Backspace", control)).action).toEqual({ kind: "up", path: "/" })
  })
})

describe("search", () => {
  test("Ctrl/Cmd+F and Ctrl/Cmd+P search names", () => {
    const names = taken({ kind: "search", mode: "names" })
    expect(decide(ctrl("f"))).toEqual(names)
    expect(decide(cmd("f"))).toEqual(names)
    expect(decide(ctrl("p"))).toEqual(names)
    expect(decide(cmd("P"))).toEqual(names)
  })

  test("Ctrl/Cmd+Shift+F searches contents; Shift+P is not a search", () => {
    const contents = taken({ kind: "search", mode: "content" })
    expect(decide(ctrl("F", { shiftKey: true }))).toEqual(contents)
    expect(decide(cmd("F", { shiftKey: true }))).toEqual(contents)
    expect(decide(ctrl("P", { shiftKey: true }))).toEqual(NOTHING)
  })
})

describe("refresh", () => {
  test("F5 and Ctrl/Cmd+R refresh the folder", () => {
    const reload = taken({ kind: "reload" })
    expect(decide(press("F5"))).toEqual(reload)
    expect(decide(ctrl("F5"))).toEqual(reload)
    expect(decide(ctrl("r"))).toEqual(reload)
    expect(decide(cmd("r"))).toEqual(reload)
  })

  test("with Shift or Alt held the refresh is the browser's", () => {
    expect(decide(shift("F5"))).toEqual(NOTHING)
    expect(decide(alt("F5"))).toEqual(NOTHING)
    expect(decide(ctrl("R", { shiftKey: true }))).toEqual(NOTHING)
    expect(decide(cmd("r", { altKey: true }))).toEqual(NOTHING)
  })
})

describe("history", () => {
  test("Alt+Left and Cmd+[ go back", () => {
    expect(decide(alt("ArrowLeft"))).toEqual(taken({ kind: "back" }))
    expect(decide(cmd("["))).toEqual(taken({ kind: "back" }))
    // Ctrl+[ is not Cmd+[.
    expect(decide(ctrl("["))).toEqual(NOTHING)
  })

  test("Alt+Right and Cmd+] go forward", () => {
    expect(decide(alt("ArrowRight"))).toEqual(taken({ kind: "forward" }))
    expect(decide(cmd("]"))).toEqual(taken({ kind: "forward" }))
    expect(decide(ctrl("]"))).toEqual(NOTHING)
  })

  test("with nowhere to go the key is left alone, and not read as an arrow", () => {
    expect(decide(alt("ArrowLeft"), { canBack: false })).toEqual(NOTHING)
    expect(decide(cmd("["), { canBack: false })).toEqual(NOTHING)
    expect(decide(alt("ArrowRight"), { canForward: false, active: photos })).toEqual(NOTHING)
    expect(decide(cmd("]"), { canForward: false })).toEqual(NOTHING)
  })
})

describe("up a level", () => {
  test("Alt+Up, Backspace and Left in the details view go to the parent", () => {
    const up = taken({ kind: "up", path: "/" })
    expect(decide(alt("ArrowUp"))).toEqual(up)
    expect(decide(press("Backspace"))).toEqual(up)
    expect(decide(press("ArrowLeft"), { view: "list" })).toEqual(up)
  })

  test("at the top of what the server lists, the key is still the listing's", () => {
    expect(decide(alt("ArrowUp"), { up: null })).toEqual(taken(null))
    expect(decide(press("Backspace"), { up: null })).toEqual(taken(null))
    expect(decide(press("ArrowLeft"), { up: null, view: "list" })).toEqual(taken(null))
  })
})

describe("folders and hidden files", () => {
  test("Ctrl/Cmd+Shift+N makes a folder where the operator may write", () => {
    expect(decide(ctrl("N", { shiftKey: true }))).toEqual(taken({ kind: "new-folder" }))
    expect(decide(cmd("N", { shiftKey: true }))).toEqual(taken({ kind: "new-folder" }))
    expect(decide(ctrl("N", { shiftKey: true }), { canWrite: false })).toEqual(NOTHING)
    expect(decide(ctrl("n"))).toEqual(NOTHING)
  })

  test("Ctrl/Cmd+Shift+. turns hidden files on and off, by the key's place too", () => {
    const toggle = taken({ kind: "toggle-hidden" })
    expect(decide(ctrl(".", { shiftKey: true }))).toEqual(toggle)
    expect(decide(cmd(">", { shiftKey: true }))).toEqual(toggle)
    expect(decide(ctrl("…", { shiftKey: true, code: "Period" }))).toEqual(toggle)
    expect(decide(ctrl("."))).toEqual(NOTHING)
  })
})

describe("selection", () => {
  test("Ctrl/Cmd+Space toggles the active entry", () => {
    expect(decide(ctrl(" "), { active: notes })).toEqual(
      taken({ kind: "toggle", entry: notes, on: true }),
    )
    expect(decide(cmd(" "), { active: notes, selected: new Set([notes.path]) })).toEqual(
      taken({ kind: "toggle", entry: notes, on: false }),
    )
    expect(decide(ctrl(" "))).toEqual(NOTHING)
  })

  test("Ctrl/Cmd+A selects every entry in the folder", () => {
    const all = taken({ kind: "select-all", paths: ENTRIES.map((e) => e.path) })
    expect(decide(ctrl("a"))).toEqual(all)
    expect(decide(cmd("a"))).toEqual(all)
    expect(decide(ctrl("A", { shiftKey: true }))).toEqual(all)
    expect(decide(ctrl("a"), { entries: [] })).toEqual(taken({ kind: "select-all", paths: [] }))
  })

  test("Escape lets go of the selection and the active entry before the clipboard", () => {
    const selection = taken({ kind: "escape", clear: "selection" })
    expect(decide(press("Escape"), { selected: new Set([notes.path]) })).toEqual(selection)
    expect(decide(press("Escape"), { active: notes, clipHeld: true })).toEqual(selection)
    expect(decide(press("Escape"), { clipHeld: true })).toEqual(
      taken({ kind: "escape", clear: "clip" }),
    )
    // With nothing to let go of, Escape still starts name typing again.
    expect(decide(press("Escape"))).toEqual(taken({ kind: "escape", clear: null }))
  })
})

describe("clipboard", () => {
  test("Ctrl/Cmd+C and X hold the selection, or else the active entry, and are not prevented", () => {
    const selected = new Set([notes.path, readme.path])
    expect(decide(ctrl("c"), { selected, active: photos })).toEqual({
      prevent: false,
      action: { kind: "hold-clip", clip: { mode: "copy", paths: [notes.path, readme.path] } },
    })
    expect(decide(cmd("x"), { active: photos })).toEqual({
      prevent: false,
      action: { kind: "hold-clip", clip: { mode: "cut", paths: [photos.path] } },
    })
  })

  test("with nothing to copy, or no right to write, Ctrl/Cmd+C and X are left alone", () => {
    expect(decide(ctrl("c"))).toEqual(NOTHING)
    expect(decide(ctrl("x"))).toEqual(NOTHING)
    expect(decide(ctrl("c"), { active: notes, canWrite: false })).toEqual(NOTHING)
  })

  test("Ctrl/Cmd+V waits for the paste event, in a folder the operator may write", () => {
    const paste = { prevent: false, action: { kind: "hold-paste" } }
    expect(decide(ctrl("v"))).toEqual(paste)
    expect(decide(cmd("v"))).toEqual(paste)
    expect(decide(ctrl("v"), { canWrite: false })).toEqual(NOTHING)
    expect(decide(ctrl("v"), { path: null })).toEqual(NOTHING)
  })

  test("every other modified key is left to the page and the browser", () => {
    for (const keyPress of [ctrl("z"), cmd("s"), alt("n"), ctrl("Delete"), alt("Enter")]) {
      expect(decide(keyPress, { active: notes })).toEqual(NOTHING)
    }
  })
})

describe("the active entry", () => {
  test("F2 renames it where the operator may write", () => {
    expect(decide(press("F2"), { active: notes })).toEqual(taken({ kind: "rename", entry: notes }))
    expect(decide(press("F2"), { active: notes, canWrite: false })).toEqual(NOTHING)
    expect(decide(press("F2"))).toEqual(NOTHING)
  })

  test("Delete sends the selection, or else the active entry, to the trash; Shift past it", () => {
    const selected = new Set([notes.path])
    expect(decide(press("Delete"), { selected, active: photos })).toEqual(
      taken({ kind: "delete-selected", permanent: false }),
    )
    expect(decide(shift("Delete"), { selected })).toEqual(
      taken({ kind: "delete-selected", permanent: true }),
    )
    expect(decide(press("Delete"), { active: photos })).toEqual(
      taken({ kind: "delete", entry: photos, permanent: false }),
    )
    expect(decide(shift("Delete"), { active: photos })).toEqual(
      taken({ kind: "delete", entry: photos, permanent: true }),
    )
  })

  test("Delete without the destructive right is left alone; with nothing to delete it is kept", () => {
    expect(decide(press("Delete"), { active: notes, canDestruct: false })).toEqual(NOTHING)
    expect(decide(press("Delete"))).toEqual(taken(null))
  })

  test("Enter opens it", () => {
    expect(decide(press("Enter"), { active: photos })).toEqual(
      taken({ kind: "open", entry: photos }),
    )
    expect(decide(press("Enter"), { active: notes })).toEqual(taken({ kind: "open", entry: notes }))
    expect(decide(press("Enter"))).toEqual(NOTHING)
  })

  test("Space views a file and leaves a folder alone", () => {
    expect(decide(press(" "), { active: notes })).toEqual(taken({ kind: "view", entry: notes }))
    expect(decide(press(" "), { active: photos })).toEqual(NOTHING)
    expect(decide(press(" "))).toEqual(NOTHING)
  })

  test("? opens the shortcuts", () => {
    expect(decide(shift("?"))).toEqual(taken({ kind: "shortcuts" }))
    expect(decide(press("?"))).toEqual(taken({ kind: "shortcuts" }))
  })
})

describe("arrows", () => {
  test("in the grid every arrow, Home and End move the active entry; Shift extends", () => {
    for (const key of ["ArrowDown", "ArrowUp", "ArrowLeft", "ArrowRight", "Home", "End"]) {
      expect(decide(press(key), { view: "grid", active: notes })).toEqual(
        taken({ kind: "move", key, extend: false }),
      )
      expect(decide(shift(key), { view: "grid", active: notes })).toEqual(
        taken({ kind: "move", key, extend: true }),
      )
    }
  })

  test("in the details view Right enters a folder and is kept on a file", () => {
    expect(decide(press("ArrowRight"), { active: photos })).toEqual(
      taken({ kind: "open", entry: photos }),
    )
    expect(decide(press("ArrowRight"), { active: notes })).toEqual(taken(null))
    expect(decide(press("ArrowRight"))).toEqual(taken(null))
  })

  test("in the details view Up, Down, Home and End move", () => {
    for (const key of ["ArrowDown", "ArrowUp", "Home", "End"]) {
      expect(decide(shift(key))).toEqual(taken({ kind: "move", key, extend: true }))
    }
  })

  test("moveIndex steps a tile row up and down, one across, and starts at the top", () => {
    expect(moveIndex("Home", 5, 10, 4)).toBe(0)
    expect(moveIndex("End", 0, 10, 4)).toBe(9)
    for (const key of ["ArrowDown", "ArrowUp", "ArrowLeft", "ArrowRight"]) {
      expect(moveIndex(key, -1, 10, 4)).toBe(0)
    }
    expect(moveIndex("ArrowDown", 1, 10, 4)).toBe(5)
    expect(moveIndex("ArrowDown", 7, 10, 4)).toBe(9)
    expect(moveIndex("ArrowUp", 5, 10, 4)).toBe(1)
    expect(moveIndex("ArrowUp", 2, 10, 4)).toBe(0)
    expect(moveIndex("ArrowRight", 9, 10, 4)).toBe(9)
    expect(moveIndex("ArrowLeft", 0, 10, 4)).toBe(0)
    expect(moveIndex("ArrowDown", 3, 10, 1)).toBe(4)
  })
})

describe("typing a name", () => {
  const typed = (query, ago = 100, dir = "/srv") => ({ query, time: NOW - ago, dir })
  const find = (keyPress, extra) => decide(keyPress, extra)

  test("a letter jumps to the first name it starts, from the top", () => {
    expect(find(press("r"))).toEqual(
      taken({ kind: "find", typed: { query: "r", time: NOW, dir: "/srv" }, entry: readme }),
    )
    expect(find(shift("N")).action.entry).toBe(notes)
  })

  test("letters close together narrow a prefix that may stay on the active entry", () => {
    const decision = find(press("i"), { active: pictures, typed: typed("p") })
    expect(decision).toEqual(
      taken({ kind: "find", typed: { query: "pi", time: NOW, dir: "/srv" }, entry: pictures }),
    )
    expect(find(press("h"), { active: pictures, typed: typed("p") }).action.entry).toBe(photos)
  })

  test("the same letter again steps to the next name it starts", () => {
    expect(find(press("p"), { active: photos, typed: typed("p") }).action.entry).toBe(pictures)
    expect(find(press("p"), { active: pictures, typed: typed("pp") }).action.entry).toBe(photos)
    expect(find(press("P"), { active: photos, typed: typed("p") }).action.typed.query).toBe("P")
  })

  test("a pause, or another folder, starts a new name", () => {
    const pause = find(press("o"), { active: photos, typed: typed("n", TYPING_PAUSE_MS + 1) })
    expect(pause.action.typed.query).toBe("o")
    expect(find(press("o"), { typed: typed("n", TYPING_PAUSE_MS) }).action.typed.query).toBe("no")
    const elsewhere = find(press("o"), { typed: typed("n", 100, "/etc") })
    expect(elsewhere.action.typed).toEqual({ query: "o", time: NOW, dir: "/srv" })
  })

  test("a letter no name starts with keeps typing without taking the key", () => {
    expect(find(press("z"))).toEqual({
      prevent: false,
      action: { kind: "find", typed: { query: "z", time: NOW, dir: "/srv" }, entry: null },
    })
    expect(find(press("x"), { entries: [] }).action.entry).toBe(null)
  })

  test("keys that are not one character are not typing", () => {
    for (const key of ["Tab", "F1", "Shift", "PageDown", "Dead"]) {
      expect(find(press(key))).toEqual(NOTHING)
    }
  })
})
