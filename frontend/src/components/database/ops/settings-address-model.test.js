import { describe, expect, test } from "bun:test"
import { addressDraft } from "./settings-address-model"

const STORED =
  "postgres://app:s3cret@127.0.0.1:5432/shop_main?sslmode=verify-full&connect_timeout=5"
const ROW = { host: "127.0.0.1", port: "5432", user: "app", database: "shop_main" }
const draft = (over = {}) =>
  addressDraft({
    stored: STORED,
    base: STORED,
    shape: "fields",
    edits: {},
    whole: "",
    row: ROW,
    ...over,
  })

describe("the address fields against the saved string", () => {
  test("untouched fields are not a change, read or unread", () => {
    expect(draft()).toMatchObject({ dirty: false, changes: 0, dsn: STORED })
    expect(draft({ stored: undefined, base: undefined })).toMatchObject({
      dirty: false,
      changes: 0,
      dsn: "",
      parts: undefined,
    })
  })

  test("before the string is read an edit is counted against the connection's row", () => {
    const unread = { stored: undefined, base: undefined }
    expect(draft({ ...unread, edits: { host: "db.internal" } })).toMatchObject({
      dirty: true,
      changes: 1,
      dsn: "",
    })
    // Typed back to what the row says: nothing to save, and nothing to read for.
    expect(draft({ ...unread, edits: { host: "127.0.0.1" } }).dirty).toBe(false)
    expect(draft({ ...unread, edits: { password: "n3w" } }).changes).toBe(1)
  })

  test("a change replaces the characters that say it and keeps every option", () => {
    expect(draft({ edits: { host: "db.internal", password: "n3w p@ss" } })).toMatchObject({
      dirty: true,
      changes: 2,
      dsn: "postgres://app:n3w%20p%40ss@db.internal:5432/shop_main?sslmode=verify-full&connect_timeout=5",
    })
  })

  test("an empty password keeps the saved one", () => {
    expect(draft({ edits: { password: "" } })).toMatchObject({ dirty: false, dsn: STORED })
  })

  test("a string typed whole is one change, and the saved one typed again is none", () => {
    expect(draft({ shape: "whole", whole: " postgres://app@db/x " })).toMatchObject({
      dirty: true,
      changes: 1,
      dsn: "postgres://app@db/x",
    })
    expect(draft({ shape: "whole", whole: STORED }).dirty).toBe(false)
    expect(draft({ shape: "whole", whole: "" }).dirty).toBe(false)
    // A row whose string could not be read: whatever is typed replaces it.
    expect(
      draft({ stored: undefined, base: undefined, shape: "whole", whole: "postgres://a@b/c" })
        .dirty,
    ).toBe(true)
  })

  test("fields laid over a retyped string differ from the saved one though none was edited", () => {
    const retyped = "postgres://app:s3cret@db.internal:6000/shop_main?sslmode=require"
    expect(draft({ base: retyped })).toMatchObject({ dirty: true, changes: 1, dsn: retyped })
  })

  test("a file's path emptied is said to be nothing to save", () => {
    const file = { stored: "/srv/notes/notes.db", base: "/srv/notes/notes.db", row: {} }
    expect(draft({ ...file, edits: { database: "/srv/notes/other.db" } })).toMatchObject({
      dirty: true,
      empty: false,
      dsn: "/srv/notes/other.db",
    })
    expect(draft({ ...file, edits: { database: " " } })).toMatchObject({ dirty: true, empty: true })
  })
})
