import { describe, expect, test } from "bun:test"
import { choiceStep } from "./choice-card"

// A single-choice grid's keys, held without a document. The grid moving the
// pick and the focus is held in tests/browser/deploy-alerts.spec.ts.

const key = (name, modifiers = {}) => ({
  key: name,
  altKey: false,
  ctrlKey: false,
  metaKey: false,
  shiftKey: false,
  ...modifiers,
})

describe("choiceStep", () => {
  test("a bare arrow moves the pick one card, and Home and End to either end", () => {
    expect(choiceStep(key("ArrowRight"))).toBe(1)
    expect(choiceStep(key("ArrowDown"))).toBe(1)
    expect(choiceStep(key("ArrowLeft"))).toBe(-1)
    expect(choiceStep(key("ArrowUp"))).toBe(-1)
    expect(choiceStep(key("Home"))).toBe("first")
    expect(choiceStep(key("End"))).toBe("last")
  })

  test("a key that is not the grid's is left alone", () => {
    expect(choiceStep(key("Enter"))).toBeUndefined()
    expect(choiceStep(key("a"))).toBeUndefined()
  })

  test("a modified key is the browser's or the page's, never a new pick", () => {
    // Alt+← is Back, ⌘+← too on a Mac; Ctrl+Home is the page's top.
    expect(choiceStep(key("ArrowLeft", { altKey: true }))).toBeUndefined()
    expect(choiceStep(key("ArrowRight", { altKey: true }))).toBeUndefined()
    expect(choiceStep(key("ArrowLeft", { metaKey: true }))).toBeUndefined()
    expect(choiceStep(key("Home", { ctrlKey: true }))).toBeUndefined()
    expect(choiceStep(key("ArrowDown", { shiftKey: true }))).toBeUndefined()
  })
})
