import { describe, expect, test } from "bun:test"
import {
  BRANCHES,
  DEFAULT_DOCUMENT,
  KINDS,
  SIZE,
  census,
  decodeDocument,
  followersOf,
  foldedByDefault,
  isArranged,
  isFolded,
  kindOf,
  layoutMap,
  relatedTo,
  searchMap,
  shelfCap,
  toggleFold,
  visibleKinds,
} from "./server-map"

const item = (id, name, tone = "ok") => ({ id, name, tone, state: "running", href: `/x/${id}` })

function reading(key, items, extra = {}) {
  return { key, total: items.length, items, state: "read", ...extra }
}

function serverMap(kinds, links = []) {
  return { host: { hostname: "vps-1" }, kinds, links, checkedAt: "2026-10-25T10:00:00Z" }
}

const everyone = () => true
const box = (laid, id) => laid.nodes.find((n) => n.id === id)
const overlaps = (a, b) =>
  a.position.x < b.position.x + b.width &&
  b.position.x < a.position.x + a.width &&
  a.position.y < b.position.y + b.height &&
  b.position.y < a.position.y + a.height

describe("the registry", () => {
  test("every kind belongs to a branch the map draws", () => {
    const branches = new Set(BRANCHES.map((b) => b.key))
    for (const kind of KINDS) expect(branches.has(kind.branch)).toBe(true)
  })

  test("kind keys are unique, so a shelf id names one shelf", () => {
    expect(new Set(KINDS.map((k) => k.key)).size).toBe(KINDS.length)
  })

  test("a kind the server did not send is left out, a tool never is", () => {
    const map = serverMap([reading("containers", [])])
    const keys = visibleKinds(map, everyone).map((k) => k.key)
    expect(keys).toContain("containers")
    expect(keys).toContain("files")
    expect(keys).not.toContain("pm2")
  })

  test("a module this server does not run has no shelf", () => {
    const map = serverMap([reading("pm2", [], { state: "absent" })])
    expect(visibleKinds(map, everyone).map((k) => k.key)).not.toContain("pm2")
  })

  test("a kind the role cannot open is not drawn", () => {
    const map = serverMap([reading("accounts", [item("account:1", "admin")])])
    const readonly = (cap) => cap === "read"
    expect(visibleKinds(map, readonly).map((k) => k.key)).not.toContain("accounts")
    expect(visibleKinds(map, everyone).map((k) => k.key)).toContain("accounts")
  })
})

