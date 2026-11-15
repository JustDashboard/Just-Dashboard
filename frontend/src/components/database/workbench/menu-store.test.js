import { expect, test } from "bun:test"
import { menuStore } from "./menu-store"

const Glyph = () => null
const Other = () => null
const verb = (key, label, extra = {}) => ({ key, label, icon: Glyph, run: () => {}, ...extra })

test("the head hears of a change in what it draws, and of nothing else", () => {
  const store = menuStore()
  let told = 0
  store.subscribe(() => told++)

  store.set("table", [verb("reload", "Read the rows again")])
  expect(told).toBe(1)
  const drawn = store.verbs()

  // The same verbs on another render: new closures, nothing new to draw.
  store.set("table", [verb("reload", "Read the rows again")])
  expect(told).toBe(1)
  expect(store.verbs()).toBe(drawn)

  // A word, a verb that may not be pressed, another glyph: each is drawn differently.
  store.set("table", [verb("reload", "Read the documents again")])
  store.set("table", [verb("reload", "Read the documents again", { disabled: true })])
  store.set("table", [verb("reload", "Read the documents again", { disabled: true, icon: Other })])
  expect(told).toBe(4)
})

test("a verb pressed runs what its part handed over last", () => {
  const store = menuStore()
  const ran = []
  store.set("table", [verb("row", "Show the row", { run: () => ran.push("first") })])
  const drawn = store.verbs()
  store.set("table", [verb("row", "Show the row", { run: () => ran.push("second") })])
  drawn[0].run()
  expect(ran).toEqual(["second"])
})

test("parts keep the order they arrived in, their keys apart, and leave with their verbs", () => {
  const store = menuStore()
  store.set("table", [verb("toggle", "Show the row")])
  store.set("page", [verb("toggle", "Hide the tables")])
  expect(store.verbs().map((entry) => entry.label)).toEqual(["Show the row", "Hide the tables"])
  expect(new Set(store.verbs().map((entry) => entry.key)).size).toBe(2)

  // Handing over again does not move a part to the end.
  store.set("table", [verb("toggle", "Hide the row")])
  expect(store.verbs().map((entry) => entry.label)).toEqual(["Hide the row", "Hide the tables"])

  store.drop("table")
  expect(store.verbs().map((entry) => entry.label)).toEqual(["Hide the tables"])
  // A part that was never there leaves without a word.
  let told = 0
  store.subscribe(() => told++)
  store.drop("table")
  expect(told).toBe(0)
})
