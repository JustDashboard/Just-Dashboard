import { describe, expect, test } from "bun:test"
import { EMPTY_FILTER } from "@/lib/log-filter"
import { lensFor } from "@/lib/log-lenses"
import {
  askOf,
  emptiedFile,
  eventColumnFor,
  readingFor,
  readingShown,
  sameQuestion,
  unaskedReadings,
  withAsk,
} from "./logs-model"

const tile = (reading, figure) => ({ reading, figure })

describe("a reading on the quick view that asks its question", () => {
  const postgres = lensFor("postgres")
  const tiles = postgres.readings.map((reading) => tile(reading, { value: 3 }))

  test("a view and a reading asking the same fields and levels are one question", () => {
    expect(sameQuestion({ levels: ["error", "critical"] }, { levels: ["critical", "error"] })).toBe(
      true,
    )
    expect(
      sameQuestion({ fields: { event: ["slow"] } }, { fields: { event: ["slow"] }, levels: [] }),
    ).toBe(true)
    expect(sameQuestion({ fields: { event: ["slow"] } }, { fields: { event: ["deadlock"] } })).toBe(
      false,
    )
    expect(sameQuestion({ levels: ["error"] }, { levels: ["error", "critical"] })).toBe(false)
  })

  test("Errors and Slow carry their readings; Restarts, which no view asks, is a chip of its own", () => {
    const view = (id) => postgres.views.find((v) => v.id === id)
    expect(readingFor(view("errors"), tiles)?.reading.id).toBe("errors")
    expect(readingFor(view("slow"), tiles)?.reading.id).toBe("slow")
    expect(readingFor(view("maintenance"), tiles)).toBeUndefined()
    const unasked = unaskedReadings(postgres.views, tiles).map((t) => t.reading.id)
    expect(unasked).toContain("restarts")
    expect(unasked).not.toContain("errors")
    expect(unasked).not.toContain("slow")
  })

  test("a view that also searches for words asks what no reading does", () => {
    const worded = { id: "w", label: "W", levels: ["error", "critical"], q: "timeout" }
    expect(readingFor(worded, tiles)).toBeUndefined()
  })
})

describe("a reading's figure as it is drawn", () => {
  test("a count, capped when the distinct count stopped being exact", () => {
    const reading = { id: "a", label: "Attackers", hint: "", distinct: "client" }
    expect(readingShown(reading, { value: 1200, capped: true }, "24h").text).toBe(
      `${(1200).toLocaleString()}+`,
    )
    expect(readingShown(reading, undefined, "24h")).toEqual({ value: 0, text: "—" })
  })

  test("a per-minute reading is its rate over the window", () => {
    const reading = { id: "rate", label: "Requests/min", hint: "", figure: "per_minute" }
    const shown = readingShown(reading, { value: 90 }, "1h")
    expect(shown.value).toBe(1.5)
    expect(shown.text).toBe((1.5).toLocaleString())
  })
})

describe("what a page's narrowing leaves of the filter", () => {
  const filter = { ...EMPTY_FILTER, q: "timeout", levels: ["warn"], fields: { user: ["bob"] } }

  test("each part replaces the filter's own only when given", () => {
    expect(withAsk(filter, { fields: { host: ["shop.test"] } })).toEqual({
      ...filter,
      fields: { host: ["shop.test"] },
    })
    expect(withAsk(filter, { fields: {}, levels: [], q: "" })).toEqual({
      ...filter,
      fields: {},
      levels: [],
      q: "",
    })
  })

  test("a window that names nothing is no narrowing at all", () => {
    expect(askOf({ since: "a", until: "b", label: "Around" })).toBeUndefined()
    expect(askOf(undefined)).toBeUndefined()
    expect(askOf({ since: "a", until: "b", levels: [] })).toEqual({
      fields: undefined,
      levels: [],
      q: undefined,
    })
  })
})

test("a file logrotate emptied is one whose history is all in its rotated set", () => {
  expect(emptiedFile({ path: "/var/log/postgresql/main.log", archives: 2 })).toBe(true)
  expect(emptiedFile({ path: "/var/log/postgresql/main.log", size: 0, archives: 1 })).toBe(true)
  expect(emptiedFile({ path: "/var/log/postgresql/main.log", size: 12, archives: 1 })).toBe(false)
  expect(emptiedFile({ path: "/var/log/postgresql/main.log" })).toBe(false)
  // A container's output has no file and no rotated set.
  expect(emptiedFile({ archives: 3 })).toBe(false)
})

describe("the event column's width", () => {
  const line = (event, extra = {}) => ({ text: event, event, ...extra })

  test("as wide as the longest event word on screen, so none is cut", () => {
    expect(eventColumnFor([line("upstream_refused"), line("ban")], "nginx-error")).toBe(
      "upstream refused".length,
    )
    expect(eventColumnFor([line("restart_scheduled")], "systemd")).toBe("restart scheduled".length)
  })

  test("never narrower than a level word needs, never wider than the bound", () => {
    expect(eventColumnFor([line("ban")], "fail2ban")).toBe(6)
    expect(eventColumnFor([line("too_many_connections")], "mysql")).toBe(17)
  })

  test("none while no line draws one: a continuation, an unmarked event, no event at all", () => {
    expect(eventColumnFor([], "postgres")).toBe(0)
    expect(eventColumnFor([{ text: "plain" }], "postgres")).toBe(0)
    expect(eventColumnFor([line("deadlock", { cont: true })], "postgres")).toBe(0)
    // A connection is too common to mark, and draws its level instead.
    expect(eventColumnFor([line("connection")], "postgres")).toBe(0)
  })
})
