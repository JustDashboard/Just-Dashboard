import { describe, expect, test } from "bun:test"
import {
  drainFigure,
  drainLine,
  drainState,
  drainStateWord,
  drainTakes,
  drainsCount,
} from "./log-drain-state"
import { shortAge } from "./short-age"

const now = Date.parse("2026-11-14T12:00:00Z")
const ago = (seconds) => new Date(now - seconds * 1000).toISOString()
const status = (fields = {}) => ({ shipped: 0, dropped: 0, batches: 0, queued: 0, ...fields })

const shipping = {
  enabled: true,
  status: status({ shipped: 1240, batches: 31, lastSuccessAt: ago(5) }),
}
const failing = {
  enabled: true,
  status: status({
    shipped: 1240,
    lastSuccessAt: ago(600),
    lastErrorAt: ago(190),
    lastError: "the destination refused the credentials (401)",
    queued: 86,
  }),
}
const paused = { enabled: false, status: failing.status }
const fresh = { enabled: true, status: status() }

describe("drainState", () => {
  test("paused says so before anything its last delivery says", () => {
    expect(drainState(paused)).toBe("paused")
  })
  test("a failure newer than the last success is failing, an older one is not", () => {
    expect(drainState(failing)).toBe("failing")
    expect(
      drainState({
        enabled: true,
        status: status({ lastSuccessAt: ago(5), lastErrorAt: ago(600), lastError: "refused" }),
      }),
    ).toBe("shipping")
  })
  test("a drain that has sent nothing is waiting", () => {
    expect(drainState(fresh)).toBe("waiting")
  })
})

describe("shortAge", () => {
  test("one unit, the largest that fits", () => {
    expect(shortAge(ago(12), now)).toBe("12s")
    expect(shortAge(ago(190), now)).toBe("3m")
    expect(shortAge(ago(2 * 3600 + 59), now)).toBe("2h")
    expect(shortAge(ago(47 * 3600), now)).toBe("47h")
    expect(shortAge(ago(5 * 86400), now)).toBe("5d")
  })
  test("nothing for a time that is not one", () => {
    expect(shortAge(undefined, now)).toBe("")
    expect(shortAge("never", now)).toBe("")
  })
})

describe("drainStateWord", () => {
  test("a dot and a word for each state", () => {
    expect(drainStateWord(shipping, now)).toEqual({ tone: "running", label: "Shipping" })
    expect(drainStateWord(failing, now)).toEqual({ tone: "warning", label: "Failing 3m" })
    expect(drainStateWord(paused, now)).toEqual({ tone: "stopped", label: "Paused" })
    expect(drainStateWord(fresh, now)).toEqual({ tone: "unknown", label: "Nothing sent" })
  })
})

describe("drainFigure", () => {
  test("what waits while the destination refuses comes first", () => {
    expect(drainFigure(failing)).toEqual({ text: "86 waiting", warning: true })
  })
  test("then what was dropped, then what was shipped", () => {
    expect(
      drainFigure({
        enabled: true,
        status: status({ shipped: 9, dropped: 3, lastSuccessAt: ago(5) }),
      }),
    ).toEqual({ text: "3 dropped", warning: true })
    expect(drainFigure(shipping)).toEqual({ text: "1,240 shipped", warning: false })
    // A paused drain is not refusing anything: its queue is not the figure.
    expect(drainFigure(paused)).toEqual({ text: "1,240 shipped", warning: false })
  })
})

describe("drainLine", () => {
  test("a pulse only while the last delivery arrived", () => {
    expect(drainLine(shipping)).toMatchObject({ state: "up", moving: true })
    expect(drainLine(fresh)).toEqual({ state: "up" })
    expect(drainLine(failing)).toEqual({ state: "warning" })
    expect(drainLine(paused)).toEqual({ state: "idle" })
  })
})

describe("drainsCount", () => {
  test("the count, then what needs a look", () => {
    expect(drainsCount([shipping])).toBe("1 drain")
    expect(drainsCount([shipping, failing])).toBe("2 drains · 1 failing")
    expect(drainsCount([shipping, failing, paused, fresh])).toBe("4 drains · 1 failing · 1 paused")
  })
})

describe("drainTakes", () => {
  test("every project's, or the ones it names", () => {
    expect(drainTakes({ projects: [] }, 7)).toBe(true)
    expect(drainTakes({ projects: [7] }, 7)).toBe(true)
    expect(drainTakes({ projects: [8] }, 7)).toBe(false)
  })
})
