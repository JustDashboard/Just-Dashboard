import { expect, test } from "bun:test"
import { hasReducedMotionListener, prefersReducedMotion } from "motion/react"
import {
  MOTION_PAUSED,
  MOTION_STORAGE_KEY,
  THEME_STORAGE_KEY,
  themeBootstrapScript,
} from "@/lib/themes"

/** The document the bootstrap script runs against, with a store that may refuse. */
function boot(stored, { refuses = false } = {}) {
  const store = new Map(Object.entries(stored))
  const root = {
    classes: new Set(),
    attributes: {},
    style: {},
    classList: { add: (name) => root.classes.add(name) },
    setAttribute: (name, value) => (root.attributes[name] = value),
  }
  const refuse = () => {
    throw new Error("storage is blocked")
  }
  const localStorage = refuses
    ? { getItem: refuse, removeItem: refuse }
    : { getItem: (key) => store.get(key) ?? null, removeItem: (key) => store.delete(key) }
  new Function("document", "localStorage", themeBootstrapScript())(
    { documentElement: root },
    localStorage,
  )
  return { root, store }
}

test("a browser that paused animations is paused before the first paint", () => {
  const { root } = boot({ [MOTION_STORAGE_KEY]: MOTION_PAUSED })
  expect(root.attributes["data-motion"]).toBe("paused")
  expect(root.classes.has("dark")).toBe(true)
  expect(root.style.colorScheme).toBe("dark")
})

test("a browser that did not is left moving, and loses the old theme key", () => {
  const { root, store } = boot({ [THEME_STORAGE_KEY]: "light" })
  expect(root.attributes["data-motion"]).toBeUndefined()
  expect(store.has(THEME_STORAGE_KEY)).toBe(false)
  expect(boot({ [MOTION_STORAGE_KEY]: "on" }).root.attributes["data-motion"]).toBeUndefined()
})

test("a browser that refuses storage is still dark, and not paused", () => {
  const { root } = boot({}, { refuses: true })
  expect(root.classes.has("dark")).toBe(true)
  expect(root.attributes["data-motion"]).toBeUndefined()
})

// `motion-pause.ts` writes the library's one module-level answer, which is not
// in its documentation. An upgrade that renames either fails here rather than
// leaving a paused page with the library still moving.
test("the motion library still keeps its answer where the switch writes it", () => {
  expect(prefersReducedMotion).toHaveProperty("current")
  expect(hasReducedMotionListener).toHaveProperty("current")
})
