import { afterEach, describe, expect, test } from "bun:test"
import { readdirSync, readFileSync } from "node:fs"
import { join, relative } from "node:path"
import { motionMs, reducedMotion, scrollBehavior, scrollIntoViewRespectingMotion } from "./motion"

const real = { window: globalThis.window, document: globalThis.document }

afterEach(() => {
  globalThis.window = real.window
  globalThis.document = real.document
})

/** A browser as the helper reads it: the system's setting and the product's pause. */
function browser({ reduce = false, paused = false } = {}) {
  const asked = []
  globalThis.document = { documentElement: { dataset: paused ? { motion: "paused" } : {} } }
  globalThis.window = {
    matchMedia(query) {
      asked.push(query)
      return { matches: reduce }
    },
  }
  return asked
}

/** An element that records what was asked of it. */
function element({ tabindex } = {}) {
  const calls = []
  const attributes = new Map(tabindex === undefined ? [] : [["tabindex", tabindex]])
  return {
    calls,
    attributes,
    hasAttribute: (name) => attributes.has(name),
    setAttribute: (name, value) => attributes.set(name, value),
    focus: (options) => calls.push(["focus", options]),
    scrollIntoView: (options) => calls.push(["scroll", options]),
  }
}

describe("reducedMotion", () => {
  test("is the system's setting", () => {
    const asked = browser({ reduce: true })
    expect(reducedMotion()).toBe(true)
    expect(asked).toEqual(["(prefers-reduced-motion: reduce)"])
    browser()
    expect(reducedMotion()).toBe(false)
  })

  test("is also the product's pause, which the system knows nothing of", () => {
    browser({ paused: true })
    expect(reducedMotion()).toBe(true)
  })

  test("is false where there is no browser to ask", () => {
    globalThis.window = undefined
    expect(reducedMotion()).toBe(false)
  })
})

describe("motionMs", () => {
  test("keeps a duration, and drops it when motion is reduced", () => {
    browser()
    expect(motionMs(450)).toBe(450)
    browser({ reduce: true })
    expect(motionMs(450)).toBe(0)
    browser({ paused: true })
    expect(motionMs(150)).toBe(0)
  })
})

describe("scrollIntoViewRespectingMotion", () => {
  test("glides to the start of the target", () => {
    browser()
    const el = element()
    scrollIntoViewRespectingMotion(el)
    expect(el.calls).toEqual([
      ["scroll", { block: "start", inline: undefined, behavior: "smooth" }],
    ])
  })

  test("jumps when the system asks for less motion", () => {
    browser({ reduce: true })
    const el = element()
    scrollIntoViewRespectingMotion(el, { block: "nearest" })
    expect(el.calls).toEqual([
      ["scroll", { block: "nearest", inline: undefined, behavior: "instant" }],
    ])
  })

  test("jumps when the product's animations are paused", () => {
    browser({ paused: true })
    const el = element()
    scrollIntoViewRespectingMotion(el)
    expect(el.calls[0][1].behavior).toBe("instant")
  })

  test("takes the keyboard to the target before it scrolls, without a stop of its own", () => {
    browser()
    const el = element()
    scrollIntoViewRespectingMotion(el, { focus: true })
    expect(el.attributes.get("tabindex")).toBe("-1")
    expect(el.calls.map((call) => call[0])).toEqual(["focus", "scroll"])
    // The focus must not scroll by itself: that jump is instant.
    expect(el.calls[0][1]).toEqual({ preventScroll: true })
  })

  test("leaves a target's own tabindex as it is", () => {
    browser()
    const el = element({ tabindex: "0" })
    scrollIntoViewRespectingMotion(el, { focus: true })
    expect(el.attributes.get("tabindex")).toBe("0")
  })

  test("does nothing for a target that is not on the page", () => {
    browser()
    expect(() => scrollIntoViewRespectingMotion(null)).not.toThrow()
    expect(() => scrollIntoViewRespectingMotion(undefined, { focus: true })).not.toThrow()
  })
})

describe("scrollBehavior", () => {
  test("glides, and goes at once when either request is made", () => {
    browser()
    expect(scrollBehavior()).toBe("smooth")
    browser({ reduce: true })
    expect(scrollBehavior()).toBe("instant")
    browser({ paused: true })
    expect(scrollBehavior()).toBe("instant")
  })
})

/** Every source file under `src`, tests and this module aside. */
function sources(dir = join(import.meta.dir, "..")) {
  return readdirSync(dir, { withFileTypes: true }).flatMap((entry) => {
    const path = join(dir, entry.name)
    if (entry.isDirectory()) return sources(path)
    if (!/\.(ts|tsx)$/.test(entry.name) || /\.test\./.test(entry.name)) return []
    return [path]
  })
}

describe("script that scrolls", () => {
  const src = join(import.meta.dir, "..")
  const own = new Set(["lib/motion.ts", "components/shell/motion-pause.ts"])

  // A glide written by hand overrides the root rule by specification, so a
  // reveal on a paused page still glided (A11Y-17): Certificates' Renewal
  // runs and a project's failure reading did, among fourteen others.
  test("asks for a glide only through this module", () => {
    const written = sources()
      .filter((path) => !own.has(relative(src, path)))
      .filter((path) => /behavior:[^,}]*"smooth"/.test(readFileSync(path, "utf8")))
      .map((path) => relative(src, path))
    expect(written).toEqual([])
  })

  // The media query alone is the system's half of the answer: a page paused
  // from the account menu still moved.
  test("reads reduced motion through this module, which also hears the pause", () => {
    const asked = sources()
      .filter((path) => !own.has(relative(src, path)))
      .filter((path) => /matchMedia\(\s*"\(prefers-reduced-motion/.test(readFileSync(path, "utf8")))
      .map((path) => relative(src, path))
    expect(asked).toEqual([])
  })
})