describe("the layout", () => {
  const map = serverMap([
    reading("projects", [item("project:1", "shop"), item("project:2", "blog")]),
    reading(
      "containers",
      Array.from({ length: 12 }, (_, i) => item(`container:${i}`, `c${i}`)),
    ),
    reading("backups", [item("backup:1", "nightly")]),
    reading("keys", []),
  ])
  const kinds = visibleKinds(map, everyone)

  test("the server is the middle of the map", () => {
    const laid = layoutMap({ map, kinds, doc: DEFAULT_DOCUMENT })
    const server = box(laid, "server")
    expect(server.position.x + server.width / 2).toBe(0)
    expect(server.position.y + server.height / 2).toBe(0)
  })

  test("branches grow on their own side", () => {
    const laid = layoutMap({ map, kinds, doc: DEFAULT_DOCUMENT })
    expect(box(laid, "branch:deployed").position.x).toBeGreaterThan(0)
    expect(box(laid, "branch:jobs").position.x).toBeLessThan(0)
    // A shelf on the left keeps its right edge towards the server.
    const shelf = box(laid, "shelf:backups")
    expect(shelf.position.x + shelf.width).toBeLessThan(box(laid, "branch:jobs").position.x)
  })

  test("a branch with nothing in it is not drawn", () => {
    const laid = layoutMap({
      map,
      kinds: kinds.filter((k) => k.branch !== "host"),
      doc: DEFAULT_DOCUMENT,
    })
    expect(box(laid, "branch:host")).toBeUndefined()
  })

  test("is the same every time", () => {
    const a = layoutMap({ map, kinds, doc: DEFAULT_DOCUMENT })
    const b = layoutMap({ map, kinds, doc: DEFAULT_DOCUMENT })
    expect(a).toEqual(b)
  })

  test("no two shelves, branches or the server overlap", () => {
    const laid = layoutMap({ map, kinds, doc: DEFAULT_DOCUMENT })
    const big = laid.nodes.filter((n) => !n.parentId)
    for (let i = 0; i < big.length; i++) {
      for (let j = i + 1; j < big.length; j++) {
        expect(overlaps(big[i], big[j])).toBe(false)
      }
    }
  })

  test("a long shelf draws two rows and says how many more", () => {
    const laid = layoutMap({ map, kinds, doc: DEFAULT_DOCUMENT })
    const containers = laid.nodes.filter((n) => n.type === "item" && n.kind.key === "containers")
    const cap = shelfCap(kindOf("containers"))
    expect(containers).toHaveLength(cap - 1)
    expect(box(laid, "more:containers").count).toBe(12 - (cap - 1))
  })

  test("an expanded shelf draws every one", () => {
    const doc = { ...DEFAULT_DOCUMENT, expanded: ["shelf:containers"] }
    const laid = layoutMap({ map, kinds, doc })
    expect(laid.nodes.filter((n) => n.type === "item" && n.kind.key === "containers")).toHaveLength(
      12,
    )
    expect(box(laid, "more:containers")).toBeUndefined()
  })

  test("a list the server capped still counts what it held back", () => {
    const capped = serverMap([reading("volumes", [item("volume:a", "a")], { total: 40 })])
    const laid = layoutMap({
      map: capped,
      kinds: visibleKinds(capped, everyone),
      doc: DEFAULT_DOCUMENT,
    })
    expect(box(laid, "more:volumes").count).toBe(39)
  })

  test("an empty shelf that can be added to offers the first one", () => {
    const laid = layoutMap({ map, kinds, doc: DEFAULT_DOCUMENT })
    expect(box(laid, "add:keys")).toBeDefined()
    expect(box(laid, "add:projects")).toBeUndefined()
  })

  test("an unread shelf offers nothing to add: it is not known to be empty", () => {
    const unread = serverMap([reading("keys", [], { state: "unread", reason: "boom" })])
    const laid = layoutMap({
      map: unread,
      kinds: visibleKinds(unread, everyone),
      doc: DEFAULT_DOCUMENT,
    })
    expect(box(laid, "add:keys")).toBeUndefined()
  })

  test("things on a shelf are its children, laid in its grid", () => {
    const laid = layoutMap({ map, kinds, doc: DEFAULT_DOCUMENT })
    const first = box(laid, "project:1")
    const second = box(laid, "project:2")
    expect(first.parentId).toBe("shelf:projects")
    expect(first.position).toEqual({ x: SIZE.shelfPad, y: SIZE.shelfHead })
    expect(second.position.x).toBe(SIZE.shelfPad + SIZE.item.width + SIZE.gap)
  })

  test("a thing placed by hand leaves its shelf and keeps its place", () => {
    const doc = { ...DEFAULT_DOCUMENT, positions: { "project:2": { x: 900, y: 40 } } }
    const laid = layoutMap({ map, kinds, doc })
    const moved = box(laid, "project:2")
    expect(moved.parentId).toBeUndefined()
    expect(moved.position).toEqual({ x: 900, y: 40 })
    expect(laid.edges.find((e) => e.target === "project:2")?.source).toBe("shelf:projects")
    expect(box(laid, "shelf:projects").away).toEqual(["project:2"])
  })

  test("a branch moved by hand takes its shelves along", () => {
    const before = layoutMap({ map, kinds, doc: DEFAULT_DOCUMENT })
    const branch = box(before, "branch:deployed")
    const doc = {
      ...DEFAULT_DOCUMENT,
      positions: { "branch:deployed": { x: branch.position.x + 50, y: branch.position.y - 20 } },
    }
    const after = layoutMap({ map, kinds, doc })
    const was = box(before, "shelf:projects").position
    expect(box(after, "shelf:projects").position).toEqual({ x: was.x + 50, y: was.y - 20 })
  })

  test("a folded branch keeps only its head", () => {
    const doc = { ...DEFAULT_DOCUMENT, collapsed: ["branch:deployed"] }
    const laid = layoutMap({ map, kinds, doc })
    expect(box(laid, "branch:deployed").folded).toBe(true)
    expect(box(laid, "shelf:projects")).toBeUndefined()
    expect(box(laid, "project:1")).toBeUndefined()
  })

  test("a folded shelf keeps its head and no things", () => {
    const doc = { ...DEFAULT_DOCUMENT, collapsed: ["shelf:projects"] }
    const laid = layoutMap({ map, kinds, doc })
    expect(box(laid, "shelf:projects").height).toBe(SIZE.shelfHead)
    expect(box(laid, "project:1")).toBeUndefined()
  })

  test("every line joins two boxes that are drawn", () => {
    const laid = layoutMap({ map, kinds, doc: DEFAULT_DOCUMENT })
    const ids = new Set(laid.nodes.map((n) => n.id))
    for (const edge of laid.edges) {
      expect(ids.has(edge.source)).toBe(true)
      expect(ids.has(edge.target)).toBe(true)
    }
  })
})

