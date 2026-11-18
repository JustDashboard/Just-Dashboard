import { describe, expect, test } from "bun:test"
import { readFileSync } from "node:fs"
import { join } from "node:path"
import { createElement } from "react"
import { renderToString } from "react-dom/server"
import {
  BREAKPOINTS,
  breakpointQuery,
  mediaQueryStore,
  useBelow,
  useBreakpoint,
} from "./breakpoints"

const css = readFileSync(join(import.meta.dir, "..", "app", "globals.css"), "utf8")

describe("BREAKPOINTS", () => {
  test("are the stylesheet's own @theme breakpoints, in pixels", () => {
    const declared = Object.fromEntries(
      [...css.matchAll(/--breakpoint-([a-z0-9]+):\s*([0-9.]+)rem;/g)].map((m) => [
        m[1],
        Number(m[2]) * 16,
      ]),
    )
    expect(declared).toEqual({ ...BREAKPOINTS })
  })

  test("a query is written in rem, as Tailwind writes sm: and max-sm:", () => {
    expect(breakpointQuery("lg")).toBe("(min-width: 64rem)")
    expect(breakpointQuery("sm", "below")).toBe("(width < 40rem)")
    expect(breakpointQuery("2xl")).toBe("(min-width: 96rem)")
  })
})

describe("following a media query", () => {
  function fakeWindow(matches) {
    const listeners = new Set()
    const asked = []
    globalThis.window = {
      matchMedia(query) {
        asked.push(query)
        return {
          get matches() {
            return matches()
          },
          addEventListener: (_, fn) => listeners.add(fn),
          removeEventListener: (_, fn) => listeners.delete(fn),
        }
      },
    }
    return { listeners, asked }
  }

  test("it answers what the browser answers, and stops listening when told", () => {
    let wide = false
    const { listeners, asked } = fakeWindow(() => wide)
    try {
      const store = mediaQueryStore(breakpointQuery("lg"))
      let changes = 0
      const stop = store.subscribe(() => changes++)
      expect(listeners.size).toBe(1)
      expect(store.getSnapshot()).toBe(false)
      wide = true
      for (const fn of listeners) fn()
      expect(changes).toBe(1)
      expect(store.getSnapshot()).toBe(true)
      stop()
      expect(listeners.size).toBe(0)
      expect(new Set(asked)).toEqual(new Set(["(min-width: 64rem)"]))
    } finally {
      delete globalThis.window
    }
  })

  test("the server, which has no viewport, answers false", () => {
    expect(mediaQueryStore("(min-width: 64rem)").getServerSnapshot()).toBe(false)
  })

  test("useBreakpoint('lg') and useBelow('sm') render on the server with that answer", () => {
    const Probe = () => createElement("i", null, `${useBreakpoint("lg")} ${useBelow("sm")}`)
    expect(renderToString(createElement(Probe))).toBe("<i>false false</i>")
  })
})
