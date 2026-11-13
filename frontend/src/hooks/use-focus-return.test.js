import { expect, test } from "bun:test"
import { firstReachable } from "./use-focus-return"

// Which element gets the keyboard back is the part with no document in it; the
// dialogs themselves are held in tests/browser/api-keys.spec.ts.

test("the opener gets the keyboard back while it is still on the page", () => {
  const opener = { isConnected: true }
  const menu = { isConnected: true }
  expect(firstReachable([opener, menu])).toBe(opener)
})

test("a menu item that left with its menu gives way to the menu's button", () => {
  const item = { isConnected: false }
  const button = { isConnected: true }
  expect(firstReachable([item, button])).toBe(button)
})

test("nothing is chosen when both are gone, or the one left cannot be focused", () => {
  expect(firstReachable([{ isConnected: false }, null])).toBeUndefined()
  expect(firstReachable([null, undefined])).toBeUndefined()
  expect(firstReachable([{ isConnected: true, disabled: true }])).toBeUndefined()
})
