import { expect, test } from "bun:test"
import { firstReachable, menuOpener, openerOf } from "./use-focus-return"

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

/** An element as the walk reads it: what `closest('[role="menu"]')` finds, and an id. */
function element({ id, menu = null } = {}) {
  return { id, focus() {}, closest: () => menu, isConnected: true }
}
function menu(labelledBy) {
  return { getAttribute: (name) => (name === "aria-labelledby" ? labelledBy : null) }
}

function withDocument(elements, run) {
  const saved = globalThis.document
  globalThis.document = {
    body: { tag: "body" },
    getElementById: (id) => elements.find((el) => el.id === id) ?? null,
  }
  try {
    run()
  } finally {
    globalThis.document = saved
  }
}

test("a control outside every menu is its own opener", () => {
  const button = element({ id: "save" })
  withDocument([button], () => {
    expect(openerOf(button)).toBe(button)
    expect(menuOpener(button)).toBeNull()
  })
})

test("a menu row names the menu's button, and a submenu's row the button outside both", () => {
  const trigger = element({ id: "row-menu" })
  const submenuItem = element({ id: "move-to", menu: menu("row-menu") })
  const row = element({ id: "deep", menu: menu("move-to") })
  const shallow = element({ id: "rename", menu: menu("row-menu") })
  withDocument([trigger, submenuItem, row, shallow], () => {
    expect(openerOf(shallow)).toBe(trigger)
    expect(openerOf(row)).toBe(trigger)
  })
})

test("nothing opened it when only the document held the keyboard", () => {
  withDocument([], () => {
    expect(openerOf(null)).toBeNull()
    expect(openerOf(globalThis.document.body)).toBeNull()
  })
})

test("a menu whose button has gone leaves the row as the opener", () => {
  const row = element({ id: "orphan", menu: menu("gone") })
  withDocument([row], () => expect(openerOf(row)).toBe(row))
})
