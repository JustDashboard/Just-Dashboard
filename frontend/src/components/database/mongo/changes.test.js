import { describe, expect, test } from "bun:test"
import { parseDocument, typedValue } from "./bson"
import {
  NO_EDITS,
  addedUnder,
  buildUpdate,
  countEdits,
  editAt,
  editSummary,
  idsFilter,
  overtaken,
  staged,
  withEdit,
  withStaged,
  withoutEdit,
  withoutStaged,
} from "./changes"

const ID = '{"$oid":"6abe79972945ac11a3124bfc"}'
const ROOT = parseDocument(
  `{"_id":${ID},"name":"User 1","loginCount":{"$numberLong":"3"},` +
    '"address":{"city":"Bucharest","zip":"10001"},"roles":["member"],"verified":true}',
)
const value = (type, text) => {
  const typed = typedValue(type, text)
  if (!typed.ok) throw new Error(typed.message)
  return typed.node
}

describe("staging edits", () => {
  test("an edit of a field replaces the one already staged on it", () => {
    let edits = withEdit(NO_EDITS, { op: "set", path: ["name"], value: value("string", "A") })
    edits = withEdit(edits, { op: "set", path: ["name"], value: value("string", "B") })
    expect(Object.keys(edits)).toHaveLength(1)
    expect(editAt(edits, ["name"]).own.value.text).toBe("B")
  })

  test("removing a document takes the edits staged inside it", () => {
    let edits = withEdit(NO_EDITS, {
      op: "set",
      path: ["address", "city"],
      value: value("string", "Iasi"),
    })
    edits = withEdit(edits, { op: "unset", path: ["address"] })
    expect(Object.values(edits)).toEqual([{ op: "unset", path: ["address"] }])
    expect(editAt(edits, ["address", "city"]).above).toEqual({ op: "unset", path: ["address"] })
  })

  test("an edit is undone by its path, and undoing what is not staged changes nothing", () => {
    const edits = withEdit(NO_EDITS, { op: "unset", path: ["verified"] })
    expect(withoutEdit(edits, ["verified"])).toEqual({})
    expect(withoutEdit(edits, ["name"])).toBe(edits)
  })

  test("a path that only shares a prefix of a name is not below it", () => {
    let edits = withEdit(NO_EDITS, { op: "set", path: ["a", "b"], value: value("int", "1") })
    edits = withEdit(edits, { op: "unset", path: ["a2"] })
    expect(Object.keys(edits)).toHaveLength(2)
  })

  test("fields the document does not have yet are told from changed ones", () => {
    let edits = withEdit(NO_EDITS, { op: "set", path: ["nickname"], value: value("string", "x") })
    edits = withEdit(edits, { op: "set", path: ["name"], value: value("string", "y") })
    edits = withEdit(edits, { op: "unset", path: ["verified"] })
    edits = withEdit(edits, {
      op: "set",
      path: ["address", "country"],
      value: value("string", "RO"),
    })
    expect(addedUnder(edits, ROOT, []).map((edit) => edit.path)).toEqual([["nickname"]])
    expect(addedUnder(edits, ROOT, ["address"]).map((edit) => edit.path)).toEqual([
      ["address", "country"],
    ])
    expect(countEdits(edits, ROOT)).toEqual({ changed: 1, added: 2, removed: 1 })
    expect(editSummary(countEdits(edits, ROOT))).toBe("1 changed, 2 added, 1 removed")
    expect(editSummary({ changed: 0, added: 1, removed: 0 })).toBe("1 added")
  })
})

