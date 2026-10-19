import { expect, test } from "bun:test"
import { remember, unsent } from "./board-live"

test("a peer is sent each element once per version, and never what it was sent", () => {
  const sent = new Map()
  const first = [
    { id: "a", version: 1 },
    { id: "b", version: 3 },
  ]
  expect(unsent(first, sent)).toEqual(first)
  remember(sent, first)
  expect(unsent(first, sent)).toEqual([])

  // b arrived from another peer at version 4: drawing it here sends nothing.
  remember(sent, [{ id: "b", version: 4 }])
  const next = [
    { id: "a", version: 2 },
    { id: "b", version: 4 },
    { id: "c", version: 1 },
  ]
  expect(unsent(next, sent).map((element) => element.id)).toEqual(["a", "c"])
})
