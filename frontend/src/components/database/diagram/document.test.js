import { describe, expect, test } from "bun:test"
import {
  DEFAULT_DOCUMENT,
  chooseDocument,
  decodeDocument,
  isArranged,
  migrateDocument,
} from "./document"

const v1 = {
  version: 1,
  direction: "TB",
  positions: { orders: { x: 10, y: 20 }, gone: { x: 1, y: 2 }, bad: { x: "a" } },
  hidden: ["audit_log", 7],
  notes: { orders: "the money" },
  colors: { orders: "red", customers: "chartreuse" },
}

describe("a stored diagram", () => {
  test("is read field by field: a wrong shape is dropped, not drawn", () => {
    const doc = decodeDocument(v1)
    expect(doc.direction).toBe("TB")
    expect(doc.positions).toEqual({ orders: { x: 10, y: 20 }, gone: { x: 1, y: 2 } })
    expect(doc.hidden).toEqual(["audit_log"])
    expect(doc.colors).toEqual({ orders: "red" })
    expect(doc.legacy).toBe(true)
    expect(decodeDocument("nonsense")).toBeNull()
    expect(decodeDocument({ version: 2 }).legacy).toBeUndefined()
  })

  test("one keyed by bare names is re-keyed by the tables of its picture", () => {
    const tables = [
      { id: "public.orders", name: "orders" },
      { id: "public.audit_log", name: "audit_log" },
      { id: "public.customers", name: "customers" },
    ]
    const doc = migrateDocument(decodeDocument(v1), tables)
    expect(doc.version).toBe(2)
    expect(doc.legacy).toBeUndefined()
    expect(doc.positions).toEqual({ "public.orders": { x: 10, y: 20 }, gone: { x: 1, y: 2 } })
    expect(doc.hidden).toEqual(["public.audit_log"])
    expect(doc.notes).toEqual({ "public.orders": "the money" })
    expect(doc.colors).toEqual({ "public.orders": "red" })
  })

  test("a name two schemas share is given to neither: nobody can say which was meant", () => {
    const tables = [
      { id: "sales.orders", name: "orders" },
      { id: "archive.orders", name: "orders" },
    ]
    const doc = migrateDocument(decodeDocument(v1), tables)
    expect(doc.positions).toEqual({ gone: { x: 1, y: 2 } })
    expect(doc.notes).toEqual({})
  })

  test("a document already keyed by id is left as it is", () => {
    const doc = { ...DEFAULT_DOCUMENT, positions: { "public.orders": { x: 1, y: 1 } } }
    expect(migrateDocument(doc, [{ id: "public.orders", name: "orders" }])).toEqual(doc)
  })

  test("a fresh diagram is not an arrangement; a moved table is", () => {
    expect(isArranged(DEFAULT_DOCUMENT)).toBe(false)
    expect(isArranged({ ...DEFAULT_DOCUMENT, hidden: ["public.t"] })).toBe(true)
    // Where the canvas was last looking is not something to forget or to save a name for.
    expect(isArranged({ ...DEFAULT_DOCUMENT, viewport: { x: 0, y: 0, zoom: 1 } })).toBe(false)
  })
})

describe("which copy of an arrangement opens", () => {
  const mine = { ...DEFAULT_DOCUMENT, detail: "keys" }
  const theirs = { ...DEFAULT_DOCUMENT, detail: "names" }

  test("the server's, when this browser holds nothing the server has not confirmed", () => {
    const chosen = chooseDocument(
      { doc: mine },
      { layout: theirs, updatedAt: "2026-10-01T10:00:00Z" },
    )
    expect(chosen.document.detail).toBe("names")
    expect(chosen.saved).toBe(true)
  })

  test("this browser's, when its change is newer than the server's copy", () => {
    const chosen = chooseDocument(
      { doc: mine, dirtyAt: "2026-10-01T10:05:00Z" },
      { layout: theirs, updatedAt: "2026-10-01T10:00:00Z" },
    )
    expect(chosen.document.detail).toBe("keys")
    expect(chosen.saved).toBe(false)
  })

  test("the server's again, when somebody saved after this browser's change", () => {
    const chosen = chooseDocument(
      { doc: mine, dirtyAt: "2026-10-01T10:05:00Z" },
      { layout: theirs, updatedAt: "2026-10-01T11:00:00Z" },
    )
    expect(chosen.document.detail).toBe("names")
  })

  test("this browser's, when the server has none; the defaults, when neither has", () => {
    expect(chooseDocument({ doc: mine }, { layout: null })).toMatchObject({ saved: false })
    expect(chooseDocument(null, { layout: null })).toEqual({
      document: DEFAULT_DOCUMENT,
      saved: true,
    })
  })
})