describe("the update the edits are", () => {
  test("nothing staged is no update", () => {
    expect(buildUpdate(ID, ROOT, NO_EDITS)).toBeNull()
  })

  test("a changed field is a $set that keeps its type, held to the value that was read", () => {
    const edits = withEdit(NO_EDITS, {
      op: "set",
      path: ["loginCount"],
      value: value("long", "12345678901234567"),
    })
    expect(buildUpdate(ID, ROOT, edits)).toEqual({
      filter: `{"_id":${ID},"loginCount":{"$numberLong":"3"}}`,
      update: '{"$set":{"loginCount":{"$numberLong":"12345678901234567"}}}',
      statement: '{ $set: { loginCount: Long("12345678901234567") } }',
    })
  })

  test("a nested field and a list element are named by their path", () => {
    let edits = withEdit(NO_EDITS, {
      op: "set",
      path: ["address", "city"],
      value: value("string", "Iasi"),
    })
    edits = withEdit(edits, { op: "set", path: ["roles", 0], value: value("string", "admin") })
    const update = buildUpdate(ID, ROOT, edits)
    expect(update.update).toBe('{"$set":{"address.city":"Iasi","roles.0":"admin"}}')
    expect(update.filter).toBe(`{"_id":${ID},"address.city":"Bucharest","roles.0":"member"}`)
    expect(update.statement).toBe('{ $set: { "address.city": "Iasi", "roles.0": "admin" } }')
  })

  test("a new field is guarded by its absence, a removed one by what it held", () => {
    let edits = withEdit(NO_EDITS, { op: "set", path: ["nickname"], value: value("string", "x") })
    edits = withEdit(edits, { op: "unset", path: ["verified"] })
    expect(buildUpdate(ID, ROOT, edits)).toEqual({
      filter: `{"_id":${ID},"nickname":{"$exists":false},"verified":true}`,
      update: '{"$set":{"nickname":"x"},"$unset":{"verified":""}}',
      statement: '{ $set: { nickname: "x" }, $unset: { verified: "" } }',
    })
  })

  test("a value an equality cannot be trusted to match is not put in the guard", () => {
    const root = parseDocument(
      `{"_id":${ID},"p":{"$regularExpression":{"pattern":"^a","options":""}}}`,
    )
    const edits = withEdit(NO_EDITS, { op: "set", path: ["p"], value: value("string", "a") })
    expect(buildUpdate(ID, root, edits).filter).toBe(`{"_id":${ID}}`)
  })

  test("a path no update can name is no update, and so is a document with no _id", () => {
    const dotted = withEdit(NO_EDITS, { op: "set", path: ["a.b"], value: value("int", "1") })
    expect(buildUpdate(ID, ROOT, dotted)).toBeNull()
    const edits = withEdit(NO_EDITS, { op: "unset", path: ["verified"] })
    expect(buildUpdate("", ROOT, edits)).toBeNull()
  })
})

describe("an edit remembers what the document held when it was made", () => {
  const CHANGED = parseDocument(
    `{"_id":${ID},"name":"Renamed by somebody","loginCount":{"$numberLong":"3"},` +
      '"address":{"city":"Bucharest","zip":"10001"},"roles":["member"],"verified":true,"nick":"x"}',
  )

  test("the guard is the value read when the edit was staged, not the one read since", () => {
    const edits = staged(NO_EDITS, ROOT, {
      op: "set",
      path: ["name"],
      value: value("string", "Mine"),
    })
    // The document was re-read, and somebody else has changed the field.
    const update = buildUpdate(ID, CHANGED, edits)
    expect(update.filter).toBe(`{"_id":${ID},"name":"User 1"}`)
    expect(overtaken(edits, CHANGED).map((edit) => edit.path)).toEqual([["name"]])
    expect(overtaken(edits, ROOT)).toEqual([])
  })

  test("a field staged as new is guarded by its absence even once somebody has added it", () => {
    const edits = staged(NO_EDITS, ROOT, {
      op: "set",
      path: ["nick"],
      value: value("string", "u1"),
    })
    expect(buildUpdate(ID, CHANGED, edits).filter).toBe(`{"_id":${ID},"nick":{"$exists":false}}`)
    expect(countEdits(edits, CHANGED)).toEqual({ changed: 0, added: 1, removed: 0 })
    expect(overtaken(edits, CHANGED)).toHaveLength(1)
  })

  test("editing the same field again keeps what it held before the first edit", () => {
    let edits = staged(NO_EDITS, ROOT, { op: "set", path: ["name"], value: value("string", "A") })
    edits = staged(edits, CHANGED, { op: "set", path: ["name"], value: value("string", "B") })
    expect(editAt(edits, ["name"]).own.had).toBe('"User 1"')
  })
})

describe("the edits of a connection's collections", () => {
  const one = withEdit(NO_EDITS, { op: "unset", path: ["name"] })

  test("are kept by collection and by document", () => {
    let all = withStaged({}, "app\u0000users", ID, one)
    all = withStaged(all, "app\u0000orders", '"o1"', one)
    expect(Object.keys(all)).toEqual(["app\u0000users", "app\u0000orders"])
    expect(all["app\u0000users"][ID]).toBe(one)
  })

  test("a document with nothing staged leaves, and so does a collection with no document", () => {
    let all = withStaged({}, "app\u0000users", ID, one)
    all = withStaged(all, "app\u0000users", '"second"', one)
    all = withStaged(all, "app\u0000users", ID, NO_EDITS)
    expect(Object.keys(all["app\u0000users"])).toEqual(['"second"'])
    expect(withStaged(all, "app\u0000users", '"second"', NO_EDITS)).toEqual({})
    expect(withoutStaged(all, "app\u0000users")).toEqual({})
  })

  test("the documents they are on are found by their ids", () => {
    expect(idsFilter([ID])).toBe(`{ "_id": ${ID} }`)
    expect(idsFilter([ID, '"second"'])).toBe(`{ "_id": { "$in": [${ID}, "second"] } }`)
  })
})