describe("dragging", () => {
  const map = serverMap([
    reading("projects", [item("project:1", "shop"), item("project:2", "blog")]),
    reading("databases", [item("db:1", "main")]),
  ])
  const kinds = visibleKinds(map, everyone)
  const doc = { ...DEFAULT_DOCUMENT, positions: { "project:2": { x: 900, y: 40 } } }
  const laid = layoutMap({ map, kinds, doc })

  test("a branch takes its shelves and what was pulled off them", () => {
    const followers = followersOf(laid, "branch:deployed")
    expect(followers).toContain("shelf:projects")
    expect(followers).toContain("shelf:databases")
    expect(followers).toContain("project:2")
    // Things still on a shelf move with it as its children, not as followers.
    expect(followers).not.toContain("project:1")
  })

  test("the server takes everything that has no shelf to carry it", () => {
    const followers = followersOf(laid, "server")
    expect(followers).toContain("branch:deployed")
    expect(followers).toContain("project:2")
    expect(followers).not.toContain("project:1")
  })

  test("a thing moves alone", () => {
    expect(followersOf(laid, "project:1")).toEqual([])
  })
})

describe("folding", () => {
  const byDefault = foldedByDefault(KINDS, new Set(["docker"]))

  test("shelves in a hidden section start folded", () => {
    expect(isFolded(DEFAULT_DOCUMENT, "shelf:containers", byDefault)).toBe(true)
    expect(isFolded(DEFAULT_DOCUMENT, "shelf:projects", byDefault)).toBe(false)
  })

  test("opening one folded by default remembers it was opened", () => {
    const doc = toggleFold(DEFAULT_DOCUMENT, "shelf:containers", byDefault)
    expect(isFolded(doc, "shelf:containers", byDefault)).toBe(false)
    expect(doc.opened).toEqual(["shelf:containers"])
    const again = toggleFold(doc, "shelf:containers", byDefault)
    expect(isFolded(again, "shelf:containers", byDefault)).toBe(true)
    expect(again.opened).toEqual([])
  })

  test("folding one of the reader's own is a choice it keeps", () => {
    const doc = toggleFold(DEFAULT_DOCUMENT, "branch:jobs", byDefault)
    expect(doc.collapsed).toEqual(["branch:jobs"])
    expect(isArranged(doc)).toBe(true)
  })
})

describe("the saved document", () => {
  test("anything unreadable falls back to the defaults", () => {
    expect(decodeDocument(null)).toEqual(DEFAULT_DOCUMENT)
    expect(decodeDocument([])).toEqual(DEFAULT_DOCUMENT)
  })

  test("keeps what it can read and drops what it cannot", () => {
    const doc = decodeDocument({
      positions: { a: { x: 1, y: 2 }, b: { x: "1", y: 2 }, c: null },
      collapsed: ["branch:jobs", 4, "branch:jobs"],
      viewport: { x: 1, y: 2, zoom: 0 },
      minimap: false,
    })
    expect(doc.positions).toEqual({ a: { x: 1, y: 2 } })
    expect(doc.collapsed).toEqual(["branch:jobs"])
    expect(doc.viewport).toBeUndefined()
    expect(doc.minimap).toBe(false)
    expect(doc.connections).toBe(true)
  })
})

describe("reading the map", () => {
  const map = serverMap(
    [
      reading("projects", [item("project:1", "shop")]),
      reading("domains", [item("domain:shop.example.com", "shop.example.com")]),
      reading("databases", [item("db:1", "main", "danger")]),
      reading("volumes", [item("volume:data", "data")]),
    ],
    [
      { from: "domain:shop.example.com", to: "project:1", via: "route" },
      { from: "project:1", to: "db:1", via: "binding" },
      { from: "project:1", to: "volume:gone", via: "mount" },
    ],
  )

  test("a thing's links run both ways, and only to things on the map", () => {
    const related = relatedTo(map, "project:1")
    expect(related.map((r) => r.item.id)).toEqual(["db:1", "domain:shop.example.com"])
    expect(related.find((r) => r.item.id === "domain:shop.example.com").direction).toBe("from")
  })

  test("a search finds by name and by kind", () => {
    expect(searchMap(map, "shop")).toEqual(new Set(["project:1", "domain:shop.example.com"]))
    expect(searchMap(map, "volumes")).toEqual(new Set(["volume:data"]))
    expect(searchMap(map, "  ")).toBeNull()
  })

  test("the census counts what is drawn and what is down", () => {
    expect(census(map, visibleKinds(map, everyone))).toEqual({ things: 4, down: 1 })
  })
})
